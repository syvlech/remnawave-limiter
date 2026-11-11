package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/i18n"
)

func nodeEntries(userID int64, at time.Time, ips ...string) []api.UserIPEntry {
	infos := make([]api.IPInfo, len(ips))
	for i, ip := range ips {
		infos[i] = api.IPInfo{IP: ip, LastSeen: at}
	}
	return []api.UserIPEntry{{UserID: userID, IPs: infos}}
}

func TestCheck_HealthTimestamp(t *testing.T) {
	boom := errors.New("boom")
	twoNodes := []api.Node{{UUID: "node-1", Name: "One"}, {UUID: "node-2", Name: "Two"}}

	cases := []struct {
		name        string
		cfg         func(*config.Config)
		prep        func(h *harness)
		wantHealthy bool
		wantLevel   logrus.Level
		wantLog     string
		wantFetches int
	}{
		{
			name: "обычный цикл засчитывается",
			prep: func(h *harness) {
				h.panel.nodes = twoNodes
				h.cacheUser(1, 5)
				h.panel.ips["node-1"] = nodeEntries(1, time.Now(), "10.0.0.1")
			},
			wantHealthy: true,
			wantLevel:   logrus.InfoLevel,
			wantLog:     "log.check",
			wantFetches: 2,
		},
		{
			name:        "ноды без подключений — цикл засчитывается",
			prep:        func(h *harness) { h.panel.nodes = twoNodes },
			wantHealthy: true,
			wantLevel:   logrus.InfoLevel,
			wantLog:     "log.check",
			wantFetches: 2,
		},
		{
			name: "часть нод не ответила — цикл засчитывается с предупреждением",
			prep: func(h *harness) {
				h.panel.nodes = twoNodes
				h.panel.nodeErrs["node-2"] = boom
			},
			wantHealthy: true,
			wantLevel:   logrus.WarnLevel,
			wantLog:     "log.nodes_partial",
			wantFetches: 2,
		},
		{
			name: "ни одна нода не ответила — цикл не засчитывается",
			prep: func(h *harness) {
				h.panel.nodes = twoNodes
				h.panel.nodeErrs["node-1"] = boom
				h.panel.nodeErrs["node-2"] = boom
			},
			wantLevel:   logrus.ErrorLevel,
			wantLog:     "log.nodes_all_failed",
			wantFetches: 2,
		},
		{
			name: "список нод получить не удалось — цикл не засчитывается",
			prep: func(h *harness) {
				h.panel.nodes = twoNodes
				h.panel.failOn("GetActiveNodes", boom)
			},
			wantLevel: logrus.ErrorLevel,
			wantLog:   "log.nodes_fetch_failed",
		},
		{
			name:      "панель не вернула ни одной активной ноды — цикл не засчитывается",
			prep:      func(h *harness) {},
			wantLevel: logrus.WarnLevel,
			wantLog:   "log.nodes_none",
		},
		{
			name:        "все ноды исключены настройкой — цикл засчитывается",
			cfg:         func(c *config.Config) { c.IgnoredNodeUUIDs = []string{"node-1", "node-2"} },
			prep:        func(h *harness) { h.panel.nodes = []api.Node{{UUID: "node-1"}, {UUID: "NODE-2"}} },
			wantHealthy: true,
			wantLevel:   logrus.DebugLevel,
			wantLog:     "log.nodes_all_ignored",
		},
		{
			name: "ни одного пользователя проверить не удалось — цикл не засчитывается",
			prep: func(h *harness) {
				h.panel.nodes = twoNodes
				h.panel.ips["node-1"] = nodeEntries(1, time.Now(), "10.0.0.1")
				h.panel.ips["node-2"] = nodeEntries(2, time.Now(), "10.0.0.2")
				h.store.failOn("GetUser", boom)
			},
			wantLevel:   logrus.ErrorLevel,
			wantLog:     "log.users_all_failed",
			wantFetches: 2,
		},
		{
			name: "часть пользователей проверить не удалось — цикл засчитывается",
			prep: func(h *harness) {
				h.panel.nodes = twoNodes
				h.cacheUser(1, 5)
				h.panel.ips["node-1"] = nodeEntries(1, time.Now(), "10.0.0.1")
				h.panel.ips["node-2"] = nodeEntries(2, time.Now(), "10.0.0.2")
			},
			wantHealthy: true,
			wantLevel:   logrus.InfoLevel,
			wantLog:     "log.check",
			wantFetches: 2,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.cfg)
			c.prep(h)

			if !h.m.LastSuccessfulCheck().IsZero() {
				t.Fatal("до первой проверки отметки быть не должно")
			}
			before := time.Now().Add(-time.Second)
			h.m.check(context.Background())

			last := h.m.LastSuccessfulCheck()
			if healthy := !last.IsZero(); healthy != c.wantHealthy {
				t.Errorf("проверка засчитана = %v, ожидалось %v", healthy, c.wantHealthy)
			}
			if c.wantHealthy && last.Before(before) {
				t.Errorf("отметка успешной проверки %v раньше начала теста %v", last, before)
			}
			if !h.logs.has(c.wantLevel, i18n.T(c.wantLog)) {
				t.Errorf("в логе нет записи %s уровня %s", c.wantLog, c.wantLevel)
			}
			if n := h.panel.called("FetchUsersIPs"); n != c.wantFetches {
				t.Errorf("опрошено нод: %d, ожидалось %d", n, c.wantFetches)
			}
		})
	}
}

