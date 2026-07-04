//go:build windows

package vpncore

import (
	"log"
	"net"
	"os/exec"
	"strings"
	"syscall"
)

// hiddenCmd builds an exec.Cmd that runs without flashing a console window —
// essential for a GUI app that shells out to route/netsh/powershell.
func hiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd
}

// setupRouting installs full-tunnel routing on Windows: every destination is
// sent through the tunnel via two /1 routes (more specific than the system
// default, so the original default route is left untouched), while the VPN
// server IPs are pinned to the real gateway so encrypted packets escape the
// tunnel instead of looping. Returns a cleanup function.
func (e *Engine) setupRouting(serverIPs ...net.IP) func() {
	// Capture the device directly so the returned cleanup operates on it without
	// touching e.mu (Disconnect runs cleanup with the device already detached).
	e.mu.Lock()
	dev := e.tunDev
	e.mu.Unlock()
	if dev == nil {
		return func() {}
	}

	gw := defaultGateway()
	var excluded []string
	if gw != "" {
		for _, ip := range serverIPs {
			if ip == nil {
				continue
			}
			s := ip.To4().String()
			if err := hiddenCmd("route", "add", s, "mask", "255.255.255.255", gw, "metric", "1").Run(); err != nil {
				log.Printf("[engine] exclude %s: %v", s, err)
			} else {
				excluded = append(excluded, s)
			}
		}
	} else {
		log.Printf("[engine] could not determine default gateway; servers not excluded")
	}

	for _, p := range splitHalves {
		if err := dev.AddInterfaceRoute(p); err != nil {
			log.Printf("[engine] full-tunnel route %s: %v", p, err)
		}
	}

	return func() {
		for _, p := range splitHalves {
			dev.DelInterfaceRoute(p)
		}
		for _, s := range excluded {
			hiddenCmd("route", "delete", s).Run()
		}
	}
}

// defaultGateway returns the next hop of the current IPv4 default route.
func defaultGateway() string {
	out, err := hiddenCmd("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Sort-Object RouteMetric | Select-Object -First 1).NextHop").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
