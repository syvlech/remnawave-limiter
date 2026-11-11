package traffic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/config"
)

const gb = gib

func gbf(v float64) int64 { return int64(v * float64(gb)) }

type fakePanel struct {
	nodes    []api.Node
	usage    map[string]map[string]map[int64]int64
	err      error
	nodesErr error
	calls    []string
	asked    [][]string
}

func (p *fakePanel) GetNodes(context.Context) ([]api.Node, error) { return p.nodes, p.nodesErr }

func (p *fakePanel) GetNodesUsage(_ context.Context, uuids []string, day string, minBytes int64) ([]api.NodeUsage, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.calls = append(p.calls, day+"@"+itoa(minBytes))
	p.asked = append(p.asked, uuids)
	var out []api.NodeUsage
	for _, uuid := range uuids {
		nu := api.NodeUsage{UUID: uuid}
		for id, b := range p.usage[day][uuid] {
			if b >= minBytes {
				nu.Users = append(nu.Users, api.UserUsage{ID: id, TotalBytes: b})
			}
		}
		out = append(out, nu)
	}
	return out, nil
}

func (p *fakePanel) set(day, node string, user int64, b int64) {
	if p.usage == nil {
		p.usage = map[string]map[string]map[int64]int64{}
	}
	if p.usage[day] == nil {
		p.usage[day] = map[string]map[int64]int64{}
	}
	if p.usage[day][node] == nil {
		p.usage[day][node] = map[int64]int64{}
	}
	p.usage[day][node][user] = b
}

type fakeStore struct {
	days       map[string]map[int64]int64
	finalized  string
	alerted    map[string]time.Duration
	expires    map[string]time.Time
	whitelist  map[int64]bool
	violations map[int64]int
}

func newStore() *fakeStore {
	return &fakeStore{days: map[string]map[int64]int64{}, alerted: map[string]time.Duration{}, expires: map[string]time.Time{}, whitelist: map[int64]bool{}, violations: map[int64]int{}}
}

func (s *fakeStore) SaveTrafficDay(_ context.Context, day string, totals map[int64]int64, _ time.Duration) error {
	s.days[day] = totals
	return nil
}

func (s *fakeStore) GetTrafficDays(_ context.Context, days []string, ids []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	for _, id := range ids {
		out[id] = make([]int64, len(days))
		for i, d := range days {
			out[id][i] = s.days[d][id]
		}
	}
	return out, nil
}

func (s *fakeStore) CountTrafficDays(_ context.Context, days []string) (int, error) {
	n := 0
	for _, d := range days {
		if _, ok := s.days[d]; ok {
			n++
		}
	}
	return n, nil
}

func (s *fakeStore) GetTrafficFinalized(context.Context) (string, error) { return s.finalized, nil }
func (s *fakeStore) SetTrafficFinalized(_ context.Context, day string) error {
	s.finalized = day
	return nil
}

func (s *fakeStore) TrafficAlerted(_ context.Context, key string) (bool, error) {
	exp, ok := s.expires[key]
	return ok && testNow.Before(exp), nil
}
func (s *fakeStore) MarkTrafficAlert(_ context.Context, key string, ttl time.Duration) error {
	s.alerted[key] = ttl
	s.expires[key] = testNow.Add(ttl)
	return nil
}
func (s *fakeStore) IsWhitelisted(_ context.Context, id int64) (bool, error) {
	return s.whitelist[id], nil
}
func (s *fakeStore) GetTrafficDaysAll(_ context.Context, days []string) ([]map[int64]int64, error) {
	out := make([]map[int64]int64, len(days))
	for i, d := range days {
		out[i] = map[int64]int64{}
		for id, v := range s.days[d] {
			out[i][id] = v
		}
	}
	return out, nil
}

func (s *fakeStore) CountUserViolations(_ context.Context, id int64, _ time.Duration) (int, error) {
	return s.violations[id], nil
}

type fakeUsers struct{}

func (fakeUsers) LookupUser(_ context.Context, id int64) (*api.CachedUser, error) {
	return &api.CachedUser{UserID: id, Username: "user" + itoa(id)}, nil
}

