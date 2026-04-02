# Centralized Remnawave Limiter — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite remnawave-limiter from a per-node log-parsing system into a centralized API-driven service that monitors IP connections across all Remnawave nodes and reacts to device limit violations via Telegram bot.

**Architecture:** Single Go binary + Redis, deployed via Docker Compose. Polls Remnawave API for user IPs across all nodes, aggregates per-user, compares against hwidDeviceLimit (cached in Redis). Two action modes: manual (Telegram alerts with action buttons) and auto (disable subscription + timed restore). No fail2ban, no log parsing, no per-node install.

**Tech Stack:** Go 1.26, Redis (valkey:8.1-alpine), Docker Compose, Telegram Bot API (long polling), Remnawave REST API (Bearer JWT auth).

**Spec:** `docs/superpowers/specs/2026-04-02-centralized-limiter-design.md`

---

## File Structure

```
remnawave-limiter/
├── cmd/limiter/main.go                  → Entry point: load config, init components, start
├── internal/
│   ├── config/config.go                 → Load .env, Config struct, validation
│   ├── api/client.go                    → Remnawave API HTTP client (auth, retry, job polling)
│   ├── api/types.go                     → API request/response types
│   ├── monitor/monitor.go               → Main loop: fetch IPs, aggregate, check limits, react
│   ├── cache/cache.go                   → Redis wrapper: user cache, cooldowns, whitelist, restore timers
│   ├── telegram/bot.go                  → Telegram bot: long polling, callback handling
│   ├── telegram/messages.go             → Message formatting, inline keyboards
│   └── version/version.go              → Version constant
├── docker-compose.yml
├── Dockerfile
├── .env.example
├── Makefile
└── go.mod
```

**Removed files (old architecture):**
- `cmd/limiter-cli/` — entire directory
- `internal/parser/` — entire directory
- `internal/limiter/` — entire directory (limiter.go, ban_watcher.go, webhook.go)
- `pkg/logger/` — entire directory (Docker logs to stdout, use logrus directly)

---

### Task 1: Clean up old code, update go.mod

**Files:**
- Delete: `cmd/limiter-cli/main.go`
- Delete: `internal/parser/parser.go`
- Delete: `internal/limiter/limiter.go`
- Delete: `internal/limiter/ban_watcher.go`
- Delete: `internal/limiter/webhook.go`
- Delete: `pkg/logger/logger.go`
- Modify: `go.mod`
- Modify: `internal/version/version.go`

- [ ] **Step 1: Delete old files**

```bash
rm -rf cmd/limiter-cli/
rm -rf internal/parser/
rm -rf internal/limiter/
rm -rf pkg/
```

- [ ] **Step 2: Add Redis and Telegram dependencies to go.mod**

```bash
go get github.com/redis/go-redis/v9@latest
go get github.com/go-telegram-bot-api/telegram-bot-api/v5@latest
```

- [ ] **Step 3: Remove unused dependency**

```bash
go mod tidy
```

- [ ] **Step 4: Bump version**

Update `internal/version/version.go`:

```go
package version

const Version = "2.0.0"
```

- [ ] **Step 5: Verify go.mod is clean**

```bash
go mod tidy
cat go.mod
```

Expected: `go-redis`, `telegram-bot-api`, `godotenv`, `logrus` present. No unused deps.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: remove old per-node architecture, add redis and telegram deps"
```

---

### Task 2: Config

**Files:**
- Rewrite: `internal/config/config.go`
- Create: `.env.example`

- [ ] **Step 1: Write config tests**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear any existing env vars
	envVars := []string{
		"REMNAWAVE_API_URL", "REMNAWAVE_API_TOKEN",
		"CHECK_INTERVAL", "ACTIVE_IP_WINDOW", "TOLERANCE", "COOLDOWN",
		"USER_CACHE_TTL", "DEFAULT_DEVICE_LIMIT",
		"ACTION_MODE", "AUTO_DISABLE_DURATION",
		"TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID", "TELEGRAM_THREAD_ID", "TELEGRAM_ADMIN_IDS",
		"WHITELIST_USER_IDS", "REDIS_URL", "TIMEZONE",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}

	// Set required vars
	os.Setenv("REMNAWAVE_API_URL", "https://panel.example.com")
	os.Setenv("REMNAWAVE_API_TOKEN", "test-token")
	os.Setenv("TELEGRAM_BOT_TOKEN", "123:ABC")
	os.Setenv("TELEGRAM_CHAT_ID", "-100123")
	os.Setenv("TELEGRAM_ADMIN_IDS", "111,222")
	defer func() {
		for _, v := range envVars {
			os.Unsetenv(v)
		}
	}()

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.CheckInterval != 30 {
		t.Errorf("CheckInterval = %d, want 30", cfg.CheckInterval)
	}
	if cfg.ActiveIPWindow != 300 {
		t.Errorf("ActiveIPWindow = %d, want 300", cfg.ActiveIPWindow)
	}
	if cfg.Tolerance != 0 {
		t.Errorf("Tolerance = %d, want 0", cfg.Tolerance)
	}
	if cfg.Cooldown != 300 {
		t.Errorf("Cooldown = %d, want 300", cfg.Cooldown)
	}
	if cfg.UserCacheTTL != 600 {
		t.Errorf("UserCacheTTL = %d, want 600", cfg.UserCacheTTL)
	}
	if cfg.DefaultDeviceLimit != 0 {
		t.Errorf("DefaultDeviceLimit = %d, want 0", cfg.DefaultDeviceLimit)
	}
	if cfg.ActionMode != "manual" {
		t.Errorf("ActionMode = %s, want manual", cfg.ActionMode)
	}
	if cfg.AutoDisableDuration != 0 {
		t.Errorf("AutoDisableDuration = %d, want 0", cfg.AutoDisableDuration)
	}
	if cfg.RedisURL != "redis://redis:6379" {
		t.Errorf("RedisURL = %s, want redis://redis:6379", cfg.RedisURL)
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("Timezone = %s, want UTC", cfg.Timezone)
	}
}

func TestLoadConfig_Validation_MissingRequired(t *testing.T) {
	os.Clearenv()

	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}
}

func TestLoadConfig_Validation_InvalidActionMode(t *testing.T) {
	os.Setenv("REMNAWAVE_API_URL", "https://panel.example.com")
	os.Setenv("REMNAWAVE_API_TOKEN", "test-token")
	os.Setenv("TELEGRAM_BOT_TOKEN", "123:ABC")
	os.Setenv("TELEGRAM_CHAT_ID", "-100123")
	os.Setenv("TELEGRAM_ADMIN_IDS", "111")
	os.Setenv("ACTION_MODE", "invalid")
	defer os.Clearenv()

	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected error for invalid ACTION_MODE")
	}
}

func TestParseInt64List(t *testing.T) {
	result := parseInt64List("111,222,333")
	if len(result) != 3 {
		t.Fatalf("len = %d, want 3", len(result))
	}
	if result[0] != 111 || result[1] != 222 || result[2] != 333 {
		t.Errorf("got %v, want [111 222 333]", result)
	}
}

func TestParseInt64List_Empty(t *testing.T) {
	result := parseInt64List("")
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/config/
```

