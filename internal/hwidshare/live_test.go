//go:build live

package hwidshare

import (
	"bytes"
	"context"
	"io"
	"os"
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

type countingPanel struct {
	Panel
	list, find, users int
}

func (p *countingPanel) ListHWIDDevices(ctx context.Context, start, size int) ([]api.HWIDDevice, int, error) {
	p.list++
	return p.Panel.ListHWIDDevices(ctx, start, size)
}

func (p *countingPanel) FindHWIDDevices(ctx context.Context, hwid string, size int) ([]api.HWIDDevice, error) {
	p.find++
	return p.Panel.FindHWIDDevices(ctx, hwid, size)
}

func (p *countingPanel) GetUserByID(ctx context.Context, id int64) (*api.UserData, error) {
	p.users++
	return p.Panel.GetUserByID(ctx, id)
}

type liveUsers struct{ panel Panel }

func (u liveUsers) LookupUser(ctx context.Context, id int64) (*api.CachedUser, error) {
	d, err := u.panel.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.NewCachedUser(id, d), nil
}

func TestLiveSharedHWID(t *testing.T) {
	config.LoadEnvFile("../../.env")
	baseURL, token := os.Getenv("REMNAWAVE_API_URL"), os.Getenv("REMNAWAVE_API_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("нет REMNAWAVE_API_URL или REMNAWAVE_API_TOKEN в .env")
	}
	hours := 24
	if v, err := strconv.Atoi(os.Getenv("LIVE_HWID_HOURS")); err == nil && v > 0 {
		hours = v
	}
	days := 30
	if v, err := strconv.Atoi(os.Getenv("SHARED_HWID_HISTORY_DAYS")); err == nil && v >= 0 {
		days = v
	}
	quiet := logrus.New()
	quiet.SetOutput(io.Discard)
	c := api.NewClient(baseURL, token)
	c.SetLogger(quiet)
	if v := os.Getenv("REMNAWAVE_COOKIES"); v != "" {
		c.SetCookies(api.ParseCookies(v))
	}
	if v := os.Getenv("REMNAWAVE_HEADERS"); v != "" {
		c.SetHeaders(api.ParseHeaders(v))
	}

	passes := []struct {
		label  string
		squads []string
	}{{"все пользователи", nil}}
	if v := strings.TrimSpace(os.Getenv("SHARED_HWID_SQUAD_UUIDS")); v != "" {
		var squads []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
				squads = append(squads, s)
			}
		}
		passes = append(passes, struct {
			label  string
			squads []string
		}{"только SHARED_HWID_SQUAD_UUIDS", squads})
	}

	for _, pass := range passes {
		t.Run(pass.label, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			panel := &countingPanel{Panel: c}
			store := newStore()
			notifier := &fakeNotifier{}
			var logs bytes.Buffer
			logger := logrus.New()
			logger.SetOutput(&logs)
			m := New(config.NewProvider(&config.Config{SharedHWIDMonitor: true, SharedHWIDSquadUUIDs: pass.squads, SharedHWIDHistoryDays: days}), panel, liveUsers{panel}, store, notifier, logger, time.UTC)

			start := time.Now()
			m.Poll(ctx)
			total := 0
			for _, byUser := range store.history {
				total += len(byUser)
			}
			t.Logf("заполнение истории за %d дн.: %d устройств, %d запросов списка, %v", days, total, panel.list, time.Since(start).Round(time.Millisecond))
			if total == 0 && days > 0 {
				t.Fatalf("история пуста, лог:\n%s", logs.String())
			}

			since := time.Now().Add(-time.Duration(hours) * time.Hour)
			replay := 0
			for hwid, byUser := range store.history {
				for id, r := range byUser {
					if r.SeenAt.After(since) {
						delete(byUser, id)
						replay++
					}
				}
				if len(byUser) == 0 {
					delete(store.history, hwid)
				}
			}
			store.state = cache.HWIDState{Watermark: since.Add(overlap), Polled: time.Now()}
			*panel = countingPanel{Panel: c}

			start = time.Now()
			m.Poll(ctx)
			t.Logf("сухой прогон за %d ч: %d новых устройств, алертов %d, запросов: список %d, поиск по HWID %d, пользователи %d, %v",
				hours, replay, len(notifier.sent), panel.list, panel.find, panel.users, time.Since(start).Round(time.Millisecond))
			sort.Slice(notifier.sent, func(i, j int) bool { return notifier.sent[i].userID < notifier.sent[j].userID })
			for _, a := range notifier.sent {
				var others []string
				for id := range store.reported[a.userID] {
					others = append(others, strconv.FormatInt(id, 10))
				}
				sort.Strings(others)
				t.Logf("  алерт по пользователю %d, другие аккаунты %s, скрыты с тем же Telegram ID: %v",
					a.userID, strings.Join(others, ","), strings.Contains(a.text, "<blockquote>"))
			}
			for _, line := range strings.Split(logs.String(), "\n") {
				if strings.Contains(line, "level=error") {
					t.Logf("ошибка монитора: %s", line)
				}
			}
		})
	}
}
