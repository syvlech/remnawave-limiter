package telegram

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/i18n"
)

const (
	asnOrgMaxLen = 40
	maxIPRows    = 30
)

func FormatManualAlert(user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, loc *time.Location, subnetGroups int, subnetEnabled bool, asnGroups int, asnEnabled bool) string {
	var b strings.Builder

	b.WriteString("<h3>" + escapeHTML(i18n.T("alert.manual.title")) + "</h3>")
	b.WriteString("<table bordered striped compact>")
	writeUserRows(&b, user)
	writeGroupingRows(&b, limit, ips, subnetGroups, subnetEnabled, asnGroups, asnEnabled)
	b.WriteString(tableRow(i18n.T("alert.violations_24h"), strconv.FormatInt(violationCount, 10)))
	b.WriteString(tableRow(i18n.T("alert.time"), tgTime(time.Now(), loc, "wDT")))
	b.WriteString("</table>")

	writeIPTable(&b, ips, true)

	return b.String()
}

func FormatAutoAlert(user *api.CachedUser, ips []api.ActiveIP, limit, durationMinutes int, violationCount int64, loc *time.Location, subnetGroups int, subnetEnabled bool, asnGroups int, asnEnabled bool) string {
	var b strings.Builder
	now := time.Now()

	b.WriteString("<h3>" + escapeHTML(i18n.T("alert.auto.title")) + "</h3>")
	b.WriteString("<table bordered striped compact>")
	writeUserRows(&b, user)
	writeGroupingRows(&b, limit, ips, subnetGroups, subnetEnabled, asnGroups, asnEnabled)
	b.WriteString(tableRow(i18n.T("alert.violations_24h"), strconv.FormatInt(violationCount, 10)))
	if durationMinutes == 0 {
		b.WriteString(tableRow(i18n.T("alert.disabled_for"), "<b>"+escapeHTML(i18n.T("alert.permanent"))+"</b>"))
	} else {
		b.WriteString(tableRow(i18n.T("alert.disabled_for"), escapeHTML(FormatDuration(durationMinutes))))
		restoreAt := now.Add(time.Duration(durationMinutes) * time.Minute)
		b.WriteString(tableRow(i18n.T("alert.restores_at"), tgTime(restoreAt, loc, "wDT")))
	}
	b.WriteString(tableRow(i18n.T("alert.time"), tgTime(now, loc, "wDT")))
	b.WriteString("</table>")

	writeIPTable(&b, ips, false)

	return b.String()
}

func FormatSoftAlert(user *api.CachedUser, ips []api.ActiveIP, limit, banThreshold int, loc *time.Location, subnetGroups int, subnetEnabled bool, asnGroups int, asnEnabled bool) string {
	var b strings.Builder

	b.WriteString("<h3>" + escapeHTML(i18n.T("alert.soft.title")) + "</h3>")
	b.WriteString("<table bordered striped compact>")
	writeUserRows(&b, user)
	writeGroupingRows(&b, limit, ips, subnetGroups, subnetEnabled, asnGroups, asnEnabled)
	b.WriteString(tableRow(i18n.T("alert.ban_threshold"), strconv.Itoa(banThreshold)))
	b.WriteString(tableRow(i18n.T("alert.time"), tgTime(time.Now(), loc, "wDT")))
	b.WriteString("</table>")
	b.WriteString("<blockquote>" + escapeHTML(i18n.T("alert.soft.note")) + "</blockquote>")

	writeIPTable(&b, ips, false)

	return b.String()
}

func FormatStats(stats *cache.ViolationStats, loc *time.Location) string {
	return formatStatsMessage(i18n.T("stats.title"), stats, loc)
}

func FormatDailyReport(stats *cache.ViolationStats, loc *time.Location) string {
	return formatStatsMessage(i18n.T("stats.daily_title"), stats, loc)
}

