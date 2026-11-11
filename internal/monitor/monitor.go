package monitor

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/geoip"
	"github.com/remnawave/limiter/internal/i18n"
	"github.com/remnawave/limiter/internal/telegram"
	"github.com/remnawave/limiter/internal/webhook"
)

const (
	statsTopN = 5

	maxConcurrentNodes = 32

	maxConcurrentUsers = 8

	restoreRetryDelay = 60 * time.Second

	restoreQueueTimeout = 5 * time.Second

	maxRestoreAttempts = 3

	restoreScanInterval = 30 * time.Second

	webhookSendTimeout = webhook.MaxSendDuration + 4*time.Second

	webhookGracePeriod = 15 * time.Second

	maxTolerance = 1 << 30
)

type panelAPI interface {
	GetActiveNodes(ctx context.Context) ([]api.Node, error)
	FetchUsersIPs(ctx context.Context, nodeUUID string) ([]api.UserIPEntry, error)
	GetUserByID(ctx context.Context, userID int64) (*api.UserData, error)
	DisableUser(ctx context.Context, userID int64) error
	EnableUser(ctx context.Context, userID int64) error
}

type store interface {
	IsWhitelisted(ctx context.Context, userID int64) (bool, error)
	GetUser(ctx context.Context, userID int64) (*api.CachedUser, error)
	SetUser(ctx context.Context, userID int64, user *api.CachedUser, ttl time.Duration) error
	IsCooldownActive(ctx context.Context, userID int64) (bool, error)
	SetCooldown(ctx context.Context, userID int64, ttl time.Duration) error
	IsSoftCooldownActive(ctx context.Context, userID int64) (bool, error)
	SetSoftCooldown(ctx context.Context, userID int64, ttl time.Duration) error
	IncrViolationCount(ctx context.Context, userID int64) (int64, error)
	IncrThresholdCount(ctx context.Context, userID int64, window time.Duration) (int64, error)
	ResetThresholdCount(ctx context.Context, userID int64) error
	RecordViolation(ctx context.Context, userID int64, username string) error
	GetViolationStats(ctx context.Context, topN int) (*cache.ViolationStats, error)
	SetRestoreTimer(ctx context.Context, userID int64, duration time.Duration) error
	RemoveRestoreTimer(ctx context.Context, userID int64) error
	ClaimExpiredRestoreTimers(ctx context.Context) ([]string, cache.RestoreLease, error)
	RestoreLeaseHeld(ctx context.Context, member string, lease cache.RestoreLease) (bool, error)
	CompleteRestore(ctx context.Context, member string, lease cache.RestoreLease) error
	RescheduleRestore(ctx context.Context, member string, lease cache.RestoreLease, delay time.Duration) error
	IncrRestoreAttempts(ctx context.Context, userID int64) (int64, error)
	ResetRestoreAttempts(ctx context.Context, userID int64) error
}

type notifier interface {
	SendManualAlert(ctx context.Context, text string, userID int64, disableDuration int, ignoreDuration int) error
	SendAutoAlert(ctx context.Context, text string, userID int64) error
	SendSoftAlert(ctx context.Context, text string, userID int64, ignoreDuration int) error
	SendMessage(ctx context.Context, text string) error
	SendDailyReport(ctx context.Context, text string) error
}

type webhookSender interface {
	Send(ctx context.Context, payload *webhook.Payload)
}

type dependencies struct {
	api      panelAPI
	evidence evidenceAPI
	cache    store
	bot      notifier
	webhook  webhookSender
}

type Monitor struct {
	cfg          *config.Provider
	api          panelAPI
	cache        store
	bot          notifier
	webhook      webhookSender
	logger       *logrus.Logger
	location     *time.Location
	resolver     geoip.Resolver
	ignoredNodes map[string]struct{}
	ipWhitelist  *ipFilter

	lastCheckUnix atomic.Int64

	evidence       evidenceAPI
	evidenceWarned sync.Map

	trafficReport TrafficReport

	webhookCtx    context.Context
	webhookCancel context.CancelFunc
	webhookWG     sync.WaitGroup
	restoreWG     sync.WaitGroup
}

