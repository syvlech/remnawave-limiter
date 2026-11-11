package telegram

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/remnawave/limiter/internal/i18n"
)

const maxEvidenceRows = 10

type Evidence struct {
	Fetches *FetchEvidence
	Devices *DeviceEvidence
}

type FetchEvidence struct {
	Total int
	IPs   int
	ASNs  int
	Apps  int
	Span  time.Duration
	Rows  []FetchRow
}

type FetchRow struct {
	IP     string
	Org    string
	Apps   []string
	Count  int
	Online bool
}

type DeviceEvidence struct {
	Items []DeviceItem
}

type DeviceItem struct {
	Name string
	App  string
	Seen time.Time
}

func (ev *Evidence) hasFetches() bool { return ev != nil && ev.Fetches != nil && ev.Fetches.Total > 0 }
func (ev *Evidence) hasDevices() bool {
	return ev != nil && ev.Devices != nil && len(ev.Devices.Items) > 0
}

func writeEvidenceRows(b *strings.Builder, ev *Evidence, limit int) {
	if ev.hasFetches() {
		f := ev.Fetches
		value := fmt.Sprintf("<b>%d IP</b>", f.IPs)
		if f.ASNs > 0 {
			value += fmt.Sprintf(" · %d %s", f.ASNs, escapeHTML(i18n.T("alert.asn_count")))
		}
		if f.Apps > 0 {
			value += " · " + escapeHTML(fmt.Sprintf(i18n.T("alert.fetch_apps"), f.Apps))
		}
		b.WriteString(tableRow(i18n.T("alert.fetches"), value))
	}
	if ev.hasDevices() {
		value := fmt.Sprintf("<b>%d</b> / %d", len(ev.Devices.Items), limit)
		b.WriteString(tableRow(i18n.T("alert.hwid_devices"), value))
	}
}

func writeEvidenceDetails(b *strings.Builder, ev *Evidence, loc *time.Location) {
	if ev.hasFetches() {
		writeFetchTable(b, ev.Fetches)
	}
	if ev.hasDevices() {
		writeDeviceTable(b, ev.Devices, loc)
	}
}

func writeFetchTable(b *strings.Builder, f *FetchEvidence) {
	summary := strconv.Itoa(f.Total)
	if f.Span >= time.Minute {
		summary = fmt.Sprintf(i18n.T("alert.fetches_span"), f.Total, FormatDuration(int(f.Span/time.Minute)))
	}
	b.WriteString("<details>")
	b.WriteString("<summary>" + escapeHTML(i18n.T("alert.fetches")) + " (" + escapeHTML(summary) + ")</summary>")
	b.WriteString("<table striped compact><tr>")
	b.WriteString("<th align=\"left\">IP</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("alert.col_provider")) + "</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("alert.col_apps")) + "</th>")
	b.WriteString("<th align=\"right\">" + escapeHTML(i18n.T("alert.col_count")) + "</th>")
	b.WriteString("</tr>")

	online := false
	for i, row := range f.Rows {
		if i >= maxEvidenceRows {
			b.WriteString("<tr><td colspan=\"4\" align=\"center\">" +
				escapeHTML(fmt.Sprintf(i18n.T("alert.and_more"), len(f.Rows)-maxEvidenceRows)) + "</td></tr>")
			break
		}
		ip := escapeHTML(row.IP)
		if row.IP != "" {
			ip = "<a href=\"" + escapeHTML("https://ipinfo.io/"+url.PathEscape(row.IP)) + "\">" + ip + "</a>"
		} else {
			ip = "—"
		}
		if row.Online {
			ip = "🟢 " + ip
			online = true
		}
		org := strings.TrimSpace(row.Org)
		if org == "" {
			org = "—"
		} else {
			org = truncateASNOrg(org)
		}
		apps := strings.Join(row.Apps, ", ")
		if apps == "" {
			apps = "—"
		}
		b.WriteString("<tr>")
		b.WriteString("<td>" + ip + "</td>")
		b.WriteString("<td>" + escapeHTML(org) + "</td>")
		b.WriteString("<td>" + escapeHTML(apps) + "</td>")
		b.WriteString("<td align=\"right\">" + strconv.Itoa(row.Count) + "</td>")
		b.WriteString("</tr>")
	}
	if online {
		b.WriteString("<tr><td colspan=\"4\">" + escapeHTML(i18n.T("alert.online_legend")) + "</td></tr>")
	}
	b.WriteString("</table></details>")
}

func writeDeviceTable(b *strings.Builder, d *DeviceEvidence, loc *time.Location) {
	b.WriteString("<details>")
	b.WriteString(fmt.Sprintf("<summary>%s (%d)</summary>", escapeHTML(i18n.T("alert.hwid_devices")), len(d.Items)))
	b.WriteString("<table striped compact><tr>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("alert.col_device")) + "</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("alert.col_app")) + "</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("alert.col_seen")) + "</th>")
	b.WriteString("</tr>")
	for i, item := range d.Items {
		if i >= maxEvidenceRows {
			b.WriteString("<tr><td colspan=\"3\" align=\"center\">" +
				escapeHTML(fmt.Sprintf(i18n.T("alert.and_more"), len(d.Items)-maxEvidenceRows)) + "</td></tr>")
			break
		}
		app := item.App
		if app == "" {
			app = "—"
		}
		seen := "—"
		if !item.Seen.IsZero() {
			seen = item.Seen.In(loc).Format("02.01 15:04")
		}
		b.WriteString("<tr>")
		b.WriteString("<td>" + escapeHTML(item.Name) + "</td>")
		b.WriteString("<td>" + escapeHTML(app) + "</td>")
		b.WriteString("<td>" + escapeHTML(seen) + "</td>")
		b.WriteString("</tr>")
	}
	b.WriteString("</table></details>")
}
