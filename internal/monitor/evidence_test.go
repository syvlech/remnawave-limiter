package monitor

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/geoip"
)

type mapResolver map[string]geoip.ASNInfo

func (r mapResolver) Lookup(ip string) (geoip.ASNInfo, bool) {
	info, ok := r[ip]
	return info, ok
}

type fakeEvidenceAPI struct {
	devices    []api.HWIDDevice
	history    []api.SubscriptionRequest
	devErr     error
	historyErr error
	calls      int
}

func (f *fakeEvidenceAPI) GetUserHWIDDevices(context.Context, int64) ([]api.HWIDDevice, error) {
	f.calls++
	return f.devices, f.devErr
}

func (f *fakeEvidenceAPI) GetUserSubscriptionHistory(context.Context, int64) ([]api.SubscriptionRequest, error) {
	return f.history, f.historyErr
}

func TestBuildFetchEvidence_GroupsByIPAndMarksOnline(t *testing.T) {
	base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	records := []api.SubscriptionRequest{
		{RequestAt: base, RequestIP: "1.1.1.1", UserAgent: "Happ/2.7.0/ios"},
		{RequestAt: base.Add(-time.Hour), RequestIP: "1.1.1.1", UserAgent: "v2rayNG/1.9.30"},
		{RequestAt: base.Add(-2 * time.Hour), RequestIP: "1.1.1.1", UserAgent: "happ/2.6.0"},
		{RequestAt: base.Add(-3 * time.Hour), RequestIP: "2.2.2.2", UserAgent: "Mozilla/5.0 (Windows NT 10.0)"},
		{RequestAt: base.Add(-26 * time.Hour), RequestIP: "3.3.3.3", UserAgent: ""},
	}
	active := []api.ActiveIP{{IP: "2.2.2.2"}}
	resolver := mapResolver{
		"1.1.1.1": {Number: 13335, Org: "Cloudflare"},
		"2.2.2.2": {Number: 13335, Org: "Cloudflare"},
		"3.3.3.3": {Number: 24940, Org: "Hetzner"},
	}

	ev := buildFetchEvidence(records, active, resolver)
	if ev.Total != 5 || ev.IPs != 3 || ev.ASNs != 2 {
		t.Fatalf("Total/IPs/ASNs = %d/%d/%d, ожидалось 5/3/2", ev.Total, ev.IPs, ev.ASNs)
	}
	if ev.Apps != 3 {
		t.Errorf("Apps = %d, ожидалось 3 (Happ, v2rayNG, браузер)", ev.Apps)
	}
	if ev.Span != 26*time.Hour {
		t.Errorf("Span = %v, ожидалось 26h", ev.Span)
	}
	if ev.Rows[0].IP != "1.1.1.1" || ev.Rows[0].Count != 3 {
		t.Fatalf("первым должен идти IP с наибольшим числом запросов: %+v", ev.Rows[0])
	}
	if got := strings.Join(ev.Rows[0].Apps, ","); got != "Happ,v2rayNG" {
		t.Errorf("приложения IP 1.1.1.1 = %q", got)
	}
	if ev.Rows[0].Online || !ev.Rows[1].Online || ev.Rows[1].IP != "2.2.2.2" {
		t.Errorf("онлайн должен быть отмечен только у 2.2.2.2: %+v", ev.Rows)
	}
	if ev.Rows[1].Org != "Cloudflare" || ev.Rows[1].Apps[0] != "браузер" {
		t.Errorf("строка 2.2.2.2: %+v", ev.Rows[1])
	}
}

func TestBuildFetchEvidence_EmptyAndWithoutASN(t *testing.T) {
	if buildFetchEvidence(nil, nil, geoip.NopResolver{}) != nil {
		t.Error("без записей блок не нужен")
	}
	ev := buildFetchEvidence([]api.SubscriptionRequest{{RequestIP: "1.1.1.1"}, {RequestIP: ""}}, nil, geoip.NopResolver{})
	if ev.IPs != 1 || ev.ASNs != 0 || ev.Total != 2 {
		t.Errorf("пустой IP не считается отдельным адресом, ASN без базы — 0: %+v", ev)
	}
}

