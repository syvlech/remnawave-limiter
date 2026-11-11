package telegram

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/i18n"
	"unicode/utf8"
)

func init() {
	i18n.SetLanguage("ru")
}

func row(header, value string) string {
	return tableRow(header, value)
}

func TestFormatManualAlert(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{
		UserID:          1234,
		Username:        "testuser",
		Email:           "test@example.com",
		SubscriptionURL: "https://example.com/sub/123",
		HWIDDeviceLimit: 3,
	}
	ips := []api.ActiveIP{
		{IP: "1.1.1.1", NodeName: "Node-DE", NodeUUID: "n1", ASN: 13335, ASNOrg: "Cloudflare, Inc."},
		{IP: "2.2.2.2", NodeName: "Node-US", NodeUUID: "n2"},
		{IP: "3.3.3.3", NodeName: "Node-NL", NodeUUID: "n3", ASN: 24940, ASNOrg: "Hetzner Online GmbH"},
		{IP: "4.4.4.4", NodeName: "Node-DE", NodeUUID: "n1"},
	}

	result := FormatManualAlert(user, ips, 3, 5, loc, 0, false, 0, false, nil)

	checks := []struct {
		name string
		want string
	}{
		{"heading", "<h3>⚠️ Превышение лимита устройств</h3>"},
		{"username", row("👤 Пользователь", "<code>testuser</code>")},
		{"limit", row("📊 Лимит", "<b>3</b>")},
		{"detected with asn count", row("📡 Обнаружено", "4 IP · 2 ASN")},
		{"overflow", row("🚦 Превышение", "<mark>+1</mark>")},
		{"violations", row("📈 Нарушений за 24 ч", "5")},
		{"subscription link", `<a href="https://example.com/sub/123">открыть</a>`},
		{"time is tg-time", `<tg-time unix="`},
		{"ip table open", "<details open><summary>📍 IP-адреса (4)</summary>"},
		{"ip1 link", `<a href="https://ipinfo.io/1.1.1.1">1.1.1.1</a>`},
		{"ip1 row", "<td>Cloudflare, Inc.</td><td>Node-DE</td>"},
		{"ip without asn", "<td>—</td><td>Node-US</td>"},
		{"ip3 org", "<td>Hetzner Online GmbH</td><td>Node-NL</td>"},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(result, c.want) {
				t.Errorf("expected result to contain %q, got:\n%s", c.want, result)
			}
		})
	}
	assertBalancedHTML(t, result)
}

func TestFormatManualAlert_NoOverflowRowWithinLimit(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	ips := []api.ActiveIP{{IP: "10.0.0.1", NodeName: "Local", NodeUUID: "n"}}

	result := FormatManualAlert(user, ips, 1, 1, time.UTC, 0, false, 0, false, nil)

	if strings.Contains(result, "Превышение</th>") {
		t.Errorf("count within limit must not show overflow row, got:\n%s", result)
	}
}

func TestFormatManualAlert_IPRowsCapped(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	ips := make([]api.ActiveIP, maxIPRows+5)
	for i := range ips {
		ips[i] = api.ActiveIP{IP: fmt.Sprintf("10.0.0.%d", i), NodeName: "N"}
	}

	result := FormatManualAlert(user, ips, 1, 1, time.UTC, 0, false, 0, false, nil)

	if got := strings.Count(result, "https://ipinfo.io/"); got != maxIPRows {
		t.Errorf("expected %d IP rows, got %d", maxIPRows, got)
	}
	if !strings.Contains(result, "… и ещё 5") {
		t.Errorf("expected remainder row, got:\n%s", result)
	}
}

func TestFormatAutoAlert(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UserID: 5678, Username: "autouser", Email: "auto@example.com"}
	ips := []api.ActiveIP{
		{IP: "5.5.5.5", NodeName: "Node-FR", NodeUUID: "n5"},
		{IP: "6.6.6.6", NodeName: "Node-UK", NodeUUID: "n6"},
	}

	result := FormatAutoAlert(user, ips, 2, 30, 3, loc, 0, false, 0, false, nil)

	checks := []struct{ name, want string }{
		{"auto title", "<h3>🔒 Подписка автоматически отключена</h3>"},
		{"username", "<code>autouser</code>"},
		{"duration", row("⏱ Отключена на", "30 мин")},
		{"restore time", "🔓 Включится</th><td><tg-time"},
		{"ip link", "https://ipinfo.io/5.5.5.5"},
		{"violations", row("📈 Нарушений за 24 ч", "3")},
		{"ip list collapsed", "<details><summary>"},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(result, c.want) {
				t.Errorf("expected result to contain %q, got:\n%s", c.want, result)
			}
		})
	}
	assertBalancedHTML(t, result)
}

