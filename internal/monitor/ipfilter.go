package monitor

import (
	"fmt"
	"net"
	"strings"

	"github.com/remnawave/limiter/internal/i18n"
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
				return nil, fmt.Errorf(i18n.T("error.monitor.cidr"), entry, err)
			}
			f.nets = append(f.nets, network)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf(i18n.T("error.monitor.ip"), entry)
		}
		f.exact[ip.String()] = struct{}{}
	}
	return f, nil
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
