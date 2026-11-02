package config

import (
	"os"
	"testing"
)

func TestLoadConfigWithOverrides_Priority(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("COOLDOWN", "111")
	os.Setenv("ACTIVE_IP_WINDOW", "150")
	defer clearEnv()

	overrides := map[string]string{
		"COOLDOWN":    "222",
		"ACTION_MODE": "auto",
	}

	cfg, err := LoadConfigWithOverrides("", overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Cooldown != 222 {
		t.Errorf("Cooldown = %d, want 222 (override > env)", cfg.Cooldown)
	}
	if cfg.ActionMode != "auto" {
		t.Errorf("ActionMode = %q, want auto (override)", cfg.ActionMode)
	}
	if cfg.ActiveIPWindow != 150 {
		t.Errorf("ActiveIPWindow = %d, want 150 (env, no override)", cfg.ActiveIPWindow)
	}
	if cfg.UserCacheTTL != 600 {
		t.Errorf("UserCacheTTL = %d, want 600 (default)", cfg.UserCacheTTL)
	}
}

func TestLoadConfigWithOverrides_InvalidRejected(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	defer clearEnv()

	if _, err := LoadConfigWithOverrides("", map[string]string{"COOLDOWN": "-5"}); err == nil {
		t.Fatal("expected error for negative COOLDOWN, got nil")
	}
}

func TestValidateRaw(t *testing.T) {
	cases := []struct {
		key     string
		raw     string
		wantErr bool
	}{
		{"COOLDOWN", "300", false},
		{"COOLDOWN", "abc", true},
		{"TOLERANCE_MULTIPLIER", "1.5", false},
		{"TOLERANCE_MULTIPLIER", "x", true},
		{"ACTION_MODE", "auto", false},
		{"ACTION_MODE", "weird", true},
		{"AUTO_NOTIFY_SOFT", "true", false},
		{"AUTO_NOTIFY_SOFT", "1", true},
		{"REDIS_URL", "anything", true},
	}
	for _, c := range cases {
		err := ValidateRaw(c.key, c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateRaw(%q, %q) error = %v, wantErr = %v", c.key, c.raw, err, c.wantErr)
		}
	}
}

func TestMergeSavedOverrides_AllValid(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	defer clearEnv()

	cfg, applied, rejected, err := MergeSavedOverrides("", map[string]string{"COOLDOWN": "222", "ACTION_MODE": "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cooldown != 222 || cfg.ActionMode != "auto" {
		t.Errorf("переопределения не применены: cooldown=%d mode=%s", cfg.Cooldown, cfg.ActionMode)
	}
	if len(applied) != 2 || len(rejected) != 0 {
		t.Errorf("applied=%v rejected=%v", applied, rejected)
	}
}

func TestMergeSavedOverrides_DropsOnlyInvalidKeys(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	defer clearEnv()

	saved := map[string]string{
		"COOLDOWN":             "222",
		"TOLERANCE_MULTIPLIER": "NaN",
		"TELEGRAM_BOT_TOKEN":   "1:evil",
		"CHECK_INTERVAL":       "-1",
	}
	cfg, applied, rejected, err := MergeSavedOverrides("", saved)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cooldown != 222 {
		t.Errorf("валидное переопределение потеряно: COOLDOWN=%d", cfg.Cooldown)
	}
	if cfg.TelegramBotToken != "123456:ABC-DEF" {
		t.Errorf("нередактируемый ключ применён: %q", cfg.TelegramBotToken)
	}
	if len(applied) != 1 || applied["COOLDOWN"] != "222" {
		t.Errorf("applied = %v", applied)
	}
	for _, key := range []string{"TOLERANCE_MULTIPLIER", "TELEGRAM_BOT_TOKEN", "CHECK_INTERVAL"} {
		if rejected[key] == nil {
			t.Errorf("%s должен быть отброшен, rejected = %v", key, rejected)
		}
	}
}

func TestMergeSavedOverrides_ConflictingCombination(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	defer clearEnv()

	saved := map[string]string{
		"COOLDOWN":            "3600",
		"VIOLATION_THRESHOLD": "3",
		"IGNORE_DURATION":     "60",
	}
	cfg, applied, rejected, err := MergeSavedOverrides("", saved)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IgnoreDuration != 60 {
		t.Errorf("независимое переопределение потеряно: IGNORE_DURATION=%d", cfg.IgnoreDuration)
	}
	if len(rejected) != 1 || len(applied) != 2 {
		t.Errorf("ожидался один отброшенный ключ: applied=%v rejected=%v", applied, rejected)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("итоговый конфиг невалиден: %v", err)
	}
}
