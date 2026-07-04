//go:build linux

package tun

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	tunSetIFF  = 0x400454ca
	iffTUN     = 0x0001
	iffNOPI    = 0x1000
)

type ifReq struct {
	Name  [16]byte
	Flags uint16
	_     [22]byte
}

// Device wraps a Linux TUN file descriptor.
type Device struct {
	file   *os.File
	name   string
	mtu    int
	dnsLockIP string // resolver IP the DNS kill-switch allows, for clean removal
}

// Open creates or attaches to a TUN interface (e.g. "mini-tun-asymmetric0").
// Returns the device ready for Read/Write of raw IP packets.
func Open(ifName string, mtu int) (*Device, error) {
	fd, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}

	var req ifReq
	copy(req.Name[:], ifName)
	req.Flags = iffTUN | iffNOPI

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		fd.Fd(), tunSetIFF, uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		fd.Close()
		return nil, fmt.Errorf("ioctl TUNSETIFF: %w", errno)
	}

	// Extract actual interface name assigned by kernel
	name := string(req.Name[:])
	for i, c := range name {
		if c == 0 {
			name = name[:i]
			break
		}
	}

	return &Device{file: fd, name: name, mtu: mtu}, nil
}

func (d *Device) Read(buf []byte) (int, error)  { return d.file.Read(buf) }
func (d *Device) Write(buf []byte) (int, error) { return d.file.Write(buf) }
func (d *Device) Close() error                  { return d.file.Close() }
func (d *Device) Name() string                  { return d.name }
func (d *Device) MTU() int                      { return d.mtu }

// Configure sets the tunnel IP address, peer (gateway), and brings the interface up.
func (d *Device) Configure(localIP, peerIP net.IP, mtu int) error {
	// Use ip(8) — available on every modern Linux
	cidr := fmt.Sprintf("%s/32", localIP)
	if err := run("ip", "addr", "add", cidr, "peer", peerIP.String(), "dev", d.name); err != nil {
		return fmt.Errorf("set addr: %w", err)
	}
	if err := run("ip", "link", "set", "dev", d.name, "mtu", fmt.Sprint(mtu), "up"); err != nil {
		return fmt.Errorf("bring up: %w", err)
	}
	return nil
}

// SetDefaultRoute adds a default route through the tunnel.
func (d *Device) SetDefaultRoute(gatewayIP net.IP) error {
	return run("ip", "route", "add", "default", "via", gatewayIP.String(), "dev", d.name)
}

// AddHostRoute routes a single host (ip/32) through the tunnel.
func (d *Device) AddHostRoute(ip, gatewayIP net.IP) error {
	return run("ip", "route", "add", ip.String()+"/32", "dev", d.name)
}

// AddInterfaceRoute routes an arbitrary prefix (e.g. "0.0.0.0/1") through the tunnel.
func (d *Device) AddInterfaceRoute(prefix string) error {
	return run("ip", "route", "add", prefix, "dev", d.name)
}

// DelInterfaceRoute removes a prefix route previously added via the tunnel.
func (d *Device) DelInterfaceRoute(prefix string) error {
	return run("ip", "route", "del", prefix, "dev", d.name)
}

// DelHostRoute removes a host route previously added through the tunnel.
func (d *Device) DelHostRoute(ip net.IP) error {
	return run("ip", "route", "del", ip.String()+"/32", "dev", d.name)
}

func run(args ...string) error {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, out)
	}
	return nil
}

const resolvConf = "/etc/resolv.conf"
const resolvBackup = "/etc/resolv.conf.mini-tun-asymmetric-bak"

