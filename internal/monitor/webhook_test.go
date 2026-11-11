package monitor

import (
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
)

func TestWebhookSendTimeout_CoversEveryAttempt(t *testing.T) {
	const (
		attempts       = 3
		attemptTimeout = 10 * time.Second
		firstPause     = 2 * time.Second
		secondPause    = 4 * time.Second
	)
	schedule := attempts*attemptTimeout + firstPause + secondPause

	if webhookSendTimeout <= schedule {
		t.Errorf("общий срок отправки %v не вмещает %d попытки по %v с паузами %v и %v (%v)",
			webhookSendTimeout, attempts, attemptTimeout, firstPause, secondPause, schedule)
	}
	if webhookSendTimeout > schedule+10*time.Second {
		t.Errorf("запас общего срока отправки слишком велик: %v при расписании %v", webhookSendTimeout, schedule)
	}
	if webhookGracePeriod > 15*time.Second {
		t.Errorf("ожидание webhook при остановке должно оставаться в пределах 15s, сейчас %v", webhookGracePeriod)
	}
}

func TestSendWebhook_DeadlineDoesNotDependOnCheckContext(t *testing.T) {
	h := newHarness(t, nil)
	user := &api.CachedUser{UserID: 1, Username: "u"}

	h.m.sendWebhook(h.provider.Load(), "violation_detected", user, activeIPs("10.0.0.1", "10.0.0.2"), 1, 1, 0, 0)
	h.m.webhookWG.Wait()

	if len(h.webhook.deadlines) != 1 {
		t.Fatalf("у отправки webhook должен быть общий срок, получено %v", h.webhook.deadlines)
	}
	left := h.webhook.deadlines[0]
	if left > webhookSendTimeout || left < webhookSendTimeout-5*time.Second {
		t.Errorf("срок отправки webhook = %v, ожидался около %v", left, webhookSendTimeout)
	}
}

func TestWaitWebhooks_ReturnsWhenDeliveriesFinish(t *testing.T) {
	h := newHarness(t, nil)
	user := &api.CachedUser{UserID: 1, Username: "u"}
	h.m.sendWebhook(h.provider.Load(), "violation_detected", user, activeIPs("10.0.0.1"), 1, 1, 0, 0)

	started := time.Now()
	h.m.waitWebhooks(5 * time.Second)

	if took := time.Since(started); took > time.Second {
		t.Errorf("ожидание завершённых отправок заняло %v", took)
	}
	if len(h.webhook.sent()) != 1 || h.webhook.cancelled != 0 {
		t.Errorf("отправка должна завершиться сама: отправлено %d, отменено %d", len(h.webhook.sent()), h.webhook.cancelled)
	}
}

func TestWaitWebhooks_CancelsHungDeliveriesAfterGrace(t *testing.T) {
	h := newHarness(t, nil)
	h.webhook.hang = true
	user := &api.CachedUser{UserID: 1, Username: "u"}
	for i := 0; i < 3; i++ {
		h.m.sendWebhook(h.provider.Load(), "violation_detected", user, activeIPs("10.0.0.1"), 1, 1, 0, 0)
	}

	const grace = 100 * time.Millisecond
	started := time.Now()
	h.m.waitWebhooks(grace)
	took := time.Since(started)

	if took < grace {
		t.Errorf("зависшим отправкам должно даваться время %v, ожидание заняло %v", grace, took)
	}
	if took > 2*time.Second {
		t.Errorf("остановка не должна ждать общий срок отправки %v, ожидание заняло %v", webhookSendTimeout, took)
	}
	if h.webhook.cancelled != 3 {
		t.Errorf("по истечении времени зависшие отправки отменяются, отменено %d из 3", h.webhook.cancelled)
	}
}