type sentAlert struct {
	userID int64
	text   string
}

type fakeNotifier struct {
	sent []sentAlert
	err  error
}

func (n *fakeNotifier) SendManualAlert(_ context.Context, text string, userID int64, _, _ int) error {
	if n.err != nil {
		return n.err
	}
	n.sent = append(n.sent, sentAlert{userID, text})
	return nil
}

var testNow = time.Date(2026, 11, 12, 11, 30, 0, 0, time.UTC)

func day(offset int) string { return testNow.AddDate(0, 0, offset).Format(dayLayout) }

func testConfig() *config.Config {
	return &config.Config{
		TrafficMonitor:     true,
		TrafficSpikeFactor: 5,
		TrafficSpikeMinGB:  30,
		TrafficHeavyGB:     100,
		TrafficHeavyDays:   5,
	}
}

func newTestMonitor(t *testing.T, cfg *config.Config, panel *fakePanel, store *fakeStore, n *fakeNotifier) (*Monitor, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logs)
	m := New(config.NewProvider(cfg), panel, store, fakeUsers{}, n, logger, time.UTC)
	m.now = func() time.Time { return testNow }
	return m, &logs
}

func threeNodes() []api.Node {
	return []api.Node{{UUID: "n-de", Name: "DE-1"}, {UUID: "n-nl", Name: "NL-1"}, {UUID: "n-fi", Name: "FI-1"}}
}

func ivanHistory(p *fakePanel) {
	for i, v := range []float64{3.1, 2.4, 0, 4.0, 2.8, 3.5, 2.2, 5.1, 3.0, 2.6, 0, 3.9, 3.0, 3.3} {
		p.set(day(i-14), "n-de", 1042, gbf(v))
	}
}

func TestPoll_BackfillsHistoryOnFirstRun(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	ivanHistory(panel)
	panel.set(day(-1), "n-nl", 55, gb/2)
	panel.set(day(-1), "n-nl", 56, 50<<20)
	store := newStore()
	m, _ := newTestMonitor(t, testConfig(), panel, store, &fakeNotifier{})

	m.Poll(context.Background())

	if store.finalized != day(-1) {
		t.Fatalf("закрыты сутки до %q, ожидалось %q", store.finalized, day(-1))
	}
	if len(store.days) != historyDays {
		t.Fatalf("при первом запуске нужно догрузить %d суток, сохранено %d", historyDays, len(store.days))
	}
	if store.days[day(-1)][55] != gb/2 {
		t.Error("сутки от 100 МБ — это сутки с трафиком, их нужно сохранить")
	}
	if _, stored := store.days[day(-1)][56]; stored {
		t.Error("пары меньше 100 МБ ночной запрос отсекает, их в истории быть не должно")
	}
	if _, stored := store.days[day(-12)][1042]; stored {
		t.Error("сутки без трафика не сохраняются: это отсутствие данных, а не ноль")
	}
	if got := store.days[day(-1)][1042]; got != gbf(3.3) {
		t.Errorf("вчерашний итог %d", got)
	}
	nightCalls := 0
	for _, c := range panel.calls {
		if strings.HasSuffix(c, "@"+itoa(nightFloor)) {
			nightCalls++
		}
	}
	if nightCalls != historyDays || len(panel.calls) != historyDays+1 {
		t.Errorf("ожидалось %d ночных запросов и один дневной: %v", historyDays, panel.calls)
	}

	panel.calls = nil
	m.Poll(context.Background())
	if len(panel.calls) != 1 || !strings.HasPrefix(panel.calls[0], day(0)+"@") {
		t.Errorf("повторный опрос в те же сутки — только один запрос за сегодня: %v", panel.calls)
	}
}

