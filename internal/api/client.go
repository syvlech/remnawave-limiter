package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/i18n"
)

const (
	maxRetries        = 3
	initialBackoff    = 1 * time.Second
	maxBackoff        = 30 * time.Second
	maxRetryAfter     = 24 * time.Hour
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

	pollFirstDelay time.Duration
	pollInterval   time.Duration
	pollMaxTries   int
	maxRespBytes   int64
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
			Timeout:       30 * time.Second,
			Transport:     newTransport(),
			CheckRedirect: KeepMethodOnRedirect,
		},
		logger:  discardLogger(),
		backoff: initialBackoff,

		pollFirstDelay: jobFirstPollDelay,
		pollInterval:   jobPollInterval,
		pollMaxTries:   jobPollMaxTries,
		maxRespBytes:   maxResponseBytes,
	}
}

const maxRedirects = 10

func KeepMethodOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if req.Method != via[0].Method {
		return http.ErrUseLastResponse
	}
	return nil
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
	last := ""
	for _, part := range splitHeaderList(headerStr) {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		name, value, ok := strings.Cut(trimmed, ":")
		name = strings.TrimSpace(name)
		if !ok || !validHeaderName(name) {
			if last != "" {
				headers[last] += ";" + strings.TrimRight(part, " \t")
			}
			continue
		}
		headers[name] = strings.TrimSpace(value)
		last = name
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func splitHeaderList(s string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\\' && i+1 < len(s) && s[i+1] == ';':
			cur.WriteByte(';')
			i++
		case ch == '(':
			depth++
			cur.WriteByte(ch)
		case ch == ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(ch)
		case ch == ';' && depth == 0:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	return append(parts, cur.String())
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", ch) >= 0:
		default:
			return false
		}
	}
	return true
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
	return s[:maxErrBodyLen] + i18n.T("error.api.truncated")
}

func retryableStatus(code int) bool {
	return code >= 500 || code == http.StatusTooManyRequests || code == http.StatusRequestTimeout
}

type retryPolicy int

const (
	retryTransient retryPolicy = iota
	retryUnsent
)

func (p retryPolicy) retryStatus(code int) bool {
	if p == retryUnsent {
		return code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable
	}
	return retryableStatus(code)
}

type attemptError struct {
	err       error
	permanent bool
	sent      bool
}

func (e *attemptError) Error() string { return e.err.Error() }

func (e *attemptError) Unwrap() error { return e.err }

func (p retryPolicy) retryError(err error) bool {
	var ae *attemptError
	if !errors.As(err, &ae) {
		return true
	}
	if ae.permanent {
		return false
	}
	return p != retryUnsent || !ae.sent
}

func validHeaderValue(v string) bool {
	for i := 0; i < len(v); i++ {
		if ch := v[i]; (ch < ' ' && ch != '\t') || ch == 0x7f {
			return false
		}
	}
	return true
}

func checkHeaders(h http.Header) error {
	for name, values := range h {
		if !validHeaderName(name) {
			return fmt.Errorf("invalid header field name %q", name)
		}
		for _, v := range values {
			if !validHeaderValue(v) {
				return fmt.Errorf("invalid header field value for %q", name)
			}
		}
	}
	return nil
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
		if secs > int(maxRetryAfter/time.Second) {
			return maxRetryAfter
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
	return c.doRequestWith(ctx, retryTransient, method, path, body)
}

func (c *Client) doRequestWith(ctx context.Context, policy retryPolicy, method, path string, body interface{}) ([]byte, error) {
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
	var serverWait time.Duration

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := jitter(backoff)
			if serverWait > 0 {
				wait = serverWait + time.Duration(rand.Float64()*0.25*float64(serverWait))
				serverWait = 0
			}
			c.logger.WithFields(logrus.Fields{
				"method":  method,
				"path":    path,
				"attempt": attempt,
				"of":      maxRetries,
				"wait":    wait,
			}).Warn(i18n.T("log.api_retry"))

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
			if !policy.retryError(err) {
				return nil, fmt.Errorf("API %s %s: %w", method, path, err)
			}
			lastErr = err
			continue
		}

		if policy.retryStatus(status) {
			lastErr = &StatusError{Method: method, Path: path, Code: status, Body: truncateBody(respBody)}
			if d := retryAfter(header); d > maxBackoff {
				return nil, fmt.Errorf("API %s %s asked to retry after %s: %w", method, path, d.Round(time.Second), lastErr)
			} else if d > 0 {
				serverWait = d
			}
			continue
		}

		if status < 200 || status >= 300 {
			return nil, &StatusError{Method: method, Path: path, Code: status, Body: truncateBody(respBody)}
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("API %s %s failed after %d retries: %w", method, path, maxRetries, lastErr)
}

type StatusError struct {
	Method string
	Path   string
	Code   int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("API %s %s returned status %d: %s", e.Method, e.Path, e.Code, e.Body)
}

const (
	panelErrAlreadyDisabled = "A029"
	panelErrAlreadyEnabled  = "A030"
)

func hasPanelError(err error, code string) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusBadRequest && strings.Contains(se.Body, `"`+code+`"`)
}

