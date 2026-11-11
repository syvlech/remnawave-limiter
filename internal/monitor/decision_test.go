package monitor

import (
	"errors"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/geoip"
)

func TestResolveLimit(t *testing.T) {
	cfg := &config.Config{DefaultDeviceLimit: 4}
	cases := []struct {
		name string
		hwid int
		want int
	}{
		{"0 — без лимита", 0, 0},
		{"null (-1) — лимит по умолчанию", -1, 4},
		{"собственный лимит пользователя", 7, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveLimit(cfg, c.hwid); got != c.want {
				t.Errorf("resolveLimit(%d) = %d, ожидалось %d", c.hwid, got, c.want)
			}
		})
	}
}

func TestCheckUser_Decision(t *testing.T) {
	const userID int64 = 7

	sameSubnet := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	fourSubnets := []string{"10.0.1.1", "10.0.2.1", "10.0.3.1", "10.0.4.1"}
	twoASNs := map[string]uint32{"10.0.1.1": 100, "10.0.2.1": 100, "10.0.3.1": 200, "10.0.4.1": 200}

	cases := []struct {
		name       string
		cfg        func(*config.Config)
		limit      int
		ips        []string
		asn        map[string]uint32
		wantAlert  bool
		wantMode   string
		wantGroups int
		wantSubnet int
		wantASN    int
		wantLimit  int
	}{
		{
			name:  "лимит 0 — пользователь без ограничений",
			limit: 0,
			ips:   fourSubnets,
		},
		{
			name:  "IP не больше лимита — нарушения нет",
			limit: 3,
			ips:   sameSubnet,
		},
		{
			name:  "повторы одного IP с разных нод считаются один раз",
			limit: 2,
			ips:   []string{"10.0.0.1", "10.0.0.1", "10.0.0.2", "10.0.0.2", "10.0.0.1"},
		},
		{
			name:       "без группировки считается каждый IP",
			limit:      2,
			ips:        sameSubnet,
			wantAlert:  true,
			wantMode:   "ip",
			wantGroups: 3,
			wantLimit:  2,
		},
		{
			name:  "группировка по подсетям схлопывает адреса одной /24",
			cfg:   func(c *config.Config) { c.SubnetGrouping = true },
			limit: 2,
			ips:   sameSubnet,
		},
		{
			name:       "группировка по подсетям считает разные /24",
			cfg:        func(c *config.Config) { c.SubnetGrouping = true },
			limit:      2,
			ips:        fourSubnets,
			wantAlert:  true,
			wantMode:   "subnet",
			wantGroups: 4,
			wantSubnet: 4,
			wantLimit:  2,
		},
		{
			name:  "префикс /16 объединяет соседние /24",
			cfg:   func(c *config.Config) { c.SubnetGrouping = true; c.SubnetPrefixV4 = 16 },
			limit: 2,
			ips:   fourSubnets,
		},
		{
			name:  "ASN важнее подсетей: 4 подсети, но 2 ASN",
			cfg:   func(c *config.Config) { c.SubnetGrouping = true; c.ASNGrouping = true },
			limit: 2,
			ips:   fourSubnets,
			asn:   twoASNs,
		},
		{
			name:       "ASN-группировка: превышение по числу ASN",
			cfg:        func(c *config.Config) { c.SubnetGrouping = true; c.ASNGrouping = true },
			limit:      1,
			ips:        fourSubnets,
			asn:        twoASNs,
			wantAlert:  true,
			wantMode:   "asn",
			wantGroups: 2,
			wantASN:    2,
			wantLimit:  1,
		},
		{
			name:       "IP без ASN считаются отдельными группами",
			cfg:        func(c *config.Config) { c.ASNGrouping = true },
			limit:      2,
			ips:        fourSubnets,
			asn:        map[string]uint32{"10.0.1.1": 100, "10.0.2.1": 100},
			wantAlert:  true,
			wantMode:   "asn",
			wantGroups: 3,
			wantASN:    3,
			wantLimit:  2,
		},
		{
			name:  "допуск: ровно на пороге — нарушения нет",
			cfg:   func(c *config.Config) { c.Tolerance = 1 },
			limit: 2,
			ips:   sameSubnet,
		},
		{
			name:       "допуск: выше порога — нарушение",
			cfg:        func(c *config.Config) { c.Tolerance = 1 },
			limit:      2,
			ips:        fourSubnets,
			wantAlert:  true,
			wantMode:   "ip",
			wantGroups: 4,
			wantLimit:  2,
		},
		{
			name:  "множитель допуска: порог 2 + floor(2×0.5) = 3",
			cfg:   func(c *config.Config) { c.ToleranceMultiplier = 0.5 },
			limit: 2,
			ips:   sameSubnet,
		},
		{
			name:       "множитель допуска: 4 > 3 — нарушение",
			cfg:        func(c *config.Config) { c.ToleranceMultiplier = 0.5 },
			limit:      2,
			ips:        fourSubnets,
			wantAlert:  true,
			wantMode:   "ip",
			wantGroups: 4,
			wantLimit:  2,
		},
		{
			name:  "в ручном режиме превышение в пределах допуска не даёт мягкого алерта",
			cfg:   func(c *config.Config) { c.Tolerance = 5; c.AutoNotifySoft = true },
			limit: 2,
			ips:   fourSubnets,
		},
		{
			name:  "в авто-режиме без AUTO_NOTIFY_SOFT мягкого алерта нет",
			cfg:   func(c *config.Config) { c.Tolerance = 5; c.ActionMode = "auto" },
			limit: 2,
			ips:   fourSubnets,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.cfg)
			h.cacheUser(userID, c.limit)
			h.resolver.asn = c.asn

			st := h.checkUser(userID, c.ips...)

			if got := len(h.bot.manual) == 1; got != c.wantAlert {
				t.Fatalf("алерт отправлен = %v, ожидалось %v (ручных %d, авто %d, мягких %d)",
					got, c.wantAlert, len(h.bot.manual), len(h.bot.auto), len(h.bot.soft))
			}
			if h.bot.alerts() != len(h.bot.manual) {
				t.Errorf("в ручном режиме допустим только ручной алерт: авто %d, мягких %d", len(h.bot.auto), len(h.bot.soft))
			}
			if len(h.panel.disabled) != 0 {
				t.Errorf("в ручном режиме подписка не отключается: %v", h.panel.disabled)
			}
			if st.errors.Load() != 0 {
				t.Errorf("ошибок проверки = %d, ожидалось 0", st.errors.Load())
			}

			payloads := h.webhook.sent()
			if !c.wantAlert {
				if st.violations.Load() != 0 || len(payloads) != 0 || h.store.called("SetCooldown") != 0 {
					t.Errorf("без нарушения не должно быть ни счётчика, ни webhook, ни кулдауна: violations=%d webhooks=%d cooldowns=%d",
						st.violations.Load(), len(payloads), h.store.called("SetCooldown"))
				}
				return
			}

			if st.violations.Load() != 1 {
				t.Errorf("violations = %d, ожидалось 1", st.violations.Load())
			}
			if len(payloads) != 1 {
				t.Fatalf("webhook отправлен %d раз, ожидался 1", len(payloads))
			}
			v := payloads[0].Violation
			if v.GroupingMode != c.wantMode || v.DeviceGroupCount != c.wantGroups {
				t.Errorf("группировка = %s/%d, ожидалось %s/%d", v.GroupingMode, v.DeviceGroupCount, c.wantMode, c.wantGroups)
			}
			if v.SubnetCount != c.wantSubnet || v.ASNGroupCount != c.wantASN {
				t.Errorf("подсетей/ASN-групп = %d/%d, ожидалось %d/%d", v.SubnetCount, v.ASNGroupCount, c.wantSubnet, c.wantASN)
			}
			if v.DeviceLimit != c.wantLimit {
				t.Errorf("лимит в webhook = %d, ожидалось %d", v.DeviceLimit, c.wantLimit)
			}
		})
	}
}

