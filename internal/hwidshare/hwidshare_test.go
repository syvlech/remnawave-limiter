package hwidshare

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/cache"
	"github.com/remnawave/limiter/internal/config"
)

const (
	trialSquad = "47efc25d-9988-47c5-9a79-2c6a5b4add22"
	paidSquad  = "130aa6eb-51ae-4149-93ec-b78090559ff4"
	hwidX      = "hwid-xxxxxxxxxxxx"
)

var squadNames = map[string]string{trialSquad: "Trial", paidSquad: "Cluster1"}

type fakePanel struct {
	devices   []api.HWIDDevice
	users     map[int64]*api.UserData
	userErr   map[int64]error
	listErr   error
	findErr   error
	listCalls []int
	listSizes []int
	findCalls []string
	userCalls []int64
	limitErr  error
	limits    []string
}

func (p *fakePanel) SetTrafficLimit(_ context.Context, id int64, limit int64, activate bool) error {
	if p.limitErr != nil {
		return p.limitErr
	}
	p.limits = append(p.limits, fmt.Sprintf("%d=%d", id, limit))
	if u := p.users[id]; u != nil {
		u.TrafficLimitBytes = api.Bytes(limit)
	}
	return nil
}

func newPanel() *fakePanel {
	return &fakePanel{users: map[int64]*api.UserData{}, userErr: map[int64]error{}}
}

func (p *fakePanel) ListHWIDDevices(_ context.Context, start, size int) ([]api.HWIDDevice, int, error) {
	if p.listErr != nil {
		return nil, 0, p.listErr
	}
	p.listCalls = append(p.listCalls, start)
	p.listSizes = append(p.listSizes, size)
	sorted := append([]api.HWIDDevice(nil), p.devices...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })
	if start >= len(sorted) {
		return nil, len(sorted), nil
	}
	end := start + size
	if end > len(sorted) {
		end = len(sorted)
	}
	return sorted[start:end], len(sorted), nil
}

func (p *fakePanel) FindHWIDDevices(_ context.Context, hwid string, _ int) ([]api.HWIDDevice, error) {
	if p.findErr != nil {
		return nil, p.findErr
	}
	p.findCalls = append(p.findCalls, hwid)
	var out []api.HWIDDevice
	for _, d := range p.devices {
		if d.HWID == hwid {
			out = append(out, d)
		}
	}
	return out, nil
}

func (p *fakePanel) GetUserByID(_ context.Context, id int64) (*api.UserData, error) {
	p.userCalls = append(p.userCalls, id)
	if err := p.userErr[id]; err != nil {
		return nil, err
	}
	u, ok := p.users[id]
	if !ok {
		return nil, &api.StatusError{Method: http.MethodGet, Path: "/api/users", Code: http.StatusNotFound}
	}
	return u, nil
}

func (p *fakePanel) addUser(id int64, name string, tg int64, squads ...string) {
	u := &api.UserData{ID: id, Username: name, Status: "ACTIVE", CreatedAt: testNow.Add(-time.Duration(id) * time.Hour)}
	if tg != 0 {
		u.TelegramID = &tg
	}
	for _, s := range squads {
		u.ActiveInternalSquads = append(u.ActiveInternalSquads, api.InternalSquad{UUID: strings.ToUpper(s), Name: squadNames[s]})
	}
	p.users[id] = u
}

func (p *fakePanel) addDevice(hwid string, user int64, at time.Time) {
	p.devices = append(p.devices, api.HWIDDevice{
		HWID: hwid, UserID: user, Platform: "iOS", OSVersion: "18.1", DeviceModel: "iPhone15,2",
		UserAgent: "Happ/2.7.0", CreatedAt: at, UpdatedAt: at,
	})
}

func (p *fakePanel) removeUser(id int64) {
	delete(p.users, id)
	kept := p.devices[:0]
	for _, d := range p.devices {
		if d.UserID != id {
			kept = append(kept, d)
		}
	}
	p.devices = kept
}

type fakeUsers struct {
	panel  *fakePanel
	cached map[int64]*api.CachedUser
	calls  int
}

func (u *fakeUsers) LookupUser(ctx context.Context, id int64) (*api.CachedUser, error) {
	u.calls++
	if c, ok := u.cached[id]; ok {
		return c, nil
	}
	d, err := u.panel.GetUserByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("api get user: %w", err)
	}
	return api.NewCachedUser(id, d), nil
}

type fakeStore struct {
	state     cache.HWIDState
	history   map[string]map[int64]cache.HWIDRecord
	reported  map[int64]map[int64]bool
	whitelist map[int64]bool
	err       error
	recordErr error

	recordTTL     time.Duration
	seedRetention time.Duration
	markErr       error
	clock         func() time.Time
	expires       map[string]time.Time
	unmarked      int
	cuts          map[int64]cache.TrafficCut
}

