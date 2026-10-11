package alerts

import (
	"net"
	"testing"
)

func TestIsBlockedIP_internalRanges(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.1.2.3", "192.168.0.1", "169.254.169.254", "::1",
		"0.1.2.3", "100.64.0.1", "100.100.100.200", "100.127.255.254", "198.18.0.1", "198.19.255.254",
		"::ffff:100.100.100.200", "64:ff9b::100.100.100.200",
	}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = false, want true", s)
		}
	}
	allowed := []string{"8.8.8.8", "100.63.255.255", "100.128.0.1", "198.17.255.255", "198.20.0.1", "2606:4700::1111"}
	for _, s := range allowed {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = true, want false", s)
		}
	}
}
