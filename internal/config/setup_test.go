package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSetupWizardSkipsOptionalFieldsOnEmptyInput(t *testing.T) {
	keys := make([]string, 0, len(setupFields))
	for _, f := range setupFields {
		keys = append(keys, f.key)
	}
	unsetForTest(t, keys...)

	dir := t.TempDir()
	chdirForTest(t, dir)

	// 依次对应 setupFields 顺序：必填三项给值，其余全部留空跳过。
	input := "token\nkey\n111\n\n\n\n\n\n"
	var out bytes.Buffer
	if err := runSetupWizard(strings.NewReader(input), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if os.Getenv("TELEGRAM_BOT_TOKEN") != "token" {
		t.Errorf("expected TELEGRAM_BOT_TOKEN to be set from input, got %q", os.Getenv("TELEGRAM_BOT_TOKEN"))
	}
	if os.Getenv("GMAIL_OAUTH_CLIENT_ID") != "" {
		t.Errorf("expected optional field to remain empty, got %q", os.Getenv("GMAIL_OAUTH_CLIENT_ID"))
	}

	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("expected .env to be written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "TELEGRAM_BOT_TOKEN=token") {
		t.Errorf(".env missing TELEGRAM_BOT_TOKEN, got: %s", content)
	}
	if strings.Contains(content, "GMAIL_OAUTH_CLIENT_ID=") {
		t.Errorf(".env should not contain skipped optional field, got: %s", content)
	}
}

func TestRunSetupWizardRepromptsOnEmptyRequiredField(t *testing.T) {
	keys := make([]string, 0, len(setupFields))
	for _, f := range setupFields {
		keys = append(keys, f.key)
	}
	unsetForTest(t, keys...)

	dir := t.TempDir()
	chdirForTest(t, dir)

	// TELEGRAM_BOT_TOKEN 先留空一次再补上，之后必填项和剩余可选项全部给值/跳过。
	input := "\ntoken\nkey\n222\n\n\n\n\n\n"
	var out bytes.Buffer
	if err := runSetupWizard(strings.NewReader(input), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if os.Getenv("TELEGRAM_BOT_TOKEN") != "token" {
		t.Errorf("expected reprompt to eventually capture value, got %q", os.Getenv("TELEGRAM_BOT_TOKEN"))
	}
	if !strings.Contains(out.String(), "该项为必填") {
		t.Errorf("expected reprompt message in output, got: %s", out.String())
	}
}

func TestHasRequiredEnv(t *testing.T) {
	unsetForTest(t, "TELEGRAM_BOT_TOKEN", "MASTER_KEY", "ALLOWED_TELEGRAM_USERS")

	if hasRequiredEnv() {
		t.Error("expected hasRequiredEnv to be false when required vars are missing")
	}

	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("MASTER_KEY", "key")
	t.Setenv("ALLOWED_TELEGRAM_USERS", "1")

	if !hasRequiredEnv() {
		t.Error("expected hasRequiredEnv to be true when all required vars are set")
	}
}

// chdirForTest 切换工作目录到 dir，并在测试结束后恢复原目录。
func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir into temp dir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(original) })
}
