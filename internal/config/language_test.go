package config

import (
	"testing"

	"github.com/remnawave/limiter/internal/i18n"
)

func TestLoadConfig_ChineseLanguage(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	t.Setenv("LANGUAGE", "zh")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LANGUAGE=zh должен приниматься: %v", err)
	}
	if cfg.Language != "zh" {
		t.Errorf("Language = %q, ожидался zh", cfg.Language)
	}
}

func TestLoadConfig_ErrorsUseSelectedLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage("ru") })
	for _, tc := range []struct{ lang, want string }{
		{"ru", "REMNAWAVE_API_URL — обязательный параметр"},
		{"en", "REMNAWAVE_API_URL is required"},
		{"zh", "缺少必填参数 REMNAWAVE_API_URL"},
	} {
		clearEnv()
		i18n.SetLanguage(tc.lang)
		_, err := LoadConfig("")
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: ошибка = %v, ожидалась %q", tc.lang, err, tc.want)
		}
	}
}