func New(provider *config.Provider, apiClient *api.Client, c *cache.Cache, bot *telegram.Bot, wh *webhook.Client, resolver geoip.Resolver, logger *logrus.Logger) (*Monitor, error) {
	var deps dependencies
	if apiClient != nil {
		deps.api = apiClient
		deps.evidence = apiClient
	}
	if c != nil {
		deps.cache = c
	}
	if bot != nil {
		deps.bot = bot
	}
	if wh != nil {
		deps.webhook = wh
	}
	return newMonitor(provider, deps, resolver, logger)
}

func newMonitor(provider *config.Provider, deps dependencies, resolver geoip.Resolver, logger *logrus.Logger) (*Monitor, error) {
	cfg := provider.Load()

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("error.monitor.timezone"), cfg.Timezone, err)
	}

	ignored := make(map[string]struct{}, len(cfg.IgnoredNodeUUIDs))
	for _, uuid := range cfg.IgnoredNodeUUIDs {
		ignored[uuid] = struct{}{}
	}

	ipWhitelist, err := newIPFilter(cfg.IPWhitelist)
	if err != nil {
		return nil, fmt.Errorf("IP_WHITELIST: %w", err)
	}

	if resolver == nil {
		resolver = geoip.NopResolver{}
	}

	webhookCtx, webhookCancel := context.WithCancel(context.Background())

	return &Monitor{
		cfg:           provider,
		api:           deps.api,
		cache:         deps.cache,
		bot:           deps.bot,
		webhook:       deps.webhook,
		logger:        logger,
		location:      loc,
		resolver:      resolver,
		ignoredNodes:  ignored,
		ipWhitelist:   ipWhitelist,
		evidence:      deps.evidence,
		webhookCtx:    webhookCtx,
		webhookCancel: webhookCancel,
	}, nil
}

func (m *Monitor) Run(ctx context.Context) {
	interval := m.cfg.Load().CheckInterval
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	configChanged := make(chan struct{}, 1)
	m.cfg.Watch(func(*config.Config) {
		select {
		case configChanged <- struct{}{}:
		default:
		}
	})
	applyInterval := func() {
		if cur := m.cfg.Load().CheckInterval; cur != interval {
			interval = cur
			ticker.Reset(time.Duration(interval) * time.Second)
		}
	}

	m.restoreWG.Add(1)
	go func() {
		defer m.restoreWG.Done()
		m.restoreLoop(ctx)
	}()
	go m.dailyReportLoop(ctx)

	m.logger.Info(i18n.T("log.monitoring_started"))

	m.check(ctx)

	for {
		select {
		case <-ctx.Done():
			m.logger.Info(i18n.T("log.monitoring_stopped"))
			m.restoreWG.Wait()
			m.waitWebhooks(webhookGracePeriod)
			return
		case <-configChanged:
			applyInterval()
		case <-ticker.C:
			m.check(ctx)
			applyInterval()
		}
	}
}

func (m *Monitor) waitWebhooks(grace time.Duration) {
	done := make(chan struct{})
	go func() {
		m.webhookWG.Wait()
		close(done)
	}()

	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
		m.webhookCancel()
		<-done
	}
}

