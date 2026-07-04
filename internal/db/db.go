package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	telegram_user_id INTEGER NOT NULL,
	label TEXT NOT NULL,
	email TEXT NOT NULL,
	imap_host TEXT NOT NULL,
	imap_port INTEGER NOT NULL,
	imap_username TEXT NOT NULL,
	encrypted_password TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mail_state (
	account_id INTEGER NOT NULL,
	folder TEXT NOT NULL DEFAULT 'INBOX',
	uid_validity INTEGER NOT NULL DEFAULT 0,
	last_uid INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (account_id, folder),
	FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS pop3_seen_uids (
	account_id INTEGER NOT NULL,
	uidl TEXT NOT NULL,
	seen_at TEXT NOT NULL,
	PRIMARY KEY (account_id, uidl),
	FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_pop3_seen_uids_seen_at ON pop3_seen_uids(account_id, seen_at);

CREATE TABLE IF NOT EXISTS sessions (
	user_id INTEGER NOT NULL,
	session_type TEXT NOT NULL,
	step INTEGER NOT NULL DEFAULT 0,
	draft_json TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (user_id, session_type)
);
`

// accountColumns 列出本项目迭代过程中追加到 accounts 表的列。
// CREATE TABLE IF NOT EXISTS 只对全新数据库生效，已存在的旧数据库需要靠这里的
// ALTER TABLE 补齐新列，新列必须使用 NOT NULL DEFAULT 才能安全地加到已有数据行上。
var accountColumns = []string{
	"smtp_host TEXT NOT NULL DEFAULT ''",
	"smtp_port INTEGER NOT NULL DEFAULT 0",
	"auth_type TEXT NOT NULL DEFAULT 'password'",
	"oauth_provider TEXT NOT NULL DEFAULT ''",
	"oauth_refresh_token TEXT NOT NULL DEFAULT ''",
	"oauth_access_token TEXT NOT NULL DEFAULT ''",
	"oauth_token_expiry TEXT NOT NULL DEFAULT ''",
	"protocol TEXT NOT NULL DEFAULT 'imap'",
}

// Open 打开 SQLite 数据库并确保表结构存在。
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: ping %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: enable foreign keys: %w", err)
	}
	// WAL 模式：读写并发更好，适合多 goroutine 场景（Manager 多账号监听）。
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: enable wal: %w", err)
	}
	// 锁等待超时 5 秒，避免并发写入时立即报 SQLITE_BUSY。
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: set busy_timeout: %w", err)
	}
	// WAL 模式下 synchronous=NORMAL 即可保证持久性，且性能优于 FULL。
	if _, err := db.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: set synchronous: %w", err)
	}
	// SQLite 是单写者，限制最大打开连接数避免过多 WAL 文件。
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: create schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: migrate: %w", err)
	}
	return db, nil
}

// migrate 为已存在的 accounts 表补齐缺失的列。SQLite 的 CREATE TABLE IF NOT EXISTS
// 只在表不存在时生效，无法给旧表补列，所以需要先查现有列名再决定要不要 ADD COLUMN。
func migrate(db *sql.DB) error {
	existing, err := existingColumns(db, "accounts")
	if err != nil {
		return err
	}

	for _, def := range accountColumns {
		name := strings.Fields(def)[0]
		if existing[name] {
			continue
		}
		if _, err := db.Exec("ALTER TABLE accounts ADD COLUMN " + def); err != nil {
			return fmt.Errorf("add column %s: %w", name, err)
		}
	}
	return nil
}

func existingColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}