func IsAlreadyDisabled(err error) bool { return hasPanelError(err, panelErrAlreadyDisabled) }

func IsAlreadyEnabled(err error) bool { return hasPanelError(err, panelErrAlreadyEnabled) }

func (c *Client) attempt(ctx context.Context, method, path string, bodyData []byte) ([]byte, int, http.Header, error) {
	var reqBody io.Reader
	if bodyData != nil {
		reqBody = bytes.NewReader(bodyData)
	}

	var gotConn atomic.Bool
	traceCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { gotConn.Store(true) },
	})

	req, err := http.NewRequestWithContext(traceCtx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, 0, nil, &attemptError{err: fmt.Errorf("create request: %w", err), permanent: true}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	for name, value := range c.headers {
		if strings.EqualFold(name, "Host") {
			req.Host = value
			continue
		}
		req.Header.Set(name, value)
	}
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}

	if err := checkHeaders(req.Header); err != nil {
		return nil, 0, nil, &attemptError{err: fmt.Errorf("create request: %w", err), permanent: true}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, nil, &attemptError{err: fmt.Errorf("http request: %w", err), sent: gotConn.Load()}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, c.maxRespBytes+1))
	if err != nil {
		return nil, 0, nil, &attemptError{err: fmt.Errorf("read response body: %w", err), sent: true}
	}
	if int64(len(respBody)) > c.maxRespBytes {
		return nil, 0, nil, &attemptError{
			err:       fmt.Errorf(i18n.T("error.api.response_too_large"), c.maxRespBytes),
			permanent: true,
			sent:      true,
		}
	}

	return respBody, resp.StatusCode, resp.Header, nil
}

func (c *Client) GetNodes(ctx context.Context) ([]Node, error) {
	data, err := c.doRequest(ctx, http.MethodGet, "/api/nodes", nil)
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}

	var resp NodesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode nodes response: %w", err)
	}
	return resp.Response, nil
}

func (c *Client) GetActiveNodes(ctx context.Context) ([]Node, error) {
	all, err := c.GetNodes(ctx)
	if err != nil {
		return nil, err
	}

	active := make([]Node, 0, len(all))
	for _, node := range all {
		if node.IsConnected && !node.IsDisabled {
			active = append(active, node)
		}
	}

	c.logger.WithFields(logrus.Fields{
		"total":  len(all),
		"active": len(active),
	}).Debug(i18n.T("log.nodes_received"))
	return active, nil
}

func (c *Client) FetchUsersIPs(ctx context.Context, nodeUUID string) ([]UserIPEntry, error) {
	data, err := c.doRequestWith(ctx, retryUnsent, http.MethodPost, "/api/connections/by-node/"+url.PathEscape(nodeUUID), nil)
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
	}).Debug(i18n.T("log.job_started"))

	started := time.Now()
	delay := c.pollFirstDelay

	for i := 0; i < c.pollMaxTries; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = c.pollInterval

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
			}).Debug(i18n.T("log.job_completed"))
			return resultResp.Response.Result.Users, nil
		}
	}

	return nil, fmt.Errorf("job %s timed out after %d polls (%s)", jobID, c.pollMaxTries, time.Since(started).Truncate(time.Second))
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

func (c *Client) GetUserHWIDDevices(ctx context.Context, userID int64) ([]HWIDDevice, error) {
	data, err := c.doRequest(ctx, http.MethodGet, "/api/hwid/devices/"+strconv.FormatInt(userID, 10), nil)
	if err != nil {
		return nil, fmt.Errorf("get hwid devices %d: %w", userID, err)
	}
	var resp HWIDDevicesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode hwid devices: %w", err)
	}
	return resp.Response.Devices, nil
}

func (c *Client) GetUserSubscriptionHistory(ctx context.Context, userID int64) ([]SubscriptionRequest, error) {
	data, err := c.doRequest(ctx, http.MethodGet, "/api/users/"+strconv.FormatInt(userID, 10)+"/subscription-request-history", nil)
	if err != nil {
		return nil, fmt.Errorf("get subscription history %d: %w", userID, err)
	}
	var resp SubscriptionHistoryResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode subscription history: %w", err)
	}
	return resp.Response.Records, nil
}

func (c *Client) GetNodesUsage(ctx context.Context, nodeUUIDs []string, day string, minBytes int64) ([]NodeUsage, error) {
	q := url.Values{}
	q.Set("start", day)
	q.Set("end", day)
	q.Set("minTotalBytes", strconv.FormatInt(minBytes, 10))
	data, err := c.doRequest(ctx, http.MethodPost, "/api/bandwidth-stats/nodes/usage?"+q.Encode(),
		NodesUsageRequest{NodesUUIDs: nodeUUIDs})
	if err != nil {
		return nil, fmt.Errorf("get nodes usage %s: %w", day, err)
	}
	var resp NodesUsageResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode nodes usage: %w", err)
	}
	return resp.Response.Nodes, nil
}
