package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

const testNodeUUID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

func fastJobClient(baseURL string) (*Client, *logrustest.Hook) {
	c := fastClient(baseURL)
	c.pollFirstDelay = time.Millisecond
	c.pollInterval = time.Millisecond
	logger, hook := logrustest.NewNullLogger()
	c.SetLogger(logger)
	return c, hook
}

func retryWarnings(hook *logrustest.Hook) int {
	n := 0
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel {
			n++
		}
	}
	return n
}

func dropConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	hj, ok := w.(http.Hijacker)
	if !ok {
		t.Error("тестовый сервер не умеет Hijack")
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		t.Errorf("Hijack: %v", err)
		return
	}
	_ = conn.Close()
}

type jobServer struct {
	*httptest.Server
	posts atomic.Int32
	polls atomic.Int32
}

func newJobServer(t *testing.T, post, poll func(w http.ResponseWriter, r *http.Request, n int32)) *jobServer {
	t.Helper()
	js := &jobServer{}
	js.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/connections/by-node/"+testNodeUUID:
			n := js.posts.Add(1)
			if post == nil {
				_, _ = w.Write([]byte(`{"response":{"jobId":"job-1"}}`))
				return
			}
			post(w, r, n)
		case r.Method == http.MethodGet && r.URL.Path == "/api/connections/by-node/job-1":
			poll(w, r, js.polls.Add(1))
		default:
			t.Errorf("неожиданный запрос %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(js.Close)
	return js
}

func jobDone(w http.ResponseWriter, _ *http.Request, _ int32) {
	_, _ = w.Write([]byte(`{"response":{"isCompleted":true,"isFailed":false,"result":{"success":true,"nodeUuid":"n","users":[{"userId":7,"ips":[]}]}}}`))
}

func TestClient_ResponseSizeLimit(t *testing.T) {
	const limit = 1024
	for _, tc := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"ровно предел", limit, false},
		{"на байт больше", limit + 1, true},
		{"намного больше", 16 * limit, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			body := `{"response":[],"pad":"` + strings.Repeat("x", tc.size-len(`{"response":[],"pad":""}`)) + `"}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			c, hook := fastJobClient(srv.URL)
			c.maxRespBytes = limit
			_, err := c.GetNodes(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("ответ %d байт при пределе %d: ошибка %v", len(body), limit, err)
			}
			if calls.Load() != 1 {
				t.Errorf("слишком большой ответ не должен скачиваться повторно, запросов %d", calls.Load())
			}
			if n := retryWarnings(hook); n != 0 {
				t.Errorf("повторов быть не должно, в логе %d", n)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "1024") {
				t.Errorf("в ошибке должен быть предел: %v", err)
			}
		})
	}
}

func TestClient_DefaultResponseLimit(t *testing.T) {
	c := NewClient("http://127.0.0.1", "t")
	if c.maxRespBytes != maxResponseBytes || c.pollMaxTries != jobPollMaxTries ||
		c.pollInterval != jobPollInterval || c.pollFirstDelay != jobFirstPollDelay {
		t.Errorf("NewClient должен брать пределы из констант: %+v", c)
	}
}

func TestClient_InvalidHeaderIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"response":[]}`))
	}))
	defer srv.Close()

	for name, headers := range map[string]map[string]string{
		"перевод строки в значении": {"X-Api-Key": "secret\r\nX-Injected: 1"},
		"управляющий символ":        {"X-Api-Key": "sec\x00ret"},
		"пробел в имени":            {"X Api Key": "secret"},
	} {
		t.Run(name, func(t *testing.T) {
			calls.Store(0)
			c, hook := fastJobClient(srv.URL)
			c.SetHeaders(headers)

			_, err := c.GetNodes(context.Background())
			if err == nil {
				t.Fatal("ожидалась ошибка о недопустимом заголовке")
			}
			if !strings.Contains(err.Error(), "invalid header field") {
				t.Errorf("ошибка должна называть причину: %v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("значение заголовка не должно попадать в ошибку: %v", err)
			}
			if calls.Load() != 0 || retryWarnings(hook) != 0 {
				t.Errorf("запросов %d, повторов %d — не должно быть ни тех, ни других", calls.Load(), retryWarnings(hook))
			}
		})
	}

	c, _ := fastJobClient(srv.URL)
	c.SetHeaders(map[string]string{"X-Note": "tab\there, юникод и (скобки)"})
	calls.Store(0)
	if _, err := c.GetNodes(context.Background()); err != nil || calls.Load() != 1 {
		t.Errorf("допустимое значение должно уходить как раньше: %v, запросов %d", err, calls.Load())
	}
}

