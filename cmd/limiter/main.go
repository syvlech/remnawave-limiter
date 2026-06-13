package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/geoip"
	"github.com/remnawave/limiter/internal/i18n"
	"github.com/remnawave/limiter/internal/monitor"
	"github.com/remnawave/limiter/internal/settings"
	"github.com/remnawave/limiter/internal/telegram"
	"github.com/remnawave/limiter/internal/version"
	"github.com/remnawave/limiter/internal/webhook"
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

	var resolver geoip.Resolver = geoip.NopResolver{}
	var asnDB *geoip.DBResolver
	var maxmindLoaded bool

	dbPath := cfg.ASNDatabasePath
	_, statErr := os.Stat(dbPath)
	switch {
	case cfg.MaxMindLicenseKey != "":
		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			logger.Fatalf("Не удалось создать директорию для базы ASN %s: %v", filepath.Dir(dbPath), err)
		}
		if os.IsNotExist(statErr) {
			logger.Infof("Файл базы ASN %s не найден, скачиваю через MaxMind...", dbPath)
			dl := &geoip.Downloader{
				LicenseKey: cfg.MaxMindLicenseKey,
				Validate:   geoip.DefaultValidate,
			}
			bootstrapCtx, cancelBootstrap := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := dl.Download(bootstrapCtx, dbPath); err != nil {
				cancelBootstrap()
				logger.Fatalf("Ошибка загрузки базы ASN: %v", err)
			}
			cancelBootstrap()
			logger.Info("База ASN успешно загружена")
		}
		db, err := geoip.NewDBResolver(dbPath)
		if err != nil {
			logger.Fatalf("Ошибка открытия базы ASN: %v", err)
		}
		defer db.Close()
		asnDB = db
		resolver = db
		maxmindLoaded = true
		logger.Infof("ASN enrichment включён, база: %s", dbPath)
	case statErr == nil:
		db, err := geoip.NewDBResolver(dbPath)
		if err != nil {
			logger.Warnf("Не удалось открыть базу ASN %s: %v. ASN enrichment отключён", dbPath, err)
		} else {
			defer db.Close()
			asnDB = db
			resolver = db
			maxmindLoaded = true
			logger.Infof("ASN enrichment включён (без auto-update, MAXMIND_LICENSE_KEY не задан), база: %s", dbPath)
		}
	default:
		logger.Info("ASN enrichment отключён: MAXMIND_LICENSE_KEY не задан и файл базы не найден")
	}

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

	appliedOverrides := map[string]string{}
	if overrides, err := redisCache.GetConfigOverrides(ctx); err != nil {
		logger.WithError(err).Warn("Не удалось загрузить сохранённые настройки из Redis, использую .env")
	} else if len(overrides) > 0 {
		if merged, err := config.LoadConfigWithOverrides("", overrides); err != nil {
			logger.WithError(err).Error("Сохранённые настройки невалидны, игнорирую их и использую .env")
		} else {
			cfg = merged
			appliedOverrides = overrides
			logger.Infof("Применены сохранённые настройки из бота: %d параметр(ов)", len(overrides))
		}
	}

	cfgProvider := config.NewProvider(cfg)

	logger.Infof("Режим: %s", cfg.ActionMode)
	logger.Infof("Интервал проверки: %dс", cfg.CheckInterval)
	logger.Infof("API: %s", cfg.RemnawaveAPIURL)
	if len(cfg.IgnoredNodeUUIDs) > 0 {
		logger.Infof("Игнорируемые ноды (%d): %v", len(cfg.IgnoredNodeUUIDs), cfg.IgnoredNodeUUIDs)
	}

	redisCache.InitWhitelist(ctx, cfg.WhitelistUserIDs)

	apiClient := api.NewClient(cfg.RemnawaveAPIURL, cfg.RemnawaveAPIToken)
	apiClient.SetLogger(logger)

	if cfg.RemnawaveCookies != "" {
		cookies := api.ParseCookies(cfg.RemnawaveCookies)
		apiClient.SetCookies(cookies)
		logger.Info("Cookie авторизация включена")
	}

	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramChatID, cfg.TelegramThreadID, cfg.TelegramAdminIDs, cfg.TelegramProxy, logger)
	if err != nil {
		logger.Fatalf("Ошибка Telegram: %v", err)
	}
	logger.Info("Telegram бот подключён")

	var webhookClient *webhook.Client
	if cfg.WebhookURL != "" {
		webhookClient = webhook.NewClient(cfg.WebhookURL, cfg.WebhookSecret, logger)
		logger.Info("Webhook включён")
	}

	mon, err := monitor.New(cfgProvider, apiClient, redisCache, bot, webhookClient, resolver, logger)
	if err != nil {
		logger.Fatalf("Ошибка монитора: %v", err)
	}

	settingsMgr := settings.NewManager(cfgProvider, redisCache, "", appliedOverrides)
	bot.SetSettingsProvider(settingsMgr)
	bot.SetStatsHandler(mon.StatsText)

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
			if dur := cfgProvider.Load().AutoDisableDuration; dur > 0 {
				duration := time.Duration(dur) * time.Minute
				if err := redisCache.SetRestoreTimer(ctx, userUUID, duration); err != nil {
					logger.WithError(err).WithField("uuid", userUUID).Error("Ошибка установки таймера восстановления (manual disable_temp)")
				}
			}
			return nil
		case "enable":
			return apiClient.EnableUser(ctx, userUUID)
		case "ignore":
			return redisCache.AddToWhitelist(ctx, userID)
		case "ignore_temp":
			ttl := time.Duration(cfgProvider.Load().IgnoreDuration) * time.Minute
			return redisCache.AddToWhitelistTemp(ctx, userID, ttl)
		}
		return nil
	})

	if cfg.ASNGrouping && cfg.SubnetGrouping {
		logger.Warn("Включены оба режима ASN_GROUPING и SUBNET_GROUPING — приоритет у ASN, подсети будут игнорироваться")
	}
	if cfg.ASNGrouping && !maxmindLoaded {
		logger.Warn("ASN_GROUPING включён, но MaxMind ASN база не загружена — все IP без ASN будут считаться отдельными группами")
	}

	startupMsg := telegram.FormatStartupMessage(
		version.Version,
		cfg.ActionMode,
		cfg.CheckInterval,
		cfg.Cooldown,
		cfg.Tolerance,
		cfg.ToleranceMultiplier,
		cfg.DefaultDeviceLimit,
		cfg.AutoDisableDuration,
		cfg.AutoNotifySoft,
		cfg.WebhookURL != "",
		cfg.SubnetGrouping,
		cfg.SubnetPrefixV4,
		cfg.ASNGrouping,
		maxmindLoaded,
		cfg.ViolationThreshold,
		cfg.ViolationThresholdWindow,
	)
	if err := bot.SendStartupMessage(startupMsg); err != nil {
		logger.WithError(err).Warn("Не удалось отправить стартовое сообщение в Telegram")
	}

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.HealthAddr != "" {
		startHealthServer(sigCtx, cfg.HealthAddr, mon, cfgProvider, logger)
	}

	if asnDB != nil && cfg.MaxMindLicenseKey != "" {
		updater := &geoip.Updater{
			Downloader: &geoip.Downloader{
				LicenseKey: cfg.MaxMindLicenseKey,
				Validate:   geoip.DefaultValidate,
			},
			Reloader: asnDB,
			DstPath:  cfg.ASNDatabasePath,
			Interval: cfg.MaxMindUpdateInterval,
			Logger:   logger,
		}
		go updater.Run(sigCtx)
		logger.Infof("Авто-обновление базы ASN включено, интервал: %v", cfg.MaxMindUpdateInterval)
	}

	go bot.StartPolling(sigCtx)
	mon.Run(sigCtx)

	logger.Info("Remnawave Limiter остановлен")
}
