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