func TestCheckUser_EarlyExitSkipsEnrichmentAndCooldown(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.ASNGrouping = true })
	h.cacheUser(1, 3)

	h.checkUser(1, "10.0.0.1", "10.0.1.1", "10.0.2.1")

	if h.resolver.lookups != 0 {
		t.Errorf("при числе IP не выше лимита ASN не запрашивается, запросов: %d", h.resolver.lookups)
	}
	if n := h.store.called("IsCooldownActive"); n != 0 {
		t.Errorf("при числе IP не выше лимита кулдаун не проверяется, проверок: %d", n)
	}
}

func TestCheckUser_NullLimitUsesDefault(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.DefaultDeviceLimit = 2 })
	h.panel.users[5] = &api.UserData{ID: 5, Username: "nolimit"}

	st := h.checkUser(5, "10.0.0.1", "10.0.0.2")
	if st.violations.Load() != 0 || h.bot.alerts() != 0 {
		t.Fatalf("2 IP при лимите по умолчанию 2 — не нарушение")
	}
	if cached := h.store.users[5]; cached == nil || cached.HWIDDeviceLimit != -1 {
		t.Fatalf("null-лимит должен кэшироваться как -1: %+v", cached)
	}

	st = h.checkUser(5, "10.0.0.1", "10.0.0.2", "10.0.0.3")
	if st.violations.Load() != 1 || len(h.bot.manual) != 1 {
		t.Fatalf("3 IP при лимите по умолчанию 2 — нарушение: violations=%d, алертов=%d", st.violations.Load(), len(h.bot.manual))
	}
	if got := h.webhook.sent()[0].Violation.DeviceLimit; got != 2 {
		t.Errorf("лимит в webhook = %d, ожидался DEFAULT_DEVICE_LIMIT = 2", got)
	}
	if n := h.panel.called("GetUserByID"); n != 1 {
		t.Errorf("повторная проверка должна брать пользователя из кэша, запросов к панели: %d", n)
	}
}

