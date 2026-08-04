package config

import (
	"sync"
	"testing"
)

func TestProvider_LoadReturnsLatest(t *testing.T) {
	p := NewProvider(&Config{CheckInterval: 30})
	if got := p.Load().CheckInterval; got != 30 {
		t.Fatalf("CheckInterval = %d, want 30", got)
	}

	p.Store(&Config{CheckInterval: 60})
	if got := p.Load().CheckInterval; got != 60 {
		t.Errorf("CheckInterval = %d, want 60", got)
	}
}

func TestProvider_WatchFiresOnStore(t *testing.T) {
	p := NewProvider(&Config{LogLevel: "info"})

	var seen []string
	p.Watch(func(c *Config) { seen = append(seen, c.LogLevel) })

	p.Store(&Config{LogLevel: "debug"})
	p.Store(&Config{LogLevel: "warn"})

	if len(seen) != 2 || seen[0] != "debug" || seen[1] != "warn" {
		t.Errorf("watcher saw %v, want [debug warn]", seen)
	}
}

func TestProvider_WatchIgnoresNil(t *testing.T) {
	p := NewProvider(&Config{})
	p.Watch(nil)
	p.Store(&Config{}) // не должно паниковать
}

func TestProvider_MultipleWatchers(t *testing.T) {
	p := NewProvider(&Config{})

	calls := 0
	p.Watch(func(*Config) { calls++ })
	p.Watch(func(*Config) { calls++ })
	p.Store(&Config{})

	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

// Load вызывается из каждой горутины проверки, Store — из обработчика
// /settings: гонки между ними быть не должно.
func TestProvider_ConcurrentLoadStore(t *testing.T) {
	p := NewProvider(&Config{CheckInterval: 1})
	p.Watch(func(*Config) {})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				p.Store(&Config{CheckInterval: n + 1})
				if p.Load().CheckInterval == 0 {
					t.Error("Load вернул неинициализированный конфиг")
					return
				}
			}
		}(i)
	}
	wg.Wait()
}