func (s *fakeStore) GetTrafficCut(_ context.Context, id int64) (*cache.TrafficCut, error) {
	if s.err != nil {
		return nil, s.err
	}
	c, ok := s.cuts[id]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (s *fakeStore) SaveTrafficCut(_ context.Context, id int64, c cache.TrafficCut, _ time.Duration) error {
	if s.err != nil {
		return s.err
	}
	s.cuts[id] = c
	return nil
}

func (s *fakeStore) DeleteTrafficCut(_ context.Context, id int64) error {
	delete(s.cuts, id)
	return nil
}

func newStore() *fakeStore {
	return &fakeStore{history: map[string]map[int64]cache.HWIDRecord{}, reported: map[int64]map[int64]bool{}, whitelist: map[int64]bool{}, expires: map[string]time.Time{}, cuts: map[int64]cache.TrafficCut{}, clock: time.Now}
}

func (s *fakeStore) GetHWIDState(context.Context) (cache.HWIDState, error) { return s.state, s.err }

func (s *fakeStore) SetHWIDState(_ context.Context, st cache.HWIDState) error {
	if s.err != nil {
		return s.err
	}
	s.state = st
	return nil
}

func (s *fakeStore) HWIDRecorded(_ context.Context, recs []cache.HWIDRecord) ([]bool, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]bool, len(recs))
	for i, r := range recs {
		_, out[i] = s.history[r.HWID][r.UserID]
		if exp, ok := s.expires[r.HWID+"|"+strconv.FormatInt(r.UserID, 10)]; ok && !s.clock().Before(exp) {
			out[i] = false
		}
	}
	return out, nil
}

func (s *fakeStore) put(r cache.HWIDRecord, overwrite bool) {
	if s.history[r.HWID] == nil {
		s.history[r.HWID] = map[int64]cache.HWIDRecord{}
	}
	if _, ok := s.history[r.HWID][r.UserID]; ok && !overwrite {
		return
	}
	s.history[r.HWID][r.UserID] = r
}

func (s *fakeStore) RecordHWID(_ context.Context, r cache.HWIDRecord, ttl time.Duration) error {
	if s.err != nil {
		return s.err
	}
	if s.recordErr != nil {
		return s.recordErr
	}
	s.recordTTL = ttl
	s.expires[r.HWID+"|"+strconv.FormatInt(r.UserID, 10)] = s.clock().Add(ttl)
	s.put(r, true)
	return nil
}

func (s *fakeStore) SeedHWIDHistory(_ context.Context, recs []cache.HWIDRecord, retention time.Duration) error {
	if s.err != nil {
		return s.err
	}
	s.seedRetention = retention
	for _, r := range recs {
		s.put(r, false)
	}
	return nil
}

func (s *fakeStore) GetHWIDHistory(_ context.Context, hwid string) (map[int64]cache.HWIDRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := map[int64]cache.HWIDRecord{}
	for id, r := range s.history[hwid] {
		out[id] = r
	}
	return out, nil
}

func (s *fakeStore) GetHWIDReported(_ context.Context, id int64) (map[int64]bool, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := map[int64]bool{}
	for k := range s.reported[id] {
		out[k] = true
	}
	return out, nil
}

func (s *fakeStore) MarkHWIDReported(_ context.Context, id int64, others []int64, _ time.Duration) error {
	if s.err != nil {
		return s.err
	}
	if s.markErr != nil {
		return s.markErr
	}
	if s.reported[id] == nil {
		s.reported[id] = map[int64]bool{}
	}
	for _, o := range others {
		s.reported[id][o] = true
	}
	return nil
}

func (s *fakeStore) UnmarkHWIDReported(_ context.Context, id int64, others []int64) error {
	s.unmarked++
	for _, o := range others {
		delete(s.reported[id], o)
	}
	return nil
}

func (s *fakeStore) IsWhitelisted(_ context.Context, id int64) (bool, error) {
	return s.whitelist[id], s.err
}

type sentAlert struct {
	text    string
	userID  int64
	disable int
	ignore  int
	restore bool
}

type fakeNotifier struct {
	sent []sentAlert
	err  error
}

func (n *fakeNotifier) SendSharedHWIDAlert(_ context.Context, text string, userID int64, disable, ignore int, restore bool) error {
	if n.err != nil {
		return n.err
	}
	n.sent = append(n.sent, sentAlert{text: text, userID: userID, disable: disable, ignore: ignore, restore: restore})
	return nil
}

var testNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type harness struct {
	m        *Monitor
	users    *fakeUsers
	panel    *fakePanel
	store    *fakeStore
	notifier *fakeNotifier
	cfg      *config.Provider
	logs     *bytes.Buffer
	now      time.Time
}

func (h *harness) setDays(days int) {
	cfg := *h.cfg.Load()
	cfg.SharedHWIDHistoryDays = days
	h.cfg.Store(&cfg)
}

func newHarness(squads ...string) *harness {
	h := &harness{panel: newPanel(), store: newStore(), notifier: &fakeNotifier{}, logs: &bytes.Buffer{}, now: testNow}
	h.cfg = config.NewProvider(&config.Config{SharedHWIDMonitor: true, SharedHWIDSquadUUIDs: squads, SharedHWIDHistoryDays: 30, AutoDisableDuration: 15, IgnoreDuration: 30})
	logger := logrus.New()
	logger.SetOutput(h.logs)
	logger.SetLevel(logrus.DebugLevel)
	h.users = &fakeUsers{panel: h.panel, cached: map[int64]*api.CachedUser{}}
	h.m = New(h.cfg, h.panel, h.users, h.store, h.notifier, logger, time.UTC)
	h.m.now = func() time.Time { return h.now }
	h.store.clock = func() time.Time { return h.now }
	h.m.seedPause = 0
	return h
}

func (h *harness) poll(after time.Duration) {
	h.now = h.now.Add(after)
	h.m.Poll(context.Background())
}

func (h *harness) seeded() {
	h.m.Poll(context.Background())
	h.notifier.sent = nil
	h.panel.userCalls = nil
	h.panel.listCalls = nil
	h.panel.findCalls = nil
}