func TestPoll_SpikeAgainstOwnNorm(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	ivanHistory(panel)
	store := newStore()
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, testConfig(), panel, store, n)

	panel.set(day(0), "n-de", 1042, 9*gb)
	panel.set(day(0), "n-nl", 1042, 6*gb)
	m.Poll(context.Background())
	if len(n.sent) != 0 {
		t.Fatalf("15 ГБ меньше TRAFFIC_SPIKE_MIN_GB, алерта быть не должно: %v", n.sent)
	}

	panel.set(day(0), "n-de", 1042, 22*gb)
	panel.set(day(0), "n-nl", 1042, 12*gb)
	m.Poll(context.Background())
	if len(n.sent) != 1 || n.sent[0].userID != 1042 {
		t.Fatalf("ожидался один алерт по 1042: %v", n.sent)
	}
	text := n.sent[0].text
	for _, want := range []string{"📈 Необычный трафик", "34 ГБ", "к 11:30", "3.0 ГБ в день (медиана за 12 дн. с трафиком)", "×11", "DE-1 22 ГБ · NL-1 12 ГБ"} {
		if !strings.Contains(text, want) {
			t.Errorf("в алерте нет %q:\n%s", want, text)
		}
	}
	if ttl, ok := store.alerted["spike:1042"]; !ok || ttl != spikeAlertTTL {
		t.Error("после отправки должна стоять метка, чтобы не повторять алерт")
	}

	m.Poll(context.Background())
	if len(n.sent) != 1 {
		t.Errorf("второй алерт за те же сутки не нужен: %d", len(n.sent))
	}
}

func TestPoll_HeavyNormalDayDoesNotSpike(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	for i := -14; i < 0; i++ {
		panel.set(day(i), "n-de", 7, 12*gb)
	}
	panel.set(day(0), "n-de", 7, 45*gb)
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, testConfig(), panel, newStore(), n)
	m.Poll(context.Background())
	if len(n.sent) != 0 {
		t.Errorf("45 ГБ при норме 12 ГБ — меньше ×5, алерт не нужен: %v", n.sent)
	}
}

func TestPoll_SpreadUserCaughtWhenDayCloses(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	ivanHistory(panel)
	store := newStore()
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, testConfig(), panel, store, n)
	m.Poll(context.Background())

	var nodes []api.Node
	for i := 0; i < 16; i++ {
		id := "s" + itoa(int64(i))
		nodes = append(nodes, api.Node{UUID: id, Name: "S" + itoa(int64(i))})
		panel.set(day(0), id, 1042, gb*5/2)
	}
	panel.nodes = append(threeNodes(), nodes...)
	m.Poll(context.Background())
	if len(n.sent) != 0 {
		t.Fatalf("днём размазанный трафик не виден: %v", n.sent)
	}

	testNow = testNow.AddDate(0, 0, 1)
	defer func() { testNow = testNow.AddDate(0, 0, -1) }()
	m.Poll(context.Background())
	if len(n.sent) != 1 || !strings.Contains(n.sent[0].text, "📊 За сутки") || !strings.Contains(n.sent[0].text, "40 ГБ") {
		t.Fatalf("при закрытии суток всплеск должен найтись: %v", n.sent)
	}
}

func TestPoll_PersistentlyHeavyUser(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	for i, v := range []int64{0, 0, 0, 0, 0, 0, 0, 0, 120, 95, 140, 130, 110, 150} {
		panel.set(day(i-14), "n-fi", 777, v*gb)
	}
	store := newStore()
	store.violations[777] = 3
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, testConfig(), panel, store, n)

	m.Poll(context.Background())
	if len(n.sent) != 1 {
		t.Fatalf("5 тяжёлых суток из 7 — ожидался один алерт: %v", n.sent)
	}
	text := n.sent[0].text
	for _, want := range []string{"📈 Постоянно высокий трафик", "Дней от 100 ГБ", "5 из 7", "📦 За 7 дней", "745 ГБ", "Нарушений лимита за неделю</th><td>3"} {
		if !strings.Contains(text, want) {
			t.Errorf("в алерте нет %q:\n%s", want, text)
		}
	}
	if ttl := store.alerted["heavy:777"]; ttl != heavyAlertTTL {
		t.Errorf("повтор алерта о постоянном трафике — не раньше чем через 7 дней, TTL %v", ttl)
	}
}

