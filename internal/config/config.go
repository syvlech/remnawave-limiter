package config

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"

	"github.com/remnawave/limiter/internal/i18n"
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
	WebhookSecretHeader      bool
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
	UpdateCheck              bool

	TrafficMonitor          bool
	TrafficSpikeFactor      float64
	TrafficSpikeMinGB       int
	TrafficHeavyGB          int
	TrafficHeavyDays        int
	TrafficIgnoredNodeUUIDs []string
	HealthAddr              string
}

var (
	LogLevels  = []string{"trace", "debug", "info", "warn", "error"}
	LogFormats = []string{"text", "json"}
)

const MaxToleranceMultiplier = 100

const (
	MinTrafficSpikeFactor = 1.5
	MaxTrafficSpikeFactor = 100
	MaxSeconds            = 365 * 24 * 3600
	MaxMinutes            = 365 * 24 * 60
	MaxTrafficGB          = 1_000_000
)

func LoadConfig(envPath string) (*Config, error) {
	return LoadConfigWithOverrides(envPath, nil)
}

var (
	envFileMu     sync.Mutex
	envFileLoaded = map[string]error{}
)

func LoadEnvFile(envPath string) error {
	if envPath == "" {
		envPath = ".env"
	}
	envFileMu.Lock()
	defer envFileMu.Unlock()
	if err, done := envFileLoaded[envPath]; done {
		return err
	}
	var result error
	if err := godotenv.Load(envPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		result = fmt.Errorf(i18n.T("error.config.env_file"), envPath, err)
	}
	envFileLoaded[envPath] = result
	return result
}

