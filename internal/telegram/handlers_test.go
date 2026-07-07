package telegram

import (
	"context"
	"net/http"
	"testing"

	"golang.org/x/oauth2"
)

func TestOAuthContextInjectsClient(t *testing.T) {
	client := &http.Client{Timeout: 123}
	ctx := oauthContext(context.Background(), client)

	got, ok := ctx.Value(oauth2.HTTPClient).(*http.Client)
	if !ok {
		t.Fatalf("expected *http.Client in context, got %T", ctx.Value(oauth2.HTTPClient))
	}
	if got != client {
		t.Fatal("context did not contain the same client")
	}
}

func TestOAuthContextReturnsOriginalWhenClientNil(t *testing.T) {
	base := context.Background()
	ctx := oauthContext(base, nil)

	if ctx != base {
		t.Fatal("nil client should return the original context unchanged")
	}
}
