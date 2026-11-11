package geoip

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func buildFakeArchive(t *testing.T, mmdbPayload []byte, includeMMDB bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&tar.Header{
		Name: "GeoLite2-ASN_20260101/README.txt",
		Mode: 0644,
		Size: int64(len("ignored")),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("ignored")); err != nil {
		t.Fatal(err)
	}

	if includeMMDB {
		if err := tw.WriteHeader(&tar.Header{
			Name: "GeoLite2-ASN_20260101/GeoLite2-ASN.mmdb",
			Mode: 0644,
			Size: int64(len(mmdbPayload)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(mmdbPayload); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownloader_Download_Success(t *testing.T) {
	fakePayload := []byte("fake-mmdb-contents")
	archive := buildFakeArchive(t, fakePayload, true)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("license_key"); got != "abc123" {
			t.Errorf("license_key = %q, want abc123", got)
		}
		if got := r.URL.Query().Get("edition_id"); got != "GeoLite2-ASN" {
			t.Errorf("edition_id = %q, want GeoLite2-ASN", got)
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(archive)
	}))
	defer ts.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "GeoLite2-ASN.mmdb")

	d := &Downloader{
		HTTPClient: ts.Client(),
		LicenseKey: "abc123",
		BaseURL:    ts.URL,
		Validate:   func(path string) error { return nil },
		Timeout:    5 * time.Second,
	}

	if err := d.Download(context.Background(), dst); err != nil {
		t.Fatalf("Download error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, fakePayload) {
		t.Errorf("dst contents = %q, want %q", got, fakePayload)
	}
}

func TestDownloader_Download_HTTPError_4xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("bad key"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "GeoLite2-ASN.mmdb")

	d := &Downloader{
		HTTPClient: ts.Client(),
		LicenseKey: "wrong",
		BaseURL:    ts.URL,
		Validate:   func(string) error { return nil },
		Timeout:    5 * time.Second,
	}

	err := d.Download(context.Background(), dst)
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "http status 401 Unauthorized") {
		t.Errorf("error = %v, want containing \"http status 401 Unauthorized\"", err)
	}
	if strings.Count(err.Error(), "401") != 1 {
		t.Errorf("error = %v, status code must be printed once", err)
	}

	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("dst exists, want non-existent on download failure")
	}
}

func TestDownloader_Download_MissingMMDB(t *testing.T) {
	archive := buildFakeArchive(t, nil, false)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer ts.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "GeoLite2-ASN.mmdb")

	d := &Downloader{
		HTTPClient: ts.Client(),
		LicenseKey: "abc",
		BaseURL:    ts.URL,
		Validate:   func(string) error { return nil },
		Timeout:    5 * time.Second,
	}

	err := d.Download(context.Background(), dst)
	if err == nil {
		t.Fatal("expected error when archive has no .mmdb, got nil")
	}
}

