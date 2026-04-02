package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	maxRetries       = 3
	initialBackoff   = 1 * time.Second
	jobPollInterval  = 1 * time.Second
	jobPollMaxTries  = 30
)

// Client is an HTTP client for the Remnawave API.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *logrus.Logger
}

// NewClient creates a new API client with the given base URL and bearer token.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetLogger sets a logrus logger for the client.
func (c *Client) SetLogger(logger *logrus.Logger) {
	c.logger = logger
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

// doRequest executes an HTTP request with retry logic for 5xx errors.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
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

			// Re-create body reader for retry
			if body != nil {
				data, _ := json.Marshal(body)
				reqBody = bytes.NewReader(data)
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")

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

		// Retry only on 5xx
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

// GetActiveNodes returns nodes that are connected and not disabled.
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

// FetchUsersIPs starts a job to fetch user IPs for a node and polls until complete.
func (c *Client) FetchUsersIPs(ctx context.Context, nodeUUID string) ([]UserIPEntry, error) {
	// Start the job
	data, err := c.doRequest(ctx, http.MethodPost, "/api/ip-control/fetch-users-ips/"+nodeUUID, nil)
	if err != nil {
		return nil, fmt.Errorf("start fetch-users-ips job: %w", err)
	}

	var jobResp JobResponse
	if err := json.Unmarshal(data, &jobResp); err != nil {
		return nil, fmt.Errorf("decode job response: %w", err)
	}

	jobID := jobResp.Response.JobID
	if jobID == "" {
		return nil, fmt.Errorf("empty job ID in response")
	}

	c.logDebug(fmt.Sprintf("Started fetch-users-ips job %s for node %s", jobID, nodeUUID))

	// Poll for result
	for i := 0; i < jobPollMaxTries; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(jobPollInterval):
		}

		data, err := c.doRequest(ctx, http.MethodGet, "/api/ip-control/fetch-users-ips/result/"+jobID, nil)
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

// GetUserByID retrieves a user by their subscription ID.
func (c *Client) GetUserByID(ctx context.Context, id string) (*UserData, error) {
	data, err := c.doRequest(ctx, http.MethodGet, "/api/users/by-id/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("get user by id %s: %w", id, err)
	}

	var resp UserResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode user response: %w", err)
	}

	return &resp.Response, nil
}

// DisableUser disables a user by their UUID.
func (c *Client) DisableUser(ctx context.Context, uuid string) error {
	_, err := c.doRequest(ctx, http.MethodPost, "/api/users/"+uuid+"/actions/disable", nil)
	if err != nil {
		return fmt.Errorf("disable user %s: %w", uuid, err)
	}
	return nil
}

// EnableUser enables a user by their UUID.
func (c *Client) EnableUser(ctx context.Context, uuid string) error {
	_, err := c.doRequest(ctx, http.MethodPost, "/api/users/"+uuid+"/actions/enable", nil)
	if err != nil {
		return fmt.Errorf("enable user %s: %w", uuid, err)
	}
	return nil
}

// DropConnections drops active connections for the given user UUIDs on all nodes.
func (c *Client) DropConnections(ctx context.Context, userUUIDs []string) error {
	req := DropConnectionsRequest{
		DropBy: DropBy{
			By:        "userUuids",
			UserUUIDs: userUUIDs,
		},
		TargetNodes: TargetNodes{
			Target: "allNodes",
		},
	}

	_, err := c.doRequest(ctx, http.MethodPost, "/api/ip-control/drop-connections", req)
	if err != nil {
		return fmt.Errorf("drop connections: %w", err)
	}
	return nil
}
