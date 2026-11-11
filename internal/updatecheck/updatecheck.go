package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/i18n"
)

const (
	DefaultAPIURL     = "https://api.github.com"
	DefaultInterval   = 24 * time.Hour
	DefaultFirstDelay = time.Minute

	requestTimeout = 15 * time.Second
	maxBodyBytes   = 1 << 20
)

type Notifier interface {
	SendUpdateNotice(ctx context.Context, current, latest, releaseURL string) error
}

type Store interface {
	GetNotifiedVersion(ctx context.Context) (string, error)
	SetNotifiedVersion(ctx context.Context, version string) error
}

type Release struct {
	Version string
	URL     string
}

type Checker struct {
	Current  string
	Repo     string
	Enabled  func() bool
	Notifier Notifier
	Store    Store
	Logger   *logrus.Logger

	APIURL     string
	HTTPClient *http.Client
	Interval   time.Duration
	FirstDelay time.Duration
}

func (c *Checker) Run(ctx context.Context) {
	delay := c.FirstDelay
	if delay <= 0 {
		delay = DefaultFirstDelay
	}
	interval := c.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if c.Enabled == nil || c.Enabled() {
			c.Check(ctx)
		}
		timer.Reset(interval)
	}
}

func (c *Checker) Check(ctx context.Context) {
	current, ok := parseVersion(c.Current)
	if !ok {
		c.Logger.WithField("version", c.Current).Debug(i18n.T("log.update_skip_dev"))
		return
	}

	rel, err := c.Latest(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.Logger.WithError(err).Warn(i18n.T("log.update_check_failed"))
		}
		return
	}
	latest, ok := parseVersion(rel.Version)
	if !ok {
		c.Logger.WithField("latest", rel.Version).Warn(i18n.T("log.update_bad_version"))
		return
	}
	if !newer(latest, current) {
		c.Logger.WithFields(logrus.Fields{"current": c.Current, "latest": rel.Version}).Debug(i18n.T("log.update_up_to_date"))
		return
	}

	notified, err := c.Store.GetNotifiedVersion(ctx)
	if err != nil {
		c.Logger.WithError(err).Warn(i18n.T("log.update_state_failed"))
		return
	}
	if notified == rel.Version {
		return
	}

	c.Logger.WithFields(logrus.Fields{"current": c.Current, "latest": rel.Version}).Info(i18n.T("log.update_available"))
	if err := c.Notifier.SendUpdateNotice(ctx, c.Current, rel.Version, rel.URL); err != nil {
		c.Logger.WithError(err).Warn(i18n.T("log.update_notify_failed"))
		return
	}
	if err := c.Store.SetNotifiedVersion(ctx, rel.Version); err != nil {
		c.Logger.WithError(err).Warn(i18n.T("log.update_store_failed"))
	}
}

func (c *Checker) Latest(ctx context.Context) (Release, error) {
	apiURL := c.APIURL
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
		strings.TrimRight(apiURL, "/")+"/repos/"+c.Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "remnawave-limiter/"+c.Current)

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		return Release{}, fmt.Errorf(i18n.T("error.update.status"), resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf(i18n.T("error.update.decode"), err)
	}

	tag := strings.TrimSpace(body.TagName)
	releaseURL := body.HTMLURL
	if !strings.HasPrefix(releaseURL, "https://github.com/"+c.Repo+"/") {
		releaseURL = "https://github.com/" + c.Repo + "/releases/tag/" + url.PathEscape(tag)
	}
	return Release{Version: strings.TrimPrefix(tag, "v"), URL: releaseURL}, nil
}

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
