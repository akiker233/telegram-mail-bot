package telegram

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/oauth2"

	"telegram-mail-bot/internal/crypto"
	"telegram-mail-bot/internal/db"
	"telegram-mail-bot/internal/oauth"
	"telegram-mail-bot/internal/smtp"
	"telegram-mail-bot/internal/update"
)

// htmlTagRe 用于 HTML 发送失败时的降级：剥除所有标签得到纯文本重发一次。
var htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)

const helpText = `👋 我可以把邮箱新邮件转发到 Telegram，也能用已添加的账号发信。

可用命令：
📥 /addaccount - 添加一个邮箱账号（IMAP，默认）
📥 /addaccount pop3 - 用 POP3 协议添加账号（无实时推送，定时轮询）
📋 /listaccounts - 列出已添加的账号
🗑️ /delaccount <id> - 删除一个账号
📤 /send - 用已添加的账号发一封邮件
📊 /status - 查看账号状态
🔑 /reauthorize - 重新授权 OAuth 账号（授权失效时使用，可选择账号）
ℹ️ /version - 查看版本信息与更新
🔄 /update - 检查并更新到最新版本
🚫 /cancel - 取消当前正在进行的操作`

// AccountStarter 抽象了账号添加成功后启动监听的动作，避免 telegram 包依赖 manager 包。
type AccountStarter interface {
	Start(ctx context.Context, account *db.Account) error
	Stop(accountID int64)
	IsRunning(accountID int64) bool
}

// oauthContext 在 client 非空时把全局代理客户端注入 context，供 OAuth2 流程使用。
func oauthContext(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	if !b.allowedUsers[userID] {
		return
	}

	if msg.IsCommand() {
		b.handleCommand(chatID, userID, msg.Command(), msg.CommandArguments())
		return
	}

	if sess := b.sessions.Get(userID); sess != nil {
		b.handleSessionReply(chatID, userID, sess, msg.Text)
		return
	}

	if sendSess := b.sendSessions.Get(userID); sendSess != nil {
		b.handleSendSessionReply(chatID, userID, sendSess, msg.Text)
		return
	}
}

func (b *Bot) handleCommand(chatID, userID int64, command, args string) {
	// /addaccount 和 /send 是两套独立的会话状态机，同一用户同一时刻只应有一种在进行，
	// 避免两边的输入互相串到对方的 Advance 里产生难以理解的行为。
	if command != "cancel" && (b.sessions.Get(userID) != nil || b.sendSessions.Get(userID) != nil) {
		b.reply(chatID, "⚠️ 请先完成或使用 /cancel 取消当前操作")
		return
	}

	switch command {
	case "start", "help":
		b.reply(chatID, helpText)
	case "addaccount":
		protocol := strings.ToLower(strings.TrimSpace(args))
		if protocol != "" && protocol != "imap" && protocol != "pop3" {
			b.reply(chatID, "用法: /addaccount 或 /addaccount pop3")
			return
		}
		if protocol == "" {
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("IMAP（推荐）", "addproto:imap"),
					tgbotapi.NewInlineKeyboardButtonData("POP3", "addproto:pop3"),
				),
			)
			b.replyWithKeyboard(chatID, "请选择协议：", keyboard)
			return
		}
		b.startAddAccountSession(chatID, userID, protocol)
	case "send":
		b.handleSendStart(chatID, userID)
	case "cancel":
		b.sessions.Clear(userID)
		b.sendSessions.Clear(userID)
		b.reply(chatID, "🚫 已取消当前操作")
	case "listaccounts":
		b.handleListAccounts(chatID, userID)
	case "delaccount":
		b.handleDelAccount(chatID, userID, args)
	case "status":
		b.handleAccountStatus(chatID, userID)
	case "reauthorize":
		b.handleReauthorize(chatID, userID, args)
	case "version":
		b.handleVersion(chatID)
	case "update":
		b.handleUpdate(chatID)
	default:
		b.reply(chatID, helpText)
	}
}

// startAddAccountSession 用给定协议开始 /addaccount 会话，供文字参数快捷方式和协议选择按钮共用。
func (b *Bot) startAddAccountSession(chatID, userID int64, protocol string) {
	availableProviders := make(map[string]bool, len(b.oauthConfigs))
	for provider := range b.oauthConfigs {
		availableProviders[provider] = true
	}
	b.sessions.Start(userID, availableProviders, protocol)
	b.reply(chatID, "请输入要添加的邮箱地址")
}