func formatStatsMessage(title string, stats *cache.ViolationStats, loc *time.Location) string {
	var b strings.Builder

	b.WriteString("<h3>" + escapeHTML(title) + "</h3>")
	b.WriteString("<table bordered compact><tr>")
	b.WriteString("<th align=\"center\">" + escapeHTML(i18n.T("stats.last_24h")) + "</th>")
	b.WriteString("<th align=\"center\">" + escapeHTML(i18n.T("stats.last_week")) + "</th>")
	b.WriteString("</tr><tr>")
	b.WriteString(fmt.Sprintf("<td align=\"center\"><b>%d</b></td>", stats.Count24h))
	b.WriteString(fmt.Sprintf("<td align=\"center\"><b>%d</b></td>", stats.CountWeek))
	b.WriteString("</tr></table>")

	if len(stats.Top) == 0 {
		b.WriteString("<blockquote>" + escapeHTML(i18n.T("stats.empty")) + "</blockquote>")
	} else {
		b.WriteString("<h4>" + escapeHTML(i18n.T("stats.top_header")) + "</h4>")
		b.WriteString("<table striped compact><tr>")
		b.WriteString("<th align=\"center\">#</th>")
		b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("stats.col_user")) + "</th>")
		b.WriteString("<th align=\"right\">" + escapeHTML(i18n.T("stats.col_count")) + "</th>")
		b.WriteString("</tr>")
		for i, v := range stats.Top {
			name := v.Username
			if name == "" {
				name = v.UserID
			}
			b.WriteString("<tr>")
			b.WriteString("<td align=\"center\">" + rankMark(i) + "</td>")
			b.WriteString("<td><code>" + escapeHTML(name) + "</code></td>")
			b.WriteString(fmt.Sprintf("<td align=\"right\"><b>%d</b></td>", v.Count))
			b.WriteString("</tr>")
		}
		b.WriteString("</table>")
	}

	b.WriteString("<footer>" + escapeHTML(i18n.T("stats.updated")) + " " + tgTime(time.Now(), loc, "wDT") + "</footer>")

	return b.String()
}

func rankMark(i int) string {
	switch i {
	case 0:
		return "🥇"
	case 1:
		return "🥈"
	case 2:
		return "🥉"
	}
	return strconv.Itoa(i + 1)
}

func writeUserRows(b *strings.Builder, user *api.CachedUser) {
	b.WriteString(tableRow(i18n.T("alert.user"), "<code>"+escapeHTML(user.Username)+"</code>"))
	if user.SubscriptionURL != "" {
		b.WriteString(tableRow(i18n.T("alert.profile"),
			"<a href=\""+escapeHTML(user.SubscriptionURL)+"\">"+escapeHTML(i18n.T("alert.open"))+"</a>"))
	}
}

func writeGroupingRows(b *strings.Builder, limit int, ips []api.ActiveIP, subnetGroups int, subnetEnabled bool, asnGroups int, asnEnabled bool) {
	ipCount := len(ips)
	counted := ipCount

	b.WriteString(tableRow(i18n.T("alert.limit"), "<b>"+strconv.Itoa(limit)+"</b>"))

	switch {
	case asnEnabled:
		counted = asnGroups
		b.WriteString(tableRow(i18n.T("alert.asn_groups"), "<b>"+strconv.Itoa(asnGroups)+"</b>"))
	case subnetEnabled && subnetGroups > 0 && subnetGroups < ipCount:
		counted = subnetGroups
		b.WriteString(tableRow(i18n.T("alert.subnets"), "<b>"+strconv.Itoa(subnetGroups)+"</b>"))
	}

	detected := fmt.Sprintf("%d IP", ipCount)
	if asnCount := countUniqueASN(ips); !asnEnabled && asnCount > 0 {
		detected += fmt.Sprintf(" · %d %s", asnCount, escapeHTML(i18n.T("alert.asn_count")))
	}
	b.WriteString(tableRow(i18n.T("alert.detected_ips"), detected))

	if over := counted - limit; over > 0 && limit > 0 {
		b.WriteString(tableRow(i18n.T("alert.overflow"), fmt.Sprintf("<mark>+%d</mark>", over)))
	}
}

