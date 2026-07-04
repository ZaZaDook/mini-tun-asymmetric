//go:build windows

// Package tun provides a TUN virtual network interface on Windows via WinTun.
// WinTun is the same driver used by WireGuard for Windows.
// The wintun.dll must be present alongside the executable.
// Download from: https://www.wintun.net/
package tun

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wintun                        *windows.DLL
	procWintunCreateAdapter       *windows.Proc
	procWintunOpenAdapter         *windows.Proc
	procWintunCloseAdapter        *windows.Proc
	procWintunStartSession        *windows.Proc
	procWintunEndSession          *windows.Proc
	procWintunAllocateSendPacket  *windows.Proc
	procWintunSendPacket          *windows.Proc
	procWintunReceivePacket       *windows.Proc
	procWintunReleaseReceivePacket *windows.Proc
)

func init() {
	var err error
	wintun, err = windows.LoadDLL("wintun.dll")
	if err != nil {
		// DLL not found — TUN won't work; will fail on Open()
		return
	}
	load := func(name string) *windows.Proc {
		p, _ := wintun.FindProc(name)
		return p
	}
	procWintunCreateAdapter        = load("WintunCreateAdapter")
	procWintunOpenAdapter          = load("WintunOpenAdapter")
	procWintunCloseAdapter         = load("WintunCloseAdapter")
	procWintunStartSession         = load("WintunStartSession")
	procWintunEndSession           = load("WintunEndSession")
	procWintunAllocateSendPacket   = load("WintunAllocateSendPacket")
	procWintunSendPacket           = load("WintunSendPacket")
	procWintunReceivePacket        = load("WintunReceivePacket")
	procWintunReleaseReceivePacket = load("WintunReleaseReceivePacket")
}

// adapterGUID is a fixed GUID for the Mini-Tun Asymmetric adapter.
// Generated once; must stay constant across installs.
var adapterGUID = &windows.GUID{
	Data1: 0x6b4a3e2f,
	Data2: 0x1c8d,
	Data3: 0x4a91,
	Data4: [8]byte{0x8b, 0x2e, 0x3f, 0x7a, 0x9c, 0x1d, 0x5e, 0x4b},
}

const wintunCapacity = 0x800000 // 8 MiB ring buffer

// Device wraps a WinTun adapter + session.
type Device struct {
	adapter uintptr
	session uintptr
	name    string
	mtu     int
	readEvt windows.Handle
	closed  atomic.Bool
}

// Open creates or opens the Mini-Tun Asymmetric WinTun adapter.
func Open(ifName string, mtu int) (*Device, error) {
	if wintun == nil {
		return nil, fmt.Errorf("wintun.dll not found — place it alongside \"Mini-Tun Asymmetric.exe\"")
	}

	namePtr, _ := syscall.UTF16PtrFromString(ifName)
	tunnelTypePtr, _ := syscall.UTF16PtrFromString("Mini-Tun Asymmetric")

	// Try to open existing adapter first
	adapter, _, _ := procWintunOpenAdapter.Call(uintptr(unsafe.Pointer(namePtr)))
	if adapter == 0 {
		// Create new adapter
		var err error
		adapter, _, err = procWintunCreateAdapter.Call(
			uintptr(unsafe.Pointer(namePtr)),
			uintptr(unsafe.Pointer(tunnelTypePtr)),
			uintptr(unsafe.Pointer(adapterGUID)),
		)
		if adapter == 0 {
			return nil, fmt.Errorf("WintunCreateAdapter: %w", err)
		}
	}

	// Start a session with ring buffer
	session, _, errno := procWintunStartSession.Call(adapter, wintunCapacity)
	if session == 0 {
		procWintunCloseAdapter.Call(adapter)
		return nil, fmt.Errorf("WintunStartSession: %w", errno)
	}

	// Get the read-wait event handle
	// WintunGetReadWaitEvent is needed for blocking reads; use polling fallback here
	return &Device{
		adapter: adapter,
		session: session,
		name:    ifName,
		mtu:     mtu,
	}, nil
}

// Read blocks until a packet is received from the TUN interface.
func (d *Device) Read(buf []byte) (int, error) {
	for {
		if d.closed.Load() {
			return 0, io.EOF
		}
		pktPtr, size, rawErr := procWintunReceivePacket.Call(d.session)
		if pktPtr != 0 {
			n := int(size)
			if n > len(buf) {
				n = len(buf)
			}
			copy(buf[:n], unsafe.Slice((*byte)(unsafe.Pointer(pktPtr)), n))
			procWintunReleaseReceivePacket.Call(d.session, pktPtr)
			return n, nil
		}
		errno, _ := rawErr.(syscall.Errno)
		if errno == windows.ERROR_NO_MORE_ITEMS {
			// No packet yet; sleep briefly and retry
			windows.SleepEx(1, false)
			continue
		}
		return 0, fmt.Errorf("WintunReceivePacket: %w", rawErr)
	}
}