func (b *Bot) handleSessionReply(chatID, userID int64, sess *Session, text string) {
	reply, finished, cancelled := sess.Advance(text)

	if cancelled {
		b.sessions.Clear(userID)
		b.reply(chatID, reply)
		return
	}

	if finished {
		b.sessions.Clear(userID)
		if err := b.saveAccount(userID, sess.Draft); err != nil {
			b.reply(chatID, "❌ 保存账号失败: "+err.Error())
			return
		}
		b.reply(chatID, reply)
		return
	}

	if sess.Step == StepOAuthPending {
		b.startOAuthFlow(chatID, userID, sess)
		return
	}

	// 非终态步数变化后持久化当前会话状态。
	b.sessions.Persist(userID)

	if kb := keyboardForStep(sess.Step); kb != nil {
		b.replyWithKeyboard(chatID, reply, *kb)
		return
	}
	b.reply(chatID, reply)
}

// keyboardForStep 返回 Advance() 推进到某个 Step 后的提示语应带的按钮，
// 不需要按钮的 Step 返回 nil。
func keyboardForStep(step Step) *tgbotapi.InlineKeyboardMarkup {
	switch step {
	case StepAuthMethod:
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("OAuth", "authmethod:oauth"),
				tgbotapi.NewInlineKeyboardButtonData("密码/授权码", "authmethod:password"),
			),
		)
		return &kb
	case StepSMTPOptional:
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("是", "smtpopt:yes"),
				tgbotapi.NewInlineKeyboardButtonData("否", "smtpopt:no"),
			),
		)
		return &kb
	case StepConfirm:
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ 确认", "addconfirm:yes"),
				tgbotapi.NewInlineKeyboardButtonData("🚫 取消", "addconfirm:no"),
			),
		)
		return &kb
	}
	return nil
}

// startOAuthFlow 发起 device flow：立刻回复用户授权链接和一次性代码，然后在后台
// goroutine 里轮询 token 端点。轮询是异步的，不会阻塞 Telegram 长轮询主循环。
func (b *Bot) startOAuthFlow(chatID, userID int64, sess *Session) {
	cfg, ok := b.oauthConfigs[sess.Draft.OAuthProvider]
	if !ok {
		b.sessions.Clear(userID)
		b.reply(chatID, "❌ 该 OAuth 登录方式未配置，请使用密码/授权码方式添加账号")
		return
	}

	flowCtx := oauthContext(b.ctx, b.httpClient)
	resp, err := oauth.StartDeviceFlow(flowCtx, cfg)
	if err != nil {
		b.sessions.Clear(userID)
		b.reply(chatID, "❌ 发起 OAuth 授权失败: "+err.Error())
		return
	}

	b.reply(chatID, fmt.Sprintf("🔐 请在浏览器打开 %s 并输入代码 %s 完成授权（最多等待8分钟）", resp.VerificationURI, resp.UserCode))

	go func() {
		token, err := oauth.PollToken(oauthContext(b.ctx, b.httpClient), cfg, resp)

		// 轮询期间用户可能已经 /cancel 或重新 /addaccount，此时 sess 已不是当前会话，
		// 结果应该被丢弃，不能写进一个不再代表当前状态的会话里。
		if b.sessions.Get(userID) != sess {
			return
		}

		if err != nil {
			b.sessions.Clear(userID)
			b.reply(chatID, "❌ OAuth 授权失败或超时: "+err.Error())
			return
		}

		reply := sess.CompleteOAuth(token.AccessToken, token.RefreshToken, token.Expiry)
		kb := keyboardForStep(StepConfirm)
		b.replyWithKeyboard(chatID, reply, *kb)
	}()
}

// handleReauthorize 重新授权一个已有的 OAuth 账号。
// 适用场景：OAuth refresh token 过期/被撤销后，用户无需删除账号重新添加。
// 不带参数时展示该用户所有 OAuth 账号供选择（类似 /delaccount 的按钮列表）。
func (b *Bot) handleReauthorize(chatID, userID int64, args string) {
	args = strings.TrimSpace(args)
	if args == "" {
		text, keyboard, err := b.renderOAuthAccountsList(userID)
		if err != nil {
			b.reply(chatID, "❌ 查询账号失败: "+err.Error())
			return
		}
		if len(keyboard.InlineKeyboard) == 0 {
			b.reply(chatID, text)
			return
		}
		b.replyWithKeyboard(chatID, text, keyboard)
		return
	}

	accountID, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		b.reply(chatID, "用法: /reauthorize 或 /reauthorize <account_id>")
		return
	}
	b.startReauthorize(chatID, userID, accountID)
}

