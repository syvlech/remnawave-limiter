package monitor

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/geoip"
	"github.com/remnawave/limiter/internal/i18n"
	"github.com/remnawave/limiter/internal/telegram"
)

const evidenceTimeout = 8 * time.Second

type evidenceAPI interface {
	GetUserHWIDDevices(ctx context.Context, userID int64) ([]api.HWIDDevice, error)
	GetUserSubscriptionHistory(ctx context.Context, userID int64) ([]api.SubscriptionRequest, error)
}

func (m *Monitor) collectEvidence(ctx context.Context, userID int64, active []api.ActiveIP) *telegram.Evidence {
	if m.evidence == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, evidenceTimeout)
	defer cancel()

	var (
		wg              sync.WaitGroup
		devices         []api.HWIDDevice
		history         []api.SubscriptionRequest
		devErr, histErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		devices, devErr = m.evidence.GetUserHWIDDevices(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		history, histErr = m.evidence.GetUserSubscriptionHistory(ctx, userID)
	}()
	wg.Wait()

	ev := &telegram.Evidence{}
	if histErr != nil {
		m.evidenceFailed("subscription-request-history", userID, histErr)
	} else {
		ev.Fetches = buildFetchEvidence(history, active, m.resolver)
	}
	if devErr != nil {
		m.evidenceFailed("hwid-devices", userID, devErr)
	} else {
		ev.Devices = buildDeviceEvidence(devices)
	}
	return ev
}

func (m *Monitor) evidenceFailed(endpoint string, userID int64, err error) {
	var se *api.StatusError
	if errors.As(err, &se) && (se.Code == http.StatusForbidden || se.Code == http.StatusNotFound) {
		if _, seen := m.evidenceWarned.LoadOrStore(endpoint, true); !seen {
			m.logger.WithError(err).WithField("endpoint", endpoint).Warn(i18n.T("log.evidence_unavailable"))
		}
		return
	}
	m.logger.WithError(err).WithFields(logrus.Fields{"endpoint": endpoint, "userID": userID}).
		Warn(i18n.T("log.evidence_failed"))
}

func buildFetchEvidence(records []api.SubscriptionRequest, active []api.ActiveIP, resolver geoip.Resolver) *telegram.FetchEvidence {
	if len(records) == 0 {
		return nil
	}
	online := make(map[string]struct{}, len(active))
	for _, ip := range active {
		online[ip.IP] = struct{}{}
	}

	type group struct {
		row     telegram.FetchRow
		apps    map[string]int
		lastAt  time.Time
		appSeen []string
	}
	groups := make(map[string]*group)
	allApps := make(map[string]struct{})
	asns := make(map[uint32]struct{})
	var oldest, newest time.Time

	for _, rec := range records {
		if !rec.RequestAt.IsZero() {
			if oldest.IsZero() || rec.RequestAt.Before(oldest) {
				oldest = rec.RequestAt
			}
			if rec.RequestAt.After(newest) {
				newest = rec.RequestAt
			}
		}
		ip := strings.TrimSpace(rec.RequestIP)
		g, ok := groups[ip]
		if !ok {
			g = &group{row: telegram.FetchRow{IP: ip}, apps: map[string]int{}}
			if _, isOnline := online[ip]; isOnline && ip != "" {
				g.row.Online = true
			}
			if ip != "" {
				if info, found := resolver.Lookup(ip); found {
					g.row.Org = info.Org
					if info.Number != 0 {
						asns[info.Number] = struct{}{}
					}
				}
			}
			groups[ip] = g
		}
		g.row.Count++
		if rec.RequestAt.After(g.lastAt) {
			g.lastAt = rec.RequestAt
		}
		if app := appName(rec.UserAgent); app != "" {
			key := strings.ToLower(app)
			if _, dup := g.apps[key]; !dup {
				g.appSeen = append(g.appSeen, app)
			}
			g.apps[key]++
			allApps[key] = struct{}{}
		}
	}

	ev := &telegram.FetchEvidence{Total: len(records), ASNs: len(asns), Apps: len(allApps)}
	if !oldest.IsZero() {
		ev.Span = newest.Sub(oldest)
	}
	sorted := make([]*group, 0, len(groups))
	for ip, g := range groups {
		if ip != "" {
			ev.IPs++
		}
		g.row.Apps = g.appSeen
		sorted = append(sorted, g)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].row.Count != sorted[j].row.Count {
			return sorted[i].row.Count > sorted[j].row.Count
		}
		if !sorted[i].lastAt.Equal(sorted[j].lastAt) {
			return sorted[i].lastAt.After(sorted[j].lastAt)
		}
		return sorted[i].row.IP < sorted[j].row.IP
	})
	ev.Rows = make([]telegram.FetchRow, 0, len(sorted))
	for _, g := range sorted {
		ev.Rows = append(ev.Rows, g.row)
	}
	return ev
}

func buildDeviceEvidence(devices []api.HWIDDevice) *telegram.DeviceEvidence {
	if len(devices) == 0 {
		return nil
	}
	items := make([]telegram.DeviceItem, 0, len(devices))
	for _, d := range devices {
		seen := d.UpdatedAt
		if seen.IsZero() {
			seen = d.CreatedAt
		}
		items = append(items, telegram.DeviceItem{Name: deviceName(d), App: appName(d.UserAgent), Seen: seen})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Seen.After(items[j].Seen) })
	return &telegram.DeviceEvidence{Items: items}
}

func deviceName(d api.HWIDDevice) string {
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

func appName(userAgent string) string {
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
