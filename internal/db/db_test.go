package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestInsertAndListAccounts(t *testing.T) {
	database := openTestDB(t)

	id, err := InsertAccount(database, &Account{
		TelegramUserID:    111,
		Label:             "gmail",
		Email:             "user@gmail.com",
		IMAPHost:          "imap.gmail.com",
		IMAPPort:          993,
		IMAPUsername:      "user@gmail.com",
		EncryptedPassword: "encrypted-blob",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero account id")
	}

	accounts, err := ListAccountsByUser(database, 111)
	if err != nil {
		t.Fatalf("ListAccountsByUser returned error: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
	if accounts[0].Email != "user@gmail.com" {
		t.Errorf("unexpected email: %q", accounts[0].Email)
	}
	if !accounts[0].Enabled {
		t.Error("expected account to be enabled by default")
	}
	if accounts[0].AuthType != AuthTypePassword {
		t.Errorf("expected default AuthType=password, got %q", accounts[0].AuthType)
	}
	if accounts[0].Protocol != "imap" {
		t.Errorf("expected default Protocol=imap, got %q", accounts[0].Protocol)
	}

	other, err := ListAccountsByUser(database, 999)
	if err != nil {
		t.Fatalf("ListAccountsByUser returned error: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("expected 0 accounts for other user, got %d", len(other))
	}
}

func TestGetAccountByIDNotFound(t *testing.T) {
	database := openTestDB(t)
	if _, err := GetAccountByID(database, 12345); err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestDeleteAccountCascadesMailState(t *testing.T) {
	database := openTestDB(t)

	id, err := InsertAccount(database, &Account{
		TelegramUserID:    111,
		Label:             "test",
		Email:             "a@b.com",
		IMAPHost:          "imap.b.com",
		IMAPPort:          993,
		IMAPUsername:      "a@b.com",
		EncryptedPassword: "blob",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	if err := SaveMailState(database, &MailState{AccountID: id, Folder: "INBOX", UIDValidity: 1, LastUID: 42}); err != nil {
		t.Fatalf("SaveMailState returned error: %v", err)
	}

	if err := DeleteAccount(database, id); err != nil {
		t.Fatalf("DeleteAccount returned error: %v", err)
	}

	state, err := GetMailState(database, id, "INBOX")
	if err != nil {
		t.Fatalf("GetMailState returned error: %v", err)
	}
	if state.LastUID != 0 {
		t.Errorf("expected mail_state to be cascade-deleted, got LastUID=%d", state.LastUID)
	}
}

func TestMailStateUpsertAndUIDValidityReset(t *testing.T) {
	database := openTestDB(t)

	id, err := InsertAccount(database, &Account{
		TelegramUserID:    111,
		Label:             "test",
		Email:             "a@b.com",
		IMAPHost:          "imap.b.com",
		IMAPPort:          993,
		IMAPUsername:      "a@b.com",
		EncryptedPassword: "blob",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	state, err := GetMailState(database, id, "INBOX")
	if err != nil {
		t.Fatalf("GetMailState returned error: %v", err)
	}
	if state.UIDValidity != 0 || state.LastUID != 0 {
		t.Fatalf("expected zero-value state for new account, got %+v", state)
	}

	state.UIDValidity = 100
	state.LastUID = 50
	if err := SaveMailState(database, state); err != nil {
		t.Fatalf("SaveMailState returned error: %v", err)
	}

	reloaded, err := GetMailState(database, id, "INBOX")
	if err != nil {
		t.Fatalf("GetMailState returned error: %v", err)
	}
	if reloaded.UIDValidity != 100 || reloaded.LastUID != 50 {
		t.Fatalf("unexpected state after save: %+v", reloaded)
	}

	// 模拟 UIDVALIDITY 变化：调用方应重置 LastUID 为 0 再保存。
	reloaded.UIDValidity = 200
	reloaded.LastUID = 0
	if err := SaveMailState(database, reloaded); err != nil {
		t.Fatalf("SaveMailState returned error: %v", err)
	}

	final, err := GetMailState(database, id, "INBOX")
	if err != nil {
		t.Fatalf("GetMailState returned error: %v", err)
	}
	if final.UIDValidity != 200 || final.LastUID != 0 {
		t.Fatalf("unexpected state after uid_validity reset: %+v", final)
	}
}
