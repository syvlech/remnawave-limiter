package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/i18n"
)

const asnOrgMaxLen = 40

func FormatManualAlert(user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, loc *time.Location, subnetGroups int, subnetEnabled bool, asnGroups int, asnEnabled bool) string {
	var b strings.Builder

	b.WriteString(i18n.T("alert.manual.title") + "\n\n")
	b.WriteString(fmt.Sprintf("%s: <code>%s</code>\n", i18n.T("alert.user"), escapeHTML(user.Username)))
	b.WriteString(formatGroupingLine(limit, subnetGroups, len(ips), countUniqueASN(ips), subnetEnabled, asnGroups, asnEnabled))
	b.WriteString(fmt.Sprintf("%s: %d\n", i18n.T("alert.violations_24h"), violationCount))
	b.WriteString(fmt.Sprintf("🕐 %s\n", time.Now().In(loc).Format("02.01.2006 15:04:05")))

	b.WriteString(fmt.Sprintf("\n%s:\n", i18n.T("alert.ips_header")))
	writeIPList(&b, ips)

	if user.SubscriptionURL != "" {
		b.WriteString(fmt.Sprintf("\n<a href=\"%s\">%s</a>", escapeHTML(user.SubscriptionURL), i18n.T("alert.profile")))
	}

	return b.String()
}

func FormatAutoAlert(user *api.CachedUser, ips []api.ActiveIP, limit, durationMinutes int, violationCount int64, loc *time.Location, subnetGroups int, subnetEnabled bool, asnGroups int, asnEnabled bool) string {
	var b strings.Builder

	b.WriteString(i18n.T("alert.auto.title") + "\n\n")
	b.WriteString(fmt.Sprintf("%s: <code>%s</code>\n", i18n.T("alert.user"), escapeHTML(user.Username)))
	b.WriteString(formatGroupingLine(limit, subnetGroups, len(ips), countUniqueASN(ips), subnetEnabled, asnGroups, asnEnabled))
	b.WriteString(fmt.Sprintf("%s: %d\n", i18n.T("alert.violations_24h"), violationCount))

	if durationMinutes == 0 {
		b.WriteString(fmt.Sprintf("%s: %s\n", i18n.T("alert.disabled_for"), i18n.T("alert.permanent")))
	} else {
		b.WriteString(fmt.Sprintf("%s: %d %s\n", i18n.T("alert.disabled_for"), durationMinutes, i18n.T("duration.min")))
	}

	b.WriteString(fmt.Sprintf("🕐 %s\n", time.Now().In(loc).Format("02.01.2006 15:04:05")))

	b.WriteString(fmt.Sprintf("\n%s:\n", i18n.T("alert.ips_header")))
	writeIPList(&b, ips)

	return b.String()
}

func writeIPList(b *strings.Builder, ips []api.ActiveIP) {
	const maxIPs = 10
	for i, ip := range ips {
		if i >= maxIPs {
			b.WriteString(fmt.Sprintf("  … %s %d\n", i18n.T("alert.and_more"), len(ips)-maxIPs))
			return
		}
		org := strings.TrimSpace(ip.ASNOrg)
		if org != "" {
			b.WriteString(fmt.Sprintf("  • <a href=\"https://ipinfo.io/%s\">%s</a> - %s (%s)\n",
				ip.IP, escapeHTML(ip.IP), escapeHTML(truncateASNOrg(org)), escapeHTML(ip.NodeName)))
		} else {
			b.WriteString(fmt.Sprintf("  • <a href=\"https://ipinfo.io/%s\">%s</a> (%s)\n",
				ip.IP, escapeHTML(ip.IP), escapeHTML(ip.NodeName)))
		}
	}
}

