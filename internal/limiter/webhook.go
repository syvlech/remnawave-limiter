package limiter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const webhookMaxRetries = 2

func (l *Limiter) sendWebhook(ctx context.Context, email, ip string, activeIPCount int, action string) {
	if l.config.WebhookURL == "" || l.config.WebhookTemplate == "" {
		return
	}

	l.webhookWg.Add(1)
	go func() {
		defer l.webhookWg.Done()

		bodyData := []byte(l.renderTemplate(email, ip, activeIPCount, action))

		var lastErr error
		for attempt := 0; attempt <= webhookMaxRetries; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					l.logger.Debug("Webhook retry отменён: контекст завершён")
					return
				case <-time.After(time.Duration(attempt) * 2 * time.Second):
				}
			}

			req, err := http.NewRequestWithContext(ctx, "POST", l.config.WebhookURL, bytes.NewBuffer(bodyData))
			if err != nil {
				l.logger.WithError(err).Warn("Ошибка создания webhook запроса")
				return
			}

			for key, value := range l.config.WebhookHeaders {
				req.Header.Set(key, value)
			}

			resp, err := l.httpClient.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				lastErr = err
				l.logger.WithFields(logrus.Fields{
					"attempt": attempt + 1,
					"error":   err,
				}).Debug("Попытка отправки webhook не удалась")
				continue
			}
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				l.logger.WithFields(logrus.Fields{
					"email":  email,
					"ip":     ip,
					"action": action,
				}).Debug("Webhook отправлен")
				return
			}

			l.logger.WithFields(logrus.Fields{
				"status":  resp.StatusCode,
				"attempt": attempt + 1,
			}).Debug("Webhook вернул ошибку, повтор...")
			lastErr = fmt.Errorf("webhook returned status %d", resp.StatusCode)
		}

		if lastErr != nil {
			l.logger.WithError(lastErr).Warn("Ошибка отправки webhook после всех попыток")
		}
	}()
}

func jsonEscapeValue(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	return string(b[1 : len(b)-1])
}

func (l *Limiter) renderTemplate(email, ip string, activeIPCount int, action string) string {
	template := l.config.WebhookTemplate

	hostname, _ := os.Hostname()

	replacements := map[string]string{
		"%email":     jsonEscapeValue(email),
		"%ip":        jsonEscapeValue(ip),
		"%server":    jsonEscapeValue(hostname),
		"%action":    jsonEscapeValue(action),
		"%duration":  strconv.Itoa(l.config.BanDurationMinutes),
		"%timestamp": time.Now().Format(time.RFC3339),
	}

	result := template
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}
