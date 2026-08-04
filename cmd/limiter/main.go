package main

import (
	"context"
	"fmt"
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
	os.Exit(run())
}

func groupingMode(cfg *config.Config) string {
	switch {
	case cfg.ASNGrouping:
		return "asn"
	case cfg.SubnetGrouping:
		return fmt.Sprintf("subnet/%d", cfg.SubnetPrefixV4)
	default:
		return "ip"
	}
}

func run() int {
	logger := newLogger()

	logger.Infof("Remnawave Limiter v%s", version.Version)

	cfg, err := config.LoadConfig("")
	if err != nil {
		logger.Errorf("Ошибка конфигурации: %v", err)
		return 1
	}

	applyLogSettings(logger, cfg)
	i18n.SetLanguage(cfg.Language)

	var resolver geoip.Resolver = geoip.NopResolver{}
	var asnDB *geoip.DBResolver
	var maxmindLoaded bool

	dbPath := cfg.ASNDatabasePath
	_, statErr := os.Stat(dbPath)
	switch {
	case cfg.MaxMindLicenseKey != "":
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			logger.Errorf("Не удалось создать директорию для базы ASN %s: %v", filepath.Dir(dbPath), err)
			return 1
		}
		if os.IsNotExist(statErr) {
			logger.Infof("Файл базы ASN %s не найден, скачиваю через MaxMind...", dbPath)
			dl := &geoip.Downloader{
				LicenseKey: cfg.MaxMindLicenseKey,
				Validate:   geoip.DefaultValidate,
			}
			bootstrapCtx, cancelBootstrap := context.WithTimeout(context.Background(), 5*time.Minute)
			err := dl.Download(bootstrapCtx, dbPath)
			cancelBootstrap()
			if err != nil {
				logger.Errorf("Ошибка загрузки базы ASN: %v", err)
				return 1
			}
			logger.Info("База ASN успешно загружена")
		}
		db, err := geoip.NewDBResolver(dbPath)
		if err != nil {
			logger.Errorf("Ошибка открытия базы ASN: %v", err)
			return 1
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
		logger.Errorf("Ошибка Redis: %v", err)
		return 1
	}
	defer redisCache.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := redisCache.Ping(ctx); err != nil {
		logger.Errorf("Redis недоступен: %v", err)
		return 1
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
			applyLogSettings(logger, cfg)
			logger.Infof("Применены сохранённые настройки из бота: %d параметр(ов)", len(overrides))
		}
	}

	cfgProvider := config.NewProvider(cfg)
	cfgProvider.Watch(func(c *config.Config) { applyLogSettings(logger, c) })

	startupFields := logrus.Fields{
		"mode":     cfg.ActionMode,
		"interval": fmt.Sprintf("%ds", cfg.CheckInterval),
		"api":      cfg.RemnawaveAPIURL,
		"grouping": groupingMode(cfg),
	}
	if len(cfg.IgnoredNodeUUIDs) > 0 {
		startupFields["ignoredNodes"] = len(cfg.IgnoredNodeUUIDs)
	}
	logger.WithFields(startupFields).Info("Конфигурация")

	if err := redisCache.InitWhitelist(ctx, cfg.WhitelistUserIDs); err != nil {
		logger.WithError(err).Error("Не удалось записать WHITELIST_USER_IDS в Redis — эти пользователи не будут игнорироваться")
	}

	apiClient := api.NewClient(cfg.RemnawaveAPIURL, cfg.RemnawaveAPIToken)
	apiClient.SetLogger(logger)

	if cfg.RemnawaveCookies != "" {
		cookies := api.ParseCookies(cfg.RemnawaveCookies)
		apiClient.SetCookies(cookies)
		logger.Infof("Cookie авторизация включена (%d)", len(cookies))
	}

	if cfg.RemnawaveHeaders != "" {
		headers := api.ParseHeaders(cfg.RemnawaveHeaders)
		apiClient.SetHeaders(headers)
		logger.Infof("Кастомные заголовки включены (%d)", len(headers))
	}

	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramChatID, cfg.TelegramThreadID, cfg.TelegramAdminIDs, cfg.TelegramProxy, logger)
	if err != nil {
		logger.Errorf("Ошибка Telegram: %v", err)
		return 1
	}
	logger.Info("Telegram бот подключён")

	var webhookClient *webhook.Client
	if cfg.WebhookURL != "" {
		webhookClient = webhook.NewClient(cfg.WebhookURL, cfg.WebhookSecret, logger)
		logger.Info("Webhook включён")
	}

	mon, err := monitor.New(cfgProvider, apiClient, redisCache, bot, webhookClient, resolver, logger)
	if err != nil {
		logger.Errorf("Ошибка монитора: %v", err)
		return 1
	}

	settingsMgr := settings.NewManager(cfgProvider, redisCache, "", appliedOverrides)
	bot.SetSettingsProvider(settingsMgr)
	bot.SetStatsHandler(mon.StatsText)

	bot.SetActionHandler(func(ctx context.Context, action string, userID int64) error {
		switch action {
		case "drop":
			return apiClient.DropConnections(ctx, []int64{userID})
		case "disable":
			return apiClient.DisableUser(ctx, userID)
		case "disable_temp":
			if err := apiClient.DisableUser(ctx, userID); err != nil {
				return err
			}
			if dur := cfgProvider.Load().AutoDisableDuration; dur > 0 {
				duration := time.Duration(dur) * time.Minute
				if err := redisCache.SetRestoreTimer(ctx, userID, duration); err != nil {
					logger.WithError(err).WithField("userID", userID).Error("Ошибка установки таймера восстановления (manual disable_temp)")
					return err
				}
			}
			return nil
		case "enable":
			return apiClient.EnableUser(ctx, userID)
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
	if cfg.ActiveIPWindow < 2*cfg.CheckInterval {
		logger.Warnf("ACTIVE_IP_WINDOW (%dс) меньше двух интервалов проверки (%dс) — часть подключений может не попасть в подсчёт",
			cfg.ActiveIPWindow, 2*cfg.CheckInterval)
	}

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.HealthAddr != "" {
		startHealthServer(sigCtx, cfg.HealthAddr, mon, cfgProvider, logger)
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
	go func() {
		if err := bot.SendStartupMessage(sigCtx, startupMsg); err != nil {
			logger.WithError(err).Warn("Не удалось отправить стартовое сообщение в Telegram")
		}
	}()

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
	return 0
}
