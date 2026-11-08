package main

import (
	"github.com/remnawave/limiter/internal/i18n"
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/telegram"
)

type userActions interface {
	DisableUser(ctx context.Context, userID int64) error
	EnableUser(ctx context.Context, userID int64) error
	DropConnections(ctx context.Context, userIDs []int64) error
}

type actionStore interface {
	SetRestoreTimer(ctx context.Context, userID int64, duration time.Duration) error
	RemoveRestoreTimer(ctx context.Context, userID int64) error
	AddToWhitelist(ctx context.Context, userID int64) error
	AddToWhitelistTemp(ctx context.Context, userID int64, ttl time.Duration) error
}

func newActionHandler(panel userActions, store actionStore, cfg *config.Provider, logger *logrus.Logger) telegram.ActionHandler {
	return func(ctx context.Context, a telegram.Action) error {
		switch a.Name {
		case "drop":
			return panel.DropConnections(ctx, []int64{a.UserID})

		case "disable":
			if err := panel.DisableUser(ctx, a.UserID); err != nil {
				return err
			}
			return store.RemoveRestoreTimer(ctx, a.UserID)

		case "disable_temp":
			minutes := buttonMinutes(a, cfg.Load().AutoDisableDuration)
			if minutes <= 0 {
				return fmt.Errorf("длительность отключения неизвестна: %w", telegram.ErrStaleAction)
			}
			if err := panel.DisableUser(ctx, a.UserID); err != nil {
				return err
			}
			if err := store.SetRestoreTimer(ctx, a.UserID, time.Duration(minutes)*time.Minute); err != nil {
				logger.WithError(err).WithField("userID", a.UserID).Error(i18n.T("log.restore_timer_failed"))
				return err
			}
			return nil

		case "enable":
			if err := panel.EnableUser(ctx, a.UserID); err != nil {
				return err
			}
			return store.RemoveRestoreTimer(ctx, a.UserID)

		case "ignore":
			return store.AddToWhitelist(ctx, a.UserID)

		case "ignore_temp":
			minutes := buttonMinutes(a, cfg.Load().IgnoreDuration)
			if minutes <= 0 {
				return fmt.Errorf("длительность игнорирования неизвестна: %w", telegram.ErrStaleAction)
			}
			return store.AddToWhitelistTemp(ctx, a.UserID, time.Duration(minutes)*time.Minute)
		}
		return fmt.Errorf("неизвестное действие %q: %w", a.Name, telegram.ErrStaleAction)
	}
}

func buttonMinutes(a telegram.Action, current int) int {
	if a.Minutes > 0 {
		return a.Minutes
	}
	return current
}
