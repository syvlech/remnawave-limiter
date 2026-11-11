package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
				w.Write([]byte(`{"response":{"isCompleted":false,"isFailed":false,"result":null}}`))
				return
			}
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

func TestClient_GetUserHWIDDevices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/hwid/devices/42" {
			t.Errorf("неожиданный путь %s", r.URL.Path)
		}
		w.Write([]byte(`{"response":{"total":2,"devices":[
			{"hwid":"abc123def456","userId":42,"platform":"iOS","osVersion":"18.1","deviceModel":"iPhone15,2","userAgent":"Happ/2.7.0","requestIp":"1.1.1.1","createdAt":"2026-11-01T10:00:00.000Z","updatedAt":"2026-11-11T09:30:00.000Z"},
			{"hwid":"zzz999zzz999","userId":42,"platform":null,"osVersion":null,"deviceModel":null,"userAgent":null,"requestIp":null,"createdAt":"2026-11-02T10:00:00.000Z","updatedAt":"2026-11-02T10:00:00.000Z"}
		]}}`))
	}))
	defer srv.Close()

	devices, err := fastClient(srv.URL).GetUserHWIDDevices(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].DeviceModel != "iPhone15,2" || devices[0].UpdatedAt.Hour() != 9 {
		t.Fatalf("устройства разобраны неверно: %+v", devices)
	}
	if devices[1].Platform != "" || devices[1].UserAgent != "" {
		t.Errorf("null в строковых полях должен давать пустую строку: %+v", devices[1])
	}
}

func TestClient_GetUserSubscriptionHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/42/subscription-request-history" {
			t.Errorf("неожиданный путь %s", r.URL.Path)
		}
		w.Write([]byte(`{"response":{"total":1,"records":[
			{"id":1,"userId":42,"requestAt":"2026-11-11T09:30:00.000Z","srrResponseType":"XRAY_BASE64","requestIp":"1.1.1.1","userAgent":"v2rayNG/1.9.30","srrRuleName":null}
		]}}`))
	}))
	defer srv.Close()

	records, err := fastClient(srv.URL).GetUserSubscriptionHistory(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].RequestIP != "1.1.1.1" || records[0].UserAgent != "v2rayNG/1.9.30" {
		t.Fatalf("история разобрана неверно: %+v", records)
	}
}

func TestClient_StatusErrorCarriesCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Forbidden resource"}`))
	}))
	defer srv.Close()

	_, err := fastClient(srv.URL).GetUserHWIDDevices(context.Background(), 1)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden {
		t.Fatalf("ожидалась StatusError с кодом 403, получено %v", err)
	}
	if !strings.Contains(err.Error(), "returned status 403") {
		t.Errorf("текст ошибки должен остаться прежним: %v", err)
	}
}

func TestClient_GetNodesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/bandwidth-stats/nodes/usage" {
			t.Errorf("неожиданный запрос %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("start") != "2026-11-12" || q.Get("end") != "2026-11-12" || q.Get("minTotalBytes") != "3221225472" {
			t.Errorf("неверные параметры: %s", r.URL.RawQuery)
		}
		var body NodesUsageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.NodesUUIDs) != 2 {
			t.Errorf("тело запроса: %+v, %v", body, err)
		}
		w.Write([]byte(`{"response":{"nodes":[{"uuid":"n1","users":[{"id":42,"totalBytes":10737418240}]},{"uuid":"n2","users":[]}]}}`))
	}))
	defer srv.Close()

	nodes, err := fastClient(srv.URL).GetNodesUsage(context.Background(), []string{"n1", "n2"}, "2026-11-12", 3<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Users[0].ID != 42 || nodes[0].Users[0].TotalBytes != 10<<30 {
		t.Errorf("ответ разобран неверно: %+v", nodes)
	}
}

func TestClient_GetNodesIncludesDisconnected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":[{"uuid":"a","isConnected":true},{"uuid":"b","isConnected":false,"isDisabled":true}]}`))
	}))
	defer srv.Close()
	c := fastClient(srv.URL)
	all, err := c.GetNodes(context.Background())
	if err != nil || len(all) != 2 {
		t.Fatalf("GetNodes должен вернуть все ноды: %v, %v", all, err)
	}
	active, err := c.GetActiveNodes(context.Background())
	if err != nil || len(active) != 1 || active[0].UUID != "a" {
		t.Errorf("GetActiveNodes по-прежнему только подключённые: %v, %v", active, err)
	}
}

