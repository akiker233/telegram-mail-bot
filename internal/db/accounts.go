package db

import (
	"database/sql"
	"time"
)

// Account 表示一个已配置的邮箱账号。
type Account struct {
	ID                int64
	TelegramUserID    int64
	Label             string
	Email             string
	IMAPHost          string
	IMAPPort          int
	IMAPUsername      string
	EncryptedPassword string
	Enabled           bool
	CreatedAt         time.Time
	SMTPHost          string // 空字符串表示该账号未配置发信
	SMTPPort          int
	AuthType          string // "password" | "oauth"
	OAuthProvider     string // "gmail" | "outlook"，AuthType="oauth" 时才有意义
	OAuthRefreshToken string // crypto.Encrypt 加密后存储
	OAuthAccessToken  string // crypto.Encrypt 加密后存储，仅作缓存
	OAuthTokenExpiry  time.Time
	Protocol          string // "imap" | "pop3"
}

const accountColumnsSQL = `id, telegram_user_id, label, email, imap_host, imap_port, imap_username, encrypted_password, enabled, created_at,
	smtp_host, smtp_port, auth_type, oauth_provider, oauth_refresh_token, oauth_access_token, oauth_token_expiry, protocol`

// InsertAccount 插入一个新账号，返回其自增 ID。
func InsertAccount(db *sql.DB, a *Account) (int64, error) {
	authType := a.AuthType
	if authType == "" {
		authType = "password"
	}
	protocol := a.Protocol
	if protocol == "" {
		protocol = "imap"
	}
	var tokenExpiry string
	if !a.OAuthTokenExpiry.IsZero() {
		tokenExpiry = a.OAuthTokenExpiry.UTC().Format(time.RFC3339)
	}

	res, err := db.Exec(
		`INSERT INTO accounts (
			telegram_user_id, label, email, imap_host, imap_port, imap_username, encrypted_password, enabled, created_at,
			smtp_host, smtp_port, auth_type, oauth_provider, oauth_refresh_token, oauth_access_token, oauth_token_expiry, protocol
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.TelegramUserID, a.Label, a.Email, a.IMAPHost, a.IMAPPort, a.IMAPUsername, a.EncryptedPassword, time.Now().UTC().Format(time.RFC3339),
		a.SMTPHost, a.SMTPPort, authType, a.OAuthProvider, a.OAuthRefreshToken, a.OAuthAccessToken, tokenExpiry, protocol,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateOAuthTokens 在刷新 access token 后写回新的 token 和过期时间。
// refreshToken 为空字符串时保留原有值不变（部分服务商刷新时不会返回新的 refresh_token）。
func UpdateOAuthTokens(db *sql.DB, accountID int64, encryptedAccessToken, encryptedRefreshToken string, expiry time.Time) error {
	if encryptedRefreshToken == "" {
		_, err := db.Exec(
			`UPDATE accounts SET oauth_access_token = ?, oauth_token_expiry = ? WHERE id = ?`,
			encryptedAccessToken, expiry.UTC().Format(time.RFC3339), accountID,
		)
		return err
	}
	_, err := db.Exec(
		`UPDATE accounts SET oauth_access_token = ?, oauth_refresh_token = ?, oauth_token_expiry = ? WHERE id = ?`,
		encryptedAccessToken, encryptedRefreshToken, expiry.UTC().Format(time.RFC3339), accountID,
	)
	return err
}

// ListAccountsByUser 返回指定 Telegram 用户添加的所有账号。
func ListAccountsByUser(db *sql.DB, telegramUserID int64) ([]*Account, error) {
	rows, err := db.Query(
		`SELECT `+accountColumnsSQL+`
		 FROM accounts WHERE telegram_user_id = ? ORDER BY id`,
		telegramUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAccounts(rows)
}

// ListEnabledAccounts 返回所有启用状态的账号，用于程序启动时恢复监听。
func ListEnabledAccounts(db *sql.DB) ([]*Account, error) {
	rows, err := db.Query(
		`SELECT ` + accountColumnsSQL + `
		 FROM accounts WHERE enabled = 1 ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAccounts(rows)
}

// GetAccountByID 按 ID 查找账号，找不到返回 sql.ErrNoRows。
func GetAccountByID(db *sql.DB, id int64) (*Account, error) {
	row := db.QueryRow(
		`SELECT `+accountColumnsSQL+`
		 FROM accounts WHERE id = ?`,
		id,
	)
	return scanAccount(row)
}

// DeleteAccount 删除账号（级联删除关联的 mail_state 记录）。
func DeleteAccount(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM accounts WHERE id = ?`, id)
	return err
}

func scanAccounts(rows *sql.Rows) ([]*Account, error) {
	var accounts []*Account
	for rows.Next() {
		a, err := scanAccountRow(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (*Account, error) {
	return scanAccountRow(row)
}

func scanAccountRow(row rowScanner) (*Account, error) {
	var a Account
	var enabled int
	var createdAt, tokenExpiry string
	if err := row.Scan(
		&a.ID, &a.TelegramUserID, &a.Label, &a.Email, &a.IMAPHost, &a.IMAPPort, &a.IMAPUsername, &a.EncryptedPassword, &enabled, &createdAt,
		&a.SMTPHost, &a.SMTPPort, &a.AuthType, &a.OAuthProvider, &a.OAuthRefreshToken, &a.OAuthAccessToken, &tokenExpiry, &a.Protocol,
	); err != nil {
		return nil, err
	}
	a.Enabled = enabled == 1
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		a.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, tokenExpiry); err == nil {
		a.OAuthTokenExpiry = t
	}
	return &a, nil
}