func LoadConfigWithOverrides(envPath string, overrides map[string]string) (*Config, error) {
	if err := LoadEnvFile(envPath); err != nil {
		return nil, err
	}

	l := &loader{overrides: overrides}

	remnawaveAPIURL := l.lookup("REMNAWAVE_API_URL")
	if remnawaveAPIURL == "" {
		return nil, fmt.Errorf(i18n.T("error.config.required"), "REMNAWAVE_API_URL")
	}

	remnawaveAPIToken := l.lookup("REMNAWAVE_API_TOKEN")
	if remnawaveAPIToken == "" {
		return nil, fmt.Errorf(i18n.T("error.config.required"), "REMNAWAVE_API_TOKEN")
	}

	telegramBotToken := l.lookup("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		return nil, fmt.Errorf(i18n.T("error.config.required"), "TELEGRAM_BOT_TOKEN")
	}

	telegramChatIDStr := l.lookup("TELEGRAM_CHAT_ID")
	if telegramChatIDStr == "" {
		return nil, fmt.Errorf(i18n.T("error.config.required"), "TELEGRAM_CHAT_ID")
	}
	telegramChatID, err := strconv.ParseInt(telegramChatIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("error.config.chat_id_number"), err)
	}

	telegramAdminIDsStr := l.lookup("TELEGRAM_ADMIN_IDS")
	if telegramAdminIDsStr == "" {
		return nil, fmt.Errorf(i18n.T("error.config.required"), "TELEGRAM_ADMIN_IDS")
	}
	telegramAdminIDs, err := parseint64list(telegramAdminIDsStr)
	if err != nil {
		return nil, fmt.Errorf("TELEGRAM_ADMIN_IDS: %v", err)
	}
	if len(telegramAdminIDs) == 0 {
		return nil, fmt.Errorf(i18n.T("error.config.required"), "TELEGRAM_ADMIN_IDS")
	}

	cfg := &Config{
		RemnawaveAPIURL:          strings.TrimRight(strings.TrimSpace(remnawaveAPIURL), "/"),
		RemnawaveAPIToken:        remnawaveAPIToken,
		CheckInterval:            l.getEnvInt("CHECK_INTERVAL", 60),
		ActiveIPWindow:           l.getEnvInt("ACTIVE_IP_WINDOW", 120),
		Tolerance:                l.getEnvInt("TOLERANCE", 0),
		ToleranceMultiplier:      l.getEnvFloat64("TOLERANCE_MULTIPLIER", 0),
		Cooldown:                 l.getEnvInt("COOLDOWN", 300),
		UserCacheTTL:             l.getEnvInt("USER_CACHE_TTL", 600),
		DefaultDeviceLimit:       l.getEnvInt("DEFAULT_DEVICE_LIMIT", 0),
		ActionMode:               l.getEnv("ACTION_MODE", "manual"),
		AutoDisableDuration:      l.getEnvInt("AUTO_DISABLE_DURATION", 15),
		AutoNotifySoft:           l.getEnvBool("AUTO_NOTIFY_SOFT", false),
		IgnoreDuration:           l.getEnvInt("IGNORE_DURATION", 0),
		TelegramBotToken:         telegramBotToken,
		TelegramChatID:           telegramChatID,
		TelegramThreadID:         l.getEnvInt64("TELEGRAM_THREAD_ID", 0),
		TelegramAdminIDs:         telegramAdminIDs,
		TelegramProxy:            l.getEnv("TELEGRAM_PROXY", ""),
		WhitelistUserIDs:         canonicalIDs(parseList(l.getEnv("WHITELIST_USER_IDS", ""))),
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
		WebhookSecretHeader:      l.getEnvBool("WEBHOOK_SECRET_HEADER", true),
		SubnetGrouping:           l.getEnvBool("SUBNET_GROUPING", false),
		SubnetPrefixV4:           l.getEnvInt("SUBNET_PREFIX_V4", 24),
		ASNGrouping:              l.getEnvBool("ASN_GROUPING", false),
		ASNDatabasePath:          l.getEnv("ASN_DATABASE_PATH", "./geoip/GeoLite2-ASN.mmdb"),
		MaxMindLicenseKey:        l.getEnv("MAXMIND_LICENSE_KEY", ""),
		MaxMindUpdateInterval:    l.getEnvDuration("MAXMIND_UPDATE_INTERVAL", 168*time.Hour),
		ViolationThreshold:       l.getEnvInt("VIOLATION_THRESHOLD", 3),
		ViolationThresholdWindow: l.getEnvInt("VIOLATION_THRESHOLD_WINDOW", 3600),
		IgnoredNodeUUIDs:         parseLowercaseList(l.getEnv("IGNORED_NODE_UUIDS", "")),
		DailyReport:              l.getEnvBool("DAILY_REPORT", true),
		DailyReportTime:          l.getEnv("DAILY_REPORT_TIME", "21:00"),
		UpdateCheck:              l.getEnvBool("UPDATE_CHECK", true),
		TrafficMonitor:           l.getEnvBool("TRAFFIC_MONITOR", true),
		TrafficSpikeFactor:       l.getEnvFloat64("TRAFFIC_SPIKE_FACTOR", 10),
		TrafficSpikeMinGB:        l.getEnvInt("TRAFFIC_SPIKE_MIN_GB", 150),
		TrafficHeavyGB:           l.getEnvInt("TRAFFIC_HEAVY_GB", 200),
		TrafficHeavyDays:         l.getEnvInt("TRAFFIC_HEAVY_DAYS", 5),
		TrafficIgnoredNodeUUIDs:  parseLowercaseList(l.getEnv("TRAFFIC_IGNORED_NODE_UUIDS", "")),
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
		return fmt.Errorf(i18n.T("error.config.action_mode"), cfg.ActionMode)
	}
	if err := validateBaseURL("REMNAWAVE_API_URL", cfg.RemnawaveAPIURL); err != nil {
		return err
	}
	if u, err := url.Parse(cfg.RemnawaveAPIURL); err == nil && (u.RawQuery != "" || u.Fragment != "") {
		return fmt.Errorf(i18n.T("error.config.url_query"), "REMNAWAVE_API_URL", RedactURL(cfg.RemnawaveAPIURL))
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
		{"TRAFFIC_SPIKE_MIN_GB", cfg.TrafficSpikeMinGB},
	}
	for _, p := range positive {
		if p.value <= 0 {
			return fmt.Errorf(i18n.T("error.config.positive"), p.key, p.value)
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
		{"TRAFFIC_HEAVY_GB", cfg.TrafficHeavyGB},
	}
	for _, p := range nonNegative {
		if p.value < 0 {
			return fmt.Errorf(i18n.T("error.config.non_negative"), p.key, p.value)
		}
	}

	bounded := []struct {
		key   string
		value int
		max   int
	}{
		{"CHECK_INTERVAL", cfg.CheckInterval, MaxSeconds},
		{"ACTIVE_IP_WINDOW", cfg.ActiveIPWindow, MaxSeconds},
		{"COOLDOWN", cfg.Cooldown, MaxSeconds},
		{"USER_CACHE_TTL", cfg.UserCacheTTL, MaxSeconds},
		{"VIOLATION_THRESHOLD_WINDOW", cfg.ViolationThresholdWindow, MaxSeconds},
		{"AUTO_DISABLE_DURATION", cfg.AutoDisableDuration, MaxMinutes},
		{"IGNORE_DURATION", cfg.IgnoreDuration, MaxMinutes},
		{"TRAFFIC_SPIKE_MIN_GB", cfg.TrafficSpikeMinGB, MaxTrafficGB},
		{"TRAFFIC_HEAVY_GB", cfg.TrafficHeavyGB, MaxTrafficGB},
	}
	for _, p := range bounded {
		if p.value > p.max {
			return fmt.Errorf(i18n.T("error.config.too_large"), p.key, p.max, p.value)
		}
	}

	if math.IsNaN(cfg.ToleranceMultiplier) || cfg.ToleranceMultiplier < 0 || cfg.ToleranceMultiplier > MaxToleranceMultiplier {
		return fmt.Errorf(i18n.T("error.config.tolerance_multiplier"), MaxToleranceMultiplier, cfg.ToleranceMultiplier)
	}
	if err := validateThresholdReachable(cfg); err != nil {
		return err
	}
	if math.IsNaN(cfg.TrafficSpikeFactor) || cfg.TrafficSpikeFactor < MinTrafficSpikeFactor || cfg.TrafficSpikeFactor > MaxTrafficSpikeFactor {
		return fmt.Errorf(i18n.T("error.config.traffic_factor"), cfg.TrafficSpikeFactor)
	}
	if cfg.TrafficHeavyDays < 1 || cfg.TrafficHeavyDays > 7 {
		return fmt.Errorf(i18n.T("error.config.traffic_days"), cfg.TrafficHeavyDays)
	}
	if cfg.SubnetPrefixV4 < 8 || cfg.SubnetPrefixV4 > 32 {
		return fmt.Errorf(i18n.T("error.config.subnet_prefix"), cfg.SubnetPrefixV4)
	}
	if cfg.MaxMindUpdateInterval < time.Hour {
		return fmt.Errorf(i18n.T("error.config.maxmind_interval"), cfg.MaxMindUpdateInterval)
	}
	if _, _, err := ParseDailyReportTime(cfg.DailyReportTime); err != nil {
		return fmt.Errorf("DAILY_REPORT_TIME: %v", err)
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return fmt.Errorf(i18n.T("error.config.timezone"), cfg.Timezone, err)
	}
	if cfg.Language != "ru" && cfg.Language != "en" && cfg.Language != "zh" {
		return fmt.Errorf(i18n.T("error.config.language"), cfg.Language)
	}
	if !contains(LogLevels, cfg.LogLevel) {
		return fmt.Errorf(i18n.T("error.config.one_of"), "LOG_LEVEL", LogLevels, cfg.LogLevel)
	}
	if !contains(LogFormats, cfg.LogFormat) {
		return fmt.Errorf(i18n.T("error.config.one_of"), "LOG_FORMAT", LogFormats, cfg.LogFormat)
	}
	if cfg.HealthAddr != "" {
		_, port, err := net.SplitHostPort(cfg.HealthAddr)
		if err != nil {
			return fmt.Errorf(i18n.T("error.config.health_addr"), cfg.HealthAddr)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf(i18n.T("error.config.health_addr"), cfg.HealthAddr)
		}
	}

	for _, entry := range cfg.WhitelistUserIDs {
		if _, err := strconv.ParseInt(entry, 10, 64); err != nil {
			return fmt.Errorf(i18n.T("error.config.whitelist_user_id"), entry)
		}
	}

	for _, entry := range cfg.IPWhitelist {
		if strings.Contains(entry, "/") {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return fmt.Errorf(i18n.T("error.config.ip_whitelist_cidr"), entry, err)
			}
			continue
		}
		if net.ParseIP(entry) == nil {
			return fmt.Errorf(i18n.T("error.config.ip_whitelist_ip"), entry)
		}
	}
	if cfg.TelegramProxy != "" {
		u, err := url.Parse(cfg.TelegramProxy)
		if err != nil {
			return fmt.Errorf(i18n.T("error.config.url_parse"), "TELEGRAM_PROXY", RedactURL(cfg.TelegramProxy), urlParseReason(err))
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf(i18n.T("error.config.proxy_scheme"), u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf(i18n.T("error.config.proxy_host"), RedactURL(cfg.TelegramProxy))
		}
	}
	return nil
}

