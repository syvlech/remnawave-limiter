package monitor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
)

func quietLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

// nodeJobServer отдаёт результат connections/by-node для нод из ok
// и 500 для всех остальных.
func nodeJobServer(t *testing.T, healthy map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/connections/by-node/")

		if r.Method == http.MethodPost {
			if !healthy[id] {
				// 400, а не 5xx: клиент не ретраит 4xx, и тест не ждёт бэкофф.
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// jobId кодирует ноду, чтобы опрос статуса знал, что вернуть.
			w.Write([]byte(`{"response":{"jobId":"job-` + id + `"}}`))
			return
		}

		node := strings.TrimPrefix(id, "job-")
		w.Write([]byte(`{"response":{"isCompleted":true,"isFailed":false,"result":{
			"success":true,"nodeUuid":"` + node + `",
			"users":[{"userId":1,"ips":[{"ip":"1.1.1.1","lastSeen":"2026-08-04T10:00:00.000Z"}]}]}}}`))
	}))
}

func TestFetchNodes_PartialFailure(t *testing.T) {
	srv := nodeJobServer(t, map[string]bool{"good-1": true, "good-2": true})
	defer srv.Close()

	client := api.NewClient(srv.URL, "token")
	m := &Monitor{api: client, logger: quietLogger()}

	results, failed := m.fetchNodes(context.Background(), []api.Node{
		{UUID: "good-1", Name: "Good 1"},
		{UUID: "bad-1", Name: "Bad 1"},
		{UUID: "good-2", Name: "Good 2"},
	})

	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	// Сбойная нода не должна оставлять пустую запись среди результатов:
	// иначе её "нулевые" данные учитывались бы как отсутствие подключений.
	for _, res := range results {
		if res.nodeUUID == "" || len(res.entries) == 0 {
			t.Errorf("empty result leaked into successful set: %+v", res)
		}
	}
}

func TestFetchNodes_AllFailed(t *testing.T) {
	srv := nodeJobServer(t, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "token")
	m := &Monitor{api: client, logger: quietLogger()}

	nodes := []api.Node{{UUID: "bad-1"}, {UUID: "bad-2"}}
	results, failed := m.fetchNodes(context.Background(), nodes)

	// check() опирается на failed == len(nodes), чтобы не отмечать
	// проверку успешной и не врать в /healthz.
	if failed != len(nodes) {
		t.Errorf("failed = %d, want %d", failed, len(nodes))
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}

func TestFetchNodes_CancelledContext(t *testing.T) {
	srv := nodeJobServer(t, map[string]bool{"good-1": true})
	defer srv.Close()

	client := api.NewClient(srv.URL, "token")
	m := &Monitor{api: client, logger: quietLogger()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, failed := m.fetchNodes(ctx, []api.Node{{UUID: "good-1"}})
	if len(results) != 0 || failed != 1 {
		t.Errorf("results = %d, failed = %d; want 0 and 1", len(results), failed)
	}
}