func TestClient_InvalidURLIsNotRetried(t *testing.T) {
	c, hook := fastJobClient("http://bad\x7fhost")

	_, err := c.GetNodes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "create request") {
		t.Fatalf("ожидалась ошибка создания запроса, получено %v", err)
	}
	if n := retryWarnings(hook); n != 0 {
		t.Errorf("ошибка создания запроса не должна повторяться, повторов %d", n)
	}
}

func TestClient_NetworkErrorIsStillRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			dropConnection(t, w)
			return
		}
		_, _ = w.Write([]byte(`{"response":[]}`))
	}))
	defer srv.Close()

	c, hook := fastJobClient(srv.URL)
	if _, err := c.GetNodes(context.Background()); err != nil {
		t.Fatalf("обрыв соединения на GET должен повторяться: %v", err)
	}
	if calls.Load() != 3 || retryWarnings(hook) != 2 {
		t.Errorf("запросов %d, повторов %d, ожидалось 3 и 2", calls.Load(), retryWarnings(hook))
	}
}

func TestClient_NetworkErrorIsStillRetriedOnOtherPosts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			dropConnection(t, w)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = w.Write([]byte(`{"response":{}}`))
		}
	}))
	defer srv.Close()

	c, _ := fastJobClient(srv.URL)
	if err := c.DisableUser(context.Background(), 42); err != nil {
		t.Fatalf("у остальных POST политика повторов прежняя: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("запросов %d, ожидалось 3", calls.Load())
	}
}

func TestFetchUsersIPs_JobStartNotRetriedOnServerError(t *testing.T) {
	for _, code := range []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusGatewayTimeout,
		http.StatusRequestTimeout,
	} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			js := newJobServer(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
				w.WriteHeader(code)
			}, jobDone)

			c, hook := fastJobClient(js.URL)
			_, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
			var se *StatusError
			if !errors.As(err, &se) || se.Code != code {
				t.Fatalf("ожидалась StatusError %d, получено %v", code, err)
			}
			if js.posts.Load() != 1 || js.polls.Load() != 0 || retryWarnings(hook) != 0 {
				t.Errorf("задача должна создаваться один раз: POST %d, опросов %d, повторов %d",
					js.posts.Load(), js.polls.Load(), retryWarnings(hook))
			}
		})
	}
}

func TestFetchUsersIPs_JobStartRetriedOn429And503(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			js := newJobServer(t, func(w http.ResponseWriter, _ *http.Request, n int32) {
				if n < 3 {
					w.WriteHeader(code)
					return
				}
				_, _ = w.Write([]byte(`{"response":{"jobId":"job-1"}}`))
			}, jobDone)

			c, _ := fastJobClient(js.URL)
			users, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
			if err != nil || len(users) != 1 {
				t.Fatalf("после %d запрос должен повториться и пройти: %v, %v", code, users, err)
			}
			if js.posts.Load() != 3 {
				t.Errorf("POST %d, ожидалось 3", js.posts.Load())
			}
		})
	}
}

func TestFetchUsersIPs_JobStartNotRetriedWhenAnswerIsLost(t *testing.T) {
	js := newJobServer(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		dropConnection(t, w)
	}, jobDone)

	c, hook := fastJobClient(js.URL)
	_, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
	if err == nil {
		t.Fatal("ожидалась ошибка: ответа на POST не было")
	}
	if js.posts.Load() != 1 || retryWarnings(hook) != 0 {
		t.Errorf("запрос дошёл до панели, вторую задачу создавать нельзя: POST %d, повторов %d",
			js.posts.Load(), retryWarnings(hook))
	}
}

