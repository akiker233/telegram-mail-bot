package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"telegram-mail-bot/internal/crypto"
	"telegram-mail-bot/internal/db"
)

func TestRefreshIfNeededReusesValidToken(t *testing.T) {
	database, key := newTestDBAndKey(t)

	encryptedAccess, err := crypto.Encrypt(key, "still-valid-access-token")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	account := &db.Account{
		ID:               1,
		OAuthAccessToken: encryptedAccess,
		OAuthTokenExpiry: time.Now().Add(time.Hour),
	}

	// cfg 里的端点故意留空/指向一个不存在的地址：不过期的 token 不应该发起任何网络请求。
	cfg := oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: "http://127.0.0.1:0/should-not-be-called"}}

	token, err := RefreshIfNeeded(context.Background(), database, key, cfg, account)
	if err != nil {
		t.Fatalf("RefreshIfNeeded returned error: %v", err)
	}
	if token != "still-valid-access-token" {
		t.Errorf("expected cached token to be reused, got %q", token)
	}
}

func TestRefreshIfNeededTreatsNearExpiryAsExpired(t *testing.T) {
	database, key := newTestDBAndKey(t)

	server := newTokenServer(t, tokenServerResponse{
		AccessToken: "refreshed-access-token",
		ExpiresIn:   3600,
	})
	defer server.Close()

	encryptedRefresh, err := crypto.Encrypt(key, "original-refresh-token")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	id, err := db.InsertAccount(database, &db.Account{
		TelegramUserID:    1,
		Label:             "test",
		Email:             "a@b.com",
		IMAPHost:          "imap.b.com",
		IMAPPort:          993,
		IMAPUsername:      "a@b.com",
		AuthType:          "oauth",
		OAuthRefreshToken: encryptedRefresh,
		// 在 expiryMargin (2分钟) 之内，应被当作已过期处理。
		OAuthTokenExpiry: time.Now().Add(30 * time.Second),
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}
	account, err := db.GetAccountByID(database, id)
	if err != nil {
		t.Fatalf("GetAccountByID returned error: %v", err)
	}

	cfg := oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: server.URL}}

	token, err := RefreshIfNeeded(context.Background(), database, key, cfg, account)
	if err != nil {
		t.Fatalf("RefreshIfNeeded returned error: %v", err)
	}
	if token != "refreshed-access-token" {
		t.Errorf("expected refreshed token, got %q", token)
	}

	updated, err := db.GetAccountByID(database, id)
	if err != nil {
		t.Fatalf("GetAccountByID returned error: %v", err)
	}
	decryptedAccess, err := crypto.Decrypt(key, updated.OAuthAccessToken)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if decryptedAccess != "refreshed-access-token" {
		t.Errorf("expected refreshed access token to be persisted, got %q", decryptedAccess)
	}
}

func TestRefreshIfNeededPersistsRotatedRefreshToken(t *testing.T) {
	database, key := newTestDBAndKey(t)

	server := newTokenServer(t, tokenServerResponse{
		AccessToken:  "new-access-token",
		RefreshToken: "rotated-refresh-token",
		ExpiresIn:    3600,
	})
	defer server.Close()

	encryptedRefresh, err := crypto.Encrypt(key, "original-refresh-token")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	id, err := db.InsertAccount(database, &db.Account{
		TelegramUserID:    1,
		Label:             "test",
		Email:             "a@b.com",
		IMAPHost:          "imap.b.com",
		IMAPPort:          993,
		IMAPUsername:      "a@b.com",
		AuthType:          "oauth",
		OAuthRefreshToken: encryptedRefresh,
		OAuthTokenExpiry:  time.Now().Add(-time.Minute), // 已过期
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}
	account, err := db.GetAccountByID(database, id)
	if err != nil {
		t.Fatalf("GetAccountByID returned error: %v", err)
	}

	cfg := oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: server.URL}}

	if _, err := RefreshIfNeeded(context.Background(), database, key, cfg, account); err != nil {
		t.Fatalf("RefreshIfNeeded returned error: %v", err)
	}

	updated, err := db.GetAccountByID(database, id)
	if err != nil {
		t.Fatalf("GetAccountByID returned error: %v", err)
	}
	decryptedRefresh, err := crypto.Decrypt(key, updated.OAuthRefreshToken)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if decryptedRefresh != "rotated-refresh-token" {
		t.Errorf("expected rotated refresh token to be persisted, got %q", decryptedRefresh)
	}
}

func newTestDBAndKey(t *testing.T) (*sql.DB, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open returned error: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database, crypto.DeriveKey("test-master-key")
}

type tokenServerResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type,omitempty"`
}

func newTokenServer(t *testing.T, resp tokenServerResponse) *httptest.Server {
	t.Helper()
	if resp.TokenType == "" {
		resp.TokenType = "Bearer"
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}
