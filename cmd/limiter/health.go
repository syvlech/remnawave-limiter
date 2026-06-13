package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/monitor"
	"github.com/remnawave/limiter/internal/version"
)

func startHealthServer(ctx context.Context, addr string, mon *monitor.Monitor, cfgProvider *config.Provider, logger *logrus.Logger) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		last := mon.LastSuccessfulCheck()

		interval := cfgProvider.Load().CheckInterval
		staleAfter := time.Duration(interval*3) * time.Second
		if staleAfter < 90*time.Second {
			staleAfter = 90 * time.Second
		}

		if last.IsZero() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "starting: no successful check yet")
			return
		}

		age := time.Since(last).Truncate(time.Second)
		if age > staleAfter {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "unhealthy: last check %s ago (> %s)\n", age, staleAfter)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok: last check %s ago, v%s\n", age, version.Version)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		logger.Infof("Health endpoint слушает %s/healthz", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithError(err).Error("Health endpoint остановлен с ошибкой")
		}
	}()
}
