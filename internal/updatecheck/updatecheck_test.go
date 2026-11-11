package updatecheck

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

type fakeNotifier struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeNotifier) SendUpdateNotice(_ context.Context, current, latest, releaseURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, current+"→"+latest+" "+releaseURL)
	return f.err
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeStore struct {
	mu      sync.Mutex
	version string
	getErr  error
}

func (s *fakeStore) GetNotifiedVersion(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version, s.getErr
}

func (s *fakeStore) SetNotifiedVersion(_ context.Context, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = v
	return nil
}

func quietLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

func githubServer(t *testing.T, status int, body string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			t.Errorf("неожиданный путь %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("GitHub требует заголовок User-Agent")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newChecker(srvURL, current string, n *fakeNotifier, s *fakeStore) *Checker {
	return &Checker{
		Current:  current,
		Repo:     "owner/repo",
		Notifier: n,
		Store:    s,
		Logger:   quietLogger(),
		APIURL:   srvURL,
	}
}

func TestCheck_NotifiesOncePerVersion(t *testing.T) {
	var hits atomic.Int32
	srv := githubServer(t, 200, `{"tag_name":"v4.7.0","html_url":"https://github.com/owner/repo/releases/tag/v4.7.0"}`, &hits)
	n, s := &fakeNotifier{}, &fakeStore{}
	c := newChecker(srv.URL, "4.6.0", n, s)

	c.Check(context.Background())
	c.Check(context.Background())

	if n.count() != 1 {
		t.Fatalf("уведомлений %d, ожидалось 1: %v", n.count(), n.calls)
	}
	if want := "4.6.0→4.7.0 https://github.com/owner/repo/releases/tag/v4.7.0"; n.calls[0] != want {
		t.Errorf("уведомление %q, ожидалось %q", n.calls[0], want)
	}
	if s.version != "4.7.0" {
		t.Errorf("сохранена версия %q, ожидалась 4.7.0", s.version)
	}
	if hits.Load() != 2 {
		t.Errorf("запросов к GitHub %d, ожидалось 2", hits.Load())
	}
}

func TestCheck_NoNoticeWhenUpToDateOrOlder(t *testing.T) {
	for _, latest := range []string{"4.6.0", "4.5.9", "3.9.9"} {
		var hits atomic.Int32
		srv := githubServer(t, 200, `{"tag_name":"`+latest+`"}`, &hits)
		n := &fakeNotifier{}
		newChecker(srv.URL, "4.6.0", n, &fakeStore{}).Check(context.Background())
		if n.count() != 0 {
			t.Errorf("релиз %s: уведомление не нужно, получено %v", latest, n.calls)
		}
	}
}

func TestCheck_RetriesNoticeAfterSendFailure(t *testing.T) {
	var hits atomic.Int32
	srv := githubServer(t, 200, `{"tag_name":"4.7.0"}`, &hits)
	n, s := &fakeNotifier{err: errors.New("telegram недоступен")}, &fakeStore{}
	c := newChecker(srv.URL, "4.6.0", n, s)

	c.Check(context.Background())
	if s.version != "" {
		t.Fatalf("версия сохранена %q, хотя уведомление не ушло", s.version)
	}
	n.err = nil
	c.Check(context.Background())
	if n.count() != 2 || s.version != "4.7.0" {
		t.Errorf("после сбоя уведомление должно уйти повторно: вызовов %d, сохранено %q", n.count(), s.version)
	}
}

func TestCheck_SkipsWhenStateUnreadable(t *testing.T) {
	var hits atomic.Int32
	srv := githubServer(t, 200, `{"tag_name":"4.7.0"}`, &hits)
	n := &fakeNotifier{}
	newChecker(srv.URL, "4.6.0", n, &fakeStore{getErr: errors.New("redis недоступен")}).Check(context.Background())
	if n.count() != 0 {
		t.Errorf("без состояния из Redis уведомление слать нельзя (иначе повтор каждые сутки): %v", n.calls)
	}
}

func TestCheck_GitHubErrorAndBadTag(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"rate limit":      {403, `{"message":"API rate limit exceeded"}`},
		"нет релизов":     {404, `{"message":"Not Found"}`},
		"битый JSON":      {200, `{"tag_name":`},
		"тег не semver":   {200, `{"tag_name":"nightly"}`},
		"pre-release тег": {200, `{"tag_name":"4.7.0-rc1"}`},
	} {
		var hits atomic.Int32
		srv := githubServer(t, tc.status, tc.body, &hits)
		n := &fakeNotifier{}
		newChecker(srv.URL, "4.6.0", n, &fakeStore{}).Check(context.Background())
		if n.count() != 0 {
			t.Errorf("%s: уведомление не нужно, получено %v", name, n.calls)
		}
	}
}

func TestCheck_DevBuildSkipsRequest(t *testing.T) {
	var hits atomic.Int32
	srv := githubServer(t, 200, `{"tag_name":"9.9.9"}`, &hits)
	newChecker(srv.URL, "dev", &fakeNotifier{}, &fakeStore{}).Check(context.Background())
	if hits.Load() != 0 {
		t.Errorf("для версии не вида X.Y.Z запрос к GitHub не нужен, запросов %d", hits.Load())
	}
}

func TestLatest_ForeignReleaseURLReplaced(t *testing.T) {
	var hits atomic.Int32
	srv := githubServer(t, 200, `{"tag_name":"4.7.0","html_url":"https://evil.example/phish"}`, &hits)
	rel, err := newChecker(srv.URL, "4.6.0", nil, nil).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://github.com/owner/repo/releases/tag/4.7.0"; rel.URL != want {
		t.Errorf("URL = %q, ожидался %q", rel.URL, want)
	}
}

func TestRun_RespectsEnabledAndStops(t *testing.T) {
	var hits atomic.Int32
	srv := githubServer(t, 200, `{"tag_name":"4.6.0"}`, &hits)
	var enabled atomic.Bool
	c := newChecker(srv.URL, "4.6.0", &fakeNotifier{}, &fakeStore{})
	c.Enabled = enabled.Load
	c.FirstDelay = time.Millisecond
	c.Interval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()

	time.Sleep(30 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatalf("при выключенной проверке запросов быть не должно, их %d", hits.Load())
	}
	enabled.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() == 0 {
		t.Fatal("после включения проверка так и не выполнилась")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
}

func TestParseVersionAndNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"4.7.0", "4.6.0", true},
		{"4.10.0", "4.9.9", true},
		{"5.0.0", "4.99.99", true},
		{"v4.6.1", "4.6.0", true},
		{"4.6.0", "4.6.0", false},
		{"4.5.9", "4.6.0", false},
	} {
		a, okA := parseVersion(tc.a)
		b, okB := parseVersion(tc.b)
		if !okA || !okB {
			t.Fatalf("не разобрались версии %q / %q", tc.a, tc.b)
		}
		if got := newer(a, b); got != tc.want {
			t.Errorf("newer(%s, %s) = %v, ожидалось %v", tc.a, tc.b, got, tc.want)
		}
	}
	for _, bad := range []string{"", "dev", "4.6", "4.6.0.1", "4.6.0-rc1", "4.6.x", "04.6.0", "-1.0.0"} {
		if _, ok := parseVersion(bad); ok {
			t.Errorf("версия %q не должна разбираться", bad)
		}
	}
}
