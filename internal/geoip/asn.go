package geoip

import (
	"fmt"
	"net/netip"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"
)

type ASNInfo struct {
	Number uint32
	Org    string
}

type Resolver interface {
	Lookup(ipStr string) (ASNInfo, bool)
}

type NopResolver struct{}

func (NopResolver) Lookup(string) (ASNInfo, bool) { return ASNInfo{}, false }

type DBResolver struct {
	mu     sync.RWMutex
	reader *maxminddb.Reader
}

func NewDBResolver(path string) (*DBResolver, error) {
	if path == "" {
		return nil, fmt.Errorf("geoip: database path is empty")
	}
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: open %s: %w", path, err)
	}
	return &DBResolver{reader: r}, nil
}

func (d *DBResolver) Reload(path string) error {
	if d == nil {
		return fmt.Errorf("geoip: Reload called on nil DBResolver")
	}
	if path == "" {
		return fmt.Errorf("geoip: Reload path is empty")
	}
	r, err := maxminddb.Open(path)
	if err != nil {
		return fmt.Errorf("geoip: reload open %s: %w", path, err)
	}
	d.mu.Lock()
	old := d.reader
	d.reader = r
	d.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (d *DBResolver) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	old := d.reader
	d.reader = nil
	d.mu.Unlock()

	if old == nil {
		return nil
	}
	return old.Close()
}

func (d *DBResolver) Lookup(ipStr string) (ASNInfo, bool) {
	if d == nil {
		return ASNInfo{}, false
	}
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		return ASNInfo{}, false
	}
	var rec struct {
		ASN uint32 `maxminddb:"autonomous_system_number"`
		Org string `maxminddb:"autonomous_system_organization"`
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.reader == nil {
		return ASNInfo{}, false
	}
	if err := d.reader.Lookup(ip.Unmap()).Decode(&rec); err != nil {
		return ASNInfo{}, false
	}
	if rec.ASN == 0 {
		return ASNInfo{}, false
	}
	return ASNInfo{Number: rec.ASN, Org: rec.Org}, true
}