func TestPoll_SkipsWhitelisted(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	ivanHistory(panel)
	panel.set(day(0), "n-de", 1042, 80*gb)
	store := newStore()
	store.whitelist[1042] = true
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, testConfig(), panel, store, n)
	m.Poll(context.Background())
	if len(n.sent) != 0 {
		t.Errorf("пользователь из белого списка алерт не получает: %v", n.sent)
	}
}

func TestPoll_IgnoredNodesNotRequested(t *testing.T) {
	cfg := testConfig()
	cfg.IgnoredNodeUUIDs = []string{"n-de"}
	cfg.TrafficIgnoredNodeUUIDs = []string{"n-nl"}
	panel := &fakePanel{nodes: threeNodes()}
	store := newStore()
	store.finalized = day(-1)
	m, _ := newTestMonitor(t, cfg, panel, store, &fakeNotifier{})
	m.Poll(context.Background())
	if len(panel.asked) != 1 || strings.Join(panel.asked[0], ",") != "n-fi" {
		t.Errorf("исключённые ноды не должны попадать в запрос: %v", panel.asked)
	}
}

func TestPoll_FailedSendRetriedNextPoll(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	ivanHistory(panel)
	panel.set(day(0), "n-de", 1042, 50*gb)
	store := newStore()
	n := &fakeNotifier{err: errors.New("telegram недоступен")}
	m, _ := newTestMonitor(t, testConfig(), panel, store, n)
	m.Poll(context.Background())
	if len(store.alerted) != 0 {
		t.Fatal("без успешной отправки метку ставить нельзя")
	}
	n.err = nil
	m.Poll(context.Background())
	if len(n.sent) != 1 {
		t.Errorf("после сбоя алерт должен уйти на следующем опросе: %v", n.sent)
	}
}

func TestPoll_MissingScopeWarnsOnce(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes(), err: &api.StatusError{Method: "POST", Path: "/api/bandwidth-stats/nodes/usage", Code: 403}}
	m, logs := newTestMonitor(t, testConfig(), panel, newStore(), &fakeNotifier{})
	for i := 0; i < 3; i++ {
		m.Poll(context.Background())
	}
	if n := strings.Count(logs.String(), "bandwidth-stats:node-usage"); n != 1 {
		t.Errorf("о нехватке scope нужно написать один раз, написано %d:\n%s", n, logs.String())
	}
}

func TestRun_RespectsToggle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := testConfig()
		cfg.TrafficMonitor = enabled
		panel := &fakePanel{nodes: threeNodes()}
		m, _ := newTestMonitor(t, cfg, panel, newStore(), &fakeNotifier{})
		m.logger.SetOutput(io.Discard)
		m.firstDelay = 0

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		m.Run(ctx)
		cancel()

		if enabled && len(panel.calls) == 0 {
			t.Error("при включённом мониторинге первый опрос должен состояться")
		}
		if !enabled && len(panel.calls) != 0 {
			t.Error("при выключенном мониторинге запросов к панели быть не должно")
		}
	}
}

func TestPoll_AlertBudgetIsSharedWithinOnePoll(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	const users = maxAlertsPerPass
	for id := int64(1); id <= users; id++ {
		for i := -14; i <= -2; i++ {
			panel.set(day(i), "n-de", id, 3*gb)
		}
		panel.set(day(-1), "n-de", id, 40*gb)
	}
	for id := int64(101); id <= 100+users; id++ {
		for i := -14; i <= -1; i++ {
			panel.set(day(i), "n-de", id, 3*gb)
		}
		panel.set(day(0), "n-de", id, 40*gb)
	}
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, testConfig(), panel, newStore(), n)

	m.Poll(context.Background())
	if len(n.sent) != maxAlertsPerPass {
		t.Fatalf("лимит %d алертов действует на весь опрос, ушло %d", maxAlertsPerPass, len(n.sent))
	}
	m.Poll(context.Background())
	if len(n.sent) != 2*users {
		t.Errorf("остальные алерты уходят на следующем опросе: ушло %d из %d", len(n.sent), 2*users)
	}
}

