package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func clearEnv() {
	vars := []string{
		"REMNAWAVE_API_URL", "REMNAWAVE_API_TOKEN",
		"CHECK_INTERVAL", "ACTIVE_IP_WINDOW", "TOLERANCE", "TOLERANCE_MULTIPLIER", "COOLDOWN",
		"USER_CACHE_TTL", "DEFAULT_DEVICE_LIMIT",
		"ACTION_MODE", "AUTO_DISABLE_DURATION", "IGNORE_DURATION",
		"TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID", "TELEGRAM_THREAD_ID", "TELEGRAM_ADMIN_IDS",
		"TELEGRAM_PROXY",
		"WHITELIST_USER_IDS",
		"REDIS_URL",
		"TIMEZONE",
		"LANGUAGE",
		"WEBHOOK_URL", "WEBHOOK_SECRET", "WEBHOOK_SECRET_HEADER",
		"SUBNET_GROUPING",
		"SUBNET_PREFIX_V4",
		"ASN_GROUPING",
		"ASN_DATABASE_PATH",
		"MAXMIND_LICENSE_KEY", "MAXMIND_UPDATE_INTERVAL",
		"IGNORED_NODE_UUIDS",
		"IP_WHITELIST",
		"VIOLATION_THRESHOLD", "VIOLATION_THRESHOLD_WINDOW",
		"AUTO_NOTIFY_SOFT",
		"DAILY_REPORT", "DAILY_REPORT_TIME",
		"HEALTH_ADDR",
		"LOG_LEVEL", "LOG_FORMAT",
		"REMNAWAVE_COOKIES", "REMNAWAVE_HEADERS",
		"UPDATE_CHECK",
		"TRAFFIC_MONITOR", "TRAFFIC_SPIKE_FACTOR", "TRAFFIC_SPIKE_MIN_GB",
		"TRAFFIC_HEAVY_GB", "TRAFFIC_HEAVY_DAYS", "TRAFFIC_IGNORED_NODE_UUIDS",
	}
	for _, v := range vars {
		os.Unsetenv(v)
	}
}

func setRequiredEnv() {
	os.Setenv("REMNAWAVE_API_URL", "https://api.example.com")
	os.Setenv("REMNAWAVE_API_TOKEN", "test-token-123")
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF")
	os.Setenv("TELEGRAM_CHAT_ID", "-1001234567890")
	os.Setenv("TELEGRAM_ADMIN_IDS", "111,222")
}

func TestLoadConfig_Defaults(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.RemnawaveAPIURL != "https://api.example.com" {
		t.Errorf("RemnawaveAPIURL = %q, want %q", cfg.RemnawaveAPIURL, "https://api.example.com")
	}
	if cfg.RemnawaveAPIToken != "test-token-123" {
		t.Errorf("RemnawaveAPIToken = %q, want %q", cfg.RemnawaveAPIToken, "test-token-123")
	}
	if cfg.TelegramBotToken != "123456:ABC-DEF" {
		t.Errorf("TelegramBotToken = %q, want %q", cfg.TelegramBotToken, "123456:ABC-DEF")
	}
	if cfg.TelegramChatID != -1001234567890 {
		t.Errorf("TelegramChatID = %d, want %d", cfg.TelegramChatID, -1001234567890)
	}
	if len(cfg.TelegramAdminIDs) != 2 || cfg.TelegramAdminIDs[0] != 111 || cfg.TelegramAdminIDs[1] != 222 {
		t.Errorf("TelegramAdminIDs = %v, want [111 222]", cfg.TelegramAdminIDs)
	}

	if cfg.CheckInterval != 60 {
		t.Errorf("CheckInterval = %d, want 60", cfg.CheckInterval)
	}
	if cfg.ActiveIPWindow != 120 {
		t.Errorf("ActiveIPWindow = %d, want 120", cfg.ActiveIPWindow)
	}
	if cfg.Tolerance != 0 {
		t.Errorf("Tolerance = %d, want 0", cfg.Tolerance)
	}
	if cfg.ToleranceMultiplier != 0 {
		t.Errorf("ToleranceMultiplier = %f, want 0", cfg.ToleranceMultiplier)
	}
	if cfg.Cooldown != 300 {
		t.Errorf("Cooldown = %d, want 300", cfg.Cooldown)
	}
	if cfg.UserCacheTTL != 600 {
		t.Errorf("UserCacheTTL = %d, want 600", cfg.UserCacheTTL)
	}
	if cfg.DefaultDeviceLimit != 0 {
		t.Errorf("DefaultDeviceLimit = %d, want 0", cfg.DefaultDeviceLimit)
	}
	if cfg.ActionMode != "manual" {
		t.Errorf("ActionMode = %q, want %q", cfg.ActionMode, "manual")
	}
	if cfg.AutoDisableDuration != 15 {
		t.Errorf("AutoDisableDuration = %d, want 15", cfg.AutoDisableDuration)
	}
	if cfg.IgnoreDuration != 0 {
		t.Errorf("IgnoreDuration = %d, want 0", cfg.IgnoreDuration)
	}
	if cfg.RedisURL != "redis://redis:6379" {
		t.Errorf("RedisURL = %q, want %q", cfg.RedisURL, "redis://redis:6379")
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, "UTC")
	}
	if cfg.Language != "ru" {
		t.Errorf("Language = %q, want %q", cfg.Language, "ru")
	}
	if cfg.TelegramThreadID != 0 {
		t.Errorf("TelegramThreadID = %d, want 0", cfg.TelegramThreadID)
	}
	if len(cfg.WhitelistUserIDs) != 0 {
		t.Errorf("WhitelistUserIDs = %v, want []", cfg.WhitelistUserIDs)
	}
}

