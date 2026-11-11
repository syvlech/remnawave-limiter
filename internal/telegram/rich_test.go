package telegram

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/i18n"
)

var reTag = regexp.MustCompile(`<(/?)([a-zA-Z0-9-]+)[^>]*?(/?)>`)

var voidTags = map[string]bool{"br": true, "hr": true, "img": true, "input": true}

func assertBalancedHTML(t *testing.T, s string) {
	t.Helper()
	var stack []string
	for _, m := range reTag.FindAllStringSubmatch(s, -1) {
		closing, name, selfClosing := m[1] == "/", strings.ToLower(m[2]), m[3] == "/"
		if voidTags[name] || selfClosing {
			continue
		}
		if !closing {
			stack = append(stack, name)
			continue
		}
		if len(stack) == 0 || stack[len(stack)-1] != name {
			t.Fatalf("unbalanced </%s> (open: %v) in:\n%s", name, stack, s)
		}
		stack = stack[:len(stack)-1]
	}
	if len(stack) != 0 {
		t.Fatalf("unclosed tags %v in:\n%s", stack, s)
	}
}

func TestRichMsgRender_ButtonsAndStyles(t *testing.T) {
	m := richMsg{
		HTML: "<p>x</p>",
		Rows: [][]richButton{
			{cbButton("Drop", "drop:1", stylePrimary), cbButton(`Bad "x"`, "a:b", styleNone)},
			{urlButton("Open", "https://e.com/?a=1&b=2"), disabledButton("Off")},
		},
	}
	out := m.render()

	for _, want := range []string{
		`<tg-button-row><tg-button type="callback_data" style="primary" data="drop:1">Drop</tg-button>`,
		`<tg-button type="callback_data" data="a:b">Bad &quot;x&quot;</tg-button></tg-button-row>`,
		`<tg-button type="url" url="https://e.com/?a=1&amp;b=2">Open</tg-button>`,
		`<tg-button type="disabled">Off</tg-button>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render() missing %q:\n%s", want, out)
		}
	}
	assertBalancedHTML(t, out)
}

func TestRichButton_LinkStyleOnlyForCallbacks(t *testing.T) {
	btn := richButton{Text: "x", URL: "https://e.com", Style: styleLink}
	if strings.Contains(btn.html(), "style=") {
		t.Errorf("link style is allowed only for callback buttons: %s", btn.html())
	}
}

func TestRichMsgKeyboard_Fallback(t *testing.T) {
	m := richMsg{Rows: [][]richButton{
		{cbButton("A", "a:1", styleDanger), cbButton("B", "b:1", styleLink)},
		{urlButton("U", "https://e.com"), disabledButton("D")},
	}}
	kb := m.keyboard()

	if len(kb.InlineKeyboard) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(kb.InlineKeyboard))
	}
	if b := kb.InlineKeyboard[0][0]; b.CallbackData != "a:1" || b.Style != styleDanger {
		t.Errorf("unexpected first button %+v", b)
	}
	if b := kb.InlineKeyboard[0][1]; b.Style != "" {
		t.Errorf("link style must not leak into inline keyboard, got %q", b.Style)
	}
	if b := kb.InlineKeyboard[1][0]; b.URL != "https://e.com" {
		t.Errorf("unexpected url button %+v", b)
	}
	if b := kb.InlineKeyboard[1][1]; b.Disabled == nil {
		t.Errorf("expected disabled button, got %+v", b)
	}
}

func TestPlainText(t *testing.T) {
	in := `<h3>Title &amp; more</h3><table><tr><th>User</th><td><code>bob</code></td></tr></table>` +
		`<p>a<br/>b</p>`
	got := plainText(in)

	for _, want := range []string{"Title & more", "User  bob", "a\nb"} {
		if !strings.Contains(got, want) {
			t.Errorf("plainText missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<") {
		t.Errorf("plainText must drop tags:\n%s", got)
	}
}

func TestPlainText_KeepsLinkTargets(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"текст отличается от адреса", `<a href="https://e.com/sub/abc">открыть</a>`, "открыть (https://e.com/sub/abc)"},
		{"текст совпадает с адресом", `<a href="https://e.com/x">https://e.com/x</a>`, "https://e.com/x"},
		{"сущности в адресе", `<a href="https://e.com/?a=1&amp;b=&quot;2&quot;&amp;c=&lt;3&gt;">link</a>`, `link (https://e.com/?a=1&b="2"&c=<3>)`},
		{"сущность в адресе не раскрывается дважды", `<a href="https://e.com/?q=&amp;amp;">link</a>`, "link (https://e.com/?q=&amp;)"},
		{"совпадение после раскрытия сущностей", `<a href="https://e.com/?a=1&amp;b=2">https://e.com/?a=1&amp;b=2</a>`, "https://e.com/?a=1&b=2"},
		{"разметка внутри ссылки", `<a href="https://e.com"><b>сайт</b> &amp; ко</a>`, "сайт & ко (https://e.com)"},
		{"одинарные кавычки и лишние атрибуты", `<A class="x" HREF='https://e.com/a' target="_blank">a</A>`, "a (https://e.com/a)"},
		{"пустой текст", `<a href="https://e.com"></a>`, "https://e.com"},
		{"пустой адрес", `<a href="">текст</a>`, "текст"},
		{"без href", `<a name="x">текст</a>`, "текст"},
		{"не ссылка", `<abbr href="https://e.com">x</abbr>`, "x"},
		{"две ссылки", `<a href="https://a.io">1</a>, <a href="https://b.io">2</a>`, "1 (https://a.io), 2 (https://b.io)"},
	}
	for _, c := range cases {
		if got := plainText(c.in); got != c.want {
			t.Errorf("%s: plainText(%q) = %q, ожидалось %q", c.name, c.in, got, c.want)
		}
	}
}

