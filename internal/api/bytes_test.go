package api

import (
	"encoding/json"
	"testing"
)

func TestBytes_Unmarshal(t *testing.T) {
	cases := map[string]int64{
		`{"trafficLimitBytes":104857600,"userTraffic":{"usedTrafficBytes":"5368709120"}}`: 104857600,
		`{"trafficLimitBytes":"0"}`:   0,
		`{"trafficLimitBytes":null}`:  0,
		`{"trafficLimitBytes":1.5e9}`: 1500000000,
		`{}`:                          0,
	}
	for raw, want := range cases {
		var u UserData
		if err := json.Unmarshal([]byte(raw), &u); err != nil {
			t.Errorf("%s: ошибка %v", raw, err)
			continue
		}
		if int64(u.TrafficLimitBytes) != want {
			t.Errorf("%s: лимит %d, ожидалось %d", raw, u.TrafficLimitBytes, want)
		}
	}
	var u UserData
	_ = json.Unmarshal([]byte(`{"userTraffic":{"usedTrafficBytes":"5368709120"}}`), &u)
	if u.UsedTraffic() != 5368709120 {
		t.Errorf("использованный трафик строкой: %d", u.UsedTraffic())
	}
	if (*UserData)(nil).UsedTraffic() != 0 {
		t.Error("nil-пользователь даёт 0")
	}
	if err := json.Unmarshal([]byte(`{"trafficLimitBytes":"abc"}`), &u); err == nil {
		t.Error("мусор в лимите — ошибка")
	}
}
