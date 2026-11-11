package monitor

import (
	"context"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
	"github.com/remnawave/limiter/internal/geoip"
	"github.com/remnawave/limiter/internal/webhook"
)

type callLog struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
	hook  func(method string)
}

func (l *callLog) enter(method string) error {
	l.mu.Lock()
	l.calls = append(l.calls, method)
	err := l.fail[method]
	hook := l.hook
	l.mu.Unlock()
	if hook != nil {
		hook(method)
	}
	return err
}

func (l *callLog) failOn(method string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail == nil {
		l.fail = make(map[string]error)
	}
	l.fail[method] = err
}

func (l *callLog) called(method string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, c := range l.calls {
		if c == method {
			n++
		}
	}
	return n
}

type fakePanel struct {
	callLog

	nodes    []api.Node
	ips      map[string][]api.UserIPEntry
	nodeErrs map[string]error
	users    map[int64]*api.UserData

	disabled []int64
	enabled  []int64
}

func (p *fakePanel) GetActiveNodes(context.Context) ([]api.Node, error) {
	if err := p.enter("GetActiveNodes"); err != nil {
		return nil, err
	}
	return p.nodes, nil
}

func (p *fakePanel) FetchUsersIPs(_ context.Context, nodeUUID string) ([]api.UserIPEntry, error) {
	if err := p.enter("FetchUsersIPs"); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.nodeErrs[nodeUUID]; err != nil {
		return nil, err
	}
	return p.ips[nodeUUID], nil
}

func (p *fakePanel) GetUserByID(_ context.Context, userID int64) (*api.UserData, error) {
	if err := p.enter("GetUserByID"); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.users[userID]
	if !ok {
		return nil, &api.StatusError{Method: "GET", Path: "/api/users/by-id", Code: 404, Body: "not found"}
	}
	return u, nil
}

