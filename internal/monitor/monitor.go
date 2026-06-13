package monitor

import (
	"context"
	"fmt"
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

const statsTopN = 5

type Monitor struct {
	cfg          *config.Provider
	api          *api.Client
	cache        *cache.Cache
	bot          *telegram.Bot
	webhook      *webhook.Client
	logger       *logrus.Logger
	location     *time.Location
	resolver     geoip.Resolver
	ignoredNodes map[string]struct{}
	ipWhitelist  *ipFilter

	lastCheckUnix atomic.Int64
}

func New(provider *config.Provider, apiClient *api.Client, c *cache.Cache, bot *telegram.Bot, wh *webhook.Client, resolver geoip.Resolver, logger *logrus.Logger) (*Monitor, error) {
	cfg := provider.Load()

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("неверная таймзона %q: %w", cfg.Timezone, err)
	}

	ignored := make(map[string]struct{}, len(cfg.IgnoredNodeUUIDs))
	for _, uuid := range cfg.IgnoredNodeUUIDs {
		ignored[uuid] = struct{}{}
	}

	ipWhitelist, err := newIPFilter(cfg.IPWhitelist)
	if err != nil {
		return nil, fmt.Errorf("IP_WHITELIST: %w", err)
	}

	return &Monitor{
		cfg:          provider,
		api:          apiClient,
		cache:        c,
		bot:          bot,
		webhook:      wh,
		logger:       logger,
		location:     loc,
		resolver:     resolver,
		ignoredNodes: ignored,
		ipWhitelist:  ipWhitelist,
	}, nil
}

func (m *Monitor) Run(ctx context.Context) {
	interval := m.cfg.Load().CheckInterval
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	go m.restoreLoop(ctx)
	go m.dailyReportLoop(ctx)

	m.logger.Info(i18n.T("log.monitoring_started"))

	m.check(ctx)

	for {
		select {
		case <-ctx.Done():
			m.logger.Info(i18n.T("log.monitoring_stopped"))
			return
		case <-ticker.C:
			m.check(ctx)

			if cur := m.cfg.Load().CheckInterval; cur != interval {
				interval = cur
				ticker.Reset(time.Duration(interval) * time.Second)
			}
		}
	}
}