func TestLoadConfig_Validation_MissingRequired(t *testing.T) {
	requiredVars := []string{
		"REMNAWAVE_API_URL",
		"REMNAWAVE_API_TOKEN",
		"TELEGRAM_BOT_TOKEN",
		"TELEGRAM_CHAT_ID",
		"TELEGRAM_ADMIN_IDS",
	}

	for _, missing := range requiredVars {
		t.Run(missing, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Unsetenv(missing)

			_, err := LoadConfig("")
			if err == nil {
				t.Errorf("expected error when %s is missing, got nil", missing)
			}
		})
	}
}

func TestLoadConfig_Validation_InvalidActionMode(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("ACTION_MODE", "invalid")

	_, err := LoadConfig("")
	if err == nil {
		t.Error("expected error for invalid ACTION_MODE, got nil")
	}
}

func TestWebhookConfig(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	os.Setenv("WEBHOOK_URL", "https://example.com/hook")
	os.Setenv("WEBHOOK_SECRET", "secret123")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.WebhookURL != "https://example.com/hook" {
		t.Errorf("expected WebhookURL https://example.com/hook, got %s", cfg.WebhookURL)
	}
	if cfg.WebhookSecret != "secret123" {
		t.Errorf("expected WebhookSecret secret123, got %s", cfg.WebhookSecret)
	}
}

func TestWebhookConfig_Defaults(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.WebhookURL != "" {
		t.Errorf("expected empty WebhookURL by default, got %s", cfg.WebhookURL)
	}
	if cfg.WebhookSecret != "" {
		t.Errorf("expected empty WebhookSecret by default, got %s", cfg.WebhookSecret)
	}
}

func TestParseInt64List(t *testing.T) {
	result, err := parseint64list("111,222,333")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("len = %d, want 3", len(result))
	}
	expected := []int64{111, 222, 333}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("result[%d] = %d, want %d", i, result[i], v)
		}
	}
}

func TestParseInt64List_Empty(t *testing.T) {
	result, err := parseint64list("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestLoadConfig_SubnetGrouping_Default(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SubnetGrouping != false {
		t.Errorf("SubnetGrouping = %v, want false", cfg.SubnetGrouping)
	}
}

func TestLoadConfig_SubnetGrouping_Enabled(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("SUBNET_GROUPING", "true")
	defer os.Unsetenv("SUBNET_GROUPING")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SubnetGrouping != true {
		t.Errorf("SubnetGrouping = %v, want true", cfg.SubnetGrouping)
	}
}

func TestLoadConfig_SubnetPrefix_Defaults(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SubnetPrefixV4 != 24 {
		t.Errorf("SubnetPrefixV4 = %d, want 24", cfg.SubnetPrefixV4)
	}
}

