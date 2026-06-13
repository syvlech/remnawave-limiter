package settings

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/i18n"
	"github.com/remnawave/limiter/internal/telegram"
)

type Manager struct {
	provider *config.Provider
	cache    *cache.Cache
	envPath  string

	mu        sync.Mutex
	overrides map[string]string
}

func NewManager(provider *config.Provider, c *cache.Cache, envPath string, overrides map[string]string) *Manager {
	cp := make(map[string]string, len(overrides))
	for k, v := range overrides {
		if config.IsEditable(k) {
			cp[k] = v
		}
	}
	return &Manager{
		provider:  provider,
		cache:     c,
		envPath:   envPath,
		overrides: cp,
	}
}

func mapKind(k config.Kind) telegram.SettingKind {
	switch k {
	case config.KindFloat:
		return telegram.SettingFloat
	case config.KindBool:
		return telegram.SettingBool
	case config.KindEnum:
		return telegram.SettingEnum
	default:
		return telegram.SettingInt
	}
}

func (m *Manager) Items() []telegram.SettingItem {
	cfg := m.provider.Load()
	m.mu.Lock()
	defer m.mu.Unlock()

	fields := config.Registry()
	items := make([]telegram.SettingItem, 0, len(fields))
	for _, f := range fields {
		_, overridden := m.overrides[f.Key]
		items = append(items, telegram.SettingItem{
			Key:        f.Key,
			Title:      i18n.T(f.TitleKey),
			Display:    config.Display(cfg, f.Key),
			Kind:       mapKind(f.Kind),
			Allowed:    f.Allowed,
			Overridden: overridden,
		})
	}
	return items
}

func (m *Manager) Item(key string) (telegram.SettingItem, bool) {
	f, ok := config.FieldByKey(key)
	if !ok {
		return telegram.SettingItem{}, false
	}
	cfg := m.provider.Load()
	m.mu.Lock()
	_, overridden := m.overrides[key]
	m.mu.Unlock()
	return telegram.SettingItem{
		Key:        f.Key,
		Title:      i18n.T(f.TitleKey),
		Display:    config.Display(cfg, f.Key),
		Kind:       mapKind(f.Kind),
		Allowed:    f.Allowed,
		Overridden: overridden,
	}, true
}

func (m *Manager) Apply(ctx context.Context, key, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if err := config.ValidateRaw(key, raw); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	merged := m.cloneOverrides()
	merged[key] = raw

	newCfg, err := config.LoadConfigWithOverrides(m.envPath, merged)
	if err != nil {
		return "", err
	}

	if base := m.envValue(key, merged); base != "" {
		if config.Display(newCfg, key) == base {
			if err := m.cache.DeleteConfigOverride(ctx, key); err != nil {
				return "", fmt.Errorf("удаление из Redis: %w", err)
			}
			delete(m.overrides, key)
			m.provider.Store(newCfg)
			return config.Display(newCfg, key), nil
		}
	}

	if err := m.cache.SetConfigOverride(ctx, key, raw); err != nil {
		return "", fmt.Errorf("сохранение в Redis: %w", err)
	}

	m.overrides[key] = raw
	m.provider.Store(newCfg)
	return config.Display(newCfg, key), nil
}

func (m *Manager) envValue(key string, merged map[string]string) string {
	base := make(map[string]string, len(merged))
	for k, v := range merged {
		if k == key {
			continue
		}
		base[k] = v
	}
	baseCfg, err := config.LoadConfigWithOverrides(m.envPath, base)
	if err != nil {
		return ""
	}
	return config.Display(baseCfg, key)
}

func (m *Manager) Reset(ctx context.Context, key string) (string, error) {
	if !config.IsEditable(key) {
		return "", fmt.Errorf("параметр %q нельзя менять из бота", key)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	merged := m.cloneOverrides()
	delete(merged, key)

	newCfg, err := config.LoadConfigWithOverrides(m.envPath, merged)
	if err != nil {
		return "", err
	}

	if err := m.cache.DeleteConfigOverride(ctx, key); err != nil {
		return "", fmt.Errorf("удаление из Redis: %w", err)
	}

	delete(m.overrides, key)
	m.provider.Store(newCfg)
	return config.Display(newCfg, key), nil
}

func (m *Manager) ResetAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	newCfg, err := config.LoadConfigWithOverrides(m.envPath, nil)
	if err != nil {
		return err
	}

	if err := m.cache.ClearConfigOverrides(ctx); err != nil {
		return fmt.Errorf("очистка Redis: %w", err)
	}

	m.overrides = make(map[string]string)
	m.provider.Store(newCfg)
	return nil
}

func (m *Manager) cloneOverrides() map[string]string {
	cp := make(map[string]string, len(m.overrides))
	for k, v := range m.overrides {
		cp[k] = v
	}
	return cp
}