// renderOAuthAccountsList 渲染该用户所有 OAuth 账号列表和每个账号的重新授权按钮，
// 供 /reauthorize 不带参数时使用，与 renderAccountsList 结构类似。
func (b *Bot) renderOAuthAccountsList(userID int64) (string, tgbotapi.InlineKeyboardMarkup, error) {
	accounts, err := db.ListAccountsByUser(b.db, userID)
	if err != nil {
		return "", tgbotapi.InlineKeyboardMarkup{}, err
	}

	var sb strings.Builder
	sb.WriteString("选择要重新授权的账号：\n")
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, a := range accounts {
		if a.AuthType != db.AuthTypeOAuth {
			continue
		}
		fmt.Fprintf(&sb, "#%d %s\n", a.ID, a.Email)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🔑 重新授权 #%d", a.ID), fmt.Sprintf("reauth:%d", a.ID)),
		))
	}
	if len(rows) == 0 {
		return "没有使用 OAuth 认证的账号，无需重新授权", tgbotapi.InlineKeyboardMarkup{}, nil
	}
	return sb.String(), tgbotapi.NewInlineKeyboardMarkup(rows...), nil
}

// startReauthorize 发起指定账号的 OAuth device flow 重新授权，校验账号归属和认证方式后执行。
func (b *Bot) startReauthorize(chatID, userID, accountID int64) {
	account, err := db.GetAccountByID(b.db, accountID)
	if err != nil {
		b.reply(chatID, "❌ 未找到该账号")
		return
	}
	if account.TelegramUserID != userID {
		b.reply(chatID, "❌ 该账号不属于你")
		return
	}
	if account.AuthType != db.AuthTypeOAuth {
		b.reply(chatID, "❌ 该账号使用密码认证，无需重新授权")
		return
	}

	cfg, ok := b.oauthConfigs[account.OAuthProvider]
	if !ok {
		b.reply(chatID, "❌ 该 OAuth 登录方式未配置")
		return
	}

	flowCtx := oauthContext(b.ctx, b.httpClient)
	resp, err := oauth.StartDeviceFlow(flowCtx, cfg)
	if err != nil {
		b.reply(chatID, "❌ 发起 OAuth 授权失败: "+err.Error())
		return
	}

	b.reply(chatID, fmt.Sprintf("🔐 请在浏览器打开 %s 并输入代码 %s 完成授权（最多等待8分钟）", resp.VerificationURI, resp.UserCode))

	go func() {
		token, err := oauth.PollToken(oauthContext(b.ctx, b.httpClient), cfg, resp)
		if err != nil {
			b.reply(chatID, "❌ OAuth 重新授权失败或超时: "+err.Error())
			return
		}

		// 加密新 token 并更新数据库
		encryptedAccess, err := crypto.Encrypt(b.encryptionKey, token.AccessToken)
		if err != nil {
			b.reply(chatID, "❌ 加密 token 失败: "+err.Error())
			return
		}
		encryptedRefresh, err := crypto.Encrypt(b.encryptionKey, token.RefreshToken)
		if err != nil {
			b.reply(chatID, "❌ 加密 token 失败: "+err.Error())
			return
		}

		account.OAuthAccessToken = encryptedAccess
		account.OAuthRefreshToken = encryptedRefresh
		account.OAuthTokenExpiry = token.Expiry
		if err := db.UpdateAccountOAuthTokens(b.db, account); err != nil {
			b.reply(chatID, "❌ 更新 token 失败: "+err.Error())
			return
		}

		// 重启监听，让新 token 立即生效。
		b.manager.Stop(accountID)
		if err := b.manager.Start(b.ctx, account); err != nil {
			b.reply(chatID, "✅ 授权已更新，但启动监听失败: "+err.Error())
			return
		}

		b.reply(chatID, fmt.Sprintf("✅ 账号 %s 已重新授权，监听已恢复", account.Label))
	}()
}