func TestFetchUsersIPs_JobStartNotRetriedOnTimeout(t *testing.T) {
	release := make(chan struct{})
	js := newJobServer(t, func(_ http.ResponseWriter, r *http.Request, _ int32) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}, jobDone)
	defer close(release)

	c, hook := fastJobClient(js.URL)
	c.httpClient.Timeout = 150 * time.Millisecond
	_, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
	if err == nil {
		t.Fatal("ожидалась ошибка по таймауту")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("ожидалась ошибка таймаута, получено %v", err)
	}
	if js.posts.Load() != 1 || retryWarnings(hook) != 0 {
		t.Errorf("после таймаута задача не должна создаваться заново: POST %d, повторов %d",
			js.posts.Load(), retryWarnings(hook))
	}
}

func TestFetchUsersIPs_JobStartRetriedWhenDialFails(t *testing.T) {
	js := newJobServer(t, nil, jobDone)

	var dials atomic.Int32
	c, hook := fastJobClient(js.URL)
	c.httpClient.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if dials.Add(1) <= 2 {
				return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("connection refused")}
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}

	users, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
	if err != nil || len(users) != 1 {
		t.Fatalf("запрос, не дошедший до панели, должен повторяться: %v, %v", users, err)
	}
	if js.posts.Load() != 1 || retryWarnings(hook) != 2 {
		t.Errorf("POST %d, повторов %d, ожидалось 1 и 2", js.posts.Load(), retryWarnings(hook))
	}
}

func TestFetchUsersIPs_JobStartRetriedWhenPortIsClosed(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	baseURL := srv.URL
	srv.Close()

	c, hook := fastJobClient(baseURL)
	_, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
	if err == nil || !strings.Contains(err.Error(), "failed after 3 retries") {
		t.Fatalf("ожидалась ошибка после всех повторов, получено %v", err)
	}
	if n := retryWarnings(hook); n != maxRetries {
		t.Errorf("отказ в соединении должен повторяться %d раз, повторов %d", maxRetries, n)
	}
}

func TestFetchUsersIPs_PollIsStillRetried(t *testing.T) {
	js := newJobServer(t, nil, func(w http.ResponseWriter, r *http.Request, n int32) {
		switch n {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
		case 2:
			dropConnection(t, w)
		default:
			jobDone(w, r, n)
		}
	})

	c, _ := fastJobClient(js.URL)
	users, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
	if err != nil || len(users) != 1 || users[0].UserID != 7 {
		t.Fatalf("опрос задачи должен повторяться как раньше: %v, %v", users, err)
	}
	if js.posts.Load() != 1 || js.polls.Load() != 3 {
		t.Errorf("POST %d, опросов %d, ожидалось 1 и 3", js.posts.Load(), js.polls.Load())
	}
}

func TestFetchUsersIPs_JobOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{"isFailed", `{"response":{"isCompleted":false,"isFailed":true,"result":null}}`, "job job-1 failed"},
		{"isFailed важнее isCompleted", `{"response":{"isCompleted":true,"isFailed":true,"result":{"success":true,"users":[]}}}`, "job job-1 failed"},
		{"result nil", `{"response":{"isCompleted":true,"isFailed":false,"result":null}}`, "result is nil"},
		{"success false", `{"response":{"isCompleted":true,"isFailed":false,"result":{"success":false,"nodeUuid":"n","users":[]}}}`, "success=false"},
		{"не JSON", `<html>502</html>`, "decode job result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			js := newJobServer(t, nil, func(w http.ResponseWriter, _ *http.Request, _ int32) {
				_, _ = w.Write([]byte(tc.body))
			})

			c, _ := fastJobClient(js.URL)
			users, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ожидалась ошибка с %q, получено %v (%v)", tc.wantErr, err, users)
			}
			if js.polls.Load() != 1 {
				t.Errorf("после окончательного ответа опрос должен прекратиться, опросов %d", js.polls.Load())
			}
		})
	}
}

func TestFetchUsersIPs_JobStartAnswers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{"пустой jobId", `{"response":{"jobId":""}}`, "empty job ID"},
		{"не JSON", `<html>ok</html>`, "decode job response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			js := newJobServer(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
				_, _ = w.Write([]byte(tc.body))
			}, jobDone)

			c, _ := fastJobClient(js.URL)
			_, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ожидалась ошибка с %q, получено %v", tc.wantErr, err)
			}
			if js.posts.Load() != 1 || js.polls.Load() != 0 {
				t.Errorf("POST %d, опросов %d, ожидалось 1 и 0", js.posts.Load(), js.polls.Load())
			}
		})
	}
}

