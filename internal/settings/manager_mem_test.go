package settings

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/remnawave/limiter/internal/config"
)

type memStore struct {
	values map[string]string
	err    error
}

func (s *memStore) SetConfigOverride(_ context.Context, key, value string) error {
	if s.err != nil {
		return s.err
	}
	s.values[key] = value
	return nil
}

func (s *memStore) DeleteConfigOverride(_ context.Context, key string) error {
	if s.err != nil {
		return s.err
	}
	delete(s.values, key)
	return nil
}

func (s *memStore) ClearConfigOverrides(context.Context) error {
	if s.err != nil {
		return s.err
	}
	s.values = map[string]string{}
	return nil
}

func newMemManager(t *testing.T, saved map[string]string) (*Manager, *memStore, *config.Provider) {
	t.Helper()
	setRequiredEnv()
	os.Setenv("COOLDOWN", "300")
	t.Cleanup(func() { os.Unsetenv("COOLDOWN") })

	base, err := config.LoadConfigWithOverrides("", saved)
	if err != nil {
		t.Fatalf("load base config: %v", err)
	}
	store := &memStore{values: map[string]string{}}
	for k, v := range saved {
		store.values[k] = v
	}
	provider := config.NewProvider(base)
	return NewManager(provider, store, "", saved), store, provider
}

func TestManagerMem_ApplyStoresOverrideAndSwapsConfig(t *testing.T) {
	mgr, store, provider := newMemManager(t, nil)

	display, err := mgr.Apply(context.Background(), "COOLDOWN", " 777 ")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if display != "777" || provider.Load().Cooldown != 777 || store.values["COOLDOWN"] != "777" {
		t.Errorf("display=%q cooldown=%d store=%v", display, provider.Load().Cooldown, store.values)
	}
	item, ok := mgr.Item("COOLDOWN")
	if !ok || !item.Overridden {
		t.Errorf("параметр должен быть помечен как изменённый в боте: %+v", item)
	}
}

func TestManagerMem_ValueEqualToEnvRemovesOverride(t *testing.T) {
	mgr, store, provider := newMemManager(t, map[string]string{"COOLDOWN": "777"})

	if _, err := mgr.Apply(context.Background(), "COOLDOWN", "300"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, stored := store.values["COOLDOWN"]; stored {
		t.Errorf("значение, равное .env, не должно храниться как override: %v", store.values)
	}
	if provider.Load().Cooldown != 300 {
		t.Errorf("cooldown = %d, ожидалось 300", provider.Load().Cooldown)
	}
	if item, _ := mgr.Item("COOLDOWN"); item.Overridden {
		t.Error("отметка об изменении должна сняться")
	}
}

func TestManagerMem_RejectsInvalidAndNonEditable(t *testing.T) {
	mgr, store, provider := newMemManager(t, nil)
	ctx := context.Background()

	for key, value := range map[string]string{
		"COOLDOWN":            "-5",
		"CHECK_INTERVAL":      "abc",
		"TELEGRAM_BOT_TOKEN":  "1:evil",
		"VIOLATION_THRESHOLD": "1000",
	} {
		if _, err := mgr.Apply(ctx, key, value); err == nil {
			t.Errorf("%s=%s должен отклоняться", key, value)
		}
	}
	if len(store.values) != 0 || provider.Load().Cooldown != 300 {
		t.Errorf("отклонённое значение не должно ничего менять: store=%v cooldown=%d", store.values, provider.Load().Cooldown)
	}
	if _, err := mgr.Reset(ctx, "TELEGRAM_BOT_TOKEN"); err == nil {
		t.Error("сброс нередактируемого параметра должен отклоняться")
	}
}

func TestManagerMem_StoreFailureKeepsConfig(t *testing.T) {
	mgr, store, provider := newMemManager(t, map[string]string{"COOLDOWN": "777"})
	store.err = errors.New("redis down")
	ctx := context.Background()

	if _, err := mgr.Apply(ctx, "COOLDOWN", "888"); err == nil {
		t.Error("сбой записи должен вернуться ошибкой")
	}
	if _, err := mgr.Reset(ctx, "COOLDOWN"); err == nil {
		t.Error("сбой удаления должен вернуться ошибкой")
	}
	if err := mgr.ResetAll(ctx); err == nil {
		t.Error("сбой очистки должен вернуться ошибкой")
	}
	if provider.Load().Cooldown != 777 {
		t.Errorf("при сбое хранилища рабочий конфиг не меняется, cooldown = %d", provider.Load().Cooldown)
	}
	if item, _ := mgr.Item("COOLDOWN"); !item.Overridden {
		t.Error("при сбое хранилища отметка об изменении остаётся")
	}
}

func TestManagerMem_ResetAndResetAll(t *testing.T) {
	mgr, store, provider := newMemManager(t, map[string]string{"COOLDOWN": "777", "TOLERANCE": "2"})
	ctx := context.Background()

	display, err := mgr.Reset(ctx, "COOLDOWN")
	if err != nil || display != "300" {
		t.Fatalf("Reset: %q, %v", display, err)
	}
	if provider.Load().Cooldown != 300 || provider.Load().Tolerance != 2 {
		t.Errorf("сброс одного ключа не трогает остальные: %+v", provider.Load())
	}
	if _, stored := store.values["COOLDOWN"]; stored || store.values["TOLERANCE"] != "2" {
		t.Errorf("store = %v", store.values)
	}

	if err := mgr.ResetAll(ctx); err != nil {
		t.Fatalf("ResetAll: %v", err)
	}
	if len(store.values) != 0 || provider.Load().Tolerance != 0 {
		t.Errorf("после полного сброса: store=%v tolerance=%d", store.values, provider.Load().Tolerance)
	}
}
