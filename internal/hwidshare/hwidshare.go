package hwidshare

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/i18n"
	"github.com/remnawave/limiter/internal/telegram"
)

const (
	PollInterval   = 5 * time.Minute
	firstPollDelay = 90 * time.Second

	pageSize        = 100
	pageOverlap     = 10
	maxPages        = 6
	seedPageSize    = 1000
	seedPageOverlap = 50
	maxSeedPages    = 500
	seedPause       = 200 * time.Millisecond

	overlap      = 10 * time.Minute
	staleAfter   = 24 * time.Hour
	giveUpAfter  = 6 * time.Hour
	processedTTL = giveUpAfter + time.Hour
	reportedTTL  = 30 * 24 * time.Hour
	trafficTTL   = 30 * 24 * time.Hour
	mib          = 1 << 20

	lookupSize       = 100
	maxLookups       = 20
	maxAlertsPerPoll = 20
)

type Panel interface {
	ListHWIDDevices(ctx context.Context, start, size int) ([]api.HWIDDevice, int, error)
	FindHWIDDevices(ctx context.Context, hwid string, size int) ([]api.HWIDDevice, error)
	GetUserByID(ctx context.Context, userID int64) (*api.UserData, error)
	SetTrafficLimit(ctx context.Context, userID int64, limitBytes int64, activate bool) error
}

type Users interface {
	LookupUser(ctx context.Context, userID int64) (*api.CachedUser, error)
}

type Store interface {
	GetHWIDState(ctx context.Context) (cache.HWIDState, error)
	SetHWIDState(ctx context.Context, st cache.HWIDState) error
	HWIDRecorded(ctx context.Context, records []cache.HWIDRecord) ([]bool, error)
	RecordHWID(ctx context.Context, r cache.HWIDRecord, ttl time.Duration) error
	SeedHWIDHistory(ctx context.Context, records []cache.HWIDRecord, retention time.Duration) error
	GetHWIDHistory(ctx context.Context, hwid string) (map[int64]cache.HWIDRecord, error)
	GetHWIDReported(ctx context.Context, userID int64) (map[int64]bool, error)
	MarkHWIDReported(ctx context.Context, userID int64, others []int64, ttl time.Duration) error
	UnmarkHWIDReported(ctx context.Context, userID int64, others []int64) error
	IsWhitelisted(ctx context.Context, userID int64) (bool, error)
	GetTrafficCut(ctx context.Context, userID int64) (*cache.TrafficCut, error)
	SaveTrafficCut(ctx context.Context, userID int64, cut cache.TrafficCut, ttl time.Duration) error
	DeleteTrafficCut(ctx context.Context, userID int64) error
}

type Notifier interface {
	SendSharedHWIDAlert(ctx context.Context, text string, userID int64, disableDuration, ignoreDuration int, trafficRestore bool) error
}

type Monitor struct {
	cfg      *config.Provider
	panel    Panel
	users    Users
	store    Store
	notifier Notifier
	logger   *logrus.Logger
	location *time.Location
	now      func() time.Time

	unavailableWarned atomic.Bool

	firstDelay time.Duration
	seedPause  time.Duration
}

func New(cfg *config.Provider, panel Panel, users Users, store Store, notifier Notifier, logger *logrus.Logger, loc *time.Location) *Monitor {
	return &Monitor{
		cfg: cfg, panel: panel, users: users, store: store, notifier: notifier, logger: logger, location: loc,
		now: time.Now, firstDelay: firstPollDelay, seedPause: seedPause,
	}
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
		if m.cfg.Load().SharedHWIDMonitor {
			m.Poll(ctx)
		}
		timer.Reset(PollInterval)
	}
}

type outcome int

const (
	outcomeDone outcome = iota
	outcomeAlerted
	outcomePending
	outcomeDeferred
)

