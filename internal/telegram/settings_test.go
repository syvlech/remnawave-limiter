package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

type fakeSettings struct {
	items   []SettingItem
	applied []string
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{items: []SettingItem{
		{Key: "ACTION_MODE", Title: "Режим действий", Display: "manual", Kind: SettingEnum, Allowed: []string{"manual", "auto"}},
		{Key: "CHECK_INTERVAL", Title: "Интервал проверки (с)", Display: "60", Kind: SettingInt},
		{Key: "AUTO_DISABLE_DURATION", Title: "Авто-отключение (мин)", Display: "30", Kind: SettingInt},
		{Key: "ASN_GROUPING", Title: "Группировка по ASN", Display: "false", Kind: SettingBool, Allowed: []string{"true", "false"}},
		{Key: "FUTURE_KEY", Title: "Новый <параметр>", Display: "1", Kind: SettingInt},
	}}
}

func (f *fakeSettings) Items() []SettingItem { return append([]SettingItem(nil), f.items...) }

func (f *fakeSettings) Item(key string) (SettingItem, bool) {
	for _, it := range f.items {
		if it.Key == key {
			return it, true
		}
	}
	return SettingItem{}, false
}

func (f *fakeSettings) Apply(_ context.Context, key, raw string) (string, error) {
	for i, it := range f.items {
		if it.Key != key {
			continue
		}
		if it.Kind == SettingInt {
			if _, err := strconv.Atoi(raw); err != nil {
				return "", fmt.Errorf("ожидается целое число, получено %q", raw)
			}
		}
		f.items[i].Display = raw
		f.items[i].Overridden = true
		f.applied = append(f.applied, key+"="+raw)
		return raw, nil
	}
	return "", fmt.Errorf("unknown key %s", key)
}

func (f *fakeSettings) Reset(_ context.Context, key string) (string, error) {
	for i, it := range f.items {
		if it.Key == key {
			f.items[i].Overridden = false
			return it.Display, nil
		}
	}
	return "", fmt.Errorf("unknown key %s", key)
}

func (f *fakeSettings) ResetAll(context.Context) error {
	for i := range f.items {
		f.items[i].Overridden = false
	}
	return nil
}

func newSettingsBot(t *testing.T) (*Bot, *fakeAPI, *fakeSettings) {
	t.Helper()
	b, fake := newTestBot(t, 100, 0)
	fs := newFakeSettings()
	b.SetSettingsProvider(fs)
	return b, fake, fs
}

func adminMessage(text string, id int) *models.Update {
	return chatMessage(100, models.ChatTypePrivate, text, id)
}

func chatMessage(chatID int64, chatType models.ChatType, text string, id int) *models.Update {
	return &models.Update{
		ID: 2,
		Message: &models.Message{
			ID:   id,
			From: &models.User{ID: 42},
			Chat: models.Chat{ID: chatID, Type: chatType},
			Text: text,
		},
	}
}

func lastEditHTML(t *testing.T, fake *fakeAPI) string {
	t.Helper()
	edits := fake.find("editMessageText")
	if len(edits) == 0 {
		t.Fatal("editMessageText не вызывался")
	}
	return richHTML(t, edits[len(edits)-1].fields)
}

