package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

type Config struct {
	RemnawaveAPIURL     string
	RemnawaveAPIToken   string
	CheckInterval       int
	ActiveIPWindow      int
	Tolerance           int
	ToleranceMultiplier float64
	Cooldown            int
	UserCacheTTL        int
	DefaultDeviceLimit  int
	ActionMode          string
	AutoDisableDuration int
	IgnoreDuration      int
	TelegramBotToken    string
	TelegramChatID      int64
	TelegramThreadID    int64
	TelegramAdminIDs    []int64
	TelegramProxy       string
	WhitelistUserIDs    []string
	RedisURL            string
	Timezone            string
	Language            string
	RemnawaveCookies    string
	WebhookURL          string
	WebhookSecret       string
	SubnetGrouping           bool
	SubnetPrefixV4           int
	ASNGrouping              bool
	ASNDatabasePath          string
	MaxMindLicenseKey        string
	MaxMindUpdateInterval    time.Duration
	ViolationThreshold       int
	ViolationThresholdWindow int
	IgnoredNodeUUIDs         []string
}

func LoadConfig(envPath string) (*Config, error) {
	if envPath == "" {
		envPath = ".env"
	}

	if err := godotenv.Load(envPath); err != nil {
		logrus.Debug("Файл .env не найден, используются переменные окружения")
	}

	remnawaveAPIURL := os.Getenv("REMNAWAVE_API_URL")
	if remnawaveAPIURL == "" {
		return nil, fmt.Errorf("REMNAWAVE_API_URL обязательный параметр")
	}

	remnawaveAPIToken := os.Getenv("REMNAWAVE_API_TOKEN")
	if remnawaveAPIToken == "" {
		return nil, fmt.Errorf("REMNAWAVE_API_TOKEN обязательный параметр")
	}

	telegramBotToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN обязательный параметр")
	}

	telegramChatIDStr := os.Getenv("TELEGRAM_CHAT_ID")
	if telegramChatIDStr == "" {
		return nil, fmt.Errorf("TELEGRAM_CHAT_ID обязательный параметр")
	}
	telegramChatID, err := strconv.ParseInt(telegramChatIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("TELEGRAM_CHAT_ID должен быть числом: %v", err)
	}

	telegramAdminIDsStr := os.Getenv("TELEGRAM_ADMIN_IDS")
	if telegramAdminIDsStr == "" {
		return nil, fmt.Errorf("TELEGRAM_ADMIN_IDS обязательный параметр")
	}
	telegramAdminIDs, err := parseint64list(telegramAdminIDsStr)
	if err != nil {
		return nil, fmt.Errorf("TELEGRAM_ADMIN_IDS: %v", err)
	}

	telegramThreadID := getEnvInt64("TELEGRAM_THREAD_ID", 0)

	actionMode := getEnv("ACTION_MODE", "manual")

	maxmindInterval, err := getEnvDuration("MAXMIND_UPDATE_INTERVAL", 168*time.Hour)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		RemnawaveAPIURL:     remnawaveAPIURL,
		RemnawaveAPIToken:   remnawaveAPIToken,
		CheckInterval:       getEnvInt("CHECK_INTERVAL", 30),
		ActiveIPWindow:      getEnvInt("ACTIVE_IP_WINDOW", 300),
		Tolerance:           getEnvInt("TOLERANCE", 0),
		ToleranceMultiplier: getEnvFloat64("TOLERANCE_MULTIPLIER", 0),
		Cooldown:            getEnvInt("COOLDOWN", 300),
		UserCacheTTL:        getEnvInt("USER_CACHE_TTL", 600),
		DefaultDeviceLimit:  getEnvInt("DEFAULT_DEVICE_LIMIT", 0),
		ActionMode:          actionMode,
		AutoDisableDuration: getEnvInt("AUTO_DISABLE_DURATION", 0),
		IgnoreDuration:      getEnvInt("IGNORE_DURATION", 0),
		TelegramBotToken:    telegramBotToken,
		TelegramChatID:      telegramChatID,
		TelegramThreadID:    telegramThreadID,
		TelegramAdminIDs:    telegramAdminIDs,
		TelegramProxy:       getEnv("TELEGRAM_PROXY", ""),
		WhitelistUserIDs:    parseList(getEnv("WHITELIST_USER_IDS", "")),
		RedisURL:            getEnv("REDIS_URL", "redis://redis:6379"),
		Timezone:            getEnv("TIMEZONE", "UTC"),
		Language:            getEnv("LANGUAGE", "ru"),
		RemnawaveCookies:    getEnv("REMNAWAVE_COOKIES", ""),
		WebhookURL:          getEnv("WEBHOOK_URL", ""),
		WebhookSecret:       getEnv("WEBHOOK_SECRET", ""),
		SubnetGrouping:           getEnvBool("SUBNET_GROUPING", false),
		SubnetPrefixV4:           getEnvInt("SUBNET_PREFIX_V4", 24),
		ASNGrouping:              getEnvBool("ASN_GROUPING", false),
		ASNDatabasePath:          getEnv("ASN_DATABASE_PATH", "./geoip/GeoLite2-ASN.mmdb"),
		MaxMindLicenseKey:        getEnv("MAXMIND_LICENSE_KEY", ""),
		MaxMindUpdateInterval:    maxmindInterval,
		ViolationThreshold:       getEnvInt("VIOLATION_THRESHOLD", 1),
		ViolationThresholdWindow: getEnvInt("VIOLATION_THRESHOLD_WINDOW", 3600),
		IgnoredNodeUUIDs:         parseLowercaseList(getEnv("IGNORED_NODE_UUIDS", "")),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) Validate() error {
	if cfg.ActionMode != "manual" && cfg.ActionMode != "auto" {
		return fmt.Errorf("ACTION_MODE должен быть \"manual\" или \"auto\", получено %q", cfg.ActionMode)
	}
	if cfg.CheckInterval <= 0 {
		return fmt.Errorf("CHECK_INTERVAL должен быть > 0, получено %d", cfg.CheckInterval)
	}
	if cfg.ActiveIPWindow <= 0 {
		return fmt.Errorf("ACTIVE_IP_WINDOW должен быть > 0, получено %d", cfg.ActiveIPWindow)
	}
	if cfg.Cooldown <= 0 {
		return fmt.Errorf("COOLDOWN должен быть > 0, получено %d", cfg.Cooldown)
	}
	if cfg.ViolationThreshold <= 0 {
		return fmt.Errorf("VIOLATION_THRESHOLD должен быть > 0, получено %d", cfg.ViolationThreshold)
	}
	if cfg.ViolationThresholdWindow <= 0 {
		return fmt.Errorf("VIOLATION_THRESHOLD_WINDOW должен быть > 0, получено %d", cfg.ViolationThresholdWindow)
	}
	if cfg.SubnetPrefixV4 < 8 || cfg.SubnetPrefixV4 > 32 {
		return fmt.Errorf("SUBNET_PREFIX_V4 должен быть в диапазоне 8..32, получено %d", cfg.SubnetPrefixV4)
	}
	if cfg.MaxMindUpdateInterval < time.Hour {
		return fmt.Errorf("MAXMIND_UPDATE_INTERVAL должен быть >= 1h, получено %v", cfg.MaxMindUpdateInterval)
	}
	if cfg.TelegramProxy != "" {
		u, err := url.Parse(cfg.TelegramProxy)
		if err != nil {
			return fmt.Errorf("TELEGRAM_PROXY: невозможно разобрать URL %q: %v", cfg.TelegramProxy, err)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("TELEGRAM_PROXY: неподдерживаемая схема %q (ожидается http, https или socks5)", u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("TELEGRAM_PROXY: отсутствует host:port в %q", cfg.TelegramProxy)
		}
	}
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		intVal, err := strconv.Atoi(value)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"key":     key,
				"value":   value,
				"default": defaultValue,
			}).Warnf("Не удалось преобразовать %s в число, используется значение по умолчанию %d", key, defaultValue)
			return defaultValue
		}
		return intVal
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		intVal, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"key":     key,
				"value":   value,
				"default": defaultValue,
			}).Warnf("Не удалось преобразовать %s в число, используется значение по умолчанию %d", key, defaultValue)
			return defaultValue
		}
		return intVal
	}
	return defaultValue
}

func getEnvFloat64(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		floatVal, err := strconv.ParseFloat(value, 64)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"key":     key,
				"value":   value,
				"default": defaultValue,
			}).Warnf("Не удалось преобразовать %s в число, используется значение по умолчанию %v", key, defaultValue)
			return defaultValue
		}
		return floatVal
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) (time.Duration, error) {
	if value := os.Getenv(key); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("%s: невозможно преобразовать %q в duration: %v", key, value, err)
		}
		return d, nil
	}
	return defaultValue, nil
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return strings.EqualFold(value, "true") || value == "1"
	}
	return defaultValue
}

func parseint64list(s string) ([]int64, error) {
	if s == "" {
		return []int64{}, nil
	}
	parts := strings.Split(s, ",")
	result := make([]int64, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		val, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("невозможно преобразовать %q в число: %v", trimmed, err)
		}
		result = append(result, val)
	}
	return result, nil
}

func parseLowercaseList(listStr string) []string {
	if listStr == "" {
		return []string{}
	}
	items := strings.Split(listStr, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.ToLower(strings.TrimSpace(item))
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseList(listStr string) []string {
	if listStr == "" {
		return []string{}
	}
	items := strings.Split(listStr, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