func (m *Monitor) Poll(ctx context.Context) {
	cfg := m.cfg.Load()
	now := m.now()
	keep := retention(cfg)

	state, err := m.store.GetHWIDState(ctx)
	if err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		return
	}
	if state.Polled.IsZero() || now.Sub(state.Polled) > staleAfter {
		m.fill(ctx, keep, time.Time{}, now)
		return
	}

	since := state.Watermark.Add(-overlap)
	devices, complete, err := m.fetchSince(ctx, since)
	if err != nil {
		m.apiFailed(err)
		return
	}
	if !complete {
		m.logger.WithField("devices", len(devices)).Warn(i18n.T("log.shared_hwid_backlog"))
		m.fill(ctx, keep, since, now)
		return
	}
	sort.SliceStable(devices, func(i, j int) bool {
		if !devices[i].CreatedAt.Equal(devices[j].CreatedAt) {
			return devices[i].CreatedAt.Before(devices[j].CreatedAt)
		}
		return devices[i].UserID < devices[j].UserID
	})

	keys := make([]cache.HWIDRecord, len(devices))
	for i, d := range devices {
		keys[i] = cache.HWIDRecord{HWID: d.HWID, UserID: d.UserID}
	}
	known, err := m.store.HWIDRecorded(ctx, keys)
	if err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		return
	}

	watermark := state.Watermark
	blocked := false
	budget := maxAlertsPerPoll
	fresh, alerts, pending := 0, 0, 0
	for i, d := range devices {
		if !known[i] {
			fresh++
			res := outcomeDeferred
			if budget > 0 {
				res = m.handle(ctx, cfg, d, now, &budget)
			}
			switch res {
			case outcomePending, outcomeDeferred:
				pending++
				blocked = true
				continue
			case outcomeAlerted:
				alerts++
			}
		}
		if !blocked && d.CreatedAt.After(watermark) {
			watermark = d.CreatedAt
		}
	}

	if err := m.store.SetHWIDState(ctx, cache.HWIDState{Watermark: watermark, Polled: now}); err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
	}

	entry := m.logger.WithFields(logrus.Fields{"devices": fresh, "alerts": alerts, "pending": pending})
	if alerts > 0 || pending > 0 {
		entry.Info(i18n.T("log.shared_hwid_poll"))
	} else {
		entry.Debug(i18n.T("log.shared_hwid_poll"))
	}
}

func (m *Monitor) fetchSince(ctx context.Context, since time.Time) ([]api.HWIDDevice, bool, error) {
	seen := map[string]bool{}
	var out []api.HWIDDevice
	start := 0
	for page := 0; page < maxPages; page++ {
		devs, _, err := m.panel.ListHWIDDevices(ctx, start, pageSize)
		if err != nil {
			return nil, false, err
		}
		reached := false
		for _, d := range devs {
			if d.CreatedAt.Before(since) {
				reached = true
				continue
			}
			k := deviceKey(d)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, d)
		}
		if reached || len(devs) < pageSize {
			return out, true, nil
		}
		start += pageSize - pageOverlap
	}
	return out, false, nil
}