func TestFormatAutoAlert_Permanent(t *testing.T) {
	user := &api.CachedUser{UserID: 9, Username: "permuser"}
	ips := []api.ActiveIP{{IP: "7.7.7.7", NodeName: "Node-JP", NodeUUID: "n7"}}

	result := FormatAutoAlert(user, ips, 1, 0, 1, time.UTC, 0, false, 0, false, nil)

	if !strings.Contains(result, row("⏱ Отключена на", "<b>Навсегда</b>")) {
		t.Errorf("expected permanent row for duration=0, got:\n%s", result)
	}
	if strings.Contains(result, "Включится") {
		t.Errorf("permanent disable must not show a restore time, got:\n%s", result)
	}
}

func TestFormatSoftAlert(t *testing.T) {
	user := &api.CachedUser{UserID: 9, Username: "soft"}
	ips := []api.ActiveIP{{IP: "7.7.7.7", NodeName: "N"}, {IP: "8.8.8.8", NodeName: "N"}}

	result := FormatSoftAlert(user, ips, 1, 3, time.UTC, 0, false, 0, false, nil)

	for _, want := range []string{
		"<h3>🔔 Превышение лимита в пределах допуска</h3>",
		row("🚫 Порог бана", "3"),
		"<blockquote>ℹ️ Бан не применён — превышение в пределах допуска</blockquote>",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q, got:\n%s", want, result)
		}
	}
	assertBalancedHTML(t, result)
}

func TestFormatManualAlert_English(t *testing.T) {
	i18n.SetLanguage("en")
	defer i18n.SetLanguage("ru")

	user := &api.CachedUser{UserID: 7, Username: "enuser"}
	ips := []api.ActiveIP{{IP: "8.8.8.8", NodeName: "Node-US", NodeUUID: "n8"}}

	result := FormatManualAlert(user, ips, 2, 1, time.UTC, 0, false, 0, false, nil)

	checks := []struct{ name, want string }{
		{"en title", "Device limit exceeded"},
		{"en user", "👤 User"},
		{"en violations", row("📈 Violations in 24h", "1")},
		{"en ips header", "IP addresses"},
		{"en provider column", "Provider"},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(result, c.want) {
				t.Errorf("expected result to contain %q, got:\n%s", c.want, result)
			}
		})
	}
}

func TestFormatManualAlert_SubnetGroupingHeader(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	ips := []api.ActiveIP{
		{IP: "1.1.1.1", NodeName: "A", NodeUUID: "n", ASN: 13335},
		{IP: "1.1.1.2", NodeName: "A", NodeUUID: "n", ASN: 13335},
		{IP: "2.2.2.2", NodeName: "A", NodeUUID: "n", ASN: 24940},
	}

	result := FormatManualAlert(user, ips, 1, 1, time.UTC, 2, true, 0, false, nil)

	if !strings.Contains(result, row("🌐 Подсетей", "<b>2</b>")) {
		t.Errorf("expected subnet row, got:\n%s", result)
	}
	if !strings.Contains(result, "3 IP · 2 ASN") {
		t.Errorf("expected '3 IP · 2 ASN', got:\n%s", result)
	}
	if !strings.Contains(result, "<mark>+1</mark>") {
		t.Errorf("expected overflow computed from subnets, got:\n%s", result)
	}
}

func TestFormatManualAlert_NoASN_OmitsCount(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	ips := []api.ActiveIP{
		{IP: "1.1.1.1", NodeName: "A", NodeUUID: "n"},
		{IP: "2.2.2.2", NodeName: "A", NodeUUID: "n"},
	}

	result := FormatManualAlert(user, ips, 1, 1, time.UTC, 0, false, 0, false, nil)

	if strings.Contains(result, " ASN<") || strings.Contains(result, "· 0") {
		t.Errorf("ASN count must be omitted when no IP has ASN, got:\n%s", result)
	}
}