func TestCheckUser_ZeroLimitFromPanelIsUnlimited(t *testing.T) {
	h := newHarness(t, nil)
	zero := 0
	h.panel.users[5] = &api.UserData{ID: 5, Username: "unlimited", HWIDDeviceLimit: &zero}

	st := h.checkUser(5, "10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5")
	if st.violations.Load() != 0 || h.bot.alerts() != 0 || h.store.called("IsCooldownActive") != 0 {
		t.Errorf("hwidDeviceLimit = 0 означает «без лимита»: violations=%d, алертов=%d", st.violations.Load(), h.bot.alerts())
	}
}

func TestCheckUser_WhitelistedUserIsSkipped(t *testing.T) {
	h := newHarness(t, nil)
	h.store.whitelisted[9] = true
	h.cacheUser(9, 1)

	st := h.checkUser(9, "10.0.0.1", "10.0.0.2", "10.0.0.3")

	if h.bot.alerts() != 0 || st.violations.Load() != 0 || st.errors.Load() != 0 {
		t.Errorf("пользователь из белого списка не проверяется: алертов=%d violations=%d errors=%d",
			h.bot.alerts(), st.violations.Load(), st.errors.Load())
	}
	if n := h.store.called("GetUser") + h.panel.called("GetUserByID"); n != 0 {
		t.Errorf("для пользователя из белого списка данные не запрашиваются, запросов: %d", n)
	}
}

func TestCheckUser_LookupFailuresCountAsErrors(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		prep func(h *harness)
	}{
		{"ошибка проверки белого списка", func(h *harness) { h.store.failOn("IsWhitelisted", boom) }},
		{"ошибка чтения кэша пользователя", func(h *harness) { h.store.failOn("GetUser", boom) }},
		{"ошибка панели при запросе пользователя", func(h *harness) { h.panel.failOn("GetUserByID", boom) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, nil)
			c.prep(h)

			st := h.checkUser(3, "10.0.0.1", "10.0.0.2", "10.0.0.3")

			if st.errors.Load() != 1 {
				t.Errorf("errors = %d, ожидалось 1", st.errors.Load())
			}
			if h.bot.alerts() != 0 || st.violations.Load() != 0 {
				t.Errorf("при сбое проверки алертов быть не должно")
			}
		})
	}
}