func (m *Monitor) fill(ctx context.Context, keep time.Duration, floor, now time.Time) {
	if keep <= 0 {
		devs, _, err := m.panel.ListHWIDDevices(ctx, 0, 1)
		if err != nil {
			m.apiFailed(err)
			return
		}
		var newest time.Time
		if len(devs) > 0 {
			newest = devs[0].CreatedAt
		}
		if err := m.store.SetHWIDState(ctx, cache.HWIDState{Watermark: newest, Polled: now}); err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		}
		return
	}

	since := now.Add(-keep)
	if floor.After(since) {
		since = floor
	}
	var newest time.Time
	total, pages, start := 0, 0, 0
	for page := 0; page < maxSeedPages; page++ {
		devs, _, err := m.panel.ListHWIDDevices(ctx, start, seedPageSize)
		if err != nil {
			m.apiFailed(err)
			return
		}
		pages++
		if page == 0 && len(devs) > 0 {
			newest = devs[0].CreatedAt
		}
		reachedOld := false
		records := make([]cache.HWIDRecord, 0, len(devs))
		for _, d := range devs {
			if d.CreatedAt.Before(since) {
				reachedOld = true
				continue
			}
			if d.CreatedAt.After(newest) {
				continue
			}
			records = append(records, cache.HWIDRecord{HWID: d.HWID, UserID: d.UserID, SeenAt: d.CreatedAt})
		}
		if err := m.store.SeedHWIDHistory(ctx, records, keep); err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
			return
		}
		total += len(records)
		if reachedOld || len(devs) < seedPageSize {
			break
		}
		start += seedPageSize - seedPageOverlap
		if !wait(ctx, m.seedPause) {
			return
		}
	}
	if err := m.store.SetHWIDState(ctx, cache.HWIDState{Watermark: newest, Polled: now}); err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		return
	}
	m.logger.WithFields(logrus.Fields{"devices": total, "pages": pages, "days": int(keep / (24 * time.Hour))}).
		Info(i18n.T("log.shared_hwid_seeded"))
}

func (m *Monitor) handle(ctx context.Context, cfg *config.Config, d api.HWIDDevice, now time.Time, budget *int) outcome {
	rec := cache.HWIDRecord{HWID: d.HWID, UserID: d.UserID, SeenAt: d.CreatedAt}
	res := m.check(ctx, cfg, d, &rec, budget)
	if res == outcomeDeferred {
		return outcomeDeferred
	}
	giveUp := res == outcomePending && now.Sub(d.CreatedAt) > giveUpAfter
	if res == outcomePending && !giveUp {
		return outcomePending
	}
	if giveUp {
		m.logger.WithField("userID", d.UserID).Warn(i18n.T("log.shared_hwid_gave_up"))
		res = outcomeDone
	}
	if err := m.store.RecordHWID(ctx, rec, recordTTL(retention(cfg))); err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		if res == outcomeDone && !giveUp {
			return outcomePending
		}
	}
	return res
}

func (m *Monitor) check(ctx context.Context, cfg *config.Config, d api.HWIDDevice, rec *cache.HWIDRecord, budget *int) outcome {
	if wl, err := m.store.IsWhitelisted(ctx, d.UserID); err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		return outcomePending
	} else if wl {
		return outcomeDone
	}

	user, err := m.trigger(ctx, cfg, d.UserID)
	if err != nil {
		if api.IsNotFound(err) {
			return outcomeDone
		}
		m.logger.WithError(err).WithField("userID", d.UserID).Warn(i18n.T("log.shared_hwid_user_failed"))
		return outcomePending
	}
	rec.Username = user.Username
	rec.TelegramID = user.TelegramID
	if !inSquads(cfg.SharedHWIDSquadUUIDs, user.SquadUUIDs) {
		return outcomeDone
	}

	accounts, hidden, err := m.matches(ctx, d, user.TelegramID, retention(cfg))
	if err != nil {
		return outcomePending
	}
	if len(accounts) == 0 {
		return outcomeDone
	}
	reported, err := m.store.GetHWIDReported(ctx, d.UserID)
	if err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		return outcomePending
	}
	others := make([]int64, len(accounts))
	var news []int64
	for i, a := range accounts {
		others[i] = a.UserID
		if !reported[a.UserID] {
			news = append(news, a.UserID)
		}
	}
	if len(news) == 0 {
		return outcomeDone
	}
	if *budget <= 0 {
		return outcomeDeferred
	}

	data, err := m.panel.GetUserByID(ctx, d.UserID)
	if err != nil {
		if api.IsNotFound(err) {
			return outcomeDone
		}
		m.logger.WithError(err).WithField("userID", d.UserID).Warn(i18n.T("log.shared_hwid_user_failed"))
		return outcomePending
	}
	alert := telegram.SharedHWIDAlert{
		User:     api.NewCachedUser(d.UserID, data),
		Status:   data.Status,
		Created:  data.CreatedAt,
		App:      api.AppName(d.UserAgent),
		HWID:     d.HWID,
		Accounts: accounts,
		Hidden:   hidden,
		At:       m.now(),
	}
	if strings.TrimSpace(d.DeviceModel+d.Platform+d.OSVersion) != "" {
		alert.Device = d.DisplayName()
	}

	if err := m.store.MarkHWIDReported(ctx, d.UserID, news, reportedTTL); err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		return outcomePending
	}
	alert.Traffic = m.limitTraffic(ctx, cfg, d.UserID, data)
	text := telegram.FormatSharedHWIDAlert(alert, m.location)
	restore := alert.Traffic.State == telegram.TrafficLimited
	if err := m.notifier.SendSharedHWIDAlert(ctx, text, d.UserID, cfg.AutoDisableDuration, cfg.IgnoreDuration, restore); err != nil {
		m.logger.WithError(err).WithField("userID", d.UserID).Error(i18n.T("log.shared_hwid_alert_failed"))
		if err := m.store.UnmarkHWIDReported(context.WithoutCancel(ctx), d.UserID, news); err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		}
		return outcomePending
	}
	*budget--

	ids := make([]string, len(others))
	for i, id := range others {
		ids[i] = strconv.FormatInt(id, 10)
	}
	m.logger.WithFields(logrus.Fields{
		"userID": d.UserID, "username": data.Username, "others": strings.Join(ids, ","),
	}).Warn(i18n.T("log.shared_hwid_alert"))
	return outcomeAlerted
}