func writeIPTable(b *strings.Builder, ips []api.ActiveIP, open bool) {
	if len(ips) == 0 {
		return
	}
	if open {
		b.WriteString("<details open>")
	} else {
		b.WriteString("<details>")
	}
	b.WriteString(fmt.Sprintf("<summary>%s (%d)</summary>", escapeHTML(i18n.T("alert.ips_header")), len(ips)))
	b.WriteString("<table striped compact><tr>")
	b.WriteString("<th align=\"left\">IP</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("alert.col_provider")) + "</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("alert.col_node")) + "</th>")
	b.WriteString("</tr>")
	for i, ip := range ips {
		if i >= maxIPRows {
			b.WriteString(fmt.Sprintf("<tr><td colspan=\"3\" align=\"center\">… %s %d</td></tr>",
				escapeHTML(i18n.T("alert.and_more")), len(ips)-maxIPRows))
			break
		}
		href := "https://ipinfo.io/" + url.PathEscape(ip.IP)
		org := strings.TrimSpace(ip.ASNOrg)
		if org == "" {
			org = "—"
		} else {
			org = truncateASNOrg(org)
		}
		b.WriteString("<tr>")
		b.WriteString("<td><a href=\"" + escapeHTML(href) + "\">" + escapeHTML(ip.IP) + "</a></td>")
		b.WriteString("<td>" + escapeHTML(org) + "</td>")
		b.WriteString("<td>" + escapeHTML(ip.NodeName) + "</td>")
		b.WriteString("</tr>")
	}
	b.WriteString("</table></details>")
}

func truncateASNOrg(s string) string {
	runes := []rune(s)
	if len(runes) <= asnOrgMaxLen {
		return s
	}
	cut := strings.TrimRight(string(runes[:asnOrgMaxLen-1]), " ")
	return cut + "…"
}

func countUniqueASN(ips []api.ActiveIP) int {
	seen := make(map[uint32]struct{})
	for _, ip := range ips {
		if ip.ASN == 0 {
			continue
		}
		seen[ip.ASN] = struct{}{}
	}
	return len(seen)
}

func actionResultText(action string) string {
	switch action {
	case "drop":
		return i18n.T("action.drop")
	case "disable":
		return i18n.T("action.disable")
	case "disable_temp":
		return i18n.T("action.disable_temp")
	case "ignore":
		return i18n.T("action.ignore")
	case "ignore_temp":
		return i18n.T("action.ignore_temp")
	case "enable":
		return i18n.T("action.enable")
	}
	return i18n.T("action.unknown")
}

func FormatActionResult(action, adminName string) string {
	return fmt.Sprintf("\n\n%s (%s: %s)", actionResultText(action), i18n.T("action.admin"), escapeHTML(adminName))
}

func FormatDuration(minutes int) string {
	if minutes <= 0 {
		return i18n.T("duration.forever")
	}
	if minutes < 60 {
		return fmt.Sprintf("%d %s", minutes, i18n.T("duration.min"))
	}
	hours := minutes / 60
	mins := minutes % 60
	if hours < 24 {
		if mins == 0 {
			return fmt.Sprintf("%d %s", hours, i18n.T("duration.hour"))
		}
		return fmt.Sprintf("%d %s %d %s", hours, i18n.T("duration.hour"), mins, i18n.T("duration.min"))
	}
	days := hours / 24
	remHours := hours % 24
	if remHours == 0 && mins == 0 {
		return fmt.Sprintf("%d %s", days, i18n.T("duration.day"))
	}
	if mins == 0 {
		return fmt.Sprintf("%d %s %d %s", days, i18n.T("duration.day"), remHours, i18n.T("duration.hour"))
	}
	return fmt.Sprintf("%d %s %d %s %d %s", days, i18n.T("duration.day"), remHours, i18n.T("duration.hour"), mins, i18n.T("duration.min"))
}