func (b *Bot) saveAccount(userID int64, draft Draft) error {
	protocol := draft.Protocol
	if protocol == "" {
		protocol = "imap"
	}
	account := &db.Account{
		TelegramUserID: userID,
		Label:          draft.Email,
		Email:          draft.Email,
		IMAPHost:       draft.Host,
		IMAPPort:       draft.Port,
		IMAPUsername:   draft.Email,
		SMTPHost:       draft.SMTPHost,
		SMTPPort:       draft.SMTPPort,
		Protocol:       protocol,
	}

	if draft.AuthType == db.AuthTypeOAuth {
		encryptedAccess, err := crypto.Encrypt(b.encryptionKey, draft.OAuthAccessToken)
		if err != nil {
			return fmt.Errorf("加密 access token 失败: %w", err)
		}
		encryptedRefresh, err := crypto.Encrypt(b.encryptionKey, draft.OAuthRefreshToken)
		if err != nil {
			return fmt.Errorf("加密 refresh token 失败: %w", err)
		}
		account.AuthType = db.AuthTypeOAuth
		account.OAuthProvider = draft.OAuthProvider
		account.OAuthAccessToken = encryptedAccess
		account.OAuthRefreshToken = encryptedRefresh
		account.OAuthTokenExpiry = draft.OAuthTokenExpiry
	} else {
		encryptedPassword, err := crypto.Encrypt(b.encryptionKey, draft.Password)
		if err != nil {
			return fmt.Errorf("加密密码失败: %w", err)
		}
		account.AuthType = db.AuthTypePassword
		account.EncryptedPassword = encryptedPassword
	}

	id, err := db.InsertAccount(b.db, account)
	if err != nil {
		return fmt.Errorf("写入数据库失败: %w", err)
	}
	account.ID = id

	return b.manager.Start(b.ctx, account)
}

func (b *Bot) handleListAccounts(chatID, userID int64) {
	text, keyboard, err := b.renderAccountsList(userID)
	if err != nil {
		b.reply(chatID, "❌ 查询账号失败: "+err.Error())
		return
	}
	if len(keyboard.InlineKeyboard) == 0 {
		b.reply(chatID, text)
		return
	}
	b.replyWithKeyboard(chatID, text, keyboard)
}

// renderAccountsList 渲染账号列表文本和每个账号的删除按钮，供 /listaccounts 文字命令
// 和删除后重新渲染的回调共用。没有账号时 keyboard 为空（InlineKeyboard 为 nil）。
func (b *Bot) renderAccountsList(userID int64) (string, tgbotapi.InlineKeyboardMarkup, error) {
	accounts, err := db.ListAccountsByUser(b.db, userID)
	if err != nil {
		return "", tgbotapi.InlineKeyboardMarkup{}, err
	}
	if len(accounts) == 0 {
		return "还没有添加任何账号，使用 /addaccount 添加", tgbotapi.InlineKeyboardMarkup{}, nil
	}

	var sb strings.Builder
	sb.WriteString("已添加的账号：\n")
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, a := range accounts {
		status := "✅ 启用"
		if !a.Enabled {
			status = "⛔ 已停用"
		}
		protocol := strings.ToUpper(a.Protocol)
		fmt.Fprintf(&sb, "#%d %s [%s %s:%d] [%s]\n", a.ID, a.Email, protocol, a.IMAPHost, a.IMAPPort, status)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🗑️ 删除 #%d", a.ID), fmt.Sprintf("del:%d", a.ID)),
		))
	}
	return sb.String(), tgbotapi.NewInlineKeyboardMarkup(rows...), nil
}

// handleAccountStatus 展示每个账号的基础信息和同步进度（IMAP 用 LastUID，POP3 用已处理邮件数）。
func (b *Bot) handleAccountStatus(chatID, userID int64) {
	text, err := b.renderAccountStatus(userID)
	if err != nil {
		b.reply(chatID, "❌ 查询账号失败: "+err.Error())
		return
	}
	b.reply(chatID, text)
}

