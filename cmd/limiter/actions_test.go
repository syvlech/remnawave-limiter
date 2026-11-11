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

	"github.com/remnawave/limiter/internal/api"
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

type alreadyBackend struct {
	fakeBackend
}

func (f *alreadyBackend) DisableUser(ctx context.Context, id int64) error {
	_ = f.fakeBackend.DisableUser(ctx, id)
	return &api.StatusError{Method: "POST", Path: "/disable", Code: 400, Body: `{"errorCode":"A029"}`}
}

func (f *alreadyBackend) EnableUser(ctx context.Context, id int64) error {
	_ = f.fakeBackend.EnableUser(ctx, id)
	return &api.StatusError{Method: "POST", Path: "/enable", Code: 400, Body: `{"errorCode":"A030"}`}
}

func TestActionHandler_AlreadyInTargetStateStillUpdatesTimer(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	cases := []struct {
		a    telegram.Action
		want string
	}{
		{telegram.Action{Name: "disable", UserID: 7}, "disable 7|unrestore 7"},
		{telegram.Action{Name: "disable_temp", UserID: 7, Minutes: 30}, "disable 7|restore 7 in 30m0s"},
		{telegram.Action{Name: "enable", UserID: 7}, "enable 7|unrestore 7"},
	}
	for _, tc := range cases {
		backend := &alreadyBackend{}
		handler := newActionHandler(backend, backend, config.NewProvider(&config.Config{}), logger)
		if err := handler(context.Background(), tc.a); err != nil {
			t.Errorf("%s: ответ панели «уже в этом состоянии» не ошибка: %v", tc.a.Name, err)
		}
		if got := strings.Join(backend.calls, "|"); got != tc.want {
			t.Errorf("%s: вызовы %q, ожидалось %q", tc.a.Name, got, tc.want)
		}
	}
}

type timerFailBackend struct {
	fakeBackend
	enableErr error
}

func (f *timerFailBackend) SetRestoreTimer(ctx context.Context, id int64, d time.Duration) error {
	_ = f.fakeBackend.SetRestoreTimer(ctx, id, d)
	return errors.New("redis down")
}

func (f *timerFailBackend) EnableUser(ctx context.Context, id int64) error {
	_ = f.fakeBackend.EnableUser(ctx, id)
	return f.enableErr
}

func TestActionHandler_DisableTempRollsBackWithoutTimer(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	action := telegram.Action{Name: "disable_temp", UserID: 7, Minutes: 30}

	backend := &timerFailBackend{}
	handler := newActionHandler(backend, backend, config.NewProvider(&config.Config{}), logger)
	err := handler(context.Background(), action)
	if err == nil {
		t.Fatal("сбой записи таймера должен вернуться ошибкой")
	}
	if got := strings.Join(backend.calls, "|"); got != "disable 7|restore 7 in 30m0s|enable 7" {
		t.Errorf("без таймера отключение нужно отменить, вызовы %q", got)
	}

	stuck := &timerFailBackend{enableErr: errors.New("panel down")}
	handler = newActionHandler(stuck, stuck, config.NewProvider(&config.Config{}), logger)
	rolledBack, missing := err.Error(), ""
	if err := handler(context.Background(), action); err != nil {
		missing = err.Error()
	}
	if missing == "" || missing == rolledBack {
		t.Errorf("если отменить не удалось, текст ошибки должен это отличать: %q и %q", rolledBack, missing)
	}
}