func (m *Monitor) check(ctx context.Context) {
	started := time.Now()

	allNodes, err := m.api.GetActiveNodes(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		m.logger.WithError(err).Error(i18n.T("log.nodes_fetch_failed"))
		return
	}

	if len(allNodes) == 0 {
		m.logger.Warn(i18n.T("log.nodes_none"))
		return
	}

	nodes := make([]api.Node, 0, len(allNodes))
	skipped := 0
	for _, n := range allNodes {
		if _, ignored := m.ignoredNodes[strings.ToLower(n.UUID)]; ignored {
			skipped++
			continue
		}
		nodes = append(nodes, n)
	}

	if skipped > 0 {
		m.logger.WithField("skipped", skipped).Debug(i18n.T("log.nodes_ignored"))
	}

	if len(nodes) == 0 {
		m.logger.Debug(i18n.T("log.nodes_all_ignored"))
		m.lastCheckUnix.Store(time.Now().Unix())
		return
	}

	results, failed := m.fetchNodes(ctx, nodes)
	if ctx.Err() != nil {
		return
	}

	if failed == len(nodes) {
		m.logger.WithField("nodes", failed).Error(i18n.T("log.nodes_all_failed"))
		return
	}
	if failed > 0 {
		m.logger.WithFields(logrus.Fields{
			"failed": failed,
			"total":  len(nodes),
		}).Warn(i18n.T("log.nodes_partial"))
	}

	cfg := m.cfg.Load()
	cutoff := time.Now().Add(-time.Duration(cfg.ActiveIPWindow) * time.Second)
	aggregated := make(map[int64][]api.ActiveIP)
	whitelistedIPs := 0
	staleIPs := 0

	for _, res := range results {
		for _, entry := range res.entries {
			for _, ip := range entry.IPs {
				if ip.LastSeen.Before(cutoff) {
					staleIPs++
					continue
				}
				if m.ipWhitelist.Match(ip.IP) {
					whitelistedIPs++
					continue
				}
				aggregated[entry.UserID] = append(aggregated[entry.UserID], api.ActiveIP{
					IP:       ip.IP,
					LastSeen: ip.LastSeen,
					NodeName: res.nodeName,
					NodeUUID: res.nodeUUID,
				})
			}
		}
	}

	var st checkStats
	m.checkUsers(ctx, aggregated, &st)
	if ctx.Err() != nil {
		return
	}

	userErrors := st.errors.Load()
	summary := logrus.Fields{
		"nodes":      fmt.Sprintf("%d/%d", len(nodes)-failed, len(nodes)),
		"users":      len(aggregated),
		"violations": st.violations.Load(),
		"took":       time.Since(started).Truncate(time.Millisecond).String(),
	}
	if userErrors > 0 {
		summary["errors"] = userErrors
	}
	if len(aggregated) > 0 && userErrors >= int64(len(aggregated)) {
		m.logger.WithFields(summary).Error(i18n.T("log.users_all_failed"))
	} else {
		m.lastCheckUnix.Store(time.Now().Unix())
		m.logger.WithFields(summary).Info(i18n.T("log.check"))
	}

	m.logger.WithFields(logrus.Fields{
		"staleIPs":    staleIPs,
		"whitelisted": whitelistedIPs,
		"softAlerts":  st.soft.Load(),
	}).Debug(i18n.T("log.check_details"))
}

type checkStats struct {
	violations atomic.Int64
	soft       atomic.Int64
	errors     atomic.Int64
}

type nodeResult struct {
	nodeName string
	nodeUUID string
	entries  []api.UserIPEntry
}

func (m *Monitor) fetchNodes(ctx context.Context, nodes []api.Node) ([]nodeResult, int) {
	results := make([]nodeResult, len(nodes))
	ok := make([]bool, len(nodes))

	sem := make(chan struct{}, maxConcurrentNodes)
	var wg sync.WaitGroup

	for i, node := range nodes {
		wg.Add(1)
		go func(idx int, n api.Node) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			entries, err := m.api.FetchUsersIPs(ctx, n.UUID)
			if err != nil {
				if ctx.Err() == nil {
					m.logger.WithError(err).WithFields(logrus.Fields{
						"node":     n.Name,
						"nodeUUID": n.UUID,
					}).Error(i18n.T("log.node_ips_failed"))
				}
				return
			}

			results[idx] = nodeResult{nodeName: n.Name, nodeUUID: n.UUID, entries: entries}
			ok[idx] = true
		}(i, node)
	}

	wg.Wait()

	out := make([]nodeResult, 0, len(nodes))
	failed := 0
	for i := range results {
		if ok[i] {
			out = append(out, results[i])
			continue
		}
		failed++
	}
	return out, failed
}

