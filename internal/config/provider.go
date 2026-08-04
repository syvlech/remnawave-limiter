package config

import (
	"sync"
	"sync/atomic"
)

type Provider struct {
	v atomic.Pointer[Config]

	mu       sync.RWMutex
	watchers []func(*Config)
}

func NewProvider(c *Config) *Provider {
	p := &Provider{}
	p.v.Store(c)
	return p
}

func (p *Provider) Load() *Config {
	return p.v.Load()
}

func (p *Provider) Store(c *Config) {
	p.v.Store(c)

	p.mu.RLock()
	watchers := p.watchers
	p.mu.RUnlock()

	for _, w := range watchers {
		w(c)
	}
}

func (p *Provider) Watch(fn func(*Config)) {
	if fn == nil {
		return
	}
	p.mu.Lock()
	p.watchers = append(p.watchers, fn)
	p.mu.Unlock()
}