func TestLoadConfig_SubnetPrefix_Custom(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("SUBNET_PREFIX_V4", "16")
	defer os.Unsetenv("SUBNET_PREFIX_V4")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SubnetPrefixV4 != 16 {
		t.Errorf("SubnetPrefixV4 = %d, want 16", cfg.SubnetPrefixV4)
	}
}

func TestLoadConfig_SubnetPrefix_ValidationV4(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"too_low", "7"},
		{"too_high", "33"},
		{"zero", "0"},
		{"negative", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv("SUBNET_PREFIX_V4", tc.value)
			defer os.Unsetenv("SUBNET_PREFIX_V4")

			_, err := LoadConfig("")
			if err == nil {
				t.Errorf("expected validation error for SUBNET_PREFIX_V4=%s, got nil", tc.value)
			}
		})
	}
}

func TestLoadConfig_ASNGrouping_Default(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ASNGrouping != false {
		t.Errorf("ASNGrouping = %v, want false", cfg.ASNGrouping)
	}
}

func TestLoadConfig_ASNGrouping_Enabled(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("ASN_GROUPING", "true")
	defer os.Unsetenv("ASN_GROUPING")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ASNGrouping != true {
		t.Errorf("ASNGrouping = %v, want true", cfg.ASNGrouping)
	}
}

func TestLoadConfig_ASN_Database_Defaults(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ASNDatabasePath != "./geoip/GeoLite2-ASN.mmdb" {
		t.Errorf("ASNDatabasePath = %q, want default ./geoip/GeoLite2-ASN.mmdb", cfg.ASNDatabasePath)
	}
}

func TestLoadConfig_ASN_Database_Custom(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("ASN_DATABASE_PATH", "/var/lib/geoip/GeoLite2-ASN.mmdb")
	defer os.Unsetenv("ASN_DATABASE_PATH")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ASNDatabasePath != "/var/lib/geoip/GeoLite2-ASN.mmdb" {
		t.Errorf("ASNDatabasePath = %q, want /var/lib/geoip/GeoLite2-ASN.mmdb", cfg.ASNDatabasePath)
	}
}

func TestLoadConfig_MaxMind_Defaults(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MaxMindLicenseKey != "" {
		t.Errorf("MaxMindLicenseKey = %q, want empty", cfg.MaxMindLicenseKey)
	}
	expected := 168 * time.Hour
	if cfg.MaxMindUpdateInterval != expected {
		t.Errorf("MaxMindUpdateInterval = %v, want %v", cfg.MaxMindUpdateInterval, expected)
	}
}

func TestLoadConfig_MaxMind_Custom(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("MAXMIND_LICENSE_KEY", "abc123")
	os.Setenv("MAXMIND_UPDATE_INTERVAL", "24h")
	defer os.Unsetenv("MAXMIND_LICENSE_KEY")
	defer os.Unsetenv("MAXMIND_UPDATE_INTERVAL")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MaxMindLicenseKey != "abc123" {
		t.Errorf("MaxMindLicenseKey = %q, want %q", cfg.MaxMindLicenseKey, "abc123")
	}
	if cfg.MaxMindUpdateInterval != 24*time.Hour {
		t.Errorf("MaxMindUpdateInterval = %v, want 24h", cfg.MaxMindUpdateInterval)
	}
}

func TestLoadConfig_MaxMind_IntervalTooShort_Fails(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("MAXMIND_UPDATE_INTERVAL", "30m")
	defer os.Unsetenv("MAXMIND_UPDATE_INTERVAL")

	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected validation error for MAXMIND_UPDATE_INTERVAL=30m, got nil")
	}
}

func TestLoadConfig_MaxMind_IntervalInvalid_Fails(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("MAXMIND_UPDATE_INTERVAL", "not-a-duration")
	defer os.Unsetenv("MAXMIND_UPDATE_INTERVAL")

	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected validation error for unparseable MAXMIND_UPDATE_INTERVAL, got nil")
	}
}

func TestLoadConfig_TelegramProxy_Default(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TelegramProxy != "" {
		t.Errorf("TelegramProxy = %q, want empty", cfg.TelegramProxy)
	}
}

