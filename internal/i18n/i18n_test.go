package i18n

import (
	"fmt"
	"strings"
	"testing"
)

func TestRestoreMessageFormat(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		SetLanguage(lang)

		got := fmt.Sprintf(T("restore.message"), int64(777))
		if strings.Contains(got, "%!") {
			t.Errorf("%s: неверный глагол формата в restore.message: %s", lang, got)
		}
		if !strings.Contains(got, "777") {
			t.Errorf("%s: ID пользователя не попал в сообщение: %s", lang, got)
		}
	}
}

func TestTranslationKeysMatchAcrossLocales(t *testing.T) {
	ru, en := translations["ru"], translations["en"]

	for key := range ru {
		if _, ok := en[key]; !ok {
			t.Errorf("ключ %q есть в ru, но отсутствует в en", key)
		}
	}
	for key := range en {
		if _, ok := ru[key]; !ok {
			t.Errorf("ключ %q есть в en, но отсутствует в ru", key)
		}
	}
}