func (m *Monitor) check(ctx context.Context) {
	allNodes, err := m.api.GetActiveNodes(ctx)
	if err != nil {
		m.logger.WithError(err).Error("Ошибка получения активных нод")
		return
	}

	m.lastCheckUnix.Store(time.Now().Unix())

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
		m.logger.WithField("skipped", skipped).Debug("Игнорируемые ноды пропущены")
	}

	if len(nodes) == 0 {
		m.logger.Debug("Нет активных нод")
		return
	}

	type nodeResult struct {
		nodeName string
		nodeUUID string
		entries  []api.UserIPEntry
		err      error
	}

	results := make([]nodeResult, len(nodes))
	var wg sync.WaitGroup

	for i, node := range nodes {
		wg.Add(1)
		go func(idx int, n api.Node) {
			defer wg.Done()
			entries, err := m.api.FetchUsersIPs(ctx, n.UUID)
			results[idx] = nodeResult{
				nodeName: n.Name,
				nodeUUID: n.UUID,
				entries:  entries,
				err:      err,
			}
		}(i, node)
	}

	wg.Wait()

	activeWindow := time.Duration(m.cfg.Load().ActiveIPWindow) * time.Second
	cutoff := time.Now().Add(-activeWindow)
	aggregated := make(map[string][]api.ActiveIP)
	whitelistedIPs := 0

	for _, res := range results {
		if res.err != nil {
			m.logger.WithError(res.err).WithField("node", res.nodeName).Error("Ошибка получения IP с ноды")
			continue
		}

		for _, entry := range res.entries {
			for _, ip := range entry.IPs {
				if ip.LastSeen.Before(cutoff) {
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

	if whitelistedIPs > 0 {
		m.logger.WithField("ips", whitelistedIPs).Debug("IP из whitelist исключены из учёта")
	}

	m.logger.WithField("users", len(aggregated)).Debug("Проверка пользователей")

	for userID, ips := range aggregated {
		m.checkUser(ctx, userID, ips)
	}
}

func (m *Monitor) checkUser(ctx context.Context, userID string, activeIPs []api.ActiveIP) {
	cfg := m.cfg.Load()
	uniqueMap := make(map[string]api.ActiveIP)
	for _, ip := range activeIPs {
		existing, ok := uniqueMap[ip.IP]
		if !ok || ip.LastSeen.After(existing.LastSeen) {
			uniqueMap[ip.IP] = ip
		}
	}

	uniqueIPs := make([]api.ActiveIP, 0, len(uniqueMap))
	for _, ip := range uniqueMap {
		uniqueIPs = append(uniqueIPs, ip)
	}

	if m.resolver != nil {
		for i := range uniqueIPs {
			if info, ok := m.resolver.Lookup(uniqueIPs[i].IP); ok {
				uniqueIPs[i].ASN = info.Number
				uniqueIPs[i].ASNOrg = info.Org
			}
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

	whitelisted, err := m.cache.IsWhitelisted(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка проверки whitelist")
		return
	}
	if whitelisted {
		return
	}

	user, err := m.getUser(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка получения данных пользователя")
		return
	}

	limit := m.resolveLimit(cfg, user.HWIDDeviceLimit)
	if limit == 0 {
		return
	}

	effectiveTolerance := cfg.Tolerance + int(float64(limit)*cfg.ToleranceMultiplier)
	banThreshold := limit + effectiveTolerance

	if deviceCount > banThreshold {
		m.handleHardViolation(ctx, user, uniqueIPs, limit, deviceCount, subnetGroups, asnGroups)
		return
	}

	if cfg.ActionMode == "auto" && cfg.AutoNotifySoft && deviceCount > limit {
		m.handleSoftWarning(ctx, user, uniqueIPs, limit, deviceCount, banThreshold, subnetGroups, asnGroups)
	}
}

func (m *Monitor) handleHardViolation(ctx context.Context, user *api.CachedUser, uniqueIPs []api.ActiveIP, limit, deviceCount, subnetGroups, asnGroups int) {
	cfg := m.cfg.Load()
	userID := user.UserID

	active, err := m.cache.IsCooldownActive(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка проверки cooldown")
		return
	}
	if active {
		return
	}

	if err := m.cache.SetCooldown(ctx, userID, time.Duration(cfg.Cooldown)*time.Second); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка установки cooldown")
	}

	violationCount, err := m.cache.IncrViolationCount(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка инкремента счётчика нарушений")
		violationCount = 1
	}

	thresholdWindow := time.Duration(cfg.ViolationThresholdWindow) * time.Second
	thresholdCount, err := m.cache.IncrThresholdCount(ctx, userID, thresholdWindow)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка инкремента порогового счётчика")
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
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка сброса порогового счётчика")
	}

	m.logger.WithFields(logrus.Fields{
		"userID":     userID,
		"username":   user.Username,
		"ips":        len(uniqueIPs),
		"devices":    deviceCount,
		"limit":      limit,
		"violations": violationCount,
	}).Warn(i18n.T("log.limit_exceeded"))

	if err := m.cache.RecordViolation(ctx, userID, user.Username); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Warn("Ошибка записи статистики нарушения")
	}

	m.sendWebhook(ctx, "violation_detected", user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)

	if cfg.ActionMode == "auto" {
		m.handleAutoAction(ctx, user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)
	} else {
		m.handleManualAction(user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)
	}
}

func (m *Monitor) handleSoftWarning(ctx context.Context, user *api.CachedUser, uniqueIPs []api.ActiveIP, limit, deviceCount, banThreshold, subnetGroups, asnGroups int) {
	cfg := m.cfg.Load()
	userID := user.UserID

	active, err := m.cache.IsSoftCooldownActive(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка проверки soft cooldown")
		return
	}
	if active {
		return
	}

	if err := m.cache.SetSoftCooldown(ctx, userID, time.Duration(cfg.Cooldown)*time.Second); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка установки soft cooldown")
	}

	m.logger.WithFields(logrus.Fields{
		"userID":       userID,
		"username":     user.Username,
		"ips":          len(uniqueIPs),
		"devices":      deviceCount,
		"limit":        limit,
		"banThreshold": banThreshold,
	}).Warn(i18n.T("log.soft_warning"))

	m.sendWebhook(ctx, "soft_violation_detected", user, uniqueIPs, limit, 0, subnetGroups, asnGroups)

	text := telegram.FormatSoftAlert(user, uniqueIPs, limit, banThreshold, m.location, subnetGroups, cfg.SubnetGrouping, asnGroups, cfg.ASNGrouping)
	if err := m.bot.SendMessage(text); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка отправки soft alert")
	}
}

