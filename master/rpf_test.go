package main

import (
	"net"
	"testing"
)

// ipv4Packet builds a minimal 20-byte IPv4 header with the given source address.
func ipv4Packet(src net.IP) []byte {
	p := make([]byte, 20)
	p[0] = 0x45 // version 4, IHL 5
	s := src.To4()
	copy(p[12:16], s)
	return p
}

func TestSrcMatchesTunnel(t *testing.T) {
	tun := net.ParseIP("10.8.0.7")

	// Matching source → accepted.
	if !srcMatchesTunnel(ipv4Packet(net.ParseIP("10.8.0.7")), tun) {
		t.Error("matching src should pass")
	}
	// Spoofed source (another tunnel IP) → rejected.
	if srcMatchesTunnel(ipv4Packet(net.ParseIP("10.8.0.9")), tun) {
		t.Error("spoofed src must be rejected")
	}
	// Public spoof → rejected.
	if srcMatchesTunnel(ipv4Packet(net.ParseIP("8.8.8.8")), tun) {
		t.Error("public spoof must be rejected")
	}
	// Truncated packet → rejected.
	if srcMatchesTunnel(make([]byte, 19), tun) {
		t.Error("truncated packet must be rejected")
	}
	// Non-IPv4 (version nibble 6) → rejected.
	v6 := make([]byte, 40)
	v6[0] = 0x60
	if srcMatchesTunnel(v6, tun) {
		t.Error("non-IPv4 must be rejected")
	}
	// Empty → rejected.
	if srcMatchesTunnel(nil, tun) {
		t.Error("nil packet must be rejected")
	}
}