func (m *Monitor) checkUsers(ctx context.Context, aggregated map[int64][]api.ActiveIP, st *checkStats) {
	if len(aggregated) == 0 {
		return
	}

	workers := maxConcurrentUsers
	if len(aggregated) < workers {
		workers = len(aggregated)
	}

	type job struct {
		userID int64
		ips    []api.ActiveIP
	}

	jobs := make(chan job)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				m.checkUser(ctx, j.userID, j.ips, st)
			}
		}()
	}

	for userID, ips := range aggregated {
		select {
		case jobs <- job{userID: userID, ips: ips}:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func (m *Monitor) checkUser(ctx context.Context, userID int64, activeIPs []api.ActiveIP, st *checkStats) {
	cfg := m.cfg.Load()

	whitelisted, err := m.cache.IsWhitelisted(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.whitelist_check_failed"))
		st.errors.Add(1)
		return
	}
	if whitelisted {
		return
	}

	user, err := m.getUser(ctx, cfg, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.user_fetch_failed"))
		st.errors.Add(1)
		return
	}

	limit := resolveLimit(cfg, user.HWIDDeviceLimit)
	if limit == 0 {
		return
	}

	uniqueMap := make(map[string]api.ActiveIP, len(activeIPs))
	for _, ip := range activeIPs {
		existing, ok := uniqueMap[ip.IP]
		if !ok || ip.LastSeen.After(existing.LastSeen) {
			uniqueMap[ip.IP] = ip
		}
	}

	if len(uniqueMap) <= limit {
		return
	}

	uniqueIPs := make([]api.ActiveIP, 0, len(uniqueMap))
	for _, ip := range uniqueMap {
		uniqueIPs = append(uniqueIPs, ip)
	}

	for i := range uniqueIPs {
		if info, ok := m.resolver.Lookup(uniqueIPs[i].IP); ok {
			uniqueIPs[i].ASN = info.Number
			uniqueIPs[i].ASNOrg = info.Org
		}
	}

	deviceCount := len(uniqueIPs)
	subnetGroups := 0
	asnGroups := 0
	switch {
	case cfg.ASNGrouping:
		seenASN := make(map[uint32]struct{})
		unknown := 0
		for _, ip := range uniqueIPs {
			if ip.ASN == 0 {
				unknown++
				continue
			}
			seenASN[ip.ASN] = struct{}{}
		}
		asnGroups = len(seenASN) + unknown
		deviceCount = asnGroups
	case cfg.SubnetGrouping:
		seen := make(map[string]struct{})
		for _, ip := range uniqueIPs {
			seen[subnetPrefix(ip.IP, cfg.SubnetPrefixV4)] = struct{}{}
		}
		subnetGroups = len(seen)
		deviceCount = subnetGroups
	}

	banThreshold := limit + effectiveTolerance(cfg, limit)

	if deviceCount > banThreshold {
		m.handleHardViolation(ctx, cfg, user, uniqueIPs, limit, deviceCount, subnetGroups, asnGroups, st)
		return
	}

	if cfg.ActionMode == "auto" && cfg.AutoNotifySoft && deviceCount > limit {
		m.handleSoftWarning(ctx, cfg, user, uniqueIPs, limit, deviceCount, banThreshold, subnetGroups, asnGroups, st)
	}
}

func (m *Monitor) handleHardViolation(ctx context.Context, cfg *config.Config, user *api.CachedUser, uniqueIPs []api.ActiveIP, limit, deviceCount, subnetGroups, asnGroups int, st *checkStats) {
	userID := user.UserID

	active, err := m.cache.IsCooldownActive(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.cooldown_check_failed"))
		return
	}
	if active {
		return
	}

	if err := m.cache.SetCooldown(ctx, userID, time.Duration(cfg.Cooldown)*time.Second); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.cooldown_set_failed"))
		return
	}

	violationCount, err := m.cache.IncrViolationCount(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.violation_increment_failed"))
		violationCount = 1
	}

	thresholdWindow := time.Duration(cfg.ViolationThresholdWindow) * time.Second
	thresholdCount, err := m.cache.IncrThresholdCount(ctx, userID, thresholdWindow)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.threshold_increment_failed"))
		thresholdCount = 1
	}

	if thresholdCount < int64(cfg.ViolationThreshold) {
		m.logger.WithFields(logrus.Fields{
			"userID":    userID,
			"username":  user.Username,
			"devices":   deviceCount,
			"limit":     limit,
			"threshold": fmt.Sprintf("%d/%d", thresholdCount, cfg.ViolationThreshold),
		}).Warn(i18n.T("log.threshold_not_reached"))
		return
	}

	if err := m.cache.ResetThresholdCount(ctx, userID); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.threshold_reset_failed"))
	}

	st.violations.Add(1)

	m.logger.WithFields(logrus.Fields{
		"userID":     userID,
		"username":   user.Username,
		"ips":        len(uniqueIPs),
		"devices":    deviceCount,
		"limit":      limit,
		"violations": violationCount,
	}).Warn(i18n.T("log.limit_exceeded"))

	if err := m.cache.RecordViolation(ctx, userID, user.Username); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Warn(i18n.T("log.stats_record_failed"))
	}

	m.sendWebhook(cfg, "violation_detected", user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)

	if cfg.ActionMode == "auto" {
		m.handleAutoAction(ctx, cfg, user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)
	} else {
		m.handleManualAction(ctx, cfg, user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)
	}
}

