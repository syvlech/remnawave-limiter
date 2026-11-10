package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/remnawave/limiter/internal/i18n"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	maxAttempts       = 3
	defaultRetryDelay = 2 * time.Second

	maxDrainBytes = 4 << 10
)

type Client struct {
	url        string
	secret     string
	httpClient *http.Client
	logger     *logrus.Logger
	retryDelay time.Duration

	rawSecretHeader bool
}

func NewClient(url, secret string, logger *logrus.Logger) *Client {
	return &Client{
		url:    url,
		secret: secret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:   true,
				MaxIdleConns:        10,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
		logger:          logger,
		retryDelay:      defaultRetryDelay,
		rawSecretHeader: true,
	}
}

func (c *Client) SetRawSecretHeader(enabled bool) {
	c.rawSecretHeader = enabled
}

func (c *Client) sign(parts ...[]byte) string {
	mac := hmac.New(sha256.New, []byte(c.secret))
	for _, p := range parts {
		mac.Write(p)
	}
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (c *Client) Send(ctx context.Context, payload *Payload) {
	data, err := json.Marshal(payload)
	if err != nil {
		c.logger.WithError(err).Error(i18n.T("log.webhook_encode_failed"))
		return
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	var sigs signatures
	if c.secret != "" {
		sigs.body = c.sign(data)
		sigs.timestamped = c.sign([]byte(timestamp), []byte("."), data)
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.retryDelay * time.Duration(attempt-1)):
			}
		}

		retry, err := c.attempt(ctx, data, timestamp, sigs)
		if err == nil {
			return
		}
		if !retry || ctx.Err() != nil {
			c.logger.WithError(err).WithField("event", payload.Event).Error(i18n.T("log.webhook_delivery_failed"))
			return
		}
		c.logger.WithError(err).WithFields(logrus.Fields{
			"attempt": attempt,
			"of":      maxAttempts,
			"event":   payload.Event,
		}).Warn(i18n.T("log.webhook_retry"))
	}

	c.logger.WithField("event", payload.Event).Error(i18n.T("log.webhook_exhausted"))
}

type signatures struct {
	body        string
	timestamped string
}

func (c *Client) attempt(ctx context.Context, data []byte, timestamp string, sigs signatures) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return false, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "remnawave-limiter")
	req.Header.Set("X-Timestamp", timestamp)
	if c.secret != "" {
		if c.rawSecretHeader {
			req.Header.Set("X-Webhook-Secret", c.secret)
		}
		req.Header.Set("X-Signature", sigs.body)
		req.Header.Set("X-Signature-V2", sigs.timestamped)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return true, &statusError{code: resp.StatusCode}
	}
	if resp.StatusCode >= 400 {
		return false, &statusError{code: resp.StatusCode}
	}
	return false, nil
}

type statusError struct{ code int }

func (e *statusError) Error() string {
	return "получатель вернул статус " + strconv.Itoa(e.code)
}
