package config

import "sync/atomic"

type Provider struct {
	v atomic.Pointer[Config]
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
}