func (m *Monitor) handleSoftWarning(ctx context.Context, cfg *config.Config, user *api.CachedUser, uniqueIPs []api.ActiveIP, limit, deviceCount, banThreshold, subnetGroups, asnGroups int, st *checkStats) {
	userID := user.UserID

	active, err := m.cache.IsSoftCooldownActive(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.soft_cooldown_check_failed"))
		return
	}
	if active {
		return
	}

	if err := m.cache.SetSoftCooldown(ctx, userID, time.Duration(cfg.Cooldown)*time.Second); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.soft_cooldown_set_failed"))
	}

	st.soft.Add(1)

	m.logger.WithFields(logrus.Fields{
		"userID":       userID,
		"username":     user.Username,
		"ips":          len(uniqueIPs),
		"devices":      deviceCount,
		"limit":        limit,
		"banThreshold": banThreshold,
	}).Warn(i18n.T("log.soft_warning"))

	m.sendWebhook(cfg, "soft_violation_detected", user, uniqueIPs, limit, 0, subnetGroups, asnGroups)

	ev := m.collectEvidence(ctx, user.UserID, uniqueIPs)
	text := telegram.FormatSoftAlert(user, uniqueIPs, limit, banThreshold, m.location, subnetGroups, cfg.SubnetGrouping, asnGroups, cfg.ASNGrouping, ev)
	if err := m.bot.SendSoftAlert(ctx, text, user.UserID, cfg.IgnoreDuration); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.soft_alert_failed"))
	}
}

type TrafficReport interface {
	HeavyReport(ctx context.Context) (*telegram.HeavyTrafficReport, error)
}

func (m *Monitor) SetTrafficReport(r TrafficReport) {
	m.trafficReport = r
}

func (m *Monitor) LookupUser(ctx context.Context, userID int64) (*api.CachedUser, error) {
	return m.getUser(ctx, m.cfg.Load(), userID)
}

func (m *Monitor) getUser(ctx context.Context, cfg *config.Config, userID int64) (*api.CachedUser, error) {
	cached, err := m.cache.GetUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("cache get user: %w", err)
	}
	if cached != nil {
		return cached, nil
	}

	userData, err := m.api.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("api get user: %w", err)
	}

	cu := &api.CachedUser{
		UserID:   userID,
		Username: userData.Username,
	}

	if userData.Email != nil {
		cu.Email = *userData.Email
	}
	if userData.TelegramID != nil {
		cu.TelegramID = *userData.TelegramID
	}
	if userData.HWIDDeviceLimit != nil {
		cu.HWIDDeviceLimit = *userData.HWIDDeviceLimit
	} else {
		cu.HWIDDeviceLimit = -1
	}
	cu.SubscriptionURL = userData.SubscriptionURL

	ttl := time.Duration(cfg.UserCacheTTL) * time.Second
	if err := m.cache.SetUser(ctx, userID, cu, ttl); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Warn(i18n.T("log.user_cache_failed"))
	}

	return cu, nil
}