func TestFormatManualAlert_ASNGroupingHeader(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	ips := []api.ActiveIP{
		{IP: "1.1.1.1", NodeName: "A", NodeUUID: "n", ASN: 13335},
		{IP: "1.1.1.2", NodeName: "A", NodeUUID: "n", ASN: 13335},
		{IP: "2.2.2.2", NodeName: "A", NodeUUID: "n", ASN: 24940},
	}

	result := FormatManualAlert(user, ips, 9, 1, time.UTC, 0, false, 2, true, nil)

	if !strings.Contains(result, row("🛰 ASN-групп", "<b>2</b>")) {
		t.Errorf("expected ASN groups row, got:\n%s", result)
	}
	if !strings.Contains(result, row("📡 Обнаружено", "3 IP")) {
		t.Errorf("ASN mode must not append the ASN count to detected IPs, got:\n%s", result)
	}
}

func TestFormatManualAlert_SubnetGroupingDisabled_NoSubnetLine(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	ips := []api.ActiveIP{{IP: "1.1.1.1", NodeName: "A", NodeUUID: "n"}}

	result := FormatManualAlert(user, ips, 1, 1, time.UTC, 0, false, 0, false, nil)

	if strings.Contains(result, "Подсетей") {
		t.Errorf("subnetEnabled=false must not show subnets, got:\n%s", result)
	}
}

func TestTruncateASNOrg(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"short_passes_through", "Hetzner", "Hetzner"},
		{"exactly_40_chars", strings.Repeat("a", 40), strings.Repeat("a", 40)},
		{"41_chars_truncates_to_39_plus_ellipsis", strings.Repeat("a", 41), strings.Repeat("a", 39) + "…"},
		{"47_chars_truncates_with_trailing_slash", "Amazon Data Services Ireland Limited / AWS EMEA", "Amazon Data Services Ireland Limited /…"},
		{"trailing_space_trimmed_before_ellipsis", strings.Repeat("a", 38) + " bb", strings.Repeat("a", 38) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateASNOrg(tc.input)
			if got != tc.want {
				t.Errorf("truncateASNOrg(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFormatManualAlert_LongASNOrgIsTruncated(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UserID: 1, Username: "u"}
	longOrg := "Amazon Data Services Ireland Limited / AWS EMEA SARL"
	ips := []api.ActiveIP{{IP: "52.0.0.1", NodeName: "N", NodeUUID: "n", ASN: 16509, ASNOrg: longOrg}}

	result := FormatManualAlert(user, ips, 1, 1, loc, 0, false, 0, false, nil)

	if strings.Contains(result, longOrg) {
		t.Errorf("full long org must not appear; expected truncated form, got:\n%s", result)
	}
	if !strings.Contains(result, "…") {
		t.Errorf("expected ellipsis in truncated ASN org, got:\n%s", result)
	}
}

func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"<b>bold</b>", "&lt;b&gt;bold&lt;/b&gt;"},
		{"a & b", "a &amp; b"},
	}
	for _, tc := range tests {
		got := escapeHTML(tc.input)
		if got != tc.want {
			t.Errorf("escapeHTML(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		minutes int
		want    string
	}{
		{0, "навсегда"},
		{-1, "навсегда"},
		{1, "1 мин"},
		{30, "30 мин"},
		{59, "59 мин"},
		{60, "1 ч"},
		{90, "1 ч 30 мин"},
		{120, "2 ч"},
		{1440, "1 д"},
		{1500, "1 д 1 ч"},
		{1441, "1 д 0 ч 1 мин"},
		{2880, "2 д"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d_minutes", tc.minutes), func(t *testing.T) {
			got := FormatDuration(tc.minutes)
			if got != tc.want {
				t.Errorf("FormatDuration(%d) = %q, want %q", tc.minutes, got, tc.want)
			}
		})
	}
}