Expected: FAIL — functions not defined.

- [ ] **Step 3: Implement config**

Rewrite `internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	// API
	RemnawaveAPIURL   string
	RemnawaveAPIToken string

	// Monitoring
	CheckInterval      int
	ActiveIPWindow     int
	Tolerance          int
	Cooldown           int
	UserCacheTTL       int
	DefaultDeviceLimit int

	// Action
	ActionMode           string
	AutoDisableDuration  int

	// Telegram
	TelegramBotToken string
	TelegramChatID   int64
	TelegramThreadID int64
	TelegramAdminIDs []int64

	// Whitelist
	WhitelistUserIDs []string

	// Redis
	RedisURL string

	// Timezone
	Timezone string
}

func LoadConfig(envPath string) (*Config, error) {
	if envPath != "" {
		_ = godotenv.Load(envPath)
	} else {
		_ = godotenv.Load()
	}

	cfg := &Config{
		RemnawaveAPIURL:   getEnv("REMNAWAVE_API_URL", ""),
		RemnawaveAPIToken: getEnv("REMNAWAVE_API_TOKEN", ""),

		CheckInterval:      getEnvInt("CHECK_INTERVAL", 30),
		ActiveIPWindow:     getEnvInt("ACTIVE_IP_WINDOW", 300),
		Tolerance:          getEnvInt("TOLERANCE", 0),
		Cooldown:           getEnvInt("COOLDOWN", 300),
		UserCacheTTL:       getEnvInt("USER_CACHE_TTL", 600),
		DefaultDeviceLimit: getEnvInt("DEFAULT_DEVICE_LIMIT", 0),

		ActionMode:          getEnv("ACTION_MODE", "manual"),
		AutoDisableDuration: getEnvInt("AUTO_DISABLE_DURATION", 0),

		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   getEnvInt64("TELEGRAM_CHAT_ID", 0),
		TelegramThreadID: getEnvInt64("TELEGRAM_THREAD_ID", 0),
		TelegramAdminIDs: parseInt64List(getEnv("TELEGRAM_ADMIN_IDS", "")),

		WhitelistUserIDs: parseStringList(getEnv("WHITELIST_USER_IDS", "")),

		RedisURL: getEnv("REDIS_URL", "redis://redis:6379"),

		Timezone: getEnv("TIMEZONE", "UTC"),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) Validate() error {
	if cfg.RemnawaveAPIURL == "" {
		return fmt.Errorf("REMNAWAVE_API_URL is required")
	}
	if cfg.RemnawaveAPIToken == "" {
		return fmt.Errorf("REMNAWAVE_API_TOKEN is required")
	}
	if cfg.TelegramBotToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if cfg.TelegramChatID == 0 {
		return fmt.Errorf("TELEGRAM_CHAT_ID is required")
	}
	if len(cfg.TelegramAdminIDs) == 0 {
		return fmt.Errorf("TELEGRAM_ADMIN_IDS is required")
	}
	if cfg.ActionMode != "manual" && cfg.ActionMode != "auto" {
		return fmt.Errorf("ACTION_MODE must be 'manual' or 'auto', got '%s'", cfg.ActionMode)
	}
	if cfg.CheckInterval <= 0 {
		return fmt.Errorf("CHECK_INTERVAL must be > 0")
	}
	if cfg.ActiveIPWindow <= 0 {
		return fmt.Errorf("ACTIVE_IP_WINDOW must be > 0")
	}
	if cfg.Cooldown <= 0 {
		return fmt.Errorf("COOLDOWN must be > 0")
	}
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func parseInt64List(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if v, err := strconv.ParseInt(p, 10, 64); err == nil {
			result = append(result, v)
		}
	}
	return result
}

func parseStringList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/config/
```

Expected: PASS.

- [ ] **Step 5: Create .env.example**

Create `.env.example`:

```env
# === Remnawave API ===
REMNAWAVE_API_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=your-api-token-here

# === Monitoring ===
CHECK_INTERVAL=30              # Interval between checks (seconds)
ACTIVE_IP_WINDOW=300           # IP is "active" if lastSeen < this many seconds ago
TOLERANCE=0                    # Extra IPs allowed above limit before reaction
COOLDOWN=300                   # Cooldown between alerts for same user (seconds)
USER_CACHE_TTL=600             # How long to cache user data from API (seconds)
DEFAULT_DEVICE_LIMIT=0         # Fallback limit if user has no hwidDeviceLimit (0 = skip)

# === Action Mode ===
ACTION_MODE=manual             # "manual" = alert with buttons, "auto" = auto-disable
AUTO_DISABLE_DURATION=0        # Minutes to disable subscription (0 = permanent). Auto mode only.

# === Telegram ===
TELEGRAM_BOT_TOKEN=123456:ABC-DEF
TELEGRAM_CHAT_ID=-1001234567890
TELEGRAM_THREAD_ID=              # Optional: topic/thread ID in supergroup
TELEGRAM_ADMIN_IDS=123456789,987654321

# === Whitelist ===
WHITELIST_USER_IDS=              # Comma-separated user IDs to skip

# === Redis ===
REDIS_URL=redis://redis:6379

# === Timezone ===
TIMEZONE=UTC                   # For timestamps in alerts (e.g. Europe/Moscow)
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go .env.example
git commit -m "feat: rewrite config for centralized architecture"
```

---

### Task 3: API Types

**Files:**
- Create: `internal/api/types.go`

- [ ] **Step 1: Create API types**

Create `internal/api/types.go`:

```go
package api

import "time"

// --- Nodes ---

type NodesResponse struct {
	Response []Node `json:"response"`
}

type Node struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	IsConnected bool   `json:"isConnected"`
	IsDisabled  bool   `json:"isDisabled"`
	CountryCode string `json:"countryCode"`
}

// --- Fetch Users IPs (job-based) ---

type JobResponse struct {
	Response struct {
		JobID string `json:"jobId"`
	} `json:"response"`
}

type UsersIPsResultResponse struct {
	Response struct {
		IsCompleted bool `json:"isCompleted"`
		IsFailed    bool `json:"isFailed"`
		Result      *UsersIPsResult `json:"result"`
	} `json:"response"`
}

type UsersIPsResult struct {
	Success  bool          `json:"success"`
	NodeUUID string        `json:"nodeUuid"`
	Users    []UserIPEntry `json:"users"`
}

type UserIPEntry struct {
	UserID string   `json:"userId"`
	IPs    []IPInfo `json:"ips"`
}

type IPInfo struct {
	IP       string    `json:"ip"`
	LastSeen time.Time `json:"lastSeen"`
}

// --- User ---

type UserResponse struct {
	Response UserData `json:"response"`
}

type UserData struct {
	UUID             string  `json:"uuid"`
	ID               int     `json:"id"`
	Username         string  `json:"username"`
	Status           string  `json:"status"`
	Email            *string `json:"email"`
	TelegramID       *int64  `json:"telegramId"`
	HWIDDeviceLimit  *int    `json:"hwidDeviceLimit"`
	SubscriptionURL  string  `json:"subscriptionUrl,omitempty"`
}

// --- Drop Connections ---

type DropConnectionsRequest struct {
	DropBy      DropBy      `json:"dropBy"`
	TargetNodes TargetNodes `json:"targetNodes"`
}

type DropBy struct {
	By        string   `json:"by"`
	UserUUIDs []string `json:"userUuids,omitempty"`
}

type TargetNodes struct {
	Target string `json:"target"`
}

type DropConnectionsResponse struct {
	Response struct {
		EventSent bool `json:"eventSent"`
	} `json:"response"`
}

// --- Aggregated data (internal, not API) ---

type UserIPAggregated struct {
	UserID    string
	ActiveIPs []ActiveIP
}

type ActiveIP struct {
	IP       string
	LastSeen time.Time
	NodeName string
	NodeUUID string
}

// --- Cached user info ---

type CachedUser struct {
	UUID            string `json:"uuid"`
	UserID          string `json:"user_id"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	TelegramID      int64  `json:"telegram_id"`
	HWIDDeviceLimit int    `json:"hwid_device_limit"` // -1 = null (use default), 0 = unlimited
	Status          string `json:"status"`
	SubscriptionURL string `json:"subscription_url"`
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/api/
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/api/types.go
git commit -m "feat: add Remnawave API types"
```

---

### Task 4: API Client

**Files:**
- Create: `internal/api/client.go`
- Create: `internal/api/client_test.go`

- [ ] **Step 1: Write API client tests**

Create `internal/api/client_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_GetNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing auth header")
		}

		resp := NodesResponse{
			Response: []Node{
				{UUID: "node-1", Name: "yandex-1", IsConnected: true, IsDisabled: false},
				{UUID: "node-2", Name: "germany-1", IsConnected: false, IsDisabled: false},
				{UUID: "node-3", Name: "disabled-1", IsConnected: true, IsDisabled: true},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	nodes, err := client.GetActiveNodes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("expected 1 active node, got %d", len(nodes))
	}
	if nodes[0].UUID != "node-1" {
		t.Errorf("expected node-1, got %s", nodes[0].UUID)
	}
}

