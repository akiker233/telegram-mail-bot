package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestBuildEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		apiURL string
		want   string
	}{
		{
			name:   "empty uses default",
			apiURL: "",
			want:   tgbotapi.APIEndpoint,
		},
		{
			name:   "custom without trailing slash",
			apiURL: "https://api.example.com",
			want:   "https://api.example.com/bot%s/%s",
		},
		{
			name:   "custom with trailing slash",
			apiURL: "https://api.example.com/",
			want:   "https://api.example.com/bot%s/%s",
		},
		{
			name:   "custom with path",
			apiURL: "https://api.example.com/v1/",
			want:   "https://api.example.com/v1/bot%s/%s",
		},
		{
			name:   "custom with multiple trailing slashes",
			apiURL: "https://api.example.com//",
			want:   "https://api.example.com/bot%s/%s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildEndpoint(tt.apiURL); got != tt.want {
				t.Errorf("buildEndpoint(%q) = %q, want %q", tt.apiURL, got, tt.want)
			}
		})
	}
}
