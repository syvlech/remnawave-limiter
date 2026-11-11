package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/i18n"
)

var violatingIPs = []string{"10.0.1.1", "10.0.2.1", "10.0.3.1"}

func autoMode(c *config.Config) { c.ActionMode = "auto" }

func alreadyDisabledErr() error {
	return fmt.Errorf("disable user 1: %w", &api.StatusError{
		Method: "POST", Path: "/api/users/1/actions/disable", Code: 400, Body: `{"errorCode":"A029"}`,
	})
}

func alreadyEnabledErr() error {
	return fmt.Errorf("enable user 1: %w", &api.StatusError{
		Method: "POST", Path: "/api/users/1/actions/enable", Code: 400, Body: `{"errorCode":"A030"}`,
	})
}

func TestHardViolation_CooldownGate(t *testing.T) {
	boom := errors.New("redis down")
	cases := []struct {
		name         string
		prep         func(h *harness)
		wantCooldown int
	}{
		{"кулдаун активен — действий нет", func(h *harness) { h.store.cooldown[1] = time.Minute }, 0},
		{"кулдаун не удалось проверить — действий нет", func(h *harness) { h.store.failOn("IsCooldownActive", boom) }, 0},
		{"кулдаун не удалось поставить — алерта нет", func(h *harness) { h.store.failOn("SetCooldown", boom) }, 1},
	}
	for _, mode := range []string{"manual", "auto"} {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				h := newHarness(t, func(cfg *config.Config) { cfg.ActionMode = mode })
				h.cacheUser(1, 2)
				c.prep(h)

				st := h.checkUser(1, violatingIPs...)

				if h.bot.alerts() != 0 || len(h.bot.messages) != 0 {
					t.Errorf("алертов быть не должно: %d алертов, %d сообщений", h.bot.alerts(), len(h.bot.messages))
				}
				if len(h.panel.disabled) != 0 {
					t.Errorf("подписка не должна отключаться: %v", h.panel.disabled)
				}
				if st.violations.Load() != 0 || len(h.webhook.sent()) != 0 {
					t.Errorf("нарушение не должно учитываться: violations=%d, webhooks=%d", st.violations.Load(), len(h.webhook.sent()))
				}
				if n := h.store.called("SetCooldown"); n != c.wantCooldown {
					t.Errorf("SetCooldown вызван %d раз, ожидалось %d", n, c.wantCooldown)
				}
				for _, method := range []string{"IncrViolationCount", "IncrThresholdCount", "RecordViolation"} {
					if n := h.store.called(method); n != 0 {
						t.Errorf("%s не должен вызываться, вызовов: %d", method, n)
					}
				}
			})
		}
	}
}

func TestHardViolation_ThresholdCounting(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.ViolationThreshold = 3
		c.ViolationThresholdWindow = 1800
	})
	h.cacheUser(1, 2)

	for attempt := 1; attempt <= 2; attempt++ {
		st := h.checkUser(1, violatingIPs...)
		if h.bot.alerts() != 0 || st.violations.Load() != 0 {
			t.Fatalf("срабатывание %d из 3: алерта быть не должно", attempt)
		}
		if got := h.store.threshold[1]; got != int64(attempt) {
			t.Fatalf("счётчик порога = %d, ожидалось %d", got, attempt)
		}
		if _, ok := h.store.cooldown[1]; !ok {
			t.Fatalf("срабатывание %d: кулдаун должен ставиться до достижения порога", attempt)
		}
		delete(h.store.cooldown, 1)
	}
	if n := h.store.called("RecordViolation"); n != 0 {
		t.Errorf("до достижения порога нарушение не попадает в статистику, записей: %d", n)
	}
	if len(h.webhook.sent()) != 0 {
		t.Errorf("до достижения порога webhook не отправляется")
	}

	st := h.checkUser(1, violatingIPs...)

	if len(h.bot.manual) != 1 || st.violations.Load() != 1 {
		t.Fatalf("на третьем срабатывании нужен алерт: алертов=%d violations=%d", len(h.bot.manual), st.violations.Load())
	}
	if _, left := h.store.threshold[1]; left {
		t.Errorf("после алерта счётчик порога должен сбрасываться, осталось %d", h.store.threshold[1])
	}
	if len(h.store.recorded) != 1 || h.store.recorded[0] != 1 {
		t.Errorf("нарушение должно попасть в статистику один раз: %v", h.store.recorded)
	}
	if got := h.webhook.sent()[0].Violation.ViolationCount24h; got != 3 {
		t.Errorf("нарушений за 24ч в webhook = %d, ожидалось 3", got)
	}
	for _, w := range h.store.windows {
		if w != 1800*time.Second {
			t.Errorf("окно счётчика порога = %v, ожидалось VIOLATION_THRESHOLD_WINDOW = 30m", w)
		}
	}
}

