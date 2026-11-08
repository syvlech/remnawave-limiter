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
	i18n.SetLanguage(os.Getenv("LANGUAGE"))
	logger := newLogger()

	logger.Infof(i18n.T("log.application_version"), version.Version)

	cfg, err := config.LoadConfig("")
	if err != nil {
		i18n.SetLanguage(os.Getenv("LANGUAGE"))
		logger.Errorf(i18n.T("log.config_failed"), err)
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
			logger.Errorf(i18n.T("log.asn_mkdir_failed"), filepath.Dir(dbPath), err)
			return 1
		}
		if os.IsNotExist(statErr) {
			logger.Infof(i18n.T("log.asn_downloading"), dbPath)
			dl := &geoip.Downloader{
				LicenseKey: cfg.MaxMindLicenseKey,
				Validate:   geoip.DefaultValidate,
			}
			bootstrapCtx, cancelBootstrap := context.WithTimeout(context.Background(), 5*time.Minute)
			err := dl.Download(bootstrapCtx, dbPath)
			cancelBootstrap()
			if err != nil {
				logger.Errorf(i18n.T("log.asn_download_failed"), err)
				return 1
			}
			logger.Info(i18n.T("log.asn_downloaded"))
		}
		db, err := geoip.NewDBResolver(dbPath)
		if err != nil {
			logger.Errorf(i18n.T("log.asn_open_failed"), err)
			return 1
		}
		defer db.Close()
		asnDB = db
		resolver = db
		maxmindLoaded = true
		logger.Infof(i18n.T("log.asn_enabled"), dbPath)
	case statErr == nil:
		db, err := geoip.NewDBResolver(dbPath)
		if err != nil {
			logger.Warnf(i18n.T("log.asn_open_disabled"), dbPath, err)
		} else {
			defer db.Close()
			asnDB = db
			resolver = db
			maxmindLoaded = true
			logger.Infof(i18n.T("log.asn_enabled_no_update"), dbPath)
		}
	default:
		logger.Info(i18n.T("log.asn_disabled"))
	}

	redisCache, err := cache.New(cfg.RedisURL)
	if err != nil {
		logger.Errorf(i18n.T("log.redis_failed"), err)
		return 1
	}
	defer redisCache.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := redisCache.Ping(ctx); err != nil {
		logger.Errorf(i18n.T("log.redis_unavailable"), err)
		return 1
	}
	logger.Info(i18n.T("log.redis_connected"))

	appliedOverrides := map[string]string{}
	if overrides, err := redisCache.GetConfigOverrides(ctx); err != nil {
		logger.WithError(err).Warn(i18n.T("log.settings_load_failed"))
	} else if len(overrides) > 0 {
		merged, applied, rejected, err := config.MergeSavedOverrides("", overrides)
		if err != nil {
			logger.WithError(err).Error(i18n.T("log.settings_apply_failed"))
		} else {
			for key, reason := range rejected {
				logger.WithError(reason).WithFields(logrus.Fields{
					"key":   key,
					"value": overrides[key],
				}).Warn(i18n.T("log.settings_invalid_removed"))
				if err := redisCache.DeleteConfigOverride(ctx, key); err != nil {
					logger.WithError(err).WithField("key", key).Warn(i18n.T("log.settings_delete_failed"))
				}
			}
			cfg = merged
			appliedOverrides = applied
			applyLogSettings(logger, cfg)
			if len(applied) > 0 {
				logger.Infof(i18n.T("log.settings_applied"), len(applied))
			}
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
	logger.WithFields(startupFields).Info(i18n.T("log.configuration"))

	if err := redisCache.InitWhitelist(ctx, cfg.WhitelistUserIDs); err != nil {
		logger.WithError(err).Error(i18n.T("log.whitelist_init_failed"))
	}

	apiClient := api.NewClient(cfg.RemnawaveAPIURL, cfg.RemnawaveAPIToken)
	apiClient.SetLogger(logger)

	if cfg.RemnawaveCookies != "" {
		cookies := api.ParseCookies(cfg.RemnawaveCookies)
		apiClient.SetCookies(cookies)
		logger.Infof(i18n.T("log.cookies_enabled"), len(cookies))
	}

	if cfg.RemnawaveHeaders != "" {
		headers := api.ParseHeaders(cfg.RemnawaveHeaders)
		apiClient.SetHeaders(headers)
		logger.Infof(i18n.T("log.headers_enabled"), len(headers))
	}

	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramChatID, cfg.TelegramThreadID, cfg.TelegramAdminIDs, cfg.TelegramProxy, logger)
	if err != nil {
		logger.Errorf(i18n.T("log.telegram_failed"), err)
		return 1
	}
	logger.Info(i18n.T("log.telegram_connected"))

	var webhookClient *webhook.Client
	if cfg.WebhookURL != "" {
		webhookClient = webhook.NewClient(cfg.WebhookURL, cfg.WebhookSecret, logger)
		webhookClient.SetRawSecretHeader(cfg.WebhookSecretHeader)
		logger.Info(i18n.T("log.webhook_enabled"))
	}

	mon, err := monitor.New(cfgProvider, apiClient, redisCache, bot, webhookClient, resolver, logger)
	if err != nil {
		logger.Errorf(i18n.T("log.monitor_failed"), err)
		return 1
	}

	settingsMgr := settings.NewManager(cfgProvider, redisCache, "", appliedOverrides)
	bot.SetSettingsProvider(settingsMgr)
	bot.SetStatsHandler(mon.StatsText)

	bot.SetActionHandler(newActionHandler(apiClient, redisCache, cfgProvider, logger))

	if cfg.ASNGrouping && cfg.SubnetGrouping {
		logger.Warn(i18n.T("log.grouping_priority"))
	}
	if cfg.ASNGrouping && !maxmindLoaded {
		logger.Warn(i18n.T("log.asn_missing"))
	}
	if cfg.ActiveIPWindow < 2*cfg.CheckInterval {
		logger.Warnf(i18n.T("log.active_window_short"),
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
			logger.WithError(err).Warn(i18n.T("log.startup_message_failed"))
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
		logger.Infof(i18n.T("log.asn_update_enabled"), cfg.MaxMindUpdateInterval)
	}

	go bot.StartPolling(sigCtx)
	mon.Run(sigCtx)

	logger.Info(i18n.T("log.application_stopped"))
	return 0
}
