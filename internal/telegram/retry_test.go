package telegram

import (
	"context"
	"errors"
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

type flakyTransport struct {
	failures atomic.Int32
	attempts atomic.Int32
	next     http.RoundTripper
}

func (f *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.attempts.Add(1)
	if f.failures.Add(-1) >= 0 {
		if req.Body != nil {
			_, _ = io.Copy(io.Discard, req.Body)
			_ = req.Body.Close()
		}
		return nil, errors.New("connection reset by peer")
	}
	return f.next.RoundTrip(req)
}

func TestRetryClient_RetriesNetworkErrorWithSameBody(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("после сетевой ошибки тело должно отправляться заново, получено %q", body)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	transport := &flakyTransport{next: http.DefaultTransport}
	transport.failures.Store(2)
	c := testRetryClient()
	c.client = &http.Client{Transport: transport, Timeout: 5 * time.Second}

	resp := postForm(t, c, srv.URL+"/botX/sendMessage", "payload")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус %d, ожидался 200", resp.StatusCode)
	}
	if got := transport.attempts.Load(); got != 3 {
		t.Errorf("попыток %d, ожидалось 3 (две сетевые ошибки и успех)", got)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("до сервера должен дойти один запрос, дошло %d", got)
	}
}

func TestRetryClient_NetworkErrorReturnedAfterMaxAttempts(t *testing.T) {
	transport := &flakyTransport{next: http.DefaultTransport}
	transport.failures.Store(100)
	c := testRetryClient()
	c.client = &http.Client{Transport: transport, Timeout: 5 * time.Second}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://127.0.0.1:1/botX/sendMessage", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("после исчерпания попыток должна вернуться сетевая ошибка")
	}
	if !strings.Contains(err.Error(), "connection reset by peer") {
		t.Errorf("должна вернуться исходная ошибка, получено %v", err)
	}
	if got := transport.attempts.Load(); got != 4 {
		t.Errorf("попыток %d, ожидалось 4", got)
	}
}

func TestRetryClient_GetUpdatesNetworkErrorNotRetried(t *testing.T) {
	transport := &flakyTransport{next: http.DefaultTransport}
	transport.failures.Store(100)
	c := testRetryClient()
	c.client = &http.Client{Transport: transport, Timeout: 5 * time.Second}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://127.0.0.1:1/botX/getUpdates", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := c.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("ожидалась сетевая ошибка")
	}
	if got := transport.attempts.Load(); got != 1 {
		t.Errorf("getUpdates не должен повторяться, попыток %d", got)
	}
}

func TestRetryClient_ContextCancelledDuringPause(t *testing.T) {
	var calls atomic.Int32
	firstDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			defer close(firstDone)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := testRetryClient()
	c.startDelay = 30 * time.Second
	c.maxDelay = 30 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-firstDone
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/botX/sendMessage", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := c.Do(req)
	if !errors.Is(err, context.Canceled) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Fatalf("ожидалась ошибка отмены контекста, получено %v", err)
	}
	if resp != nil {
		t.Error("при отмене во время паузы ответ возвращаться не должен")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("пауза должна прерываться отменой контекста, прошло %v", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("после отмены повторов быть не должно, попыток %d", got)
	}
}

func TestRetryClient_RetryAfterHeaderFallback(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	c := testRetryClient()
	c.maxDelay = 5 * time.Second

	start := time.Now()
	resp := postForm(t, c, srv.URL+"/botX/sendMessage", "x")
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("статус %d после %d попыток, ожидался 200 после 2", resp.StatusCode, calls.Load())
	}
	if elapsed < time.Second {
		t.Errorf("пауза должна браться из заголовка Retry-After (1с), прошло %v", elapsed)
	}
}

func TestRetryClient_LongRetryAfterHeaderReturnedAsIs(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	resp := postForm(t, testRetryClient(), srv.URL+"/botX/sendMessage", "x")
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusTooManyRequests || string(got) != "rate limited" {
		t.Errorf("ответ 429 должен вернуться как есть: статус %d, тело %q", resp.StatusCode, got)
	}
	if calls.Load() != 1 {
		t.Errorf("Retry-After больше maxDelay не ждём, попыток %d", calls.Load())
	}
}

func TestValidToken(t *testing.T) {
	cases := map[string]bool{
		"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi": true,
		"123456:short": false,
		"abc:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi":      false,
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi":          false,
		"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabc/efghi":   false,
		"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefg%zz":  false,
		"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ_abc-defghi": true,
		"": false,
	}
	for tok, want := range cases {
		if got := validToken(tok); got != want {
			t.Errorf("validToken(%q) = %v, ожидалось %v", tok, got, want)
		}
	}
}
