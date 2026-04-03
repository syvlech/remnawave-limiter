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
		{IP: "1.1.1.1", NodeName: "Node-DE", NodeUUID: "n1"},
		{IP: "2.2.2.2", NodeName: "Node-US", NodeUUID: "n2"},
		{IP: "3.3.3.3", NodeName: "Node-NL", NodeUUID: "n3"},
		{IP: "4.4.4.4", NodeName: "Node-DE", NodeUUID: "n1"},
	}
	limit := 3

	result := FormatManualAlert(user, ips, limit, 5, loc)

	checks := []struct {
		name string
		want string
	}{
		{"contains title", "Превышение лимита устройств"},
		{"contains username", "<code>testuser</code>"},
		{"contains limit", "3"},
		{"contains ip count", "4 IP"},
		{"contains ip1 link", "https://ipinfo.io/1.1.1.1"},
		{"contains ip2 link", "https://ipinfo.io/2.2.2.2"},
		{"contains ip3 link", "https://ipinfo.io/3.3.3.3"},
		{"contains ip4 link", "https://ipinfo.io/4.4.4.4"},
		{"contains node1", "Node-DE"},
		{"contains node2", "Node-US"},
		{"contains node3", "Node-NL"},
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
	limit := 2
	duration := 30

	result := FormatAutoAlert(user, ips, limit, duration, 3, loc)

	checks := []struct {
		name string
		want string
	}{
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
	user := &api.CachedUser{
		UUID:     "uuid-789",
		UserID:   "9012",
		Username: "permuser",
		Email:    "perm@example.com",
	}
	ips := []api.ActiveIP{
		{IP: "7.7.7.7", NodeName: "Node-JP", NodeUUID: "n7"},
	}

	result := FormatAutoAlert(user, ips, 1, 0, 1, loc)

	if !strings.Contains(result, "Перманентно") {
		t.Errorf("expected result to contain 'Перманентно' for duration=0, got:\n%s", result)
	}
}

func TestFormatManualAlert_English(t *testing.T) {
	i18n.SetLanguage("en")
	defer i18n.SetLanguage("ru")

	loc := time.UTC
	user := &api.CachedUser{
		UUID:     "uuid-en",
		UserID:   "en01",
		Username: "enuser",
	}
	ips := []api.ActiveIP{
		{IP: "8.8.8.8", NodeName: "Node-US", NodeUUID: "n8"},
	}

	result := FormatManualAlert(user, ips, 2, 1, loc)

	checks := []struct {
		name string
		want string
	}{
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
		{"enable", "admin4", "user4", "Подписка включена"},
	}

	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			result := FormatActionResult(tc.action, tc.admin, tc.username)
			if !strings.Contains(result, tc.want) {
				t.Errorf("FormatActionResult(%q, %q, %q) = %q, want to contain %q",
					tc.action, tc.admin, tc.username, result, tc.want)
			}
			if !strings.Contains(result, tc.admin) {
				t.Errorf("expected result to contain admin name %q", tc.admin)
			}
		})
	}
}