func TestHardViolation_CounterFailuresStillAlert(t *testing.T) {
	h := newHarness(t, nil)
	h.cacheUser(1, 2)
	boom := errors.New("redis down")
	for _, method := range []string{"IncrViolationCount", "IncrThresholdCount", "ResetThresholdCount", "RecordViolation"} {
		h.store.failOn(method, boom)
	}

	st := h.checkUser(1, violatingIPs...)

	if len(h.bot.manual) != 1 || st.violations.Load() != 1 {
		t.Fatalf("сбой счётчиков не должен скрывать нарушение: алертов=%d violations=%d", len(h.bot.manual), st.violations.Load())
	}
	if got := h.webhook.sent()[0].Violation.ViolationCount24h; got != 1 {
		t.Errorf("при сбое счётчика нарушений используется 1, получено %d", got)
	}
}

func TestHardViolation_ManualModeSendsManualAlert(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.AutoDisableDuration = 45
		c.IgnoreDuration = 90
		c.Cooldown = 600
	})
	user := h.cacheUser(1, 2)

	st := h.checkUser(1, violatingIPs...)

	if len(h.bot.manual) != 1 || h.bot.alerts() != 1 {
		t.Fatalf("ожидался один ручной алерт: ручных %d, всего %d", len(h.bot.manual), h.bot.alerts())
	}
	alert := h.bot.manual[0]
	if alert.userID != 1 || alert.disableDuration != 45 || alert.ignoreDuration != 90 {
		t.Errorf("параметры кнопок = %+v, ожидались userID=1, disable=45, ignore=90", alert)
	}
	if !strings.Contains(alert.text, user.Username) {
		t.Errorf("в тексте алерта нет имени пользователя: %s", alert.text)
	}
	if len(h.panel.disabled) != 0 || h.store.called("SetRestoreTimer") != 0 {
		t.Errorf("в ручном режиме лимитер сам ничего не отключает")
	}
	if got := h.store.cooldown[1]; got != 600*time.Second {
		t.Errorf("кулдаун = %v, ожидалось COOLDOWN = 10m", got)
	}
	if st.violations.Load() != 1 || st.soft.Load() != 0 {
		t.Errorf("violations/soft = %d/%d, ожидалось 1/0", st.violations.Load(), st.soft.Load())
	}

	payloads := h.webhook.sent()
	if len(payloads) != 1 {
		t.Fatalf("webhook отправлен %d раз, ожидался 1", len(payloads))
	}
	p := payloads[0]
	if p.Event != "violation_detected" || p.ActionMode != "manual" || p.User.UserID != 1 {
		t.Errorf("webhook = %s/%s/user %d, ожидалось violation_detected/manual/1", p.Event, p.ActionMode, p.User.UserID)
	}
	if p.Violation.IPCount != 3 || p.Violation.EffectiveLimit != 2 || p.Action.AutoDisableDurationMin != 45 {
		t.Errorf("webhook: ip_count=%d effective_limit=%d duration=%d", p.Violation.IPCount, p.Violation.EffectiveLimit, p.Action.AutoDisableDurationMin)
	}
}

func TestHardViolation_AutoModeDisablesAndSchedulesRestore(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		autoMode(c)
		c.AutoDisableDuration = 45
	})
	h.cacheUser(1, 2)

	st := h.checkUser(1, violatingIPs...)

	if len(h.panel.disabled) != 1 || h.panel.disabled[0] != 1 {
		t.Fatalf("подписка должна быть отключена один раз: %v", h.panel.disabled)
	}
	if got := h.store.timers[1]; got != 45*time.Minute {
		t.Errorf("таймер восстановления = %v, ожидалось 45m", got)
	}
	if len(h.bot.auto) != 1 || h.bot.alerts() != 1 || h.bot.auto[0].userID != 1 {
		t.Fatalf("ожидался один авто-алерт: авто %d, всего %d", len(h.bot.auto), h.bot.alerts())
	}
	if strings.Contains(h.bot.auto[0].text, i18n.T("alert.permanent")) {
		t.Errorf("временное отключение не должно показываться как перманентное: %s", h.bot.auto[0].text)
	}
	if !strings.Contains(h.bot.auto[0].text, i18n.T("alert.restores_at")) {
		t.Errorf("в алерте должно быть время восстановления: %s", h.bot.auto[0].text)
	}
	if st.violations.Load() != 1 || len(h.store.recorded) != 1 {
		t.Errorf("нарушение должно учитываться: violations=%d, записей=%d", st.violations.Load(), len(h.store.recorded))
	}
	if p := h.webhook.sent(); len(p) != 1 || p[0].ActionMode != "auto" {
		t.Errorf("webhook должен уйти с action_mode=auto: %+v", p)
	}
}

