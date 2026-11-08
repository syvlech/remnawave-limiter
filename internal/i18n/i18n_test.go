package i18n

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestRestoreMessageFormat(t *testing.T) {
	t.Cleanup(func() { SetLanguage("ru") })
	for _, lang := range []string{"ru", "en", "zh"} {
		SetLanguage(lang)

		got := fmt.Sprintf(T("restore.message"), int64(777))
		if strings.Contains(got, "%!") {
			t.Errorf("%s: invalid format in restore.message: %s", lang, got)
		}
		if !strings.Contains(got, "777") {
			t.Errorf("%s: user ID missing from message: %s", lang, got)
		}
	}
}

func TestTranslationKeysMatchAcrossLocales(t *testing.T) {
	en := translations["en"]
	for lang, locale := range translations {
		for key := range en {
			if value, ok := locale[key]; !ok || value == "" {
				t.Errorf("%s: missing or empty translation for %q", lang, key)
			}
		}
		for key := range locale {
			if _, ok := en[key]; !ok {
				t.Errorf("%s: key %q is absent from en", lang, key)
			}
		}
	}
}

func TestChineseTranslationFormatting(t *testing.T) {
	format := regexp.MustCompile(`%(?:\[[0-9]+\])?[-+#0 ]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)
	tags := regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	for key, en := range translations["en"] {
		zh := translations["zh"][key]
		for name, pattern := range map[string]*regexp.Regexp{"format": format, "HTML": tags} {
			want := strings.Join(pattern.FindAllString(en, -1), "|")
			got := strings.Join(pattern.FindAllString(zh, -1), "|")
			if got != want {
				t.Errorf("%s: %s tokens = %q, want %q", key, name, got, want)
			}
		}
	}
}

func TestChineseLanguageSelection(t *testing.T) {
	t.Cleanup(func() { SetLanguage("ru") })
	SetLanguage("zh")
	if got := T("settings.on"); got != "开启" {
		t.Errorf("Chinese translation = %q, want 开启", got)
	}
	if got := T("missing.translation.key"); got != "missing.translation.key" {
		t.Errorf("unknown key = %q", got)
	}
}
