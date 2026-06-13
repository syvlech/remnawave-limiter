package config

import (
	"fmt"
	"strconv"
	"strings"
)

type Kind int

const (
	KindInt Kind = iota
	KindFloat
	KindBool
	KindEnum
)

type Field struct {
	Key      string
	TitleKey string
	Kind     Kind
	Allowed  []string
}

var registry = []Field{
	{Key: "ACTION_MODE", TitleKey: "setting.ACTION_MODE", Kind: KindEnum, Allowed: []string{"manual", "auto"}},
	{Key: "CHECK_INTERVAL", TitleKey: "setting.CHECK_INTERVAL", Kind: KindInt},
	{Key: "ACTIVE_IP_WINDOW", TitleKey: "setting.ACTIVE_IP_WINDOW", Kind: KindInt},
	{Key: "COOLDOWN", TitleKey: "setting.COOLDOWN", Kind: KindInt},
	{Key: "TOLERANCE", TitleKey: "setting.TOLERANCE", Kind: KindInt},
	{Key: "TOLERANCE_MULTIPLIER", TitleKey: "setting.TOLERANCE_MULTIPLIER", Kind: KindFloat},
	{Key: "DEFAULT_DEVICE_LIMIT", TitleKey: "setting.DEFAULT_DEVICE_LIMIT", Kind: KindInt},
	{Key: "USER_CACHE_TTL", TitleKey: "setting.USER_CACHE_TTL", Kind: KindInt},
	{Key: "VIOLATION_THRESHOLD", TitleKey: "setting.VIOLATION_THRESHOLD", Kind: KindInt},
	{Key: "VIOLATION_THRESHOLD_WINDOW", TitleKey: "setting.VIOLATION_THRESHOLD_WINDOW", Kind: KindInt},
	{Key: "AUTO_DISABLE_DURATION", TitleKey: "setting.AUTO_DISABLE_DURATION", Kind: KindInt},
	{Key: "IGNORE_DURATION", TitleKey: "setting.IGNORE_DURATION", Kind: KindInt},
	{Key: "AUTO_NOTIFY_SOFT", TitleKey: "setting.AUTO_NOTIFY_SOFT", Kind: KindBool, Allowed: []string{"true", "false"}},
	{Key: "SUBNET_GROUPING", TitleKey: "setting.SUBNET_GROUPING", Kind: KindBool, Allowed: []string{"true", "false"}},
	{Key: "SUBNET_PREFIX_V4", TitleKey: "setting.SUBNET_PREFIX_V4", Kind: KindInt},
	{Key: "ASN_GROUPING", TitleKey: "setting.ASN_GROUPING", Kind: KindBool, Allowed: []string{"true", "false"}},
	{Key: "DAILY_REPORT", TitleKey: "setting.DAILY_REPORT", Kind: KindBool, Allowed: []string{"true", "false"}},
}

func Registry() []Field {
	out := make([]Field, len(registry))
	copy(out, registry)
	return out
}

func FieldByKey(key string) (Field, bool) {
	for _, f := range registry {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

func IsEditable(key string) bool {
	_, ok := FieldByKey(key)
	return ok
}

func ValidateRaw(key, raw string) error {
	f, ok := FieldByKey(key)
	if !ok {
		return fmt.Errorf("параметр %q нельзя менять из бота", key)
	}
	raw = strings.TrimSpace(raw)
	switch f.Kind {
	case KindInt:
		if _, err := strconv.Atoi(raw); err != nil {
			return fmt.Errorf("ожидается целое число, получено %q", raw)
		}
	case KindFloat:
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return fmt.Errorf("ожидается число, получено %q", raw)
		}
	case KindBool, KindEnum:
		for _, allowed := range f.Allowed {
			if raw == allowed {
				return nil
			}
		}
		return fmt.Errorf("ожидается одно из %v, получено %q", f.Allowed, raw)
	}
	return nil
}

func Display(cfg *Config, key string) string {
	switch key {
	case "ACTION_MODE":
		return cfg.ActionMode
	case "CHECK_INTERVAL":
		return strconv.Itoa(cfg.CheckInterval)
	case "ACTIVE_IP_WINDOW":
		return strconv.Itoa(cfg.ActiveIPWindow)
	case "COOLDOWN":
		return strconv.Itoa(cfg.Cooldown)
	case "TOLERANCE":
		return strconv.Itoa(cfg.Tolerance)
	case "TOLERANCE_MULTIPLIER":
		return strconv.FormatFloat(cfg.ToleranceMultiplier, 'g', -1, 64)
	case "DEFAULT_DEVICE_LIMIT":
		return strconv.Itoa(cfg.DefaultDeviceLimit)
	case "USER_CACHE_TTL":
		return strconv.Itoa(cfg.UserCacheTTL)
	case "VIOLATION_THRESHOLD":
		return strconv.Itoa(cfg.ViolationThreshold)
	case "VIOLATION_THRESHOLD_WINDOW":
		return strconv.Itoa(cfg.ViolationThresholdWindow)
	case "AUTO_DISABLE_DURATION":
		return strconv.Itoa(cfg.AutoDisableDuration)
	case "IGNORE_DURATION":
		return strconv.Itoa(cfg.IgnoreDuration)
	case "AUTO_NOTIFY_SOFT":
		return strconv.FormatBool(cfg.AutoNotifySoft)
	case "SUBNET_GROUPING":
		return strconv.FormatBool(cfg.SubnetGrouping)
	case "SUBNET_PREFIX_V4":
		return strconv.Itoa(cfg.SubnetPrefixV4)
	case "ASN_GROUPING":
		return strconv.FormatBool(cfg.ASNGrouping)
	case "DAILY_REPORT":
		return strconv.FormatBool(cfg.DailyReport)
	}
	return ""
}
