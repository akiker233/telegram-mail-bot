package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"telegram-mail-bot/internal/config"
	"telegram-mail-bot/internal/crypto"
	"telegram-mail-bot/internal/db"
	"telegram-mail-bot/internal/manager"
	"telegram-mail-bot/internal/telegram"
	"telegram-mail-bot/internal/update"
)

// version 由发布流程通过 -ldflags "-X main.version=..." 注入，本地开发编译时为空字符串。
var version string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		if err := config.RunReconfigure(); err != nil {
			log.Fatalf("重新配置失败: %v", err)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "update" {
		if err := update.Run(version); err != nil {
			log.Fatalf("更新失败: %v", err)
		}
		os.Exit(0)
	}

	if err := config.RunInteractiveSetupIfNeeded(); err != nil {
		log.Fatalf("初始化配置失败: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
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
	mgr := manager.New(database, encryptionKey, send, oauthConfigs)

	bot, err = telegram.New(cfg.TelegramBotToken, database, mgr, cfg.AllowedUsers, encryptionKey, oauthConfigs)
	if err != nil {
		log.Fatalf("初始化 Telegram 机器人失败: %v", err)
	}

	if err := mgr.StartAll(ctx); err != nil {
		log.Fatalf("恢复邮箱监听失败: %v", err)
	}

	log.Println("机器人已启动")
	bot.Run(ctx)
	log.Println("机器人已退出")
}
