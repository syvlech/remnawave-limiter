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

var banPattern = regexp.MustCompile(`(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2})\s+(BAN|UNBAN)\s+\[Email\]\s+=\s+(\S+)\s+\[IP\]\s+=\s+(\S+)`)

const banEventMaxAge = 60 * time.Second

func (l *Limiter) watchBannedLog(ctx context.Context) {
	bannedLogPath := "/var/log/remnawave-limiter/banned.log"

	l.logger.Info("📡 Запущен мониторинг banned.log")

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
			l.logger.Info("📡 Мониторинг banned.log остановлен")
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
				if len(match) >= 5 {
					eventTime, err := time.Parse("2006/01/02 15:04:05", match[1])
					if err != nil {
						continue
					}

					action := strings.ToLower(match[2])
					email := match[3]
					ip := match[4]

					if action == "ban" {
						l.logger.WithFields(logrus.Fields{
							"email":    email,
							"ip":       ip,
							"duration": l.config.BanDurationMinutes,
						}).Warnf("🔒 Заблокирован: %s (подписка %s) на %d мин.", ip, email, l.config.BanDurationMinutes)
					} else {
						l.logger.WithFields(logrus.Fields{
							"email": email,
							"ip":    ip,
						}).Infof("🔓 Разблокирован: %s (подписка %s)", ip, email)
					}

					if l.config.WebhookURL != "" {
						if time.Since(eventTime) > banEventMaxAge {
							l.logger.WithFields(logrus.Fields{
								"email":  email,
								"ip":     ip,
								"action": action,
								"age":    time.Since(eventTime).Round(time.Second).String(),
							}).Debug("Пропуск webhook для старого события")
							continue
						}
						l.sendWebhook(ctx, email, ip, l.config.MaxIPsPerKey+1, action)
					}
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