// renderAccountStatus 渲染 handleAccountStatus 的文本内容，供命令处理和单测共用。
func (b *Bot) renderAccountStatus(userID int64) (string, error) {
	accounts, err := db.ListAccountsByUser(b.db, userID)
	if err != nil {
		return "", err
	}
	if len(accounts) == 0 {
		return "还没有添加任何账号，使用 /addaccount 添加", nil
	}

	var sb strings.Builder
	sb.WriteString("账号状态：\n")
	for _, a := range accounts {
		enabled := "✅ 启用"
		if !a.Enabled {
			enabled = "⛔ 已停用"
		}
		running := "🔴 未运行"
		if b.manager.IsRunning(a.ID) {
			running = "🟢 运行中"
		}
		authType := "密码"
		if a.AuthType == db.AuthTypeOAuth {
			authType = "OAuth"
		}

		var progress string
		if a.Protocol == "pop3" {
			count, err := db.CountSeenUIDs(b.db, a.ID)
			if err != nil {
				progress = "查询失败"
			} else {
				progress = fmt.Sprintf("已处理 %d 封", count)
			}
		} else {
			state, err := db.GetMailState(b.db, a.ID, "INBOX")
			if err != nil {
				progress = "查询失败"
			} else {
				progress = fmt.Sprintf("LastUID %d", state.LastUID)
			}
		}

		fmt.Fprintf(&sb, "\n#%d %s\n协议: %s | 认证: %s | %s | %s\n同步进度: %s\n",
			a.ID, a.Email, strings.ToUpper(a.Protocol), authType, enabled, running, progress)
	}
	return sb.String(), nil
}

// handleVersion 展示当前版本信息与仓库地址。
func (b *Bot) handleVersion(chatID int64) {
	ver := b.version
	if ver == "" {
		ver = "开发版本"
	}
	text := fmt.Sprintf("📦 telegram-mail-bot\n版本: %s\n仓库: https://github.com/akiker233/telegram-mail-bot\n\n使用 /update 检查是否有新版本可更新", ver)
	b.reply(chatID, text)
}

// handleUpdate 检查 GitHub Releases 是否有新版本，如果有则下载并尝试替换当前二进制。
// 更新成功后自动重启程序。
func (b *Bot) handleUpdate(chatID int64) {
	if b.version == "" {
		b.reply(chatID, "❌ 当前是开发版本，不支持自动更新。请手动从 Release 页面下载。\nhttps://github.com/akiker233/telegram-mail-bot/releases")
		return
	}

	b.reply(chatID, "🔍 正在检查更新...")

	go func() {
		newVer, err := update.CheckVersion(b.version)
		if err != nil {
			b.reply(chatID, fmt.Sprintf("❌ 检查更新失败: %v", err))
			return
		}
		if newVer == "" {
			b.reply(chatID, fmt.Sprintf("✅ 已是最新版本（%s）", b.version))
			return
		}

		// 发现新版本，执行下载并替换。
		b.reply(chatID, fmt.Sprintf("⬇️ 发现新版本 %s，正在下载更新...", newVer))

		if err := update.Run(b.version); err != nil {
			b.reply(chatID, fmt.Sprintf("❌ 更新失败: %v", err))
			return
		}

		b.reply(chatID, fmt.Sprintf("✅ 已更新到 %s，正在重启...", newVer))

		// 自动重启：Windows 启动子进程，Linux/macOS 使用 syscall.Exec 替换当前进程。
		if err := update.Restart(); err != nil {
			slog.Warn("telegram: 自动重启失败", "error", err)
			b.reply(chatID, "⚠️ 重启失败，请手动重启程序。")
			return
		}
	}()
}

func (b *Bot) handleSendStart(chatID, userID int64) {
	accounts, err := db.ListAccountsByUser(b.db, userID)
	if err != nil {
		b.reply(chatID, "❌ 查询账号失败: "+err.Error())
		return
	}

	var sendable []SendableAccount
	for _, a := range accounts {
		if a.SMTPHost != "" {
			sendable = append(sendable, SendableAccount{ID: a.ID, Email: a.Email})
		}
	}
	if len(sendable) == 0 {
		b.reply(chatID, "没有已配置发信（SMTP）的账号，添加账号时可选择配置发信，或用 /listaccounts 查看现有账号状态")
		return
	}

	b.sendSessions.Start(userID, sendable)

	var rows [][]tgbotapi.InlineKeyboardButton
	for i, a := range sendable {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(a.Email, fmt.Sprintf("sendacc:%d", i+1)),
		))
	}
	b.replyWithKeyboard(chatID, "请选择发件账号：", tgbotapi.NewInlineKeyboardMarkup(rows...))
}

