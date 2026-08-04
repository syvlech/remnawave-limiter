package geoip

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

const (
	defaultBaseURL = "https://download.maxmind.com/app/geoip_download"

	maxDBSize = 512 << 20
)

func redactLicenseKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}

type Downloader struct {
	HTTPClient *http.Client
	LicenseKey string
	BaseURL    string
	Validate   func(path string) error
	Timeout    time.Duration
}

func DefaultValidate(path string) error {
	r, err := maxminddb.Open(path)
	if err != nil {
		return err
	}
	return r.Close()
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
		return fmt.Errorf("downloader: http status %d %s", resp.StatusCode, resp.Status)
	}

	dstDir := filepath.Dir(dstPath)
	tmpFile, err := os.CreateTemp(dstDir, "GeoLite2-ASN.*.mmdb.tmp")
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
		if !strings.HasSuffix(hdr.Name, "/GeoLite2-ASN.mmdb") && !strings.HasSuffix(hdr.Name, "GeoLite2-ASN.mmdb") {
			continue
		}
		written, err := io.Copy(tmpFile, io.LimitReader(tr, maxDBSize+1))
		if err != nil {
			return fmt.Errorf("downloader: extract: %w", err)
		}
		if written > maxDBSize {
			return fmt.Errorf("downloader: база больше %d байт — отброшена", maxDBSize)
		}
		found = true
		break
	}

	if !found {
		return fmt.Errorf("downloader: GeoLite2-ASN.mmdb not found in archive")
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
