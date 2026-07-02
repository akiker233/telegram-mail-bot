package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"golang.org/x/oauth2"

	"telegram-mail-bot/internal/oauth"
)

// Config 保存机器人启动所需的全部配置。
type Config struct {
	TelegramBotToken string
	MasterKey        string
	AllowedUsers     map[int64]bool
	DBPath           string

	// OAuth Client ID/Secret 均可选。为空表示不启用该 provider 的 OAuth 登录，
	// /addaccount 流程中不会出现对应选项。
	GmailOAuthClientID       string
	GmailOAuthClientSecret   string
	OutlookOAuthClientID     string
	OutlookOAuthClientSecret string
}

// Load 从环境变量读取配置，缺失必填项时返回错误。
// 如果工作目录下存在 .env 文件，会先加载其中的键值对到环境变量；.env 不存在时静默跳过——
// 生产部署通常直接通过系统环境变量或容器编排工具注入配置，不依赖 .env 文件。
// 已经存在的系统环境变量优先级更高，不会被 .env 覆盖。
func Load() (*Config, error) {
	_ = godotenv.Load()

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("config: TELEGRAM_BOT_TOKEN is required")
	}

	masterKey := os.Getenv("MASTER_KEY")
	if masterKey == "" {
		return nil, fmt.Errorf("config: MASTER_KEY is required")
	}

	rawUsers := os.Getenv("ALLOWED_TELEGRAM_USERS")
	if rawUsers == "" {
		return nil, fmt.Errorf("config: ALLOWED_TELEGRAM_USERS is required")
	}
	allowedUsers, err := parseAllowedUsers(rawUsers)
	if err != nil {
		return nil, err
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./mailbot.db"
	}

	return &Config{
		TelegramBotToken:         token,
		MasterKey:                masterKey,
		AllowedUsers:             allowedUsers,
		DBPath:                   dbPath,
		GmailOAuthClientID:       os.Getenv("GMAIL_OAUTH_CLIENT_ID"),
		GmailOAuthClientSecret:   os.Getenv("GMAIL_OAUTH_CLIENT_SECRET"),
		OutlookOAuthClientID:     os.Getenv("OUTLOOK_OAUTH_CLIENT_ID"),
		OutlookOAuthClientSecret: os.Getenv("OUTLOOK_OAUTH_CLIENT_SECRET"),
	}, nil
}

// OAuthConfigs 返回按 provider 名称（"gmail"/"outlook"）索引的 oauth2.Config，
// 只包含已配置了 Client ID 的 provider。
func (c *Config) OAuthConfigs() map[string]oauth2.Config {
	configs := make(map[string]oauth2.Config)
	if c.GmailOAuthClientID != "" {
		configs[string(oauth.Gmail)] = oauth.Config(oauth.Gmail, c.GmailOAuthClientID, c.GmailOAuthClientSecret)
	}
	if c.OutlookOAuthClientID != "" {
		configs[string(oauth.Outlook)] = oauth.Config(oauth.Outlook, c.OutlookOAuthClientID, c.OutlookOAuthClientSecret)
	}
	return configs
}

func parseAllowedUsers(raw string) (map[int64]bool, error) {
	parts := strings.Split(raw, ",")
	users := make(map[int64]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("config: invalid ALLOWED_TELEGRAM_USERS entry %q: %w", p, err)
		}
		users[id] = true
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("config: ALLOWED_TELEGRAM_USERS must contain at least one user id")
	}
	return users, nil
}
