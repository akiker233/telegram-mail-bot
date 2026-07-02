package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingToken(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("MASTER_KEY", "key")
	t.Setenv("ALLOWED_TELEGRAM_USERS", "123")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when TELEGRAM_BOT_TOKEN is missing")
	}
}

func TestLoadMissingMasterKey(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("MASTER_KEY", "")
	t.Setenv("ALLOWED_TELEGRAM_USERS", "123")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when MASTER_KEY is missing")
	}
}

func TestLoadInvalidAllowedUsers(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("MASTER_KEY", "key")
	t.Setenv("ALLOWED_TELEGRAM_USERS", "abc")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when ALLOWED_TELEGRAM_USERS contains a non-numeric id")
	}
}

func TestLoadSuccess(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("MASTER_KEY", "key")
	t.Setenv("ALLOWED_TELEGRAM_USERS", "111, 222,333")
	t.Setenv("DB_PATH", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DBPath != "./mailbot.db" {
		t.Errorf("expected default DBPath, got %q", cfg.DBPath)
	}

	for _, id := range []int64{111, 222, 333} {
		if !cfg.AllowedUsers[id] {
			t.Errorf("expected user %d to be allowed", id)
		}
	}
	if cfg.AllowedUsers[444] {
		t.Error("user 444 should not be allowed")
	}
}

// unsetForTest 彻底清除给定环境变量（而不是像 t.Setenv 那样设为空字符串），并在测试结束时恢复原值。
func unsetForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		original, wasSet := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if wasSet {
				os.Setenv(key, original)
			}
		})
	}
}

func TestLoadReadsDotEnvFile(t *testing.T) {
	// godotenv.Load 只在变量完全不存在时才从 .env 填充（用 os.LookupEnv 判断存在性），
	// t.Setenv(key, "") 会让变量以空字符串"存在"，反而会阻止 .env 生效，所以这里要真正 Unsetenv。
	unsetForTest(t, "TELEGRAM_BOT_TOKEN", "MASTER_KEY", "ALLOWED_TELEGRAM_USERS", "DB_PATH")

	dir := t.TempDir()
	envContent := "TELEGRAM_BOT_TOKEN=dotenv-token\nMASTER_KEY=dotenv-key\nALLOWED_TELEGRAM_USERS=555\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0o600); err != nil {
		t.Fatalf("failed to write .env fixture: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir into temp dir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(originalWD) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TelegramBotToken != "dotenv-token" {
		t.Errorf("expected token from .env, got %q", cfg.TelegramBotToken)
	}
	if cfg.MasterKey != "dotenv-key" {
		t.Errorf("expected master key from .env, got %q", cfg.MasterKey)
	}
	if !cfg.AllowedUsers[555] {
		t.Error("expected allowed user 555 from .env")
	}
}

func TestLoadRealEnvVarTakesPrecedenceOverDotEnv(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "real-env-token")
	t.Setenv("MASTER_KEY", "real-env-key")
	t.Setenv("ALLOWED_TELEGRAM_USERS", "999")

	dir := t.TempDir()
	envContent := "TELEGRAM_BOT_TOKEN=dotenv-token\nMASTER_KEY=dotenv-key\nALLOWED_TELEGRAM_USERS=555\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0o600); err != nil {
		t.Fatalf("failed to write .env fixture: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir into temp dir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(originalWD) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TelegramBotToken != "real-env-token" {
		t.Errorf("expected real env var to take precedence, got %q", cfg.TelegramBotToken)
	}
}
