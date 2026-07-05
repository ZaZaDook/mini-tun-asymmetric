package vpncore

import (
	"net"
	"testing"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
)

func TestSlaveEndpointIPs(t *testing.T) {
	slaves := []protocol.SlaveEndpoint{
		{IP: net.ParseIP("1.2.3.4"), Port: 7002},
		{IP: nil, Port: 7002}, // skipped
		{IP: net.ParseIP("5.6.7.8"), Port: 7002},
	}
	got := slaveEndpointIPs(slaves)
	if len(got) != 2 {
		t.Fatalf("want 2 IPs, got %d: %v", len(got), got)
	}
	if got[0].String() != "1.2.3.4" || got[1].String() != "5.6.7.8" {
		t.Errorf("unexpected IPs: %v", got)
	}
}

func TestDedupeIPs(t *testing.T) {
	in := []net.IP{
		net.ParseIP("10.0.0.1"),
		net.ParseIP("1.2.3.4"),
		net.ParseIP("10.0.0.1"), // dup
		nil,                     // skipped
		net.ParseIP("5.6.7.8"),
		net.ParseIP("1.2.3.4"), // dup
	}
	got := dedupeIPs(in)
	if len(got) != 3 {
		t.Fatalf("want 3 unique IPs, got %d: %v", len(got), got)
	}
	// order preserved: 10.0.0.1, 1.2.3.4, 5.6.7.8
	want := []string{"10.0.0.1", "1.2.3.4", "5.6.7.8"}
	for i, w := range want {
		if got[i].String() != w {
			t.Errorf("index %d: want %s, got %s", i, w, got[i])
		}
	}
}

// TestFullTunnelExclusionSet verifies the exclusion set the connect path builds
// under full tunnel contains the master, the round-robin slave, and every v3
// candidate — with duplicates collapsed. This is the C1 regression guard:
// missing a candidate black-holes its RTT probe / downlink into the tunnel.
func TestFullTunnelExclusionSet(t *testing.T) {
	master := net.ParseIP("100.100.100.100")
	rrSlave := net.ParseIP("1.2.3.4")
	candidates := []protocol.SlaveEndpoint{
		{IP: net.ParseIP("1.2.3.4"), Port: 7002}, // == rrSlave, must dedupe
		{IP: net.ParseIP("5.6.7.8"), Port: 7002},
		{IP: net.ParseIP("9.10.11.12"), Port: 7002},
	}
	set := dedupeIPs(append([]net.IP{master, rrSlave}, slaveEndpointIPs(candidates)...))
	want := map[string]bool{
		"100.100.100.100": true,
		"1.2.3.4":         true,
		"5.6.7.8":         true,
		"9.10.11.12":      true,
	}
	if len(set) != len(want) {
		t.Fatalf("want %d unique exclusions, got %d: %v", len(want), len(set), set)
	}
	for _, ip := range set {
		if !want[ip.String()] {
			t.Errorf("unexpected exclusion %s", ip)
		}
	}
}
