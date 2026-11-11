package monitor

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/remnawave/limiter/internal/i18n"
)

func TestRestoreUser(t *testing.T) {
	const member = "42"
	boom := errors.New("panel 500")

	type want struct {
		enableCalls int
		completed   bool
		rescheduled []rescheduleCall
		message     string
		attempts    int64
	}

	cases := []struct {
		name      string
		member    string
		cancelled bool
		prep      func(h *harness)
		want      want
	}{
		{
			name:   "успех завершает аренду и сообщает админу",
			member: member,
			prep:   func(h *harness) { h.store.attempts[42] = 1 },
			want:   want{enableCalls: 1, completed: true, message: "restore.message"},
		},
		{
			name:   "«уже включена» считается успехом",
			member: member,
			prep:   func(h *harness) { h.panel.failOn("EnableUser", alreadyEnabledErr()) },
			want:   want{enableCalls: 1, completed: true, message: "restore.message"},
		},
		{
			name:   "аренда потеряна — пользователь пропускается",
			member: member,
			prep:   func(h *harness) { h.store.leaseLost[member] = true },
			want:   want{},
		},
		{
			name:   "аренду не удалось проверить — пользователь пропускается",
			member: member,
			prep:   func(h *harness) { h.store.failOn("RestoreLeaseHeld", boom) },
			want:   want{},
		},
		{
			name:   "сбой включения возвращает запись в очередь",
			member: member,
			prep:   func(h *harness) { h.panel.failOn("EnableUser", boom) },
			want: want{
				enableCalls: 1,
				rescheduled: []rescheduleCall{{member: member, lease: "1800000000.5", delay: restoreRetryDelay}},
				attempts:    1,
			},
		},
		{
			name:   "сбой счётчика попыток считается первой попыткой",
			member: member,
			prep: func(h *harness) {
				h.panel.failOn("EnableUser", boom)
				h.store.failOn("IncrRestoreAttempts", boom)
			},
			want: want{
				enableCalls: 1,
				rescheduled: []rescheduleCall{{member: member, lease: "1800000000.5", delay: restoreRetryDelay}},
			},
		},
		{
			name:   "попытки исчерпаны — аренда завершается, админ уведомлён",
			member: member,
			prep: func(h *harness) {
				h.panel.failOn("EnableUser", boom)
				h.store.attempts[42] = maxRestoreAttempts - 1
			},
			want: want{enableCalls: 1, completed: true, message: "restore.failed"},
		},
		{
			name:      "отмена контекста освобождает аренду без расхода попытки",
			member:    member,
			cancelled: true,
			prep:      func(h *harness) {},
			want: want{
				enableCalls: 1,
				rescheduled: []rescheduleCall{{member: member, lease: "1800000000.5", delay: 0}},
			},
		},
		{
			name:   "запись старого формата убирается из очереди",
			member: "3f2c8a1e-uuid",
			prep:   func(h *harness) {},
			want:   want{completed: true},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, nil)
			c.prep(h)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.cancelled {
				cancel()
			}

			h.m.restoreUser(ctx, c.member, h.store.lease)

			if n := h.panel.called("EnableUser"); n != c.want.enableCalls {
				t.Errorf("EnableUser вызван %d раз, ожидалось %d", n, c.want.enableCalls)
			}
			if completed := len(h.store.completed) == 1 && h.store.completed[0] == c.member; completed != c.want.completed {
				t.Errorf("аренда завершена = %v (%v), ожидалось %v", completed, h.store.completed, c.want.completed)
			}
			if len(h.store.rescheduled) != len(c.want.rescheduled) {
				t.Fatalf("возвратов в очередь: %+v, ожидалось %+v", h.store.rescheduled, c.want.rescheduled)
			}
			for i, r := range c.want.rescheduled {
				if h.store.rescheduled[i] != r {
					t.Errorf("возврат в очередь = %+v, ожидалось %+v", h.store.rescheduled[i], r)
				}
			}
			if c.want.message == "" {
				if len(h.bot.messages) != 0 {
					t.Errorf("сообщений админу быть не должно: %q", h.bot.messages)
				}
			} else {
				text := fmt.Sprintf(i18n.T(c.want.message), int64(42))
				if len(h.bot.messages) != 1 || h.bot.messages[0] != text {
					t.Errorf("сообщение админу = %q, ожидалось %q", h.bot.messages, text)
				}
			}
			if got := h.store.attempts[42]; got != c.want.attempts {
				t.Errorf("счётчик попыток = %d, ожидалось %d", got, c.want.attempts)
			}
		})
	}
}

