package config

import (
	"errors"
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
	LogLevel                 string
	LogFormat                string
	RemnawaveCookies         string
	RemnawaveHeaders         string
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

var (
	LogLevels  = []string{"trace", "debug", "info", "warn", "error"}
	LogFormats = []string{"text", "json"}
)

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

	cfg := &Config{
		RemnawaveAPIURL:          strings.TrimRight(strings.TrimSpace(remnawaveAPIURL), "/"),
		RemnawaveAPIToken:        remnawaveAPIToken,
		CheckInterval:            l.getEnvInt("CHECK_INTERVAL", 30),
		ActiveIPWindow:           l.getEnvInt("ACTIVE_IP_WINDOW", 300),
		Tolerance:                l.getEnvInt("TOLERANCE", 0),
		ToleranceMultiplier:      l.getEnvFloat64("TOLERANCE_MULTIPLIER", 0),
		Cooldown:                 l.getEnvInt("COOLDOWN", 300),
		UserCacheTTL:             l.getEnvInt("USER_CACHE_TTL", 600),
		DefaultDeviceLimit:       l.getEnvInt("DEFAULT_DEVICE_LIMIT", 0),
		ActionMode:               l.getEnv("ACTION_MODE", "manual"),
		AutoDisableDuration:      l.getEnvInt("AUTO_DISABLE_DURATION", 0),
		AutoNotifySoft:           l.getEnvBool("AUTO_NOTIFY_SOFT", false),
		IgnoreDuration:           l.getEnvInt("IGNORE_DURATION", 0),
		TelegramBotToken:         telegramBotToken,
		TelegramChatID:           telegramChatID,
		TelegramThreadID:         l.getEnvInt64("TELEGRAM_THREAD_ID", 0),
		TelegramAdminIDs:         telegramAdminIDs,
		TelegramProxy:            l.getEnv("TELEGRAM_PROXY", ""),
		WhitelistUserIDs:         parseList(l.getEnv("WHITELIST_USER_IDS", "")),
		IPWhitelist:              parseList(l.getEnv("IP_WHITELIST", "")),
		RedisURL:                 l.getEnv("REDIS_URL", "redis://redis:6379"),
		Timezone:                 l.getEnv("TIMEZONE", "UTC"),
		Language:                 l.getEnv("LANGUAGE", "ru"),
		LogLevel:                 strings.ToLower(l.getEnv("LOG_LEVEL", "info")),
		LogFormat:                strings.ToLower(l.getEnv("LOG_FORMAT", "text")),
		RemnawaveCookies:         l.getEnv("REMNAWAVE_COOKIES", ""),
		RemnawaveHeaders:         l.getEnv("REMNAWAVE_HEADERS", ""),
		WebhookURL:               l.getEnv("WEBHOOK_URL", ""),
		WebhookSecret:            l.getEnv("WEBHOOK_SECRET", ""),
		SubnetGrouping:           l.getEnvBool("SUBNET_GROUPING", false),
		SubnetPrefixV4:           l.getEnvInt("SUBNET_PREFIX_V4", 24),
		ASNGrouping:              l.getEnvBool("ASN_GROUPING", false),
		ASNDatabasePath:          l.getEnv("ASN_DATABASE_PATH", "./geoip/GeoLite2-ASN.mmdb"),
		MaxMindLicenseKey:        l.getEnv("MAXMIND_LICENSE_KEY", ""),
		MaxMindUpdateInterval:    l.getEnvDuration("MAXMIND_UPDATE_INTERVAL", 168*time.Hour),
		ViolationThreshold:       l.getEnvInt("VIOLATION_THRESHOLD", 1),
		ViolationThresholdWindow: l.getEnvInt("VIOLATION_THRESHOLD_WINDOW", 3600),
		IgnoredNodeUUIDs:         parseLowercaseList(l.getEnv("IGNORED_NODE_UUIDS", "")),
		DailyReport:              l.getEnvBool("DAILY_REPORT", false),
		DailyReportTime:          l.getEnv("DAILY_REPORT_TIME", "09:00"),
		HealthAddr:               l.getEnv("HEALTH_ADDR", ""),
	}

	if err := l.err(); err != nil {
		return nil, err
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
	if err := validateBaseURL("REMNAWAVE_API_URL", cfg.RemnawaveAPIURL); err != nil {
		return err
	}
	if cfg.WebhookURL != "" {
		if err := validateBaseURL("WEBHOOK_URL", cfg.WebhookURL); err != nil {
			return err
		}
	}
	if err := validateRedisURL(cfg.RedisURL); err != nil {
		return err
	}

	positive := []struct {
		key   string
		value int
	}{
		{"CHECK_INTERVAL", cfg.CheckInterval},
		{"ACTIVE_IP_WINDOW", cfg.ActiveIPWindow},
		{"COOLDOWN", cfg.Cooldown},
		{"USER_CACHE_TTL", cfg.UserCacheTTL},
		{"VIOLATION_THRESHOLD", cfg.ViolationThreshold},
		{"VIOLATION_THRESHOLD_WINDOW", cfg.ViolationThresholdWindow},
	}
	for _, p := range positive {
		if p.value <= 0 {
			return fmt.Errorf("%s должен быть > 0, получено %d", p.key, p.value)
		}
	}

	nonNegative := []struct {
		key   string
		value int
	}{
		{"TOLERANCE", cfg.Tolerance},
		{"DEFAULT_DEVICE_LIMIT", cfg.DefaultDeviceLimit},
		{"AUTO_DISABLE_DURATION", cfg.AutoDisableDuration},
		{"IGNORE_DURATION", cfg.IgnoreDuration},
	}
	for _, p := range nonNegative {
		if p.value < 0 {
			return fmt.Errorf("%s не может быть отрицательным, получено %d", p.key, p.value)
		}
	}

	if cfg.ToleranceMultiplier < 0 {
		return fmt.Errorf("TOLERANCE_MULTIPLIER не может быть отрицательным, получено %v", cfg.ToleranceMultiplier)
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
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return fmt.Errorf("TIMEZONE: неверная таймзона %q: %v", cfg.Timezone, err)
	}
	if cfg.Language != "ru" && cfg.Language != "en" {
		return fmt.Errorf("LANGUAGE должен быть \"ru\" или \"en\", получено %q", cfg.Language)
	}
	if !contains(LogLevels, cfg.LogLevel) {
		return fmt.Errorf("LOG_LEVEL должен быть одним из %v, получено %q", LogLevels, cfg.LogLevel)
	}
	if !contains(LogFormats, cfg.LogFormat) {
		return fmt.Errorf("LOG_FORMAT должен быть одним из %v, получено %q", LogFormats, cfg.LogFormat)
	}
	if cfg.HealthAddr != "" {
		if _, _, err := net.SplitHostPort(cfg.HealthAddr); err != nil {
			return fmt.Errorf("HEALTH_ADDR: ожидается host:port (например \":8080\"), получено %q", cfg.HealthAddr)
		}
	}

	for _, entry := range cfg.WhitelistUserIDs {
		if _, err := strconv.ParseInt(entry, 10, 64); err != nil {
			return fmt.Errorf("WHITELIST_USER_IDS: ожидается числовой ID пользователя, получено %q", entry)
		}
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

func validateBaseURL(key, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: невозможно разобрать URL %q: %v", key, raw, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%s: ожидается http:// или https://, получено %q", key, raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%s: отсутствует host в %q", key, raw)
	}
	return nil
}

func validateRedisURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("REDIS_URL: невозможно разобрать URL %q: %v", raw, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "redis", "rediss", "unix":
	default:
		return fmt.Errorf("REDIS_URL: ожидается redis://, rediss:// или unix://, получено %q", raw)
	}
	return nil
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

type loader struct {
	overrides map[string]string
	errs      []error
}

func (l *loader) lookup(key string) string {
	if l.overrides != nil {
		if value, ok := l.overrides[key]; ok {
			return value
		}
	}
	return os.Getenv(key)
}

func (l *loader) err() error {
	return errors.Join(l.errs...)
}

func (l *loader) invalid(key, value, expected string) {
	l.errs = append(l.errs, fmt.Errorf("%s: ожидается %s, получено %q", key, expected, value))
}

func (l *loader) getEnv(key, defaultValue string) string {
	if value := l.lookup(key); value != "" {
		return value
	}
	return defaultValue
}

func (l *loader) getEnvInt(key string, defaultValue int) int {
	value := l.lookup(key)
	if value == "" {
		return defaultValue
	}
	intVal, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		l.invalid(key, value, "целое число")
		return defaultValue
	}
	return intVal
}

func (l *loader) getEnvInt64(key string, defaultValue int64) int64 {
	value := l.lookup(key)
	if value == "" {
		return defaultValue
	}
	intVal, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		l.invalid(key, value, "целое число")
		return defaultValue
	}
	return intVal
}

func (l *loader) getEnvFloat64(key string, defaultValue float64) float64 {
	value := l.lookup(key)
	if value == "" {
		return defaultValue
	}
	floatVal, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		l.invalid(key, value, "число")
		return defaultValue
	}
	return floatVal
}

func (l *loader) getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	value := l.lookup(key)
	if value == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		l.invalid(key, value, "длительность вида 24h, 30m")
		return defaultValue
	}
	return d
}

func (l *loader) getEnvBool(key string, defaultValue bool) bool {
	value := l.lookup(key)
	if value == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		l.invalid(key, value, "true или false")
		return defaultValue
	}
	return b
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
	items := parseList(listStr)
	for i, item := range items {
		items[i] = strings.ToLower(item)
	}
	return items
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