func TestLoadConfig_TelegramProxy_Valid(t *testing.T) {
	cases := []string{
		"socks5://127.0.0.1:1080",
		"socks5://user:pass@proxy.example.com:1080",
		"http://proxy.example.com:3128",
		"https://user:pass@proxy.example.com:8443",
	}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv("TELEGRAM_PROXY", value)
			defer os.Unsetenv("TELEGRAM_PROXY")

			cfg, err := LoadConfig("")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.TelegramProxy != value {
				t.Errorf("TelegramProxy = %q, want %q", cfg.TelegramProxy, value)
			}
		})
	}
}

func TestLoadConfig_TelegramProxy_Invalid(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"unsupported_scheme", "ftp://proxy.example.com:21"},
		{"missing_host", "socks5://"},
		{"no_scheme", "proxy.example.com:1080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv("TELEGRAM_PROXY", tc.value)
			defer os.Unsetenv("TELEGRAM_PROXY")

			_, err := LoadConfig("")
			if err == nil {
				t.Errorf("expected validation error for TELEGRAM_PROXY=%q, got nil", tc.value)
			}
		})
	}
}

func TestLoadConfig_IgnoreDuration_Custom(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("IGNORE_DURATION", "15")
	defer os.Unsetenv("IGNORE_DURATION")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.IgnoreDuration != 15 {
		t.Errorf("IgnoreDuration = %d, want 15", cfg.IgnoreDuration)
	}
}

func TestLoadConfig_IgnoredNodeUUIDs_Default(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.IgnoredNodeUUIDs) != 0 {
		t.Errorf("IgnoredNodeUUIDs = %v, want []", cfg.IgnoredNodeUUIDs)
	}
}

func TestLoadConfig_IgnoredNodeUUIDs_Custom(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("IGNORED_NODE_UUIDS", "4F2D0F6D-551F-4C98-A20E-058DA935673C, 9a8b0000-0000-0000-0000-000000000001 ,  ")
	defer os.Unsetenv("IGNORED_NODE_UUIDS")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"4f2d0f6d-551f-4c98-a20e-058da935673c",
		"9a8b0000-0000-0000-0000-000000000001",
	}
	if len(cfg.IgnoredNodeUUIDs) != len(want) {
		t.Fatalf("IgnoredNodeUUIDs = %v, want %v", cfg.IgnoredNodeUUIDs, want)
	}
	for i, v := range want {
		if cfg.IgnoredNodeUUIDs[i] != v {
			t.Errorf("IgnoredNodeUUIDs[%d] = %q, want %q", i, cfg.IgnoredNodeUUIDs[i], v)
		}
	}
}

func TestLoadConfig_NumericTypo_IsRejected(t *testing.T) {
	cases := []struct{ key, value string }{
		{"CHECK_INTERVAL", "6O"},
		{"TOLERANCE", "one"},
		{"TOLERANCE_MULTIPLIER", "0,5"},
		{"TELEGRAM_THREAD_ID", "abc"},
		{"AUTO_NOTIFY_SOFT", "yes please"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv(tc.key, tc.value)
			defer os.Unsetenv(tc.key)

			if _, err := LoadConfig(""); err == nil {
				t.Errorf("expected error for %s=%q, got nil", tc.key, tc.value)
			}
		})
	}
}

func TestLoadConfig_ReportsAllTypos(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("CHECK_INTERVAL", "x")
	os.Setenv("COOLDOWN", "y")
	defer os.Unsetenv("CHECK_INTERVAL")
	defer os.Unsetenv("COOLDOWN")

	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, key := range []string{"CHECK_INTERVAL", "COOLDOWN"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error should mention %s, got: %v", key, err)
		}
	}
}

func TestLoadConfig_APIURL_TrailingSlashTrimmed(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("REMNAWAVE_API_URL", "https://panel.example.com///")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RemnawaveAPIURL != "https://panel.example.com" {
		t.Errorf("RemnawaveAPIURL = %q, want %q", cfg.RemnawaveAPIURL, "https://panel.example.com")
	}
}

func TestLoadConfig_APIURL_Invalid(t *testing.T) {
	cases := []struct{ name, value string }{
		{"no_scheme", "panel.example.com"},
		{"wrong_scheme", "ftp://panel.example.com"},
		{"no_host", "https://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv("REMNAWAVE_API_URL", tc.value)

			if _, err := LoadConfig(""); err == nil {
				t.Errorf("expected error for REMNAWAVE_API_URL=%q, got nil", tc.value)
			}
		})
	}
}