func (m *Monitor) limitTraffic(ctx context.Context, cfg *config.Config, userID int64, data *api.UserData) telegram.TrafficCut {
	if cfg.SharedHWIDTrafficMB <= 0 || len(cfg.SharedHWIDSquadUUIDs) == 0 {
		return telegram.TrafficCut{}
	}
	want := int64(cfg.SharedHWIDTrafficMB) * mib
	current := int64(data.TrafficLimitBytes)
	res := telegram.TrafficCut{Limit: want, Previous: current, Used: data.UsedTraffic()}

	saved, err := m.store.GetTrafficCut(ctx, userID)
	if err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		res.State = telegram.TrafficCutFailed
		return res
	}
	if saved != nil && saved.Limit == current {
		res.Limit, res.Previous, res.State = saved.Limit, saved.Previous, telegram.TrafficLimited
		return res
	}
	if current > 0 && current <= want {
		res.State = telegram.TrafficAlreadyLower
		return res
	}

	if err := m.store.SaveTrafficCut(ctx, userID, cache.TrafficCut{Previous: current, Limit: want}, trafficTTL); err != nil {
		m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		res.State = telegram.TrafficCutFailed
		return res
	}
	if err := m.panel.SetTrafficLimit(ctx, userID, want, false); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.shared_hwid_traffic_failed"))
		if err := m.store.DeleteTrafficCut(context.WithoutCancel(ctx), userID); err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
		}
		res.State = telegram.TrafficCutFailed
		return res
	}
	m.logger.WithFields(logrus.Fields{
		"userID": userID, "limitBytes": want, "previousBytes": current, "usedBytes": res.Used,
	}).Warn(i18n.T("log.shared_hwid_traffic_limited"))
	res.State = telegram.TrafficLimited
	return res
}

func (m *Monitor) trigger(ctx context.Context, cfg *config.Config, userID int64) (*api.CachedUser, error) {
	user, err := m.users.LookupUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(cfg.SharedHWIDSquadUUIDs) == 0 || len(user.SquadUUIDs) > 0 || len(user.Squads) == 0 {
		return user, nil
	}
	data, err := m.panel.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return api.NewCachedUser(userID, data), nil
}

