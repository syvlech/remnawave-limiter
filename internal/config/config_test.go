package config

import (
	"os"
	"testing"
)

func clearEnv() {
	vars := []string{
		"REMNAWAVE_API_URL", "REMNAWAVE_API_TOKEN",
		"CHECK_INTERVAL", "ACTIVE_IP_WINDOW", "TOLERANCE", "COOLDOWN",
		"USER_CACHE_TTL", "DEFAULT_DEVICE_LIMIT",
		"ACTION_MODE", "AUTO_DISABLE_DURATION",
		"TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID", "TELEGRAM_THREAD_ID", "TELEGRAM_ADMIN_IDS",
		"WHITELIST_USER_IDS",
		"REDIS_URL",
		"TIMEZONE",
		"LANGUAGE",
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
