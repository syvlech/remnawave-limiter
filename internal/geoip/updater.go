package geoip

import (
	"context"
	"github.com/remnawave/limiter/internal/i18n"
	"time"

	"github.com/sirupsen/logrus"
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
	ticker := time.NewTicker(u.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.tick(ctx)
		}
	}
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
