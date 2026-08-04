package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	maxRetries      = 3
	initialBackoff  = 1 * time.Second
	jobPollInterval = 1 * time.Second
	jobPollMaxTries = 30
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *logrus.Logger
	cookies    []*http.Cookie
	headers    map[string]string
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) SetLogger(logger *logrus.Logger) {
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

func (c *Client) logDebug(args ...interface{}) {
	if c.logger != nil {
		c.logger.Debug(args...)
	}
}

func (c *Client) logWarn(args ...interface{}) {
	if c.logger != nil {
		c.logger.Warn(args...)
	}
}

func (c *Client) logError(args ...interface{}) {
	if c.logger != nil {
		c.logger.Error(args...)
	}
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
	backoff := initialBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			c.logWarn(fmt.Sprintf("API retry %d/%d for %s %s after %v", attempt, maxRetries, method, path, backoff))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}

		var reqBody io.Reader
		if bodyData != nil {
			reqBody = bytes.NewReader(bodyData)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")

		for name, value := range c.headers {
			req.Header.Set(name, value)
		}

		for _, cookie := range c.cookies {
			req.AddCookie(cookie)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http request: %w", err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response body: %w", err)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("API %s %s returned status %d: %s", method, path, resp.StatusCode, string(respBody))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("API %s %s returned status %d: %s", method, path, resp.StatusCode, string(respBody))
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("API %s %s failed after %d retries: %w", method, path, maxRetries, lastErr)
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

	var active []Node
	for _, node := range resp.Response {
		if node.IsConnected && !node.IsDisabled {
			active = append(active, node)
		}
	}

	c.logDebug(fmt.Sprintf("Got %d nodes, %d active", len(resp.Response), len(active)))
	return active, nil
}

func (c *Client) FetchUsersIPs(ctx context.Context, nodeUUID string) ([]UserIPEntry, error) {
	data, err := c.doRequest(ctx, http.MethodPost, "/api/connections/by-node/"+nodeUUID, nil)
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

	c.logDebug(fmt.Sprintf("Started connections/by-node job %s for node %s", jobID, nodeUUID))

	for i := 0; i < jobPollMaxTries; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(jobPollInterval):
		}

		data, err := c.doRequest(ctx, http.MethodGet, "/api/connections/by-node/"+jobID, nil)
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
			c.logDebug(fmt.Sprintf("Job %s completed: %d users", jobID, len(resultResp.Response.Result.Users)))
			return resultResp.Response.Result.Users, nil
		}
	}

	return nil, fmt.Errorf("job %s timed out after %d polls", jobID, jobPollMaxTries)
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
	// Панель требует непустой userIds, пустой запрос отклоняется с 400.
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