func effectiveTolerance(cfg *config.Config, limit int) int {
	t := float64(cfg.Tolerance) + math.Floor(float64(limit)*cfg.ToleranceMultiplier)
	switch {
	case math.IsNaN(t) || t <= 0:
		return 0
	case t >= maxTolerance:
		return maxTolerance
	}
	return int(t)
}

func resolveLimit(cfg *config.Config, hwidDeviceLimit int) int {
	if hwidDeviceLimit == 0 {
		return 0
	}
	if hwidDeviceLimit == -1 {
		return cfg.DefaultDeviceLimit
	}
	return hwidDeviceLimit
}

func (m *Monitor) handleManualAction(ctx context.Context, cfg *config.Config, user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
	ev := m.collectEvidence(ctx, user.UserID, ips)
	text := telegram.FormatManualAlert(user, ips, limit, violationCount, m.location, subnetGroups, cfg.SubnetGrouping, asnGroups, cfg.ASNGrouping, ev)
	if err := m.bot.SendManualAlert(ctx, text, user.UserID, cfg.AutoDisableDuration, cfg.IgnoreDuration); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error(i18n.T("log.manual_alert_failed"))
	}
}

func (m *Monitor) handleAutoAction(ctx context.Context, cfg *config.Config, user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
	err := m.api.DisableUser(ctx, user.UserID)
	if api.IsAlreadyDisabled(err) {
		m.logger.WithField("userID", user.UserID).Info(i18n.T("log.user_already_disabled"))
		m.extendCooldown(ctx, cfg, user.UserID)
		return
	}
	if err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error(i18n.T("log.user_disable_failed"))
		if ctx.Err() == nil {
			if sendErr := m.bot.SendMessage(ctx, fmt.Sprintf(i18n.T("auto.disable_failed"), user.UserID)); sendErr != nil {
				m.logger.WithError(sendErr).Error(i18n.T("log.auto_alert_failed"))
			}
		}
		return
	}
	m.extendCooldown(ctx, cfg, user.UserID)

	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreQueueTimeout)
	defer cancel()
	disabledFor := cfg.AutoDisableDuration
	if cfg.AutoDisableDuration > 0 {
		duration := time.Duration(cfg.AutoDisableDuration) * time.Minute
		if err := m.cache.SetRestoreTimer(storeCtx, user.UserID, duration); err != nil {
			m.logger.WithError(err).WithFields(logrus.Fields{
				"userID":      user.UserID,
				"durationMin": cfg.AutoDisableDuration,
			}).Error(i18n.T("log.restore_timer_missing"))
			disabledFor = 0
		}
	} else if err := m.cache.RemoveRestoreTimer(storeCtx, user.UserID); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).
			Error(i18n.T("log.restore_old_timer"))
	}

	ev := m.collectEvidence(ctx, user.UserID, ips)
	text := telegram.FormatAutoAlert(user, ips, limit, disabledFor, violationCount, m.location, subnetGroups, cfg.SubnetGrouping, asnGroups, cfg.ASNGrouping, ev)
	if err := m.bot.SendAutoAlert(ctx, text, user.UserID); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error(i18n.T("log.auto_alert_failed"))
	}
}

func (m *Monitor) extendCooldown(ctx context.Context, cfg *config.Config, userID int64) {
	need := time.Duration(cfg.ActiveIPWindow+cfg.CheckInterval) * time.Second
	if need <= time.Duration(cfg.Cooldown)*time.Second {
		return
	}
	if err := m.cache.SetCooldown(ctx, userID, need); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.cooldown_set_failed"))
	}
}

