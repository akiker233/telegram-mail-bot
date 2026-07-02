package telegram

import (
	"context"
	"database/sql"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/oauth2"
)

const updateTimeoutSeconds = 60

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
	return &Bot{
		api:           api,
		db:            database,
		manager:       manager,
		sessions:      NewSessionStore(),
		sendSessions:  NewSendSessionStore(),
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
			if update.Message == nil {
				continue
			}
			b.handleMessage(update.Message)
		}
	}
}
