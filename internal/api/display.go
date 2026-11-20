package api

import (
	"strings"

	"github.com/remnawave/limiter/internal/i18n"
)

func NewCachedUser(userID int64, d *UserData) *CachedUser {
	cu := &CachedUser{
		UserID:          userID,
		Username:        d.Username,
		SubscriptionURL: d.SubscriptionURL,
		HWIDDeviceLimit: -1,
	}
	if d.Email != nil {
		cu.Email = *d.Email
	}
	if d.TelegramID != nil {
		cu.TelegramID = *d.TelegramID
	}
	if d.HWIDDeviceLimit != nil {
		cu.HWIDDeviceLimit = *d.HWIDDeviceLimit
	}
	cu.Squads = SquadNames(d.ActiveInternalSquads)
	for _, sq := range d.ActiveInternalSquads {
		if id := strings.ToLower(strings.TrimSpace(sq.UUID)); id != "" {
			cu.SquadUUIDs = append(cu.SquadUUIDs, id)
		}
	}
	return cu
}

func SquadNames(squads []InternalSquad) []string {
	var names []string
	for _, sq := range squads {
		if name := strings.TrimSpace(sq.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func (d HWIDDevice) DisplayName() string {
	model := strings.TrimSpace(d.DeviceModel)
	platform := strings.TrimSpace(d.Platform)
	osVersion := strings.TrimSpace(d.OSVersion)

	system := platform
	switch {
	case osVersion == "":
	case platform == "" || strings.HasPrefix(strings.ToLower(osVersion), strings.ToLower(platform)):
		system = osVersion
	default:
		system = platform + " " + osVersion
	}

	var parts []string
	if model != "" {
		parts = append(parts, model)
	}
	if system != "" && !strings.EqualFold(system, model) {
		parts = append(parts, system)
	}
	if len(parts) == 0 {
		hwid := []rune(strings.TrimSpace(d.HWID))
		if len(hwid) > 8 {
			return string(hwid[:8]) + "…"
		}
		return string(hwid)
	}
	return strings.Join(parts, " · ")
}

func AppName(userAgent string) string {
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		return ""
	}
	if strings.HasPrefix(ua, "Mozilla/") {
		return i18n.T("alert.app_browser")
	}
	name := ua
	if end := strings.IndexAny(ua, "/ ;("); end > 0 {
		name = ua[:end]
	}
	if r := []rune(name); len(r) > 24 {
		name = string(r[:24])
	}
	return name
}
