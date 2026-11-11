package monitor

import (
	"fmt"
	"net"
)

func subnetPrefix(ipStr string, v4 int) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ipStr
	}
	if ip4 := ip.To4(); ip4 != nil {
		return fmt.Sprintf("%s/%d", ip4.Mask(net.CIDRMask(v4, 32)), v4)
	}
	return ipStr
}