func TestGetUser_CachesPanelAnswer(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.UserCacheTTL = 90 })
	email := "u@example.com"
	tgID := int64(4242)
	limit := 3
	h.panel.users[11] = &api.UserData{
		ID: 11, Username: "eleven", Email: &email, TelegramID: &tgID,
		HWIDDeviceLimit: &limit, SubscriptionURL: "https://sub.example/11",
	}

	u, err := h.m.LookupUser(t.Context(), 11)
	if err != nil {
		t.Fatalf("LookupUser: %v", err)
	}
	want := api.CachedUser{
		UserID: 11, Username: "eleven", Email: email, TelegramID: tgID,
		HWIDDeviceLimit: 3, SubscriptionURL: "https://sub.example/11",
	}
	if *u != want {
		t.Errorf("пользователь = %+v, ожидалось %+v", *u, want)
	}
	if ttl := h.store.userTTL[11]; ttl != 90*time.Second {
		t.Errorf("TTL кэша = %v, ожидалось USER_CACHE_TTL = 90s", ttl)
	}

	if _, err := h.m.LookupUser(t.Context(), 11); err != nil {
		t.Fatalf("LookupUser из кэша: %v", err)
	}
	if n := h.panel.called("GetUserByID"); n != 1 {
		t.Errorf("второй запрос должен обслуживаться из кэша, запросов к панели: %d", n)
	}
}

func TestGetUser_CacheWriteFailureIsNotFatal(t *testing.T) {
	h := newHarness(t, nil)
	h.panel.users[12] = &api.UserData{ID: 12, Username: "twelve"}
	h.store.failOn("SetUser", errors.New("redis down"))

	u, err := h.m.LookupUser(t.Context(), 12)
	if err != nil || u == nil || u.Username != "twelve" {
		t.Fatalf("сбой записи в кэш не должен мешать проверке: %+v, %v", u, err)
	}
}

func TestNew_NilDependenciesStayNilInterfaces(t *testing.T) {
	provider := config.NewProvider(testConfig())

	m, err := New(provider, nil, nil, nil, nil, nil, quietLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.api != nil || m.cache != nil || m.bot != nil || m.webhook != nil || m.evidence != nil {
		t.Fatalf("nil-зависимости не должны превращаться в ненулевые интерфейсы: api=%v cache=%v bot=%v webhook=%v evidence=%v",
			m.api != nil, m.cache != nil, m.bot != nil, m.webhook != nil, m.evidence != nil)
	}
	if _, ok := m.resolver.(geoip.NopResolver); !ok {
		t.Errorf("nil-резолвер должен заменяться на geoip.NopResolver, получен %T", m.resolver)
	}

	m.sendWebhook(provider.Load(), "violation_detected", &api.CachedUser{UserID: 1}, activeIPs("10.0.0.1"), 1, 1, 0, 0)
	if ev := m.collectEvidence(t.Context(), 1, nil); ev != nil {
		t.Errorf("без клиента панели признаки не собираются: %+v", ev)
	}

	withAPI, err := New(provider, api.NewClient("http://127.0.0.1:1", "token"), nil, nil, nil, geoip.NopResolver{}, quietLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if withAPI.api == nil || withAPI.evidence == nil {
		t.Error("клиент панели должен использоваться и для проверок, и для сбора признаков")
	}
}

func TestNew_RejectsBadTimezoneAndWhitelist(t *testing.T) {
	badTZ := testConfig()
	badTZ.Timezone = "Mars/Olympus"
	if _, err := New(config.NewProvider(badTZ), nil, nil, nil, nil, nil, quietLogger()); err == nil {
		t.Error("неизвестный часовой пояс должен давать ошибку")
	}

	badIP := testConfig()
	badIP.IPWhitelist = []string{"10.0.0.0/99"}
	if _, err := New(config.NewProvider(badIP), nil, nil, nil, nil, nil, quietLogger()); err == nil {
		t.Error("неверный IP_WHITELIST должен давать ошибку")
	}
}