func (m *Monitor) sendWebhook(cfg *config.Config, event string, user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
	if m.webhook == nil {
		return
	}

	ipPayloads := make([]webhook.IPPayload, len(ips))
	for i, ip := range ips {
		ipPayloads[i] = webhook.IPPayload{
			IP:       ip.IP,
			NodeName: ip.NodeName,
			NodeUUID: ip.NodeUUID,
			LastSeen: ip.LastSeen,
			ASN:      ip.ASN,
			ASNOrg:   ip.ASNOrg,
		}
	}

	tolerance := effectiveTolerance(cfg, limit)
	deviceGroupCount := len(ips)
	groupingMode := "ip"
	switch {
	case cfg.ASNGrouping:
		groupingMode = "asn"
		if asnGroups > 0 {
			deviceGroupCount = asnGroups
		}
	case cfg.SubnetGrouping:
		groupingMode = "subnet"
		if subnetGroups > 0 {
			deviceGroupCount = subnetGroups
		}
	}

	payload := &webhook.Payload{
		Event:      event,
		ActionMode: cfg.ActionMode,
		User: webhook.UserPayload{
			UserID:          user.UserID,
			Username:        user.Username,
			Email:           user.Email,
			TelegramID:      user.TelegramID,
			SubscriptionURL: user.SubscriptionURL,
		},
		Violation: webhook.ViolationPayload{
			IPs:               ipPayloads,
			IPCount:           len(ips),
			DeviceLimit:       limit,
			Tolerance:         tolerance,
			EffectiveLimit:    limit + tolerance,
			ViolationCount24h: violationCount,
			SubnetCount:       subnetGroups,
			ASNGroupCount:     asnGroups,
			DeviceGroupCount:  deviceGroupCount,
			GroupingMode:      groupingMode,
		},
		Action: webhook.ActionPayload{
			AutoDisableDurationMin: cfg.AutoDisableDuration,
		},
		Timestamp: time.Now(),
	}

	sendCtx, cancel := context.WithTimeout(m.webhookCtx, webhookSendTimeout)

	m.webhookWG.Add(1)
	go func() {
		defer m.webhookWG.Done()
		defer cancel()
		m.webhook.Send(sendCtx, payload)
	}()
}

func (m *Monitor) LastSuccessfulCheck() time.Time {
	ts := m.lastCheckUnix.Load()
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

func (m *Monitor) StatsText(ctx context.Context) (string, error) {
	stats, err := m.cache.GetViolationStats(ctx, statsTopN)
	if err != nil {
		return "", err
	}
	return telegram.FormatStats(stats, m.location), nil
}

func (m *Monitor) dailyReportLoop(ctx context.Context) {
	for {
		cfg := m.cfg.Load()
		hour, minute, err := config.ParseDailyReportTime(cfg.DailyReportTime)
		if err != nil {
			hour, minute = 9, 0
		}

		timer := time.NewTimer(time.Until(m.nextDailyReport(time.Now(), hour, minute)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if !m.cfg.Load().DailyReport {
			continue
		}
		m.sendDailyReport(ctx)
	}
}

func (m *Monitor) nextDailyReport(now time.Time, hour, minute int) time.Time {
	now = now.In(m.location)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, m.location)
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, m.location)
	}
	return next
}

func (m *Monitor) sendDailyReport(ctx context.Context) {
	stats, err := m.cache.GetViolationStats(ctx, statsTopN)
	if err != nil {
		m.logger.WithError(err).Error(i18n.T("log.daily_stats_failed"))
		return
	}
	var heavy *telegram.HeavyTrafficReport
	if m.trafficReport != nil {
		h, err := m.trafficReport.HeavyReport(ctx)
		if err != nil {
			m.logger.WithError(err).Warn(i18n.T("log.daily_traffic_failed"))
		} else {
			heavy = h
		}
	}
	text := telegram.FormatDailyReport(stats, m.location, heavy)
	if err := m.bot.SendDailyReport(ctx, text); err != nil {
		m.logger.WithError(err).Error(i18n.T("log.daily_report_failed"))
	}
}

