package traffic

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/i18n"
	"github.com/remnawave/limiter/internal/telegram"
)

const (
	PollInterval   = 30 * time.Minute
	firstPollDelay = 2 * time.Minute

	historyDays = 14
	historyTTL  = 16 * 24 * time.Hour
	heavyWindow = 7

	intradayDivisor  = 10
	minIntradayFloor = 512 << 20
	nightFloor       = 100 << 20
	minActiveDays    = 5

	spikeAlertTTL    = 72 * time.Hour
	heavyAlertTTL    = 7 * 24 * time.Hour
	violationsWindow = 7 * 24 * time.Hour
	maxReportRows    = 10
	maxAlertsPerPass = 20

	dayLayout = "2006-01-02"
	gib       = int64(1) << 30
)

type Panel interface {
	GetNodes(ctx context.Context) ([]api.Node, error)
	GetNodesUsage(ctx context.Context, nodeUUIDs []string, day string, minBytes int64) ([]api.NodeUsage, error)
}

type Store interface {
	SaveTrafficDay(ctx context.Context, day string, totals map[int64]int64, ttl time.Duration) error
	GetTrafficDays(ctx context.Context, days []string, userIDs []int64) (map[int64][]int64, error)
	CountTrafficDays(ctx context.Context, days []string) (int, error)
	GetTrafficFinalized(ctx context.Context) (string, error)
	SetTrafficFinalized(ctx context.Context, day string) error
	TrafficAlerted(ctx context.Context, key string) (bool, error)
	MarkTrafficAlert(ctx context.Context, key string, ttl time.Duration) error
	IsWhitelisted(ctx context.Context, userID int64) (bool, error)
	CountUserViolations(ctx context.Context, userID int64, since time.Duration) (int, error)
	GetTrafficDaysAll(ctx context.Context, days []string) ([]map[int64]int64, error)
}

type Users interface {
	LookupUser(ctx context.Context, userID int64) (*api.CachedUser, error)
}

type Notifier interface {
	SendManualAlert(ctx context.Context, text string, userID int64, disableDuration, ignoreDuration int) error
}

type Monitor struct {
	cfg      *config.Provider
	panel    Panel
	store    Store
	users    Users
	notifier Notifier
	logger   *logrus.Logger
	location *time.Location
	now      func() time.Time

	unavailableWarned atomic.Bool
	noDataWarned      atomic.Bool

	pendingDay   string
	pendingHeavy map[int64]bool

	firstDelay time.Duration
}

func New(cfg *config.Provider, panel Panel, store Store, users Users, notifier Notifier, logger *logrus.Logger, loc *time.Location) *Monitor {
	return &Monitor{cfg: cfg, panel: panel, store: store, users: users, notifier: notifier, logger: logger, location: loc, now: time.Now, firstDelay: firstPollDelay}
}

func (m *Monitor) Run(ctx context.Context) {
	timer := time.NewTimer(m.firstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if m.cfg.Load().TrafficMonitor {
			m.Poll(ctx)
		}
		timer.Reset(PollInterval)
	}
}

type dayUsage struct {
	totals map[int64]int64
	nodes  map[int64][]telegram.NodeBytes
}

func (m *Monitor) Poll(ctx context.Context) {
	cfg := m.cfg.Load()
	now := m.now()

	nodes, err := m.panel.GetNodes(ctx)
	if err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.traffic_poll_failed"))
		return
	}
	uuids, names := filterNodes(nodes, cfg)
	if len(uuids) == 0 {
		return
	}

	budget := maxAlertsPerPass
	sent := m.finalize(ctx, cfg, uuids, names, now, &budget)
	today := dayStart(now)
	floor := int64(cfg.TrafficSpikeMinGB) * gib / intradayDivisor
	if floor < minIntradayFloor {
		floor = minIntradayFloor
	}
	usage, err := m.fetchDay(ctx, uuids, names, today.Format(dayLayout), floor)
	if err != nil {
		m.apiFailed(err)
		return
	}
	spikes, _ := m.checkSpikes(ctx, cfg, today, usage, true, now, nil, &budget)
	sent += len(spikes)

	entry := m.logger.WithFields(logrus.Fields{"users": len(usage.totals), "alerts": sent, "nodes": len(uuids)})
	if sent > 0 {
		entry.Info(i18n.T("log.traffic_poll"))
	} else {
		entry.Debug(i18n.T("log.traffic_poll"))
	}
}

