package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/i18n"
	"github.com/remnawave/limiter/internal/monitor"
	"github.com/remnawave/limiter/internal/telegram"
	"github.com/remnawave/limiter/internal/version"
)

func main() {
	logger := logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	logger.Infof("Remnawave Limiter v%s", version.Version)

	cfg, err := config.LoadConfig("")
	if err != nil {
		logger.Fatalf("Ошибка конфигурации: %v", err)
	}

	i18n.SetLanguage(cfg.Language)

	logger.Infof("Режим: %s", cfg.ActionMode)
	logger.Infof("Интервал проверки: %dс", cfg.CheckInterval)
	logger.Infof("API: %s", cfg.RemnawaveAPIURL)

	redisCache, err := cache.New(cfg.RedisURL)
	if err != nil {
		logger.Fatalf("Ошибка Redis: %v", err)
	}
	defer redisCache.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := redisCache.Ping(ctx); err != nil {
		logger.Fatalf("Redis недоступен: %v", err)
	}
	logger.Info("Redis подключён")

	redisCache.InitWhitelist(ctx, cfg.WhitelistUserIDs)

	apiClient := api.NewClient(cfg.RemnawaveAPIURL, cfg.RemnawaveAPIToken)
	apiClient.SetLogger(logger)

	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramChatID, cfg.TelegramThreadID, cfg.TelegramAdminIDs, logger)
	if err != nil {
		logger.Fatalf("Ошибка Telegram: %v", err)
	}
	logger.Info("Telegram бот подключён")

	mon, err := monitor.New(cfg, apiClient, redisCache, bot, logger)
	if err != nil {
		logger.Fatalf("Ошибка монитора: %v", err)
	}

	bot.SetActionHandler(func(ctx context.Context, action, userUUID, userID string) error {
		switch action {
		case "drop":
			return apiClient.DropConnections(ctx, []string{userUUID})
		case "disable":
			return apiClient.DisableUser(ctx, userUUID)
		case "disable_temp":
			if err := apiClient.DisableUser(ctx, userUUID); err != nil {
				return err
			}
			if cfg.AutoDisableDuration > 0 {
				duration := time.Duration(cfg.AutoDisableDuration) * time.Minute
				if err := redisCache.SetRestoreTimer(ctx, userUUID, duration); err != nil {
					logger.WithError(err).WithField("uuid", userUUID).Error("Ошибка установки таймера восстановления (manual disable_temp)")
				}
			}
			return nil
		case "enable":
			return apiClient.EnableUser(ctx, userUUID)
		case "ignore":
			return redisCache.AddToWhitelist(ctx, userID)
		}
		return nil
	})

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go bot.StartPolling(sigCtx)
	mon.Run(sigCtx)

	logger.Info("Remnawave Limiter остановлен")
}
