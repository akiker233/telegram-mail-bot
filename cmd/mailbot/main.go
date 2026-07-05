package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/oauth2"

	"telegram-mail-bot/internal/config"
	"telegram-mail-bot/internal/crypto"
	"telegram-mail-bot/internal/db"
	"telegram-mail-bot/internal/manager"
	"telegram-mail-bot/internal/telegram"
	"telegram-mail-bot/internal/update"
)

// repoURL 是项目仓库地址，仅用于启动日志展示。
const repoURL = "https://github.com/akiker233/telegram-mail-bot"

// version 由发布流程通过 -ldflags "-X main.version=..." 注入，本地开发编译时为空字符串。
var version string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		if err := config.RunReconfigure(); err != nil {
			slog.Error("重新配置失败", "err", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "update" {
		if err := update.Run(version); err != nil {
			slog.Error("更新失败", "err", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := config.RunInteractiveSetupIfNeeded(); err != nil {
		slog.Error("初始化配置失败", "err", err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("加载配置失败", "err", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		slog.Error("打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	encryptionKey := crypto.DeriveKey(cfg.MasterKey)

	// bot 在 mgr 之前声明，send 闭包延迟到 mgr 创建时才被调用，此时 bot 已完成初始化。
	var bot *telegram.Bot
	send := func(telegramUserID int64, text string, parseMode string) {
		bot.Send(telegramUserID, text, parseMode)
	}
	oauthConfigs := cfg.OAuthConfigs()

	logStartupInfo(cfg.DBPath, oauthConfigs)

	mgr := manager.New(database, encryptionKey, send, oauthConfigs)

	bot, err = telegram.New(cfg.TelegramBotToken, database, mgr, cfg.AllowedUsers, encryptionKey, oauthConfigs, version)
	if err != nil {
		slog.Error("初始化 Telegram 机器人失败", "err", err)
		os.Exit(1)
	}

	// 恢复进程重启前未完成的 /addaccount 会话。
	bot.RestoreSessions()

	if err := mgr.StartAll(ctx); err != nil {
		slog.Error("恢复邮箱监听失败", "err", err)
		os.Exit(1)
	}

	slog.Info("机器人已启动")
	bot.Run(ctx)
	slog.Info("机器人已退出")
}

// logStartupInfo 打印启动时的关键信息，方便排查部署问题。
func logStartupInfo(dbPath string, oauthConfigs map[string]oauth2.Config) {
	slog.Info("仓库地址: " + repoURL)
	if version == "" {
		slog.Info("版本: 开发版本")
	} else {
		slog.Info("版本: " + version)
	}
	slog.Info("数据库路径: " + dbPath)

	if len(oauthConfigs) == 0 {
		slog.Info("OAuth: 未配置")
		return
	}
	providers := make([]string, 0, len(oauthConfigs))
	for provider := range oauthConfigs {
		providers = append(providers, provider)
	}
	slog.Info("OAuth: 已加载 " + strings.Join(providers, ", "))
}
