package geoip

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

type recordingDownloader struct {
	calls        atomic.Int32
	errOnNthCall int32
	errValue     error
}

func (r *recordingDownloader) Download(ctx context.Context, dst string) error {
	n := r.calls.Add(1)
	if r.errOnNthCall > 0 && n == r.errOnNthCall {
		return r.errValue
	}
	return nil
}

type recordingReloader struct {
	calls atomic.Int32
	err   error
}

func (r *recordingReloader) Reload(path string) error {
	r.calls.Add(1)
	return r.err
}

func runUpdaterUntil(t *testing.T, u *Updater, reached func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		u.Run(ctx)
		close(done)
	}()

	deadline := time.After(5 * time.Second)
	for !reached() {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("Updater.Run не дошёл до ожидаемого числа обновлений за 5 секунд")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Updater.Run did not return after context cancel")
	}
}

func testLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

func TestUpdater_RunsOnTickThenStopsOnContextCancel(t *testing.T) {
	d := &recordingDownloader{}
	rel := &recordingReloader{}
	u := &Updater{
		Downloader: d,
		Reloader:   rel,
		DstPath:    filepath.Join(t.TempDir(), "fake.mmdb"),
		Interval:   10 * time.Millisecond,
		Logger:     testLogger(),
	}

	runUpdaterUntil(t, u, func() bool { return rel.calls.Load() >= 2 })

	downloads, reloads := d.calls.Load(), rel.calls.Load()
	if downloads < 2 {
		t.Errorf("Downloader.Download called %d times, want at least 2", downloads)
	}
	if reloads != downloads {
		t.Errorf("Reloader.Reload called %d times, Download called %d times; want equal", reloads, downloads)
	}
}

func TestUpdater_DownloadErrorDoesNotStopLoop(t *testing.T) {
	d := &recordingDownloader{errOnNthCall: 1, errValue: errors.New("network down")}
	rel := &recordingReloader{}
	u := &Updater{
		Downloader: d,
		Reloader:   rel,
		DstPath:    filepath.Join(t.TempDir(), "fake.mmdb"),
		Interval:   10 * time.Millisecond,
		Logger:     testLogger(),
	}

	runUpdaterUntil(t, u, func() bool { return rel.calls.Load() >= 1 })

	downloads, reloads := d.calls.Load(), rel.calls.Load()
	if downloads < 2 {
		t.Errorf("Download was called %d times; expected >=2 even after an error", downloads)
	}
	if reloads != downloads-1 {
		t.Errorf("Reload called %d times for %d downloads; want exactly one fewer (the failed download must not reload)", reloads, downloads)
	}
}

func TestUpdater_ReloadErrorDoesNotStopLoop(t *testing.T) {
	d := &recordingDownloader{}
	rel := &recordingReloader{err: errors.New("bad database")}
	u := &Updater{
		Downloader: d,
		Reloader:   rel,
		DstPath:    filepath.Join(t.TempDir(), "fake.mmdb"),
		Interval:   10 * time.Millisecond,
		Logger:     testLogger(),
	}

	runUpdaterUntil(t, u, func() bool { return rel.calls.Load() >= 2 })

	if downloads, reloads := d.calls.Load(), rel.calls.Load(); reloads != downloads {
		t.Errorf("Reload called %d times, Download %d; ошибка Reload не должна останавливать цикл", reloads, downloads)
	}
}

func TestDBResolver_BuildTimeSafeWithoutDatabase(t *testing.T) {
	var nilResolver *DBResolver
	if !nilResolver.BuildTime().IsZero() {
		t.Error("у отсутствующей базы даты сборки нет")
	}
	closed := &DBResolver{}
	if !closed.BuildTime().IsZero() {
		t.Error("у закрытой базы даты сборки нет")
	}
}

func TestUpdater_FirstDelayFollowsFileAge(t *testing.T) {
	path := t.TempDir() + "/asn.mmdb"
	u := &Updater{DstPath: path, Interval: time.Hour}
	if got := u.firstDelay(); got != time.Hour {
		t.Errorf("без файла первый запуск через полный интервал, получено %v", got)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if got := u.firstDelay(); got != 0 {
		t.Errorf("устаревшая база обновляется сразу, получено %v", got)
	}
	recent := time.Now().Add(-20 * time.Minute)
	if err := os.Chtimes(path, recent, recent); err != nil {
		t.Fatal(err)
	}
	if got := u.firstDelay(); got < 39*time.Minute || got > 40*time.Minute {
		t.Errorf("свежая база ждёт остаток интервала (~40 мин), получено %v", got)
	}
}