func TestLoadConfig_NegativeValues_Rejected(t *testing.T) {
	cases := []struct{ key, value string }{
		{"TOLERANCE", "-1"},
		{"TOLERANCE_MULTIPLIER", "-0.5"},
		{"USER_CACHE_TTL", "-10"},
		{"DEFAULT_DEVICE_LIMIT", "-3"},
		{"AUTO_DISABLE_DURATION", "-5"},
		{"IGNORE_DURATION", "-5"},
		{"VIOLATION_THRESHOLD", "0"},
		{"VIOLATION_THRESHOLD_WINDOW", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv(tc.key, tc.value)
			defer os.Unsetenv(tc.key)

			if _, err := LoadConfig(""); err == nil {
				t.Errorf("expected error for %s=%s, got nil", tc.key, tc.value)
			}
		})
	}
}

func TestLoadConfig_Timezone_Invalid(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("TIMEZONE", "Mars/Olympus")
	defer os.Unsetenv("TIMEZONE")

	if _, err := LoadConfig(""); err == nil {
		t.Error("expected error for unknown TIMEZONE, got nil")
	}
}

func TestLoadConfig_Language_Invalid(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("LANGUAGE", "de")
	defer os.Unsetenv("LANGUAGE")

	if _, err := LoadConfig(""); err == nil {
		t.Error("expected error for unsupported LANGUAGE, got nil")
	}
}

func TestLoadConfig_WhitelistUserIDs_MustBeNumeric(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("WHITELIST_USER_IDS", "111,4f2d0f6d-551f-4c98-a20e-058da935673c")
	defer os.Unsetenv("WHITELIST_USER_IDS")

	if _, err := LoadConfig(""); err == nil {
		t.Error("expected error for non-numeric WHITELIST_USER_IDS, got nil")
	}
}

func TestLoadConfig_Logging_Defaults(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q, want text", cfg.LogFormat)
	}
}

func TestLoadConfig_Logging_Custom(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("LOG_LEVEL", "DEBUG")
	os.Setenv("LOG_FORMAT", "JSON")
	defer os.Unsetenv("LOG_LEVEL")
	defer os.Unsetenv("LOG_FORMAT")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
}

func TestLoadConfig_Logging_Invalid(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"LOG_LEVEL", "verbose"},
		{"LOG_FORMAT", "xml"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv(tc.key, tc.value)
			defer os.Unsetenv(tc.key)

			if _, err := LoadConfig(""); err == nil {
				t.Errorf("expected error for %s=%s, got nil", tc.key, tc.value)
			}
		})
	}
}

func TestLoadConfig_HealthAddr(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("HEALTH_ADDR", "8080")
	defer os.Unsetenv("HEALTH_ADDR")

	if _, err := LoadConfig(""); err == nil {
		t.Error("expected error for HEALTH_ADDR without colon, got nil")
	}

	os.Setenv("HEALTH_ADDR", ":8080")
	if _, err := LoadConfig(""); err != nil {
		t.Errorf("unexpected error for HEALTH_ADDR=:8080: %v", err)
	}
}

func TestLoadConfig_RedisURL_Invalid(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("REDIS_URL", "http://redis:6379")
	defer os.Unsetenv("REDIS_URL")

	if _, err := LoadConfig(""); err == nil {
		t.Error("expected error for non-redis REDIS_URL, got nil")
	}
}

func TestValidate_ToleranceMultiplierMustBeFiniteAndBounded(t *testing.T) {
	for _, raw := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "1e300", "101", "-0.5"} {
		clearEnv()
		setRequiredEnv()
		os.Setenv("TOLERANCE_MULTIPLIER", raw)
		if _, err := LoadConfig(""); err == nil {
			t.Errorf("TOLERANCE_MULTIPLIER=%s должен отклоняться", raw)
		}
	}
	for _, raw := range []string{"0", "0.5", "100"} {
		clearEnv()
		setRequiredEnv()
		os.Setenv("TOLERANCE_MULTIPLIER", raw)
		if _, err := LoadConfig(""); err != nil {
			t.Errorf("TOLERANCE_MULTIPLIER=%s должен приниматься: %v", raw, err)
		}
	}
	clearEnv()
}