type candidate struct {
	id     int64
	seen   time.Time
	live   bool
	record cache.HWIDRecord
}

func (m *Monitor) matches(ctx context.Context, d api.HWIDDevice, telegramID int64, keep time.Duration) ([]telegram.SharedAccount, int, error) {
	live, err := m.panel.FindHWIDDevices(ctx, d.HWID, lookupSize)
	if err != nil {
		m.apiFailed(err)
		return nil, 0, err
	}
	var hist map[int64]cache.HWIDRecord
	if keep > 0 {
		hist, err = m.store.GetHWIDHistory(ctx, d.HWID)
		if err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
			return nil, 0, err
		}
	}
	oldest := m.now().Add(-keep)

	byID := map[int64]*candidate{}
	get := func(id int64) *candidate {
		c := byID[id]
		if c == nil {
			c = &candidate{id: id}
			byID[id] = c
		}
		return c
	}
	earlier := func(c *candidate, t time.Time) {
		if c.seen.IsZero() || t.Before(c.seen) {
			c.seen = t
		}
	}
	for _, dev := range live {
		if dev.UserID == d.UserID {
			continue
		}
		c := get(dev.UserID)
		c.live = true
		earlier(c, dev.CreatedAt)
	}
	for id, r := range hist {
		if id == d.UserID || r.SeenAt.Before(oldest) {
			continue
		}
		c := get(id)
		c.record = r
		earlier(c, r.SeenAt)
	}
	list := make([]*candidate, 0, len(byID))
	for _, c := range byID {
		if c.seen.After(d.CreatedAt) || (c.seen.Equal(d.CreatedAt) && c.id > d.UserID) {
			continue
		}
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].seen.Equal(list[j].seen) {
			return list[i].seen.Before(list[j].seen)
		}
		return list[i].id < list[j].id
	})

	var out []telegram.SharedAccount
	hidden := 0
	for i, c := range list {
		wl, err := m.store.IsWhitelisted(ctx, c.id)
		if err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_store_failed"))
			return nil, 0, err
		}
		if wl {
			continue
		}
		acc := telegram.SharedAccount{UserID: c.id, Username: c.record.Username, Seen: c.seen}
		accTelegram := c.record.TelegramID
		if i < maxLookups {
			u, err := m.panel.GetUserByID(ctx, c.id)
			switch {
			case err == nil:
				acc.Username = u.Username
				acc.Status = u.Status
				acc.Created = u.CreatedAt
				acc.Squads = api.SquadNames(u.ActiveInternalSquads)
				acc.DeviceRemoved = !c.live
				accTelegram = 0
				if u.TelegramID != nil {
					accTelegram = *u.TelegramID
				}
			case api.IsNotFound(err):
				acc.Deleted = true
			default:
				m.logger.WithError(err).WithField("userID", c.id).Warn(i18n.T("log.shared_hwid_user_failed"))
				return nil, 0, err
			}
		}
		if telegramID != 0 && accTelegram == telegramID {
			hidden++
			continue
		}
		out = append(out, acc)
	}
	return out, hidden, nil
}

func (m *Monitor) apiFailed(err error) {
	if api.IsUnavailable(err) {
		if !m.unavailableWarned.Swap(true) {
			m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_unavailable"))
		}
		return
	}
	m.logger.WithError(err).Warn(i18n.T("log.shared_hwid_poll_failed"))
}

func retention(cfg *config.Config) time.Duration {
	return time.Duration(cfg.SharedHWIDHistoryDays) * 24 * time.Hour
}

func recordTTL(keep time.Duration) time.Duration {
	if keep < processedTTL {
		return processedTTL
	}
	return keep
}

func inSquads(want, squads []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, id := range squads {
		for _, w := range want {
			if id == w {
				return true
			}
		}
	}
	return false
}

func deviceKey(d api.HWIDDevice) string {
	return d.HWID + ":" + strconv.FormatInt(d.UserID, 10)
}

func wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
