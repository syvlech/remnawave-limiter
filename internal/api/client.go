package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	maxRetries        = 3
	initialBackoff    = 1 * time.Second
	maxBackoff        = 30 * time.Second
	jobFirstPollDelay = 300 * time.Millisecond
	jobPollInterval   = 1 * time.Second
	jobPollMaxTries   = 30

	maxResponseBytes = 64 << 20

	maxErrBodyLen = 512
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *logrus.Logger
	cookies    []*http.Cookie
	headers    map[string]string

	backoff time.Duration
}

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func discardLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: newTransport(),
		},
		logger:  discardLogger(),
		backoff: initialBackoff,
	}
}

func (c *Client) SetLogger(logger *logrus.Logger) {
	if logger == nil {
		return
	}
	c.logger = logger
}

func (c *Client) SetCookies(cookies []*http.Cookie) {
	c.cookies = cookies
}

func (c *Client) SetHeaders(headers map[string]string) {
	c.headers = headers
}

func ParseHeaders(headerStr string) map[string]string {
	if headerStr == "" {
		return nil
	}

	headers := make(map[string]string)
	for _, part := range strings.Split(headerStr, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		name := strings.TrimSpace(kv[0])
		if name == "" {
			continue
		}
		headers[name] = strings.TrimSpace(kv[1])
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func ParseCookies(cookieStr string) []*http.Cookie {
	if cookieStr == "" {
		return nil
	}

	var cookies []*http.Cookie
	for _, part := range strings.Split(cookieStr, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:  strings.TrimSpace(kv[0]),
			Value: strings.TrimSpace(kv[1]),
		})
	}
	return cookies
}

func truncateBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) <= maxErrBodyLen {
		return s
	}
	return s[:maxErrBodyLen] + "… (обрезано)"
}

func retryableStatus(code int) bool {
	return code >= 500 || code == http.StatusTooManyRequests || code == http.StatusRequestTimeout
}

func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	delta := float64(d) * 0.25
	return time.Duration(float64(d) - delta + rand.Float64()*2*delta)
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var bodyData []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyData = data
	}

	var lastErr error
	backoff := c.backoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := jitter(backoff)
			c.logger.WithFields(logrus.Fields{
				"method":  method,
				"path":    path,
				"attempt": attempt,
				"of":      maxRetries,
				"wait":    wait,
			}).Warn("Повтор запроса к панели")

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}

			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}

		respBody, status, header, err := c.attempt(ctx, method, path, bodyData)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		if retryableStatus(status) {
			lastErr = fmt.Errorf("API %s %s returned status %d: %s", method, path, status, truncateBody(respBody))
			if d := retryAfter(header); d > 0 && d <= maxBackoff {
				backoff = d
			}
			continue
		}

		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("API %s %s returned status %d: %s", method, path, status, truncateBody(respBody))
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("API %s %s failed after %d retries: %w", method, path, maxRetries, lastErr)
}

func (c *Client) attempt(ctx context.Context, method, path string, bodyData []byte) ([]byte, int, http.Header, error) {
	var reqBody io.Reader
	if bodyData != nil {
		reqBody = bytes.NewReader(bodyData)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("read response body: %w", err)
	}
	if len(respBody) > maxResponseBytes {
		return nil, 0, nil, fmt.Errorf("ответ панели больше %d байт — отброшен", maxResponseBytes)
	}

	return respBody, resp.StatusCode, resp.Header, nil
}

func (c *Client) GetActiveNodes(ctx context.Context) ([]Node, error) {
	data, err := c.doRequest(ctx, http.MethodGet, "/api/nodes", nil)
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}

	var resp NodesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode nodes response: %w", err)
	}

	active := make([]Node, 0, len(resp.Response))
	for _, node := range resp.Response {
		if node.IsConnected && !node.IsDisabled {
			active = append(active, node)
		}
	}

	c.logger.WithFields(logrus.Fields{
		"total":  len(resp.Response),
		"active": len(active),
	}).Debug("Получен список нод")
	return active, nil
}