func (m *Monitor) finalize(ctx context.Context, cfg *config.Config, uuids []string, names map[string]string, now time.Time, budget *int) int {
	today := dayStart(now)
	yesterday := today.AddDate(0, 0, -1)
	oldest := today.AddDate(0, 0, -historyDays)

	last, err := m.store.GetTrafficFinalized(ctx)
	if err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
		return 0
	}
	from := oldest
	if t, err := time.Parse(dayLayout, last); err == nil && !t.Before(oldest) && !t.After(yesterday) {
		from = t.AddDate(0, 0, 1)
	}
	if from.After(yesterday) {
		return 0
	}

	var latest *dayUsage
	for d := from; !d.After(yesterday); d = d.AddDate(0, 0, 1) {
		day := d.Format(dayLayout)
		usage, err := m.fetchDay(ctx, uuids, names, day, nightFloor)
		if err != nil {
			m.apiFailed(err)
			return 0
		}
		if err := m.store.SaveTrafficDay(ctx, day, usage.totals, historyTTL); err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
			return 0
		}
		if d.Equal(yesterday) {
			latest = usage
			break
		}
		if err := m.store.SetTrafficFinalized(ctx, day); err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
			return 0
		}
		m.logger.WithFields(logrus.Fields{"day": day, "users": len(usage.totals)}).
			Debug(i18n.T("log.traffic_finalized"))
	}
	if latest == nil {
		return 0
	}

	day := yesterday.Format(dayLayout)
	sent, pending := 0, false
	if len(latest.totals) == 0 {
		if !m.noDataWarned.Swap(true) {
			m.logger.Warn(i18n.T("log.traffic_no_data"))
		}
	} else {
		if m.pendingDay != day {
			m.pendingDay, m.pendingHeavy = day, map[int64]bool{}
		}
		heavy, heavyPending := m.checkHeavy(ctx, cfg, yesterday, latest, now, budget)
		for id := range heavy {
			m.pendingHeavy[id] = true
		}
		spikes, spikesPending := m.checkSpikes(ctx, cfg, yesterday, latest, false, now, m.pendingHeavy, budget)
		sent = len(heavy) + len(spikes)
		pending = heavyPending || spikesPending
	}
	if pending {
		return sent
	}
	if err := m.store.SetTrafficFinalized(ctx, day); err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
		return sent
	}
	m.pendingDay, m.pendingHeavy = "", nil
	m.logger.WithFields(logrus.Fields{"day": day, "users": len(latest.totals)}).
		Debug(i18n.T("log.traffic_finalized"))
	return sent
}

func (m *Monitor) fetchDay(ctx context.Context, uuids []string, names map[string]string, day string, floor int64) (*dayUsage, error) {
	res, err := m.panel.GetNodesUsage(ctx, uuids, day, floor)
	if err != nil {
		return nil, err
	}
	u := &dayUsage{totals: map[int64]int64{}, nodes: map[int64][]telegram.NodeBytes{}}
	for _, n := range res {
		name := names[strings.ToLower(n.UUID)]
		if name == "" {
			name = shortUUID(n.UUID)
		}
		for _, user := range n.Users {
			if user.TotalBytes <= 0 {
				continue
			}
			u.totals[user.ID] += user.TotalBytes
			u.nodes[user.ID] = append(u.nodes[user.ID], telegram.NodeBytes{Name: name, Bytes: user.TotalBytes})
		}
	}
	return u, nil
}

type finding struct {
	id       int64
	bytes    int64
	norm     int64
	normDays int
	severity float64
	history  []int64
	heavy    int
	week     int64
}

func (m *Monitor) checkSpikes(ctx context.Context, cfg *config.Config, day time.Time, usage *dayUsage, isToday bool, now time.Time, exclude map[int64]bool, budget *int) (map[int64]bool, bool) {
	minBytes := int64(cfg.TrafficSpikeMinGB) * gib
	candidates := idsAtLeast(usage.totals, minBytes)
	if len(candidates) == 0 {
		return nil, false
	}
	normDays := daysBefore(day, historyDays)
	hist, err := m.store.GetTrafficDays(ctx, normDays, candidates)
	if err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
		return nil, true
	}

	var found []finding
	for _, id := range candidates {
		if exclude[id] {
			continue
		}
		bytes := usage.totals[id]
		active := activeDays(hist[id])
		if len(active) < minActiveDays {
			continue
		}
		norm := median(active)
		threshold := int64(cfg.TrafficSpikeFactor * float64(norm))
		if threshold < minBytes {
			threshold = minBytes
		}
		if bytes < threshold {
			continue
		}
		found = append(found, finding{id: id, bytes: bytes, norm: norm, normDays: len(active), severity: float64(bytes) / float64(max(norm, gib)), history: hist[id]})
	}
	return m.send(ctx, cfg, found, budget, func(f finding) (string, time.Duration, telegram.TrafficAlert) {
		a := telegram.TrafficAlert{
			Bytes: f.bytes, Day: day, Today: isToday, At: now,
			Norm: f.norm, NormDays: f.normDays,
			Nodes: sortedNodes(usage.nodes[f.id]), History: append(tail(f.history, historyDays-1), f.bytes),
		}
		if f.norm > 0 {
			a.Ratio = float64(f.bytes) / float64(f.norm)
		}
		return "spike:" + itoa(f.id), spikeAlertTTL, a
	})
}

