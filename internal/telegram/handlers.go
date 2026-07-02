package telegram

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"telegram-mail-bot/internal/crypto"
	"telegram-mail-bot/internal/db"
	"telegram-mail-bot/internal/oauth"
	"telegram-mail-bot/internal/smtp"
)

// htmlTagRe 用于 HTML 发送失败时的降级：剥除所有标签得到纯文本重发一次。
var htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)

const helpText = `可用命令：
/addaccount - 添加一个邮箱账号（IMAP，默认）
/addaccount pop3 - 用 POP3 协议添加账号（无实时推送，定时轮询）
/listaccounts - 列出已添加的账号
/delaccount <id> - 删除一个账号
/send - 用已添加的账号发一封邮件
/cancel - 取消当前正在进行的操作`

// AccountStarter 抽象了账号添加成功后启动监听的动作，避免 telegram 包依赖 manager 包。
type AccountStarter interface {
	Start(ctx context.Context, account *db.Account) error
	Stop(accountID int64)
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
		b.reply(chatID, "请先完成或使用 /cancel 取消当前操作")
		return
	}

	switch command {
	case "start":
		b.reply(chatID, helpText)
	case "addaccount":
		protocol := strings.ToLower(strings.TrimSpace(args))
		if protocol != "" && protocol != "imap" && protocol != "pop3" {
			b.reply(chatID, "用法: /addaccount 或 /addaccount pop3")
			return
		}
		availableProviders := make(map[string]bool, len(b.oauthConfigs))
		for provider := range b.oauthConfigs {
			availableProviders[provider] = true
		}
		b.sessions.Start(userID, availableProviders, protocol)
		b.reply(chatID, "请输入要添加的邮箱地址")
	case "send":
		b.handleSendStart(chatID, userID)
	case "cancel":
		b.sessions.Clear(userID)
		b.sendSessions.Clear(userID)
		b.reply(chatID, "已取消当前操作")
	case "listaccounts":
		b.handleListAccounts(chatID, userID)
	case "delaccount":
		b.handleDelAccount(chatID, userID, args)
	default:
		b.reply(chatID, helpText)
	}
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
			b.reply(chatID, "保存账号失败: "+err.Error())
			return
		}
		b.reply(chatID, reply)
		return
	}

	if sess.Step == StepOAuthPending {
		b.startOAuthFlow(chatID, userID, sess)
		return
	}

	b.reply(chatID, reply)
}