func FormatStartupMessage(version, actionMode string, checkInterval, cooldown, tolerance int, toleranceMultiplier float64, defaultDeviceLimit, autoDisableDuration int, autoNotifySoft, webhookEnabled, subnetGrouping bool, subnetPrefixV4 int, asnGrouping, maxmindLoaded bool, violationThreshold, violationThresholdWindow int) string {
	var b strings.Builder
	sec := i18n.T("startup.sec")

	b.WriteString("<h3>" + escapeHTML(i18n.T("startup.title")) + "</h3>")
	b.WriteString("<table bordered striped compact>")
	b.WriteString(tableRow("📦 "+i18n.T("startup.version"), "<code>"+escapeHTML(version)+"</code>"))

	mode := i18n.T("startup.mode_manual")
	if actionMode == "auto" {
		mode = i18n.T("startup.mode_auto")
	}
	b.WriteString(tableRow("⚙️ "+i18n.T("startup.mode"), "<b>"+escapeHTML(mode)+"</b>"))
	b.WriteString(tableRow("⏱ "+i18n.T("startup.interval"), fmt.Sprintf("%d%s", checkInterval, escapeHTML(sec))))
	b.WriteString(tableRow("🕐 "+i18n.T("startup.cooldown"), fmt.Sprintf("%d%s", cooldown, escapeHTML(sec))))

	tol := strconv.Itoa(tolerance)
	if toleranceMultiplier > 0 {
		tol += fmt.Sprintf(" · ×%.2f", toleranceMultiplier)
	}
	b.WriteString(tableRow("📊 "+i18n.T("startup.tolerance"), tol))

	if defaultDeviceLimit == 0 {
		b.WriteString(tableRow("📱 "+i18n.T("startup.default_limit"), escapeHTML(i18n.T("startup.unlimited"))))
	} else {
		b.WriteString(tableRow("📱 "+i18n.T("startup.default_limit"), strconv.Itoa(defaultDeviceLimit)))
	}

	if autoDisableDuration > 0 {
		b.WriteString(tableRow("🔒 "+i18n.T("startup.auto_disable"), escapeHTML(FormatDuration(autoDisableDuration))))
	}
	if actionMode == "auto" {
		b.WriteString(tableRow("🔔 "+i18n.T("startup.auto_notify_soft"), onOff(autoNotifySoft)))
	}
	if violationThreshold > 1 {
		b.WriteString(tableRow("🚦 "+i18n.T("startup.violation_threshold"),
			fmt.Sprintf("%d / %d%s", violationThreshold, violationThresholdWindow, escapeHTML(sec))))
	}

	b.WriteString(tableRow("🛰 "+i18n.T("startup.asn_grouping"), onOff(asnGrouping)))
	subnet := onOff(subnetGrouping)
	if subnetGrouping && !asnGrouping {
		subnet += fmt.Sprintf(" · <code>/%d</code>", subnetPrefixV4)
	}
	b.WriteString(tableRow("🌐 "+i18n.T("startup.subnet_grouping"), subnet))
	b.WriteString(tableRow("🔗 "+i18n.T("startup.webhook"), onOff(webhookEnabled)))

	maxmind := "✅ " + escapeHTML(i18n.T("startup.maxmind_loaded"))
	if !maxmindLoaded {
		maxmind = "⚠️ " + escapeHTML(i18n.T("startup.maxmind_unavailable"))
	}
	b.WriteString(tableRow("🌍 "+i18n.T("startup.maxmind"), maxmind))
	b.WriteString("</table>")

	b.WriteString("<footer>" + escapeHTML(i18n.T("startup.settings_hint")) + "</footer>")

	return b.String()
}

func onOff(v bool) string {
	if v {
		return "✅ " + escapeHTML(i18n.T("startup.enabled"))
	}
	return "⬜ " + escapeHTML(i18n.T("startup.disabled"))
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
