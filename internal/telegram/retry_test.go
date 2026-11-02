package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testRetryClient() *retryClient {
	return &retryClient{
		client:      &http.Client{Timeout: 5 * time.Second},
		maxAttempts: 4,
		startDelay:  time.Millisecond,
		maxDelay:    50 * time.Millisecond,
	}
}

func postForm(t *testing.T, c *retryClient, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestRetryClient_RetriesGatewayErrorsWithSameBody(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("попытка %d: тело %q, ожидалось payload", calls.Load()+1, body)
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	resp := postForm(t, testRetryClient(), srv.URL+"/botX/sendMessage", "payload")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус %d, ожидался 200", resp.StatusCode)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("попыток %d, ожидалось 3", got)
	}
}

func TestRetryClient_RateLimitWithinMaxDelayRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":0}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	resp := postForm(t, testRetryClient(), srv.URL+"/botX/sendMessage", "x")
	if resp.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Errorf("статус %d после %d попыток, ожидался 200 после 2", resp.StatusCode, calls.Load())
	}
}

func TestRetryClient_LongRateLimitReturnedAsIs(t *testing.T) {
	var calls atomic.Int32
	const body = `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":60}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	resp := postForm(t, testRetryClient(), srv.URL+"/botX/sendMessage", "x")
	got, _ := io.ReadAll(resp.Body)
	if string(got) != body {
		t.Errorf("тело ответа потеряно: %q", got)
	}
	if calls.Load() != 1 {
		t.Errorf("попыток %d, ожидалась 1", calls.Load())
	}
}

func TestRetryClient_ClientErrorNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request"}`))
	}))
	defer srv.Close()

	postForm(t, testRetryClient(), srv.URL+"/botX/sendMessage", "x")
	if calls.Load() != 1 {
		t.Errorf("400 не должен повторяться, попыток %d", calls.Load())
	}
}

func TestRetryClient_GetUpdatesNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	postForm(t, testRetryClient(), srv.URL+"/botX/getUpdates", "x")
	if calls.Load() != 1 {
		t.Errorf("getUpdates не должен повторяться, попыток %d", calls.Load())
	}
}

func TestRetryClient_GivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	resp := postForm(t, testRetryClient(), srv.URL+"/botX/sendMessage", "x")
	if resp.StatusCode != http.StatusServiceUnavailable || calls.Load() != 4 {
		t.Errorf("статус %d после %d попыток, ожидался 503 после 4", resp.StatusCode, calls.Load())
	}
}

func TestValidToken(t *testing.T) {
	cases := map[string]bool{
		"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi": true,
		"123456:short": false,
		"abc:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi":    false,
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi":        false,
		"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabc/efghi": false,
		"": false,
	}
	for tok, want := range cases {
		if got := validToken(tok); got != want {
			t.Errorf("validToken(%q) = %v, ожидалось %v", tok, got, want)
		}
	}
}
