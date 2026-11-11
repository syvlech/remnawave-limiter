package geoip

import (
	"context"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/i18n"
)

type downloader interface {
	Download(ctx context.Context, dstPath string) error
}

type reloader interface {
	Reload(path string) error
}

type Updater struct {
	Downloader downloader
	Reloader   reloader
	DstPath    string
	Interval   time.Duration
	Logger     *logrus.Logger
}

func (u *Updater) Run(ctx context.Context) {
	timer := time.NewTimer(u.firstDelay())
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			u.tick(ctx)
			timer.Reset(u.Interval)
		}
	}
}

func (u *Updater) firstDelay() time.Duration {
	info, err := os.Stat(u.DstPath)
	if err != nil {
		return u.Interval
	}
	age := time.Since(info.ModTime())
	if age < 0 {
		return u.Interval
	}
	if age >= u.Interval {
		return 0
	}
	return u.Interval - age
}

func (u *Updater) tick(ctx context.Context) {
	if err := u.Downloader.Download(ctx, u.DstPath); err != nil {
		u.Logger.WithError(err).Warn(i18n.T("log.maxmind_download_failed"))
		return
	}
	if err := u.Reloader.Reload(u.DstPath); err != nil {
		u.Logger.WithError(err).Warn(i18n.T("log.maxmind_reload_failed"))
		return
	}
	u.Logger.Info(i18n.T("log.maxmind_updated"))
}
