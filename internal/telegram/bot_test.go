package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestBuildEndpoint(t *testing.T) {
	if got := buildEndpoint(""); got != tgbotapi.APIEndpoint {
		t.Errorf("empty apiURL should use default endpoint, got %q", got)
	}
	if got := buildEndpoint("https://api.example.com"); got != "https://api.example.com/bot%s/%s" {
		t.Errorf("unexpected endpoint: %q", got)
	}
	if got := buildEndpoint("https://api.example.com/"); got != "https://api.example.com/bot%s/%s" {
		t.Errorf("trailing slash should be trimmed, got %q", got)
	}
}