func TestSettingsCommand_SendsGroupedMenu(t *testing.T) {
	b, fake, _ := newSettingsBot(t)

	b.handleUpdate(context.Background(), nil, adminMessage("/settings", 9))

	calls := fake.find("sendRichMessage")
	if len(calls) != 1 {
		t.Fatalf("sendRichMessage вызван %d раз", len(calls))
	}
	html := richHTML(t, calls[0].fields)
	for _, want := range []string{
		"<h3>⚙️ Настройки лимитера</h3>",
		`<th colspan="2" align="left">🎯 Обнаружение</th>`,
		"<td>Интервал проверки (с)</td><td><code>60</code></td>",
		"<td>Авто-отключение (мин)</td><td><code>30</code> · 30 мин</td>",
		"<td>Группировка по ASN</td><td>⬜ выкл</td>",
		"🧩 Прочее",
		"<td>Новый &lt;параметр&gt;</td>",
		"📄 Все значения взяты из .env",
		`<tg-button type="disabled">♻️ Сбросить всё к .env</tg-button>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("меню не содержит %q:\n%s", want, html)
		}
	}
	if got := buttonData(html); got != "cfg:sec:detect,cfg:sec:actions,cfg:sec:grouping,cfg:sec:other,ui:stats" {
		t.Errorf("кнопки меню: %s", got)
	}
	assertBalancedHTML(t, html)
}

func TestSettingsSection_ShowsTogglesAndHints(t *testing.T) {
	b, fake, _ := newSettingsBot(t)

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:sec:grouping"))

	html := lastEditHTML(t, fake)
	for _, want := range []string{
		"<h3>🌐 Группировка</h3>",
		"<details><summary>💡 Что означают параметры</summary>",
		`data="cfg:toggle:ASN_GROUPING">⬜ Группировка по ASN</tg-button>`,
		`style="link" data="cfg:menu"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("раздел не содержит %q:\n%s", want, html)
		}
	}
	assertBalancedHTML(t, html)
}

func TestSettingsToggle_AppliesAndStaysInSection(t *testing.T) {
	b, fake, fs := newSettingsBot(t)

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:toggle:ASN_GROUPING"))

	if len(fs.applied) != 1 || fs.applied[0] != "ASN_GROUPING=true" {
		t.Fatalf("applied = %v", fs.applied)
	}
	html := lastEditHTML(t, fake)
	if !strings.Contains(html, "<blockquote>✅ Группировка по ASN: true — применено и сохранено</blockquote>") {
		t.Errorf("нет уведомления о применении:\n%s", html)
	}
	if !strings.Contains(html, `style="success" data="cfg:toggle:ASN_GROUPING">✅ Группировка по ASN`) {
		t.Errorf("включённый флаг должен быть зелёным:\n%s", html)
	}
}

func TestSettingsEdit_PresetsAndTypedValue(t *testing.T) {
	b, fake, fs := newSettingsBot(t)

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:edit:CHECK_INTERVAL"))

	html := lastEditHTML(t, fake)
	for _, want := range []string{
		"<h3>✏️ Интервал проверки (с)</h3>",
		`data="cfg:set:CHECK_INTERVAL:30">30</tg-button>`,
		`style="success" data="cfg:set:CHECK_INTERVAL:60">✅ 60</tg-button>`,
		"<code>CHECK_INTERVAL</code>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("экран редактирования не содержит %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "cfg:reset:") {
		t.Errorf("сброс доступен только для переопределённых значений:\n%s", html)
	}

	b.handleUpdate(context.Background(), nil, adminMessage("abc", 10))
	html = lastEditHTML(t, fake)
	if !strings.Contains(html, "<blockquote>❌ ожидается целое число") {
		t.Errorf("нет уведомления об ошибке:\n%s", html)
	}

	b.handleUpdate(context.Background(), nil, adminMessage("45", 11))
	if len(fs.applied) != 1 || fs.applied[0] != "CHECK_INTERVAL=45" {
		t.Fatalf("applied = %v", fs.applied)
	}
	edits := fake.find("editMessageText")
	if f := edits[len(edits)-1].fields; f["message_id"] != "5" {
		t.Errorf("редактироваться должно сообщение меню, а не %s", f["message_id"])
	}
	html = lastEditHTML(t, fake)
	if !strings.Contains(html, "<h3>🎯 Обнаружение</h3>") || !strings.Contains(html, "45 — применено и сохранено") {
		t.Errorf("после ввода должен открыться раздел с уведомлением:\n%s", html)
	}
	if n := len(fake.find("deleteMessage")); n != 2 {
		t.Errorf("введённые значения должны удаляться, deleteMessage вызван %d раз", n)
	}
}