func TestClient_GetUserByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/by-id/1234" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		limit := 3
		email := "test@vpn.com"
		telegramID := int64(999)
		resp := UserResponse{
			Response: UserData{
				UUID:            "uuid-1234",
				ID:              1234,
				Username:        "testuser",
				Status:          "ACTIVE",
				Email:           &email,
				TelegramID:      &telegramID,
				HWIDDeviceLimit: &limit,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	user, err := client.GetUserByID(context.Background(), "1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if user.UUID != "uuid-1234" {
		t.Errorf("UUID = %s, want uuid-1234", user.UUID)
	}
	if *user.HWIDDeviceLimit != 3 {
		t.Errorf("HWIDDeviceLimit = %d, want 3", *user.HWIDDeviceLimit)
	}
}

func TestClient_DisableUser(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/uuid-123/actions/disable" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		called = true
		w.WriteHeader(200)
		w.Write([]byte(`{"response":{}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	err := client.DisableUser(context.Background(), "uuid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("disable endpoint was not called")
	}
}

func TestClient_EnableUser(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/uuid-123/actions/enable" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		called = true
		w.WriteHeader(200)
		w.Write([]byte(`{"response":{}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	err := client.EnableUser(context.Background(), "uuid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("enable endpoint was not called")
	}
}

func TestClient_DropConnections(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ip-control/drop-connections" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		called = true
		resp := DropConnectionsResponse{}
		resp.Response.EventSent = true
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	err := client.DropConnections(context.Background(), []string{"uuid-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("drop-connections was not called")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/api/
```

Expected: FAIL — `NewClient` not defined.

- [ ] **Step 3: Implement API client**

Create `internal/api/client.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"bytes"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	maxRetries       = 3
	jobPollInterval  = 1 * time.Second
	jobPollMaxTries  = 30
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *logrus.Logger
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger: logrus.StandardLogger(),
	}
}

func (c *Client) SetLogger(logger *logrus.Logger) {
	c.logger = logger
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")

		// Reset body reader for retry
		if body != nil {
			data, _ := json.Marshal(body)
			req.Body = io.NopCloser(bytes.NewReader(data))
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			c.logger.WithFields(logrus.Fields{
				"attempt": attempt + 1,
				"path":    path,
				"error":   err,
			}).Debug("API request failed, retrying")
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("API returned status %d", resp.StatusCode)
			continue
		}

		if resp.StatusCode >= 400 {
			return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
		}

		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}

		if result != nil {
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("API request failed after %d retries: %w", maxRetries, lastErr)
}

func (c *Client) GetActiveNodes(ctx context.Context) ([]Node, error) {
	var resp NodesResponse
	if err := c.doRequest(ctx, "GET", "/api/nodes", nil, &resp); err != nil {
		return nil, err
	}

	active := make([]Node, 0)
	for _, node := range resp.Response {
		if node.IsConnected && !node.IsDisabled {
			active = append(active, node)
		}
	}
	return active, nil
}

func (c *Client) FetchUsersIPs(ctx context.Context, nodeUUID string) ([]UserIPEntry, error) {
	// Step 1: Create job
	var jobResp JobResponse
	if err := c.doRequest(ctx, "POST", "/api/ip-control/fetch-users-ips/"+nodeUUID, nil, &jobResp); err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}

	jobID := jobResp.Response.JobID

	// Step 2: Poll for result
	for i := 0; i < jobPollMaxTries; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(jobPollInterval):
		}

		var result UsersIPsResultResponse
		if err := c.doRequest(ctx, "GET", "/api/ip-control/fetch-users-ips/result/"+jobID, nil, &result); err != nil {
			return nil, fmt.Errorf("poll job: %w", err)
		}

		if result.Response.IsFailed {
			return nil, fmt.Errorf("job %s failed", jobID)
		}

		if result.Response.IsCompleted && result.Response.Result != nil {
			return result.Response.Result.Users, nil
		}
	}

	return nil, fmt.Errorf("job %s timed out after %d polls", jobID, jobPollMaxTries)
}

func (c *Client) GetUserByID(ctx context.Context, id string) (*UserData, error) {
	var resp UserResponse
	if err := c.doRequest(ctx, "GET", "/api/users/by-id/"+id, nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Response, nil
}

func (c *Client) DisableUser(ctx context.Context, uuid string) error {
	return c.doRequest(ctx, "POST", "/api/users/"+uuid+"/actions/disable", nil, nil)
}

func (c *Client) EnableUser(ctx context.Context, uuid string) error {
	return c.doRequest(ctx, "POST", "/api/users/"+uuid+"/actions/enable", nil, nil)
}

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
	return c.doRequest(ctx, "POST", "/api/ip-control/drop-connections", req, nil)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/api/
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/client.go internal/api/client_test.go
git commit -m "feat: add Remnawave API client with retry and job polling"
```

---

### Task 5: Redis Cache

**Files:**
- Create: `internal/cache/cache.go`
- Create: `internal/cache/cache_test.go`

- [ ] **Step 1: Write cache tests**

Create `internal/cache/cache_test.go`:

```go
package cache

import (
	"context"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
)

// These tests require a running Redis on localhost:6379.
// Skip in CI if not available.

func skipIfNoRedis(t *testing.T, c *Cache) {
	t.Helper()
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
}

func testCache(t *testing.T) *Cache {
	t.Helper()
	c, err := New("redis://localhost:6379/15") // Use db 15 for tests
	if err != nil {
		t.Skipf("Cannot connect to Redis: %v", err)
	}
	skipIfNoRedis(t, c)

	// Clean test db
	ctx := context.Background()
	c.client.FlushDB(ctx)
	return c
}

func TestCache_UserData(t *testing.T) {
	c := testCache(t)
	ctx := context.Background()

	user := &api.CachedUser{
		UUID:            "uuid-1",
		UserID:          "1234",
		Username:        "testuser",
		Email:           "test@vpn.com",
		TelegramID:      999,
		HWIDDeviceLimit: 3,
		Status:          "ACTIVE",
	}

	err := c.SetUser(ctx, "1234", user, 60*time.Second)
	if err != nil {
		t.Fatalf("SetUser: %v", err)
	}

	got, err := c.GetUser(ctx, "1234")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got == nil {
		t.Fatal("GetUser returned nil")
	}
	if got.UUID != "uuid-1" {
		t.Errorf("UUID = %s, want uuid-1", got.UUID)
	}
	if got.HWIDDeviceLimit != 3 {
		t.Errorf("HWIDDeviceLimit = %d, want 3", got.HWIDDeviceLimit)
	}
}

func TestCache_Cooldown(t *testing.T) {
	c := testCache(t)
	ctx := context.Background()

	active, err := c.IsCooldownActive(ctx, "user-1")
	if err != nil {
		t.Fatalf("IsCooldownActive: %v", err)
	}
	if active {
		t.Error("expected no cooldown initially")
	}

	err = c.SetCooldown(ctx, "user-1", 60*time.Second)
	if err != nil {
		t.Fatalf("SetCooldown: %v", err)
	}

	active, err = c.IsCooldownActive(ctx, "user-1")
	if err != nil {
		t.Fatalf("IsCooldownActive: %v", err)
	}
	if !active {
		t.Error("expected cooldown to be active")
	}
}

func TestCache_Whitelist(t *testing.T) {
	c := testCache(t)
	ctx := context.Background()

	is, _ := c.IsWhitelisted(ctx, "user-1")
	if is {
		t.Error("expected not whitelisted initially")
	}

	c.AddToWhitelist(ctx, "user-1")

	is, _ = c.IsWhitelisted(ctx, "user-1")
	if !is {
		t.Error("expected whitelisted after add")
	}
}

func TestCache_RestoreTimer(t *testing.T) {
	c := testCache(t)
	ctx := context.Background()

	err := c.SetRestoreTimer(ctx, "uuid-1", 1*time.Second)
	if err != nil {
		t.Fatalf("SetRestoreTimer: %v", err)
	}

	uuids, err := c.GetExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("GetExpiredRestoreTimers: %v", err)
	}
	if len(uuids) != 0 {
		t.Errorf("expected 0 expired timers, got %d", len(uuids))
	}

	time.Sleep(1500 * time.Millisecond)

	uuids, err = c.GetExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("GetExpiredRestoreTimers: %v", err)
	}
	if len(uuids) != 1 || uuids[0] != "uuid-1" {
		t.Errorf("expected [uuid-1], got %v", uuids)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/cache/
```

Expected: FAIL — `New` not defined.

- [ ] **Step 3: Implement cache**

Create `internal/cache/cache.go`:

```go
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/remnawave/limiter/internal/api"
)

const (
	prefixUser     = "user:"
	prefixCooldown = "cooldown:"
	prefixRestore  = "restore:"
	keyWhitelist   = "whitelist"
)

type Cache struct {
	client *redis.Client
}

func New(redisURL string) (*Cache, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}
	client := redis.NewClient(opts)
	return &Cache{client: client}, nil
}

func (c *Cache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *Cache) Close() error {
	return c.client.Close()
}

// --- User cache ---

func (c *Cache) SetUser(ctx context.Context, userID string, user *api.CachedUser, ttl time.Duration) error {
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, prefixUser+userID, data, ttl).Err()
}