func validateThresholdReachable(cfg *Config) error {
	if cfg.ViolationThreshold <= 1 {
		return nil
	}
	step := int64(cfg.Cooldown) + int64(cfg.CheckInterval)
	need := int64(cfg.ViolationThreshold-1) * step
	if need < int64(cfg.ViolationThresholdWindow) {
		return nil
	}
	return fmt.Errorf(i18n.T("error.config.threshold_unreachable"),
		cfg.ViolationThreshold, step, cfg.ViolationThreshold-1, need, cfg.ViolationThresholdWindow, need)
}

func validateBaseURL(key, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf(i18n.T("error.config.url_parse"), key, RedactURL(raw), urlParseReason(err))
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf(i18n.T("error.config.url_scheme"), key, RedactURL(raw))
	}
	if u.Host == "" {
		return fmt.Errorf(i18n.T("error.config.url_host"), key, RedactURL(raw))
	}
	return nil
}

func validateRedisURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf(i18n.T("error.config.url_parse"), "REDIS_URL", RedactURL(raw), urlParseReason(err))
	}
	switch strings.ToLower(u.Scheme) {
	case "redis", "rediss", "unix":
	default:
		return fmt.Errorf(i18n.T("error.config.redis_scheme"), RedactURL(raw))
	}
	return nil
}

