package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
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
	var hasSig bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasSig = r.Header["X-Signature"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "", testLogger())
	client.Send(context.Background(), testPayload())

	if hasSig {
		t.Error("expected no X-Signature header when secret is empty")
	}
}

func TestClient_Send_NoSecretHeader_WhenEmpty(t *testing.T) {
	var hasSecretHeader bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasSecretHeader = r.Header["X-Webhook-Secret"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "", testLogger())
	client.Send(context.Background(), testPayload())

	if hasSecretHeader {
		t.Error("expected no X-Webhook-Secret header when secret is empty")
	}
}

// fastClient — клиент без реальных пауз между повторами, чтобы тесты
// ретраев не занимали секунды.
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

	// 4xx — осознанный отказ получателя, повторять нечего.
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

func TestClient_Send_Unreachable_DoesNotPanic(t *testing.T) {
	fastClient("http://127.0.0.1:1", "").Send(context.Background(), testPayload())
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
