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
	UserID string   `json:"userId"`
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
	UUID            string  `json:"uuid"`
	ID              int     `json:"id"`
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
	By        string   `json:"by"`
	UserUUIDs []string `json:"userUuids,omitempty"`
}

type TargetNodes struct {
	Target string `json:"target"`
}

type DropConnectionsResponse struct {
	Response struct {
		EventSent bool `json:"eventSent"`
	} `json:"response"`
}

type UserIPAggregated struct {
	UserID    string
	ActiveIPs []ActiveIP
}

type ActiveIP struct {
	IP       string
	LastSeen time.Time
	NodeName string
	NodeUUID string
}

type CachedUser struct {
	UUID            string `json:"uuid"`
	UserID          string `json:"user_id"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	TelegramID      int64  `json:"telegram_id"`
	HWIDDeviceLimit int    `json:"hwid_device_limit"`
	Status          string `json:"status"`
	SubscriptionURL string `json:"subscription_url"`
}