func TestMedianAndHelpers(t *testing.T) {
	if got := median([]int64{5, 1, 3}); got != 3 {
		t.Errorf("median нечётный = %d", got)
	}
	if got := median([]int64{4, 1, 3, 2}); got != 2 {
		t.Errorf("median чётный = %d", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("median пустой = %d", got)
	}
	days := daysBefore(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), 3)
	if strings.Join(days, ",") != "2026-02-27,2026-02-28,2026-03-01" {
		t.Errorf("daysBefore = %v", days)
	}
}

func TestPoll_NewUserNoSpikeWithoutHistory(t *testing.T) {
	cfg := testConfig()
	cfg.TrafficHeavyGB = 0
	panel := &fakePanel{nodes: threeNodes()}
	for i := -14; i < 0; i++ {
		panel.set(day(i), "n-de", 1, gb)
	}
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, cfg, panel, newStore(), n)

	start := testNow
	defer func() { testNow = start }()
	for d := 0; d < 5; d++ {
		testNow = start.AddDate(0, 0, d)
		panel.set(day(0), "n-de", 9, 150*gb)
		m.Poll(context.Background())
	}
	if len(n.sent) != 0 {
		t.Errorf("у новой подписки нет нормы, всплесков быть не должно: %v", n.sent)
	}
}

func TestPoll_ReturningUserNoSpike(t *testing.T) {
	cfg := testConfig()
	cfg.TrafficHeavyGB = 0
	panel := &fakePanel{nodes: threeNodes()}
	for i := -14; i < 0; i++ {
		panel.set(day(i), "n-de", 1, gb)
	}
	panel.set(day(-2), "n-de", 9, 2*gb)
	panel.set(day(-1), "n-de", 9, 3*gb)
	panel.set(day(0), "n-de", 9, 148*gb)
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, cfg, panel, newStore(), n)
	m.Poll(context.Background())
	if len(n.sent) != 0 {
		t.Errorf("при %d сутках с трафиком из 14 нормы ещё нет: %v", 2, n.sent)
	}
}

func TestPoll_SpikeRepeatsNoMoreOftenThan3Days(t *testing.T) {
	cfg := testConfig()
	cfg.TrafficHeavyGB = 0
	panel := &fakePanel{nodes: threeNodes()}
	ivanHistory(panel)
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, cfg, panel, newStore(), n)

	start := testNow
	defer func() { testNow = start }()
	for d := 0; d < 5; d++ {
		testNow = start.AddDate(0, 0, d)
		panel.set(day(0), "n-de", 1042, 150*gb)
		m.Poll(context.Background())
	}
	if len(n.sent) != 2 {
		t.Errorf("за 5 дней ожидалось 2 алерта о всплеске, получено %d", len(n.sent))
	}
}

func TestHeavyReport(t *testing.T) {
	store := newStore()
	week := []int64{1100, 1000, 1200, 900, 1300, 1000, 1000}
	for i, v := range week {
		d := day(i - 7)
		store.days[d] = map[int64]int64{
			1: v * gb,
			2: []int64{150, 50, 150, 150, 50, 150, 150}[i] * gb,
			3: []int64{150, 150, 150, 150, 0, 0, 0}[i] * gb,
			4: 200 * gb,
		}
	}
	store.whitelist[4] = true
	m, _ := newTestMonitor(t, testConfig(), &fakePanel{}, store, &fakeNotifier{})

	r, err := m.HeavyReport(context.Background())
	if err != nil || r == nil {
		t.Fatalf("ожидался раздел отчёта: %v, %v", r, err)
	}
	if r.Total != 2 || len(r.Rows) != 2 || r.Rows[0].Name != "user1" || r.Rows[1].Name != "user2" {
		t.Fatalf("в разделе A и B по убыванию трафика за неделю: %+v", r)
	}
	if r.Rows[0].Days != 7 || r.Rows[1].Days != 5 || r.Rows[1].Week != 850*gb {
		t.Errorf("дни и объёмы посчитаны неверно: %+v", r.Rows)
	}
	if r.MinDays != 5 || r.Window != 7 || r.HeavyBytes != 100*gb {
		t.Errorf("правило в заголовке: %+v", r)
	}

	cfg := testConfig()
	cfg.TrafficMonitor = false
	off, _ := newTestMonitor(t, cfg, &fakePanel{}, store, &fakeNotifier{})
	if r, _ := off.HeavyReport(context.Background()); r != nil {
		t.Error("при выключенном мониторинге трафика раздела нет")
	}
	empty, _ := newTestMonitor(t, testConfig(), &fakePanel{}, newStore(), &fakeNotifier{})
	if r, _ := empty.HeavyReport(context.Background()); r != nil {
		t.Error("без истории раздела нет")
	}
}

