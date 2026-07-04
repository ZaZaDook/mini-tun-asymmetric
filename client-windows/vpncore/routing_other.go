//go:build !windows

package vpncore

import (
	"log"
	"net"
	"os/exec"
	"strings"
)

// setupRouting on non-Windows pins the VPN server IPs to the physical default
// gateway (so uplink/downlink escape the tunnel instead of looping), then
// installs the split-default routes through the tunnel.
func (e *Engine) setupRouting(serverIPs ...net.IP) func() {
	gw, iface := defaultGatewayLinux()

	e.mu.Lock()
	dev := e.tunDev
	e.mu.Unlock()

	var excluded []net.IP
	if dev != nil {
		for _, ip := range serverIPs {
			if ip == nil {
				continue
			}
			if err := dev.ExcludeHost(ip, gw, iface); err != nil {
				log.Printf("[engine] exclude server %s: %v", ip, err)
			} else {
				excluded = append(excluded, ip)
			}
		}
	}

	if err := e.SetSplitDefaultRoute(); err != nil {
		log.Printf("[engine] full-tunnel route: %v", err)
	}

	return func() {
		e.ClearSplitDefaultRoute()
		if dev != nil {
			for _, ip := range excluded {
				dev.UnexcludeHost(ip)
			}
		}
	}
}

// defaultGatewayLinux returns the next-hop IP and interface of the current IPv4
// default route via `ip route`.
func defaultGatewayLinux() (net.IP, string) {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return nil, ""
	}
	// Example: "default via 10.200.0.1 dev veth-n proto static"
	fields := strings.Fields(string(out))
	var gw net.IP
	var iface string
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "via":
			gw = net.ParseIP(fields[i+1])
		case "dev":
			iface = fields[i+1]
		}
	}
	return gw, iface
}