func TestRestoreUser_RetriesUntilAttemptsExhausted(t *testing.T) {
	h := newHarness(t, nil)
	h.panel.failOn("EnableUser", errors.New("panel 500"))

	for attempt := 1; attempt < maxRestoreAttempts; attempt++ {
		h.m.restoreUser(context.Background(), "42", h.store.lease)
		if len(h.store.rescheduled) != attempt || len(h.store.completed) != 0 || len(h.bot.messages) != 0 {
			t.Fatalf("попытка %d: запись должна вернуться в очередь без уведомлений", attempt)
		}
	}

	h.m.restoreUser(context.Background(), "42", h.store.lease)

	if len(h.store.rescheduled) != maxRestoreAttempts-1 || len(h.store.completed) != 1 {
		t.Errorf("после %d попыток запись убирается из очереди: возвратов %d, завершений %d",
			maxRestoreAttempts, len(h.store.rescheduled), len(h.store.completed))
	}
	if len(h.bot.messages) != 1 || h.bot.messages[0] != fmt.Sprintf(i18n.T("restore.failed"), int64(42)) {
		t.Errorf("админ должен узнать, что подписку нужно включить вручную: %q", h.bot.messages)
	}
	if _, left := h.store.attempts[42]; left {
		t.Error("счётчик попыток должен сбрасываться после исчерпания")
	}
}

func TestRestoreExpired(t *testing.T) {
	t.Run("включает всех, у кого истёк таймер", func(t *testing.T) {
		h := newHarness(t, nil)
		h.store.claimed = []string{"1", "2", "3"}

		h.m.restoreExpired(context.Background())

		if len(h.panel.enabled) != 3 || len(h.store.completed) != 3 || len(h.bot.messages) != 3 {
			t.Errorf("включено %v, завершено %v, сообщений %d — ожидалось по 3", h.panel.enabled, h.store.completed, len(h.bot.messages))
		}
	})

	t.Run("при остановке аренда освобождается без обращения к панели", func(t *testing.T) {
		h := newHarness(t, nil)
		h.store.claimed = []string{"1", "2"}

		ctx, cancel := context.WithCancel(context.Background())
		h.store.hook = func(method string) {
			if method == "ClaimExpiredRestoreTimers" {
				cancel()
			}
		}
		h.m.restoreExpired(ctx)

		if n := h.panel.called("EnableUser"); n != 0 {
			t.Errorf("после отмены контекста панель не вызывается, вызовов: %d", n)
		}
		if len(h.store.rescheduled) != 2 {
			t.Fatalf("обе записи должны вернуться в очередь: %+v", h.store.rescheduled)
		}
		for _, r := range h.store.rescheduled {
			if r.delay != 0 || r.lease != h.store.lease {
				t.Errorf("запись должна вернуться без задержки и со своей арендой: %+v", r)
			}
		}
	})

	t.Run("сбой чтения очереди ничего не трогает", func(t *testing.T) {
		h := newHarness(t, nil)
		h.store.claimed = []string{"1"}
		h.store.failOn("ClaimExpiredRestoreTimers", errors.New("redis down"))

		h.m.restoreExpired(context.Background())

		if h.panel.called("EnableUser") != 0 || len(h.bot.messages) != 0 {
			t.Error("при сбое очереди никого включать нельзя")
		}
	})
}