func (c *Cache) GetUser(ctx context.Context, userID string) (*api.CachedUser, error) {
	data, err := c.client.Get(ctx, prefixUser+userID).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var user api.CachedUser
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// --- Cooldown ---

func (c *Cache) SetCooldown(ctx context.Context, userID string, ttl time.Duration) error {
	return c.client.Set(ctx, prefixCooldown+userID, "1", ttl).Err()
}

func (c *Cache) IsCooldownActive(ctx context.Context, userID string) (bool, error) {
	exists, err := c.client.Exists(ctx, prefixCooldown+userID).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

// --- Whitelist ---

func (c *Cache) AddToWhitelist(ctx context.Context, userID string) error {
	return c.client.SAdd(ctx, keyWhitelist, userID).Err()
}

func (c *Cache) RemoveFromWhitelist(ctx context.Context, userID string) error {
	return c.client.SRem(ctx, keyWhitelist, userID).Err()
}

func (c *Cache) IsWhitelisted(ctx context.Context, userID string) (bool, error) {
	return c.client.SIsMember(ctx, keyWhitelist, userID).Result()
}

// --- Restore timers (auto mode) ---

// SetRestoreTimer stores a restore marker. We use a sorted set with score = Unix timestamp of expiry.
func (c *Cache) SetRestoreTimer(ctx context.Context, uuid string, duration time.Duration) error {
	expiry := time.Now().Add(duration).Unix()
	return c.client.ZAdd(ctx, prefixRestore+"queue", redis.Z{
		Score:  float64(expiry),
		Member: uuid,
	}).Err()
}

// GetExpiredRestoreTimers returns UUIDs whose restore timer has expired.
func (c *Cache) GetExpiredRestoreTimers(ctx context.Context) ([]string, error) {
	now := float64(time.Now().Unix())
	results, err := c.client.ZRangeByScore(ctx, prefixRestore+"queue", &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%f", now),
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(results) > 0 {
		// Remove processed entries
		c.client.ZRemRangeByScore(ctx, prefixRestore+"queue", "-inf", fmt.Sprintf("%f", now))
	}
	return results, nil
}

// --- Init whitelist from config ---

func (c *Cache) InitWhitelist(ctx context.Context, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	members := make([]interface{}, len(userIDs))
	for i, id := range userIDs {
		members[i] = strings.TrimSpace(id)
	}
	return c.client.SAdd(ctx, keyWhitelist, members...).Err()
}
```

- [ ] **Step 4: Run tests (skip if no Redis available)**

```bash
go test -v ./internal/cache/
```

Expected: PASS (or SKIP if no local Redis).

- [ ] **Step 5: Commit**

```bash
git add internal/cache/cache.go internal/cache/cache_test.go
git commit -m "feat: add Redis cache for users, cooldowns, whitelist, restore timers"
```

---

### Task 6: Telegram Messages

**Files:**
- Create: `internal/telegram/messages.go`
- Create: `internal/telegram/messages_test.go`

- [ ] **Step 1: Write message formatting tests**

Create `internal/telegram/messages_test.go`:

```go
package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/api"
)

func TestFormatManualAlert(t *testing.T) {
	user := &api.CachedUser{
		UUID:            "uuid-1",
		UserID:          "1234",
		Username:        "testuser",
		Email:           "test@vpn.com",
		HWIDDeviceLimit: 3,
		SubscriptionURL: "https://panel.example.com/sub/abc",
	}

	ips := []api.ActiveIP{
		{IP: "1.2.3.4", NodeName: "yandex-1"},
		{IP: "5.6.7.8", NodeName: "germany-2"},
		{IP: "9.10.11.12", NodeName: "yandex-1"},
		{IP: "13.14.15.16", NodeName: "finland-1"},
	}

	loc, _ := time.LoadLocation("Europe/Moscow")
	msg := FormatManualAlert(user, ips, 3, loc)

	if !strings.Contains(msg, "testuser") {
		t.Error("missing username")
	}
	if !strings.Contains(msg, "test@vpn.com") {
		t.Error("missing email")
	}
	if !strings.Contains(msg, "3") {
		t.Error("missing limit")
	}
	if !strings.Contains(msg, "4 IP") {
		t.Error("missing IP count")
	}
	if !strings.Contains(msg, "1.2.3.4") {
		t.Error("missing IP address")
	}
	if !strings.Contains(msg, "yandex-1") {
		t.Error("missing node name")
	}
}

func TestFormatAutoAlert(t *testing.T) {
	user := &api.CachedUser{
		UUID:     "uuid-1",
		UserID:   "1234",
		Username: "testuser",
		Email:    "test@vpn.com",
	}

	ips := []api.ActiveIP{
		{IP: "1.2.3.4", NodeName: "yandex-1"},
	}

	loc := time.UTC
	msg := FormatAutoAlert(user, ips, 1, 30, loc)

	if !strings.Contains(msg, "автоматически отключена") {
		t.Error("missing auto disable text")
	}
	if !strings.Contains(msg, "30 мин") {
		t.Error("missing duration")
	}
}

func TestFormatAutoAlert_Permanent(t *testing.T) {
	user := &api.CachedUser{
		UUID:     "uuid-1",
		Username: "testuser",
		Email:    "test@vpn.com",
	}

	loc := time.UTC
	msg := FormatAutoAlert(user, nil, 1, 0, loc)

	if !strings.Contains(msg, "Перманентно") {
		t.Error("missing permanent text")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/telegram/
```

Expected: FAIL.

- [ ] **Step 3: Implement message formatting**

Create `internal/telegram/messages.go`:

```go
package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/remnawave/limiter/internal/api"
)

func FormatManualAlert(user *api.CachedUser, ips []api.ActiveIP, limit int, loc *time.Location) string {
	var b strings.Builder

	b.WriteString("⚠️ <b>Превышение лимита устройств</b>\n\n")
	b.WriteString(fmt.Sprintf("👤 Пользователь: <code>%s</code>\n", escapeHTML(user.Username)))
	if user.Email != "" {
		b.WriteString(fmt.Sprintf("🔑 Подписка: <code>%s</code>\n", escapeHTML(user.Email)))
	}
	b.WriteString(fmt.Sprintf("📊 Лимит: %d | Обнаружено: %d IP\n", limit, len(ips)))
	b.WriteString(fmt.Sprintf("🕐 %s\n", time.Now().In(loc).Format("2006-01-02 15:04:05 (MST)")))

	if len(ips) > 0 {
		b.WriteString("\n📍 IP-адреса:\n")
		for _, ip := range ips {
			b.WriteString(fmt.Sprintf("  • <code>%s</code> (нода: %s)\n", ip.IP, escapeHTML(ip.NodeName)))
		}
	}

	if user.SubscriptionURL != "" {
		b.WriteString(fmt.Sprintf("\n🔗 <a href=\"%s\">Профиль</a>", user.SubscriptionURL))
	}

	return b.String()
}

func FormatAutoAlert(user *api.CachedUser, ips []api.ActiveIP, limit int, durationMinutes int, loc *time.Location) string {
	var b strings.Builder

	b.WriteString("🔒 <b>Подписка автоматически отключена</b>\n\n")
	b.WriteString(fmt.Sprintf("👤 Пользователь: <code>%s</code>\n", escapeHTML(user.Username)))
	if user.Email != "" {
		b.WriteString(fmt.Sprintf("🔑 Подписка: <code>%s</code>\n", escapeHTML(user.Email)))
	}
	b.WriteString(fmt.Sprintf("📊 Лимит: %d | Обнаружено: %d IP\n", limit, len(ips)))

	if durationMinutes > 0 {
		b.WriteString(fmt.Sprintf("⏱ Отключена на: %d мин\n", durationMinutes))
	} else {
		b.WriteString("⏱ Отключена: Перманентно\n")
	}

	b.WriteString(fmt.Sprintf("🕐 %s\n", time.Now().In(loc).Format("2006-01-02 15:04:05 (MST)")))

	if len(ips) > 0 {
		b.WriteString("\n📍 IP-адреса:\n")
		for _, ip := range ips {
			b.WriteString(fmt.Sprintf("  • <code>%s</code> (нода: %s)\n", ip.IP, escapeHTML(ip.NodeName)))
		}
	}

	return b.String()
}

func FormatActionResult(action, adminName, username string) string {
	switch action {
	case "drop":
		return fmt.Sprintf("\n\n✅ Подключения сброшены (админ: %s)", adminName)
	case "disable":
		return fmt.Sprintf("\n\n🔒 Подписка отключена (админ: %s)", adminName)
	case "ignore":
		return fmt.Sprintf("\n\n🔇 Добавлен в whitelist (админ: %s)", adminName)
	case "enable":
		return fmt.Sprintf("\n\n🔓 Подписка включена (админ: %s)", adminName)
	default:
		return ""
	}
}

func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/telegram/
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/messages.go internal/telegram/messages_test.go
git commit -m "feat: add Telegram message formatting for alerts"
```

---

### Task 7: Telegram Bot

**Files:**
- Create: `internal/telegram/bot.go`

- [ ] **Step 1: Implement Telegram bot**

Create `internal/telegram/bot.go`:

```go
package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sirupsen/logrus"
)

type ActionHandler func(ctx context.Context, action, userUUID, userID string) error

type Bot struct {
	api          *tgbotapi.BotAPI
	chatID       int64
	threadID     int64
	adminIDs     map[int64]bool
	logger       *logrus.Logger
	onAction     ActionHandler
	mu           sync.Mutex
}

func NewBot(token string, chatID, threadID int64, adminIDs []int64, logger *logrus.Logger) (*Bot, error) {
	botAPI, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}

	admins := make(map[int64]bool, len(adminIDs))
	for _, id := range adminIDs {
		admins[id] = true
	}

	return &Bot{
		api:      botAPI,
		chatID:   chatID,
		threadID: threadID,
		adminIDs: admins,
		logger:   logger,
	}, nil
}

func (b *Bot) SetActionHandler(handler ActionHandler) {
	b.onAction = handler
}

func (b *Bot) SendManualAlert(text string, userUUID string, userID string) error {
	msg := tgbotapi.NewMessage(b.chatID, text)
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = true

	if b.threadID != 0 {
		msg.MessageThreadID = int(b.threadID)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Сбросить подключения", fmt.Sprintf("drop:%s:%s", userUUID, userID)),
			tgbotapi.NewInlineKeyboardButtonData("🔒 Отключить подписку", fmt.Sprintf("disable:%s:%s", userUUID, userID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔇 Игнорировать", fmt.Sprintf("ignore:%s:%s", userUUID, userID)),
		),
	)
	msg.ReplyMarkup = keyboard

	_, err := b.api.Send(msg)
	return err
}

func (b *Bot) SendAutoAlert(text string, userUUID string) error {
	msg := tgbotapi.NewMessage(b.chatID, text)
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = true

	if b.threadID != 0 {
		msg.MessageThreadID = int(b.threadID)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔓 Включить подписку", fmt.Sprintf("enable:%s:", userUUID)),
		),
	)
	msg.ReplyMarkup = keyboard

	_, err := b.api.Send(msg)
	return err
}

func (b *Bot) SendMessage(text string) error {
	msg := tgbotapi.NewMessage(b.chatID, text)
	msg.ParseMode = "HTML"

	if b.threadID != 0 {
		msg.MessageThreadID = int(b.threadID)
	}

	_, err := b.api.Send(msg)
	return err
}

func (b *Bot) StartPolling(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update := <-updates:
			if update.CallbackQuery == nil {
				continue
			}
			b.handleCallback(ctx, update.CallbackQuery)
		}
	}
}

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	// Check admin
	if !b.adminIDs[cb.From.ID] {
		callback := tgbotapi.NewCallback(cb.ID, "⛔ Нет доступа")
		b.api.Request(callback)
		return
	}

	// Parse callback data: "action:userUUID:userID"
	parts := strings.SplitN(cb.Data, ":", 3)
	if len(parts) < 2 {
		return
	}

	action := parts[0]
	userUUID := parts[1]
	userID := ""
	if len(parts) > 2 {
		userID = parts[2]
	}

	adminName := cb.From.FirstName
	if cb.From.UserName != "" {
		adminName = "@" + cb.From.UserName
	}

	// Execute action
	if b.onAction != nil {
		if err := b.onAction(ctx, action, userUUID, userID); err != nil {
			b.logger.WithError(err).WithField("action", action).Error("Callback action failed")
			callback := tgbotapi.NewCallback(cb.ID, "❌ Ошибка: "+err.Error())
			b.api.Request(callback)
			return
		}
	}

	// Update message: remove keyboard, append result
	resultText := FormatActionResult(action, adminName, "")
	newText := cb.Message.Text + resultText

	if cb.Message.IsCommand() || cb.Message.Text == "" {
		// HTML message — use caption or text from entities
		newText = getHTMLText(cb.Message) + resultText
	}

	edit := tgbotapi.NewEditMessageText(b.chatID, cb.Message.MessageID, newText)
	edit.ParseMode = "HTML"
	noKeyboard := tgbotapi.NewEditMessageReplyMarkup(b.chatID, cb.Message.MessageID, tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})

	b.api.Send(edit)
	b.api.Send(noKeyboard)

	callback := tgbotapi.NewCallback(cb.ID, "✅ Выполнено")
	b.api.Request(callback)
}

