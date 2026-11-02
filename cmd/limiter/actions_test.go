package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/telegram"
)

type fakeBackend struct {
	calls []string
}

func (f *fakeBackend) DisableUser(_ context.Context, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("disable %d", id))
	return nil
}

func (f *fakeBackend) EnableUser(_ context.Context, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("enable %d", id))
	return nil
}

func (f *fakeBackend) DropConnections(_ context.Context, ids []int64) error {
	f.calls = append(f.calls, fmt.Sprintf("drop %v", ids))
	return nil
}

func (f *fakeBackend) SetRestoreTimer(_ context.Context, id int64, d time.Duration) error {
	f.calls = append(f.calls, fmt.Sprintf("restore %d in %v", id, d))
	return nil
}

func (f *fakeBackend) RemoveRestoreTimer(_ context.Context, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("unrestore %d", id))
	return nil
}

func (f *fakeBackend) AddToWhitelist(_ context.Context, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("whitelist %d", id))
	return nil
}

func (f *fakeBackend) AddToWhitelistTemp(_ context.Context, id int64, ttl time.Duration) error {
	f.calls = append(f.calls, fmt.Sprintf("whitelist %d for %v", id, ttl))
	return nil
}

func runAction(t *testing.T, cfg *config.Config, a telegram.Action) ([]string, error) {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	backend := &fakeBackend{}
	handler := newActionHandler(backend, backend, config.NewProvider(cfg), logger)
	err := handler(context.Background(), a)
	return backend.calls, err
}

func TestActionHandler(t *testing.T) {
	cfg := &config.Config{AutoDisableDuration: 0, IgnoreDuration: 0}

	cases := []struct {
		name  string
		a     telegram.Action
		want  string
		stale bool
	}{
		{"drop", telegram.Action{Name: "drop", UserID: 7}, "drop [7]", false},
		{"навсегда снимает старый таймер", telegram.Action{Name: "disable", UserID: 7}, "disable 7|unrestore 7", false},
		{"включение снимает таймер", telegram.Action{Name: "enable", UserID: 7}, "enable 7|unrestore 7", false},
		{"длительность из кнопки, а не из конфига", telegram.Action{Name: "disable_temp", UserID: 7, Minutes: 30}, "disable 7|restore 7 in 30m0s", false},
		{"игнор на время из кнопки", telegram.Action{Name: "ignore_temp", UserID: 7, Minutes: 60}, "whitelist 7 for 1h0m0s", false},
		{"игнор навсегда", telegram.Action{Name: "ignore", UserID: 7}, "whitelist 7", false},
		{"старый ignore_temp при нуле", telegram.Action{Name: "ignore_temp", UserID: 7}, "", true},
		{"старый disable_temp при нуле", telegram.Action{Name: "disable_temp", UserID: 7}, "", true},
		{"неизвестное действие", telegram.Action{Name: "explode", UserID: 7}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls, err := runAction(t, cfg, c.a)
			if got := strings.Join(calls, "|"); got != c.want {
				t.Errorf("вызовы %q, ожидалось %q", got, c.want)
			}
			if stale := errors.Is(err, telegram.ErrStaleAction); stale != c.stale {
				t.Errorf("err = %v, ожидалось stale = %v", err, c.stale)
			}
			if !c.stale && err != nil {
				t.Errorf("неожиданная ошибка: %v", err)
			}
		})
	}
}

func TestActionHandler_LegacyButtonsUseCurrentConfig(t *testing.T) {
	cfg := &config.Config{AutoDisableDuration: 15, IgnoreDuration: 120}

	calls, err := runAction(t, cfg, telegram.Action{Name: "disable_temp", UserID: 3})
	if err != nil || strings.Join(calls, "|") != "disable 3|restore 3 in 15m0s" {
		t.Errorf("disable_temp: %v, %v", calls, err)
	}
	calls, err = runAction(t, cfg, telegram.Action{Name: "ignore_temp", UserID: 3})
	if err != nil || strings.Join(calls, "|") != "whitelist 3 for 2h0m0s" {
		t.Errorf("ignore_temp: %v, %v", calls, err)
	}
}
