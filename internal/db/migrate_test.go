package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateAddsColumnsToLegacyDatabase 模拟一个只有旧版 schema（没有本次新增列）的数据库，
// 验证 Open 能自动补齐缺失列，而不是要求用户手动迁移或删库重建。
func TestMigrateAddsColumnsToLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacyDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacyDB.Exec(`
		CREATE TABLE accounts (
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
		)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := legacyDB.Exec(
		`INSERT INTO accounts (telegram_user_id, label, email, imap_host, imap_port, imap_username, encrypted_password, created_at)
		 VALUES (1, 'old', 'old@example.com', 'imap.example.com', 993, 'old@example.com', 'blob', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer database.Close()

	columns, err := existingColumns(database, "accounts")
	if err != nil {
		t.Fatalf("existingColumns returned error: %v", err)
	}
	for _, want := range []string{"smtp_host", "smtp_port", "auth_type", "oauth_provider", "oauth_refresh_token", "oauth_access_token", "oauth_token_expiry", "protocol"} {
		if !columns[want] {
			t.Errorf("expected column %q to be added by migration", want)
		}
	}

	accounts, err := ListAccountsByUser(database, 1)
	if err != nil {
		t.Fatalf("ListAccountsByUser returned error: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Email != "old@example.com" {
		t.Fatalf("expected pre-existing row to survive migration, got %+v", accounts)
	}
}

// TestMigrateIsIdempotent 验证对同一个数据库多次调用 Open（即多次运行 migrate）不会报错，
// 这是程序每次启动都会经历的路径。
func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idempotent.db")

	for i := 0; i < 3; i++ {
		database, err := Open(path)
		if err != nil {
			t.Fatalf("Open call #%d returned error: %v", i+1, err)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("close call #%d returned error: %v", i+1, err)
		}
	}
}
