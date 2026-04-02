package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_GetNodes(t *testing.T) {
	// 3 nodes: one connected+enabled, one connected+disabled, one disconnected+enabled
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

func TestClient_GetUserByID(t *testing.T) {
	email := "user@example.com"
	telegramID := int64(12345)
	hwidLimit := 3

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/by-id/user-123" {
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
				UUID:            "uuid-abc",
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
	user, err := client.GetUserByID(context.Background(), "user-123")
	if err != nil {
		t.Fatalf("GetUserByID returned error: %v", err)
	}

	if user.UUID != "uuid-abc" {
		t.Errorf("expected uuid-abc, got %s", user.UUID)
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
		if r.URL.Path != "/api/users/uuid-abc/actions/disable" {
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
	err := client.DisableUser(context.Background(), "uuid-abc")
	if err != nil {
		t.Fatalf("DisableUser returned error: %v", err)
	}
}

func TestClient_EnableUser(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/uuid-abc/actions/enable" {
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
	err := client.EnableUser(context.Background(), "uuid-abc")
	if err != nil {
		t.Fatalf("EnableUser returned error: %v", err)
	}
}

func TestClient_DropConnections(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ip-control/drop-connections" {
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

		if req.DropBy.By != "userUuids" {
			t.Errorf("expected dropBy.by=userUuids, got %s", req.DropBy.By)
		}
		if len(req.DropBy.UserUUIDs) != 2 {
			t.Errorf("expected 2 userUuids, got %d", len(req.DropBy.UserUUIDs))
		}
		if req.TargetNodes.Target != "allNodes" {
			t.Errorf("expected targetNodes.target=allNodes, got %s", req.TargetNodes.Target)
		}

		resp := DropConnectionsResponse{}
		resp.Response.EventSent = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	err := client.DropConnections(context.Background(), []string{"uuid-1", "uuid-2"})
	if err != nil {
		t.Fatalf("DropConnections returned error: %v", err)
	}
}