func TestHardViolation_AutoModePermanentDisableDropsOldTimer(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		autoMode(c)
		c.AutoDisableDuration = 0
	})
	h.cacheUser(1, 2)
	h.store.timers[1] = time.Hour

	h.checkUser(1, violatingIPs...)

	if len(h.panel.disabled) != 1 {
		t.Fatalf("подписка должна быть отключена: %v", h.panel.disabled)
	}
	if _, left := h.store.timers[1]; left || h.store.called("SetRestoreTimer") != 0 {
		t.Errorf("при перманентном отключении старый таймер снимается, а новый не ставится")
	}
	if len(h.bot.auto) != 1 || !strings.Contains(h.bot.auto[0].text, i18n.T("alert.permanent")) {
		t.Errorf("алерт должен показывать перманентное отключение: %+v", h.bot.auto)
	}
}

func TestHardViolation_AutoModeAlreadyDisabled(t *testing.T) {
	h := newHarness(t, autoMode)
	h.cacheUser(1, 2)
	h.store.timers[1] = 10 * time.Minute
	h.panel.failOn("DisableUser", alreadyDisabledErr())

	h.checkUser(1, violatingIPs...)

	if h.bot.alerts() != 0 || len(h.bot.messages) != 0 {
		t.Errorf("уже отключённая подписка не должна давать алерт: алертов %d, сообщений %d", h.bot.alerts(), len(h.bot.messages))
	}
	if h.store.called("SetRestoreTimer") != 0 || h.store.called("RemoveRestoreTimer") != 0 {
		t.Errorf("таймер уже отключённой подписки не должен меняться")
	}
	if got := h.store.timers[1]; got != 10*time.Minute {
		t.Errorf("существующий таймер = %v, ожидалось 10m", got)
	}
}

func TestHardViolation_AutoModeDisableFailureNotifiesAdmin(t *testing.T) {
	h := newHarness(t, autoMode)
	h.cacheUser(1, 2)
	h.panel.failOn("DisableUser", errors.New("panel 500"))

	h.checkUser(1, violatingIPs...)

	want := fmt.Sprintf(i18n.T("auto.disable_failed"), int64(1))
	if len(h.bot.messages) != 1 || h.bot.messages[0] != want {
		t.Fatalf("админ должен получить сообщение о сбое отключения: %q", h.bot.messages)
	}
	if h.bot.alerts() != 0 {
		t.Errorf("алерт об отключении не отправляется, если отключить не удалось")
	}
	if h.store.called("SetRestoreTimer") != 0 || h.store.called("RemoveRestoreTimer") != 0 {
		t.Errorf("таймер не должен меняться, если подписка не отключена")
	}
}

func TestHardViolation_AutoModeDisableFailureOnShutdownIsSilent(t *testing.T) {
	h := newHarness(t, autoMode)
	h.cacheUser(1, 2)
	h.panel.failOn("DisableUser", context.Canceled)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.m.checkUser(ctx, 1, activeIPs(violatingIPs...), &checkStats{})
	h.m.webhookWG.Wait()

	if len(h.bot.messages) != 0 || h.bot.alerts() != 0 {
		t.Errorf("при остановке сообщение о сбое не отправляется: %q", h.bot.messages)
	}
}

func TestHardViolation_AutoModeRestoreTimerFailureShowsPermanent(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		autoMode(c)
		c.AutoDisableDuration = 45
	})
	h.cacheUser(1, 2)
	h.store.failOn("SetRestoreTimer", errors.New("redis down"))

	h.checkUser(1, violatingIPs...)

	if len(h.panel.disabled) != 1 {
		t.Fatalf("подписка должна быть отключена: %v", h.panel.disabled)
	}
	if len(h.bot.auto) != 1 {
		t.Fatalf("ожидался авто-алерт, получено %d", len(h.bot.auto))
	}
	text := h.bot.auto[0].text
	if !strings.Contains(text, i18n.T("alert.permanent")) || strings.Contains(text, i18n.T("alert.restores_at")) {
		t.Errorf("без таймера восстановления алерт должен показывать перманентное отключение: %s", text)
	}
}

