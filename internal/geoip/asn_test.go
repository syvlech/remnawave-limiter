package geoip

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNopResolver_AlwaysReturnsMiss(t *testing.T) {
	r := NopResolver{}
	info, ok := r.Lookup("8.8.8.8")
	if ok {
		t.Errorf("NopResolver.Lookup returned ok=true, want false")
	}
	if info.Number != 0 {
		t.Errorf("NopResolver.Lookup returned Number=%d, want 0", info.Number)
	}
	if info.Org != "" {
		t.Errorf("NopResolver.Lookup returned Org=%q, want empty", info.Org)
	}
}

func TestNewDBResolver_FileNotFound(t *testing.T) {
	_, err := NewDBResolver("/nonexistent/path/GeoLite2-ASN.mmdb")
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
}

func TestNewDBResolver_EmptyPath(t *testing.T) {
	_, err := NewDBResolver("")
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func TestDBResolver_Reload_NonexistentFile_ReturnsError(t *testing.T) {
	d := &DBResolver{}
	if err := d.Reload("/nonexistent/path/foo.mmdb"); err == nil {
		t.Fatal("expected error on Reload with nonexistent path, got nil")
	}
}

func TestDBResolver_Reload_EmptyPath_ReturnsError(t *testing.T) {
	d := &DBResolver{}
	if err := d.Reload(""); err == nil {
		t.Fatal("expected error on Reload with empty path, got nil")
	}
}

func mmdbString(s string) []byte {
	if len(s) < 29 {
		return append([]byte{2<<5 | byte(len(s))}, s...)
	}
	return append([]byte{2<<5 | 29, byte(len(s) - 29)}, s...)
}

func mmdbUint(typ byte, v uint64) []byte {
	var payload []byte
	for v > 0 {
		payload = append([]byte{byte(v)}, payload...)
		v >>= 8
	}
	if typ > 7 {
		return append([]byte{byte(len(payload)), typ - 7}, payload...)
	}
	return append([]byte{typ<<5 | byte(len(payload))}, payload...)
}

func mmdbMap(pairs ...[]byte) []byte {
	out := []byte{7<<5 | byte(len(pairs)/2)}
	for _, p := range pairs {
		out = append(out, p...)
	}
	return out
}

func writeTestMMDB(t *testing.T, path string, asn uint32, org string) {
	t.Helper()
	const nodeCount = 1
	ptr := nodeCount + 16
	tree := []byte{0, 0, byte(ptr), 0, 0, byte(ptr)}

	var db []byte
	db = append(db, tree...)
	db = append(db, make([]byte, 16)...)
	db = append(db, mmdbMap(
		mmdbString("autonomous_system_number"), mmdbUint(6, uint64(asn)),
		mmdbString("autonomous_system_organization"), mmdbString(org),
	)...)
	db = append(db, "\xAB\xCD\xEFMaxMind.com"...)
	db = append(db, mmdbMap(
		mmdbString("binary_format_major_version"), mmdbUint(5, 2),
		mmdbString("binary_format_minor_version"), mmdbUint(5, 0),
		mmdbString("build_epoch"), mmdbUint(9, 1),
		mmdbString("database_type"), mmdbString("Test-ASN"),
		mmdbString("description"), mmdbMap(),
		mmdbString("ip_version"), mmdbUint(5, 4),
		mmdbString("languages"), []byte{0, 11 - 7},
		mmdbString("node_count"), mmdbUint(6, nodeCount),
		mmdbString("record_size"), mmdbUint(5, 24),
	)...)

	if err := os.WriteFile(path, db, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDBResolver_LookupAndReload(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.mmdb")
	second := filepath.Join(dir, "second.mmdb")
	writeTestMMDB(t, first, 64500, "First Net")
	writeTestMMDB(t, second, 64501, "Second Net")

	d, err := NewDBResolver(first)
	if err != nil {
		t.Fatalf("NewDBResolver: %v", err)
	}
	defer d.Close()

	if info, ok := d.Lookup("203.0.113.7"); !ok || info.Number != 64500 || info.Org != "First Net" {
		t.Fatalf("Lookup до Reload = %+v, %v", info, ok)
	}
	if err := d.Reload(second); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if info, ok := d.Lookup("203.0.113.7"); !ok || info.Number != 64501 || info.Org != "Second Net" {
		t.Fatalf("Lookup после Reload = %+v, %v", info, ok)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := d.Lookup("203.0.113.7"); ok {
		t.Error("после Close поиск не должен находить записи")
	}
}

func TestDBResolver_ReloadDuringLookups(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "a.mmdb"), filepath.Join(dir, "b.mmdb")}
	writeTestMMDB(t, paths[0], 64500, "A Net")
	writeTestMMDB(t, paths[1], 64501, "B Net")

	d, err := NewDBResolver(paths[0])
	if err != nil {
		t.Fatalf("NewDBResolver: %v", err)
	}
	defer d.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var misses atomic.Int64
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				info, ok := d.Lookup("198.51.100.1")
				if !ok || (info.Org != "A Net" && info.Org != "B Net") {
					misses.Add(1)
				}
			}
		}()
	}

	for i := range 200 {
		if err := d.Reload(paths[i%2]); err != nil {
			t.Errorf("Reload: %v", err)
			break
		}
	}
	close(stop)
	wg.Wait()

	if n := misses.Load(); n != 0 {
		t.Errorf("%d поисков вернули пустой или повреждённый результат во время Reload", n)
	}
}