func TestBuildDeviceEvidence_SortsByActivity(t *testing.T) {
	now := time.Now()
	ev := buildDeviceEvidence([]api.HWIDDevice{
		{HWID: "aaaaaaaaaaaaaaaa", UpdatedAt: now.Add(-48 * time.Hour)},
		{DeviceModel: "iPhone15,2", Platform: "iOS", OSVersion: "18.1", UserAgent: "Happ/2.7.0", UpdatedAt: now},
	})
	if len(ev.Items) != 2 || ev.Items[0].Name != "iPhone15,2 · iOS 18.1" || ev.Items[0].App != "Happ" {
		t.Fatalf("недавно активное устройство должно идти первым: %+v", ev.Items)
	}
	if ev.Items[1].Name != "aaaaaaaa…" {
		t.Errorf("без данных о себе устройство подписывается началом HWID: %q", ev.Items[1].Name)
	}
	if buildDeviceEvidence(nil) != nil {
		t.Error("без устройств блок не нужен")
	}
}

func TestDeviceName(t *testing.T) {
	for _, tc := range []struct {
		dev  api.HWIDDevice
		want string
	}{
		{api.HWIDDevice{DeviceModel: "Pixel 8", Platform: "Android", OSVersion: "14"}, "Pixel 8 · Android 14"},
		{api.HWIDDevice{Platform: "Windows", OSVersion: "Windows 11"}, "Windows 11"},
		{api.HWIDDevice{Platform: "macOS"}, "macOS"},
		{api.HWIDDevice{DeviceModel: "iPad"}, "iPad"},
		{api.HWIDDevice{HWID: "short"}, "short"},
	} {
		if got := deviceName(tc.dev); got != tc.want {
			t.Errorf("deviceName(%+v) = %q, ожидалось %q", tc.dev, got, tc.want)
		}
	}
}

func TestAppName(t *testing.T) {
	for ua, want := range map[string]string{
		"Happ/2.7.0/ios CFNetwork/1.0": "Happ",
		"v2rayNG/1.9.30":               "v2rayNG",
		"sing-box 1.10.1":              "sing-box",
		"Streisand;iOS":                "Streisand",
		"Mozilla/5.0 (iPhone)":         "браузер",
		"  ":                           "",
		"averyveryverylongclientname-without-separators": "averyveryverylongclientn",
	} {
		if got := appName(ua); got != want {
			t.Errorf("appName(%q) = %q, ожидалось %q", ua, got, want)
		}
	}
}

func TestCollectEvidence_PartialFailureAndWarnOnce(t *testing.T) {
	var logs bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logs)
	forbidden := &api.StatusError{Method: "GET", Path: "/api/hwid/devices/1", Code: 403, Body: "forbidden"}
	fake := &fakeEvidenceAPI{
		history: []api.SubscriptionRequest{{RequestIP: "1.1.1.1", UserAgent: "Happ/2"}},
		devErr:  errors.Join(errors.New("get hwid devices 1"), forbidden),
	}
	m := &Monitor{logger: logger, resolver: geoip.NopResolver{}, evidence: fake}

	for i := 0; i < 3; i++ {
		ev := m.collectEvidence(context.Background(), 1, nil)
		if ev.Fetches == nil || ev.Devices != nil {
			t.Fatalf("сбой HWID не должен убирать запросы подписки: %+v", ev)
		}
	}
	if n := strings.Count(logs.String(), "scope"); n != 1 {
		t.Errorf("о нехватке прав нужно предупредить один раз, предупреждений: %d\n%s", n, logs.String())
	}

	logs.Reset()
	fake.devErr = errors.New("connection reset")
	m.collectEvidence(context.Background(), 1, nil)
	if !strings.Contains(logs.String(), "connection reset") {
		t.Errorf("обычный сбой должен попадать в лог каждый раз: %s", logs.String())
	}
}

func TestCollectEvidence_NoSource(t *testing.T) {
	m := &Monitor{logger: logrus.New(), resolver: geoip.NopResolver{}}
	if m.collectEvidence(context.Background(), 1, nil) != nil {
		t.Error("без клиента панели признаков быть не должно")
	}
}
