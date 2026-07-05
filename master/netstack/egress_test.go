package netstack

import (
	"net"
	"testing"
)

func TestEgressForbidden(t *testing.T) {
	cases := []struct {
		ip   string
		deny bool
	}{
		// Public — must be allowed.
		{"1.1.1.1", false},
		{"104.26.12.31", false},
		{"8.8.8.8", false},
		{"2606:4700:4700::1111", false},
		// Loopback.
		{"127.0.0.1", true},
		{"127.0.0.53", true},
		{"::1", true},
		// Unspecified.
		{"0.0.0.0", true},
		{"::", true},
		// Link-local incl. cloud metadata.
		{"169.254.169.254", true},
		{"169.254.0.1", true},
		{"fe80::1", true},
		// RFC1918 private.
		{"10.0.0.1", true},
		{"10.8.0.1", true}, // tunnel gateway (DNS handled before ACL, but still private)
		{"172.16.5.9", true},
		{"192.168.1.1", true},
		// IPv6 ULA.
		{"fd00::1", true},
		// Multicast.
		{"224.0.0.1", true},
		{"ff02::1", true},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := egressForbidden(ip); got != c.deny {
			t.Errorf("egressForbidden(%s) = %v, want %v", c.ip, got, c.deny)
		}
	}
	// nil IP must be denied.
	if !egressForbidden(nil) {
		t.Error("egressForbidden(nil) = false, want true")
	}
}
