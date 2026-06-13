package webhook

import "time"

type Payload struct {
	Event      string           `json:"event"`
	ActionMode string           `json:"action_mode"`
	User       UserPayload      `json:"user"`
	Violation  ViolationPayload `json:"violation"`
	Action     ActionPayload    `json:"action"`
	Timestamp  time.Time        `json:"timestamp"`
}

type UserPayload struct {
	UUID            string `json:"uuid"`
	UserID          string `json:"user_id"`
	Username        string `json:"username"`
	Email           string `json:"email,omitempty"`
	TelegramID      int64  `json:"telegram_id,omitempty"`
	SubscriptionURL string `json:"subscription_url,omitempty"`
}

type IPPayload struct {
	IP       string    `json:"ip"`
	NodeName string    `json:"node_name"`
	NodeUUID string    `json:"node_uuid"`
	LastSeen time.Time `json:"last_seen"`
	ASN      uint32    `json:"asn,omitempty"`
	ASNOrg   string    `json:"asn_org,omitempty"`
}

type ViolationPayload struct {
	IPs               []IPPayload `json:"ips"`
	IPCount           int         `json:"ip_count"`
	DeviceLimit       int         `json:"device_limit"`
	Tolerance         int         `json:"tolerance"`
	EffectiveLimit    int         `json:"effective_limit"`
	ViolationCount24h int64       `json:"violation_count_24h"`
	SubnetCount       int         `json:"subnet_count,omitempty"`
	ASNGroupCount     int         `json:"asn_group_count,omitempty"`
	DeviceGroupCount  int         `json:"device_group_count"`
	GroupingMode      string      `json:"grouping_mode"`
}

type ActionPayload struct {
	AutoDisableDurationMin int `json:"auto_disable_duration_min"`
}
