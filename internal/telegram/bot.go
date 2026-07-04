package telegram

import (
	"context"
	"database/sql"
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/oauth2"
)

const updateTimeoutSeconds = 60

// botCommands 是注册到 Telegram 客户端的原生命令菜单（输入 "/" 时的自动补全列表）。
var botCommands = []tgbotapi.BotCommand{
	{Command: "help", Description: "显示帮助与命令列表"},
	{Command: "addaccount", Description: "添加一个邮箱账号"},
	{Command: "listaccounts", Description: "列出已添加的账号"},
	{Command: "delaccount", Description: "删除一个账号"},
	{Command: "status", Description: "查看账号状态"},
	{Command: "send", Description: "用已添加的账号发一封邮件"},
	{Command: "cancel", Description: "取消当前正在进行的操作"},
}

// Bot 是 Telegram 长轮询机器人，负责命令分发和白名单校验。
type Bot struct {
	api           *tgbotapi.BotAPI
	db            *sql.DB
	manager       AccountStarter
	sessions      *SessionStore
	sendSessions  *SendSessionStore
	allowedUsers  map[int64]bool
	encryptionKey []byte
	oauthConfigs  map[string]oauth2.Config // provider -> config，未配置 Client ID 的 provider 不在此表中
	ctx           context.Context
}

// New 创建一个 Bot。encryptionKey 用于加密新添加账号的密码。
// oauthConfigs 按 provider（"gmail"/"outlook"）索引，用于在 /addaccount 中提供 OAuth 登录选项；
// 未配置对应 provider 的 Client ID 时传空 map 即可，该 provider 不会出现在问答流程里。
func New(token string, database *sql.DB, manager AccountStarter, allowedUsers map[int64]bool, encryptionKey []byte, oauthConfigs map[string]oauth2.Config) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}

	// 注册命令菜单失败不影响机器人正常工作（只是客户端里少一个自动补全列表），只记日志。
	if _, err := api.Request(tgbotapi.NewSetMyCommands(botCommands...)); err != nil {
		slog.Warn("telegram: 注册命令菜单失败", "error", err)
	}

	return &Bot{
		api:           api,
		db:            database,
		manager:       manager,
		sessions:      NewSessionStore(database),
		sendSessions:  NewSendSessionStore(database),
		allowedUsers:  allowedUsers,
		encryptionKey: encryptionKey,
		oauthConfigs:  oauthConfigs,
	}, nil
}

// Send 向指定 Telegram 用户发送一条消息（私聊场景下 chat ID 与用户 ID 相同）。
// parseMode 为 "HTML" 时 text 必须是 Telegram 能安全解析的 HTML 子集，为空字符串则按纯文本发送。
func (b *Bot) Send(telegramUserID int64, text string, parseMode string) {
	b.replyWithParseMode(telegramUserID, text, parseMode)
}

// RestoreSessions 从数据库恢复进程重启前的未完成会话。
func (b *Bot) RestoreSessions() {
	availableProviders := make(map[string]bool, len(b.oauthConfigs))
	for provider := range b.oauthConfigs {
		availableProviders[provider] = true
	}
	b.sessions.RestoreAll(availableProviders)
}

// Run 启动长轮询循环，直到 ctx 被取消。
func (b *Bot) Run(ctx context.Context) {
	b.ctx = ctx

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = updateTimeoutSeconds
	updates := b.api.GetUpdatesChan(updateConfig)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update := <-updates:
			if update.CallbackQuery != nil {
				b.handleCallback(update.CallbackQuery)
				continue
			}
			if update.Message == nil {
				continue
			}
			b.handleMessage(update.Message)
		}
	}
}

// replyWithKeyboard 发送一条带 inline keyboard 的纯文本消息。
func (b *Bot) replyWithKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	if _, err := b.api.Send(msg); err != nil {
		slog.Warn("telegram: send message with keyboard failed", "chat_id", chatID, "error", err)
	}
}