func TestExtendCooldown(t *testing.T) {
	cases := []struct {
		name     string
		cooldown int
		window   int
		interval int
		want     time.Duration
	}{
		{"кулдаун уже перекрывает окно активности", 300, 60, 30, 0},
		{"кулдаун равен окну плюс интервалу", 90, 60, 30, 0},
		{"короткий кулдаун продлевается до окна плюс интервал", 30, 120, 30, 150 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, nil)
			cfg := &config.Config{Cooldown: c.cooldown, ActiveIPWindow: c.window, CheckInterval: c.interval}

			h.m.extendCooldown(context.Background(), cfg, 1)

			if got := h.store.cooldown[1]; got != c.want {
				t.Errorf("кулдаун = %v, ожидалось %v", got, c.want)
			}
		})
	}
}

func TestHardViolation_AutoModeExtendsShortCooldown(t *testing.T) {
	short := func(c *config.Config) {
		autoMode(c)
		c.Cooldown = 30
		c.ActiveIPWindow = 120
		c.CheckInterval = 30
	}

	t.Run("после отключения", func(t *testing.T) {
		h := newHarness(t, short)
		h.cacheUser(1, 2)
		h.checkUser(1, violatingIPs...)
		if got := h.store.cooldown[1]; got != 150*time.Second {
			t.Errorf("кулдаун = %v, ожидалось 150s", got)
		}
	})

	t.Run("если подписка уже отключена", func(t *testing.T) {
		h := newHarness(t, short)
		h.cacheUser(1, 2)
		h.panel.failOn("DisableUser", alreadyDisabledErr())
		h.checkUser(1, violatingIPs...)
		if got := h.store.cooldown[1]; got != 150*time.Second {
			t.Errorf("кулдаун = %v, ожидалось 150s", got)
		}
	})

	t.Run("если отключить не удалось — кулдаун обычный", func(t *testing.T) {
		h := newHarness(t, short)
		h.cacheUser(1, 2)
		h.panel.failOn("DisableUser", errors.New("panel 500"))
		h.checkUser(1, violatingIPs...)
		if got := h.store.cooldown[1]; got != 30*time.Second {
			t.Errorf("кулдаун = %v, ожидалось 30s", got)
		}
	})
}

func softMode(c *config.Config) {
	autoMode(c)
	c.AutoNotifySoft = true
	c.Tolerance = 2
	c.IgnoreDuration = 90
	c.Cooldown = 600
}

func TestSoftWarning_AutoModeWithinTolerance(t *testing.T) {
	h := newHarness(t, softMode)
	h.cacheUser(1, 2)

	st := h.checkUser(1, violatingIPs...)

	if len(h.bot.soft) != 1 || h.bot.alerts() != 1 {
		t.Fatalf("ожидался один мягкий алерт: мягких %d, всего %d", len(h.bot.soft), h.bot.alerts())
	}
	if alert := h.bot.soft[0]; alert.userID != 1 || alert.ignoreDuration != 90 {
		t.Errorf("параметры мягкого алерта = %+v, ожидались userID=1, ignore=90", alert)
	}
	if len(h.panel.disabled) != 0 || h.store.called("SetRestoreTimer") != 0 {
		t.Errorf("в пределах допуска подписка не отключается")
	}
	if got := h.store.softCooldown[1]; got != 600*time.Second {
		t.Errorf("мягкий кулдаун = %v, ожидалось COOLDOWN = 10m", got)
	}
	if _, hard := h.store.cooldown[1]; hard || h.store.called("IsCooldownActive") != 0 {
		t.Errorf("мягкий алерт не должен трогать основной кулдаун")
	}
	for _, method := range []string{"IncrViolationCount", "IncrThresholdCount", "RecordViolation"} {
		if n := h.store.called(method); n != 0 {
			t.Errorf("мягкий алерт не учитывается в статистике и порогах: %s вызван %d раз", method, n)
		}
	}
	if st.soft.Load() != 1 || st.violations.Load() != 0 {
		t.Errorf("soft/violations = %d/%d, ожидалось 1/0", st.soft.Load(), st.violations.Load())
	}

	payloads := h.webhook.sent()
	if len(payloads) != 1 || payloads[0].Event != "soft_violation_detected" {
		t.Fatalf("ожидался webhook soft_violation_detected: %+v", payloads)
	}
	v := payloads[0].Violation
	if v.ViolationCount24h != 0 || v.Tolerance != 2 || v.EffectiveLimit != 4 {
		t.Errorf("webhook: count=%d tolerance=%d effective=%d, ожидалось 0/2/4", v.ViolationCount24h, v.Tolerance, v.EffectiveLimit)
	}
}

