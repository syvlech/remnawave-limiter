package cache

import (
	"context"
	"testing"
)

func TestCache_ConfigOverrides(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	ov, err := c.GetConfigOverrides(ctx)
	if err != nil {
		t.Fatalf("GetConfigOverrides on empty: %v", err)
	}
	if len(ov) != 0 {
		t.Errorf("expected empty overrides, got %v", ov)
	}

	if err := c.SetConfigOverride(ctx, "COOLDOWN", "600"); err != nil {
		t.Fatalf("SetConfigOverride: %v", err)
	}
	if err := c.SetConfigOverride(ctx, "ACTION_MODE", "auto"); err != nil {
		t.Fatalf("SetConfigOverride: %v", err)
	}

	ov, err = c.GetConfigOverrides(ctx)
	if err != nil {
		t.Fatalf("GetConfigOverrides: %v", err)
	}
	if ov["COOLDOWN"] != "600" || ov["ACTION_MODE"] != "auto" {
		t.Errorf("unexpected overrides: %v", ov)
	}

	if err := c.DeleteConfigOverride(ctx, "COOLDOWN"); err != nil {
		t.Fatalf("DeleteConfigOverride: %v", err)
	}
	ov, _ = c.GetConfigOverrides(ctx)
	if _, ok := ov["COOLDOWN"]; ok {
		t.Error("COOLDOWN should be deleted")
	}
	if ov["ACTION_MODE"] != "auto" {
		t.Error("ACTION_MODE should remain")
	}

	if err := c.ClearConfigOverrides(ctx); err != nil {
		t.Fatalf("ClearConfigOverrides: %v", err)
	}
	ov, _ = c.GetConfigOverrides(ctx)
	if len(ov) != 0 {
		t.Errorf("expected empty after clear, got %v", ov)
	}
}