func TestDownloader_Download_ValidateFailure_PreservesOldFile(t *testing.T) {
	archive := buildFakeArchive(t, []byte("new-contents"), true)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer ts.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "GeoLite2-ASN.mmdb")
	if err := os.WriteFile(dst, []byte("old-contents"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Downloader{
		HTTPClient: ts.Client(),
		LicenseKey: "abc",
		BaseURL:    ts.URL,
		Validate:   func(string) error { return os.ErrInvalid },
		Timeout:    5 * time.Second,
	}

	err := d.Download(context.Background(), dst)
	if err == nil {
		t.Fatal("expected error on validate failure, got nil")
	}

	got, _ := os.ReadFile(dst)
	if string(got) != "old-contents" {
		t.Errorf("dst contents = %q, want %q (old file must be preserved)", got, "old-contents")
	}
}

func TestDownloader_Download_ContextCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Write([]byte("slow"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "GeoLite2-ASN.mmdb")

	d := &Downloader{
		HTTPClient: ts.Client(),
		LicenseKey: "abc",
		BaseURL:    ts.URL,
		Validate:   func(string) error { return nil },
		Timeout:    5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := d.Download(ctx, dst)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

func buildArchiveWithEntry(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func archiveServer(t *testing.T, archive []byte) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func testDownloader(ts *httptest.Server) *Downloader {
	return &Downloader{
		HTTPClient: ts.Client(),
		LicenseKey: "abc",
		BaseURL:    ts.URL,
		Validate:   func(string) error { return nil },
		Timeout:    5 * time.Second,
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestDownloader_Download_SizeLimit(t *testing.T) {
	const limit = 1024
	for _, tc := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"ровно предел", limit, false},
		{"на байт больше", limit + 1, true},
		{"намного больше", 64 * limit, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte("m"), tc.size)
			ts := archiveServer(t, buildFakeArchive(t, payload, true))

			dir := t.TempDir()
			dst := filepath.Join(dir, "GeoLite2-ASN.mmdb")
			if err := os.WriteFile(dst, []byte("old-contents"), 0o644); err != nil {
				t.Fatal(err)
			}

			validated := false
			d := testDownloader(ts)
			d.maxSize = limit
			d.Validate = func(string) error { validated = true; return nil }

			err := d.Download(context.Background(), dst)
			if (err != nil) != tc.wantErr {
				t.Fatalf("база %d байт при пределе %d: ошибка %v", tc.size, limit, err)
			}
			got, readErr := os.ReadFile(dst)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.wantErr {
				if !strings.Contains(err.Error(), "1024") {
					t.Errorf("в ошибке должен быть предел: %v", err)
				}
				if string(got) != "old-contents" {
					t.Errorf("слишком большая база не должна заменять старую, в файле %d байт", len(got))
				}
				if validated {
					t.Error("слишком большая база не должна доходить до проверки")
				}
			} else if !bytes.Equal(got, payload) {
				t.Errorf("база на пределе должна сохраниться, в файле %d байт", len(got))
			}
			if names := dirNames(t, dir); len(names) != 1 {
				t.Errorf("временные файлы должны быть убраны: %v", names)
			}
		})
	}
}

func TestDownloader_DefaultSizeLimit(t *testing.T) {
	ts := archiveServer(t, buildFakeArchive(t, bytes.Repeat([]byte("m"), 4096), true))
	dst := filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb")
	if err := testDownloader(ts).Download(context.Background(), dst); err != nil {
		t.Fatalf("без переопределения действует предел maxDBSize: %v", err)
	}
}

func TestDownloader_Download_RemovesStaleTempFiles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"успешная загрузка", http.StatusOK},
		{"загрузка не удалась", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := buildFakeArchive(t, []byte("new-contents"), true)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write(archive)
			}))
			defer ts.Close()

			dir := t.TempDir()
			dst := filepath.Join(dir, "GeoLite2-ASN.mmdb")
			keep := []string{
				"GeoLite2-ASN.mmdb",
				"GeoLite2-ASN.mmdb.bak",
				"GeoLite2-City.123.mmdb.tmp",
				"notes.tmp",
			}
			stale := []string{"GeoLite2-ASN.1234567890.mmdb.tmp", "GeoLite2-ASN.42.mmdb.tmp"}
			for _, name := range append(append([]string{}, keep...), stale...) {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("old-contents"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			keptDir := "GeoLite2-ASN.dir.mmdb.tmp"
			if err := os.Mkdir(filepath.Join(dir, keptDir), 0o755); err != nil {
				t.Fatal(err)
			}

			err := testDownloader(ts).Download(context.Background(), dst)
			if (err != nil) != (tc.status != http.StatusOK) {
				t.Fatalf("Download: %v", err)
			}

			want := append(append([]string{}, keep...), keptDir)
			sort.Strings(want)
			if got := dirNames(t, dir); strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("в каталоге %v, ожидалось %v", got, want)
			}
			wantDB := "new-contents"
			if err != nil {
				wantDB = "old-contents"
			}
			if got, _ := os.ReadFile(dst); string(got) != wantDB {
				t.Errorf("база = %q, ожидалось %q", got, wantDB)
			}
		})
	}
}

func TestDownloader_Download_ArchiveEntryNames(t *testing.T) {
	for _, tc := range []struct {
		entry string
		found bool
	}{
		{"GeoLite2-ASN_20260101/GeoLite2-ASN.mmdb", true},
		{"GeoLite2-ASN.mmdb", true},
		{"./GeoLite2-ASN.mmdb", true},
		{"GeoLite2-ASN_20260101/GeoLite2-ASN.mmdb.sha256", false},
		{"GeoLite2-ASN_20260101/GeoLite2-City.mmdb", false},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			ts := archiveServer(t, buildArchiveWithEntry(t, tc.entry, []byte("payload")))
			dst := filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb")

			err := testDownloader(ts).Download(context.Background(), dst)
			if (err == nil) != tc.found {
				t.Fatalf("запись %q: ошибка %v", tc.entry, err)
			}
			if !tc.found && !strings.Contains(err.Error(), "not found in archive") {
				t.Errorf("ожидалась ошибка «not found in archive», получено %v", err)
			}
		})
	}
}

