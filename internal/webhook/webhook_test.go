package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/remnawave/limiter/internal/i18n"
)

func testLogger() *logrus.Logger {
	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)
	return l
}

func testPayload() *Payload {
	return &Payload{
		Event:      "violation_detected",
		ActionMode: "auto",
		User: UserPayload{
			UserID:   42,
			Username: "testuser",
			Email:    "test@example.com",
		},
		Violation: ViolationPayload{
			IPs: []IPPayload{
				{IP: "1.1.1.1", NodeName: "DE-1", NodeUUID: "node-1", LastSeen: time.Now()},
			},
			IPCount:           1,
			DeviceLimit:       3,
			Tolerance:         1,
			EffectiveLimit:    4,
			ViolationCount24h: 2,
		},
		Action: ActionPayload{
			AutoDisableDurationMin: 10,
		},
		Timestamp: time.Now(),
	}
}

func TestClient_Send_Success(t *testing.T) {
	var receivedPayload Payload
	var receivedContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&receivedPayload); err != nil {
			t.Errorf("failed to decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "", testLogger())
	client.Send(context.Background(), testPayload())

	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", receivedContentType)
	}
	if receivedPayload.Event != "violation_detected" {
		t.Errorf("expected event violation_detected, got %s", receivedPayload.Event)
	}
	if receivedPayload.User.Username != "testuser" {
		t.Errorf("expected username testuser, got %s", receivedPayload.User.Username)
	}
}

func TestClient_Send_WithSecret(t *testing.T) {
	var receivedSecret string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSecret = r.Header.Get("X-Webhook-Secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "my-secret-123", testLogger())
	client.Send(context.Background(), testPayload())

	if receivedSecret != "my-secret-123" {
		t.Errorf("expected secret my-secret-123, got %s", receivedSecret)
	}
}

func TestClient_Send_WithSecret_SignsBody(t *testing.T) {
	const secret = "my-secret-123"
	var receivedSig string
	var receivedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Signature")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, secret, testLogger())
	client.Send(context.Background(), testPayload())

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if receivedSig != want {
		t.Errorf("expected X-Signature %s, got %s", want, receivedSig)
	}
}

func TestClient_Send_NoSignature_WhenSecretEmpty(t *testing.T) {
	var calls int
	var hasSig, hasSigV2 bool
	var timestamp string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, hasSig = r.Header["X-Signature"]
		_, hasSigV2 = r.Header["X-Signature-V2"]
		timestamp = r.Header.Get("X-Timestamp")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "", testLogger())
	client.Send(context.Background(), testPayload())

	if calls != 1 {
		t.Fatalf("expected exactly 1 delivered request, got %d", calls)
	}
	if hasSig {
		t.Error("expected no X-Signature header when secret is empty")
	}
	if hasSigV2 {
		t.Error("expected no X-Signature-V2 header when secret is empty")
	}
	if timestamp == "" {
		t.Error("expected X-Timestamp to be sent even without a secret")
	}
}

func TestClient_Send_NoSecretHeader_WhenEmpty(t *testing.T) {
	var calls int
	var hasSecretHeader bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, hasSecretHeader = r.Header["X-Webhook-Secret"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "", testLogger())
	client.Send(context.Background(), testPayload())

	if calls != 1 {
		t.Fatalf("expected exactly 1 delivered request, got %d", calls)
	}
	if hasSecretHeader {
		t.Error("expected no X-Webhook-Secret header when secret is empty")
	}
}

func fastClient(url, secret string) *Client {
	c := NewClient(url, secret, testLogger())
	c.retryDelay = time.Millisecond
	return c
}

func TestClient_Send_ServerError_Retries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fastClient(srv.URL, "").Send(context.Background(), testPayload())

	if calls != maxAttempts {
		t.Errorf("expected %d attempts on 5xx, got %d", maxAttempts, calls)
	}
}

func TestClient_Send_ClientError_DoesNotRetry(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	fastClient(srv.URL, "").Send(context.Background(), testPayload())

	if calls != 1 {
		t.Errorf("expected 1 attempt on 4xx, got %d", calls)
	}
}

func TestClient_Send_RecoversAfterTransientError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fastClient(srv.URL, "").Send(context.Background(), testPayload())

	if calls != 2 {
		t.Errorf("expected delivery on 2nd attempt, got %d calls", calls)
	}
}

func TestClient_Send_SendsTimestamp(t *testing.T) {
	var ts string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts = r.Header.Get("X-Timestamp")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fastClient(srv.URL, "secret").Send(context.Background(), testPayload())

	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		t.Errorf("expected unix timestamp in X-Timestamp, got %q", ts)
	}
}

func hookedClient(url, secret string) (*Client, *logrustest.Hook) {
	logger, hook := logrustest.NewNullLogger()
	c := NewClient(url, secret, logger)
	c.retryDelay = time.Millisecond
	return c, hook
}

func entriesWith(hook *logrustest.Hook, level logrus.Level, key string) []*logrus.Entry {
	var out []*logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Level == level && e.Message == i18n.T(key) {
			out = append(out, e)
		}
	}
	return out
}