func TestParseHeaders(t *testing.T) {
	const ua = "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/115.0"
	got := ParseHeaders("User-Agent: " + ua + "; X-Api-Key: secret123 ;Cookie: a=1; b=2; X-Note: one\\; two")
	want := map[string]string{
		"User-Agent": ua,
		"X-Api-Key":  "secret123",
		"Cookie":     "a=1; b=2",
		"X-Note":     "one; two",
	}
	if len(got) != len(want) {
		t.Fatalf("заголовков %d, ожидалось %d: %v", len(got), len(want), got)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, ожидалось %q", name, got[name], value)
		}
	}
	if ParseHeaders("") != nil || ParseHeaders(" ; ") != nil {
		t.Error("пустая строка не должна давать заголовков")
	}
}

func TestClient_RedirectDoesNotTurnPostIntoGet(t *testing.T) {
	var methods []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users/42/actions/disable", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved", http.StatusFound)
	})
	mux.HandleFunc("/api/users/43/actions/disable", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved", http.StatusPermanentRedirect)
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(srv.URL, "test-token")

	err := c.DisableUser(context.Background(), 42)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusFound {
		t.Fatalf("302 на POST должен вернуться как ошибка со статусом 302, получено: %v", err)
	}
	if len(methods) != 0 {
		t.Fatalf("POST не должен превращаться в GET: %v", methods)
	}

	if err := c.DisableUser(context.Background(), 43); err != nil {
		t.Fatalf("308 сохраняет метод и должен выполняться: %v", err)
	}
	if len(methods) != 1 || methods[0] != http.MethodPost {
		t.Errorf("после 308 ожидался один POST, получено %v", methods)
	}
}

func TestIsAlreadyDisabledAndEnabled(t *testing.T) {
	disabled := fmt.Errorf("disable user 7: %w", &StatusError{Method: "POST", Path: "/x", Code: 400, Body: `{"message":"User already disabled","errorCode":"A029"}`})
	enabled := &StatusError{Method: "POST", Path: "/x", Code: 400, Body: `{"message":"User already enabled","errorCode":"A030"}`}
	if !IsAlreadyDisabled(disabled) || IsAlreadyEnabled(disabled) {
		t.Error("A029 — это «уже отключён»")
	}
	if !IsAlreadyEnabled(enabled) || IsAlreadyDisabled(enabled) {
		t.Error("A030 — это «уже включён»")
	}
	other := &StatusError{Method: "POST", Path: "/x", Code: 404, Body: `{"errorCode":"A029"}`}
	if IsAlreadyDisabled(other) || IsAlreadyDisabled(errors.New("A029")) || IsAlreadyDisabled(nil) {
		t.Error("другие ошибки не должны считаться «уже отключён»")
	}
}

func TestClient_LongRetryAfterStopsRetries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	started := time.Now()
	_, err := fastClient(srv.URL).GetActiveNodes(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusTooManyRequests {
		t.Fatalf("ожидалась ошибка 429, получено %v", err)
	}
	if calls != 1 {
		t.Errorf("при Retry-After больше предела повторов быть не должно, запросов %d", calls)
	}
	if time.Since(started) > 5*time.Second {
		t.Errorf("клиент не должен ждать внутри окна лимита: %v", time.Since(started))
	}
}

func TestClient_RetryAfterIsNotShortened(t *testing.T) {
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		if len(times) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"response":[]}`))
	}))
	defer srv.Close()

	if _, err := fastClient(srv.URL).GetActiveNodes(context.Background()); err != nil {
		t.Fatalf("expected success after 429 retry, got %v", err)
	}
	if len(times) != 2 || times[1].Sub(times[0]) < time.Second {
		t.Errorf("повтор должен уйти не раньше, чем через Retry-After: %v", times)
	}
}

func TestRetryAfter_HugeValueDoesNotOverflow(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "18446744074")
	if d := retryAfter(h); d != maxRetryAfter {
		t.Errorf("retryAfter = %v, ожидалось %v", d, maxRetryAfter)
	}
}