func getHTMLText(msg *tgbotapi.Message) string {
	if msg.Text != "" {
		return msg.Text
	}
	return ""
}

// GetAdminName returns display name for admin ID (for logging)
func GetAdminName(id int64) string {
	return strconv.FormatInt(id, 10)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/telegram/
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/telegram/bot.go
git commit -m "feat: add Telegram bot with inline keyboard callbacks"
```

---

### Task 8: Monitor (core loop)

**Files:**
- Create: `internal/monitor/monitor.go`

- [ ] **Step 1: Implement monitor**

Create `internal/monitor/monitor.go`:

```go
package monitor

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/telegram"
)

type Monitor struct {
	config   *config.Config
	api      *api.Client
	cache    *cache.Cache
	bot      *telegram.Bot
	logger   *logrus.Logger
	location *time.Location
}

func New(cfg *config.Config, apiClient *api.Client, cache *cache.Cache, bot *telegram.Bot, logger *logrus.Logger) (*Monitor, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}

	return &Monitor{
		config:   cfg,
		api:      apiClient,
		cache:    cache,
		bot:      bot,
		logger:   logger,
		location: loc,
	}, nil
}

func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.config.CheckInterval) * time.Second)
	defer ticker.Stop()

	// Restore timer checker (auto mode)
	if m.config.ActionMode == "auto" && m.config.AutoDisableDuration > 0 {
		go m.restoreLoop(ctx)
	}

	m.logger.Info("🚀 Monitor запущен")

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("👋 Monitor остановлен")
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

