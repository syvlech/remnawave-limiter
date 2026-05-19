package monitor

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

type Monitor struct {
	config        *config.Config
	api           *api.Client
	cache         *cache.Cache
	bot           *telegram.Bot
	webhook       *webhook.Client
	logger        *logrus.Logger
	location      *time.Location
	resolver      geoip.Resolver
	ignoredNodes  map[string]struct{}
}

func New(cfg *config.Config, apiClient *api.Client, c *cache.Cache, bot *telegram.Bot, wh *webhook.Client, resolver geoip.Resolver, logger *logrus.Logger) (*Monitor, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("неверная таймзона %q: %w", cfg.Timezone, err)
	}

	ignored := make(map[string]struct{}, len(cfg.IgnoredNodeUUIDs))
	for _, uuid := range cfg.IgnoredNodeUUIDs {
		ignored[uuid] = struct{}{}
	}

	return &Monitor{
		config:       cfg,
		api:          apiClient,
		cache:        c,
		bot:          bot,
		webhook:      wh,
		logger:       logger,
		location:     loc,
		resolver:     resolver,
		ignoredNodes: ignored,
	}, nil
}

func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.config.CheckInterval) * time.Second)
	defer ticker.Stop()

	if m.config.AutoDisableDuration > 0 {
		go m.restoreLoop(ctx)
	}

	m.logger.Info(i18n.T("log.monitoring_started"))

	m.check(ctx)

	for {
		select {
		case <-ctx.Done():
			m.logger.Info(i18n.T("log.monitoring_stopped"))
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

func (m *Monitor) check(ctx context.Context) {
	allNodes, err := m.api.GetActiveNodes(ctx)
	if err != nil {
		m.logger.WithError(err).Error("Ошибка получения активных нод")
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

	activeWindow := time.Duration(m.config.ActiveIPWindow) * time.Second
	cutoff := time.Now().Add(-activeWindow)
	aggregated := make(map[string][]api.ActiveIP)

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
				aggregated[entry.UserID] = append(aggregated[entry.UserID], api.ActiveIP{
					IP:       ip.IP,
					LastSeen: ip.LastSeen,
					NodeName: res.nodeName,
					NodeUUID: res.nodeUUID,
				})
			}
		}
	}

	m.logger.WithField("users", len(aggregated)).Debug("Проверка пользователей")

	for userID, ips := range aggregated {
		m.checkUser(ctx, userID, ips)
	}
}

func (m *Monitor) checkUser(ctx context.Context, userID string, activeIPs []api.ActiveIP) {
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
	case m.config.ASNGrouping:
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
	case m.config.SubnetGrouping:
		seen := make(map[string]struct{})
		for _, ip := range uniqueIPs {
			seen[subnetPrefix(ip.IP, m.config.SubnetPrefixV4)] = struct{}{}
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

	limit := m.resolveLimit(user.HWIDDeviceLimit)
	if limit == 0 {
		return
	}

	effectiveTolerance := m.config.Tolerance + int(float64(limit)*m.config.ToleranceMultiplier)
	if deviceCount <= limit+effectiveTolerance {
		return
	}

	active, err := m.cache.IsCooldownActive(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка проверки cooldown")
		return
	}
	if active {
		return
	}

	if err := m.cache.SetCooldown(ctx, userID, time.Duration(m.config.Cooldown)*time.Second); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка установки cooldown")
	}

	violationCount, err := m.cache.IncrViolationCount(ctx, userID)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка инкремента счётчика нарушений")
		violationCount = 1
	}

	thresholdWindow := time.Duration(m.config.ViolationThresholdWindow) * time.Second
	thresholdCount, err := m.cache.IncrThresholdCount(ctx, userID, thresholdWindow)
	if err != nil {
		m.logger.WithError(err).WithField("userID", userID).Error("Ошибка инкремента порогового счётчика")
		thresholdCount = 1
	}

	if thresholdCount < int64(m.config.ViolationThreshold) {
		m.logger.WithFields(logrus.Fields{
			"userID":    userID,
			"username":  user.Username,
			"devices":   deviceCount,
			"limit":     limit,
			"threshold": fmt.Sprintf("%d/%d", thresholdCount, m.config.ViolationThreshold),
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

	m.sendWebhook(ctx, user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)

	if m.config.ActionMode == "auto" {
		m.handleAutoAction(ctx, user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)
	} else {
		m.handleManualAction(user, uniqueIPs, limit, violationCount, subnetGroups, asnGroups)
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

	ttl := time.Duration(m.config.UserCacheTTL) * time.Second
	if err := m.cache.SetUser(ctx, userID, cu, ttl); err != nil {
		m.logger.WithError(err).WithField("userID", userID).Warn("Ошибка кэширования пользователя")
	}

	return cu, nil
}

func (m *Monitor) resolveLimit(hwidDeviceLimit int) int {
	if hwidDeviceLimit == 0 {
		return 0
	}
	if hwidDeviceLimit == -1 {
		return m.config.DefaultDeviceLimit
	}
	return hwidDeviceLimit
}

func (m *Monitor) handleManualAction(user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
	text := telegram.FormatManualAlert(user, ips, limit, violationCount, m.location, subnetGroups, m.config.SubnetGrouping, asnGroups, m.config.ASNGrouping)
	if err := m.bot.SendManualAlert(text, user.UUID, user.UserID, m.config.AutoDisableDuration, m.config.IgnoreDuration); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error("Ошибка отправки manual alert")
	}
}

func (m *Monitor) handleAutoAction(ctx context.Context, user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
	if err := m.api.DisableUser(ctx, user.UUID); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error("Ошибка отключения пользователя")
		return
	}

	if m.config.AutoDisableDuration > 0 {
		duration := time.Duration(m.config.AutoDisableDuration) * time.Minute
		if err := m.cache.SetRestoreTimer(ctx, user.UUID, duration); err != nil {
			m.logger.WithError(err).WithField("userID", user.UserID).Error("Ошибка установки таймера восстановления")
		}
	}

	text := telegram.FormatAutoAlert(user, ips, limit, m.config.AutoDisableDuration, violationCount, m.location, subnetGroups, m.config.SubnetGrouping, asnGroups, m.config.ASNGrouping)
	if err := m.bot.SendAutoAlert(text, user.UUID); err != nil {
		m.logger.WithError(err).WithField("userID", user.UserID).Error("Ошибка отправки auto alert")
	}
}

func (m *Monitor) sendWebhook(ctx context.Context, user *api.CachedUser, ips []api.ActiveIP, limit int, violationCount int64, subnetGroups, asnGroups int) {
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

	effectiveTolerance := m.config.Tolerance + int(float64(limit)*m.config.ToleranceMultiplier)
	deviceGroupCount := len(ips)
	groupingMode := "ip"
	switch {
	case m.config.ASNGrouping:
		groupingMode = "asn"
		if asnGroups > 0 {
			deviceGroupCount = asnGroups
		}
	case m.config.SubnetGrouping:
		groupingMode = "subnet"
		if subnetGroups > 0 {
			deviceGroupCount = subnetGroups
		}
	}

	payload := &webhook.Payload{
		Event:      "violation_detected",
		ActionMode: m.config.ActionMode,
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
			AutoDisableDurationMin: m.config.AutoDisableDuration,
		},
		Timestamp: time.Now(),
	}

	m.webhook.Send(ctx, payload)
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
