package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/i18n"
)

type fakeSettings struct {
	items    []SettingItem
	applied  []string
	applyErr error
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{items: []SettingItem{
		{Key: "ACTION_MODE", Title: "Режим действий", Display: "manual", Kind: SettingEnum, Allowed: []string{"manual", "auto"}},
		{Key: "CHECK_INTERVAL", Title: "Интервал проверки (с)", Display: "60", Kind: SettingInt},
		{Key: "AUTO_DISABLE_DURATION", Title: "Автоотключение (мин)", Display: "30", Kind: SettingInt},
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
	if f.applyErr != nil {
		return "", f.applyErr
	}
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
		"<td>Автоотключение (мин)</td><td><code>30</code> · 30 мин</td>",
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

func TestSettingsScreens_CallbackDataFitsLimit(t *testing.T) {
	const limit = 64
	check := func(where string, m richMsg) {
		t.Helper()
		for _, row := range m.Rows {
			for _, btn := range row {
				if len(btn.Data) > limit {
					t.Errorf("%s: callback data %q занимает %d байт при лимите %d", where, btn.Data, len(btn.Data), limit)
				}
			}
		}
	}
	prefixes := func(m richMsg) map[string]bool {
		out := map[string]bool{}
		for _, row := range m.Rows {
			for _, btn := range row {
				if parts := strings.SplitN(btn.Data, ":", 3); len(parts) > 1 {
					out[parts[0]+":"+parts[1]] = true
				}
			}
		}
		return out
	}

	fields := config.Registry()
	if len(fields) == 0 {
		t.Fatal("реестр настроек пуст")
	}
	fs := &fakeSettings{}
	for _, f := range fields {
		it := SettingItem{Key: f.Key, Title: i18n.T(f.TitleKey), Allowed: f.Allowed, Overridden: true}
		switch f.Kind {
		case config.KindInt:
			it.Kind, it.Display = SettingInt, "1"
		case config.KindFloat:
			it.Kind, it.Display = SettingFloat, "1"
		case config.KindBool:
			it.Kind, it.Display = SettingBool, "true"
		case config.KindEnum:
			it.Kind = SettingEnum
			if len(f.Allowed) == 0 {
				t.Errorf("%s: у перечисления нет допустимых значений", f.Key)
				continue
			}
			it.Display = f.Allowed[0]
		default:
			t.Fatalf("%s: неизвестный тип настройки %v", f.Key, f.Kind)
		}
		fs.items = append(fs.items, it)
	}
	b, _ := newTestBot(t, 100, 0)
	b.SetSettingsProvider(fs)

	seen := map[string]bool{}
	note := func(m richMsg) {
		for p := range prefixes(m) {
			seen[p] = true
		}
	}

	menu := b.menuScreen(notice{})
	check("меню", menu)
	note(menu)
	for _, g := range groupedItems(fs.Items()) {
		section := b.sectionScreen(g.id, notice{})
		check("раздел "+g.id, section)
		note(section)
	}
	for _, it := range fs.items {
		edit := b.editScreen(it, notice{})
		check("экран "+it.Key, edit)
		note(edit)

		want := "cfg:set:" + it.Key + ":"
		for _, v := range it.Allowed {
			if it.Kind != SettingEnum {
				break
			}
			if !strings.Contains(edit.render(), `data="`+want+v+`"`) {
				t.Errorf("%s: на экране редактирования нет кнопки значения %q", it.Key, v)
			}
		}
	}
	confirm := b.resetAllScreen()
	check("подтверждение сброса", confirm)
	note(confirm)

	for _, p := range []string{"cfg:sec", "cfg:toggle", "cfg:edit", "cfg:set", "cfg:reset", "cfg:resetall", "cfg:menu"} {
		if !seen[p] {
			t.Errorf("проверка не увидела ни одной кнопки %s", p)
		}
	}

	for _, g := range settingsGroups {
		if data := "cfg:sec:" + g.id; len(data) > limit {
			t.Errorf("callback data %q длиннее %d байт", data, limit)
		}
		for _, key := range g.keys {
			for _, prefix := range []string{"cfg:toggle:", "cfg:edit:", "cfg:reset:"} {
				if data := prefix + key; len(data) > limit {
					t.Errorf("callback data %q длиннее %d байт", data, limit)
				}
			}
		}
	}
}

func TestSettingsNotice_EscapesErrorText(t *testing.T) {
	const raw = `CHECK_INTERVAL должен быть > 0 & < 100, получено "<b>-5</b>"`
	const escaped = `<blockquote>❌ CHECK_INTERVAL должен быть &gt; 0 &amp; &lt; 100, получено &quot;&lt;b&gt;-5&lt;/b&gt;&quot;</blockquote>`

	if got := (notice{text: raw, isErr: true}).html(); got != escaped {
		t.Errorf("текст уведомления должен экранироваться:\n got %s\nwant %s", got, escaped)
	}

	b, fake, fs := newSettingsBot(t)
	fs.applyErr = errors.New(raw)

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:edit:CHECK_INTERVAL"))
	b.handleUpdate(context.Background(), nil, adminMessage("-5", 10))
	typed := lastEditHTML(t, fake)

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:set:CHECK_INTERVAL:30"))
	preset := lastEditHTML(t, fake)

	b.handleUpdate(context.Background(), nil, callbackUpdate(42, "cfg:toggle:ASN_GROUPING"))
	toggled := lastEditHTML(t, fake)

	for name, html := range map[string]string{"ввод значения": typed, "пресет": preset, "переключатель": toggled} {
		if !strings.Contains(html, escaped) {
			t.Errorf("%s: текст ошибки попал в rich HTML без экранирования:\n%s", name, html)
		}
		assertBalancedHTML(t, html)
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
		"+7": true, "007": true, "1.": true, ".5": true, "-0.25": true,
		"1E3": true, "1e+3": true, "2.5e-3": true,
		"": false, "abc": false, "45 секунд": false, "4 5": false,
		"nan": false, "NaN": false, "inf": false, "+Inf": false, "-inf": false,
		"Infinity": false, "infinity": false,
		"0x1p4": false, "0x10": false, "0b11": false, "0o17": false,
		"1_0": false, "1_000": false,
		".": false, "-": false, "+": false, "e3": false, "1e": false, "1e+": false,
		"1.2.3": false, "--1": false, "1,5": false, "1-2": false,
		" 45": false, "45 ": false, "45\n": false, "45\n46": false, "\t45": false,
		"٤٥": false, "４５": false, "45%": false, "/45": false,
	} {
		if got := looksNumeric(s); got != want {
			t.Errorf("looksNumeric(%q) = %v, want %v", s, got, want)
		}
	}
}
