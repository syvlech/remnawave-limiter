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
	maxSharedAccounts = 10
	maxHWIDRunes      = 24
	sharedDateLayout  = "02.01.06 15:04"
)

type SharedHWIDAlert struct {
	User     *api.CachedUser
	Status   string
	Created  time.Time
	Device   string
	App      string
	HWID     string
	Accounts []SharedAccount
	Hidden   int
	At       time.Time
}

type SharedAccount struct {
	UserID        int64
	Username      string
	Status        string
	Squads        []string
	Created       time.Time
	Seen          time.Time
	Deleted       bool
	DeviceRemoved bool
}

func FormatSharedHWIDAlert(a SharedHWIDAlert, loc *time.Location) string {
	var b strings.Builder

	b.WriteString("<h3>" + escapeHTML(i18n.T("shared_hwid.title")) + "</h3>")
	b.WriteString("<table bordered striped compact>")
	writeUserRows(&b, a.User)
	if a.Status != "" {
		b.WriteString(tableRow(i18n.T("shared_hwid.status"), escapeHTML(userStatusText(a.Status))))
	}
	if !a.Created.IsZero() {
		b.WriteString(tableRow(i18n.T("shared_hwid.created"), tgTime(a.Created, loc, "wDT")))
	}
	device := strings.TrimSpace(a.Device)
	if app := strings.TrimSpace(a.App); app != "" {
		if device != "" {
			device += " · "
		}
		device += app
	}
	if device != "" {
		b.WriteString(tableRow(i18n.T("shared_hwid.device"), escapeHTML(device)))
	}
	b.WriteString(tableRow(i18n.T("shared_hwid.hwid"), "<code>"+escapeHTML(shortHWID(a.HWID))+"</code>"))
	b.WriteString(tableRow(i18n.T("shared_hwid.others"), "<mark>"+strconv.Itoa(len(a.Accounts))+"</mark>"))
	b.WriteString(tableRow(i18n.T("alert.time"), tgTime(a.At, loc, "wDT")))
	b.WriteString("</table>")

	writeSharedAccounts(&b, a.Accounts, loc)
	if a.Hidden > 0 {
		b.WriteString("<blockquote>" + escapeHTML(i18n.T("shared_hwid.note")) + "</blockquote>")
	}

	return b.String()
}

func writeSharedAccounts(b *strings.Builder, accounts []SharedAccount, loc *time.Location) {
	if len(accounts) == 0 {
		return
	}
	b.WriteString("<details open>")
	b.WriteString(fmt.Sprintf("<summary>%s (%d)</summary>", escapeHTML(i18n.T("shared_hwid.accounts")), len(accounts)))
	b.WriteString("<table striped compact><tr>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("shared_hwid.col_user")) + "</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("shared_hwid.col_state")) + "</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("shared_hwid.col_created")) + "</th>")
	b.WriteString("<th align=\"left\">" + escapeHTML(i18n.T("shared_hwid.col_seen")) + "</th>")
	b.WriteString("</tr>")
	for i, acc := range accounts {
		if i >= maxSharedAccounts {
			b.WriteString("<tr><td colspan=\"4\" align=\"center\">" +
				escapeHTML(fmt.Sprintf(i18n.T("alert.and_more"), len(accounts)-maxSharedAccounts)) + "</td></tr>")
			break
		}
		name := strings.TrimSpace(acc.Username)
		if name == "" {
			name = strconv.FormatInt(acc.UserID, 10)
		}
		b.WriteString("<tr>")
		b.WriteString("<td><code>" + escapeHTML(name) + "</code></td>")
		b.WriteString("<td>" + accountState(acc) + "</td>")
		b.WriteString("<td>" + escapeHTML(formatSharedDate(acc.Created, loc)) + "</td>")
		b.WriteString("<td>" + escapeHTML(formatSharedDate(acc.Seen, loc)) + "</td>")
		b.WriteString("</tr>")
	}
	b.WriteString("</table></details>")
}

func accountState(acc SharedAccount) string {
	if acc.Deleted {
		return escapeHTML(i18n.T("shared_hwid.deleted"))
	}
	parts := make([]string, 0, 3)
	if acc.Status != "" {
		parts = append(parts, userStatusText(acc.Status))
	}
	if len(acc.Squads) > 0 {
		shown := acc.Squads
		if len(shown) > maxSquadNames {
			shown = shown[:maxSquadNames]
		}
		parts = append(parts, strings.Join(shown, ", "))
	}
	if acc.DeviceRemoved {
		parts = append(parts, i18n.T("shared_hwid.device_removed"))
	}
	if len(parts) == 0 {
		return "—"
	}
	return escapeHTML(strings.Join(parts, " · "))
}

func userStatusText(status string) string {
	key := "user_status." + strings.ToUpper(strings.TrimSpace(status))
	if text := i18n.T(key); text != key {
		return text
	}
	return status
}

func shortHWID(hwid string) string {
	r := []rune(strings.TrimSpace(hwid))
	if len(r) > maxHWIDRunes {
		return string(r[:maxHWIDRunes]) + "…"
	}
	return string(r)
}

func formatSharedDate(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(loc).Format(sharedDateLayout)
}