func (b *Bot) handleSendSessionReply(chatID, userID int64, sess *SendSession, text string) {
	reply, finished, cancelled := sess.Advance(text)

	if cancelled {
		b.sendSessions.Clear(userID)
		b.reply(chatID, reply)
		return
	}

	if finished {
		b.sendSessions.Clear(userID)
		b.reply(chatID, reply)
		if err := b.sendMail(userID, sess.Draft); err != nil {
			b.reply(chatID, "❌ 发信失败: "+err.Error())
			return
		}
		b.reply(chatID, "✅ 邮件已发送")
		return
	}

	// 非终态步数变化后持久化当前会话状态。
	b.sendSessions.Persist(userID)

	if sess.Step == SendStepConfirm {
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ 确认", "sendconfirm:yes"),
				tgbotapi.NewInlineKeyboardButtonData("🚫 取消", "sendconfirm:no"),
			),
		)
		b.replyWithKeyboard(chatID, reply, kb)
		return
	}
	b.reply(chatID, reply)
}

func (b *Bot) sendMail(userID int64, draft SendDraft) error {
	account, err := db.GetAccountByID(b.db, draft.AccountID)
	if err != nil {
		return fmt.Errorf("查询账号失败: %w", err)
	}
	if account.TelegramUserID != userID || account.SMTPHost == "" {
		return fmt.Errorf("该账号不可用于发信")
	}

	cfg := smtp.Config{
		Host:     account.SMTPHost,
		Port:     account.SMTPPort,
		Username: account.IMAPUsername,
		From:     account.Email,
	}

	if account.AuthType == db.AuthTypeOAuth {
		oauthCfg, ok := b.oauthConfigs[account.OAuthProvider]
		if !ok {
			return fmt.Errorf("OAuth provider %q 未配置", account.OAuthProvider)
		}
		accessToken, err := oauth.RefreshIfNeeded(oauthContext(b.ctx, b.httpClient), b.db, b.encryptionKey, oauthCfg, account)
		if err != nil {
			return fmt.Errorf("获取 OAuth token 失败: %w", err)
		}
		cfg.Auth = smtp.NewXOAUTH2Auth(account.IMAPUsername, accessToken)
	} else {
		password, err := crypto.Decrypt(b.encryptionKey, account.EncryptedPassword)
		if err != nil {
			return fmt.Errorf("解密密码失败: %w", err)
		}
		cfg.Password = password
	}

	return smtp.Send(cfg, smtp.Message{To: draft.To, Subject: draft.Subject, Body: draft.Body})
}

func (b *Bot) handleDelAccount(chatID, userID int64, args string) {
	args = strings.TrimSpace(args)
	if args == "" {
		b.handleListAccounts(chatID, userID)
		return
	}

	id, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		b.reply(chatID, "用法: /delaccount 或 /delaccount <id>，id 可以用 /listaccounts 查看")
		return
	}

	if err := b.deleteAccount(userID, id); err != nil {
		b.reply(chatID, "❌ "+err.Error())
		return
	}
	b.reply(chatID, "✅ 账号已删除")
}

// deleteAccount 校验账号归属后删除，供 /delaccount 文字命令和列表删除按钮共用。
func (b *Bot) deleteAccount(userID, id int64) error {
	account, err := db.GetAccountByID(b.db, id)
	if err == sql.ErrNoRows || (err == nil && account.TelegramUserID != userID) {
		return fmt.Errorf("找不到该账号")
	}
	if err != nil {
		return fmt.Errorf("查询账号失败: %w", err)
	}

	b.manager.Stop(id)
	if err := db.DeleteAccount(b.db, id); err != nil {
		return fmt.Errorf("删除账号失败: %w", err)
	}
	return nil
}

func (b *Bot) reply(chatID int64, text string) {
	b.replyWithParseMode(chatID, text, "")
}

// replyWithParseMode 发送消息，parseMode="HTML" 时按 Telegram HTML 子集解析。
// 如果 HTML 解析失败（转换逻辑生成了非法标签），自动剥除所有标签降级为纯文本重发一次，
// 避免格式错误导致整条通知丢失。
func (b *Bot) replyWithParseMode(chatID int64, text string, parseMode string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode

	if _, err := b.api.Send(msg); err != nil {
		if parseMode != "" && strings.Contains(err.Error(), "can't parse entities") {
			slog.Warn("telegram: HTML parse failed, falling back to plain text", "chat_id", chatID, "error", err)
			plain := tgbotapi.NewMessage(chatID, htmlTagRe.ReplaceAllString(text, ""))
			if _, retryErr := b.api.Send(plain); retryErr != nil {
				slog.Warn("telegram: plain text fallback also failed", "chat_id", chatID, "error", retryErr)
			}
			return
		}
		slog.Warn("telegram: send message failed", "chat_id", chatID, "error", err)
	}
}