func (m *Monitor) checkHeavy(ctx context.Context, cfg *config.Config, day time.Time, usage *dayUsage, now time.Time, budget *int) (map[int64]bool, bool) {
	if cfg.TrafficHeavyGB <= 0 {
		return nil, false
	}
	heavyBytes := int64(cfg.TrafficHeavyGB) * gib
	candidates := idsAtLeast(usage.totals, heavyBytes)
	if len(candidates) == 0 {
		return nil, false
	}
	normDays := daysBefore(day, historyDays)
	hist, err := m.store.GetTrafficDays(ctx, normDays, candidates)
	if err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
		return nil, true
	}

	var found []finding
	for _, id := range candidates {
		bytes := usage.totals[id]
		prior := tail(hist[id], heavyWindow-1)
		count, week := 1, bytes
		for _, v := range prior {
			week += v
			if v >= heavyBytes {
				count++
			}
		}
		if count < cfg.TrafficHeavyDays {
			continue
		}
		active := activeDays(hist[id])
		found = append(found, finding{id: id, bytes: bytes, norm: median(active), normDays: len(active), severity: float64(week), history: hist[id], heavy: count, week: week})
	}
	return m.send(ctx, cfg, found, budget, func(f finding) (string, time.Duration, telegram.TrafficAlert) {
		return "heavy:" + itoa(f.id), heavyAlertTTL, telegram.TrafficAlert{
			Heavy: true, Bytes: f.bytes, Day: day, At: now,
			Norm: f.norm, NormDays: f.normDays,
			HeavyBytes: heavyBytes, HeavyDays: f.heavy, HeavyWindow: heavyWindow, WeekTotal: f.week,
			Nodes: sortedNodes(usage.nodes[f.id]), History: append(tail(f.history, historyDays-1), f.bytes),
		}
	})
}

func (m *Monitor) send(ctx context.Context, cfg *config.Config, found []finding, budget *int, build func(finding) (string, time.Duration, telegram.TrafficAlert)) (map[int64]bool, bool) {
	sort.Slice(found, func(i, j int) bool {
		if found[i].severity != found[j].severity {
			return found[i].severity > found[j].severity
		}
		return found[i].id < found[j].id
	})
	sent := map[int64]bool{}
	pending := false
	for _, f := range found {
		key, ttl, alert := build(f)
		if done, err := m.store.TrafficAlerted(ctx, key); err != nil || done {
			if err != nil {
				m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
				pending = true
			}
			continue
		}
		if wl, err := m.store.IsWhitelisted(ctx, f.id); err != nil || wl {
			if err != nil {
				m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
				pending = true
			}
			continue
		}
		if *budget <= 0 {
			pending = true
			break
		}
		user, err := m.users.LookupUser(ctx, f.id)
		if err != nil {
			m.logger.WithError(err).WithField("userID", f.id).Warn(i18n.T("log.traffic_user_failed"))
			var se *api.StatusError
			if !errors.As(err, &se) || se.Code != http.StatusNotFound {
				pending = true
			}
			continue
		}
		alert.User = user
		alert.Violations = -1
		if n, err := m.store.CountUserViolations(ctx, f.id, violationsWindow); err == nil {
			alert.Violations = n
		}

		text := telegram.FormatTrafficAlert(alert, m.location)
		if err := m.notifier.SendManualAlert(ctx, text, f.id, cfg.AutoDisableDuration, cfg.IgnoreDuration); err != nil {
			m.logger.WithError(err).WithField("userID", f.id).Error(i18n.T("log.traffic_alert_failed"))
			pending = true
			continue
		}
		if err := m.store.MarkTrafficAlert(ctx, key, ttl); err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.traffic_store_failed"))
		}
		m.logger.WithFields(logrus.Fields{
			"rule": strings.SplitN(key, ":", 2)[0], "userID": f.id, "username": user.Username,
			"bytes": alert.Bytes, "norm": alert.Norm,
		}).Warn(i18n.T("log.traffic_alert"))
		sent[f.id] = true
		*budget--
	}
	return sent, pending
}

