package telegram

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"telegram-mail-bot/internal/db"
)

// fakeAccountStarter 是测试用的 AccountStarter，不做任何真实的监听启动/停止。
type fakeAccountStarter struct{}

func (fakeAccountStarter) Start(ctx context.Context, account *db.Account) error { return nil }
func (fakeAccountStarter) Stop(accountID int64)                                {}
func (fakeAccountStarter) IsRunning(accountID int64) bool                      { return true }

func newTestBot(t *testing.T) *Bot {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open returned error: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return &Bot{db: database, manager: fakeAccountStarter{}}
}

func TestRenderAccountsListEmpty(t *testing.T) {
	b := newTestBot(t)
	text, keyboard, err := b.renderAccountsList(111)
	if err != nil {
		t.Fatalf("renderAccountsList returned error: %v", err)
	}
	if len(keyboard.InlineKeyboard) != 0 {
		t.Fatalf("expected no keyboard when there are no accounts, got %+v", keyboard)
	}
	if !strings.Contains(text, "还没有添加任何账号") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestRenderAccountsListWithAccounts(t *testing.T) {
	b := newTestBot(t)
	id, err := db.InsertAccount(b.db, &db.Account{
		TelegramUserID: 111,
		Label:          "gmail",
		Email:          "user@gmail.com",
		IMAPHost:       "imap.gmail.com",
		IMAPPort:       993,
		IMAPUsername:   "user@gmail.com",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	text, keyboard, err := b.renderAccountsList(111)
	if err != nil {
		t.Fatalf("renderAccountsList returned error: %v", err)
	}
	if !strings.Contains(text, "user@gmail.com") {
		t.Fatalf("expected account email in text, got %q", text)
	}
	if len(keyboard.InlineKeyboard) != 1 {
		t.Fatalf("expected 1 delete button row, got %d", len(keyboard.InlineKeyboard))
	}
	button := keyboard.InlineKeyboard[0][0]
	wantData := "del:" + strconv.FormatInt(id, 10)
	if button.CallbackData == nil || *button.CallbackData != wantData {
		t.Fatalf("unexpected callback data: %+v", button.CallbackData)
	}
}

func TestDeleteAccountRejectsOtherUsersAccount(t *testing.T) {
	b := newTestBot(t)
	id, err := db.InsertAccount(b.db, &db.Account{
		TelegramUserID: 111,
		Email:          "user@gmail.com",
		IMAPHost:       "imap.gmail.com",
		IMAPPort:       993,
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	if err := b.deleteAccount(222, id); err == nil {
		t.Fatal("expected error when deleting another user's account")
	}
}

func TestDeleteAccountRemovesOwnAccount(t *testing.T) {
	b := newTestBot(t)
	id, err := db.InsertAccount(b.db, &db.Account{
		TelegramUserID: 111,
		Email:          "user@gmail.com",
		IMAPHost:       "imap.gmail.com",
		IMAPPort:       993,
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	if err := b.deleteAccount(111, id); err != nil {
		t.Fatalf("deleteAccount returned error: %v", err)
	}

	if _, err := db.GetAccountByID(b.db, id); err == nil {
		t.Fatal("expected account to be deleted")
	}
}

func TestRenderAccountStatusEmpty(t *testing.T) {
	b := newTestBot(t)
	text, err := b.renderAccountStatus(111)
	if err != nil {
		t.Fatalf("renderAccountStatus returned error: %v", err)
	}
	if !strings.Contains(text, "还没有添加任何账号") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestRenderAccountStatusShowsProgress(t *testing.T) {
	b := newTestBot(t)

	imapID, err := db.InsertAccount(b.db, &db.Account{
		TelegramUserID: 111,
		Email:          "user@gmail.com",
		IMAPHost:       "imap.gmail.com",
		IMAPPort:       993,
		Protocol:       "imap",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}
	if err := db.SaveMailState(b.db, &db.MailState{AccountID: imapID, Folder: "INBOX", LastUID: 42}); err != nil {
		t.Fatalf("SaveMailState returned error: %v", err)
	}

	pop3ID, err := db.InsertAccount(b.db, &db.Account{
		TelegramUserID: 111,
		Email:          "user@163.com",
		IMAPHost:       "pop.163.com",
		IMAPPort:       995,
		Protocol:       "pop3",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}
	if err := db.MarkSeenUID(b.db, pop3ID, "uid-1"); err != nil {
		t.Fatalf("MarkSeenUID returned error: %v", err)
	}

	text, err := b.renderAccountStatus(111)
	if err != nil {
		t.Fatalf("renderAccountStatus returned error: %v", err)
	}
	if !strings.Contains(text, "user@gmail.com") || !strings.Contains(text, "LastUID 42") {
		t.Fatalf("expected IMAP account with LastUID 42 in text, got %q", text)
	}
	if !strings.Contains(text, "user@163.com") || !strings.Contains(text, "已处理 1 封") {
		t.Fatalf("expected POP3 account with processed count in text, got %q", text)
	}
	if !strings.Contains(text, "🟢 运行中") {
		t.Fatalf("expected running status from fakeAccountStarter, got %q", text)
	}
}

func TestButtonText(t *testing.T) {
	cases := []struct {
		prefix, value, want string
	}{
		{"authmethod", "oauth", "oauth"},
		{"authmethod", "password", "password"},
		{"smtpopt", "yes", "是"},
		{"smtpopt", "no", "否"},
		{"addconfirm", "yes", "确认"},
		{"addconfirm", "no", "取消"},
		{"sendconfirm", "yes", "确认"},
		{"sendconfirm", "no", "取消"},
		{"sendacc", "2", "2"},
	}
	for _, c := range cases {
		if got := buttonText(c.prefix, c.value); got != c.want {
			t.Errorf("buttonText(%q, %q) = %q, want %q", c.prefix, c.value, got, c.want)
		}
	}
}

func TestButtonLabel(t *testing.T) {
	cases := []struct {
		prefix, value, want string
	}{
		{"authmethod", "oauth", "OAuth"},
		{"authmethod", "password", "密码/授权码"},
		{"smtpopt", "yes", "是"},
		{"smtpopt", "no", "否"},
		{"addconfirm", "yes", "确认"},
		{"addconfirm", "no", "取消"},
		{"sendconfirm", "yes", "确认"},
		{"sendconfirm", "no", "取消"},
		{"sendacc", "2", "账号 #2"},
	}
	for _, c := range cases {
		if got := buttonLabel(c.prefix, c.value); got != c.want {
			t.Errorf("buttonLabel(%q, %q) = %q, want %q", c.prefix, c.value, got, c.want)
		}
	}
}

func TestKeyboardForStep(t *testing.T) {
	cases := []struct {
		step     Step
		wantKeys bool
	}{
		{StepAuthMethod, true},
		{StepSMTPOptional, true},
		{StepConfirm, true},
		{StepEmail, false},
		{StepHost, false},
		{StepPort, false},
		{StepPassword, false},
		{StepOAuthPending, false},
	}
	for _, c := range cases {
		kb := keyboardForStep(c.step)
		if c.wantKeys && kb == nil {
			t.Errorf("keyboardForStep(%v) = nil, want a keyboard", c.step)
		}
		if !c.wantKeys && kb != nil {
			t.Errorf("keyboardForStep(%v) = %+v, want nil", c.step, kb)
		}
	}
}
