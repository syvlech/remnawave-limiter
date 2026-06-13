package config

import (
	"os"
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
		"WEBHOOK_URL", "WEBHOOK_SECRET",
		"SUBNET_GROUPING",
		"SUBNET_PREFIX_V4",
		"ASN_GROUPING",
		"ASN_DATABASE_PATH",
		"MAXMIND_LICENSE_KEY", "MAXMIND_UPDATE_INTERVAL",
		"IGNORED_NODE_UUIDS",
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

	if cfg.CheckInterval != 30 {
		t.Errorf("CheckInterval = %d, want 30", cfg.CheckInterval)
	}
	if cfg.ActiveIPWindow != 300 {
		t.Errorf("ActiveIPWindow = %d, want 300", cfg.ActiveIPWindow)
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
	if cfg.AutoDisableDuration != 0 {
		t.Errorf("AutoDisableDuration = %d, want 0", cfg.AutoDisableDuration)
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