func TestRedactLicenseKey(t *testing.T) {
	for _, tc := range []struct {
		name, in, key, want string
	}{
		{"пустой ключ", "license_key=abc", "", "license_key=abc"},
		{"простой ключ", `Get "https://x/?license_key=abc123&suffix=tar.gz": EOF`, "abc123", `Get "https://x/?license_key=***&suffix=tar.gz": EOF`},
		{"ключ встречается дважды", "abc123 and abc123", "abc123", "*** and ***"},
		{"ключ в URL закодирован", `Get "https://x/?license_key=a%2Bb%2Fc%3D+d&suffix=tar.gz": EOF`, "a+b/c= d", `Get "https://x/?license_key=***&suffix=tar.gz": EOF`},
		{"обе формы", "raw a+b/c= d, escaped a%2Bb%2Fc%3D+d", "a+b/c= d", "raw ***, escaped ***"},
		{"процент в ключе", "license_key=a%25b", "a%b", "license_key=***"},
		{"ключа в строке нет", "dial tcp: timeout", "abc123", "dial tcp: timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactLicenseKey(tc.in, tc.key); got != tc.want {
				t.Errorf("redactLicenseKey(%q, %q) = %q, ожидалось %q", tc.in, tc.key, got, tc.want)
			}
		})
	}
}

func TestDownloader_Download_TransportErrorHidesLicenseKey(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	closedURL := ts.URL
	ts.Close()

	const key = "se+cr/et= key"
	d := &Downloader{
		LicenseKey: key,
		BaseURL:    closedURL,
		Validate:   func(string) error { return nil },
		Timeout:    5 * time.Second,
	}
	err := d.Download(context.Background(), filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb"))
	if err == nil {
		t.Fatal("ожидалась ошибка соединения")
	}
	for _, leak := range []string{key, "se%2Bcr%2Fet%3D+key", "se%2Bcr", "et= key"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("лицензионный ключ попал в ошибку (%q): %v", leak, err)
		}
	}
	if !strings.Contains(err.Error(), "license_key=***") {
		t.Errorf("ключ должен быть заменён на ***: %v", err)
	}
}

func TestNeedsDownload(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "valid.mmdb")
	writeTestMMDB(t, valid, 64500, "Test Net")
	full, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}

	truncated := filepath.Join(dir, "truncated.mmdb")
	if err := os.WriteFile(truncated, full[:len(full)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.mmdb")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	garbage := filepath.Join(dir, "garbage.mmdb")
	if err := os.WriteFile(garbage, bytes.Repeat([]byte("not a database "), 100), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		path       string
		need       bool
		wantReason bool
	}{
		{"файла нет", filepath.Join(dir, "missing.mmdb"), true, false},
		{"исправная база", valid, false, false},
		{"обрезанная база", truncated, true, true},
		{"пустой файл", empty, true, true},
		{"мусор", garbage, true, true},
		{"каталог вместо файла", dir, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			need, reason := NeedsDownload(tc.path)
			if need != tc.need || (reason != nil) != tc.wantReason {
				t.Errorf("NeedsDownload = %v, %v; ожидалось %v, причина: %v", need, reason, tc.need, tc.wantReason)
			}
		})
	}

	if _, err := NewDBResolver(valid); err != nil {
		t.Errorf("база, признанная исправной, должна открываться: %v", err)
	}
}

func TestNeedsDownload_CorruptFileIsReplacedByDownload(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mmdb")
	writeTestMMDB(t, source, 64500, "Test Net")
	good, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	ts := archiveServer(t, buildFakeArchive(t, good, true))

	dst := filepath.Join(dir, "GeoLite2-ASN.mmdb")
	if err := os.WriteFile(dst, good[:10], 0o644); err != nil {
		t.Fatal(err)
	}
	if need, reason := NeedsDownload(dst); !need || reason == nil {
		t.Fatalf("обрезанная база должна требовать загрузки: %v, %v", need, reason)
	}

	d := testDownloader(ts)
	d.Validate = DefaultValidate
	if err := d.Download(context.Background(), dst); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if need, reason := NeedsDownload(dst); need || reason != nil {
		t.Errorf("после загрузки база должна быть исправной: %v, %v", need, reason)
	}
	db, err := NewDBResolver(dst)
	if err != nil {
		t.Fatalf("NewDBResolver: %v", err)
	}
	defer db.Close()
	if info, ok := db.Lookup("203.0.113.7"); !ok || info.Number != 64500 {
		t.Errorf("Lookup = %+v, %v", info, ok)
	}
}