func (c *Client) FetchUsersIPs(ctx context.Context, nodeUUID string) ([]UserIPEntry, error) {
	data, err := c.doRequest(ctx, http.MethodPost, "/api/connections/by-node/"+url.PathEscape(nodeUUID), nil)
	if err != nil {
		return nil, fmt.Errorf("start connections/by-node job: %w", err)
	}

	var jobResp JobResponse
	if err := json.Unmarshal(data, &jobResp); err != nil {
		return nil, fmt.Errorf("decode job response: %w", err)
	}

	jobID := jobResp.Response.JobID
	if jobID == "" {
		return nil, fmt.Errorf("empty job ID in response")
	}

	c.logger.WithFields(logrus.Fields{
		"job":  jobID,
		"node": nodeUUID,
	}).Debug("Запущено задание connections/by-node")

	started := time.Now()
	delay := jobFirstPollDelay

	for i := 0; i < jobPollMaxTries; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = jobPollInterval

		data, err := c.doRequest(ctx, http.MethodGet, "/api/connections/by-node/"+url.PathEscape(jobID), nil)
		if err != nil {
			return nil, fmt.Errorf("poll job %s result: %w", jobID, err)
		}

		var resultResp UsersIPsResultResponse
		if err := json.Unmarshal(data, &resultResp); err != nil {
			return nil, fmt.Errorf("decode job result: %w", err)
		}

		if resultResp.Response.IsFailed {
			return nil, fmt.Errorf("job %s failed", jobID)
		}

		if resultResp.Response.IsCompleted {
			if resultResp.Response.Result == nil {
				return nil, fmt.Errorf("job %s completed but result is nil", jobID)
			}
			if !resultResp.Response.Result.Success {
				return nil, fmt.Errorf("job %s completed but success=false", jobID)
			}
			c.logger.WithFields(logrus.Fields{
				"job":     jobID,
				"node":    nodeUUID,
				"users":   len(resultResp.Response.Result.Users),
				"elapsed": time.Since(started).Truncate(time.Millisecond),
				"polls":   i + 1,
			}).Debug("Задание connections/by-node завершено")
			return resultResp.Response.Result.Users, nil
		}
	}

	return nil, fmt.Errorf("job %s timed out after %d polls (%s)", jobID, jobPollMaxTries, time.Since(started).Truncate(time.Second))
}

func (c *Client) GetUserByID(ctx context.Context, userID int64) (*UserData, error) {
	data, err := c.doRequest(ctx, http.MethodGet, "/api/users/"+strconv.FormatInt(userID, 10), nil)
	if err != nil {
		return nil, fmt.Errorf("get user by id %d: %w", userID, err)
	}

	var resp UserResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode user response: %w", err)
	}

	return &resp.Response, nil
}

func (c *Client) DisableUser(ctx context.Context, userID int64) error {
	_, err := c.doRequest(ctx, http.MethodPost, "/api/users/"+strconv.FormatInt(userID, 10)+"/actions/disable", nil)
	if err != nil {
		return fmt.Errorf("disable user %d: %w", userID, err)
	}
	return nil
}

func (c *Client) EnableUser(ctx context.Context, userID int64) error {
	_, err := c.doRequest(ctx, http.MethodPost, "/api/users/"+strconv.FormatInt(userID, 10)+"/actions/enable", nil)
	if err != nil {
		return fmt.Errorf("enable user %d: %w", userID, err)
	}
	return nil
}

func (c *Client) DropConnections(ctx context.Context, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}

	req := DropConnectionsRequest{
		DropBy: DropBy{
			By:      "userIds",
			UserIDs: userIDs,
		},
		TargetNodes: TargetNodes{
			Target: "allNodes",
		},
	}

	_, err := c.doRequest(ctx, http.MethodPost, "/api/connections/drop", req)
	if err != nil {
		return fmt.Errorf("drop connections: %w", err)
	}
	return nil
}
