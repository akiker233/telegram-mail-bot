package db

import (
	"testing"
	"time"
)

func TestUpdateOAuthTokensWithNewRefreshToken(t *testing.T) {
	database := openTestDB(t)

	expiry := time.Now().Add(time.Hour).Truncate(time.Second).UTC()
	id, err := InsertAccount(database, &Account{
		TelegramUserID:    111,
		Label:             "gmail-oauth",
		Email:             "user@gmail.com",
		IMAPHost:          "imap.gmail.com",
		IMAPPort:          993,
		IMAPUsername:      "user@gmail.com",
		AuthType:          AuthTypeOAuth,
		OAuthProvider:     "gmail",
		OAuthRefreshToken: "encrypted-refresh-1",
		OAuthAccessToken:  "encrypted-access-1",
		OAuthTokenExpiry:  expiry,
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	account, err := GetAccountByID(database, id)
	if err != nil {
		t.Fatalf("GetAccountByID returned error: %v", err)
	}
	if account.AuthType != AuthTypeOAuth || account.OAuthProvider != "gmail" {
		t.Fatalf("unexpected auth fields: %+v", account)
	}
	if !account.OAuthTokenExpiry.Equal(expiry) {
		t.Fatalf("expected expiry %v, got %v", expiry, account.OAuthTokenExpiry)
	}

	newExpiry := time.Now().Add(2 * time.Hour).Truncate(time.Second).UTC()
	if err := UpdateOAuthTokens(database, id, "encrypted-access-2", "encrypted-refresh-2", newExpiry); err != nil {
		t.Fatalf("UpdateOAuthTokens returned error: %v", err)
	}

	updated, err := GetAccountByID(database, id)
	if err != nil {
		t.Fatalf("GetAccountByID returned error: %v", err)
	}
	if updated.OAuthAccessToken != "encrypted-access-2" {
		t.Errorf("expected access token to be updated, got %q", updated.OAuthAccessToken)
	}
	if updated.OAuthRefreshToken != "encrypted-refresh-2" {
		t.Errorf("expected refresh token to be updated, got %q", updated.OAuthRefreshToken)
	}
	if !updated.OAuthTokenExpiry.Equal(newExpiry) {
		t.Errorf("expected expiry %v, got %v", newExpiry, updated.OAuthTokenExpiry)
	}
}

func TestUpdateOAuthTokensKeepsRefreshTokenWhenEmpty(t *testing.T) {
	database := openTestDB(t)

	id, err := InsertAccount(database, &Account{
		TelegramUserID:    111,
		Label:             "outlook-oauth",
		Email:             "user@outlook.com",
		IMAPHost:          "outlook.office365.com",
		IMAPPort:          993,
		IMAPUsername:      "user@outlook.com",
		AuthType:          AuthTypeOAuth,
		OAuthProvider:     "outlook",
		OAuthRefreshToken: "encrypted-refresh-original",
		OAuthAccessToken:  "encrypted-access-original",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	newExpiry := time.Now().Add(time.Hour).Truncate(time.Second).UTC()
	// 传空字符串模拟 Microsoft 某次刷新没有返回新 refresh_token 的情况。
	if err := UpdateOAuthTokens(database, id, "encrypted-access-updated", "", newExpiry); err != nil {
		t.Fatalf("UpdateOAuthTokens returned error: %v", err)
	}

	updated, err := GetAccountByID(database, id)
	if err != nil {
		t.Fatalf("GetAccountByID returned error: %v", err)
	}
	if updated.OAuthAccessToken != "encrypted-access-updated" {
		t.Errorf("expected access token to be updated, got %q", updated.OAuthAccessToken)
	}
	if updated.OAuthRefreshToken != "encrypted-refresh-original" {
		t.Errorf("expected refresh token to remain unchanged, got %q", updated.OAuthRefreshToken)
	}
}
