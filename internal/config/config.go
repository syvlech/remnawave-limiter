package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

type Config struct {
	RemnawaveAPIURL          string
	RemnawaveAPIToken        string
	CheckInterval            int
	ActiveIPWindow           int
	Tolerance                int
	ToleranceMultiplier      float64
	Cooldown                 int
	UserCacheTTL             int
	DefaultDeviceLimit       int
	ActionMode               string
	AutoDisableDuration      int
	AutoNotifySoft           bool
	IgnoreDuration           int
	TelegramBotToken         string
	TelegramChatID           int64
	TelegramThreadID         int64
	TelegramAdminIDs         []int64
	TelegramProxy            string
	WhitelistUserIDs         []string
	IPWhitelist              []string
	RedisURL                 string
	Timezone                 string
	Language                 string
	RemnawaveCookies         string
	WebhookURL               string
	WebhookSecret            string
	SubnetGrouping           bool
	SubnetPrefixV4           int
	ASNGrouping              bool
	ASNDatabasePath          string
	MaxMindLicenseKey        string
	MaxMindUpdateInterval    time.Duration
	ViolationThreshold       int
	ViolationThresholdWindow int
	IgnoredNodeUUIDs         []string
	DailyReport              bool
	DailyReportTime          string
	HealthAddr               string
}

func LoadConfig(envPath string) (*Config, error) {
	return LoadConfigWithOverrides(envPath, nil)
}

func LoadConfigWithOverrides(envPath string, overrides map[string]string) (*Config, error) {
	if envPath == "" {
		envPath = ".env"
	}

	if err := godotenv.Load(envPath); err != nil {
		logrus.Debug("Файл .env не найден, используются переменные окружения")
	}

	l := &loader{overrides: overrides}

	remnawaveAPIURL := l.lookup("REMNAWAVE_API_URL")
	if remnawaveAPIURL == "" {
		return nil, fmt.Errorf("REMNAWAVE_API_URL обязательный параметр")
	}

	remnawaveAPIToken := l.lookup("REMNAWAVE_API_TOKEN")
	if remnawaveAPIToken == "" {
		return nil, fmt.Errorf("REMNAWAVE_API_TOKEN обязательный параметр")
	}

	telegramBotToken := l.lookup("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN обязательный параметр")
	}

	telegramChatIDStr := l.lookup("TELEGRAM_CHAT_ID")
	if telegramChatIDStr == "" {
		return nil, fmt.Errorf("TELEGRAM_CHAT_ID обязательный параметр")
	}
	telegramChatID, err := strconv.ParseInt(telegramChatIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("TELEGRAM_CHAT_ID должен быть числом: %v", err)
	}

	telegramAdminIDsStr := l.lookup("TELEGRAM_ADMIN_IDS")
	if telegramAdminIDsStr == "" {
		return nil, fmt.Errorf("TELEGRAM_ADMIN_IDS обязательный параметр")
	}
	telegramAdminIDs, err := parseint64list(telegramAdminIDsStr)
	if err != nil {
		return nil, fmt.Errorf("TELEGRAM_ADMIN_IDS: %v", err)
	}

	telegramThreadID := l.getEnvInt64("TELEGRAM_THREAD_ID", 0)

	actionMode := l.getEnv("ACTION_MODE", "manual")

	maxmindInterval, err := l.getEnvDuration("MAXMIND_UPDATE_INTERVAL", 168*time.Hour)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		RemnawaveAPIURL:          remnawaveAPIURL,
		RemnawaveAPIToken:        remnawaveAPIToken,
		CheckInterval:            l.getEnvInt("CHECK_INTERVAL", 30),
		ActiveIPWindow:           l.getEnvInt("ACTIVE_IP_WINDOW", 300),
		Tolerance:                l.getEnvInt("TOLERANCE", 0),
		ToleranceMultiplier:      l.getEnvFloat64("TOLERANCE_MULTIPLIER", 0),
		Cooldown:                 l.getEnvInt("COOLDOWN", 300),
		UserCacheTTL:             l.getEnvInt("USER_CACHE_TTL", 600),
		DefaultDeviceLimit:       l.getEnvInt("DEFAULT_DEVICE_LIMIT", 0),
		ActionMode:               actionMode,
		AutoDisableDuration:      l.getEnvInt("AUTO_DISABLE_DURATION", 0),
		AutoNotifySoft:           l.getEnvBool("AUTO_NOTIFY_SOFT", false),
		IgnoreDuration:           l.getEnvInt("IGNORE_DURATION", 0),
		TelegramBotToken:         telegramBotToken,
		TelegramChatID:           telegramChatID,
		TelegramThreadID:         telegramThreadID,
		TelegramAdminIDs:         telegramAdminIDs,
		TelegramProxy:            l.getEnv("TELEGRAM_PROXY", ""),
		WhitelistUserIDs:         parseList(l.getEnv("WHITELIST_USER_IDS", "")),
		IPWhitelist:              parseList(l.getEnv("IP_WHITELIST", "")),
		RedisURL:                 l.getEnv("REDIS_URL", "redis://redis:6379"),
		Timezone:                 l.getEnv("TIMEZONE", "UTC"),
		Language:                 l.getEnv("LANGUAGE", "ru"),
		RemnawaveCookies:         l.getEnv("REMNAWAVE_COOKIES", ""),
		WebhookURL:               l.getEnv("WEBHOOK_URL", ""),
		WebhookSecret:            l.getEnv("WEBHOOK_SECRET", ""),
		SubnetGrouping:           l.getEnvBool("SUBNET_GROUPING", false),
		SubnetPrefixV4:           l.getEnvInt("SUBNET_PREFIX_V4", 24),
		ASNGrouping:              l.getEnvBool("ASN_GROUPING", false),
		ASNDatabasePath:          l.getEnv("ASN_DATABASE_PATH", "./geoip/GeoLite2-ASN.mmdb"),
		MaxMindLicenseKey:        l.getEnv("MAXMIND_LICENSE_KEY", ""),
		MaxMindUpdateInterval:    maxmindInterval,
		ViolationThreshold:       l.getEnvInt("VIOLATION_THRESHOLD", 1),
		ViolationThresholdWindow: l.getEnvInt("VIOLATION_THRESHOLD_WINDOW", 3600),
		IgnoredNodeUUIDs:         parseLowercaseList(l.getEnv("IGNORED_NODE_UUIDS", "")),
		DailyReport:              l.getEnvBool("DAILY_REPORT", false),
		DailyReportTime:          l.getEnv("DAILY_REPORT_TIME", "09:00"),
		HealthAddr:               l.getEnv("HEALTH_ADDR", ""),
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
	if _, _, err := ParseDailyReportTime(cfg.DailyReportTime); err != nil {
		return fmt.Errorf("DAILY_REPORT_TIME: %v", err)
	}
	for _, entry := range cfg.IPWhitelist {
		if strings.Contains(entry, "/") {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return fmt.Errorf("IP_WHITELIST: неверный CIDR %q: %v", entry, err)
			}
			continue
		}
		if net.ParseIP(entry) == nil {
			return fmt.Errorf("IP_WHITELIST: неверный IP-адрес %q", entry)
		}
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

