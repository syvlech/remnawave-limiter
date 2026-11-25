package api

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
)

type Bytes int64

func (b *Bytes) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*b = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		data = []byte(s)
		if len(data) == 0 {
			*b = 0
			return nil
		}
	}
	if n, err := strconv.ParseInt(string(data), 10, 64); err == nil {
		*b = Bytes(n)
		return nil
	}
	f, err := strconv.ParseFloat(string(data), 64)
	if err != nil {
		return err
	}
	switch {
	case math.IsNaN(f) || f <= 0:
		*b = 0
	case f >= math.MaxInt64:
		*b = Bytes(math.MaxInt64)
	default:
		*b = Bytes(f)
	}
	return nil
}
