package api

import "testing"

func TestDeviceName(t *testing.T) {
	for _, tc := range []struct {
		dev  HWIDDevice
		want string
	}{
		{HWIDDevice{DeviceModel: "Pixel 8", Platform: "Android", OSVersion: "14"}, "Pixel 8 · Android 14"},
		{HWIDDevice{Platform: "Windows", OSVersion: "Windows 11"}, "Windows 11"},
		{HWIDDevice{Platform: "macOS"}, "macOS"},
		{HWIDDevice{DeviceModel: "iPad"}, "iPad"},
		{HWIDDevice{HWID: "short"}, "short"},
	} {
		if got := tc.dev.DisplayName(); got != tc.want {
			t.Errorf("DisplayName(%+v) = %q, ожидалось %q", tc.dev, got, tc.want)
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
		if got := AppName(ua); got != want {
			t.Errorf("AppName(%q) = %q, ожидалось %q", ua, got, want)
		}
	}
}

func TestNewCachedUser_SquadUUIDs(t *testing.T) {
	cu := NewCachedUser(5, &UserData{Username: "u", ActiveInternalSquads: []InternalSquad{
		{UUID: " 47EFC25D-9988-47C5-9A79-2C6A5B4ADD22 ", Name: "Trial"},
		{UUID: "", Name: "NoID"},
	}})
	if len(cu.SquadUUIDs) != 1 || cu.SquadUUIDs[0] != "47efc25d-9988-47c5-9a79-2c6a5b4add22" {
		t.Errorf("UUID сквадов сохраняются в нижнем регистре без пустых: %v", cu.SquadUUIDs)
	}
	if cu.HWIDDeviceLimit != -1 || len(cu.Squads) != 2 {
		t.Errorf("без hwidDeviceLimit лимит -1, имена сквадов сохраняются: %+v", cu)
	}
}