func (m *Monitor) check(ctx context.Context) {
	// Step 1: Get active nodes
	nodes, err := m.api.GetActiveNodes(ctx)
	if err != nil {
		m.logger.WithError(err).Error("Ошибка получения списка нод")
		return
	}

	if len(nodes) == 0 {
		m.logger.Debug("Нет активных нод")
		return
	}

	m.logger.WithField("nodes", len(nodes)).Debug("Опрос нод")

	// Step 2: Fetch IPs from all nodes in parallel
	type nodeResult struct {
		nodeName string
		nodeUUID string
		users    []api.UserIPEntry
	}

	results := make([]nodeResult, 0, len(nodes))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, node := range nodes {
		wg.Add(1)
		go func(n api.Node) {
			defer wg.Done()

			users, err := m.api.FetchUsersIPs(ctx, n.UUID)
			if err != nil {
				m.logger.WithFields(logrus.Fields{
					"node":  n.Name,
					"error": err,
				}).Warn("Ошибка получения IP для ноды")
				return
			}

			mu.Lock()
			results = append(results, nodeResult{
				nodeName: n.Name,
				nodeUUID: n.UUID,
				users:    users,
			})
			mu.Unlock()
		}(node)
	}
	wg.Wait()

	// Step 3: Aggregate IPs by userId across all nodes
	userIPs := make(map[string][]api.ActiveIP)
	now := time.Now()
	window := time.Duration(m.config.ActiveIPWindow) * time.Second

	for _, nr := range results {
		for _, u := range nr.users {
			for _, ip := range u.IPs {
				if now.Sub(ip.LastSeen) <= window {
					userIPs[u.UserID] = append(userIPs[u.UserID], api.ActiveIP{
						IP:       ip.IP,
						LastSeen: ip.LastSeen,
						NodeName: nr.nodeName,
						NodeUUID: nr.nodeUUID,
					})
				}
			}
		}
	}

	// Step 4: Check each user against limits
	for userID, ips := range userIPs {
		m.checkUser(ctx, userID, ips)
	}
}

func (m *Monitor) checkUser(ctx context.Context, userID string, activeIPs []api.ActiveIP) {
	// Deduplicate IPs (same IP from different checks)
	seen := make(map[string]api.ActiveIP)
	for _, ip := range activeIPs {
		if existing, ok := seen[ip.IP]; !ok || ip.LastSeen.After(existing.LastSeen) {
			seen[ip.IP] = ip
		}
	}
	uniqueIPs := make([]api.ActiveIP, 0, len(seen))
	for _, ip := range seen {
		uniqueIPs = append(uniqueIPs, ip)
	}

	// Check whitelist
	whitelisted, err := m.cache.IsWhitelisted(ctx, userID)
	if err != nil {
		m.logger.WithError(err).Error("Ошибка проверки whitelist")
	}
	if whitelisted {
		return
	}

	// Get user data (cached or from API)
	user, err := m.getUser(ctx, userID)
	if err != nil {
		m.logger.WithFields(logrus.Fields{
			"userId": userID,
			"error":  err,
		}).Warn("Ошибка получения данных пользователя")
		return
	}

	// Determine limit
	limit := m.resolveLimit(user)
	if limit <= 0 {
		return // No limit — skip
	}

	// Check violation
	if len(uniqueIPs) <= limit+m.config.Tolerance {
		return
	}

	// Check cooldown
	cooldownActive, err := m.cache.IsCooldownActive(ctx, userID)
	if err != nil {
		m.logger.WithError(err).Error("Ошибка проверки cooldown")
	}
	if cooldownActive {
		return
	}

	// Set cooldown
	m.cache.SetCooldown(ctx, userID, time.Duration(m.config.Cooldown)*time.Second)

	m.logger.WithFields(logrus.Fields{
		"userId":    userID,
		"username":  user.Username,
		"activeIPs": len(uniqueIPs),
		"limit":     limit,
	}).Warn("🚫 Превышение лимита устройств")

	// React
	if m.config.ActionMode == "auto" {
		m.handleAutoAction(ctx, user, uniqueIPs, limit)
	} else {
		m.handleManualAction(user, uniqueIPs, limit)
	}
}

func (m *Monitor) getUser(ctx context.Context, userID string) (*api.CachedUser, error) {
	// Try cache first
	cached, err := m.cache.GetUser(ctx, userID)
	if err != nil {
		m.logger.WithError(err).Debug("Ошибка чтения кэша пользователя")
	}
	if cached != nil {
		return cached, nil
	}

	// Fetch from API
	userData, err := m.api.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	cached = &api.CachedUser{
		UUID:     userData.UUID,
		UserID:   userID,
		Username: userData.Username,
		Status:   userData.Status,
	}

	if userData.Email != nil {
		cached.Email = *userData.Email
	}
	if userData.TelegramID != nil {
		cached.TelegramID = *userData.TelegramID
	}
	if userData.HWIDDeviceLimit != nil {
		cached.HWIDDeviceLimit = *userData.HWIDDeviceLimit
	} else {
		cached.HWIDDeviceLimit = -1 // Marker for "null — use default"
	}
	cached.SubscriptionURL = userData.SubscriptionURL

	// Cache it
	ttl := time.Duration(m.config.UserCacheTTL) * time.Second
	m.cache.SetUser(ctx, userID, cached, ttl)

	return cached, nil
}

func (m *Monitor) resolveLimit(user *api.CachedUser) int {
	if user.HWIDDeviceLimit == 0 {
		return 0 // Explicitly unlimited
	}
	if user.HWIDDeviceLimit == -1 {
		return m.config.DefaultDeviceLimit // Fallback
	}
	return user.HWIDDeviceLimit
}

func (m *Monitor) handleManualAction(user *api.CachedUser, ips []api.ActiveIP, limit int) {
	text := telegram.FormatManualAlert(user, ips, limit, m.location)
	if err := m.bot.SendManualAlert(text, user.UUID, user.UserID); err != nil {
		m.logger.WithError(err).Error("Ошибка отправки Telegram алерта")
	}
}

func (m *Monitor) handleAutoAction(ctx context.Context, user *api.CachedUser, ips []api.ActiveIP, limit int) {
	// Disable subscription
	if err := m.api.DisableUser(ctx, user.UUID); err != nil {
		m.logger.WithError(err).Error("Ошибка отключения подписки")
		return
	}

	// Set restore timer if not permanent
	if m.config.AutoDisableDuration > 0 {
		duration := time.Duration(m.config.AutoDisableDuration) * time.Minute
		if err := m.cache.SetRestoreTimer(ctx, user.UUID, duration); err != nil {
			m.logger.WithError(err).Error("Ошибка установки таймера восстановления")
		}
	}

	// Send alert
	text := telegram.FormatAutoAlert(user, ips, limit, m.config.AutoDisableDuration, m.location)
	if err := m.bot.SendAutoAlert(text, user.UUID); err != nil {
		m.logger.WithError(err).Error("Ошибка отправки Telegram алерта")
	}
}

func (m *Monitor) restoreLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			uuids, err := m.cache.GetExpiredRestoreTimers(ctx)
			if err != nil {
				m.logger.WithError(err).Error("Ошибка проверки restore таймеров")
				continue
			}
			for _, uuid := range uuids {
				if err := m.api.EnableUser(ctx, uuid); err != nil {
					m.logger.WithFields(logrus.Fields{
						"uuid":  uuid,
						"error": err,
					}).Error("Ошибка восстановления подписки")
					continue
				}
				m.logger.WithField("uuid", uuid).Info("🔓 Подписка восстановлена по таймеру")
				m.bot.SendMessage(fmt.Sprintf("🔓 Подписка <code>%s</code> автоматически восстановлена по таймеру", uuid))
			}
		}
	}
}
```

Note: add `"fmt"` to the imports — it's used in `restoreLoop`.

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/monitor/
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/monitor/monitor.go
git commit -m "feat: add monitor - core loop for IP aggregation and limit checking"
```

