package geoip

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/remnawave/limiter/internal/i18n"
)

const (
	defaultBaseURL = "https://download.maxmind.com/app/geoip_download"

	maxDBSize = 512 << 20

	dbFileName = "GeoLite2-ASN.mmdb"
	tmpPattern = "GeoLite2-ASN.*.mmdb.tmp"
)

func redactLicenseKey(s, key string) string {
	if key == "" {
		return s
	}
	if escaped := url.QueryEscape(key); escaped != key {
		s = strings.ReplaceAll(s, escaped, "***")
	}
	return strings.ReplaceAll(s, key, "***")
}

type Downloader struct {
	HTTPClient *http.Client
	LicenseKey string
	BaseURL    string
	Validate   func(path string) error
	Timeout    time.Duration

	maxSize int64
}

func DefaultValidate(path string) error {
	r, err := maxminddb.Open(path)
	if err != nil {
		return err
	}
	return r.Close()
}

func NeedsDownload(path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err := DefaultValidate(path); err != nil {
		return true, err
	}
	return false, nil
}

func removeStaleTemp(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if ok, _ := filepath.Match(tmpPattern, e.Name()); ok {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func (d *Downloader) Download(ctx context.Context, dstPath string) error {
	if d.LicenseKey == "" {
		return fmt.Errorf("downloader: license key is empty")
	}
	if d.Validate == nil {
		return fmt.Errorf("downloader: Validate callback is nil")
	}
	if dstPath == "" {
		return fmt.Errorf("downloader: dstPath is empty")
	}

	base := d.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	httpClient := d.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	limit := d.maxSize
	if limit <= 0 {
		limit = maxDBSize
	}

	dstDir := filepath.Dir(dstPath)
	removeStaleTemp(dstDir)

	params := url.Values{}
	params.Set("edition_id", "GeoLite2-ASN")
	params.Set("license_key", d.LicenseKey)
	params.Set("suffix", "tar.gz")
	reqURL := base + "?" + params.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("downloader: build request: %w", err)
	}
	req.Header.Set("User-Agent", "remnawave-limiter/geoip-updater")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloader: http get: %s", redactLicenseKey(err.Error(), d.LicenseKey))
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("downloader: http status %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp(dstDir, tmpPattern)
	if err != nil {
		return fmt.Errorf("downloader: create tmp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("downloader: gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("downloader: tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !strings.HasSuffix(hdr.Name, dbFileName) {
			continue
		}
		written, err := io.Copy(tmpFile, io.LimitReader(tr, limit+1))
		if err != nil {
			return fmt.Errorf("downloader: extract: %w", err)
		}
		if written > limit {
			return fmt.Errorf(i18n.T("error.geoip.db_too_large"), limit)
		}
		found = true
		break
	}

	if !found {
		return fmt.Errorf("downloader: %s not found in archive", dbFileName)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("downloader: sync tmp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("downloader: close tmp: %w", err)
	}

	if err := d.Validate(tmpPath); err != nil {
		return fmt.Errorf("downloader: validate: %w", err)
	}

	if err := os.Rename(tmpPath, dstPath); err != nil {
		return fmt.Errorf("downloader: rename: %w", err)
	}

	cleanup = nil
	return nil
}
