package telegram

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type retryClient struct {
	client      *http.Client
	maxAttempts int
	startDelay  time.Duration
	maxDelay    time.Duration
}

func (c *retryClient) Do(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/getUpdates") {
		return c.client.Do(req)
	}

	delay := c.startDelay
	for attempt := 1; ; attempt++ {
		resp, err := c.client.Do(req)
		if attempt >= c.maxAttempts || req.Context().Err() != nil {
			return resp, err
		}

		wait := delay
		if err == nil {
			retry, retryAfter, kept := c.inspect(resp)
			resp = kept
			if !retry {
				return resp, nil
			}
			if retryAfter > 0 {
				if retryAfter > c.maxDelay {
					return resp, nil
				}
				wait = retryAfter
			}
		}

		next, rerr := rewind(req)
		if rerr != nil {
			return resp, err
		}
		if resp != nil {
			_ = resp.Body.Close()
		}

		t := time.NewTimer(jitter(wait))
		select {
		case <-req.Context().Done():
			t.Stop()
			return nil, req.Context().Err()
		case <-t.C:
		}

		req = next
		delay = min(delay*2, c.maxDelay)
	}
}

func (c *retryClient) inspect(resp *http.Response) (retry bool, retryAfter time.Duration, kept *http.Response) {
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true, 0, resp
	default:
		return false, 0, resp
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return false, 0, resp
	}

	var r struct {
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	secs := 0
	if json.Unmarshal(body, &r) == nil {
		secs = r.Parameters.RetryAfter
	}
	if secs <= 0 {
		secs, _ = strconv.Atoi(resp.Header.Get("Retry-After"))
	}
	return true, time.Duration(max(secs, 0)) * time.Second, resp
}

func rewind(req *http.Request) (*http.Request, error) {
	next := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		next.Body = body
	}
	return next, nil
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d + rand.N(d/4+1)
}