func TestSoftWarning_SeparateCooldown(t *testing.T) {
	t.Run("активный мягкий кулдаун гасит алерт", func(t *testing.T) {
		h := newHarness(t, softMode)
		h.cacheUser(1, 2)
		h.store.softCooldown[1] = time.Minute

		st := h.checkUser(1, violatingIPs...)

		if h.bot.alerts() != 0 || st.soft.Load() != 0 || len(h.webhook.sent()) != 0 {
			t.Errorf("при активном мягком кулдауне алерта быть не должно")
		}
	})

	t.Run("основной кулдаун мягкому алерту не мешает", func(t *testing.T) {
		h := newHarness(t, softMode)
		h.cacheUser(1, 2)
		h.store.cooldown[1] = time.Minute

		h.checkUser(1, violatingIPs...)

		if len(h.bot.soft) != 1 {
			t.Errorf("мягкий алерт использует отдельный кулдаун, алертов: %d", len(h.bot.soft))
		}
	})

	t.Run("мягкий кулдаун не мешает бану", func(t *testing.T) {
		h := newHarness(t, softMode)
		h.cacheUser(1, 2)
		h.store.softCooldown[1] = time.Minute

		h.checkUser(1, "10.0.1.1", "10.0.2.1", "10.0.3.1", "10.0.4.1", "10.0.5.1")

		if len(h.bot.auto) != 1 || len(h.panel.disabled) != 1 {
			t.Errorf("превышение выше порога должно отключать подписку: авто %d, отключено %v", len(h.bot.auto), h.panel.disabled)
		}
	})

	t.Run("сбой проверки мягкого кулдауна — алерта нет", func(t *testing.T) {
		h := newHarness(t, softMode)
		h.cacheUser(1, 2)
		h.store.failOn("IsSoftCooldownActive", errors.New("redis down"))

		h.checkUser(1, violatingIPs...)

		if h.bot.alerts() != 0 {
			t.Errorf("при сбое проверки кулдауна алерта быть не должно")
		}
	})
}

func TestViolation_UsesOneConfigSnapshot(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.SubnetGrouping = true
		c.AutoDisableDuration = 45
		c.IgnoreDuration = 90
	})
	h.cacheUser(1, 2)

	h.store.hook = func(method string) {
		if method != "IsCooldownActive" {
			return
		}
		changed := *h.provider.Load()
		changed.ActionMode = "auto"
		changed.SubnetGrouping = false
		changed.ASNGrouping = true
		changed.AutoDisableDuration = 5
		changed.IgnoreDuration = 7
		changed.Tolerance = 10
		h.provider.Store(&changed)
	}

	h.checkUser(1, "10.0.1.1", "10.0.1.2", "10.0.2.1", "10.0.3.1")

	if len(h.panel.disabled) != 0 || len(h.bot.auto) != 0 {
		t.Fatalf("решение принято в ручном режиме — смена настроек посреди проверки не должна отключать подписку")
	}
	if len(h.bot.manual) != 1 {
		t.Fatalf("ожидался ручной алерт, получено %d", len(h.bot.manual))
	}
	if alert := h.bot.manual[0]; alert.disableDuration != 45 || alert.ignoreDuration != 90 {
		t.Errorf("кнопки собраны по новым настройкам: %+v, ожидалось disable=45, ignore=90", alert)
	}
	if !strings.Contains(h.bot.manual[0].text, i18n.T("alert.subnets")) || strings.Contains(h.bot.manual[0].text, i18n.T("alert.asn_groups")) {
		t.Errorf("алерт должен показывать группировку, по которой принято решение (подсети): %s", h.bot.manual[0].text)
	}

	payloads := h.webhook.sent()
	if len(payloads) != 1 {
		t.Fatalf("webhook отправлен %d раз, ожидался 1", len(payloads))
	}
	p := payloads[0]
	if p.ActionMode != "manual" || p.Violation.GroupingMode != "subnet" || p.Violation.DeviceGroupCount != 3 ||
		p.Violation.Tolerance != 0 || p.Action.AutoDisableDurationMin != 45 {
		t.Errorf("webhook собран по новым настройкам: mode=%s grouping=%s groups=%d tolerance=%d duration=%d",
			p.ActionMode, p.Violation.GroupingMode, p.Violation.DeviceGroupCount, p.Violation.Tolerance, p.Action.AutoDisableDurationMin)
	}
}
