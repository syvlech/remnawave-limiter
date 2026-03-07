package limiter

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/parser"
	"github.com/sirupsen/logrus"
)

type Limiter struct {
	config           *config.Config
	logger           *logrus.Logger
	violationLogger  *logrus.Logger
	parser           *parser.Parser
	violationCache   map[string]map[string]int64
	violationCacheMu sync.RWMutex
	lastClear        atomic.Int64
	webhookWg        sync.WaitGroup
	whitelistSet map[string]struct{}
	httpClient   *http.Client
}

func NewLimiter(cfg *config.Config, logger, violationLogger *logrus.Logger) *Limiter {
	whitelistSet := make(map[string]struct{}, len(cfg.WhitelistEmails))
	for _, email := range cfg.WhitelistEmails {
		whitelistSet[email] = struct{}{}
	}

	l := &Limiter{
		config:           cfg,
		logger:           logger,
		violationLogger:  violationLogger,
		parser:           parser.NewParser(),
		violationCache:   make(map[string]map[string]int64),
		whitelistSet: whitelistSet,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
	l.lastClear.Store(time.Now().Unix())
	return l
}

func (l *Limiter) Run() {
	l.logger.Info("🚀 Remnawave IP Limiter запущен")
	l.logger.Infof("📁 Файл лога Remnawave: %s", l.config.RemnawaveLogPath)
	l.logger.Infof("📁 Файл лога нарушений: %s", l.config.ViolationLogPath)
	if l.config.EnableLogArchive {
		l.logger.Infof("📁 Архив access лога: %s", l.config.AccessLogArchivePath)
	} else {
		l.logger.Info("📁 Архивирование access лога отключено")
	}
	l.logger.Infof("🔢 Максимум IP на ключ: %d", l.config.MaxIPsPerKey)
	l.logger.Infof("🔄 Интервал проверки: %dс", l.config.CheckInterval)
	l.logger.Infof("🗑️ Очистка лога каждые: %dс", l.config.LogClearInterval)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go l.watchBannedLog(ctx)

	ticker := time.NewTicker(time.Duration(l.config.CheckInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.logger.Info("Получен сигнал завершения, ожидание webhook...")
			l.webhookWg.Wait()
			l.logger.Info("👋 IP Limiter остановлен")
			return
		case <-ticker.C:
			l.processLogFile()
		}
	}
}

func (l *Limiter) processLogFile() {
	l.checkViolations()

	currentTime := time.Now().Unix()
	if currentTime-l.lastClear.Load() > int64(l.config.LogClearInterval) {
		l.clearAccessLog()
	}
}

func (l *Limiter) checkViolations() {
	file, err := os.Open(l.config.RemnawaveLogPath)
	if err != nil {
		if !os.IsNotExist(err) {
			l.logger.WithError(err).Error("Ошибка открытия лога")
		}
		return
	}
	defer file.Close()

	emailIPLastSeen := make(map[string]map[string]time.Time)
	emailIPFirstSeen := make(map[string]map[string]time.Time)
	var latestTimestamp time.Time

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		entry := l.parser.ParseLine(scanner.Text())
		if entry == nil {
			continue
		}

		if emailIPLastSeen[entry.Email] == nil {
			emailIPLastSeen[entry.Email] = make(map[string]time.Time)
			emailIPFirstSeen[entry.Email] = make(map[string]time.Time)
		}

		if _, exists := emailIPFirstSeen[entry.Email][entry.IP]; !exists {
			emailIPFirstSeen[entry.Email][entry.IP] = entry.Timestamp
		}
		emailIPLastSeen[entry.Email][entry.IP] = entry.Timestamp

		if entry.Timestamp.After(latestTimestamp) {
			latestTimestamp = entry.Timestamp
		}
	}

	if err := scanner.Err(); err != nil {
		l.logger.WithError(err).Error("Ошибка чтения лога")
		return
	}

	if latestTimestamp.IsZero() {
		return
	}

	for email, ipLastSeen := range emailIPLastSeen {
		if l.isWhitelisted(email) {
			continue
		}

		activeIPs := l.getActiveIPs(ipLastSeen, emailIPFirstSeen[email], latestTimestamp)

		if len(activeIPs) > l.config.MaxIPsPerKey {
			l.handleViolation(email, activeIPs)
		}
	}
}

type ipWithTime struct {
	ip        string
	firstSeen time.Time
}

func (l *Limiter) getActiveIPs(ipLastSeen, ipFirstSeen map[string]time.Time, latestTimestamp time.Time) []string {
	var active []ipWithTime

	for ip, lastSeen := range ipLastSeen {
		if latestTimestamp.Sub(lastSeen).Seconds() <= 60 {
			active = append(active, ipWithTime{ip: ip, firstSeen: ipFirstSeen[ip]})
		}
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].firstSeen.Before(active[j].firstSeen)
	})

	activeIPs := make([]string, len(active))
	for i, a := range active {
		activeIPs[i] = a.ip
	}
	return activeIPs
}

func (l *Limiter) handleViolation(email string, activeIPs []string) {
	disallowedIPs := activeIPs[l.config.MaxIPsPerKey:]

	for _, bannedIP := range disallowedIPs {
		now := time.Now().Unix()

		shouldLog := false
		l.violationCacheMu.Lock()
		if l.violationCache[email] == nil {
			l.violationCache[email] = make(map[string]int64)
		}
		lastLogged := l.violationCache[email][bannedIP]
		if now-lastLogged > 60 {
			l.violationCache[email][bannedIP] = now
			shouldLog = true
		}
		l.violationCacheMu.Unlock()

		if shouldLog {
			l.violationLogger.Infof("[LIMIT_IP] Email = %s || SRC = %s", email, bannedIP)

			l.logger.WithFields(logrus.Fields{
				"email":      email,
				"banned_ip":  bannedIP,
				"active_ips": len(activeIPs),
				"limit":      l.config.MaxIPsPerKey,
			}).Warnf("🚫 Нарушение: %s одновременно использует %d IP (лимит: %d), банится %s",
				email, len(activeIPs), l.config.MaxIPsPerKey, bannedIP)

			l.logger.Debugf("Активные IP для %s: %v", email, activeIPs)
		}
	}
}

func (l *Limiter) clearAccessLog() {
	if l.config.EnableLogArchive {
		if err := l.archiveAccessLog(); err != nil {
			l.logger.WithError(err).Error("Ошибка архивирования лога, очистка отменена")
			return
		}
	}

	file, err := os.OpenFile(l.config.RemnawaveLogPath, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		l.logger.WithError(err).Error("Ошибка при очистке лога")
		return
	}
	file.Close()

	l.evictStaleViolations()

	l.lastClear.Store(time.Now().Unix())
	if l.config.EnableLogArchive {
		l.logger.Info("🗑️ Лог Remnawave очищен, копия сохранена в архив")
	} else {
		l.logger.Info("🗑️ Лог Remnawave очищен")
	}
}

func (l *Limiter) archiveAccessLog() error {
	src, err := os.Open(l.config.RemnawaveLogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}

	dst, err := os.OpenFile(l.config.AccessLogArchivePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

func (l *Limiter) evictStaleViolations() {
	threshold := time.Now().Unix() - 300
	l.violationCacheMu.Lock()
	for email, ips := range l.violationCache {
		for ip, ts := range ips {
			if ts < threshold {
				delete(ips, ip)
			}
		}
		if len(ips) == 0 {
			delete(l.violationCache, email)
		}
	}
	l.violationCacheMu.Unlock()
}

func (l *Limiter) isWhitelisted(email string) bool {
	_, ok := l.whitelistSet[email]
	return ok
}