func TestSettingsEdit_MinutePresetsAreHumanReadable(t *testing.T) {
	b, fake, _ := newSettingsBot(t)

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:edit:AUTO_DISABLE_DURATION"))

	html := lastEditHTML(t, fake)
	for _, want := range []string{
		`data="cfg:set:AUTO_DISABLE_DURATION:0">навсегда</tg-button>`,
		`data="cfg:set:AUTO_DISABLE_DURATION:1440">1 д</tg-button>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("нет пресета %q:\n%s", want, html)
		}
	}
}

func TestSettingsResetAll_RequiresConfirmation(t *testing.T) {
	b, fake, fs := newSettingsBot(t)
	if _, err := fs.Apply(context.Background(), "ACTION_MODE", "auto"); err != nil {
		t.Fatal(err)
	}

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:resetall"))
	html := lastEditHTML(t, fake)
	if !strings.Contains(html, "<li><b>Режим действий</b>: <code>auto</code></li>") ||
		buttonData(html) != "cfg:resetall:yes,cfg:menu" {
		t.Fatalf("ожидался экран подтверждения:\n%s", html)
	}
	if it, _ := fs.Item("ACTION_MODE"); !it.Overridden {
		t.Fatal("сброс не должен выполняться без подтверждения")
	}

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:resetall:yes"))
	if it, _ := fs.Item("ACTION_MODE"); it.Overridden {
		t.Fatal("после подтверждения значения должны быть сброшены")
	}
	if html := lastEditHTML(t, fake); !strings.Contains(html, "<h3>⚙️ Настройки лимитера</h3>") {
		t.Errorf("после сброса должно открыться меню:\n%s", html)
	}
}

func TestSettingsMenu_ResetEnabledWhenOverridden(t *testing.T) {
	b, _, fs := newSettingsBot(t)
	if _, err := fs.Apply(context.Background(), "CHECK_INTERVAL", "30"); err != nil {
		t.Fatal(err)
	}

	html := b.menuScreen(notice{}).render()
	if !strings.Contains(html, `style="danger" data="cfg:resetall"`) {
		t.Errorf("сброс должен быть доступен:\n%s", html)
	}
	if !strings.Contains(html, "<code>30</code> ♻️") || !strings.Contains(html, "♻️ Изменено в боте: 1") {
		t.Errorf("переопределённое значение должно быть отмечено:\n%s", html)
	}
}

func TestSettingsGroups_CoverRegistryKeysOnce(t *testing.T) {
	seen := map[string]string{}
	for _, g := range settingsGroups {
		for _, k := range g.keys {
			if prev, ok := seen[k]; ok {
				t.Errorf("ключ %s в разделах %s и %s", k, prev, g.id)
			}
			seen[k] = g.id
		}
	}
	for key, presets := range settingPresets {
		if _, ok := seen[key]; !ok {
			t.Errorf("пресеты для %s, но ключа нет ни в одном разделе", key)
		}
		for _, p := range presets {
			if len("cfg:set:"+key+":"+p) > 64 {
				t.Errorf("callback data для %s=%s длиннее 64 байт", key, p)
			}
		}
	}
}

func TestPendingInput_IgnoresOtherChats(t *testing.T) {
	b, fake, fs := newSettingsBot(t)
	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:edit:CHECK_INTERVAL"))

	b.handleUpdate(context.Background(), nil, chatMessage(-100500, models.ChatTypeSupergroup, "45", 10))

	if len(fs.applied) != 0 || len(fake.find("deleteMessage")) != 0 {
		t.Fatalf("сообщение из другого чата не должно применяться и удаляться: applied=%v", fs.applied)
	}
	b.handleUpdate(context.Background(), nil, adminMessage("45", 11))
	if len(fs.applied) != 1 || fs.applied[0] != "CHECK_INTERVAL=45" {
		t.Errorf("ожидание ввода должно было сохраниться, applied = %v", fs.applied)
	}
}

func TestPendingInput_GroupAcceptsOnlyNumbers(t *testing.T) {
	b, fake, fs := newSettingsBot(t)
	u := callbackUpdate(42, "cfg:edit:CHECK_INTERVAL")
	u.CallbackQuery.Message.Message.Chat = models.Chat{ID: -100500, Type: models.ChatTypeSupergroup}
	b.handleUpdate(context.Background(), nil, u)

	for i, text := range []string{"ок, смотрю", "", "45 секунд"} {
		b.handleUpdate(context.Background(), nil, chatMessage(-100500, models.ChatTypeSupergroup, text, 10+i))
	}
	if n := len(fake.find("deleteMessage")); n != 0 {
		t.Fatalf("обычные сообщения в группе не должны удаляться, deleteMessage вызван %d раз", n)
	}
	if len(fs.applied) != 0 {
		t.Fatalf("applied = %v", fs.applied)
	}

	b.handleUpdate(context.Background(), nil, chatMessage(-100500, models.ChatTypeSupergroup, "90", 20))
	if len(fs.applied) != 1 || fs.applied[0] != "CHECK_INTERVAL=90" {
		t.Errorf("число в группе должно применяться, applied = %v", fs.applied)
	}
	if n := len(fake.find("deleteMessage")); n != 1 {
		t.Errorf("введённое значение должно удаляться, deleteMessage вызван %d раз", n)
	}
}

func TestPendingInput_ErrorKeepsOriginalExpiry(t *testing.T) {
	b, _, _ := newSettingsBot(t)
	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:edit:CHECK_INTERVAL"))

	before, ok := b.pendingFor(42)
	if !ok {
		t.Fatal("ожидание ввода не установлено")
	}
	time.Sleep(10 * time.Millisecond)
	b.handleUpdate(context.Background(), nil, adminMessage("abc", 10))

	after, ok := b.pendingFor(42)
	if !ok {
		t.Fatal("после ошибки ввод должен ожидаться снова")
	}
	if !after.expires.Equal(before.expires) {
		t.Errorf("срок ожидания продлён: было %v, стало %v", before.expires, after.expires)
	}
}

func TestSettings_UneditableMenuIsResent(t *testing.T) {
	b, fake, fs := newSettingsBot(t)
	fake.rejectEdit = true

	b.handleUpdate(context.Background(), nil, inaccessibleCallbackUpdate(42, "cfg:edit:CHECK_INTERVAL"))

	sent := fake.find("sendRichMessage")
	if len(sent) != 1 || sent[0].fields["chat_id"] != "100" {
		t.Fatalf("экран редактирования должен прийти новым сообщением, sendRichMessage: %v", sent)
	}
	if !strings.Contains(richHTML(t, sent[0].fields), "<h3>✏️ Интервал проверки (с)</h3>") {
		t.Errorf("неожиданный экран: %s", richHTML(t, sent[0].fields))
	}

	fake.rejectEdit = false
	b.handleUpdate(context.Background(), nil, adminMessage("45", 11))
	if len(fs.applied) != 1 {
		t.Fatalf("applied = %v", fs.applied)
	}
	edits := fake.find("editMessageText")
	if last := edits[len(edits)-1].fields; last["message_id"] != "900" {
		t.Errorf("должно обновляться новое сообщение меню (900), а не %s", last["message_id"])
	}
}

func TestLooksNumeric(t *testing.T) {
	for s, want := range map[string]bool{
		"45": true, "0.5": true, "-1": true, "1e3": true,
		"": false, "abc": false, "45 секунд": false, "4 5": false,
	} {
		if got := looksNumeric(s); got != want {
			t.Errorf("looksNumeric(%q) = %v, want %v", s, got, want)
		}
	}
}