// startOAuthFlow 发起 device flow：立刻回复用户授权链接和一次性代码，然后在后台
// goroutine 里轮询 token 端点。轮询是异步的，不会阻塞 Telegram 长轮询主循环。
func (b *Bot) startOAuthFlow(chatID, userID int64, sess *Session) {
	cfg, ok := b.oauthConfigs[sess.Draft.OAuthProvider]
	if !ok {
		b.sessions.Clear(userID)
		b.reply(chatID, "该 OAuth 登录方式未配置，请使用密码/授权码方式添加账号")
		return
	}

	resp, err := oauth.StartDeviceFlow(b.ctx, cfg)
	if err != nil {
		b.sessions.Clear(userID)
		b.reply(chatID, "发起 OAuth 授权失败: "+err.Error())
		return
	}

	b.reply(chatID, fmt.Sprintf("请在浏览器打开 %s 并输入代码 %s 完成授权（最多等待8分钟）", resp.VerificationURI, resp.UserCode))

	go func() {
		token, err := oauth.PollToken(b.ctx, cfg, resp)

		// 轮询期间用户可能已经 /cancel 或重新 /addaccount，此时 sess 已不是当前会话，
		// 结果应该被丢弃，不能写进一个不再代表当前状态的会话里。
		if b.sessions.Get(userID) != sess {
			return
		}

		if err != nil {
			b.sessions.Clear(userID)
			b.reply(chatID, "OAuth 授权失败或超时: "+err.Error())
			return
		}

		reply := sess.CompleteOAuth(token.AccessToken, token.RefreshToken, token.Expiry)
		b.reply(chatID, reply)
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

	if draft.AuthType == "oauth" {
		encryptedAccess, err := crypto.Encrypt(b.encryptionKey, draft.OAuthAccessToken)
		if err != nil {
			return fmt.Errorf("加密 access token 失败: %w", err)
		}
		encryptedRefresh, err := crypto.Encrypt(b.encryptionKey, draft.OAuthRefreshToken)
		if err != nil {
			return fmt.Errorf("加密 refresh token 失败: %w", err)
		}
		account.AuthType = "oauth"
		account.OAuthProvider = draft.OAuthProvider
		account.OAuthAccessToken = encryptedAccess
		account.OAuthRefreshToken = encryptedRefresh
		account.OAuthTokenExpiry = draft.OAuthTokenExpiry
	} else {
		encryptedPassword, err := crypto.Encrypt(b.encryptionKey, draft.Password)
		if err != nil {
			return fmt.Errorf("加密密码失败: %w", err)
		}
		account.AuthType = "password"
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
	accounts, err := db.ListAccountsByUser(b.db, userID)
	if err != nil {
		b.reply(chatID, "查询账号失败: "+err.Error())
		return
	}
	if len(accounts) == 0 {
		b.reply(chatID, "还没有添加任何账号，使用 /addaccount 添加")
		return
	}

	var sb strings.Builder
	sb.WriteString("已添加的账号：\n")
	for _, a := range accounts {
		status := "启用"
		if !a.Enabled {
			status = "已停用"
		}
		protocol := strings.ToUpper(a.Protocol)
		fmt.Fprintf(&sb, "#%d %s [%s %s:%d] [%s]\n", a.ID, a.Email, protocol, a.IMAPHost, a.IMAPPort, status)
	}
	b.reply(chatID, sb.String())
}

func (b *Bot) handleSendStart(chatID, userID int64) {
	accounts, err := db.ListAccountsByUser(b.db, userID)
	if err != nil {
		b.reply(chatID, "查询账号失败: "+err.Error())
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

	var sb strings.Builder
	sb.WriteString("请选择发件账号（回复编号）：\n")
	for i, a := range sendable {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, a.Email)
	}
	b.reply(chatID, sb.String())
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
			b.reply(chatID, "发信失败: "+err.Error())
			return
		}
		b.reply(chatID, "邮件已发送")
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

	password, err := crypto.Decrypt(b.encryptionKey, account.EncryptedPassword)
	if err != nil {
		return fmt.Errorf("解密密码失败: %w", err)
	}

	return smtp.Send(
		smtp.Config{
			Host:     account.SMTPHost,
			Port:     account.SMTPPort,
			Username: account.IMAPUsername,
			Password: password,
			From:     account.Email,
		},
		smtp.Message{To: draft.To, Subject: draft.Subject, Body: draft.Body},
	)
}

func (b *Bot) handleDelAccount(chatID, userID int64, args string) {
	args = strings.TrimSpace(args)
	id, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		b.reply(chatID, "用法: /delaccount <id>，id 可以用 /listaccounts 查看")
		return
	}

	account, err := db.GetAccountByID(b.db, id)
	if err == sql.ErrNoRows || (err == nil && account.TelegramUserID != userID) {
		b.reply(chatID, "找不到该账号")
		return
	}
	if err != nil {
		b.reply(chatID, "查询账号失败: "+err.Error())
		return
	}

	b.manager.Stop(id)
	if err := db.DeleteAccount(b.db, id); err != nil {
		b.reply(chatID, "删除账号失败: "+err.Error())
		return
	}
	b.reply(chatID, "账号已删除")
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
			log.Printf("telegram: HTML parse failed for chat %d, falling back to plain text: %v", chatID, err)
			plain := tgbotapi.NewMessage(chatID, htmlTagRe.ReplaceAllString(text, ""))
			if _, retryErr := b.api.Send(plain); retryErr != nil {
				log.Printf("telegram: plain text fallback also failed for chat %d: %v", chatID, retryErr)
			}
			return
		}
		log.Printf("telegram: send message to chat %d failed: %v", chatID, err)
	}
}