func TestClient_Send_Unreachable_RetriesAndReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	closedURL := srv.URL
	srv.Close()

	client, hook := hookedClient(closedURL+"/hook?token=supersecret", "")
	client.Send(context.Background(), testPayload())

	retries := entriesWith(hook, logrus.WarnLevel, "log.webhook_retry")
	if len(retries) != maxAttempts-1 {
		t.Errorf("повторов в логе %d, ожидалось %d", len(retries), maxAttempts-1)
	}
	exhausted := entriesWith(hook, logrus.ErrorLevel, "log.webhook_exhausted")
	if len(exhausted) != 1 {
		t.Fatalf("строк «попытки исчерпаны» %d, ожидалась одна", len(exhausted))
	}
	if len(hook.AllEntries()) != maxAttempts {
		t.Errorf("лишние записи в логе: %d, ожидалось %d", len(hook.AllEntries()), maxAttempts)
	}
	lastErr, ok := exhausted[0].Data[logrus.ErrorKey].(error)
	if !ok || lastErr == nil {
		t.Fatalf("в итоговой записи должна быть ошибка последней попытки: %v", exhausted[0].Data)
	}
	if exhausted[0].Data["event"] != "violation_detected" {
		t.Errorf("event = %v", exhausted[0].Data["event"])
	}
	for _, e := range hook.AllEntries() {
		line, err := e.String()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "supersecret") || strings.Contains(line, "/hook") {
			t.Errorf("URL webhook не должен попадать в лог: %s", line)
		}
	}
}

func TestClient_Send_RetryIsLoggedOnlyWhenAnotherAttemptFollows(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client, hook := hookedClient(srv.URL, "")
	client.Send(context.Background(), testPayload())

	if calls != maxAttempts {
		t.Fatalf("попыток %d, ожидалось %d", calls, maxAttempts)
	}
	retries := entriesWith(hook, logrus.WarnLevel, "log.webhook_retry")
	if len(retries) != maxAttempts-1 {
		t.Fatalf("строк «повтор» %d, ожидалось %d: после последней попытки повтора нет", len(retries), maxAttempts-1)
	}
	for i, e := range retries {
		if e.Data["attempt"] != i+1 || e.Data["of"] != maxAttempts {
			t.Errorf("повтор %d: attempt=%v of=%v", i, e.Data["attempt"], e.Data["of"])
		}
	}
	exhausted := entriesWith(hook, logrus.ErrorLevel, "log.webhook_exhausted")
	if len(exhausted) != 1 {
		t.Fatalf("строк «попытки исчерпаны» %d, ожидалась одна", len(exhausted))
	}
	last := hook.LastEntry()
	if last == nil || last.Message != i18n.T("log.webhook_exhausted") {
		t.Errorf("последней записью должна быть «попытки исчерпаны»: %+v", last)
	}
	if err, ok := exhausted[0].Data[logrus.ErrorKey].(error); !ok || !strings.Contains(err.Error(), "503") {
		t.Errorf("в итоговой записи должен быть статус последней попытки: %v", exhausted[0].Data)
	}
}

func TestClient_Send_SuccessAfterRetryLogsNoFailure(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < maxAttempts {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client, hook := hookedClient(srv.URL, "")
	client.Send(context.Background(), testPayload())

	if calls != maxAttempts {
		t.Fatalf("попыток %d, ожидалось %d", calls, maxAttempts)
	}
	if n := len(entriesWith(hook, logrus.WarnLevel, "log.webhook_retry")); n != maxAttempts-1 {
		t.Errorf("строк «повтор» %d, ожидалось %d", n, maxAttempts-1)
	}
	if n := len(hook.AllEntries()); n != maxAttempts-1 {
		t.Errorf("после доставки ошибок в логе быть не должно, записей %d", n)
	}
}

func TestClient_Send_CancelledContext_StopsRetrying(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fastClient(srv.URL, "").Send(ctx, testPayload())

	if calls != 0 {
		t.Errorf("expected no calls with cancelled context, got %d", calls)
	}
}

func TestClient_Send_SignatureV2CoversTimestamp(t *testing.T) {
	const secret = "my-secret-123"
	var gotSig, gotTS string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Signature-V2")
		gotTS = r.Header.Get("X-Timestamp")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	NewClient(srv.URL, secret, testLogger()).Send(context.Background(), testPayload())

	sign := func(ts string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + "."))
		mac.Write(gotBody)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	if gotTS == "" || gotSig != sign(gotTS) {
		t.Fatalf("X-Signature-V2 %q не совпадает с HMAC(timestamp.body) %q", gotSig, sign(gotTS))
	}
	if gotSig == sign("1") {
		t.Error("подпись не должна совпадать при другом timestamp")
	}
}

func TestClient_Send_RawSecretHeaderCanBeDisabled(t *testing.T) {
	var hasSecret, hasSig bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasSecret = r.Header["X-Webhook-Secret"]
		hasSig = r.Header.Get("X-Signature") != "" && r.Header.Get("X-Signature-V2") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "my-secret-123", testLogger())
	client.SetRawSecretHeader(false)
	client.Send(context.Background(), testPayload())

	if hasSecret {
		t.Error("при SetRawSecretHeader(false) секрет не должен уходить в заголовке")
	}
	if !hasSig {
		t.Error("подписи должны отправляться и без X-Webhook-Secret")
	}
}

func TestClient_Send_RedirectIsNotDelivery(t *testing.T) {
	var methods []string
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var logs bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logs)
	client := NewClient(srv.URL+"/hook", "", logger)
	client.Send(context.Background(), testPayload())

	if len(methods) != 0 {
		t.Errorf("после 301 запрос не должен уходить как GET без тела: %v", methods)
	}
	if !strings.Contains(logs.String(), "301") {
		t.Errorf("редирект должен попасть в лог как недоставленный webhook: %q", logs.String())
	}
}