var reURLUserinfo = regexp.MustCompile(`//[^/@]*@`)

func RedactURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Redacted()
	}
	return reURLUserinfo.ReplaceAllString(raw, "//***@")
}

func urlParseReason(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func canonicalIDs(items []string) []string {
	for i, item := range items {
		if id, err := strconv.ParseInt(item, 10, 64); err == nil {
			items[i] = strconv.FormatInt(id, 10)
		}
	}
	return items
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
	l.errs = append(l.errs, fmt.Errorf(i18n.T("error.config.invalid"), key, expected, value))
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
		l.invalid(key, value, i18n.T("error.config.expect_int"))
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
		l.invalid(key, value, i18n.T("error.config.expect_int"))
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
		l.invalid(key, value, i18n.T("error.config.expect_number"))
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
		l.invalid(key, value, i18n.T("error.config.expect_duration"))
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
		l.invalid(key, value, i18n.T("error.config.expect_bool"))
		return defaultValue
	}
	return b
}

func MergeSavedOverrides(envPath string, saved map[string]string) (*Config, map[string]string, map[string]error, error) {
	rejected := make(map[string]error)
	accepted := make(map[string]string, len(saved))
	for key, value := range saved {
		if err := ValidateRaw(key, value); err != nil {
			rejected[key] = err
			continue
		}
		accepted[key] = value
	}

	if cfg, err := LoadConfigWithOverrides(envPath, accepted); err == nil {
		return cfg, accepted, rejected, nil
	}

	keys := make([]string, 0, len(accepted))
	for key := range accepted {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	kept := make(map[string]string, len(accepted))
	pending := keys
	for len(pending) > 0 {
		var failed []string
		for _, key := range pending {
			kept[key] = accepted[key]
			if _, err := LoadConfigWithOverrides(envPath, kept); err != nil {
				delete(kept, key)
				rejected[key] = err
				failed = append(failed, key)
				continue
			}
			delete(rejected, key)
		}
		if len(failed) == len(pending) {
			break
		}
		pending = failed
	}

	cfg, err := LoadConfigWithOverrides(envPath, kept)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, kept, rejected, nil
}

func ParseDailyReportTime(s string) (hour, minute int, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf(i18n.T("error.config.time_format"), s)
	}
	hour, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf(i18n.T("error.config.time_hours"), parts[0])
	}
	minute, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf(i18n.T("error.config.time_minutes"), parts[1])
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
			return nil, fmt.Errorf(i18n.T("error.config.list_number"), trimmed, err)
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