func truncateASNOrg(s string) string {
	if len(s) <= asnOrgMaxLen {
		return s
	}
	cut := strings.TrimRight(s[:asnOrgMaxLen-1], " ")
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

func FormatActionResult(action, adminName, username string) string {
	var msg string
	switch action {
	case "drop":
		msg = i18n.T("action.drop")
	case "disable":
		msg = i18n.T("action.disable")
	case "disable_temp":
		msg = i18n.T("action.disable_temp")
	case "ignore":
		msg = i18n.T("action.ignore")
	case "ignore_temp":
		msg = i18n.T("action.ignore_temp")
	case "enable":
		msg = i18n.T("action.enable")
	default:
		msg = i18n.T("action.unknown")
	}
	return fmt.Sprintf("\n\n%s (%s: %s)", msg, i18n.T("action.admin"), escapeHTML(adminName))
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

func FormatStartupMessage(version, actionMode string, checkInterval, cooldown, tolerance int, toleranceMultiplier float64, defaultDeviceLimit, autoDisableDuration int, webhookEnabled, subnetGrouping bool, subnetPrefixV4 int, asnGrouping, maxmindLoaded bool, violationThreshold, violationThresholdWindow int) string {
	var b strings.Builder

	b.WriteString(i18n.T("startup.title") + "\n\n")
	b.WriteString(fmt.Sprintf("📦 %s: <code>%s</code>\n", i18n.T("startup.version"), version))

	mode := i18n.T("startup.mode_manual")
	if actionMode == "auto" {
		mode = i18n.T("startup.mode_auto")
	}
	b.WriteString(fmt.Sprintf("⚙️ %s: %s\n", i18n.T("startup.mode"), mode))
	b.WriteString(fmt.Sprintf("⏱ %s: %d%s\n", i18n.T("startup.interval"), checkInterval, i18n.T("startup.sec")))
	b.WriteString(fmt.Sprintf("🕐 %s: %d%s\n", i18n.T("startup.cooldown"), cooldown, i18n.T("startup.sec")))
	b.WriteString(fmt.Sprintf("📊 %s: %d\n", i18n.T("startup.tolerance"), tolerance))
	if toleranceMultiplier > 0 {
		b.WriteString(fmt.Sprintf("📊 %s: %.2f\n", i18n.T("startup.tolerance_mult"), toleranceMultiplier))
	}

	if defaultDeviceLimit == 0 {
		b.WriteString(fmt.Sprintf("📱 %s: %s\n", i18n.T("startup.default_limit"), i18n.T("startup.unlimited")))
	} else {
		b.WriteString(fmt.Sprintf("📱 %s: %d\n", i18n.T("startup.default_limit"), defaultDeviceLimit))
	}

	if autoDisableDuration > 0 {
		b.WriteString(fmt.Sprintf("🔒 %s: %s\n", i18n.T("startup.auto_disable"), FormatDuration(autoDisableDuration)))
	}

	webhookStatus := i18n.T("startup.disabled")
	if webhookEnabled {
		webhookStatus = i18n.T("startup.enabled")
	}
	b.WriteString(fmt.Sprintf("🔗 %s: %s\n", i18n.T("startup.webhook"), webhookStatus))

	asnStatus := i18n.T("startup.disabled")
	if asnGrouping {
		asnStatus = i18n.T("startup.enabled")
	}
	b.WriteString(fmt.Sprintf("🛰 %s: %s\n", i18n.T("startup.asn_grouping"), asnStatus))

	subnetStatus := i18n.T("startup.disabled")
	if subnetGrouping {
		subnetStatus = i18n.T("startup.enabled")
	}
	b.WriteString(fmt.Sprintf("🌐 %s: %s\n", i18n.T("startup.subnet_grouping"), subnetStatus))
	if subnetGrouping && !asnGrouping {
		b.WriteString(fmt.Sprintf("  ↳ /%d\n", subnetPrefixV4))
	}

	maxmindStatus := i18n.T("startup.maxmind_unavailable")
	if maxmindLoaded {
		maxmindStatus = i18n.T("startup.maxmind_loaded")
	}
	b.WriteString(fmt.Sprintf("🌍 %s: %s\n", i18n.T("startup.maxmind"), maxmindStatus))

	if violationThreshold > 1 {
		b.WriteString(fmt.Sprintf("🚦 %s: %d\n", i18n.T("startup.violation_threshold"), violationThreshold))
		b.WriteString(fmt.Sprintf("🕐 %s: %d%s\n", i18n.T("startup.threshold_window"), violationThresholdWindow, i18n.T("startup.sec")))
	}

	return b.String()
}

func formatGroupingLine(limit, subnetGroups, ipCount, asnCount int, subnetEnabled bool, asnGroups int, asnEnabled bool) string {
	var base string
	switch {
	case asnEnabled:
		base = fmt.Sprintf("%s: %d | %s: %d | %s: %d IP",
			i18n.T("alert.limit"), limit,
			i18n.T("alert.asn_groups"), asnGroups,
			i18n.T("alert.detected_ips"), ipCount)
	case subnetEnabled && subnetGroups > 0 && subnetGroups < ipCount:
		base = fmt.Sprintf("%s: %d | %s: %d | %s: %d IP",
			i18n.T("alert.limit"), limit,
			i18n.T("alert.subnets"), subnetGroups,
			i18n.T("alert.detected_ips"), ipCount)
	default:
		base = fmt.Sprintf("%s: %d | %s: %d IP",
			i18n.T("alert.limit"), limit,
			i18n.T("alert.detected_ips"), ipCount)
	}

	if !asnEnabled && asnCount > 0 {
		base += fmt.Sprintf(" (%d %s)", asnCount, i18n.T("alert.asn_count"))
	}

	return base + "\n"
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