func TestPoll_FirstRunSeedsWithoutAlerts(t *testing.T) {
	h := newHarness(trialSquad)
	h.panel.addUser(1, "old", 100, paidSquad)
	h.panel.addUser(2, "trial", 200, trialSquad)
	h.panel.addDevice(hwidX, 1, testNow.Add(-48*time.Hour))
	h.panel.addDevice(hwidX, 2, testNow.Add(-time.Hour))

	h.m.Poll(context.Background())

	if len(h.notifier.sent) != 0 {
		t.Fatalf("первое заполнение истории не должно слать алерты: %d", len(h.notifier.sent))
	}
	if len(h.panel.userCalls) != 0 || len(h.panel.findCalls) != 0 {
		t.Errorf("заполнение истории — только список устройств, без запросов пользователей: %v %v", h.panel.userCalls, h.panel.findCalls)
	}
	if len(h.store.history[hwidX]) != 2 {
		t.Fatalf("оба устройства должны попасть в историю: %+v", h.store.history)
	}
	if !h.store.state.Watermark.Equal(testNow.Add(-time.Hour)) || !h.store.state.Polled.Equal(testNow) {
		t.Errorf("отметка — самое новое устройство, опрос — сейчас: %+v", h.store.state)
	}
	if !strings.Contains(h.logs.String(), "История HWID заполнена из панели") {
		t.Errorf("заполнение должно попасть в лог:\n%s", h.logs.String())
	}
}

func TestPoll_NewTrialOnKnownDevice(t *testing.T) {
	h := newHarness(trialSquad)
	h.panel.addUser(1, "paid_user", 100, paidSquad)
	h.panel.addDevice(hwidX, 1, testNow.Add(-48*time.Hour))
	h.seeded()

	h.panel.addUser(2, "new_trial", 200, trialSquad)
	h.panel.addDevice(hwidX, 2, testNow.Add(2*time.Minute))
	h.poll(5 * time.Minute)

	if len(h.notifier.sent) != 1 {
		t.Fatalf("ожидался один алерт, получено %d\n%s", len(h.notifier.sent), h.logs.String())
	}
	a := h.notifier.sent[0]
	if a.userID != 2 || a.disable != 15 || a.ignore != 30 {
		t.Errorf("кнопки алерта — для нового аккаунта с длительностями из конфига: %+v", a)
	}
	for _, want := range []string{"new_trial", "paid_user", "Cluster1", "активна", "iPhone15,2 · iOS 18.1 · Happ", "<mark>1</mark>"} {
		if !strings.Contains(a.text, want) {
			t.Errorf("в алерте нет %q:\n%s", want, a.text)
		}
	}
	rec := h.store.history[hwidX][2]
	if rec.Username != "new_trial" || rec.TelegramID != 200 {
		t.Errorf("новое устройство записывается в историю с данными пользователя: %+v", rec)
	}
	if !h.store.state.Watermark.Equal(testNow.Add(2 * time.Minute)) {
		t.Errorf("отметка должна сдвинуться на новое устройство: %v", h.store.state.Watermark)
	}

	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 {
		t.Errorf("уже разобранное устройство не должно давать повторный алерт: %d", len(h.notifier.sent))
	}
}