func (m *Monitor) restoreLoop(ctx context.Context) {
	ticker := time.NewTicker(restoreScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.restoreExpired(ctx)
		}
	}
}

func (m *Monitor) restoreExpired(ctx context.Context) {
	expired, lease, err := m.cache.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		m.logger.WithError(err).Error(i18n.T("log.restore_expired_failed"))
		return
	}

	for _, member := range expired {
		if ctx.Err() != nil {
			m.releaseRestore(ctx, member, lease)
			continue
		}
		m.restoreUser(ctx, member, lease)
	}
}

func (m *Monitor) releaseRestore(ctx context.Context, member string, lease cache.RestoreLease) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreQueueTimeout)
	defer cancel()
	if err := m.cache.RescheduleRestore(releaseCtx, member, lease, 0); err != nil {
		m.logger.WithError(err).WithField("member", member).Error(i18n.T("log.restore_requeue_failed"))
	}
}

func (m *Monitor) completeRestore(ctx context.Context, member string, lease cache.RestoreLease) {
	doneCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreQueueTimeout)
	defer cancel()
	if err := m.cache.CompleteRestore(doneCtx, member, lease); err != nil {
		m.logger.WithError(err).WithField("member", member).Error(i18n.T("log.restore_complete_failed"))
	}
}

func (m *Monitor) restoreUser(ctx context.Context, member string, lease cache.RestoreLease) {
	userID, err := strconv.ParseInt(member, 10, 64)
	if err != nil {
		m.logger.WithField("member", member).Warn(i18n.T("log.restore_legacy_skipped"))
		m.completeRestore(ctx, member, lease)
		return
	}

	held, err := m.cache.RestoreLeaseHeld(ctx, member, lease)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error(i18n.T("log.restore_expired_failed"))
		return
	}
	if !held {
		return
	}

	if err := m.api.EnableUser(ctx, userID); err != nil && !api.IsAlreadyEnabled(err) {
		if ctx.Err() != nil {
			m.releaseRestore(ctx, member, lease)
			return
		}

		attempts, attErr := m.cache.IncrRestoreAttempts(ctx, userID)
		if attErr != nil {
			m.logger.WithError(attErr).WithField("userID", userID).Warn(i18n.T("log.restore_attempts_failed"))
			attempts = 1
		}

		if attempts < maxRestoreAttempts {
			if reqErr := m.cache.RescheduleRestore(ctx, member, lease, restoreRetryDelay); reqErr != nil {
				m.logger.WithError(reqErr).WithField("userID", userID).Error(i18n.T("log.restore_requeue_failed"))
			}
			m.logger.WithError(err).WithFields(logrus.Fields{
				"userID":  userID,
				"attempt": attempts,
				"retryIn": restoreRetryDelay,
			}).Warn(i18n.T("log.restore_retry"))
			return
		}

		m.logger.WithError(err).WithFields(logrus.Fields{
			"userID":   userID,
			"attempts": attempts,
		}).Error(i18n.T("log.restore_exhausted"))
		m.completeRestore(ctx, member, lease)

		if sendErr := m.bot.SendMessage(ctx, fmt.Sprintf(i18n.T("restore.failed"), userID)); sendErr != nil {
			m.logger.WithError(sendErr).Error(i18n.T("log.restore_failure_notice_failed"))
		}
		if resetErr := m.cache.ResetRestoreAttempts(ctx, userID); resetErr != nil {
			m.logger.WithError(resetErr).WithField("userID", userID).Debug(i18n.T("log.restore_attempts_reset_failed"))
		}
		return
	}

	m.completeRestore(ctx, member, lease)
	if err := m.cache.ResetRestoreAttempts(ctx, userID); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Debug(i18n.T("log.restore_attempts_reset_failed"))
	}

	m.logger.WithField("userID", userID).Info(i18n.T("log.user_restored"))

	if err := m.bot.SendMessage(ctx, fmt.Sprintf(i18n.T("restore.message"), userID)); err != nil {
		m.logger.WithError(err).Error(i18n.T("log.restore_notice_failed"))
	}
}
