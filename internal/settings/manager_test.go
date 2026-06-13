package settings

import (
	"context"
	"os"
	"testing"

	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
)

const testRedisURL = "redis://localhost:6379/15"

func setRequiredEnv() {
	os.Setenv("REMNAWAVE_API_URL", "https://api.example.com")
	os.Setenv("REMNAWAVE_API_TOKEN", "test-token-123")
	os.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF")
	os.Setenv("TELEGRAM_CHAT_ID", "-1001234567890")
	os.Setenv("TELEGRAM_ADMIN_IDS", "111,222")
}

func setupManager(t *testing.T) (*Manager, *cache.Cache, *config.Provider) {
	t.Helper()
	setRequiredEnv()

	os.Setenv("COOLDOWN", "300")

	c, err := cache.New(testRedisURL)
	if err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		c.Close()
		t.Skipf("Redis unavailable: %v", err)
	}
	if err := c.ClearConfigOverrides(ctx); err != nil {
		t.Fatalf("clear overrides: %v", err)
	}

	t.Cleanup(func() {
		_ = c.ClearConfigOverrides(ctx)
		c.Close()
	})

	base, err := config.LoadConfig("")
	if err != nil {
		t.Fatalf("load base config: %v", err)
	}
	provider := config.NewProvider(base)
	mgr := NewManager(provider, c, "", nil)
	return mgr, c, provider
}

func TestManager_ApplyPersistsAndUpdatesProvider(t *testing.T) {
	mgr, c, provider := setupManager(t)
	ctx := context.Background()

	display, err := mgr.Apply(ctx, "COOLDOWN", "777")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if display != "777" {
		t.Errorf("display = %q, want 777", display)
	}
	if provider.Load().Cooldown != 777 {
		t.Errorf("provider Cooldown = %d, want 777", provider.Load().Cooldown)
	}

	ov, err := c.GetConfigOverrides(ctx)
	if err != nil {
		t.Fatalf("GetConfigOverrides: %v", err)
	}
	if ov["COOLDOWN"] != "777" {
		t.Errorf("Redis override COOLDOWN = %q, want 777", ov["COOLDOWN"])
	}
	reloaded, err := config.LoadConfigWithOverrides("", ov)
	if err != nil {
		t.Fatalf("reload with overrides: %v", err)
	}
	if reloaded.Cooldown != 777 {
		t.Errorf("reloaded Cooldown = %d, want 777", reloaded.Cooldown)
	}
}

func TestManager_ApplyEnvValueRemovesOverride(t *testing.T) {
	mgr, c, provider := setupManager(t)
	ctx := context.Background()

	if _, err := mgr.Apply(ctx, "COOLDOWN", "777"); err != nil {
		t.Fatalf("Apply 777: %v", err)
	}
	if ov, _ := c.GetConfigOverrides(ctx); ov["COOLDOWN"] != "777" {
		t.Fatalf("expected override 777, got %v", ov)
	}

	display, err := mgr.Apply(ctx, "COOLDOWN", "300")
	if err != nil {
		t.Fatalf("Apply 300: %v", err)
	}
	if display != "300" {
		t.Errorf("display = %q, want 300", display)
	}
	if provider.Load().Cooldown != 300 {
		t.Errorf("provider Cooldown = %d, want 300", provider.Load().Cooldown)
	}
	ov, _ := c.GetConfigOverrides(ctx)
	if _, ok := ov["COOLDOWN"]; ok {
		t.Errorf("override must be removed when value matches .env, got %v", ov)
	}
}

func TestManager_ApplyInvalidRejected(t *testing.T) {
	mgr, c, provider := setupManager(t)
	ctx := context.Background()

	if _, err := mgr.Apply(ctx, "COOLDOWN", "abc"); err == nil {
		t.Error("expected error for non-numeric COOLDOWN")
	}
	if _, err := mgr.Apply(ctx, "COOLDOWN", "-1"); err == nil {
		t.Error("expected error for negative COOLDOWN")
	}

	if provider.Load().Cooldown != 300 {
		t.Errorf("provider Cooldown = %d, want unchanged 300", provider.Load().Cooldown)
	}
	ov, _ := c.GetConfigOverrides(ctx)
	if _, ok := ov["COOLDOWN"]; ok {
		t.Error("invalid value must not be persisted")
	}
}

func TestManager_ResetAndResetAll(t *testing.T) {
	mgr, c, provider := setupManager(t)
	ctx := context.Background()

	if _, err := mgr.Apply(ctx, "COOLDOWN", "777"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := mgr.Apply(ctx, "ACTION_MODE", "auto"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	display, err := mgr.Reset(ctx, "COOLDOWN")
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if display != "300" {
		t.Errorf("after reset display = %q, want 300 (env)", display)
	}
	if provider.Load().Cooldown != 300 {
		t.Errorf("provider Cooldown = %d, want 300 after reset", provider.Load().Cooldown)
	}
	ov, _ := c.GetConfigOverrides(ctx)
	if _, ok := ov["COOLDOWN"]; ok {
		t.Error("COOLDOWN override must be removed after Reset")
	}
	if ov["ACTION_MODE"] != "auto" {
		t.Error("ACTION_MODE override must remain after resetting another key")
	}

	if err := mgr.ResetAll(ctx); err != nil {
		t.Fatalf("ResetAll: %v", err)
	}
	if provider.Load().ActionMode != "manual" {
		t.Errorf("ActionMode = %q, want manual (default) after ResetAll", provider.Load().ActionMode)
	}
	ov, _ = c.GetConfigOverrides(ctx)
	if len(ov) != 0 {
		t.Errorf("overrides not empty after ResetAll: %v", ov)
	}
}
