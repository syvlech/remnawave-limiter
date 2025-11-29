package limiter

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

func (l *Limiter) watchBannedLog() {
	bannedLogPath := "/var/log/remnawave-limiter/banned.log"

	banPattern := regexp.MustCompile(`(BAN|UNBAN).*\[IP\]\s+=\s+(\S+)`)

	l.logger.Info("📡 Запущен webhook watcher для banned.log")

	var lastSize int64 = 0
	if fileInfo, err := os.Stat(bannedLogPath); err == nil {
		lastSize = fileInfo.Size()
		l.logger.WithField("initial_size", lastSize).Debug("Пропускаем существующие записи в banned.log")
	}

	for l.running {
		time.Sleep(2 * time.Second)

		fileInfo, err := os.Stat(bannedLogPath)
		if err != nil {
			if !os.IsNotExist(err) {
				l.logger.WithError(err).Debug("Ошибка проверки banned.log")
			}
			continue
		}

		currentSize := fileInfo.Size()

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

					l.sendWebhook(subscriptionID, ip, l.config.MaxIPsPerKey+1, action)
				}
			}

			file, err := os.Open(bannedLogPath)
			if err != nil {
				l.logger.WithError(err).Error("Ошибка открытия banned.log")
				continue
			}
			defer file.Close()

		}

		if currentSize < lastSize {
			lastSize = 0
		}
	}

	l.logger.Info("📡 Webhook watcher остановлен")
}

func (l *Limiter) getSubscriptionIDByIP(ip string) string {
	file, err := os.Open(l.config.ViolationLogPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	violationPattern := regexp.MustCompile(`\[LIMIT_IP\]\s+Email\s+=\s+(\S+)\s+\|\|\s+SRC\s+=\s+` + regexp.QuoteMeta(ip))

	var lastMatch string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if match := violationPattern.FindStringSubmatch(line); len(match) >= 2 {
			lastMatch = match[1]
		}
	}

	return lastMatch
}