// Write sends a packet into the TUN interface (to the OS networking stack).
func (d *Device) Write(buf []byte) (int, error) {
	pktPtr, _, errno := procWintunAllocateSendPacket.Call(d.session, uintptr(len(buf)))
	if pktPtr == 0 {
		return 0, fmt.Errorf("WintunAllocateSendPacket: %w", errno)
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(pktPtr)), len(buf)), buf)
	procWintunSendPacket.Call(d.session, pktPtr)
	return len(buf), nil
}

func (d *Device) Close() error {
	// Signal Read to stop touching the session, then give it a moment to exit
	// its polling loop before we free the WinTun session (avoids a use-after-
	// free access violation racing Read against EndSession).
	if d.closed.Swap(true) {
		return nil // already closed
	}
	time.Sleep(10 * time.Millisecond)
	procWintunEndSession.Call(d.session)
	procWintunCloseAdapter.Call(d.adapter)
	return nil
}

func (d *Device) Name() string { return d.name }
func (d *Device) MTU() int     { return d.mtu }

// Configure sets the adapter IP address and MTU using netsh. A /24 mask gives
// the adapter an on-link subnet; routes are added on-link via the interface
// (WinTun has no L2, so next-hop-by-gateway can't be resolved — we route by
// interface). The MTU is lowered so that, after the obfuscation header and AEAD
// overhead are added on each hop, tunneled packets still fit a 1500-byte path
// without fragmentation (otherwise large flows — TLS, video — silently stall).
func (d *Device) Configure(localIP, peerIP net.IP, mtu int) error {
	mask := "255.255.255.0"
	err := runNetsh("interface", "ip", "set", "address",
		d.name, "static", localIP.String(), mask)
	if err != nil {
		return fmt.Errorf("netsh set address: %w", err)
	}
	if mtu <= 0 {
		mtu = 1400
	}
	d.mtu = mtu
	// netsh interface ipv4 set subinterface "MiniTun" mtu=1400 store=persistent
	runNetsh("interface", "ipv4", "set", "subinterface", d.name,
		"mtu="+itoa(mtu), "store=active")
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// SetDefaultRoute routes all traffic out the tunnel interface (on-link).
func (d *Device) SetDefaultRoute(gatewayIP net.IP) error {
	return runNetsh("interface", "ipv4", "add", "route",
		"prefix=0.0.0.0/0", "interface="+d.name, "store=active")
}

// AddHostRoute routes a single host (ip/32) out the tunnel interface (on-link).
func (d *Device) AddHostRoute(ip, gatewayIP net.IP) error {
	return runNetsh("interface", "ipv4", "add", "route",
		"prefix="+ip.String()+"/32", "interface="+d.name, "store=active")
}

// AddInterfaceRoute routes an arbitrary prefix (e.g. "0.0.0.0/1") out the tunnel.
func (d *Device) AddInterfaceRoute(prefix string) error {
	return runNetsh("interface", "ipv4", "add", "route",
		"prefix="+prefix, "interface="+d.name, "store=active")
}

// DelInterfaceRoute removes a prefix route previously added via the tunnel.
func (d *Device) DelInterfaceRoute(prefix string) error {
	return runNetsh("interface", "ipv4", "delete", "route",
		"prefix="+prefix, "interface="+d.name)
}

// DelHostRoute removes a host route previously added through the tunnel.
func (d *Device) DelHostRoute(ip net.IP) error {
	return runNetsh("interface", "ipv4", "delete", "route",
		"prefix="+ip.String()+"/32", "interface="+d.name)
}

func runNetsh(args ...string) error {
	cmd := exec.Command("netsh", args...)
	// Run without flashing a console window (the client is a GUI app).
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh: %s: %w", out, err)
	}
	return nil
}

// SetDNS forces the tunnel interface's DNS to the in-tunnel resolver and sets it
// as the primary resolver. Combined with full-tunnel routing, all DNS flows
// through the tunnel so a router's fake-dns can't answer first.
func (d *Device) SetDNS(dnsIP net.IP) error {
	// Set the tunnel adapter's DNS to the in-tunnel resolver.
	if err := runNetsh("interface", "ipv4", "set", "dnsservers",
		"name="+d.name, "static", dnsIP.String(), "primary", "validate=no"); err != nil {
		return err
	}
	return nil
}

