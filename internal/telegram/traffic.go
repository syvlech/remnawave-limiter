package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/i18n"
)

const (
	mib = int64(1) << 20
	gib = int64(1) << 30
	tib = int64(1) << 40

	maxTrafficNodes = 3
)

type TrafficAlert struct {
	Heavy bool
	User  *api.CachedUser

	Bytes int64
	Day   time.Time
	Today bool
	At    time.Time

	Norm     int64
	NormDays int
	Ratio    float64

	HeavyBytes  int64
	HeavyDays   int
	HeavyWindow int
	WeekTotal   int64

	Nodes      []NodeBytes
	Violations int
	History    []int64
}

type NodeBytes struct {
	Name  string
	Bytes int64
}

func FormatTrafficAlert(a TrafficAlert, loc *time.Location) string {
	var b strings.Builder

	title := i18n.T("traffic.spike.title")
	if a.Heavy {
		title = i18n.T("traffic.heavy.title")
	}
	b.WriteString("<h3>" + escapeHTML(title) + "</h3>")
	b.WriteString("<table bordered striped compact>")
	writeUserRows(&b, a.User)

	if a.Today {
		b.WriteString(tableRow(i18n.T("traffic.today"), "<b>"+escapeHTML(FormatBytes(a.Bytes))+"</b> ("+
			escapeHTML(fmt.Sprintf(i18n.T("traffic.so_far"), a.At.In(loc).Format("15:04")))+")"))
	} else {
		b.WriteString(tableRow(i18n.T("traffic.day"), "<b>"+escapeHTML(FormatBytes(a.Bytes))+"</b> · "+
			escapeHTML(a.Day.Format("02.01"))))
	}

	if a.Heavy {
		label := fmt.Sprintf(i18n.T("traffic.heavy_days"), FormatBytes(a.HeavyBytes))
		b.WriteString(tableRow(label, "<mark>"+escapeHTML(fmt.Sprintf(i18n.T("traffic.days_of"), a.HeavyDays, a.HeavyWindow))+"</mark>"))
		b.WriteString(tableRow(i18n.T("traffic.week_total"), escapeHTML(FormatBytes(a.WeekTotal))))
	}

	norm := i18n.T("traffic.no_norm")
	if a.Norm > 0 {
		norm = fmt.Sprintf(i18n.T("traffic.norm_value"), FormatBytes(a.Norm), a.NormDays)
	}
	b.WriteString(tableRow(i18n.T("traffic.norm"), escapeHTML(norm)))
	if !a.Heavy && a.Ratio > 0 {
		b.WriteString(tableRow(i18n.T("traffic.ratio"), "<mark>"+escapeHTML(formatRatio(a.Ratio))+"</mark>"))
	}

	if len(a.Nodes) > 0 {
		parts := make([]string, 0, maxTrafficNodes)
		for i, n := range a.Nodes {
			if i >= maxTrafficNodes {
				break
			}
			parts = append(parts, n.Name+" "+FormatBytes(n.Bytes))
		}
		b.WriteString(tableRow(i18n.T("traffic.nodes"), escapeHTML(strings.Join(parts, " · "))))
	}
	if a.Violations >= 0 {
		b.WriteString(tableRow(i18n.T("traffic.violations"), strconv.Itoa(a.Violations)))
	}
	if len(a.History) > 0 {
		b.WriteString(tableRow(i18n.T("traffic.history"), "<code>"+escapeHTML(sparkline(a.History))+"</code>"))
	}
	b.WriteString(tableRow(i18n.T("alert.time"), tgTime(a.At, loc, "wDT")))
	b.WriteString("</table>")

	return b.String()
}

func FormatBytes(n int64) string {
	switch {
	case n < gib:
		return fmt.Sprintf("%d %s", n/mib, i18n.T("unit.mb"))
	case n < tib:
		v := float64(n) / float64(gib)
		if v < 10 {
			return fmt.Sprintf("%.1f %s", v, i18n.T("unit.gb"))
		}
		return fmt.Sprintf("%.0f %s", v, i18n.T("unit.gb"))
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(tib), i18n.T("unit.tb"))
}

func formatRatio(r float64) string {
	if r >= 10 {
		return fmt.Sprintf("×%.0f", r)
	}
	return fmt.Sprintf("×%.1f", r)
}

func sparkline(values []int64) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	var maxV int64
	for _, v := range values {
		if v > maxV {
			maxV = v
		}
	}
	out := make([]rune, 0, len(values))
	for _, v := range values {
		if v <= 0 || maxV == 0 {
			out = append(out, bars[0])
			continue
		}
		idx := 1 + int(float64(v)/float64(maxV)*float64(len(bars)-2))
		if idx >= len(bars) {
			idx = len(bars) - 1
		}
		out = append(out, bars[idx])
	}
	return string(out)
}

type HeavyTrafficReport struct {
	HeavyBytes int64
	MinDays    int
	Window     int
	Rows       []HeavyTrafficRow
	Total      int
}

type HeavyTrafficRow struct {
	Name string
	Days int
	Week int64
}
