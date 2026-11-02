package monitor

import (
	"math"
	"testing"
	"time"

	"github.com/remnawave/limiter/internal/config"
)

func TestNextDailyReport_KeepsWallClockAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("нет tzdata: %v", err)
	}
	m := &Monitor{location: loc}

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"переход на летнее время", time.Date(2026, 3, 28, 10, 0, 0, 0, loc), time.Date(2026, 3, 29, 9, 0, 0, 0, loc)},
		{"переход на зимнее время", time.Date(2026, 10, 24, 10, 0, 0, 0, loc), time.Date(2026, 10, 25, 9, 0, 0, 0, loc)},
		{"сегодня, если время ещё не наступило", time.Date(2026, 6, 1, 8, 0, 0, 0, loc), time.Date(2026, 6, 1, 9, 0, 0, 0, loc)},
		{"ровно в момент отправки — завтра", time.Date(2026, 6, 1, 9, 0, 0, 0, loc), time.Date(2026, 6, 2, 9, 0, 0, 0, loc)},
		{"конец месяца", time.Date(2026, 1, 31, 23, 0, 0, 0, loc), time.Date(2026, 2, 1, 9, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := m.nextDailyReport(c.now, 9, 0)
			if !got.Equal(c.want) {
				t.Errorf("nextDailyReport(%v) = %v, want %v", c.now, got, c.want)
			}
		})
	}
}

func TestEffectiveTolerance(t *testing.T) {
	cases := []struct {
		name       string
		tolerance  int
		multiplier float64
		limit      int
		want       int
	}{
		{"только фиксированный", 1, 0, 3, 1},
		{"множитель округляется вниз", 0, 0.5, 3, 1},
		{"сумма", 2, 0.5, 4, 4},
		{"NaN не даёт отрицательного порога", 0, math.NaN(), 3, 0},
		{"Inf насыщается", 0, math.Inf(1), 3, maxTolerance},
		{"огромный TOLERANCE насыщается", math.MaxInt, 0, 3, maxTolerance},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{Tolerance: c.tolerance, ToleranceMultiplier: c.multiplier}
			got := effectiveTolerance(cfg, c.limit)
			if got != c.want {
				t.Errorf("effectiveTolerance = %d, want %d", got, c.want)
			}
			if c.limit+got < c.limit {
				t.Errorf("порог переполнился: %d + %d", c.limit, got)
			}
		})
	}
}
