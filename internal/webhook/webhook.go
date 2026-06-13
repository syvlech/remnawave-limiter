package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

type Client struct {
	url        string
	secret     string
	httpClient *http.Client
	logger     *logrus.Logger
}

func NewClient(url, secret string, logger *logrus.Logger) *Client {
	return &Client{
		url:    url,
		secret: secret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

func (c *Client) Send(ctx context.Context, payload *Payload) {
	data, err := json.Marshal(payload)
	if err != nil {
		c.logger.WithError(err).Error("Ошибка сериализации webhook payload")
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		c.logger.WithError(err).Error("Ошибка создания webhook запроса")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if c.secret != "" {
		req.Header.Set("X-Webhook-Secret", c.secret)
		mac := hmac.New(sha256.New, []byte(c.secret))
		mac.Write(data)
		req.Header.Set("X-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.WithError(err).Error("Ошибка отправки webhook")
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		c.logger.WithField("status", resp.StatusCode).Warn("Webhook вернул ошибку")
	}
}
