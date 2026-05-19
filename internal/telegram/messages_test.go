package telegram

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/i18n"
)

func init() {
	i18n.SetLanguage("ru")
}

func TestFormatManualAlert(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{
		UUID:            "uuid-123",
		UserID:          "1234",
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
	limit := 3

	result := FormatManualAlert(user, ips, limit, 5, loc, 0, false, 0, false)

	checks := []struct {
		name string
		want string
	}{
		{"contains title", "Превышение лимита устройств"},
		{"contains username", "<code>testuser</code>"},
		{"contains limit", "3"},
		{"contains ip count", "4 IP"},
		{"contains asn count", "4 IP (2 ASN)"},
		{"contains ip1 link", "https://ipinfo.io/1.1.1.1"},
		{"contains ip1 asn org", "Cloudflare, Inc."},
		{"contains ip3 asn org", "Hetzner Online GmbH"},
		{"contains node1", "(Node-DE)"},
		{"contains node2", "(Node-US)"},
		{"contains node3", "(Node-NL)"},
		{"contains subscription link", "https://example.com/sub/123"},
		{"contains profile link", "Профиль"},
		{"contains violation count", "Нарушений за 24ч: 5"},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(result, c.want) {
				t.Errorf("expected result to contain %q, got:\n%s", c.want, result)
			}
		})
	}
	if strings.Contains(result, "нода:") {
		t.Errorf("result must not contain old 'нода:' label, got:\n%s", result)
	}
}

func TestFormatManualAlert_IPWithoutASN_OmitsASNPart(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "1", Username: "u"}
	ips := []api.ActiveIP{{IP: "10.0.0.1", NodeName: "Local", NodeUUID: "n"}}

	result := FormatManualAlert(user, ips, 1, 1, loc, 0, false, 0, false)

	if !strings.Contains(result, "10.0.0.1</a> (Local)") {
		t.Errorf("expected IP line without ASN part, got:\n%s", result)
	}
	if strings.Contains(result, " - ") {
		t.Errorf("no ASN means no ' - ' separator, got:\n%s", result)
	}
}

func TestFormatManualAlert_IPWithASN_IncludesOrgDashFormat(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "1", Username: "u"}
	ips := []api.ActiveIP{{IP: "1.2.3.4", NodeName: "Chicago-1", NodeUUID: "n", ASN: 13335, ASNOrg: "Cloudflare, Inc."}}

	result := FormatManualAlert(user, ips, 1, 1, loc, 0, false, 0, false)

	wantSubstr := "1.2.3.4</a> - Cloudflare, Inc. (Chicago-1)"
	if !strings.Contains(result, wantSubstr) {
		t.Errorf("expected line to contain %q, got:\n%s", wantSubstr, result)
	}
}

func TestFormatAutoAlert(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{
		UUID:     "uuid-456",
		UserID:   "5678",
		Username: "autouser",
		Email:    "auto@example.com",
	}
	ips := []api.ActiveIP{
		{IP: "5.5.5.5", NodeName: "Node-FR", NodeUUID: "n5"},
		{IP: "6.6.6.6", NodeName: "Node-UK", NodeUUID: "n6"},
	}

	result := FormatAutoAlert(user, ips, 2, 30, 3, loc, 0, false, 0, false)

	checks := []struct{ name, want string }{
		{"contains auto title", "автоматически отключена"},
		{"contains username", "<code>autouser</code>"},
		{"contains duration", "30 мин"},
		{"contains ip link", "https://ipinfo.io/5.5.5.5"},
		{"contains violation count", "Нарушений за 24ч: 3"},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(result, c.want) {
				t.Errorf("expected result to contain %q, got:\n%s", c.want, result)
			}
		})
	}
}

func TestFormatAutoAlert_Permanent(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "9", Username: "permuser"}
	ips := []api.ActiveIP{{IP: "7.7.7.7", NodeName: "Node-JP", NodeUUID: "n7"}}

	result := FormatAutoAlert(user, ips, 1, 0, 1, loc, 0, false, 0, false)

	if !strings.Contains(result, "Перманентно") {
		t.Errorf("expected 'Перманентно' for duration=0, got:\n%s", result)
	}
}

