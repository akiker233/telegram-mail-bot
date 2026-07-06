package manager

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"telegram-mail-bot/internal/crypto"
	"telegram-mail-bot/internal/db"
	"telegram-mail-bot/internal/mail"
	"telegram-mail-bot/internal/oauth"
)

// SendFunc 将一条文本消息发送给指定的 Telegram 用户。parseMode 为 "HTML" 时
// text 必须是 Telegram parse_mode=HTML 能安全解析的内容，为空字符串时按纯文本发送。
type SendFunc func(telegramUserID int64, text string, parseMode string)

// Manager 管理每个邮箱账号的监听 goroutine 的生命周期。
type Manager struct {
	db           *sql.DB
	key          []byte
	send         SendFunc
	oauthConfigs map[string]oauth2.Config // provider -> config，未配置 Client ID 的 provider 不在此表中
	mu           sync.Mutex
	cancels      map[int64]context.CancelFunc
}

// New 创建一个 Manager。key 是通过 crypto.DeriveKey 派生的加密密钥。
// oauthConfigs 按 provider（"gmail"/"outlook"）索引，用于 OAuth 账号刷新 token；
// 未配置对应 provider 的 Client ID 时传空 map 即可，OAuth 账号会在启动时报认证错误。
func New(database *sql.DB, key []byte, send SendFunc, oauthConfigs map[string]oauth2.Config) *Manager {
	return &Manager{
		db:           database,
		key:          key,
		send:         send,
		oauthConfigs: oauthConfigs,
		cancels:      make(map[int64]context.CancelFunc),
	}
}

// StartAll 恢复所有已启用账号的监听，通常在程序启动时调用一次。
func (m *Manager) StartAll(ctx context.Context) error {
	accounts, err := db.ListEnabledAccounts(m.db)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if err := m.Start(ctx, a); err != nil {
			return fmt.Errorf("manager: start account %d: %w", a.ID, err)
		}
	}
	return nil
}

// Start 为一个账号启动监听 goroutine。parent 结束时该账号的监听也会停止。
func (m *Manager) Start(parent context.Context, account *db.Account) error {
	notify := m.notifyFunc(account)
	onAuthError := m.authErrorFunc(account)

	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	if existing, ok := m.cancels[account.ID]; ok {
		existing()
	}
	m.cancels[account.ID] = cancel
	m.mu.Unlock()

	if account.Protocol == "pop3" {
		password, err := crypto.Decrypt(m.key, account.EncryptedPassword)
		if err != nil {
			cancel()
			return fmt.Errorf("manager: decrypt password: %w", err)
		}
		cfg := mail.POP3AccountConfig{
			AccountID: account.ID,
			Host:      account.IMAPHost,
			Port:      account.IMAPPort,
			Username:  account.IMAPUsername,
			Password:  password,
		}
		go mail.ListenPOP3(ctx, cfg, &pop3StateStore{db: m.db}, notify, onAuthError)
		return nil
	}

	cfg := mail.AccountConfig{
		AccountID: account.ID,
		Host:      account.IMAPHost,
		Port:      account.IMAPPort,
		Username:  account.IMAPUsername,
	}

	if account.AuthType == db.AuthTypeOAuth {
		oauthCfg, ok := m.oauthConfigs[account.OAuthProvider]
		if !ok {
			cancel()
			return fmt.Errorf("manager: oauth provider %q not configured", account.OAuthProvider)
		}
		cfg.TokenProvider = m.tokenProvider(parent, oauthCfg, account.ID)
	} else {
		password, err := crypto.Decrypt(m.key, account.EncryptedPassword)
		if err != nil {
			cancel()
			return fmt.Errorf("manager: decrypt password: %w", err)
		}
		cfg.Password = password
	}

	go mail.Listen(ctx, cfg, &dbStateStore{db: m.db}, notify, onAuthError)
	return nil
}

func (m *Manager) notifyFunc(account *db.Account) mail.NotifyFunc {
	telegramUserID := account.TelegramUserID
	return func(_ int64, summary *mail.Summary) {
		if summary.BodyIsHTML {
			m.send(telegramUserID, formatSummaryHTML(account.Label, summary), "HTML")
		} else {
			m.send(telegramUserID, formatSummary(account.Label, summary), "")
		}
	}
}

