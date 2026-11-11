package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/i18n"
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

const actionStoreTimeout = 10 * time.Second

func newActionHandler(panel userActions, store actionStore, cfg *config.Provider, logger *logrus.Logger) telegram.ActionHandler {
	return func(ctx context.Context, a telegram.Action) error {
		switch a.Name {
		case "drop":
			return panel.DropConnections(ctx, []int64{a.UserID})

		case "disable":
			if err := panel.DisableUser(ctx, a.UserID); err != nil && !api.IsAlreadyDisabled(err) {
				return err
			}
			return store.RemoveRestoreTimer(ctx, a.UserID)

		case "disable_temp":
			minutes := buttonMinutes(a, cfg.Load().AutoDisableDuration)
			if minutes <= 0 {
				return fmt.Errorf(i18n.T("error.action.disable_duration"), telegram.ErrStaleAction)
			}
			disabledNow := true
			if err := panel.DisableUser(ctx, a.UserID); err != nil {
				if !api.IsAlreadyDisabled(err) {
					return err
				}
				disabledNow = false
			}
			storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), actionStoreTimeout)
			defer cancel()
			if err := store.SetRestoreTimer(storeCtx, a.UserID, time.Duration(minutes)*time.Minute); err != nil {
				logger.WithError(err).WithField("userID", a.UserID).Error(i18n.T("log.restore_timer_failed"))
				if disabledNow && panel.EnableUser(storeCtx, a.UserID) == nil {
					return fmt.Errorf(i18n.T("error.action.timer_rolled_back"), err)
				}
				return fmt.Errorf(i18n.T("error.action.timer_missing"), err)
			}
			return nil

		case "enable":
			if err := panel.EnableUser(ctx, a.UserID); err != nil && !api.IsAlreadyEnabled(err) {
				return err
			}
			return store.RemoveRestoreTimer(ctx, a.UserID)

		case "ignore":
			return store.AddToWhitelist(ctx, a.UserID)

		case "ignore_temp":
			minutes := buttonMinutes(a, cfg.Load().IgnoreDuration)
			if minutes <= 0 {
				return fmt.Errorf(i18n.T("error.action.ignore_duration"), telegram.ErrStaleAction)
			}
			return store.AddToWhitelistTemp(ctx, a.UserID, time.Duration(minutes)*time.Minute)
		}
		return fmt.Errorf(i18n.T("error.action.unknown"), a.Name, telegram.ErrStaleAction)
	}
}

func buttonMinutes(a telegram.Action, current int) int {
	if a.Minutes > 0 {
		return a.Minutes
	}
	return current
}