// RestoreDNS reverts the tunnel interface to DHCP-assigned DNS. Other adapters
// are untouched; tearing down the adapter removes its DNS entry anyway.
func (d *Device) RestoreDNS() error {
	return runNetsh("interface", "ipv4", "set", "dnsservers",
		"name="+d.name, "dhcp")
}

// dnsFirewallRule is the name used for the DNS kill-switch firewall rules so
// they can be reliably removed on disconnect.
const dnsFirewallRule = "MiniTunAsymmetric-DNS-Killswitch"

// LockDNS installs a DNS kill-switch: all outbound DNS (port 53, UDP+TCP) is
// blocked EXCEPT to dnsIP (the in-tunnel resolver). This defeats Windows
// "Smart Multi-Homed Name Resolution", which otherwise queries every adapter in
// parallel and accepts the fastest reply — on a router with fake-dns the local
// router (~1ms) always beats the tunnel (~100ms) and injects fake 198.18.x.x
// addresses, stalling downloads. With this rule the router never receives the
// query, so only the tunnel resolver can answer.
//
// Windows Firewall evaluates block rules with higher precedence than allow
// rules, so we cannot "block all + allow one". Instead we use a SINGLE block
// rule whose remote-IP set is the entire IPv4 space MINUS the resolver, leaving
// the resolver reachable without any allow rule.
func (d *Device) LockDNS(dnsIP net.IP) error {
	d.UnlockDNS() // idempotent: clear stale rules first

	rng, err := ipv4ComplementRange(dnsIP)
	if err != nil {
		return err
	}
	for _, proto := range []string{"UDP", "TCP"} {
		if err := runNetsh("advfirewall", "firewall", "add", "rule",
			"name="+dnsFirewallRule,
			"dir=out", "action=block", "protocol="+proto, "remoteport=53",
			"remoteip="+rng,
		); err != nil {
			return err
		}
	}
	return nil
}

// UnlockDNS removes the DNS kill-switch firewall rules.
func (d *Device) UnlockDNS() {
	runNetsh("advfirewall", "firewall", "delete", "rule", "name="+dnsFirewallRule)
}

// FlushDNS clears the OS DNS resolver cache. On connect this purges any fake
// (198.18.x) addresses the router's fake-dns injected before the tunnel came
// up, so applications re-resolve through the in-tunnel resolver and stop
// hammering dead cached IPs.
func (d *Device) FlushDNS() {
	cmd := exec.Command("ipconfig", "/flushdns")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.Run()
}

// ipv4ComplementRange returns a netsh remoteip value covering all of IPv4
// except the single address ip, expressed as two ranges:
//   0.0.0.0-<ip-1>,<ip+1>-255.255.255.255
// e.g. for 10.8.0.1 -> "0.0.0.0-10.8.0.0,10.8.0.2-255.255.255.255".
func ipv4ComplementRange(ip net.IP) (string, error) {
	v4 := ip.To4()
	if v4 == nil {
		return "", fmt.Errorf("LockDNS: %v is not IPv4", ip)
	}
	u := uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
	var parts []string
	if u > 0 {
		parts = append(parts, "0.0.0.0-"+uint32ToIP(u-1))
	}
	if u < 0xFFFFFFFF {
		parts = append(parts, uint32ToIP(u+1)+"-255.255.255.255")
	}
	return strings.Join(parts, ","), nil
}

func uint32ToIP(u uint32) string {
	return net.IPv4(byte(u>>24), byte(u>>16), byte(u>>8), byte(u)).To4().String()
}

// BlockIPv6 disables IPv6 routing leakage by blackholing the whole IPv6 space
// via two /1 routes on the tunnel interface. The tunnel is IPv4-only, so any
// IPv6 connection must be prevented from escaping on the physical NIC.
func (d *Device) BlockIPv6() error {
	for _, p := range []string{"::/1", "8000::/1"} {
		runNetsh("interface", "ipv6", "add", "route",
			"prefix="+p, "interface="+d.name, "store=active")
	}
	return nil
}

// UnblockIPv6 removes the IPv6 blackhole routes added by BlockIPv6.
func (d *Device) UnblockIPv6() {
	for _, p := range []string{"::/1", "8000::/1"} {
		runNetsh("interface", "ipv6", "delete", "route",
			"prefix="+p, "interface="+d.name)
	}
}