func TestCountUniqueASN(t *testing.T) {
	tests := []struct {
		name string
		ips  []api.ActiveIP
		want int
	}{
		{"empty", nil, 0},
		{"all zero", []api.ActiveIP{{IP: "1.1.1.1"}, {IP: "2.2.2.2"}}, 0},
		{"one resolved", []api.ActiveIP{{IP: "1.1.1.1", ASN: 13335}}, 1},
		{
			"mix of zeros and duplicates",
			[]api.ActiveIP{
				{IP: "1.1.1.1", ASN: 13335},
				{IP: "2.2.2.2", ASN: 0},
				{IP: "3.3.3.3", ASN: 24940},
				{IP: "4.4.4.4", ASN: 13335},
			},
			2,
		},
		{
			"three unique",
			[]api.ActiveIP{
				{IP: "1.1.1.1", ASN: 13335},
				{IP: "2.2.2.2", ASN: 24940},
				{IP: "3.3.3.3", ASN: 16509},
			},
			3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := countUniqueASN(tc.ips)
			if got != tc.want {
				t.Errorf("countUniqueASN(...) = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFormatActionResult(t *testing.T) {
	tests := []struct {
		action string
		admin  string
		want   string
	}{
		{"drop", "admin1", "Подключения сброшены"},
		{"disable", "admin2", "Подписка отключена"},
		{"ignore", "admin3", "Добавлен в whitelist"},
		{"ignore_temp", "admin5", "Добавлен в whitelist временно"},
		{"enable", "admin4", "Подписка включена"},
	}

	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			result := FormatActionResult(tc.action, tc.admin)
			if !strings.Contains(result, tc.want) {
				t.Errorf("FormatActionResult(%q, %q) = %q, want to contain %q", tc.action, tc.admin, result, tc.want)
			}
			if !strings.Contains(result, tc.admin) {
				t.Errorf("expected result to contain admin name %q", tc.admin)
			}
		})
	}
}

func TestEscapeHTML_EscapesQuote(t *testing.T) {
	got := escapeHTML(`a"b<c&d>e`)
	want := "a&quot;b&lt;c&amp;d&gt;e"
	if got != want {
		t.Errorf("escapeHTML = %q, want %q", got, want)
	}
}

func TestIPTable_EscapesHostileValues(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: `<tg-button type="callback_data" data="disable:1">x</tg-button>`}
	out := FormatManualAlert(user, []api.ActiveIP{
		{IP: `1.1.1.1" onmouseover="x`, NodeName: `DE<1>`, ASNOrg: `Acme "ISP"`},
	}, 1, 1, time.UTC, 0, false, 0, false, nil)

	if strings.Contains(out, `" onmouseover="`) {
		t.Errorf("unescaped quote leaked into markup: %s", out)
	}
	if strings.Contains(out, "<1>") {
		t.Errorf("unescaped angle brackets leaked into markup: %s", out)
	}
	if strings.Contains(out, "<tg-button") {
		t.Errorf("username injected a button into the alert: %s", out)
	}
}

func TestFormatAlert_EscapesSubscriptionURL(t *testing.T) {
	user := &api.CachedUser{
		Username:        "user",
		SubscriptionURL: `https://e.com/s?a=1"><b>x</b>`,
	}
	out := FormatManualAlert(user, nil, 3, 1, time.UTC, 0, false, 0, false, nil)

	if strings.Contains(out, `"><b>x</b>`) {
		t.Errorf("subscription URL broke out of the href attribute: %s", out)
	}
}

func TestTruncateAnswer(t *testing.T) {
	short := "всё хорошо"
	if got := truncateAnswer(short); got != short {
		t.Errorf("short text must pass through, got %q", got)
	}

	long := strings.Repeat("я", 500)
	got := truncateAnswer(long)
	if runes := []rune(got); len(runes) > callbackAnswerMaxLen {
		t.Errorf("truncateAnswer returned %d runes, want <= %d", len(runes), callbackAnswerMaxLen)
	}
}

func TestTruncateASNOrg_MultibyteSafe(t *testing.T) {
	cyr40 := strings.Repeat("Ж", 40)
	if got := truncateASNOrg(cyr40); got != cyr40 {
		t.Errorf("40 кириллических символов должны проходить без обрезки, получено %q", got)
	}
	for _, in := range []string{
		strings.Repeat("Ж", 41),
		strings.Repeat("a", 38) + "ÔÔÔ tail",
		"Telefônica Brasil S.A — Пример очень длинного названия",
	} {
		got := truncateASNOrg(in)
		if !utf8.ValidString(got) {
			t.Errorf("truncateASNOrg(%q) дал невалидный UTF-8: %q", in, got)
		}
		if n := utf8.RuneCountInString(got); n > asnOrgMaxLen {
			t.Errorf("truncateASNOrg(%q) длиннее %d символов: %d", in, asnOrgMaxLen, n)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("truncateASNOrg(%q) = %q, ожидалось многоточие", in, got)
		}
	}
}