func TestFetchUsersIPs_PollingExhausted(t *testing.T) {
	js := newJobServer(t, nil, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		_, _ = w.Write([]byte(`{"response":{"isCompleted":false,"isFailed":false,"result":null}}`))
	})

	c, _ := fastJobClient(js.URL)
	c.pollMaxTries = 4
	_, err := c.FetchUsersIPs(context.Background(), testNodeUUID)
	if err == nil || !strings.Contains(err.Error(), "timed out after 4 polls") {
		t.Fatalf("ожидалась ошибка об исчерпании опросов, получено %v", err)
	}
	if js.posts.Load() != 1 || js.polls.Load() != 4 {
		t.Errorf("POST %d, опросов %d, ожидалось 1 и 4", js.posts.Load(), js.polls.Load())
	}
}

func TestFetchUsersIPs_ContextCancelledWhilePolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	js := newJobServer(t, nil, func(w http.ResponseWriter, _ *http.Request, n int32) {
		if n == 2 {
			cancel()
		}
		_, _ = w.Write([]byte(`{"response":{"isCompleted":false,"isFailed":false,"result":null}}`))
	})

	c, _ := fastJobClient(js.URL)
	c.pollInterval = 20 * time.Millisecond
	_, err := c.FetchUsersIPs(ctx, testNodeUUID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась context.Canceled, получено %v", err)
	}
	if n := js.polls.Load(); n != 2 {
		t.Errorf("после отмены опрос должен прекратиться, опросов %d", n)
	}
}

func TestRetryAfter_HTTPDate(t *testing.T) {
	h := http.Header{}

	h.Set("Retry-After", time.Now().Add(90*time.Second).UTC().Format(http.TimeFormat))
	if d := retryAfter(h); d < 85*time.Second || d > 90*time.Second {
		t.Errorf("дата через 90 секунд дала %v", d)
	}

	h.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	if d := retryAfter(h); d != 0 {
		t.Errorf("дата в прошлом должна давать 0, получено %v", d)
	}

	h.Set("Retry-After", time.Now().Add(90*time.Second).UTC().Format(time.RFC850))
	if d := retryAfter(h); d < 85*time.Second || d > 90*time.Second {
		t.Errorf("дата в формате RFC 850 дала %v", d)
	}
}

func TestClient_RetryAfterHTTPDateBeyondLimitStopsRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", time.Now().Add(10*time.Minute).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, hook := fastJobClient(srv.URL)
	_, err := c.GetNodes(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusServiceUnavailable {
		t.Fatalf("ожидалась ошибка 503, получено %v", err)
	}
	if calls.Load() != 1 || retryWarnings(hook) != 0 {
		t.Errorf("дата дальше предела должна останавливать повторы: запросов %d, повторов %d",
			calls.Load(), retryWarnings(hook))
	}
}

func TestParseCookies(t *testing.T) {
	if got := ParseCookies(""); got != nil {
		t.Errorf("пустая строка: %v", got)
	}
	if got := ParseCookies(" ; ;novalue; "); len(got) != 0 {
		t.Errorf("части без «=» должны пропускаться: %v", got)
	}

	got := ParseCookies(" sid = abc ;cf_clearance=a=b==; ; broken ;last=1")
	want := [][2]string{{"sid", "abc"}, {"cf_clearance", "a=b=="}, {"last", "1"}}
	if len(got) != len(want) {
		t.Fatalf("cookie %d, ожидалось %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Name != w[0] || got[i].Value != w[1] {
			t.Errorf("cookie %d = %s=%s, ожидалось %s=%s", i, got[i].Name, got[i].Value, w[0], w[1])
		}
	}
}

func TestClient_SendsParsedCookies(t *testing.T) {
	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{"response":[]}`))
	}))
	defer srv.Close()

	c := fastClient(srv.URL)
	c.SetCookies(ParseCookies("sid=abc; cf_clearance=xyz"))
	if _, err := c.GetNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if header != "sid=abc; cf_clearance=xyz" {
		t.Errorf("Cookie = %q", header)
	}
}