func (p *fakePanel) DisableUser(_ context.Context, userID int64) error {
	if err := p.enter("DisableUser"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disabled = append(p.disabled, userID)
	return nil
}

func (p *fakePanel) EnableUser(ctx context.Context, userID int64) error {
	if err := p.enter("EnableUser"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = append(p.enabled, userID)
	return nil
}

type rescheduleCall struct {
	member string
	lease  cache.RestoreLease
	delay  time.Duration
}

type fakeStore struct {
	callLog

	whitelisted  map[int64]bool
	users        map[int64]*api.CachedUser
	userTTL      map[int64]time.Duration
	cooldown     map[int64]time.Duration
	softCooldown map[int64]time.Duration
	violations   map[int64]int64
	threshold    map[int64]int64
	windows      []time.Duration
	recorded     []int64
	timers       map[int64]time.Duration
	timerRemoved []int64

	claimed     []string
	lease       cache.RestoreLease
	leaseLost   map[string]bool
	completed   []string
	rescheduled []rescheduleCall
	attempts    map[int64]int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		whitelisted:  make(map[int64]bool),
		users:        make(map[int64]*api.CachedUser),
		userTTL:      make(map[int64]time.Duration),
		cooldown:     make(map[int64]time.Duration),
		softCooldown: make(map[int64]time.Duration),
		violations:   make(map[int64]int64),
		threshold:    make(map[int64]int64),
		timers:       make(map[int64]time.Duration),
		leaseLost:    make(map[string]bool),
		attempts:     make(map[int64]int64),
		lease:        "1800000000.5",
	}
}

func (s *fakeStore) IsWhitelisted(_ context.Context, userID int64) (bool, error) {
	if err := s.enter("IsWhitelisted"); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.whitelisted[userID], nil
}

func (s *fakeStore) GetUser(_ context.Context, userID int64) (*api.CachedUser, error) {
	if err := s.enter("GetUser"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[userID], nil
}

func (s *fakeStore) SetUser(_ context.Context, userID int64, user *api.CachedUser, ttl time.Duration) error {
	if err := s.enter("SetUser"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[userID] = user
	s.userTTL[userID] = ttl
	return nil
}

func (s *fakeStore) IsCooldownActive(_ context.Context, userID int64) (bool, error) {
	if err := s.enter("IsCooldownActive"); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.cooldown[userID]
	return ok, nil
}

func (s *fakeStore) SetCooldown(_ context.Context, userID int64, ttl time.Duration) error {
	if err := s.enter("SetCooldown"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cooldown[userID] = ttl
	return nil
}

func (s *fakeStore) IsSoftCooldownActive(_ context.Context, userID int64) (bool, error) {
	if err := s.enter("IsSoftCooldownActive"); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.softCooldown[userID]
	return ok, nil
}

func (s *fakeStore) SetSoftCooldown(_ context.Context, userID int64, ttl time.Duration) error {
	if err := s.enter("SetSoftCooldown"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.softCooldown[userID] = ttl
	return nil
}

func (s *fakeStore) IncrViolationCount(_ context.Context, userID int64) (int64, error) {
	if err := s.enter("IncrViolationCount"); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.violations[userID]++
	return s.violations[userID], nil
}

func (s *fakeStore) IncrThresholdCount(_ context.Context, userID int64, window time.Duration) (int64, error) {
	if err := s.enter("IncrThresholdCount"); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threshold[userID]++
	s.windows = append(s.windows, window)
	return s.threshold[userID], nil
}

func (s *fakeStore) ResetThresholdCount(_ context.Context, userID int64) error {
	if err := s.enter("ResetThresholdCount"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.threshold, userID)
	return nil
}

func (s *fakeStore) RecordViolation(_ context.Context, userID int64, _ string) error {
	if err := s.enter("RecordViolation"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, userID)
	return nil
}

func (s *fakeStore) GetViolationStats(context.Context, int) (*cache.ViolationStats, error) {
	if err := s.enter("GetViolationStats"); err != nil {
		return nil, err
	}
	return &cache.ViolationStats{}, nil
}

func (s *fakeStore) SetRestoreTimer(_ context.Context, userID int64, duration time.Duration) error {
	if err := s.enter("SetRestoreTimer"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timers[userID] = duration
	return nil
}

func (s *fakeStore) RemoveRestoreTimer(_ context.Context, userID int64) error {
	if err := s.enter("RemoveRestoreTimer"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.timers, userID)
	s.timerRemoved = append(s.timerRemoved, userID)
	return nil
}

func (s *fakeStore) ClaimExpiredRestoreTimers(context.Context) ([]string, cache.RestoreLease, error) {
	if err := s.enter("ClaimExpiredRestoreTimers"); err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claimed, s.lease, nil
}

func (s *fakeStore) RestoreLeaseHeld(_ context.Context, member string, lease cache.RestoreLease) (bool, error) {
	if err := s.enter("RestoreLeaseHeld"); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return lease == s.lease && !s.leaseLost[member], nil
}

func (s *fakeStore) CompleteRestore(ctx context.Context, member string, lease cache.RestoreLease) error {
	if err := s.enter("CompleteRestore"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if lease == s.lease {
		s.completed = append(s.completed, member)
	}
	return nil
}

func (s *fakeStore) RescheduleRestore(ctx context.Context, member string, lease cache.RestoreLease, delay time.Duration) error {
	if err := s.enter("RescheduleRestore"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rescheduled = append(s.rescheduled, rescheduleCall{member: member, lease: lease, delay: delay})
	return nil
}

func (s *fakeStore) IncrRestoreAttempts(_ context.Context, userID int64) (int64, error) {
	if err := s.enter("IncrRestoreAttempts"); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts[userID]++
	return s.attempts[userID], nil
}

func (s *fakeStore) ResetRestoreAttempts(_ context.Context, userID int64) error {
	if err := s.enter("ResetRestoreAttempts"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attempts, userID)
	return nil
}

type sentAlert struct {
	text            string
	userID          int64
	disableDuration int
	ignoreDuration  int
}

type fakeBot struct {
	callLog

	manual   []sentAlert
	auto     []sentAlert
	soft     []sentAlert
	messages []string
	reports  []string
}

func (b *fakeBot) SendManualAlert(_ context.Context, text string, userID int64, disableDuration int, ignoreDuration int) error {
	if err := b.enter("SendManualAlert"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.manual = append(b.manual, sentAlert{text: text, userID: userID, disableDuration: disableDuration, ignoreDuration: ignoreDuration})
	return nil
}

func (b *fakeBot) SendAutoAlert(_ context.Context, text string, userID int64) error {
	if err := b.enter("SendAutoAlert"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.auto = append(b.auto, sentAlert{text: text, userID: userID})
	return nil
}

func (b *fakeBot) SendSoftAlert(_ context.Context, text string, userID int64, ignoreDuration int) error {
	if err := b.enter("SendSoftAlert"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.soft = append(b.soft, sentAlert{text: text, userID: userID, ignoreDuration: ignoreDuration})
	return nil
}

func (b *fakeBot) SendMessage(_ context.Context, text string) error {
	if err := b.enter("SendMessage"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, text)
	return nil
}

func (b *fakeBot) SendDailyReport(_ context.Context, text string) error {
	if err := b.enter("SendDailyReport"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reports = append(b.reports, text)
	return nil
}

func (b *fakeBot) alerts() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.manual) + len(b.auto) + len(b.soft)
}

type fakeWebhook struct {
	mu        sync.Mutex
	payloads  []*webhook.Payload
	deadlines []time.Duration
	hang      bool
	cancelled int
}

func (w *fakeWebhook) Send(ctx context.Context, payload *webhook.Payload) {
	w.mu.Lock()
	w.payloads = append(w.payloads, payload)
	if deadline, ok := ctx.Deadline(); ok {
		w.deadlines = append(w.deadlines, time.Until(deadline))
	}
	hang := w.hang
	w.mu.Unlock()

	if !hang {
		return
	}
	<-ctx.Done()
	w.mu.Lock()
	w.cancelled++
	w.mu.Unlock()
}

func (w *fakeWebhook) sent() []*webhook.Payload {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*webhook.Payload(nil), w.payloads...)
}

type logCapture struct {
	mu      sync.Mutex
	entries []logrus.Entry
}

func (c *logCapture) Levels() []logrus.Level { return logrus.AllLevels }

func (c *logCapture) Fire(e *logrus.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, *e)
	return nil
}

func (c *logCapture) has(level logrus.Level, message string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.Level == level && e.Message == message {
			return true
		}
	}
	return false
}

type countingResolver struct {
	mu      sync.Mutex
	asn     map[string]uint32
	lookups int
}

func (r *countingResolver) Lookup(ip string) (geoip.ASNInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups++
	n, ok := r.asn[ip]
	if !ok {
		return geoip.ASNInfo{}, false
	}
	return geoip.ASNInfo{Number: n, Org: "AS" + strconv.FormatUint(uint64(n), 10)}, true
}

type harness struct {
	m        *Monitor
	provider *config.Provider
	panel    *fakePanel
	store    *fakeStore
	bot      *fakeBot
	webhook  *fakeWebhook
	resolver *countingResolver
	logs     *logCapture
}

func testConfig() *config.Config {
	return &config.Config{
		CheckInterval:            30,
		ActiveIPWindow:           60,
		Cooldown:                 300,
		UserCacheTTL:             600,
		DefaultDeviceLimit:       2,
		ActionMode:               "manual",
		AutoDisableDuration:      30,
		IgnoreDuration:           120,
		Timezone:                 "UTC",
		SubnetPrefixV4:           24,
		ViolationThreshold:       1,
		ViolationThresholdWindow: 3600,
	}
}

func newHarness(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()

	cfg := testConfig()
	if mutate != nil {
		mutate(cfg)
	}

	logs := &logCapture{}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	logger.SetLevel(logrus.DebugLevel)
	logger.AddHook(logs)

	h := &harness{
		provider: config.NewProvider(cfg),
		panel: &fakePanel{
			ips:      make(map[string][]api.UserIPEntry),
			nodeErrs: make(map[string]error),
			users:    make(map[int64]*api.UserData),
		},
		store:    newFakeStore(),
		bot:      &fakeBot{},
		webhook:  &fakeWebhook{},
		resolver: &countingResolver{asn: make(map[string]uint32)},
		logs:     logs,
	}

	m, err := newMonitor(h.provider, dependencies{
		api:     h.panel,
		cache:   h.store,
		bot:     h.bot,
		webhook: h.webhook,
	}, h.resolver, logger)
	if err != nil {
		t.Fatalf("newMonitor: %v", err)
	}
	h.m = m
	return h
}

func (h *harness) cacheUser(userID int64, limit int) *api.CachedUser {
	u := &api.CachedUser{UserID: userID, Username: "user" + strconv.FormatInt(userID, 10), HWIDDeviceLimit: limit}
	h.store.mu.Lock()
	h.store.users[userID] = u
	h.store.mu.Unlock()
	return u
}

func (h *harness) checkUser(userID int64, ips ...string) *checkStats {
	st := &checkStats{}
	h.m.checkUser(context.Background(), userID, activeIPs(ips...), st)
	h.m.webhookWG.Wait()
	return st
}

func activeIPs(ips ...string) []api.ActiveIP {
	now := time.Now()
	out := make([]api.ActiveIP, len(ips))
	for i, ip := range ips {
		out[i] = api.ActiveIP{IP: ip, LastSeen: now, NodeName: "node", NodeUUID: "node-1"}
	}
	return out
}