func TestValidate_ViolationThresholdMustBeReachable(t *testing.T) {
	cases := []struct {
		name                                  string
		threshold, cooldown, interval, window string
		wantErr                               bool
	}{
		{"порог 1 — проверки нет", "1", "3600", "30", "60", false},
		{"значения по умолчанию", "3", "300", "30", "3600", false},
		{"кулдаун равен окну", "2", "3600", "30", "3600", true},
		{"кулдаун больше окна", "5", "3600", "30", "600", true},
		{"впритык не укладывается", "3", "300", "30", "660", true},
		{"укладывается", "3", "300", "30", "661", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			defer clearEnv()
			os.Setenv("VIOLATION_THRESHOLD", c.threshold)
			os.Setenv("COOLDOWN", c.cooldown)
			os.Setenv("CHECK_INTERVAL", c.interval)
			os.Setenv("VIOLATION_THRESHOLD_WINDOW", c.window)
			os.Setenv("ACTIVE_IP_WINDOW", "600")

			_, err := LoadConfig("")
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "VIOLATION_THRESHOLD") {
				t.Errorf("ошибка должна называть параметр: %v", err)
			}
		})
	}
}

func TestLoadConfig_WebhookSecretHeaderDefaultsOn(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	defer clearEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.WebhookSecretHeader {
		t.Error("WEBHOOK_SECRET_HEADER по умолчанию включён ради совместимости")
	}

	os.Setenv("WEBHOOK_SECRET_HEADER", "false")
	cfg, err = LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookSecretHeader {
		t.Error("WEBHOOK_SECRET_HEADER=false должен отключать заголовок")
	}
}

func TestValidate_UpperBounds(t *testing.T) {
	for key, value := range map[string]string{
		"CHECK_INTERVAL":             "10000000000",
		"ACTIVE_IP_WINDOW":           "10000000000",
		"COOLDOWN":                   "9223372037",
		"USER_CACHE_TTL":             "9223372037",
		"VIOLATION_THRESHOLD_WINDOW": "10000000000",
		"AUTO_DISABLE_DURATION":      "153722868",
		"IGNORE_DURATION":            "153722868",
		"TRAFFIC_SPIKE_MIN_GB":       "17179869184",
		"TRAFFIC_HEAVY_GB":           "17179869184",
	} {
		t.Run(key, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv(key, value)
			defer os.Unsetenv(key)
			_, err := LoadConfig("")
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%s=%s должен отклоняться, ошибка: %v", key, value, err)
			}
		})
	}
}

func TestLoadConfig_WhitelistIDsAreCanonical(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("WHITELIST_USER_IDS", "007, +5,42")
	defer os.Unsetenv("WHITELIST_USER_IDS")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(cfg.WhitelistUserIDs, ","); got != "7,5,42" {
		t.Errorf("WhitelistUserIDs = %q, ожидалось 7,5,42", got)
	}
}

func TestLoadConfig_EmptyAdminList(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("TELEGRAM_ADMIN_IDS", " , ")
	if _, err := LoadConfig(""); err == nil {
		t.Fatal("пустой список администраторов должен отклоняться")
	}
}

func TestValidate_ErrorsDoNotLeakURLPasswords(t *testing.T) {
	for key, value := range map[string]string{
		"REDIS_URL":      "valkey://:s3cr3t@redis:6379",
		"TELEGRAM_PROXY": "ftp://user:s3cr3t@host:1080",
	} {
		t.Run(key, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv(key, value)
			defer os.Unsetenv(key)
			_, err := LoadConfig("")
			if err == nil {
				t.Fatalf("%s=%s должен отклоняться", key, value)
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Errorf("пароль попал в текст ошибки: %v", err)
			}
		})
	}
}

