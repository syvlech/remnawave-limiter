//go:build live

package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/config"
)

func TestLivePanel(t *testing.T) {
	config.LoadEnvFile("../../.env")
	baseURL, token := os.Getenv("REMNAWAVE_API_URL"), os.Getenv("REMNAWAVE_API_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("нет REMNAWAVE_API_URL или REMNAWAVE_API_TOKEN в .env")
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	status := func(err error) string {
		var se *api.StatusError
		switch {
		case err == nil:
			return "OK"
		case errors.As(err, &se) && se.Code == http.StatusForbidden:
			return "403 — у токена нет нужного scope"
		case errors.As(err, &se) && se.Code == http.StatusNotFound:
			return "404 — ручки нет (старая панель?)"
		}
		return "ошибка: " + err.Error()
	}
	size := func(v interface{}) string {
		b, _ := json.Marshal(v)
		return fmt.Sprintf("%.1f КБ", float64(len(b))/1024)
	}

	start := time.Now()
	nodes, err := c.GetNodes(ctx)
	t.Logf("GET /api/nodes: %s, %v, нод %d", status(err), time.Since(start).Round(time.Millisecond), len(nodes))
	if err != nil {
		t.Fatal("без списка нод дальше проверять нечего")
	}
	cfg := testLiveConfig()
	uuids, _ := filterNodes(nodes, cfg)

	today := dayStart(time.Now())
	dayFloor := int64(cfg.TrafficSpikeMinGB) * gib / intradayDivisor
	for _, q := range []struct {
		label string
		day   time.Time
		floor int64
	}{
		{fmt.Sprintf("сегодня, порог %d ГБ на ноду", dayFloor/gib), today, dayFloor},
		{"вчера, порог 100 МБ на ноду", today.AddDate(0, 0, -1), nightFloor},
	} {
		start = time.Now()
		res, err := c.GetNodesUsage(ctx, uuids, q.day.Format(dayLayout), q.floor)
		took := time.Since(start).Round(time.Millisecond)
		if err != nil {
			t.Logf("POST /api/bandwidth-stats/nodes/usage (%s): %s", q.label, status(err))
			continue
		}
		pairs, users := 0, map[int64]int64{}
		for _, n := range res {
			pairs += len(n.Users)
			for _, u := range n.Users {
				users[u.ID] += u.TotalBytes
			}
		}
		t.Logf("POST /api/bandwidth-stats/nodes/usage (%s): OK, %v, пар %d, пользователей %d, ответ ≈ %s",
			q.label, took, pairs, len(users), size(res))
		if q.floor == nightFloor {
			logDistribution(t, users)
		}
	}

	userID := int64(0)
	if v := os.Getenv("LIVE_USER_ID"); v != "" {
		userID, _ = strconv.ParseInt(v, 10, 64)
	} else if res, err := c.GetNodesUsage(ctx, uuids, today.AddDate(0, 0, -1).Format(dayLayout), gib); err == nil {
		var best int64
		for _, n := range res {
			for _, u := range n.Users {
				if u.TotalBytes > best {
					best, userID = u.TotalBytes, u.ID
				}
			}
		}
	}
	if userID != 0 {
		start = time.Now()
		devices, err := c.GetUserHWIDDevices(ctx, userID)
		t.Logf("GET /api/hwid/devices/{id} (user %d): %s, %v, устройств %d", userID, status(err), time.Since(start).Round(time.Millisecond), len(devices))
		start = time.Now()
		history, err := c.GetUserSubscriptionHistory(ctx, userID)
		ips, apps := map[string]bool{}, map[string]bool{}
		for _, r := range history {
			ips[r.RequestIP] = true
			apps[r.UserAgent] = true
		}
		t.Logf("GET /api/users/{id}/subscription-request-history: %s, %v, записей %d, разных IP %d, разных User-Agent %d",
			status(err), time.Since(start).Round(time.Millisecond), len(history), len(ips), len(apps))
	}

	store := newStore()
	n := &fakeNotifier{}
	m := New(config.NewProvider(cfg), c, store, fakeUsers{}, n, quiet, time.Local)
	start = time.Now()
	m.Poll(ctx)
	t.Logf("Прогон мониторинга (догрузка 14 суток + сегодня): %v, сохранено суток %d", time.Since(start).Round(time.Millisecond), len(store.days))
	stored := 0
	for _, d := range store.days {
		stored += len(d)
	}
	t.Logf("История: %d записей «пользователь × сутки» ≈ %.1f МБ в Redis", stored, float64(stored)*80/1024/1024)
	spikes, heavy := 0, 0
	for key := range store.alerted {
		if key[:5] == "heavy" {
			heavy++
		} else {
			spikes++
		}
	}
	t.Logf("С порогами по умолчанию сработало бы: всплесков %d, постоянно высокий трафик %d (лимит пачки %d)", spikes, heavy, maxAlertsPerPass)
	for i, a := range n.sent {
		if i >= 2 {
			break
		}
		t.Logf("Пример алерта (имя пользователя заменено):\n%s", a.text)
	}
}

func testLiveConfig() *config.Config {
	return &config.Config{
		TrafficMonitor:          true,
		TrafficSpikeFactor:      10,
		TrafficSpikeMinGB:       100,
		TrafficHeavyGB:          100,
		TrafficHeavyDays:        5,
		IgnoredNodeUUIDs:        splitList(os.Getenv("IGNORED_NODE_UUIDS")),
		TrafficIgnoredNodeUUIDs: splitList(os.Getenv("TRAFFIC_IGNORED_NODE_UUIDS")),
	}
}

func splitList(s string) []string {
	var out []string
	cur := ""
	for _, r := range s + "," {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		if r != ' ' {
			cur += string(r)
		}
	}
	return out
}

func logDistribution(t *testing.T, users map[int64]int64) {
	if len(users) == 0 {
		t.Log("Распределение: за вчера пользователей с трафиком нет")
		return
	}
	vals := make([]int64, 0, len(users))
	for _, v := range users {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	p := func(q float64) float64 { return float64(vals[int(q*float64(len(vals)-1))]) / float64(gib) }
	over := func(g int64) int {
		n := 0
		for _, v := range vals {
			if v >= g*gib {
				n++
			}
		}
		return n
	}
	t.Logf("Распределение за вчера (от 100 МБ на ноду), ГБ: p50 %.1f · p90 %.1f · p99 %.1f · max %.1f; от 30 ГБ — %d, от 100 ГБ — %d",
		p(0.5), p(0.9), p(0.99), p(1), over(30), over(100))
}

func TestLiveThresholds(t *testing.T) {
	config.LoadEnvFile("../../.env")
	baseURL, token := os.Getenv("REMNAWAVE_API_URL"), os.Getenv("REMNAWAVE_API_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("нет REMNAWAVE_API_URL или REMNAWAVE_API_TOKEN в .env")
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nodes, err := c.GetNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	uuids, _ := filterNodes(nodes, testLiveConfig())
	whitelist := map[int64]bool{}
	for _, s := range splitList(os.Getenv("WHITELIST_USER_IDS")) {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			whitelist[id] = true
		}
	}

	const total, simulated = 21, 7
	today := dayStart(time.Now())
	byDay := make([]map[int64]int64, total)
	for i := 0; i < total; i++ {
		d := today.AddDate(0, 0, i-total).Format(dayLayout)
		res, err := c.GetNodesUsage(ctx, uuids, d, nightFloor)
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		m := map[int64]int64{}
		for _, n := range res {
			for _, u := range n.Users {
				m[u.ID] += u.TotalBytes
			}
		}
		byDay[i] = m
	}

	type spikeHit struct {
		day         int
		id          int64
		bytes       int64
		norm        int64
		whitelisted bool
	}
	simulateSpikes := func(factor float64, minGB int64, keep bool) (int, []spikeHit) {
		count, until := 0, map[int64]int{}
		var hits []spikeHit
		for day := total - simulated; day < total; day++ {
			for id, b := range byDay[day] {
				if b < minGB*gib {
					continue
				}
				prior := make([]int64, 0, historyDays)
				for p := day - historyDays; p < day; p++ {
					prior = append(prior, byDay[p][id])
				}
				active := activeDays(prior)
				if len(active) < minActiveDays {
					continue
				}
				norm := median(active)
				th := int64(factor * float64(norm))
				if th < minGB*gib {
					th = minGB * gib
				}
				if b < th || day < until[id] {
					continue
				}
				if keep {
					hits = append(hits, spikeHit{day, id, b, norm, whitelist[id]})
				}
				if whitelist[id] {
					continue
				}
				until[id] = day + 3
				count++
			}
		}
		return count, hits
	}
	simulateHeavy := func(heavyGB int64, needDays int) (int, []int64) {
		count, until := 0, map[int64]int{}
		var ids []int64
		for day := total - simulated; day < total; day++ {
			for id, b := range byDay[day] {
				if b < heavyGB*gib || day < until[id] || whitelist[id] {
					continue
				}
				n := 0
				for p := day - heavyWindow + 1; p <= day; p++ {
					if byDay[p][id] >= heavyGB*gib {
						n++
					}
				}
				if n >= needDays {
					until[id] = day + 7
					count++
					ids = append(ids, id)
				}
			}
		}
		return count, ids
	}

	active := 0
	for _, m := range byDay[total-simulated:] {
		active += len(m)
	}
	t.Logf("Последние %d суток: в среднем %d пользователей с трафиком в сутки, белый список: %d", simulated, active/simulated, len(whitelist))
	for _, v := range []struct {
		f   float64
		min int64
	}{{5, 30}, {5, 50}, {10, 30}, {10, 50}, {5, 100}, {10, 100}} {
		n, _ := simulateSpikes(v.f, v.min, false)
		t.Logf("Всплеск ×%g и от %d ГБ: %d алертов за %d суток (%.1f в сутки)", v.f, v.min, n, simulated, float64(n)/simulated)
	}
	for _, v := range []struct {
		gb   int64
		days int
	}{{100, 5}, {100, 7}, {200, 5}, {300, 5}, {50, 5}} {
		n, ids := simulateHeavy(v.gb, v.days)
		t.Logf("Постоянно высокий: от %d ГБ в %d из 7 суток: %d алертов за %d суток, пользователи %v", v.gb, v.days, n, simulated, ids)
	}

	_, hits := simulateSpikes(5, 30, true)
	sort.Slice(hits, func(i, j int) bool { return hits[i].bytes > hits[j].bytes })
	for i, h := range hits {
		if i >= 25 {
			break
		}
		ratio := "—"
		if h.norm > 0 {
			ratio = fmt.Sprintf("×%.1f", float64(h.bytes)/float64(h.norm))
		}
		mark := ""
		if h.whitelisted {
			mark = " (в белом списке)"
		}
		t.Logf("всплеск: %s user %d — %.1f ГБ, норма %.1f ГБ, %s%s", today.AddDate(0, 0, h.day-total).Format("02.01"), h.id,
			float64(h.bytes)/float64(gib), float64(h.norm)/float64(gib), ratio, mark)
	}
}
