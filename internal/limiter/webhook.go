package limiter

import (
	"bytes"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

func (l *Limiter) sendWebhook(email, ip string, activeIPCount int, action string) {
	if l.config.WebhookURL == "" || l.config.WebhookTemplate == "" {
		return
	}

	go func() {
		bodyData := []byte(l.renderTemplate(email, ip, activeIPCount, action))

		req, err := http.NewRequest("POST", l.config.WebhookURL, bytes.NewBuffer(bodyData))
		if err != nil {
			l.logger.WithError(err).Warn("Ошибка создания webhook запроса")
			return
		}

		for key, value := range l.config.WebhookHeaders {
			req.Header.Set(key, value)
		}

		client := &http.Client{
			Timeout: 5 * time.Second,
		}

		resp, err := client.Do(req)
		if err != nil {
			l.logger.WithError(err).Warn("Ошибка отправки webhook")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			l.logger.WithFields(logrus.Fields{
				"email":  email,
				"ip":     ip,
				"action": action,
			}).Debug("Webhook отправлен")
		} else {
			l.logger.WithField("status", resp.StatusCode).Warn("Webhook вернул ошибку")
		}
	}()
}

func (l *Limiter) renderTemplate(email, ip string, activeIPCount int, action string) string {
	template := l.config.WebhookTemplate

	hostname, _ := os.Hostname()

	replacements := map[string]string{
		"%email":     email,
		"%ip":        ip,
		"%server":    hostname,
		"%action":    action,
		"%duration":  strconv.Itoa(l.config.BanDurationMinutes),
		"%timestamp": time.Now().Format(time.RFC3339),
	}

	result := template
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}