func TestLoadEnvFile_ReportsBrokenFileAndLoadsOnce(t *testing.T) {
	dir := t.TempDir()
	broken := dir + "/broken.env"
	if err := os.WriteFile(broken, []byte("KEY=\"unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvFile(broken); err == nil {
		t.Error("синтаксическая ошибка в .env не должна проглатываться")
	}
	if err := LoadEnvFile(dir + "/missing.env"); err != nil {
		t.Errorf("отсутствие файла — не ошибка: %v", err)
	}

	good := dir + "/good.env"
	os.Unsetenv("LIMITER_TEST_ENV_ONCE")
	defer os.Unsetenv("LIMITER_TEST_ENV_ONCE")
	if err := os.WriteFile(good, []byte("LIMITER_TEST_ENV_FIRST=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("LIMITER_TEST_ENV_FIRST")
	if err := LoadEnvFile(good); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte("LIMITER_TEST_ENV_ONCE=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvFile(good); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LIMITER_TEST_ENV_ONCE") != "" {
		t.Error("повторная загрузка .env не должна подхватывать новые переменные без перезапуска")
	}
}

func TestValidate_APIURLWithoutQueryAndHealthPort(t *testing.T) {
	bad := map[string]string{
		"REMNAWAVE_API_URL": "https://panel.example.com/?key=x",
		"HEALTH_ADDR":       ":99999",
	}
	for key, value := range bad {
		t.Run(key, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv(key, value)
			defer os.Unsetenv(key)
			if _, err := LoadConfig(""); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%s=%s должен отклоняться, ошибка: %v", key, value, err)
			}
		})
	}

	clearEnv()
	setRequiredEnv()
	os.Setenv("HEALTH_ADDR", "host:notaport")
	if _, err := LoadConfig(""); err == nil {
		t.Error("нечисловой порт в HEALTH_ADDR должен отклоняться")
	}
	os.Setenv("HEALTH_ADDR", "127.0.0.1:8080")
	os.Setenv("WEBHOOK_URL", "https://hooks.example.com/in?token=abc")
	defer os.Unsetenv("HEALTH_ADDR")
	defer os.Unsetenv("WEBHOOK_URL")
	if _, err := LoadConfig(""); err != nil {
		t.Errorf("корректный HEALTH_ADDR и WEBHOOK_URL с query должны проходить: %v", err)
	}
}

func TestDefaultsMatchEnvExample(t *testing.T) {
	data, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("не удалось прочитать .env.example: %v", err)
	}
	required := map[string]bool{
		"REMNAWAVE_API_URL": true, "REMNAWAVE_API_TOKEN": true,
		"TELEGRAM_BOT_TOKEN": true, "TELEGRAM_CHAT_ID": true, "TELEGRAM_ADMIN_IDS": true,
	}

	clearEnv()
	setRequiredEnv()
	defer clearEnv()
	defaults, err := LoadConfig("")
	if err != nil {
		t.Fatalf("умолчания кода не проходят проверку: %v", err)
	}

	seen := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || required[key] {
			continue
		}
		seen++
		os.Setenv(key, value)
		defer os.Unsetenv(key)
	}
	if seen < 30 {
		t.Fatalf("в .env.example найдено только %d параметров, файл разобран неверно", seen)
	}
	example, err := LoadConfig("")
	if err != nil {
		t.Fatalf("значения из .env.example не проходят проверку: %v", err)
	}
	if !reflect.DeepEqual(defaults, example) {
		t.Errorf("умолчания кода расходятся с .env.example:\nкод:     %+v\nпример: %+v", *defaults, *example)
	}
}

func TestValidate_TrafficBounds(t *testing.T) {
	bad := []struct{ key, value string }{
		{"TRAFFIC_SPIKE_FACTOR", "1.4"},
		{"TRAFFIC_SPIKE_FACTOR", "101"},
		{"TRAFFIC_SPIKE_FACTOR", "NaN"},
		{"TRAFFIC_SPIKE_MIN_GB", "0"},
		{"TRAFFIC_HEAVY_GB", "-1"},
		{"TRAFFIC_HEAVY_DAYS", "0"},
		{"TRAFFIC_HEAVY_DAYS", "8"},
	}
	for _, tc := range bad {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			clearEnv()
			setRequiredEnv()
			os.Setenv(tc.key, tc.value)
			defer os.Unsetenv(tc.key)
			if _, err := LoadConfig(""); err == nil {
				t.Fatalf("%s=%s должен отклоняться", tc.key, tc.value)
			}
		})
	}

	clearEnv()
	setRequiredEnv()
	os.Setenv("TRAFFIC_SPIKE_FACTOR", "1.5")
	os.Setenv("TRAFFIC_HEAVY_GB", "0")
	os.Setenv("TRAFFIC_HEAVY_DAYS", "7")
	defer clearEnv()
	if _, err := LoadConfig(""); err != nil {
		t.Errorf("граничные значения должны проходить: %v", err)
	}
}
