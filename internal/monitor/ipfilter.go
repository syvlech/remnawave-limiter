package monitor

import (
	"fmt"
	"net"
	"strings"
)

type ipFilter struct {
	exact map[string]struct{}
	nets  []*net.IPNet
}

func newIPFilter(entries []string) (*ipFilter, error) {
	f := &ipFilter{exact: make(map[string]struct{})}
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("неверный CIDR %q: %w", entry, err)
			}
			f.nets = append(f.nets, network)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("неверный IP-адрес %q", entry)
		}
		f.exact[ip.String()] = struct{}{}
	}
	return f, nil
}

func (f *ipFilter) empty() bool {
	return len(f.exact) == 0 && len(f.nets) == 0
}

func (f *ipFilter) Match(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if _, ok := f.exact[ip.String()]; ok {
		return true
	}
	for _, n := range f.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