func TestPoll_DeletedAccountFromHistory(t *testing.T) {
	h := newHarness(trialSquad)
	h.panel.addUser(1, "first_trial", 100, trialSquad)
	h.seeded()
	h.panel.addDevice(hwidX, 1, testNow.Add(time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 0 {
		t.Fatalf("у единственного аккаунта алерта нет: %d", len(h.notifier.sent))
	}

	h.panel.removeUser(1)
	h.panel.addUser(2, "second_trial", 200, trialSquad)
	h.panel.addDevice(hwidX, 2, h.now.Add(time.Minute))
	h.poll(5 * time.Minute)

	if len(h.notifier.sent) != 1 {
		t.Fatalf("удалённый пробный аккаунт должен найтись в истории: %d\n%s", len(h.notifier.sent), h.logs.String())
	}
	text := h.notifier.sent[0].text
	if !strings.Contains(text, "<code>first_trial</code>") || !strings.Contains(text, "удалён из панели") {
		t.Errorf("удалённый аккаунт показывается по имени из истории с пометкой:\n%s", text)
	}
}

func TestPoll_DeviceRemovedButAccountExists(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "kept", 100, paidSquad)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()
	h.panel.devices = nil

	h.panel.addUser(2, "other", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.poll(5 * time.Minute)

	if len(h.notifier.sent) != 1 || !strings.Contains(h.notifier.sent[0].text, "устройство удалено") {
		t.Fatalf("аккаунт, удаливший устройство, остаётся в алерте с пометкой: %+v", h.notifier.sent)
	}
}

func TestPoll_SameTelegramIsOneCustomer(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "key_one", 555)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()

	h.panel.addUser(2, "key_two", 555)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 0 {
		t.Fatalf("ключи одного клиента с одним Telegram ID — не повод для алерта: %d", len(h.notifier.sent))
	}

	h.panel.addUser(3, "stranger", 777)
	h.panel.addDevice(hwidX, 3, h.now.Add(time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 {
		t.Fatalf("чужой Telegram ID на том же устройстве даёт алерт: %d", len(h.notifier.sent))
	}
	text := h.notifier.sent[0].text
	if !strings.Contains(text, "key_one") || !strings.Contains(text, "key_two") || strings.Contains(text, "<blockquote>") {
		t.Errorf("у stranger другой Telegram ID, скрывать нечего:\n%s", text)
	}

	h.panel.addUser(4, "key_three", 555)
	h.panel.addDevice(hwidX, 4, h.now.Add(time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 2 {
		t.Fatalf("ожидался второй алерт: %d", len(h.notifier.sent))
	}
	text = h.notifier.sent[1].text
	if strings.Contains(text, "key_one") || !strings.Contains(text, "stranger") || !strings.Contains(text, "<blockquote>") {
		t.Errorf("аккаунты с тем же Telegram ID скрыты, и об этом есть пометка:\n%s", text)
	}
}

func TestPoll_SquadFilter(t *testing.T) {
	h := newHarness(trialSquad)
	h.panel.addUser(1, "a", 100, paidSquad)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()

	h.panel.addUser(2, "b", 200, paidSquad)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 0 {
		t.Fatalf("новый аккаунт не из отслеживаемого сквада — алерта нет: %d", len(h.notifier.sent))
	}
	if len(h.panel.findCalls) != 0 {
		t.Errorf("для аккаунта вне сквадов поиск по HWID не нужен: %v", h.panel.findCalls)
	}
	if _, ok := h.store.history[hwidX][2]; !ok {
		t.Errorf("устройство вне сквадов всё равно записывается в историю")
	}

	h2 := newHarness()
	h2.panel = h.panel
	h2.m.panel = h.panel
	h2.m.users = &fakeUsers{panel: h.panel}
	h2.store.history = map[string]map[int64]cache.HWIDRecord{hwidX: {1: {HWID: hwidX, UserID: 1}}}
	h2.store.state = cache.HWIDState{Watermark: testNow, Polled: testNow}
	h2.poll(5 * time.Minute)
	if len(h2.notifier.sent) != 1 {
		t.Errorf("с пустым списком сквадов проверяются все пользователи: %d", len(h2.notifier.sent))
	}
}

func TestPoll_Whitelist(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "admin_phone", 100)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()
	h.store.whitelist[1] = true

	h.panel.addUser(2, "trial", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 0 {
		t.Fatalf("аккаунт из белого списка не считается совпадением: %d", len(h.notifier.sent))
	}

	h.store.whitelist = map[int64]bool{3: true}
	h.panel.addUser(3, "tester", 300)
	h.panel.addDevice(hwidX, 3, h.now.Add(time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 0 {
		t.Fatalf("о пользователе из белого списка алерт не шлётся: %d", len(h.notifier.sent))
	}
}

func TestPoll_SendFailureRetriesNextPoll(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "a", 100)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()
	before := h.store.state.Watermark

	h.panel.addUser(2, "b", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.panel.addUser(3, "c", 300)
	h.panel.addDevice("hwid-other-0000", 3, testNow.Add(2*time.Minute))
	h.notifier.err = errors.New("telegram down")
	h.poll(5 * time.Minute)

	if _, ok := h.store.history[hwidX][2]; ok {
		t.Fatal("устройство с неотправленным алертом не записывается в историю")
	}
	if _, ok := h.store.history["hwid-other-0000"][3]; !ok {
		t.Error("следующие устройства разбираются, даже если алерт по одному не ушёл")
	}
	if !h.store.state.Watermark.Equal(before) {
		t.Errorf("отметка не должна проходить неразобранное устройство: %v", h.store.state.Watermark)
	}

	h.notifier.err = nil
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 || h.notifier.sent[0].userID != 2 {
		t.Fatalf("на следующем опросе алерт уходит ровно один раз: %+v", h.notifier.sent)
	}
	if !h.store.state.Watermark.Equal(testNow.Add(2 * time.Minute)) {
		t.Errorf("после отправки отметка доходит до самого нового устройства: %v", h.store.state.Watermark)
	}
}

func TestPoll_RecordFailureDoesNotRepeatAlert(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "a", 100)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()

	h.panel.addUser(2, "b", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.store.recordErr = errors.New("redis readonly")
	h.poll(5 * time.Minute)
	h.store.recordErr = nil
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 {
		t.Errorf("сбой записи в историю после отправки не должен повторять алерт: %d", len(h.notifier.sent))
	}
}

func TestPoll_UserLookupFailureGivesUpAfterHours(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "a", 100)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()

	h.panel.addUser(2, "b", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.panel.userErr[2] = &api.StatusError{Code: http.StatusBadGateway}
	h.poll(5 * time.Minute)
	if _, ok := h.store.history[hwidX][2]; ok {
		t.Fatal("при сбое панели устройство откладывается")
	}

	h.poll(7 * time.Hour)
	if _, ok := h.store.history[hwidX][2]; !ok {
		t.Fatal("через несколько часов неразобранное устройство записывается, чтобы не держать отметку вечно")
	}
	if !h.store.state.Watermark.Equal(testNow.Add(time.Minute)) {
		t.Errorf("отметка проходит устройство, по которому сдались: %v", h.store.state.Watermark)
	}
	if len(h.notifier.sent) != 0 || !strings.Contains(h.logs.String(), "так и не удалось проверить") {
		t.Errorf("отказ от устройства без алерта виден в логе:\n%s", h.logs.String())
	}
}

func TestPoll_AlertBudgetDefers(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "a", 100)
	h.seeded()
	total := maxAlertsPerPoll + 5
	for i := 0; i < total; i++ {
		hwid := fmt.Sprintf("hwid-budget-%04d", i)
		h.panel.addDevice(hwid, 1, testNow.Add(-time.Hour))
		h.store.put(cache.HWIDRecord{HWID: hwid, UserID: 1}, true)
		id := int64(100 + i)
		h.panel.addUser(id, fmt.Sprintf("u%d", i), 10000+id)
		h.panel.addDevice(hwid, id, testNow.Add(time.Duration(i+1)*time.Second))
	}

	h.users.calls = 0
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != maxAlertsPerPoll {
		t.Fatalf("за опрос не больше %d алертов, получено %d", maxAlertsPerPoll, len(h.notifier.sent))
	}
	if h.users.calls != maxAlertsPerPoll {
		t.Errorf("после исчерпания лимита остальные устройства не запрашиваются: %d запросов пользователей", h.users.calls)
	}
	h.poll(7 * time.Hour)
	if len(h.notifier.sent) != total {
		t.Errorf("отложенные из-за лимита алерты уходят позже и не теряются: %d из %d", len(h.notifier.sent), total)
	}
}

func TestPoll_PagesUntilWatermark(t *testing.T) {
	h := newHarness(trialSquad)
	h.seeded()
	h.store.state = cache.HWIDState{Watermark: testNow, Polled: testNow}
	for i := 0; i < 250; i++ {
		id := int64(i + 1)
		h.panel.addUser(id, fmt.Sprintf("u%d", i), id, paidSquad)
		h.panel.addDevice(fmt.Sprintf("hwid-page-%05d", i), id, testNow.Add(time.Duration(i+1)*time.Second))
	}
	h.panel.addDevice("hwid-old-000000", 9999, testNow.Add(-time.Hour))

	h.poll(5 * time.Minute)

	if fmt.Sprint(h.panel.listCalls) != "[0 90 180]" {
		t.Errorf("страницы листаются до отметки и не дальше: %v", h.panel.listCalls)
	}
	n := 0
	for _, byUser := range h.store.history {
		n += len(byUser)
	}
	if n != 250 {
		t.Errorf("все новые устройства записаны, старое не тронуто: %d", n)
	}
}

func TestPoll_StaleStateReseeds(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "a", 100)
	h.panel.addUser(2, "b", 200)
	h.panel.addDevice(hwidX, 1, testNow.Add(-72*time.Hour))
	h.panel.addDevice(hwidX, 2, testNow.Add(-time.Hour))
	h.store.state = cache.HWIDState{Watermark: testNow.Add(-72 * time.Hour), Polled: testNow.Add(-48 * time.Hour)}

	h.poll(0)
	if len(h.notifier.sent) != 0 {
		t.Fatalf("после долгого простоя история заполняется заново без лавины алертов: %d", len(h.notifier.sent))
	}
	if _, ok := h.store.history[hwidX][2]; !ok || !h.store.state.Polled.Equal(testNow) {
		t.Errorf("заполнение после простоя пишет историю и время опроса: %+v", h.store.state)
	}
}

func TestPoll_UnavailableLoggedOnce(t *testing.T) {
	h := newHarness()
	h.store.state = cache.HWIDState{Watermark: testNow, Polled: testNow}
	h.panel.listErr = &api.StatusError{Code: http.StatusForbidden}
	h.poll(5 * time.Minute)
	h.poll(5 * time.Minute)
	if n := strings.Count(h.logs.String(), "hwid-user-devices:list"); n != 1 {
		t.Errorf("отсутствие scope логируется один раз, получено %d:\n%s", n, h.logs.String())
	}
	if !h.store.state.Polled.Equal(testNow) {
		t.Errorf("неудачный опрос не обновляет время опроса: %v", h.store.state.Polled)
	}
}

func TestPoll_StoreFailureStopsEarly(t *testing.T) {
	h := newHarness()
	h.store.err = errors.New("redis down")
	h.poll(0)
	if len(h.panel.listCalls) != 0 {
		t.Errorf("без Redis к панели не обращаемся: %v", h.panel.listCalls)
	}
}

func TestRun_RespectsToggle(t *testing.T) {
	h := newHarness()
	h.cfg.Store(&config.Config{SharedHWIDMonitor: false})
	h.m.firstDelay = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	h.m.Run(ctx)
	if len(h.panel.listCalls) != 0 {
		t.Errorf("выключенный поиск не обращается к панели: %v", h.panel.listCalls)
	}
}

func TestInSquads(t *testing.T) {
	squads := []string{trialSquad}
	if !inSquads(nil, squads) || !inSquads([]string{trialSquad}, squads) {
		t.Error("пустой список — все пользователи")
	}
	if inSquads([]string{paidSquad}, squads) || inSquads([]string{trialSquad}, nil) {
		t.Error("пользователь вне указанных сквадов не проверяется")
	}
}

func TestPoll_SamePairAlertsOnce(t *testing.T) {
	h := newHarness(trialSquad)
	h.panel.addUser(1, "paid", 100, paidSquad)
	h.panel.addDevice("hwid-phone-00000", 1, testNow.Add(-48*time.Hour))
	h.panel.addDevice("hwid-tablet-0000", 1, testNow.Add(-47*time.Hour))
	h.seeded()

	h.panel.addUser(2, "trial", 200, trialSquad)
	h.panel.addDevice("hwid-phone-00000", 2, testNow.Add(time.Minute))
	h.panel.addDevice("hwid-tablet-0000", 2, testNow.Add(2*time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 {
		t.Fatalf("второе общее устройство той же пары не даёт второго алерта: %d", len(h.notifier.sent))
	}
	if !h.store.reported[2][1] {
		t.Errorf("пара должна быть отмечена после отправки: %+v", h.store.reported)
	}

	h.panel.addUser(3, "another", 300, paidSquad)
	h.panel.addDevice("hwid-tablet-0000", 3, h.now.Add(-time.Hour))
	h.panel.addDevice("hwid-laptop-0000", 2, h.now.Add(time.Minute))
	h.panel.addDevice("hwid-laptop-0000", 3, h.now.Add(-30*time.Minute))
	h.store.put(cache.HWIDRecord{HWID: "hwid-tablet-0000", UserID: 3, SeenAt: h.now.Add(-time.Hour)}, true)
	h.store.put(cache.HWIDRecord{HWID: "hwid-laptop-0000", UserID: 3, SeenAt: h.now.Add(-30 * time.Minute)}, true)
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 2 || !strings.Contains(h.notifier.sent[1].text, "another") {
		t.Fatalf("новый аккаунт в паре даёт новый алерт: %d", len(h.notifier.sent))
	}
}

func TestPoll_OnlyEarlierAccountsMatch(t *testing.T) {
	h := newHarness()
	h.seeded()
	h.store.state = cache.HWIDState{Watermark: testNow, Polled: testNow}
	h.panel.addUser(1, "first", 100)
	h.panel.addUser(2, "second", 200)
	h.panel.addDevice(hwidX, 1, testNow.Add(time.Minute))
	h.panel.addDevice(hwidX, 2, testNow.Add(2*time.Minute))

	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 || h.notifier.sent[0].userID != 2 {
		t.Fatalf("алерт только о более новом аккаунте, а не о каждом из пары: %+v", h.notifier.sent)
	}
}

func TestPoll_SeedBoundedByRetention(t *testing.T) {
	h := newHarness()
	for i := 0; i < seedPageSize+10; i++ {
		h.panel.addDevice(fmt.Sprintf("hwid-recent-%05d", i), int64(i+1), testNow.Add(-time.Duration(i+1)*time.Minute))
	}
	for i := 0; i < seedPageSize*2; i++ {
		h.panel.addDevice(fmt.Sprintf("hwid-ancient-%05d", i), int64(100000+i), testNow.Add(-40*24*time.Hour-time.Duration(i)*time.Minute))
	}

	h.m.Poll(context.Background())

	if fmt.Sprint(h.panel.listCalls) != "[0 950]" {
		t.Errorf("заполнение листает до устройств старше срока и останавливается: %v", h.panel.listCalls)
	}
	if len(h.store.history) != seedPageSize+10 {
		t.Errorf("в историю попадают только устройства за последние 30 дней: %d", len(h.store.history))
	}
	if h.store.seedRetention != 30*24*time.Hour {
		t.Errorf("срок хранения передаётся в Redis для TTL: %v", h.store.seedRetention)
	}
	if !strings.Contains(h.logs.String(), "days=30") {
		t.Errorf("в логе заполнения виден срок:\n%s", h.logs.String())
	}
}

func TestPoll_HistoryOlderThanRetentionIgnored(t *testing.T) {
	h := newHarness()
	h.seeded()
	h.store.state = cache.HWIDState{Watermark: testNow, Polled: testNow}
	h.store.put(cache.HWIDRecord{HWID: hwidX, UserID: 1, Username: "old_trial", SeenAt: testNow.Add(-40 * 24 * time.Hour)}, true)
	h.panel.addUser(2, "new_trial", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))

	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 0 {
		t.Fatalf("запись старше срока хранения не считается совпадением: %d", len(h.notifier.sent))
	}
	if h.store.recordTTL != 30*24*time.Hour {
		t.Errorf("новая запись живёт срок хранения: %v", h.store.recordTTL)
	}

	h.setDays(90)
	h.panel.addUser(3, "third", 300)
	h.panel.addDevice(hwidX, 3, h.now.Add(time.Minute))
	h.store.history[hwidX][1] = cache.HWIDRecord{HWID: hwidX, UserID: 1, Username: "old_trial", SeenAt: testNow.Add(-40 * 24 * time.Hour)}
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 || !strings.Contains(h.notifier.sent[0].text, "old_trial") {
		t.Fatalf("с более долгим сроком та же запись снова учитывается: %+v", h.notifier.sent)
	}
}

func TestPoll_NoHistoryMode(t *testing.T) {
	h := newHarness()
	h.setDays(0)
	h.panel.addUser(1, "live", 100)
	h.panel.addDevice(hwidX, 1, testNow.Add(-48*time.Hour))
	h.panel.addDevice("hwid-other-0000", 1, testNow.Add(-time.Hour))

	h.m.Poll(context.Background())
	if fmt.Sprint(h.panel.listSizes) != "[1]" || len(h.store.history) != 0 {
		t.Fatalf("без истории первый запуск — один запрос самого нового устройства: %v, история %d", h.panel.listSizes, len(h.store.history))
	}
	if !h.store.state.Watermark.Equal(testNow.Add(-time.Hour)) {
		t.Errorf("отметка — самое новое устройство: %v", h.store.state.Watermark)
	}

	h.store.put(cache.HWIDRecord{HWID: "hwid-deleted-000", UserID: 7, Username: "gone", SeenAt: testNow.Add(-time.Hour)}, true)
	h.panel.addUser(2, "new", 200)
	h.panel.addDevice("hwid-deleted-000", 2, testNow.Add(time.Minute))
	h.panel.addDevice(hwidX, 2, testNow.Add(2*time.Minute))
	h.poll(5 * time.Minute)

	if len(h.notifier.sent) != 1 || strings.Contains(h.notifier.sent[0].text, "gone") || !strings.Contains(h.notifier.sent[0].text, "live") {
		t.Fatalf("без истории совпадения только с аккаунтами в панели: %+v", h.notifier.sent)
	}
	if h.store.recordTTL != processedTTL {
		t.Errorf("без истории устройства помнятся только как разобранные: TTL %v", h.store.recordTTL)
	}
}

func TestPoll_ReadOnlyRedisSendsNothing(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "a", 100)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()

	h.panel.addUser(2, "quiet", 200)
	h.panel.addDevice("hwid-quiet-00000", 2, testNow.Add(time.Minute))
	h.panel.addUser(3, "b", 300)
	h.panel.addDevice(hwidX, 3, testNow.Add(2*time.Minute))
	readonly := errors.New("MISCONF writes are disabled")
	h.store.markErr, h.store.recordErr = readonly, readonly

	for i := 0; i < 6; i++ {
		h.poll(5 * time.Minute)
	}
	if len(h.notifier.sent) != 0 {
		t.Fatalf("если Redis не принимает запись, алерт не отправляется вовсе: %d", len(h.notifier.sent))
	}

	h.store.markErr, h.store.recordErr = nil, nil
	h.poll(5 * time.Minute)
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 || h.notifier.sent[0].userID != 3 {
		t.Fatalf("после восстановления Redis алерт уходит ровно один раз: %+v", h.notifier.sent)
	}
}

func TestPoll_SendFailureUnmarksPair(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "a", 100)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()
	h.store.reported[2] = map[int64]bool{7: true}

	h.panel.addUser(2, "b", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.notifier.err = errors.New("telegram down")
	h.poll(5 * time.Minute)

	if h.store.reported[2][1] || !h.store.reported[2][7] {
		t.Errorf("неотправленная пара снимается, прежние отметки остаются: %+v", h.store.reported[2])
	}
}

func TestPoll_NoHistoryLongPendingIsNotRehandled(t *testing.T) {
	h := newHarness()
	h.setDays(0)
	h.seeded()
	h.store.state = cache.HWIDState{Watermark: testNow, Polled: testNow}

	h.panel.addUser(1, "stuck", 100)
	h.panel.userErr[1] = &api.StatusError{Code: http.StatusBadGateway}
	h.panel.addDevice("hwid-stuck-00000", 1, testNow.Add(time.Minute))
	h.panel.addUser(2, "fine", 200)
	h.panel.addDevice("hwid-fine-000000", 2, testNow.Add(2*time.Minute))

	h.poll(5 * time.Minute)
	calls := h.users.calls
	for i := 0; i < 30; i++ {
		h.poll(5 * time.Minute)
	}
	if extra := h.users.calls - calls; extra != 30 {
		t.Errorf("за 2.5 ч повторно запрашивается только зависшее устройство (30 раз), получено %d", extra)
	}
}

func TestPoll_BacklogIsRecordedWithoutAlerts(t *testing.T) {
	h := newHarness()
	h.panel.addUser(1, "a", 100)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()
	total := maxPages*pageSize + 50
	for i := 0; i < total; i++ {
		id := int64(1000 + i)
		h.panel.addUser(id, fmt.Sprintf("u%d", i), id)
		h.panel.addDevice(fmt.Sprintf("hwid-burst-%05d", i), id, testNow.Add(time.Duration(i+1)*time.Second))
	}
	h.panel.addUser(2, "b", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Second/2))

	h.poll(5 * time.Minute)

	if len(h.notifier.sent) != 0 || h.users.calls != 0 {
		t.Fatalf("при отставании больше одного опроса устройства записываются без проверки: алертов %d, запросов %d", len(h.notifier.sent), h.users.calls)
	}
	if _, ok := h.store.history[hwidX][2]; !ok || len(h.store.history) != total+1 {
		t.Errorf("все устройства отставания попадают в историю: %d", len(h.store.history))
	}
	if !h.store.state.Watermark.Equal(testNow.Add(time.Duration(total) * time.Second)) {
		t.Errorf("отметка переходит на самое новое устройство: %v", h.store.state.Watermark)
	}
	if !strings.Contains(h.logs.String(), "записаны в историю без алертов") {
		t.Errorf("отставание видно в логе:\n%s", h.logs.String())
	}
}

func TestPoll_EarliestSeenTimeWins(t *testing.T) {
	h := newHarness()
	h.seeded()
	h.store.state = cache.HWIDState{Watermark: testNow, Polled: testNow}
	h.panel.addUser(1, "reset_devices", 100)
	h.store.put(cache.HWIDRecord{HWID: hwidX, UserID: 1, SeenAt: testNow.Add(-24 * time.Hour)}, true)
	h.panel.addDevice(hwidX, 1, testNow.Add(3*time.Minute))
	h.panel.addUser(2, "late", 200)
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.store.expires[hwidX+"|1"] = testNow

	h.poll(5 * time.Minute)
	var forLate []sentAlert
	for _, a := range h.notifier.sent {
		if a.userID == 2 {
			forLate = append(forLate, a)
		}
	}
	if len(forLate) != 1 || !strings.Contains(forLate[0].text, "reset_devices") {
		t.Fatalf("аккаунт, у которого устройство было раньше по истории, считается совпадением: %+v", h.notifier.sent)
	}
}

func TestPoll_SameMillisecondPairAlertsOnce(t *testing.T) {
	h := newHarness()
	h.seeded()
	h.store.state = cache.HWIDState{Watermark: testNow, Polled: testNow}
	h.panel.addUser(1, "first", 100)
	h.panel.addUser(2, "second", 200)
	h.panel.addDevice(hwidX, 1, testNow.Add(time.Minute))
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))

	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 || h.notifier.sent[0].userID != 2 {
		t.Fatalf("при одинаковом времени появления алерт один, о большем id: %+v", h.notifier.sent)
	}
}

func TestPoll_StaleCacheRefetchesSquads(t *testing.T) {
	h := newHarness(trialSquad)
	h.panel.addUser(1, "paid", 100, paidSquad)
	h.panel.addDevice(hwidX, 1, testNow.Add(-time.Hour))
	h.seeded()

	h.panel.addUser(2, "trial", 200, trialSquad)
	h.users.cached[2] = &api.CachedUser{UserID: 2, Username: "trial", TelegramID: 200, Squads: []string{"Trial"}}
	h.panel.addDevice(hwidX, 2, testNow.Add(time.Minute))
	h.poll(5 * time.Minute)
	if len(h.notifier.sent) != 1 {
		t.Fatalf("запись кэша без UUID сквадов перечитывается из панели: %d", len(h.notifier.sent))
	}
}

func (h *harness) setTrafficMB(mb int) {
	cfg := *h.cfg.Load()
	cfg.SharedHWIDTrafficMB = mb
	h.cfg.Store(&cfg)
}

func trafficHarness(squads ...string) *harness {
	h := newHarness(squads...)
	h.setTrafficMB(100)
	h.panel.addUser(1, "paid_user", 100, paidSquad)
	h.panel.addDevice(hwidX, 1, testNow.Add(-48*time.Hour))
	h.seeded()
	h.panel.addUser(2, "new_trial", 200, trialSquad)
	h.panel.addDevice(hwidX, 2, testNow.Add(2*time.Minute))
	return h
}

func TestPoll_TrafficLimitedOnSharedHWID(t *testing.T) {
	h := trafficHarness(trialSquad)
	h.panel.users[2].TrafficLimitBytes = 5 << 30
	h.poll(5 * time.Minute)

	if got := strings.Join(h.panel.limits, ","); got != "2=104857600" {
		t.Fatalf("новому пробному аккаунту ставится лимит 100 МБ, вызовы %q\n%s", got, h.logs.String())
	}
	if c := h.store.cuts[2]; c.Previous != 5<<30 || c.Limit != 100<<20 {
		t.Errorf("прежний лимит сохраняется для кнопки возврата: %+v", c)
	}
	if len(h.notifier.sent) != 1 || !h.notifier.sent[0].restore {
		t.Fatalf("алерт уходит с кнопкой возврата трафика: %+v", h.notifier.sent)
	}
	for _, want := range []string{"ограничен до 100 МБ", "было 5.0 ГБ"} {
		if !strings.Contains(h.notifier.sent[0].text, want) {
			t.Errorf("в алерте нет %q:\n%s", want, h.notifier.sent[0].text)
		}
	}
}

func TestPoll_TrafficNeedsSquadFilter(t *testing.T) {
	h := trafficHarness()
	h.poll(5 * time.Minute)
	if len(h.panel.limits) != 0 {
		t.Errorf("без фильтра сквадов трафик не урезается: %v", h.panel.limits)
	}
	if len(h.notifier.sent) != 1 || h.notifier.sent[0].restore || strings.Contains(h.notifier.sent[0].text, "Трафик") {
		t.Errorf("алерт без строки о трафике и без кнопки возврата: %+v", h.notifier.sent)
	}
}

func TestPoll_TrafficOffByDefault(t *testing.T) {
	h := trafficHarness(trialSquad)
	h.setTrafficMB(0)
	h.poll(5 * time.Minute)
	if len(h.panel.limits) != 0 || len(h.notifier.sent) != 1 || h.notifier.sent[0].restore {
		t.Errorf("при 0 трафик не трогается: %v %+v", h.panel.limits, h.notifier.sent)
	}
}

func TestPoll_TrafficLowerLimitKept(t *testing.T) {
	h := trafficHarness(trialSquad)
	h.panel.users[2].TrafficLimitBytes = 50 << 20
	h.poll(5 * time.Minute)
	if len(h.panel.limits) != 0 || len(h.store.cuts) != 0 {
		t.Errorf("меньший лимит не повышается: %v %v", h.panel.limits, h.store.cuts)
	}
	if len(h.notifier.sent) != 1 || h.notifier.sent[0].restore || !strings.Contains(h.notifier.sent[0].text, "лимит уже 50 МБ") {
		t.Errorf("алерт сообщает, что лимит не тронут: %+v", h.notifier.sent)
	}
}

func TestPoll_TrafficFailureStillAlerts(t *testing.T) {
	h := trafficHarness(trialSquad)
	h.panel.limitErr = errors.New("panel down")
	h.poll(5 * time.Minute)
	if len(h.store.cuts) != 0 {
		t.Errorf("при сбое панели сохранённый лимит удаляется: %v", h.store.cuts)
	}
	if len(h.notifier.sent) != 1 || h.notifier.sent[0].restore || !strings.Contains(h.notifier.sent[0].text, "не удалось ограничить") {
		t.Errorf("алерт уходит и сообщает о сбое: %+v", h.notifier.sent)
	}
}

func TestPoll_TrafficRetryAfterSendFailureKeepsPrevious(t *testing.T) {
	h := trafficHarness(trialSquad)
	h.notifier.err = errors.New("telegram down")
	h.poll(5 * time.Minute)
	h.notifier.err = nil
	h.poll(5 * time.Minute)

	if got := strings.Join(h.panel.limits, ","); got != "2=104857600" {
		t.Errorf("лимит ставится один раз, вызовы %q", got)
	}
	if c := h.store.cuts[2]; c.Previous != 0 || c.Limit != 100<<20 {
		t.Errorf("повтор не затирает прежний лимит урезанным: %+v", c)
	}
	if len(h.notifier.sent) != 1 || !h.notifier.sent[0].restore || !strings.Contains(h.notifier.sent[0].text, "было без лимита") {
		t.Errorf("повторный алерт показывает урезание и кнопку: %+v", h.notifier.sent)
	}
}