func TestPoll_ClosedDayAlertRetriedAfterFailedSend(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	ivanHistory(panel)
	for _, node := range []string{"n-de", "n-nl", "n-fi"} {
		panel.set(day(-1), node, 1042, 14*gb)
	}
	store := newStore()
	n := &fakeNotifier{err: errors.New("telegram недоступен")}
	m, _ := newTestMonitor(t, testConfig(), panel, store, n)

	m.Poll(context.Background())
	if store.finalized == day(-1) {
		t.Fatal("сутки нельзя закрывать, пока алерт за них не доставлен")
	}

	n.err = nil
	m.Poll(context.Background())
	if len(n.sent) != 1 || n.sent[0].userID != 1042 {
		t.Fatalf("после восстановления Telegram алерт за закрытые сутки должен уйти: %v", n.sent)
	}
	if store.finalized != day(-1) {
		t.Errorf("после доставки сутки закрываются, закрыто до %q", store.finalized)
	}

	m.Poll(context.Background())
	if len(n.sent) != 1 {
		t.Errorf("повторных алертов быть не должно: %v", n.sent)
	}
}

func TestPoll_ClosedDayAlertsAboveCapAreDeferred(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	const users = maxAlertsPerPass + 5
	for id := int64(1); id <= users; id++ {
		for i := -14; i <= -2; i++ {
			panel.set(day(i), "n-de", id, 3*gb)
		}
		for _, node := range []string{"n-de", "n-nl", "n-fi"} {
			panel.set(day(-1), node, id, 14*gb)
		}
	}
	store := newStore()
	n := &fakeNotifier{}
	m, _ := newTestMonitor(t, testConfig(), panel, store, n)

	m.Poll(context.Background())
	if len(n.sent) != maxAlertsPerPass {
		t.Fatalf("за проход уходит не больше %d алертов, ушло %d", maxAlertsPerPass, len(n.sent))
	}
	m.Poll(context.Background())
	if len(n.sent) != users {
		t.Fatalf("остаток должен уйти на следующем опросе: ушло %d из %d", len(n.sent), users)
	}
	if store.finalized != day(-1) {
		t.Errorf("после отправки всех алертов сутки закрываются, закрыто до %q", store.finalized)
	}
}

func TestPoll_FinalizedInFutureIsIgnored(t *testing.T) {
	panel := &fakePanel{nodes: threeNodes()}
	ivanHistory(panel)
	store := newStore()
	store.finalized = day(5)
	m, _ := newTestMonitor(t, testConfig(), panel, store, &fakeNotifier{})

	m.Poll(context.Background())
	if store.finalized != day(-1) {
		t.Errorf("метка из будущего не должна блокировать закрытие суток: %q", store.finalized)
	}
}

func TestHeavyReport_WindowEndsAtLastSavedDay(t *testing.T) {
	store := newStore()
	for i := -8; i <= -2; i++ {
		heavy := int64(0)
		if i <= -4 {
			heavy = 150 * gb
		}
		store.days[day(i)] = map[int64]int64{1: heavy}
	}
	m, _ := newTestMonitor(t, testConfig(), &fakePanel{}, store, &fakeNotifier{})

	r, err := m.HeavyReport(context.Background())
	if err != nil || r == nil || r.Total != 1 {
		t.Fatalf("пока вчерашние сутки не сохранены, окно заканчивается позавчера: %+v, %v", r, err)
	}

	store.days[day(-1)] = map[int64]int64{1: 0}
	r, err = m.HeavyReport(context.Background())
	if err != nil || r == nil || r.Total != 0 {
		t.Fatalf("после сохранения вчера окно сдвигается: 4 тяжёлых дня из 7 — меньше порога: %+v, %v", r, err)
	}
}