func (m *Monitor) apiFailed(err error) {
	var se *api.StatusError
	if errors.As(err, &se) && (se.Code == http.StatusForbidden || se.Code == http.StatusNotFound) {
		if !m.unavailableWarned.Swap(true) {
			m.logger.WithError(err).Warn(i18n.T("log.traffic_unavailable"))
		}
		return
	}
	m.logger.WithError(err).Warn(i18n.T("log.traffic_poll_failed"))
}

func filterNodes(nodes []api.Node, cfg *config.Config) ([]string, map[string]string) {
	skip := make(map[string]struct{}, len(cfg.IgnoredNodeUUIDs)+len(cfg.TrafficIgnoredNodeUUIDs))
	for _, id := range cfg.IgnoredNodeUUIDs {
		skip[id] = struct{}{}
	}
	for _, id := range cfg.TrafficIgnoredNodeUUIDs {
		skip[id] = struct{}{}
	}
	uuids := make([]string, 0, len(nodes))
	names := make(map[string]string, len(nodes))
	for _, n := range nodes {
		id := strings.ToLower(n.UUID)
		if _, ignored := skip[id]; ignored || id == "" {
			continue
		}
		uuids = append(uuids, n.UUID)
		names[id] = n.Name
	}
	return uuids, names
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func daysBefore(day time.Time, n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = day.AddDate(0, 0, i-n).Format(dayLayout)
	}
	return out
}

func idsAtLeast(totals map[int64]int64, minBytes int64) []int64 {
	ids := make([]int64, 0)
	for id, b := range totals {
		if b >= minBytes {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func activeDays(values []int64) []int64 {
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if v > 0 {
			out = append(out, v)
		}
	}
	return out
}

func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	s := append([]int64(nil), values...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

func tail(values []int64, n int) []int64 {
	if len(values) <= n {
		return append([]int64(nil), values...)
	}
	return append([]int64(nil), values[len(values)-n:]...)
}

func sortedNodes(nodes []telegram.NodeBytes) []telegram.NodeBytes {
	out := append([]telegram.NodeBytes(nil), nodes...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

func shortUUID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

func (m *Monitor) HeavyReport(ctx context.Context) (*telegram.HeavyTrafficReport, error) {
	cfg := m.cfg.Load()
	if !cfg.TrafficMonitor || cfg.TrafficHeavyGB <= 0 {
		return nil, nil
	}
	end := dayStart(m.now())
	yesterday := end.AddDate(0, 0, -1).Format(dayLayout)
	if saved, err := m.store.CountTrafficDays(ctx, []string{yesterday}); err == nil && saved == 0 {
		end = end.AddDate(0, 0, -1)
	}
	days := daysBefore(end, heavyWindow)
	covered, err := m.store.CountTrafficDays(ctx, days)
	if err != nil || covered == 0 {
		return nil, err
	}
	all, err := m.store.GetTrafficDaysAll(ctx, days)
	if err != nil {
		return nil, err
	}

	heavyBytes := int64(cfg.TrafficHeavyGB) * gib
	type agg struct {
		id   int64
		days int
		week int64
	}
	byUser := map[int64]*agg{}
	for _, day := range all {
		for id, b := range day {
			a := byUser[id]
			if a == nil {
				a = &agg{id: id}
				byUser[id] = a
			}
			a.week += b
			if b >= heavyBytes {
				a.days++
			}
		}
	}
	var heavy []*agg
	for _, a := range byUser {
		if a.days < cfg.TrafficHeavyDays {
			continue
		}
		if wl, err := m.store.IsWhitelisted(ctx, a.id); err == nil && wl {
			continue
		}
		heavy = append(heavy, a)
	}
	sort.Slice(heavy, func(i, j int) bool {
		if heavy[i].week != heavy[j].week {
			return heavy[i].week > heavy[j].week
		}
		return heavy[i].id < heavy[j].id
	})

	report := &telegram.HeavyTrafficReport{HeavyBytes: heavyBytes, MinDays: cfg.TrafficHeavyDays, Window: heavyWindow, Total: len(heavy)}
	for i, a := range heavy {
		if i >= maxReportRows {
			break
		}
		name := itoa(a.id)
		if user, err := m.users.LookupUser(ctx, a.id); err == nil && user.Username != "" {
			name = user.Username
		}
		report.Rows = append(report.Rows, telegram.HeavyTrafficRow{Name: name, Days: a.days, Week: a.week})
	}
	return report, nil
}