---

### Task 9: Entry Point and Callback Wiring

**Files:**
- Rewrite: `cmd/limiter/main.go`

- [ ] **Step 1: Implement main.go**

Rewrite `cmd/limiter/main.go`:

```go
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/monitor"
	"github.com/remnawave/limiter/internal/telegram"
	"github.com/remnawave/limiter/internal/version"
)

func main() {
	logger := logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	logger.Infof("📦 Remnawave Limiter v%s", version.Version)

	cfg, err := config.LoadConfig("")
	if err != nil {
		logger.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}

	logger.Infof("🔧 Режим: %s", cfg.ActionMode)
	logger.Infof("🔄 Интервал проверки: %dс", cfg.CheckInterval)
	logger.Infof("📡 API: %s", cfg.RemnawaveAPIURL)

	// Init Redis
	redisCache, err := cache.New(cfg.RedisURL)
	if err != nil {
		logger.Fatalf("Ошибка подключения к Redis: %v", err)
	}
	defer redisCache.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := redisCache.Ping(ctx); err != nil {
		logger.Fatalf("Redis недоступен: %v", err)
	}
	logger.Info("✅ Redis подключён")

	// Init whitelist from config
	if err := redisCache.InitWhitelist(ctx, cfg.WhitelistUserIDs); err != nil {
		logger.WithError(err).Warn("Ошибка инициализации whitelist")
	}

	// Init API client
	apiClient := api.NewClient(cfg.RemnawaveAPIURL, cfg.RemnawaveAPIToken)
	apiClient.SetLogger(logger)

	// Init Telegram bot
	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramChatID, cfg.TelegramThreadID, cfg.TelegramAdminIDs, logger)
	if err != nil {
		logger.Fatalf("Ошибка создания Telegram бота: %v", err)
	}
	logger.Info("✅ Telegram бот подключён")

	// Init monitor
	mon, err := monitor.New(cfg, apiClient, redisCache, bot, logger)
	if err != nil {
		logger.Fatalf("Ошибка создания монитора: %v", err)
	}

	// Wire callback handler
	bot.SetActionHandler(func(ctx context.Context, action, userUUID, userID string) error {
		switch action {
		case "drop":
			return apiClient.DropConnections(ctx, []string{userUUID})
		case "disable":
			return apiClient.DisableUser(ctx, userUUID)
		case "enable":
			return apiClient.EnableUser(ctx, userUUID)
		case "ignore":
			return redisCache.AddToWhitelist(ctx, userID)
		}
		return nil
	})

	// Handle shutdown
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start bot polling in background
	go bot.StartPolling(sigCtx)

	// Start monitor (blocks until shutdown)
	mon.Run(sigCtx)

	logger.Info("👋 Remnawave Limiter остановлен")
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build -o bin/remnawave-limiter ./cmd/limiter/
```

Expected: binary created.

- [ ] **Step 3: Commit**

```bash
git add cmd/limiter/main.go
git commit -m "feat: rewrite entry point for centralized architecture"
```

---

### Task 10: Docker Compose and Dockerfile

**Files:**
- Create: `Dockerfile`
- Create: `docker-compose.yml`
- Modify: `Makefile`

- [ ] **Step 1: Create Dockerfile**

Create `Dockerfile`:

```dockerfile
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/remnawave-limiter ./cmd/limiter/

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/remnawave-limiter /usr/local/bin/remnawave-limiter

ENTRYPOINT ["remnawave-limiter"]
```

- [ ] **Step 2: Create docker-compose.yml**

Create `docker-compose.yml`:

```yaml
services:
  limiter:
    build: .
    container_name: remnawave-limiter
    restart: unless-stopped
    env_file:
      - .env
    depends_on:
      redis:
        condition: service_healthy

  redis:
    image: valkey/valkey:8.1-alpine
    container_name: remnawave-limiter-redis
    restart: unless-stopped
    volumes:
      - redis-data:/data
    healthcheck:
      test: ["CMD", "valkey-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 3

volumes:
  redis-data:
```

- [ ] **Step 3: Update Makefile**

Rewrite `Makefile`:

```makefile
.PHONY: build clean test lint docker-up docker-down docker-build

GO_VERSION := 1.26
BINARY := remnawave-limiter

all: build

build:
	@echo "🔨 Сборка Remnawave Limiter..."
	go mod download
	go build -ldflags="-s -w" -o bin/$(BINARY) ./cmd/limiter/
	@echo "✅ Сборка завершена!"

clean:
	@echo "🗑️  Очистка..."
	rm -rf bin/
	@echo "✅ Очистка завершена!"

test:
	@echo "🧪 Запуск тестов..."
	go test -v ./...

lint:
	@echo "🔍 Проверка кода..."
	go vet ./...
	go fmt ./...

docker-build:
	@echo "🐳 Сборка Docker образа..."
	docker compose build

docker-up:
	@echo "🐳 Запуск..."
	docker compose up -d

docker-down:
	@echo "🐳 Остановка..."
	docker compose down
```

- [ ] **Step 4: Verify Docker build works**

```bash
docker compose build
```

Expected: image builds successfully.

- [ ] **Step 5: Commit**

```bash
git add Dockerfile docker-compose.yml Makefile
git commit -m "feat: add Docker Compose deployment (limiter + redis)"
```

---

### Task 11: Final cleanup and integration test

**Files:**
- Various cleanup

- [ ] **Step 1: Run all tests**

```bash
go test -v ./...
```

Expected: all tests pass.

- [ ] **Step 2: Run linter**

```bash
go vet ./...
```

Expected: no issues.

- [ ] **Step 3: Verify Docker Compose starts**

Create a test `.env` file with dummy values and verify containers start:

```bash
cp .env.example .env
# Edit .env with real values
docker compose up -d
docker compose logs limiter
```

Expected: limiter starts, connects to Redis, connects to Telegram.

- [ ] **Step 4: Verify shutdown handling**

```bash
docker compose down
```

Expected: clean shutdown, no errors.

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "chore: final cleanup, verify integration"
```

---

## Summary

| Task | Component | Estimated Steps |
|------|-----------|----------------|
| 1 | Cleanup old code, update deps | 6 |
| 2 | Config | 6 |
| 3 | API Types | 3 |
| 4 | API Client | 5 |
| 5 | Redis Cache | 5 |
| 6 | Telegram Messages | 5 |
| 7 | Telegram Bot | 3 |
| 8 | Monitor (core loop) | 3 |
| 9 | Entry Point | 3 |
| 10 | Docker Compose | 5 |
| 11 | Integration test | 5 |
| **Total** | | **49 steps** |