func TestCheck_FailedCycleKeepsPreviousTimestamp(t *testing.T) {
	h := newHarness(t, nil)
	h.panel.nodes = []api.Node{{UUID: "node-1"}}
	h.m.check(context.Background())
	if h.m.LastSuccessfulCheck().IsZero() {
		t.Fatal("успешная проверка должна ставить отметку")
	}

	const marker int64 = 1_000_000
	h.m.lastCheckUnix.Store(marker)

	h.panel.nodes = nil
	h.m.check(context.Background())
	if got := h.m.lastCheckUnix.Load(); got != marker {
		t.Errorf("цикл без активных нод не должен обновлять отметку: %d", got)
	}

	h.panel.nodes = []api.Node{{UUID: "node-1"}}
	h.panel.nodeErrs["node-1"] = errors.New("boom")
	h.m.check(context.Background())
	if got := h.m.lastCheckUnix.Load(); got != marker {
		t.Errorf("цикл, где не ответила ни одна нода, не должен обновлять отметку: %d", got)
	}
}

func TestCheck_AggregatesAcrossNodesAndFiltersIPs(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.ActiveIPWindow = 60
		c.IPWhitelist = []string{"192.0.2.0/24"}
		c.IgnoredNodeUUIDs = []string{"node-ignored"}
	})
	now := time.Now()
	stale := now.Add(-5 * time.Minute)

	h.panel.nodes = []api.Node{
		{UUID: "node-1", Name: "One"},
		{UUID: "node-2", Name: "Two"},
		{UUID: "Node-Ignored", Name: "Ignored"},
	}
	h.cacheUser(1, 2)
	h.cacheUser(2, 2)
	h.panel.ips["node-1"] = append(
		nodeEntries(1, now, "10.0.1.1", "10.0.2.1"),
		nodeEntries(2, now, "10.0.1.1", "192.0.2.7")...,
	)
	h.panel.ips["node-2"] = append(
		nodeEntries(1, now, "10.0.3.1", "10.0.1.1"),
		append(nodeEntries(2, now, "10.0.2.1"), nodeEntries(2, stale, "10.0.9.9")...)...,
	)
	h.panel.ips["Node-Ignored"] = nodeEntries(2, now, "10.0.7.7", "10.0.8.8")

	h.m.check(context.Background())
	h.m.webhookWG.Wait()

	if n := h.panel.called("FetchUsersIPs"); n != 2 {
		t.Errorf("игнорируемая нода не должна опрашиваться, опрошено %d", n)
	}
	if len(h.bot.manual) != 1 || h.bot.manual[0].userID != 1 {
		t.Fatalf("алерт должен быть только по пользователю 1 (3 IP с двух нод): %+v", h.bot.manual)
	}
	payloads := h.webhook.sent()
	if len(payloads) != 1 {
		t.Fatalf("webhook отправлен %d раз, ожидался 1", len(payloads))
	}
	nodes := make(map[string]string)
	for _, ip := range payloads[0].Violation.IPs {
		nodes[ip.IP] = ip.NodeUUID
	}
	if len(nodes) != 3 || nodes["10.0.2.1"] != "node-1" || nodes["10.0.3.1"] != "node-2" {
		t.Errorf("IP должны собираться со всех нод с указанием ноды: %v", nodes)
	}
	if h.m.LastSuccessfulCheck().IsZero() {
		t.Error("цикл должен быть засчитан")
	}
}

func TestCheck_CancelledContextDoesNotCountAsSuccess(t *testing.T) {
	h := newHarness(t, nil)
	h.panel.nodes = []api.Node{{UUID: "node-1"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.m.check(ctx)

	if !h.m.LastSuccessfulCheck().IsZero() {
		t.Error("прерванный цикл не должен засчитываться")
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("не дождались: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRun_ChecksImmediatelyAndFollowsIntervalChanges(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.CheckInterval = 3600 })
	h.panel.nodes = []api.Node{{UUID: "node-1"}}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		h.m.Run(ctx)
	}()
	defer func() {
		cancel()
		<-stopped
	}()

	waitFor(t, 2*time.Second, "первая проверка сразу после запуска", func() bool {
		return h.panel.called("GetActiveNodes") == 1
	})
	time.Sleep(100 * time.Millisecond)
	if n := h.panel.called("GetActiveNodes"); n != 1 {
		t.Fatalf("при интервале в час вторая проверка не должна запускаться, проверок: %d", n)
	}

	faster := *h.provider.Load()
	faster.CheckInterval = 1
	h.provider.Store(&faster)

	waitFor(t, 3*time.Second, "проверка по новому интервалу в 1 секунду", func() bool {
		return h.panel.called("GetActiveNodes") >= 2
	})

	slower := *h.provider.Load()
	slower.CheckInterval = 3600
	h.provider.Store(&slower)
	time.Sleep(50 * time.Millisecond)
	settled := h.panel.called("GetActiveNodes")
	time.Sleep(1300 * time.Millisecond)
	if n := h.panel.called("GetActiveNodes"); n != settled {
		t.Errorf("после возврата к интервалу в час проверки должны прекратиться: было %d, стало %d", settled, n)
	}
}

func TestRun_StopsOnContextCancel(t *testing.T) {
	h := newHarness(t, nil)
	h.panel.nodes = []api.Node{{UUID: "node-1"}}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		h.m.Run(ctx)
	}()

	waitFor(t, 2*time.Second, "первая проверка", func() bool { return !h.m.LastSuccessfulCheck().IsZero() })
	cancel()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
	if !h.logs.has(logrus.InfoLevel, i18n.T("log.monitoring_stopped")) {
		t.Error("остановка должна попадать в лог")
	}
}