func TestPlainText_ProfileRowKeepsSubscriptionURL(t *testing.T) {
	var b strings.Builder
	b.WriteString("<table>")
	writeUserRows(&b, &api.CachedUser{Username: "bob", SubscriptionURL: "https://panel.example/sub/a?x=1&y=2"})
	b.WriteString("</table>")

	got := plainText(b.String())
	if !strings.Contains(got, "(https://panel.example/sub/a?x=1&y=2)") {
		t.Errorf("ссылка на подписку потеряна в запасном тексте:\n%s", got)
	}
}

func TestRichToInput_DropsButtonsKeepsContent(t *testing.T) {
	raw := `[
		{"type":"heading","text":"Alert","size":3},
		{"type":"table","cells":[[{"text":"User","is_header":true,"align":"left","valign":"top"},{"text":{"type":"code","text":"bob"},"align":"left","valign":"top"}]],"is_bordered":true},
		{"type":"details","summary":"IPs","blocks":[{"type":"paragraph","text":"1.1.1.1"}],"is_open":true},
		{"type":"buttons","buttons":[{"text":"Drop","callback_data":"drop:1"}]},
		{"type":"something_new"}
	]`
	var blocks []models.RichBlock
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	in, dropped := richToInput(blocks)

	if dropped != 1 {
		t.Errorf("неизвестный блок должен считаться потерянным, dropped = %d", dropped)
	}
	if len(in) != 3 {
		t.Fatalf("expected heading, table and details, got %d blocks", len(in))
	}
	for _, blk := range in {
		if blk.Type == models.RichBlockTypeButtons {
			t.Errorf("buttons must be dropped")
		}
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input blocks: %v", err)
	}
	for _, want := range []string{`"type":"heading"`, `"is_bordered":true`, `"bob"`, `"is_open":true`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("round-trip lost %s: %s", want, out)
		}
	}
}

func TestFormatStats(t *testing.T) {
	stats := &cache.ViolationStats{
		Count24h:  3,
		CountWeek: 10,
		Top: []cache.ViolatorStat{
			{UserID: "1", Username: "alice", Count: 5},
			{UserID: "2", Count: 3},
			{UserID: "3", Username: "<carol>", Count: 1},
			{UserID: "4", Username: "dave", Count: 1},
		},
	}
	out := FormatStats(stats, time.UTC)

	for _, want := range []string{
		"<h3>📊 Статистика нарушений</h3>",
		`<td align="center"><b>3</b></td><td align="center"><b>10</b></td>`,
		`<td align="center">🥇</td><td><code>alice</code></td><td align="right"><b>5</b></td>`,
		`<td><code>2</code></td>`,
		`<code>&lt;carol&gt;</code>`,
		`<td align="center">4</td>`,
		"<footer>🕐 Обновлено <tg-time",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatStats missing %q:\n%s", want, out)
		}
	}
	assertBalancedHTML(t, out)
}

func TestFormatStats_Empty(t *testing.T) {
	out := FormatDailyReport(&cache.ViolationStats{}, time.UTC, nil)
	if !strings.Contains(out, "<blockquote>✅ Нарушений за неделю не зафиксировано</blockquote>") {
		t.Errorf("expected empty-state quote:\n%s", out)
	}
	if strings.Contains(out, "<h4>") {
		t.Errorf("empty stats must not render the top table:\n%s", out)
	}
	assertBalancedHTML(t, out)
}

func TestFormatStartupMessage(t *testing.T) {
	out := FormatStartupMessage("4.6.0", "auto", 60, 300, 1, 0.5, 0, 30, true, false, true, 24, false, true, time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC), 3, 600)

	for _, want := range []string{
		"<h3>🚀 Remnawave Limiter запущен</h3>",
		row("📦 Версия", "<code>4.6.0</code>"),
		row("⚙️ Режим", "<b>Автоматический</b>"),
		row("📊 Допуск", "1 · ×0.50"),
		row("📱 Лимит по умолчанию", "не ограничено"),
		row("🔔 Уведомления в пределах допуска", "✅ вкл"),
		row("🚦 Порог нарушений", "3 / 600с"),
		row("🌐 Группировка по подсетям", "✅ вкл · <code>/24</code>"),
		row("🔗 Webhook", "⬜ выкл"),
		row("🌍 MaxMind DB", "✅ загружена, сборка от 01.11.2026"),
		"<p>⭐ Если лимитер вам полезен, поставьте звезду репозиторию на GitHub",
		"<footer>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("startup missing %q:\n%s", want, out)
		}
	}
	assertBalancedHTML(t, out)
}

func TestFormatStartupMessage_MaxMindDateByLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage("ru") })
	built := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	for lang, want := range map[string]string{
		"en": "✅ loaded, built 1 Nov 2026",
		"zh": "✅ 已加载（2026-11-01 版）",
	} {
		i18n.SetLanguage(lang)
		out := FormatStartupMessage("4.6.0", "manual", 60, 300, 0, 0, 0, 0, false, false, false, 24, true, true, built, 1, 3600)
		if !strings.Contains(out, want) {
			t.Errorf("%s: нет %q:\n%s", lang, want, out)
		}
	}
	i18n.SetLanguage("ru")
	out := FormatStartupMessage("4.6.0", "manual", 60, 300, 0, 0, 0, 0, false, false, false, 24, true, true, time.Time{}, 1, 3600)
	if !strings.Contains(out, "✅ загружена</td>") {
		t.Errorf("без даты сборки — просто «загружена»:\n%s", out)
	}
}
