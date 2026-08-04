package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_GetNodes(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing or wrong auth header: %s", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		resp := NodesResponse{
			Response: []Node{
				{UUID: "node-1", Name: "Node 1", Address: "1.1.1.1", IsConnected: true, IsDisabled: false, CountryCode: "US"},
				{UUID: "node-2", Name: "Node 2", Address: "2.2.2.2", IsConnected: true, IsDisabled: true, CountryCode: "DE"},
				{UUID: "node-3", Name: "Node 3", Address: "3.3.3.3", IsConnected: false, IsDisabled: false, CountryCode: "FR"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	nodes, err := client.GetActiveNodes(context.Background())
	if err != nil {
		t.Fatalf("GetActiveNodes returned error: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("expected 1 active node, got %d", len(nodes))
	}
	if nodes[0].UUID != "node-1" {
		t.Errorf("expected node-1, got %s", nodes[0].UUID)
	}
}

func TestClient_FetchUsersIPs(t *testing.T) {
	const nodeUUID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	var postCalls, resultCalls int

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/connections/by-node/"+nodeUUID:
			postCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"response":{"jobId":"job-1"}}`))

		case r.Method == http.MethodGet && r.URL.Path == "/api/connections/by-node/job-1":
			resultCalls++
			w.Header().Set("Content-Type", "application/json")
			if resultCalls == 1 {
				// Задание ещё выполняется.
				w.Write([]byte(`{"response":{"isCompleted":false,"isFailed":false,"result":null}}`))
				return
			}
			// userId приходит числом, а не строкой, как было до Remnawave 3.x.
			w.Write([]byte(`{"response":{"isCompleted":true,"isFailed":false,"result":{
				"success":true,
				"nodeUuid":"` + nodeUUID + `",
				"users":[
					{"userId":42,"ips":[{"ip":"1.1.1.1","lastSeen":"2026-08-04T10:00:00.000Z"},{"ip":"2.2.2.2","lastSeen":"2026-08-04T10:01:00.000Z"}]},
					{"userId":9007199254740993,"ips":[{"ip":"3.3.3.3","lastSeen":"2026-08-04T10:02:00.000Z"}]}
				]
			}}}`))

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	entries, err := client.FetchUsersIPs(context.Background(), nodeUUID)
	if err != nil {
		t.Fatalf("FetchUsersIPs returned error: %v", err)
	}

	if postCalls != 1 {
		t.Errorf("expected 1 job start, got %d", postCalls)
	}
	if resultCalls != 2 {
		t.Errorf("expected 2 result polls, got %d", resultCalls)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].UserID != 42 {
		t.Errorf("expected userId 42, got %d", entries[0].UserID)
	}
	if len(entries[0].IPs) != 2 || entries[0].IPs[0].IP != "1.1.1.1" {
		t.Errorf("unexpected ips for first entry: %+v", entries[0].IPs)
	}
	if entries[0].IPs[0].LastSeen.IsZero() {
		t.Error("lastSeen was not parsed")
	}
	// ID больше 2^53 не должен терять точность (json.Number -> int64, а не float64).
	if entries[1].UserID != 9007199254740993 {
		t.Errorf("expected userId 9007199254740993, got %d", entries[1].UserID)
	}
}

func TestClient_GetUserByID(t *testing.T) {
	email := "user@example.com"
	telegramID := int64(12345)
	hwidLimit := 3

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/42" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		resp := UserResponse{
			Response: UserData{
				ID:              42,
				Username:        "testuser",
				Status:          "ACTIVE",
				Email:           &email,
				TelegramID:      &telegramID,
				HWIDDeviceLimit: &hwidLimit,
				SubscriptionURL: "https://example.com/sub/abc",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	user, err := client.GetUserByID(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetUserByID returned error: %v", err)
	}

	if user.ID != 42 {
		t.Errorf("expected id 42, got %d", user.ID)
	}
	if user.Username != "testuser" {
		t.Errorf("expected testuser, got %s", user.Username)
	}
	if user.Email == nil || *user.Email != "user@example.com" {
		t.Errorf("unexpected email: %v", user.Email)
	}
	if user.HWIDDeviceLimit == nil || *user.HWIDDeviceLimit != 3 {
		t.Errorf("unexpected hwidDeviceLimit: %v", user.HWIDDeviceLimit)
	}
}

func TestClient_DisableUser(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/42/actions/disable" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":{}}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	err := client.DisableUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("DisableUser returned error: %v", err)
	}
}

func TestClient_EnableUser(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/42/actions/enable" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":{}}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	err := client.EnableUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("EnableUser returned error: %v", err)
	}
}

func TestClient_DropConnections(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/connections/drop" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var req DropConnectionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if req.DropBy.By != "userIds" {
			t.Errorf("expected dropBy.by=userIds, got %s", req.DropBy.By)
		}
		if len(req.DropBy.UserIDs) != 2 || req.DropBy.UserIDs[0] != 42 || req.DropBy.UserIDs[1] != 77 {
			t.Errorf("unexpected userIds: %v", req.DropBy.UserIDs)
		}
		if req.TargetNodes.Target != "allNodes" {
			t.Errorf("expected targetNodes.target=allNodes, got %s", req.TargetNodes.Target)
		}

		w.WriteHeader(http.StatusAccepted)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	err := client.DropConnections(context.Background(), []int64{42, 77})
	if err != nil {
		t.Fatalf("DropConnections returned error: %v", err)
	}
}

// fastClient — клиент без реальных пауз между повторами.
func fastClient(baseURL string) *Client {
	c := NewClient(baseURL, "test-token")
	c.backoff = time.Millisecond
	return c
}

func TestClient_RetriesOn5xx(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"response":[]}`))
	}))
	defer srv.Close()

	if _, err := fastClient(srv.URL).GetActiveNodes(context.Background()); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestClient_RetriesOn429(t *testing.T) {
	// Панель за Cloudflare отдаёт 429; раньше это была мгновенная ошибка
	// и вся проверка срывалась.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"response":[]}`))
	}))
	defer srv.Close()

	if _, err := fastClient(srv.URL).GetActiveNodes(context.Background()); err != nil {
		t.Fatalf("expected success after 429 retry, got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestClient_DoesNotRetryOn4xx(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := fastClient(srv.URL).GetActiveNodes(context.Background()); err == nil {
		t.Fatal("expected error on 401")
	}
	if calls != 1 {
		t.Errorf("expected no retry on 401, got %d calls", calls)
	}
}

func TestClient_ErrorBodyIsTruncated(t *testing.T) {
	// WAF перед панелью умеет отдавать мегабайтные HTML-страницы;
	// целиком они не должны попадать ни в лог, ни в Telegram.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(strings.Repeat("A", 100_000)))
	}))
	defer srv.Close()

	_, err := fastClient(srv.URL).GetActiveNodes(context.Background())
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if len(err.Error()) > maxErrBodyLen+300 {
		t.Errorf("error message not truncated: %d chars", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "обрезано") {
		t.Errorf("expected truncation marker in error, got: %.200s", err.Error())
	}
}

func TestClient_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := fastClient(srv.URL).GetActiveNodes(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "5", 5 * time.Second},
		{"zero", "0", 0},
		{"negative", "-3", 0},
		{"garbage", "soon", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.header != "" {
				h.Set("Retry-After", tc.header)
			}
			if got := retryAfter(h); got != tc.want {
				t.Errorf("retryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestClient_TrimsTrailingSlashInBaseURL(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"response":[]}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL+"/", "test-token")
	if _, err := client.GetActiveNodes(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/api/nodes" {
		t.Errorf("path = %q, want /api/nodes", path)
	}
}