// SetDNS forces the system resolver to the given in-tunnel DNS address by
// rewriting /etc/resolv.conf (backing up the original). All DNS then flows
// through the tunnel, so a router's fake-dns can't answer first.
func (d *Device) SetDNS(dnsIP net.IP) error {
	// Back up the existing resolv.conf once.
	if _, err := os.Stat(resolvBackup); os.IsNotExist(err) {
		if data, rerr := os.ReadFile(resolvConf); rerr == nil {
			os.WriteFile(resolvBackup, data, 0644)
		}
	}
	content := fmt.Sprintf("# mini-tun-asymmetric: all DNS via tunnel\nnameserver %s\n", dnsIP.String())
	return os.WriteFile(resolvConf, []byte(content), 0644)
}

// RestoreDNS restores the original /etc/resolv.conf saved by SetDNS.
func (d *Device) RestoreDNS() error {
	if data, err := os.ReadFile(resolvBackup); err == nil {
		if werr := os.WriteFile(resolvConf, data, 0644); werr != nil {
			return werr
		}
		os.Remove(resolvBackup)
	}
	return nil
}

// BlockIPv6 installs blackhole routes for the whole IPv6 space so no IPv6
// traffic (or AAAA-resolved connections) can leak out the physical interface
// while the tunnel — which is IPv4-only — is up. Two /1 routes cover ::/0
// without replacing any existing default route.
func (d *Device) BlockIPv6() error {
	for _, p := range []string{"::/1", "8000::/1"} {
		// Ignore errors per-route; some hosts have IPv6 disabled entirely.
		run("ip", "-6", "route", "add", "blackhole", p)
	}
	return nil
}

// UnblockIPv6 removes the blackhole routes added by BlockIPv6.
func (d *Device) UnblockIPv6() {
	for _, p := range []string{"::/1", "8000::/1"} {
		run("ip", "-6", "route", "del", "blackhole", p)
	}
}

// LockDNS installs a DNS kill-switch via iptables: drop all outbound DNS
// (port 53) except to dnsIP (the in-tunnel resolver). Mirrors the Windows
// behaviour so a multi-homed host can't leak DNS to a local router's fake-dns.
func (d *Device) LockDNS(dnsIP net.IP) error {
	ip := dnsIP.String()
	d.dnsLockIP = ip
	for _, proto := range []string{"udp", "tcp"} {
		// Allow to the resolver (inserted first = evaluated first)...
		run("iptables", "-I", "OUTPUT", "1", "-p", proto, "--dport", "53",
			"-d", ip, "-j", "ACCEPT")
		// ...drop everything else to :53.
		run("iptables", "-A", "OUTPUT", "-p", proto, "--dport", "53", "-j", "DROP")
	}
	return nil
}

// UnlockDNS removes the DNS kill-switch iptables rules added by LockDNS.
func (d *Device) UnlockDNS() {
	for _, proto := range []string{"udp", "tcp"} {
		run("iptables", "-D", "OUTPUT", "-p", proto, "--dport", "53", "-j", "DROP")
		if d.dnsLockIP != "" {
			run("iptables", "-D", "OUTPUT", "-p", proto, "--dport", "53",
				"-d", d.dnsLockIP, "-j", "ACCEPT")
		}
	}
	d.dnsLockIP = ""
}

// FlushDNS clears any local DNS resolver cache. On Linux there is often no
// system-wide cache; flush the common resolvers if present (best-effort).
func (d *Device) FlushDNS() {
	run("resolvectl", "flush-caches")            // systemd-resolved
	run("systemctl", "restart", "nscd")          // nscd, if used
}

// ExcludeHost pins a single host to the physical default gateway so VPN server
// traffic (uplink to master, downlink from slave) escapes the tunnel instead of
// looping back into it.
func (d *Device) ExcludeHost(ip net.IP, gatewayIP net.IP, iface string) error {
	if gatewayIP != nil {
		return run("ip", "route", "add", ip.String()+"/32", "via", gatewayIP.String())
	}
	return run("ip", "route", "add", ip.String()+"/32", "dev", iface)
}

// UnexcludeHost removes a route added by ExcludeHost.
func (d *Device) UnexcludeHost(ip net.IP) {
	run("ip", "route", "del", ip.String()+"/32")
}
