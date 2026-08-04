package config

import (
	"testing"

	"github.com/remnawave/limiter/internal/i18n"
)

// Добавление рантайм-настройки требует четырёх согласованных правок:
// поле в Config + строка загрузчика, запись в registry, case в Display и
// переводы в обеих картах i18n. Тест ловит забытую правку.
func TestRegistry_EveryFieldIsFullyWired(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	for _, f := range Registry() {
		t.Run(f.Key, func(t *testing.T) {
			if Display(cfg, f.Key) == "" {
				t.Errorf("Display не знает ключ %s — забыт case в config.Display", f.Key)
			}

			for _, lang := range []string{"ru", "en"} {
				i18n.SetLanguage(lang)
				if got := i18n.T(f.TitleKey); got == f.TitleKey {
					t.Errorf("нет перевода %s для языка %s", f.TitleKey, lang)
				}
			}
			i18n.SetLanguage("ru")

			if f.Kind == KindBool || f.Kind == KindEnum {
				if len(f.Allowed) == 0 {
					t.Errorf("%s: для bool/enum нужен непустой Allowed", f.Key)
				}
				// Текущее значение обязано быть одним из допустимых,
				// иначе кнопка «✅» в меню не подсветится никогда.
				current := Display(cfg, f.Key)
				if !contains(f.Allowed, current) {
					t.Errorf("%s: текущее значение %q отсутствует в Allowed %v", f.Key, current, f.Allowed)
				}
			}
		})
	}
}

// Значение из бота проходит ValidateRaw, а затем полную перезагрузку
// конфигурации: оба слоя должны принимать одни и те же значения.
func TestRegistry_AllowedValuesPassFullValidation(t *testing.T) {
	for _, f := range Registry() {
		if len(f.Allowed) == 0 {
			continue
		}
		for _, v := range f.Allowed {
			t.Run(f.Key+"="+v, func(t *testing.T) {
				clearEnv()
				setRequiredEnv()

				if err := ValidateRaw(f.Key, v); err != nil {
					t.Fatalf("ValidateRaw отклонил допустимое значение: %v", err)
				}
				if _, err := LoadConfigWithOverrides("", map[string]string{f.Key: v}); err != nil {
					t.Errorf("LoadConfigWithOverrides отклонил допустимое значение: %v", err)
				}
			})
		}
	}
}

func TestRegistry_UneditableKeysRejected(t *testing.T) {
	// Секреты и структурные ключи не должны меняться из бота.
	for _, key := range []string{
		"REMNAWAVE_API_TOKEN", "TELEGRAM_BOT_TOKEN", "REDIS_URL",
		"TIMEZONE", "LANGUAGE", "LOG_FORMAT", "WEBHOOK_SECRET", "HEALTH_ADDR",
	} {
		if IsEditable(key) {
			t.Errorf("%s не должен быть доступен для правки из бота", key)
		}
		if err := ValidateRaw(key, "whatever"); err == nil {
			t.Errorf("ValidateRaw(%s) должен возвращать ошибку", key)
		}
	}
}

func TestValidateRaw_TypeChecking(t *testing.T) {
	cases := []struct {
		key, raw string
		wantErr  bool
	}{
		{"CHECK_INTERVAL", "60", false},
		{"CHECK_INTERVAL", "60.5", true},
		{"CHECK_INTERVAL", "abc", true},
		{"TOLERANCE_MULTIPLIER", "0.25", false},
		{"TOLERANCE_MULTIPLIER", "0,25", true},
		{"ACTION_MODE", "auto", false},
		{"ACTION_MODE", "AUTO", true},
		{"LOG_LEVEL", "debug", false},
		{"LOG_LEVEL", "verbose", true},
		{"DAILY_REPORT", "true", false},
		{"DAILY_REPORT", "yes", true},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.raw, func(t *testing.T) {
			err := ValidateRaw(tc.key, tc.raw)
			if tc.wantErr && err == nil {
				t.Errorf("ожидалась ошибка для %s=%q", tc.key, tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("неожиданная ошибка для %s=%q: %v", tc.key, tc.raw, err)
			}
		})
	}
}