func (m *Monitor) getUser(ctx context.Context, userID string) (*api.CachedUser, error) {
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
		UUID:     userData.UUID,
		UserID:   userID,
		Username: userData.Username,
		Status:   userData.Status,
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

	ttl := time.Duration(m.cfg.Load().UserCacheTTL) * time.Second
	if err := m.cache.SetUser(ctx, userID, cu, ttl); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Warn("Ошибка кэширования пользователя")
	}

	return cu, nil
}

func (m *Monitor) resolveLimit(cfg *config.Config, hwidDeviceLimit int) int {
	if hwidDeviceLimit == 0 {
		return 0
	}
	if hwidDeviceLimit == -1 {
		return cfg.DefaultDeviceLimit
	}
	return hwidDeviceLimit
}

func (m *Monitor) handleManualAction(user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
	cfg := m.cfg.Load()
	text := telegram.FormatManualAlert(user, ips, limit, violationCount, m.location, subnetGroups, cfg.SubnetGrouping, asnGroups, cfg.ASNGrouping)
	if err := m.bot.SendManualAlert(text, user.UUID, user.UserID, cfg.AutoDisableDuration, cfg.IgnoreDuration); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error("Ошибка отправки manual alert")
	}
}

func (m *Monitor) handleAutoAction(ctx context.Context, user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
	cfg := m.cfg.Load()
	if err := m.api.DisableUser(ctx, user.UUID); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error("Ошибка отключения пользователя")
		return
	}

	if cfg.AutoDisableDuration > 0 {
		duration := time.Duration(cfg.AutoDisableDuration) * time.Minute
		if err := m.cache.SetRestoreTimer(ctx, user.UUID, duration); err != nil {
			m.logger.WithError(err).WithField("userID", user.UserID).Error("Ошибка установки таймера восстановления")
		}
	}

	text := telegram.FormatAutoAlert(user, ips, limit, cfg.AutoDisableDuration, violationCount, m.location, subnetGroups, cfg.SubnetGrouping, asnGroups, cfg.ASNGrouping)
	if err := m.bot.SendAutoAlert(text, user.UUID); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error("Ошибка отправки auto alert")
	}
}

func (m *Monitor) sendWebhook(ctx context.Context, event string, user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
	if m.webhook == nil {
		return
	}

	cfg := m.cfg.Load()

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

	effectiveTolerance := cfg.Tolerance + int(float64(limit)*cfg.ToleranceMultiplier)
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
			UUID:            user.UUID,
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
			Tolerance:         effectiveTolerance,
			EffectiveLimit:    limit + effectiveTolerance,
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

	go m.webhook.Send(ctx, payload)
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

		timer := time.NewTimer(time.Until(m.nextDailyReport(hour, minute)))
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

func (m *Monitor) nextDailyReport(hour, minute int) time.Time {
	now := time.Now().In(m.location)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, m.location)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func (m *Monitor) sendDailyReport(ctx context.Context) {
	stats, err := m.cache.GetViolationStats(ctx, statsTopN)
	if err != nil {
		m.logger.WithError(err).Error("Ошибка получения статистики для ежедневного отчёта")
		return
	}
	text := telegram.FormatDailyReport(stats, m.location)
	if err := m.bot.SendMessage(text); err != nil {
		m.logger.WithError(err).Error("Ошибка отправки ежедневного отчёта")
	}
}

func (m *Monitor) restoreLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired, err := m.cache.GetExpiredRestoreTimers(ctx)
			if err != nil {
				m.logger.WithError(err).Error("Ошибка получения истёкших таймеров восстановления")
				continue
			}

			for _, uuid := range expired {
				if err := m.api.EnableUser(ctx, uuid); err != nil {
					m.logger.WithError(err).WithField("uuid", uuid).Error("Ошибка включения пользователя по таймеру")
					continue
				}

				m.logger.WithField("uuid", uuid).Info("Пользователь автоматически включён по таймеру")

				msg := fmt.Sprintf(i18n.T("restore.message"), uuid)
				if err := m.bot.SendMessage(msg); err != nil {
					m.logger.WithError(err).Error("Ошибка отправки уведомления о восстановлении")
				}
			}
		}
	}
}