func TestFormatManualAlert_English(t *testing.T) {
	i18n.SetLanguage("en")
	defer i18n.SetLanguage("ru")

	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "e", Username: "enuser"}
	ips := []api.ActiveIP{{IP: "8.8.8.8", NodeName: "Node-US", NodeUUID: "n8"}}

	result := FormatManualAlert(user, ips, 2, 1, loc, 0, false, 0, false)

	checks := []struct{ name, want string }{
		{"en title", "Device limit exceeded"},
		{"en user", "User"},
		{"en violations", "Violations in 24h: 1"},
		{"en ips header", "IP addresses"},
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
	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "1", Username: "u"}
	ips := []api.ActiveIP{
		{IP: "1.1.1.1", NodeName: "A", NodeUUID: "n", ASN: 13335},
		{IP: "1.1.1.2", NodeName: "A", NodeUUID: "n", ASN: 13335},
		{IP: "2.2.2.2", NodeName: "A", NodeUUID: "n", ASN: 24940},
	}

	result := FormatManualAlert(user, ips, 3, 1, loc, 2, true, 0, false)

	if !strings.Contains(result, "Подсетей: 2") {
		t.Errorf("expected 'Подсетей: 2' in header, got:\n%s", result)
	}
	if !strings.Contains(result, "3 IP (2 ASN)") {
		t.Errorf("expected '3 IP (2 ASN)' in header, got:\n%s", result)
	}
}

func TestFormatManualAlert_NoASN_OmitsCount(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "1", Username: "u"}
	ips := []api.ActiveIP{
		{IP: "1.1.1.1", NodeName: "A", NodeUUID: "n"},
		{IP: "2.2.2.2", NodeName: "A", NodeUUID: "n"},
	}

	result := FormatManualAlert(user, ips, 1, 1, loc, 0, false, 0, false)

	if strings.Contains(result, "ASN)") {
		t.Errorf("ASN count suffix must be omitted when no IP has ASN, got:\n%s", result)
	}
}

func TestFormatAutoAlert_ASNCount(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "9", Username: "autouser"}
	ips := []api.ActiveIP{
		{IP: "5.5.5.5", NodeName: "Node-FR", NodeUUID: "n5", ASN: 13335},
		{IP: "6.6.6.6", NodeName: "Node-UK", NodeUUID: "n6", ASN: 24940},
	}

	result := FormatAutoAlert(user, ips, 2, 30, 3, loc, 0, false, 0, false)

	if !strings.Contains(result, "2 IP (2 ASN)") {
		t.Errorf("expected '2 IP (2 ASN)' in auto alert header, got:\n%s", result)
	}
}

func TestFormatManualAlert_ASNGroupingHeader(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "1", Username: "u"}
	ips := []api.ActiveIP{
		{IP: "1.1.1.1", NodeName: "A", NodeUUID: "n", ASN: 13335},
		{IP: "1.1.1.2", NodeName: "A", NodeUUID: "n", ASN: 13335},
		{IP: "2.2.2.2", NodeName: "A", NodeUUID: "n", ASN: 24940},
	}

	result := FormatManualAlert(user, ips, 9, 1, loc, 0, false, 2, true)

	if !strings.Contains(result, "ASN-групп: 2") {
		t.Errorf("expected 'ASN-групп: 2' in header, got:\n%s", result)
	}
	if !strings.Contains(result, "3 IP") {
		t.Errorf("expected '3 IP' in header, got:\n%s", result)
	}
	if strings.Contains(result, "(2 ASN)") {
		t.Errorf("ASN-mode header must not also append '(N ASN)', got:\n%s", result)
	}
}

func TestFormatManualAlert_SubnetGroupingDisabled_NoSubnetLine(t *testing.T) {
	loc := time.UTC
	user := &api.CachedUser{UUID: "u", UserID: "1", Username: "u"}
	ips := []api.ActiveIP{{IP: "1.1.1.1", NodeName: "A", NodeUUID: "n"}}

	result := FormatManualAlert(user, ips, 1, 1, loc, 0, false, 0, false)

	if strings.Contains(result, "Подсетей") {
		t.Errorf("subnetEnabled=false must not show 'Подсетей' in header, got:\n%s", result)
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
	user := &api.CachedUser{UUID: "u", UserID: "1", Username: "u"}
	longOrg := "Amazon Data Services Ireland Limited / AWS EMEA SARL"
	ips := []api.ActiveIP{{IP: "52.0.0.1", NodeName: "N", NodeUUID: "n", ASN: 16509, ASNOrg: longOrg}}

	result := FormatManualAlert(user, ips, 1, 1, loc, 0, false, 0, false)

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
		action   string
		admin    string
		username string
		want     string
	}{
		{"drop", "admin1", "user1", "Подключения сброшены"},
		{"disable", "admin2", "user2", "Подписка отключена"},
		{"ignore", "admin3", "user3", "Добавлен в whitelist"},
		{"ignore_temp", "admin5", "user5", "Добавлен в whitelist временно"},
		{"enable", "admin4", "user4", "Подписка включена"},
	}

	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			result := FormatActionResult(tc.action, tc.admin, tc.username)
			if !strings.Contains(result, tc.want) {
				t.Errorf("FormatActionResult(%q, %q, %q) = %q, want to contain %q", tc.action, tc.admin, tc.username, result, tc.want)
			}
			if !strings.Contains(result, tc.admin) {
				t.Errorf("expected result to contain admin name %q", tc.admin)
			}
		})
	}
}