type loader struct {
	overrides map[string]string
}

func (l *loader) lookup(key string) string {
	if l.overrides != nil {
		if value, ok := l.overrides[key]; ok {
			return value
		}
	}
	return os.Getenv(key)
}

func (l *loader) getEnv(key, defaultValue string) string {
	if value := l.lookup(key); value != "" {
		return value
	}
	return defaultValue
}

func (l *loader) getEnvInt(key string, defaultValue int) int {
	if value := l.lookup(key); value != "" {
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

func (l *loader) getEnvInt64(key string, defaultValue int64) int64 {
	if value := l.lookup(key); value != "" {
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

func (l *loader) getEnvFloat64(key string, defaultValue float64) float64 {
	if value := l.lookup(key); value != "" {
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

func (l *loader) getEnvDuration(key string, defaultValue time.Duration) (time.Duration, error) {
	if value := l.lookup(key); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("%s: невозможно преобразовать %q в duration: %v", key, value, err)
		}
		return d, nil
	}
	return defaultValue, nil
}

func (l *loader) getEnvBool(key string, defaultValue bool) bool {
	if value := l.lookup(key); value != "" {
		return strings.EqualFold(value, "true") || value == "1"
	}
	return defaultValue
}

func ParseDailyReportTime(s string) (hour, minute int, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ожидается формат HH:MM, получено %q", s)
	}
	hour, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("часы должны быть в диапазоне 0..23, получено %q", parts[0])
	}
	minute, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("минуты должны быть в диапазоне 0..59, получено %q", parts[1])
	}
	return hour, minute, nil
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
