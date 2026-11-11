package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
)

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{
		512 * mib:     "512 МБ",
		gib * 34 / 10: "3.4 ГБ",
		27 * gib:      "27 ГБ",
		tib * 12 / 10: "1.2 ТБ",
	} {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, ожидалось %q", n, got, want)
		}
	}
}

func TestSparkline(t *testing.T) {
	if got := sparkline([]int64{0, 1, 4, 8}); got != "▁▂▅█" {
		t.Errorf("sparkline = %q", got)
	}
	if got := sparkline([]int64{0, 0}); got != "▁▁" {
		t.Errorf("пустая история = %q", got)
	}
}

func TestFormatTrafficAlert_SpikeToday(t *testing.T) {
	at := time.Date(2026, 11, 12, 11, 30, 0, 0, time.UTC)
	out := FormatTrafficAlert(TrafficAlert{
		User: &api.CachedUser{UserID: 1, Username: "ivan"}, Bytes: 34 * gib, Day: at, Today: true, At: at,
		Norm: 3 * gib, NormDays: 14, Ratio: 11.3,
		Nodes:      []NodeBytes{{"DE-1", 22 * gib}, {"NL-1", 12 * gib}, {"FI-1", gib}, {"X", gib}},
		Violations: 2, History: []int64{3 * gib, 0, 34 * gib},
	}, time.UTC)
	for _, want := range []string{
		"<h3>📈 Необычный трафик</h3>",
		row("📊 Сегодня", "<b>34 ГБ</b> (к 11:30)"),
		row("📏 Норма", "3.0 ГБ в день (медиана за 14 дн. с трафиком)"),
		row("🚀 Превышение", "<mark>×11</mark>"),
		row("🖥 Ноды", "DE-1 22 ГБ · NL-1 12 ГБ · FI-1 1.0 ГБ"),
		row("🚨 Нарушений лимита за неделю", "2"),
		row("📉 14 дней", "<code>▂▁█</code>"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в алерте нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "📅") || strings.Contains(out, "X 1.0") {
		t.Errorf("в алерте о всплеске нет строк постоянного трафика и не больше 3 нод:\n%s", out)
	}
	assertBalancedHTML(t, out)
}

func TestFormatTrafficAlert_HeavyDayAndEscaping(t *testing.T) {
	day := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	out := FormatTrafficAlert(TrafficAlert{
		Heavy: true, User: &api.CachedUser{UserID: 1, Username: "<b>x</b>"}, Bytes: 150 * gib, Day: day, At: day,
		HeavyBytes: 100 * gib, HeavyDays: 6, HeavyWindow: 7, WeekTotal: 870 * gib,
		Nodes: []NodeBytes{{"<script>", gib}}, Violations: -1,
	}, time.UTC)
	for _, want := range []string{
		"<h3>📈 Постоянно высокий трафик</h3>",
		row("📊 За сутки", "<b>150 ГБ</b> · 11.11"),
		row("📅 Дней от 100 ГБ", "<mark>6 из 7</mark>"),
		row("📦 За 7 дней", "870 ГБ"),
		row("📏 Норма", "нет данных"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в алерте нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "🚀") || strings.Contains(out, "🚨") {
		t.Errorf("без нормы нет превышения, без данных нет строки нарушений:\n%s", out)
	}
	if strings.Contains(out, "<script>") || strings.Contains(out, "<b>x</b>") {
		t.Errorf("неэкранированные значения:\n%s", out)
	}
	assertBalancedHTML(t, out)
}

func TestFormatDailyReport_HeavySection(t *testing.T) {
	stats := &cache.ViolationStats{}
	if out := FormatDailyReport(stats, time.UTC, nil); strings.Contains(out, "Постоянно высокий") {
		t.Errorf("без мониторинга трафика раздела нет:\n%s", out)
	}

	out := FormatDailyReport(stats, time.UTC, &HeavyTrafficReport{
		HeavyBytes: 100 * gib, MinDays: 5, Window: 7, Total: 12,
		Rows: []HeavyTrafficRow{{Name: "seedbox", Days: 7, Week: 7680 * gib}, {Name: "<b>x</b>", Days: 5, Week: 850 * gib}},
	})
	for _, want := range []string{
		"<h4>📈 Постоянно высокий трафик</h4>",
		"<p>от 100 ГБ в 5 из 7 суток</p>",
		`<td align="center">1</td><td><code>seedbox</code></td><td align="right">7 / 7</td><td align="right"><b>7.5 ТБ</b></td>`,
		"… и ещё 10",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в отчёте нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<b>x</b>") || strings.Contains(out, "🥇") {
		t.Errorf("имена экранируются, медалей у трафика нет:\n%s", out)
	}
	if strings.Index(out, "📈 Постоянно") > strings.Index(out, "<footer>") {
		t.Error("раздел должен стоять до подписи «Обновлено»")
	}
	assertBalancedHTML(t, out)

	empty := FormatDailyReport(stats, time.UTC, &HeavyTrafficReport{HeavyBytes: 100 * gib, MinDays: 5, Window: 7})
	if !strings.Contains(empty, "✅ Пользователей с постоянно высоким трафиком за неделю нет") {
		t.Errorf("пустой раздел:\n%s", empty)
	}
}
