package api

import "time"

type NodesResponse struct {
	Response []Node `json:"response"`
}

type Node struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	IsConnected bool   `json:"isConnected"`
	IsDisabled  bool   `json:"isDisabled"`
	CountryCode string `json:"countryCode"`
}

type JobResponse struct {
	Response struct {
		JobID string `json:"jobId"`
	} `json:"response"`
}

type UsersIPsResultResponse struct {
	Response struct {
		IsCompleted bool            `json:"isCompleted"`
		IsFailed    bool            `json:"isFailed"`
		Result      *UsersIPsResult `json:"result"`
	} `json:"response"`
}

type UsersIPsResult struct {
	Success  bool          `json:"success"`
	NodeUUID string        `json:"nodeUuid"`
	Users    []UserIPEntry `json:"users"`
}

type UserIPEntry struct {
	UserID int64    `json:"userId"`
	IPs    []IPInfo `json:"ips"`
}

type IPInfo struct {
	IP       string    `json:"ip"`
	LastSeen time.Time `json:"lastSeen"`
}

type UserResponse struct {
	Response UserData `json:"response"`
}

type UserData struct {
	ID              int64   `json:"id"`
	Username        string  `json:"username"`
	Status          string  `json:"status"`
	Email           *string `json:"email"`
	TelegramID      *int64  `json:"telegramId"`
	HWIDDeviceLimit *int    `json:"hwidDeviceLimit"`
	SubscriptionURL string  `json:"subscriptionUrl,omitempty"`
}

type DropConnectionsRequest struct {
	DropBy      DropBy      `json:"dropBy"`
	TargetNodes TargetNodes `json:"targetNodes"`
}

type DropBy struct {
	By      string  `json:"by"`
	UserIDs []int64 `json:"userIds,omitempty"`
}

type TargetNodes struct {
	Target string `json:"target"`
}

type ActiveIP struct {
	IP       string
	LastSeen time.Time
	NodeName string
	NodeUUID string
	ASN      uint32
	ASNOrg   string
}

type CachedUser struct {
	UserID          int64  `json:"user_id"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	TelegramID      int64  `json:"telegram_id"`
	HWIDDeviceLimit int    `json:"hwid_device_limit"`
	SubscriptionURL string `json:"subscription_url"`
}

type HWIDDevicesResponse struct {
	Response struct {
		Total   int          `json:"total"`
		Devices []HWIDDevice `json:"devices"`
	} `json:"response"`
}

type HWIDDevice struct {
	HWID        string    `json:"hwid"`
	Platform    string    `json:"platform"`
	OSVersion   string    `json:"osVersion"`
	DeviceModel string    `json:"deviceModel"`
	UserAgent   string    `json:"userAgent"`
	RequestIP   string    `json:"requestIp"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type SubscriptionHistoryResponse struct {
	Response struct {
		Total   int                   `json:"total"`
		Records []SubscriptionRequest `json:"records"`
	} `json:"response"`
}

type SubscriptionRequest struct {
	RequestAt time.Time `json:"requestAt"`
	RequestIP string    `json:"requestIp"`
	UserAgent string    `json:"userAgent"`
}

type NodesUsageRequest struct {
	NodesUUIDs []string `json:"nodesUuids"`
}

type NodesUsageResponse struct {
	Response struct {
		Nodes []NodeUsage `json:"nodes"`
	} `json:"response"`
}

type NodeUsage struct {
	UUID  string      `json:"uuid"`
	Users []UserUsage `json:"users"`
}

type UserUsage struct {
	ID         int64 `json:"id"`
	TotalBytes int64 `json:"totalBytes"`
}
