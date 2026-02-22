package limiter

import (
	"bufio"
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var banPattern = regexp.MustCompile(`(BAN|UNBAN).*\[IP\]\s+=\s+(\S+)`)

func (l *Limiter) watchBannedLog(ctx context.Context) {
	bannedLogPath := "/var/log/remnawave-limiter/banned.log"

	l.logger.Info("📡 Запущен webhook watcher для banned.log")

	var lastSize int64 = 0
	if fileInfo, err := os.Stat(bannedLogPath); err == nil {
		lastSize = fileInfo.Size()
		l.logger.WithField("initial_size", lastSize).Debug("Пропускаем существующие записи в banned.log")
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.logger.Info("📡 Webhook watcher остановлен")
			return
		case <-ticker.C:
		}

		fileInfo, err := os.Stat(bannedLogPath)
		if err != nil {
			if !os.IsNotExist(err) {
				l.logger.WithError(err).Debug("Ошибка проверки banned.log")
			}
			continue
		}

		currentSize := fileInfo.Size()

		if currentSize < lastSize {
			lastSize = 0
		}

		if currentSize > lastSize {
			file, err := os.Open(bannedLogPath)
			if err != nil {
				l.logger.WithError(err).Error("Ошибка открытия banned.log")
				continue
			}

			if _, err := file.Seek(lastSize, 0); err != nil {
				l.logger.WithError(err).Error("Ошибка позиционирования в файле")
				file.Close()
				continue
			}

			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				line := scanner.Text()

				match := banPattern.FindStringSubmatch(line)
				if len(match) >= 3 {
					action := strings.ToLower(match[1])
					ip := match[2]

					subscriptionID := l.getSubscriptionIDByIP(ip)
					if subscriptionID == "" {
						subscriptionID = "unknown"
					}

					l.logger.WithFields(logrus.Fields{
						"subscription_id": subscriptionID,
						"ip":              ip,
						"action":          action,
					}).Info("📨 Обнаружен бан/разбан в fail2ban, отправляем webhook")

					l.sendWebhook(ctx, subscriptionID, ip, l.config.MaxIPsPerKey+1, action)
				}
			}

			if err := scanner.Err(); err != nil {
				l.logger.WithError(err).Error("Ошибка чтения banned.log")
				file.Close()
				continue
			}

			newPos, err := file.Seek(0, io.SeekCurrent)
			if err == nil {
				lastSize = newPos
			} else {
				lastSize = currentSize
			}
			file.Close()
		}
	}
}

func (l *Limiter) getSubscriptionIDByIP(ip string) string {
	l.violationCacheMu.RLock()
	for email, ips := range l.violationCache {
		if _, ok := ips[ip]; ok {
			l.violationCacheMu.RUnlock()
			return email
		}
	}
	l.violationCacheMu.RUnlock()

	file, err := os.Open(l.config.ViolationLogPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	var lastMatch string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ip) {
			continue
		}
		if match := l.violationPattern.FindStringSubmatch(line); len(match) >= 2 {
			if strings.Contains(line, "SRC = "+ip) {
				lastMatch = match[1]
			}
		}
	}

	if err := scanner.Err(); err != nil {
		l.logger.WithError(err).Warn("Ошибка чтения лога нарушений при поиске подписки")
	}

	return lastMatch
}
