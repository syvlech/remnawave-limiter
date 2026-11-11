package telegram

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
)

func sampleEvidence() *Evidence {
	return &Evidence{
		Fetches: &FetchEvidence{
			Total: 24, IPs: 2, ASNs: 2, Apps: 3, Span: 3 * time.Hour,
			Rows: []FetchRow{
				{IP: "1.1.1.1", Org: "Cloudflare", Apps: []string{"Happ", "v2rayNG"}, Count: 20},
				{IP: "2.2.2.2", Org: "Hetzner", Apps: []string{"Streisand"}, Count: 4, Online: true},
			},
		},
		Devices: &DeviceEvidence{Items: []DeviceItem{
			{Name: "iPhone15,2 · iOS 18.1", App: "Happ", Seen: time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC)},
			{Name: "Pixel 8 · Android 14"},
		}},
	}
}

func TestFormatManualAlert_WithEvidence(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	ips := []api.ActiveIP{{IP: "2.2.2.2", NodeName: "DE"}, {IP: "3.3.3.3", NodeName: "NL"}, {IP: "4.4.4.4", NodeName: "NL"}}
	out := FormatManualAlert(user, ips, 3, 1, time.UTC, 0, false, 0, false, sampleEvidence())

	for _, want := range []string{
		row("📥 Запросы подписки", "<b>2 IP</b> · 2 ASN · приложений: 3"),
		row("📱 HWID-устройства", "<b>2</b> / 3"),
		"<summary>📥 Запросы подписки (24 за 3 ч)</summary>",
		`<td>🟢 <a href="https://ipinfo.io/2.2.2.2">2.2.2.2</a></td>`,
		"<td>Happ, v2rayNG</td>",
		`<td align="right">20</td>`,
		"🟢 — с этого IP сейчас есть подключение",
		"<summary>📱 HWID-устройства (2)</summary>",
		"<td>iPhone15,2 · iOS 18.1</td><td>Happ</td><td>11.11 09:30</td>",
		"<td>Pixel 8 · Android 14</td><td>—</td><td>—</td>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в алерте нет %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "📥 Запросы подписки</th>") > strings.Index(out, "📈 Нарушений за 24 ч") {
		t.Error("сводка по запросам подписки должна стоять в основной таблице выше нарушений")
	}
	if strings.Index(out, "<summary>📥") < strings.Index(out, "<summary>📍") {
		t.Error("подробности признаков должны идти после списка IP")
	}
	if strings.Contains(out, "<details open><summary>📥") || strings.Contains(out, "<details open><summary>📱") {
		t.Error("блоки признаков должны быть свёрнуты, чтобы алерт оставался коротким")
	}
	assertBalancedHTML(t, out)
}

func TestFormatAlerts_EvidenceInAllKinds(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	ips := []api.ActiveIP{{IP: "2.2.2.2"}}
	for name, out := range map[string]string{
		"auto": FormatAutoAlert(user, ips, 1, 30, 1, time.UTC, 0, false, 0, false, sampleEvidence()),
		"soft": FormatSoftAlert(user, ips, 1, 3, time.UTC, 0, false, 0, false, sampleEvidence()),
	} {
		if !strings.Contains(out, "<summary>📥 Запросы подписки") || !strings.Contains(out, "<summary>📱 HWID-устройства") {
			t.Errorf("%s: признаки должны быть и в этом алерте:\n%s", name, out)
		}
		assertBalancedHTML(t, out)
	}
}

func TestFormatManualAlert_NoEvidenceNoBlocks(t *testing.T) {
	user := &api.CachedUser{UserID: 1, Username: "u"}
	for _, ev := range []*Evidence{nil, {}, {Fetches: &FetchEvidence{}, Devices: &DeviceEvidence{}}} {
		out := FormatManualAlert(user, []api.ActiveIP{{IP: "1.1.1.1"}}, 1, 1, time.UTC, 0, false, 0, false, ev)
		if strings.Contains(out, "📥") || strings.Contains(out, "📱 HWID") {
			t.Errorf("без данных блоков признаков быть не должно (%+v):\n%s", ev, out)
		}
	}
}

func TestFetchTable_ShortSpanAndCappedRows(t *testing.T) {
	f := &FetchEvidence{Total: 15, IPs: 15}
	for i := 0; i < 15; i++ {
		f.Rows = append(f.Rows, FetchRow{IP: fmt.Sprintf("10.0.0.%d", i), Count: 1})
	}
	var b strings.Builder
	writeFetchTable(&b, f)
	out := b.String()
	if !strings.Contains(out, "<summary>📥 Запросы подписки (15)</summary>") {
		t.Errorf("без промежутка времени в заголовке только число: %s", out)
	}
	if strings.Count(out, "ipinfo.io") != maxEvidenceRows || !strings.Contains(out, "… и ещё 5") {
		t.Errorf("строк должно быть не больше %d плюс «и ещё»: %s", maxEvidenceRows, out)
	}
	if strings.Contains(out, "🟢") {
		t.Error("без онлайн-адресов легенда не нужна")
	}
	assertBalancedHTML(t, out)
}

func TestEvidence_EscapesHostileValues(t *testing.T) {
	ev := &Evidence{
		Fetches: &FetchEvidence{Total: 1, IPs: 1, Rows: []FetchRow{{
			IP: `1.1.1.1" onclick="x`, Org: `<b>ISP</b>`, Apps: []string{`<tg-button>`}, Count: 1,
		}}},
		Devices: &DeviceEvidence{Items: []DeviceItem{{Name: `<script>`, App: `"quoted"`}}},
	}
	user := &api.CachedUser{UserID: 1, Username: "u"}
	out := FormatManualAlert(user, nil, 1, 1, time.UTC, 0, false, 0, false, ev)
	for _, bad := range []string{`" onclick="`, "<b>ISP</b>", "<tg-button>", "<script>"} {
		if strings.Contains(out, bad) {
			t.Errorf("неэкранированное значение %q попало в разметку:\n%s", bad, out)
		}
	}
	assertBalancedHTML(t, out)
}