func (m *Manager) authErrorFunc(account *db.Account) mail.AuthErrorFunc {
	telegramUserID := account.TelegramUserID
	return func(accountID int64, err error) {
		slog.Warn("manager: auth error, will retry", "account_id", accountID, "error", err)
		if account.AuthType == db.AuthTypeOAuth {
			m.send(telegramUserID, fmt.Sprintf("账号 %s 的 OAuth 授权已失效（%v），请使用 /reauthorize 重新授权。系统会每隔 5 分钟自动重试连接。", account.Label, err), "")
		} else {
			m.send(telegramUserID, fmt.Sprintf("账号 %s 的密码认证失败（%v），请检查密码是否正确。系统会每隔 5 分钟自动重试连接。", account.Label, err), "")
		}
	}
}

// tokenProvider 返回一个每次调用都会拿到有效 access token 的回调，供 IMAP 长连接
// 断线重连时重新取用（一次性 token 字符串在重连时可能已经过期）。
// 只有 oauth.IsPermanent 判定为不可恢复（refresh token 被撤销/过期）时才包成
// *mail.AuthError 让 Listen 放弃重试；其余错误（网络抖动等瞬时故障）原样返回，
// 让 Listen 按已有的指数退避逻辑重试，而不是要求用户删除账号重新添加。
func (m *Manager) tokenProvider(ctx context.Context, oauthCfg oauth2.Config, accountID int64) func() (string, error) {
	return func() (string, error) {
		account, err := db.GetAccountByID(m.db, accountID)
		if err != nil {
			return "", fmt.Errorf("reload account: %w", err)
		}
		token, err := oauth.RefreshIfNeeded(ctx, m.db, m.key, oauthCfg, account)
		if err != nil && oauth.IsPermanent(err) {
			return "", &mail.AuthError{Err: err}
		}
		return token, err
	}
}

// Stop 停止指定账号的监听 goroutine。
func (m *Manager) Stop(accountID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.cancels[accountID]; ok {
		cancel()
		delete(m.cancels, accountID)
	}
}

// IsRunning 返回该账号当前是否有正在运行的监听 goroutine。
func (m *Manager) IsRunning(accountID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.cancels[accountID]
	return ok
}

func formatSummary(label string, s *mail.Summary) string {
	return fmt.Sprintf("📬 %s\n发件人: %s\n主题: %s\n\n%s", label, s.From, s.Subject, s.Body)
}

// formatSummaryHTML 与 formatSummary 等价，但用于 s.BodyIsHTML=true 的情况：
// 拼进模板的 label/From/Subject 也必须转义，否则邮件标题里的尖括号会破坏整条消息的 HTML 解析。
func formatSummaryHTML(label string, s *mail.Summary) string {
	return fmt.Sprintf(
		"📬 <b>%s</b>\n发件人: %s\n主题: %s\n\n%s",
		html.EscapeString(label), html.EscapeString(s.From), html.EscapeString(s.Subject), s.Body,
	)
}

// dbStateStore 用 SQLite 的 mail_state 表实现 mail.StateStore。
type dbStateStore struct {
	db *sql.DB
}

func (s *dbStateStore) GetState(accountID int64) (uidValidity, lastUID uint32, err error) {
	state, err := db.GetMailState(s.db, accountID, "INBOX")
	if err != nil {
		return 0, 0, err
	}
	return state.UIDValidity, state.LastUID, nil
}

func (s *dbStateStore) SaveState(accountID int64, uidValidity, lastUID uint32) error {
	return db.SaveMailState(s.db, &db.MailState{
		AccountID:   accountID,
		Folder:      "INBOX",
		UIDValidity: uidValidity,
		LastUID:     lastUID,
	})
}

// pop3StateStore 用 SQLite 的 pop3_seen_uids 表实现 mail.POP3StateStore。
type pop3StateStore struct {
	db *sql.DB
}

func (s *pop3StateStore) HasSeenUID(accountID int64, uidl string) (bool, error) {
	return db.HasSeenUID(s.db, accountID, uidl)
}

func (s *pop3StateStore) MarkSeenUID(accountID int64, uidl string) error {
	return db.MarkSeenUID(s.db, accountID, uidl)
}

func (s *pop3StateStore) PruneSeenUIDs(accountID int64, olderThan time.Time) error {
	return db.PruneSeenUIDs(s.db, accountID, olderThan)
}
