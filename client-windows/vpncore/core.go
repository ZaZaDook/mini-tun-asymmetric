// Package vpncore is the VPN engine for the Windows/Linux client.
// Manages: handshake, TUN adapter, uplink (TUN→Master) and downlink (Slave→TUN),
// ChaCha20-Poly1305 encryption on all packets.
package vpncore

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/crypto"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/nettune"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/transport"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/tun"
)

// Debug enables verbose data-path logging when set before Connect.
var Debug bool

// tunMTU is the tunnel interface MTU. It must match the master netstack MTU so
// that tunneled packets (plus transport framing + AEAD overhead) fit a 1500-byte path
// without fragmentation — otherwise large flows (TLS, video) silently stall.
const tunMTU = 1400

func dbg(format string, args ...any) {
	if Debug {
		log.Printf("[dbg] "+format, args...)
	}
}

type State int

const (
	StateDisconnected State = iota
	StateConnecting
	StateConnected
	StateError
)

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting..."
	case StateConnected:
		return "Connected"
	case StateError:
		return "Error"
	}
	return "Unknown"
}

type Stats struct {
	bytesUp   atomic.Uint64
	bytesDown atomic.Uint64
	startTime time.Time
	mu        sync.Mutex
}

func (s *Stats) AddUp(n int)   { s.bytesUp.Add(uint64(n)) }
func (s *Stats) AddDown(n int) { s.bytesDown.Add(uint64(n)) }

func (s *Stats) Snapshot() (up, down uint64, dur time.Duration) {
	s.mu.Lock()
	start := s.startTime
	s.mu.Unlock()
	return s.bytesUp.Load(), s.bytesDown.Load(), time.Since(start)
}

func (s *Stats) reset() {
	s.bytesUp.Store(0)
	s.bytesDown.Store(0)
	s.mu.Lock()
	s.startTime = time.Now()
	s.mu.Unlock()
}

// Engine is the core VPN engine.
type Engine struct {
	mu         sync.Mutex
	state      State
	stateHook  func(State)

	masterConn *net.UDPConn
	slaveConn  *net.UDPConn // receive-only (downlink from slave)
	tunDev     *tun.Device

	aead      *crypto.AEAD // client→master encryption
	slaveAead *crypto.AEAD // slave→client decryption (same key, different counter)

	tunIP     net.IP
	slaveAddr *net.UDPAddr
	stats     Stats

	stopCh       chan struct{}
	token        []byte
	tr           transport.Transport // outer transport envelope (default cs2)
	routeCleanup func() // undoes full-tunnel routes on disconnect

	// FullTunnel, when true, routes ALL traffic through the VPN (default).
	// When false the engine only brings up the adapter (caller adds routes).
	FullTunnel bool

	// SecureDNS, when true (default), forces the OS resolver to the in-tunnel DNS
	// (the tunnel gateway) and blackholes IPv6 for the duration of the connection.
	// This makes the client immune to a router's fake-dns/fake-ip: all name
	// resolution goes through the tunnel and returns real IPs.
	SecureDNS bool

	// Transport selects the transport carrier ("cs2" default, "utp", ...). It
	// must match the server. A fresh transport is built per connect (µTP is
	// stateful — each connection needs its own connection id and seq stream).
	Transport string

	// CustomPorts overrides the carrier's default control port. When non-empty,
	// dialAndHandshake tries these ports in order (fallback) instead of the
	// carrierControlPort default. The master must listen for the carrier there.
	CustomPorts []int

	// quic is true when Transport == "quic": the carrier is symmetric, so uplink
	// goes to the SLAVE (same socket as downlink), not the master — the master
	// stays hidden in the data stream. Set in connectLoop.
	quic bool

	// activeCarrier is the carrier name currently in use (set once connected),
	// surfaced in the status API so the UI can show what's actually running.
	activeCarrier string
	// lastGood remembers the carrier that last connected on transport=="auto", so
	// the next auto attempt tries it first (it likely still works on this network).
	lastGood string

	// slaveRTT is the last measured RTT (ms) to the active slave's downlink, or
	// -1 if not yet measured. Surfaced in the status API.
	slaveRTT int64
	// pongCh receives the echoed timestamp bytes from slave Pong frames, so
	// probeRTT can match a reply to the ping it sent. Non-nil only while connected.
	pongCh chan []byte
}

// SlaveRTT returns the last measured slave RTT in ms, or -1 if unknown.
func (e *Engine) SlaveRTT() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.slaveRTT
}

// ActiveTransport returns the carrier name in use, or "" if not connected.
func (e *Engine) ActiveTransport() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state == StateConnected {
		return e.activeCarrier
	}
	return ""
}

// sendUplink delivers a wrapped uplink frame to the right peer: the master in the
// asymmetric carriers, or the slave in QUIC symmetric mode.
func (e *Engine) sendUplink(frame []byte) error {
	e.mu.Lock()
	q := e.quic
	mc := e.masterConn
	sc := e.slaveConn
	sa := e.slaveAddr
	e.mu.Unlock()
	if q {
		if sc == nil || sa == nil {
			return fmt.Errorf("slave uplink not ready")
		}
		_, err := sc.WriteToUDP(frame, sa)
		return err
	}
	if mc == nil {
		return fmt.Errorf("master uplink not ready")
	}
	_, err := mc.Write(frame)
	return err
}

func NewEngine(onStateChange func(State)) *Engine {
	return &Engine{stateHook: onStateChange, state: StateDisconnected, FullTunnel: true, SecureDNS: true}
}

func (e *Engine) setState(s State) {
	e.mu.Lock()
	e.state = s
	e.mu.Unlock()
	if e.stateHook != nil {
		e.stateHook(s)
	}
}

func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func (e *Engine) Stats() *Stats { return &e.stats }

// HasTUN reports whether the TUN device is open (requires admin + wintun.dll).
func (e *Engine) HasTUN() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tunDev != nil
}

// tunGateway is the master's tunnel-side gateway IP.
var tunGateway = net.IPv4(10, 8, 0, 1)

// AddHostRoute routes a single public host through the tunnel.
func (e *Engine) AddHostRoute(ip net.IP) error {
	e.mu.Lock()
	dev := e.tunDev
	e.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("no TUN device")
	}
	return dev.AddHostRoute(ip, tunGateway)
}

// DelHostRoute removes a host route previously added through the tunnel.
func (e *Engine) DelHostRoute(ip net.IP) error {
	e.mu.Lock()
	dev := e.tunDev
	e.mu.Unlock()
	if dev == nil {
		return nil
	}
	return dev.DelHostRoute(ip)
}

// SetDefaultRoute routes all traffic through the tunnel (full-tunnel mode).
func (e *Engine) SetDefaultRoute() error {
	e.mu.Lock()
	dev := e.tunDev
	e.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("no TUN device")
	}
	return dev.SetDefaultRoute(tunGateway)
}

// splitHalves are two /1 routes that together cover all of IPv4 but are more
// specific than the system default route, so they capture all traffic without
// disturbing (or needing to restore) the original 0.0.0.0/0 default.
var splitHalves = []string{"0.0.0.0/1", "128.0.0.0/1"}

// SetSplitDefaultRoute routes all IPv4 traffic through the tunnel via two /1
// routes (more specific than the system default, so the original 0.0.0.0/0 is
// left intact). IPv6 is handled separately by Device.BlockIPv6 — routing IPv6
// prefixes through Device.AddInterfaceRoute is wrong (that helper is IPv4-only).
func (e *Engine) SetSplitDefaultRoute() error {
	e.mu.Lock()
	dev := e.tunDev
	e.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("no TUN device")
	}
	for _, p := range splitHalves {
		if err := dev.AddInterfaceRoute(p); err != nil {
			return err
		}
	}
	return nil
}

// ClearSplitDefaultRoute removes the /1 full-tunnel routes.
func (e *Engine) ClearSplitDefaultRoute() {
	e.mu.Lock()
	dev := e.tunDev
	e.mu.Unlock()
	if dev == nil {
		return
	}
	for _, p := range splitHalves {
		dev.DelInterfaceRoute(p)
	}
}

// SlaveIP returns the slave node's IP (downlink origin), known after connect.
func (e *Engine) SlaveIP() net.IP {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.slaveAddr == nil {
		return nil
	}
	return e.slaveAddr.IP
}

func (e *Engine) TunnelIP() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tunIP == nil {
		return ""
	}
	return e.tunIP.String()
}

// Connect initiates a VPN connection.
func (e *Engine) Connect(masterAddr, authToken string) error {
	e.mu.Lock()
	if e.state == StateConnecting || e.state == StateConnected {
		e.mu.Unlock()
		return fmt.Errorf("already connected")
	}
	e.mu.Unlock()

	tok, err := base64.StdEncoding.DecodeString(authToken)
	if err != nil {
		return fmt.Errorf("invalid auth token: %w", err)
	}
	e.token = tok

	e.setState(StateConnecting)
	e.stopCh = make(chan struct{})

	go func() {
		if err := e.connectLoop(masterAddr); err != nil {
			log.Printf("[engine] %v", err)
			e.setState(StateError)
		}
	}()
	return nil
}

func (e *Engine) Disconnect() {
	// Capture everything under the lock, then release it BEFORE running route
	// cleanup and closing the device. routeCleanup deletes routes via the TUN
	// device and must run while the adapter still exists and without holding
	// e.mu (the cleanup helpers lock e.mu themselves — holding it here would
	// self-deadlock).
	e.mu.Lock()
	if e.stopCh != nil {
		select {
		case <-e.stopCh:
		default:
			close(e.stopCh)
		}
		// NOTE: do NOT set e.stopCh = nil here. Long-lived goroutines select on
		// e.stopCh; closing it unblocks them (a closed channel always yields). If
		// we nil it instead, a goroutine that reads e.stopCh after this point gets
		// `<-nil` which blocks FOREVER — leaking the punch/probe loops, which keep
		// sending UDP to the slave long after Disconnect. The next Connect assigns
		// a fresh channel (those dead goroutines are already gone).
	}
	cleanup := e.routeCleanup
	mc := e.masterConn
	sc := e.slaveConn
	dev := e.tunDev
	hook := e.stateHook
	e.routeCleanup = nil
	e.masterConn = nil
	e.slaveConn = nil
	e.tunDev = nil
	e.pongCh = nil
	e.slaveRTT = -1
	e.state = StateDisconnected
	e.mu.Unlock()

	if cleanup != nil {
		cleanup() // remove routes while the adapter is still up
	}
	if dev != nil {
		// Restore DNS and IPv6 while the adapter still exists.
		dev.RestoreDNS()
		dev.UnlockDNS()
		dev.UnblockIPv6()
	}
	if mc != nil {
		mc.Close()
	}
	if sc != nil {
		sc.Close()
	}
	if dev != nil {
		dev.Close() // destroys the adapter; any remaining routes via it vanish
	}
	if hook != nil {
		hook(StateDisconnected)
	}
}

// carrierControlPort maps an transport carrier to the master control port it
// listens on (the "native" port for that carrier's native framing). The client dials
// master IP : this port for the handshake. Mirrors the master's control_ports.
var carrierControlPort = map[string]int{
	"cs2":    7000,
	"utp":    6881,
	"webrtc": 3478,
	"quic":   443,
}

// autoCarrierOrder is the default fallback order for transport == "auto". cs2 is
// excluded (its static prefix is a DPI tell); it remains available only as an
// explicit manual choice. The client tries each in turn and keeps the first that
// completes a handshake.
var autoCarrierOrder = []string{"utp", "webrtc", "quic"}

// handshakeResult carries everything dialAndHandshake produced for bringUp.
type handshakeResult struct {
	conn       *net.UDPConn
	mAddr      *net.UDPAddr
	tr         transport.Transport
	carrier    string
	welcome    protocol.WelcomeMsg
	rttMS      int64
}

// connectLoop drives one connection: for a fixed carrier it dials and brings the
// tunnel up; for "auto" it races the fallback order and keeps the first carrier
// whose handshake succeeds (the client is the judge — the master can't see what a
// censor dropped). Per-session choice only; never morphs mid-stream.
func (e *Engine) connectLoop(masterAddr string) error {
	host, _, err := net.SplitHostPort(masterAddr)
	if err != nil {
		// No port given: treat the whole string as host (carrier port is added).
		host = masterAddr
	}

	carrier := e.Transport
	if carrier == "auto" {
		// Try the last-known-good carrier first, then the rest of the order.
		order := make([]string, 0, len(autoCarrierOrder)+1)
		if e.lastGood != "" {
			order = append(order, e.lastGood)
		}
		for _, c := range autoCarrierOrder {
			if c != e.lastGood {
				order = append(order, c)
			}
		}
		var res *handshakeResult
		for _, c := range order {
			r, herr := e.dialAndHandshake(host, c, 0, 4*time.Second)
			if herr != nil {
				log.Printf("[engine] auto: carrier %s failed: %v", c, herr)
				continue
			}
			log.Printf("[engine] auto: selected carrier %s (handshake %dms)", c, r.rttMS)
			res = r
			break
		}
		if res == nil {
			return fmt.Errorf("auto: no carrier connected")
		}
		e.lastGood = res.carrier
		return e.bringUp(res)
	}

	// Fixed carrier. If custom ports are set, try each in order (fallback) until
	// one completes a handshake; otherwise dial the carrier's default port.
	ports := e.CustomPorts
	if len(ports) == 0 {
		ports = []int{0} // 0 = carrier default
	}
	var lastErr error
	for _, p := range ports {
		res, herr := e.dialAndHandshake(host, carrier, p, 10*time.Second)
		if herr != nil {
			lastErr = herr
			if len(ports) > 1 {
				log.Printf("[engine] carrier %s port %d failed: %v", carrier, p, herr)
			}
			continue
		}
		return e.bringUp(res)
	}
	return lastErr
}

// dialAndHandshake dials the master on the carrier's control port, performs the
// Hello v2 handshake, and returns the Welcome — WITHOUT bringing up the TUN. It
// owns its socket and closes it on error, so a failed attempt leaves no state
// (used for the auto-fallback race). carrier "" defaults to cs2.
func (e *Engine) dialAndHandshake(host, carrier string, port int, timeout time.Duration) (*handshakeResult, error) {
	tr, terr := transport.ByName(carrier)
	if terr != nil {
		return nil, fmt.Errorf("transport %q: %w", carrier, terr)
	}
	if port == 0 {
		// No explicit port: use the carrier's default control port.
		port = carrierControlPort[carrier]
		if port == 0 {
			port = carrierControlPort["cs2"]
		}
	}
	mAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return nil, fmt.Errorf("resolve master: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, mAddr)
	if err != nil {
		return nil, fmt.Errorf("UDP dial master: %w", err)
	}
	nettune.TuneUDP(conn)

	hello := protocol.HelloMsg{
		Version:   protocol.HelloVersion3, // announce nearest-node (Welcome v3) support
		Timestamp: uint64(time.Now().Unix()),
	}
	if _, err := rand.Read(hello.Nonce[:]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("hello nonce: %w", err)
	}
	hello.MAC = crypto.HelloMAC(e.token, hello.MarshalUnsigned())
	start := time.Now()
	if _, err := conn.Write(tr.Wrap(protocol.PktTypeHandshake, hello.Marshal())); err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake send: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake recv: %w", err)
	}
	conn.SetReadDeadline(time.Time{})
	rttMS := time.Since(start).Milliseconds()

	pktType, welcomePayload, err := tr.Unwrap(buf[:n])
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("bad welcome frame: %w", err)
	}
	if pktType != protocol.PktTypeControl || len(welcomePayload) < 1 || welcomePayload[0] != protocol.CtrlWelcome {
		conn.Close()
		return nil, fmt.Errorf("expected welcome, got pkt 0x%02x", pktType)
	}
	welcome, err := protocol.ParseWelcome(welcomePayload)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("parse welcome: %w", err)
	}
	return &handshakeResult{
		conn: conn, mAddr: mAddr, tr: tr, carrier: carrier, welcome: welcome, rttMS: rttMS,
	}, nil
}

// bringUp takes a completed handshake and brings the tunnel up: per-session AEAD,
// uplink port-hop (non-QUIC), TUN, DNS, routing, slave sockets, loops. It blocks
// on stopCh until Disconnect. This is the path that was inline in connectLoop.
func (e *Engine) bringUp(res *handshakeResult) error {
	conn, mAddr, tr, welcome := res.conn, res.mAddr, res.tr, res.welcome

	e.mu.Lock()
	e.masterConn = conn
	e.tr = tr
	e.quic = res.carrier == "quic"
	e.activeCarrier = tr.Name()
	e.mu.Unlock()

	// Port-hopping: the handshake happened on the master's well-known control port;
	// the master allocated a per-session DATA port and told us in the Welcome. Move
	// the uplink there so heavy traffic doesn't sit on the control port. In QUIC
	// mode uplink goes to the SLAVE, not the master, so we skip this.
	if !e.quic && welcome.DataPort != 0 && welcome.DataPort != uint16(mAddr.Port) {
		dataAddr := &net.UDPAddr{IP: mAddr.IP, Port: int(welcome.DataPort)}
		dconn, derr := net.DialUDP("udp", nil, dataAddr)
		if derr != nil {
			return fmt.Errorf("dial data port: %w", derr)
		}
		nettune.TuneUDP(dconn)
		conn.Close() // done with the control socket
		conn = dconn
		e.mu.Lock()
		e.masterConn = conn
		e.mu.Unlock()
		log.Printf("[engine] uplink moved to data port %d", welcome.DataPort)
	}

	// ── Derive per-session AEAD ──
	// Bound to the assigned tunnel IP; master and slave derive the identical
	// key via crypto.SessionKey so all three sides agree.
	sessionKey := crypto.SessionKey(e.token, welcome.AssignedIP)
	aead, err := crypto.NewAEAD(sessionKey)
	if err != nil {
		return fmt.Errorf("aead init: %w", err)
	}
	slaveAead, err := crypto.NewAEAD(sessionKey)
	if err != nil {
		return fmt.Errorf("slave aead: %w", err)
	}

	e.mu.Lock()
	e.tunIP = welcome.AssignedIP
	e.slaveAddr = &net.UDPAddr{IP: welcome.SlaveIP, Port: int(welcome.SlavePort)}
	e.aead = aead
	e.slaveAead = slaveAead
	e.stats.reset()
	e.mu.Unlock()

	log.Printf("[engine] connected: tunIP=%s slave=%s carrier=%s", welcome.AssignedIP, e.slaveAddr, tr.Name())

	// ── Open TUN adapter ──
	tunDev, err := tun.Open("MiniTun", tunMTU)
	if err != nil {
		log.Printf("[engine] TUN open failed: %v (running without TUN)", err)
	} else {
		gatewayIP := net.ParseIP("10.8.0.1")
		if err := tunDev.Configure(welcome.AssignedIP, gatewayIP, tunMTU); err != nil {
			log.Printf("[engine] TUN configure: %v", err)
		}
		e.mu.Lock()
		e.tunDev = tunDev
		e.mu.Unlock()
		go e.tunReadLoop() // TUN → uplink (master, or slave in QUIC mode)

		// Secure DNS: force the OS resolver to the in-tunnel gateway and blackhole
		// IPv6, so a router's fake-dns can't answer and IPv6 can't leak. The data
		// path is IPv4-only; the master resolves names and returns real IPs.
		if e.SecureDNS {
			gw := net.ParseIP("10.8.0.1")
			if err := tunDev.SetDNS(gw); err != nil {
				log.Printf("[engine] set DNS: %v", err)
			} else {
				log.Printf("[engine] DNS forced to in-tunnel resolver %s", gw)
			}
			if err := tunDev.LockDNS(gw); err != nil {
				log.Printf("[engine] lock DNS: %v", err)
			} else {
				log.Printf("[engine] DNS kill-switch active (only %s allowed)", gw)
			}
			tunDev.FlushDNS()
			log.Printf("[engine] DNS cache flushed")
		}

		// Install full-tunnel routing (all IPv4 traffic via VPN, VPN servers
		// excluded). setupRouting owns the IPv4 split routes on both platforms.
		if e.FullTunnel {
			cleanup := e.setupRouting(mAddr.IP, welcome.SlaveIP)
			e.mu.Lock()
			e.routeCleanup = cleanup
			e.mu.Unlock()
			if err := tunDev.BlockIPv6(); err != nil {
				log.Printf("[engine] block IPv6: %v", err)
			}
		}
	}

	// ── Open slave receive socket ──
	slaveListenConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return fmt.Errorf("slave listen: %w", err)
	}
	nettune.TuneUDP(slaveListenConn)
	e.mu.Lock()
	e.slaveConn = slaveListenConn
	e.slaveRTT = -1
	e.pongCh = make(chan []byte, 8)
	e.mu.Unlock()
	go e.slaveReadLoop(slaveListenConn)                      // Slave → TUN (downlink)
	go e.slavePunchLoop(slaveListenConn, welcome.AssignedIP) // open NAT for downlink
	// Nearest-node selection (Welcome v3): probe every candidate slave's RTT and
	// pin the closest. Falls back to a single-slave probe for v2 welcomes. Runs
	// after a short delay so the punch loop has registered us on the slave(s).
	// Capture stopCh locally so a disconnect during the 700ms sleep is seen even
	// if a later Connect has already swapped e.stopCh for a fresh channel.
	stop := e.stopCh
	go func() {
		select {
		case <-time.After(700 * time.Millisecond):
		case <-stop:
			return // disconnected before we even started probing
		}
		if len(welcome.Slaves) > 1 {
			e.probeAndChooseSlave(stop, slaveListenConn, welcome, conn, tr)
		} else {
			e.probeRTT(stop, slaveListenConn, welcome.AssignedIP)
		}
	}()

	e.setState(StateConnected)

	go e.keepaliveLoop()
	<-e.stopCh
	return nil
}

// probeRTT measures the round-trip time to the slave's downlink by sending a
// few Ping frames and timing the matching Pong echoes. Takes the minimum of the
// replies (least affected by jitter). Stores the result in e.slaveRTT and logs
// it. The slave only echoes once our session is registered there (the punch
// loop does that), hence the small delay before the first probe in bringUp.
// Payload: [0:4]=tunnelIP [4:12]=client send-timestamp (echoed back verbatim).
func (e *Engine) probeRTT(stop <-chan struct{}, slaveConn *net.UDPConn, tunIP net.IP) {
	e.mu.Lock()
	slaveAddr := e.slaveAddr
	e.mu.Unlock()
	if slaveAddr == nil {
		return
	}
	best := e.probeAddr(stop, slaveConn, tunIP, slaveAddr, 5)
	if best >= 0 {
		e.mu.Lock()
		e.slaveRTT = best
		e.mu.Unlock()
		log.Printf("[engine] slave %s downlink RTT=%dms", slaveAddr.IP, best)
	} else {
		log.Printf("[engine] slave %s RTT probe: no reply", slaveAddr.IP)
	}
}

// probeAddr sends `rounds` ping frames to a specific slave address and returns
// the minimum observed RTT in ms (or -1 if no reply). A few punches precede the
// pings so the slave registers this socket and will echo (its pong is gated on a
// known session). Pongs arrive via slaveReadLoop → pongCh.
func (e *Engine) probeAddr(stop <-chan struct{}, slaveConn *net.UDPConn, tunIP net.IP, addr *net.UDPAddr, rounds int) int64 {
	e.mu.Lock()
	ch := e.pongCh
	e.mu.Unlock()
	if ch == nil {
		return -1
	}
	best := int64(-1)
	// Warm up the NAT mapping + session registration toward THIS slave.
	punch := e.tr.Wrap(protocol.PktTypePunch, tunIP.To4())
	for i := 0; i < 3; i++ {
		select {
		case <-stop:
			return best
		default:
		}
		slaveConn.WriteToUDP(punch, addr)
		time.Sleep(120 * time.Millisecond)
	}
	// drain stale pongs
	for {
		select {
		case <-ch:
			continue
		default:
		}
		break
	}
	for i := 0; i < rounds; i++ {
		select {
		case <-stop:
			return best
		default:
		}
		payload := make([]byte, 12)
		copy(payload[0:4], tunIP.To4())
		binary.BigEndian.PutUint64(payload[4:12], uint64(time.Now().UnixNano()))
		if _, err := slaveConn.WriteToUDP(e.tr.Wrap(protocol.PktTypePing, payload), addr); err != nil {
			return best
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-stop:
			timer.Stop()
			return best
		case <-timer.C:
		case p := <-ch:
			timer.Stop()
			if len(p) >= 12 {
				ns := int64(binary.BigEndian.Uint64(p[4:12]))
				rttMS := (time.Now().UnixNano() - ns) / 1e6
				if rttMS >= 0 && (best < 0 || rttMS < best) {
					best = rttMS
				}
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	return best
}

// probeAndChooseSlave (Welcome v3) probes every candidate slave's RTT, pins the
// nearest as the active downlink, and tells the master via a SlaveChoice control
// packet so it routes downlink through that slave. Falls back to the master's
// round-robin pick if no candidate replies.
func (e *Engine) probeAndChooseSlave(stop <-chan struct{}, slaveConn *net.UDPConn, welcome protocol.WelcomeMsg, ctrlConn *net.UDPConn, tr transport.Transport) {
	type result struct {
		addr *net.UDPAddr
		rtt  int64
	}
	var best *result
	for _, sl := range welcome.Slaves {
		select {
		case <-stop:
			return
		default:
		}
		addr := &net.UDPAddr{IP: sl.IP, Port: int(sl.Port)}
		rtt := e.probeAddr(stop, slaveConn, welcome.AssignedIP, addr, 3)
		if rtt < 0 {
			log.Printf("[engine] slave %s: no reply", addr.IP)
			continue
		}
		log.Printf("[engine] slave %s RTT=%dms", addr.IP, rtt)
		if best == nil || rtt < best.rtt {
			best = &result{addr: addr, rtt: rtt}
		}
	}
	if best == nil {
		log.Printf("[engine] node selection: no slave replied, keeping master's pick")
		return
	}
	// Bail out if we were disconnected mid-probe — don't pin or send anything.
	select {
	case <-stop:
		return
	default:
	}
	// Pin the nearest slave for downlink + record its RTT.
	e.mu.Lock()
	e.slaveAddr = best.addr
	e.slaveRTT = best.rtt
	e.mu.Unlock()
	log.Printf("[engine] nearest slave = %s (%dms), notifying master", best.addr.IP, best.rtt)

	// Tell the master to route downlink through the chosen slave.
	choice := protocol.SlaveChoiceMsg{
		TunnelIP:  welcome.AssignedIP,
		SlaveIP:   best.addr.IP,
		SlavePort: uint16(best.addr.Port),
	}
	ctrlConn.Write(tr.Wrap(protocol.PktTypeControl, choice.Marshal()))
	// Keep the NAT mapping to the chosen slave warm immediately.
	punch := tr.Wrap(protocol.PktTypePunch, welcome.AssignedIP.To4())
	slaveConn.WriteToUDP(punch, best.addr)
}

// slavePunchLoop periodically sends a punch packet from the downlink socket to
// the slave. This opens the client's NAT mapping so the slave's asymmetric
// downlink (which originates from the slave, not the master) can reach us, and
// registers this socket's real address with the slave.
func (e *Engine) slavePunchLoop(slaveConn *net.UDPConn, tunIP net.IP) {
	e.mu.Lock()
	slaveAddr := e.slaveAddr
	e.mu.Unlock()
	if slaveAddr == nil {
		return
	}
	punch := e.tr.Wrap(protocol.PktTypePunch, tunIP.To4())

	// Punch frequently to keep the downlink NAT mapping alive even under uplink
	// load. A few rapid punches up front open it quickly.
	for i := 0; i < 3; i++ {
		slaveConn.WriteToUDP(punch, slaveAddr)
		time.Sleep(200 * time.Millisecond)
	}
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			slaveConn.WriteToUDP(punch, slaveAddr)
		case <-e.stopCh:
			return
		}
	}
}

// tunReadLoop reads IP packets from TUN, encrypts, and sends them uplink (to the
// master in asymmetric carriers, or to the slave in QUIC symmetric mode).
func (e *Engine) tunReadLoop() {
	e.mu.Lock()
	dev := e.tunDev
	e.mu.Unlock()
	if dev == nil {
		return
	}
	buf := make([]byte, 1500)
	for {
		select {
		case <-e.stopCh:
			return
		default:
		}
		n, err := dev.Read(buf)
		if err != nil {
			return
		}
		ipPkt := buf[:n]
		// Tunnel IPv4/IPv6 unicast. Windows floods every new adapter with IPv6
		// and IPv4 multicast/broadcast service-discovery (mDNS, SSDP); forwarding
		// it saturates the uplink and starves the downlink NAT mapping.
		// Multicast/broadcast filtering is version-aware.
		version := ipPkt[0] >> 4
		if n < 20 || (version != 4 && version != 6) {
			continue
		}
		if version == 4 {
			if d0 := ipPkt[16]; d0 >= 224 || d0 == 0 {
				continue // 224.0.0.0/4 multicast, 255.x broadcast, 0.x
			}
		} else {
			if ipPkt[8]&0xf0 == 0xe0 {
				continue // ff00::/8 multicast
			}
		}
		dbg("TUN→ read %d bytes (uplink)", n)
		e.mu.Lock()
		aead := e.aead
		e.mu.Unlock()
		if aead == nil {
			continue
		}
		ciphertext, serr := aead.Seal(ipPkt)
		if serr != nil {
			dbg("uplink seal err: %v", serr)
			return
		}
		frame := e.tr.Wrap(protocol.PktTypeData, ciphertext)
		if err := e.sendUplink(frame); err != nil {
			dbg("uplink send err: %v", err)
			return
		}
		dbg("→uplink sent %d enc bytes", len(frame))
		e.stats.AddUp(n)
	}
}

// slaveReadLoop receives encrypted downlink packets from Slave and injects into TUN.
func (e *Engine) slaveReadLoop(slaveConn *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		select {
		case <-e.stopCh:
			return
		default:
		}
		n, src, err := slaveConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		dbg("←slave got %d bytes from %s (downlink)", n, src)
		pktType, payload, uerr := e.tr.Unwrap(buf[:n])
		if uerr != nil {
			dbg("downlink bad frame")
			continue
		}

		// RTT probe reply: hand the echoed timestamp to probeRTT (non-blocking).
		if pktType == protocol.PktTypePong {
			e.mu.Lock()
			ch := e.pongCh
			e.mu.Unlock()
			if ch != nil {
				select {
				case ch <- append([]byte{}, payload...):
				default:
				}
			}
			continue
		}

		e.mu.Lock()
		aead := e.slaveAead
		dev := e.tunDev
		e.mu.Unlock()

		var ipPkt []byte
		if aead != nil {
			ipPkt, err = aead.Open(payload)
			if err != nil {
				dbg("downlink decrypt FAILED: %v", err)
				continue
			}
		} else {
			ipPkt = payload
		}

		if dev != nil {
			if _, werr := dev.Write(ipPkt); werr != nil {
				dbg("TUN write err: %v", werr)
			} else {
				dbg("→TUN wrote %d bytes (downlink delivered)", len(ipPkt))
			}
		}
		e.stats.AddDown(len(ipPkt))
	}
}

func (e *Engine) keepaliveLoop() {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	frame := e.tr.Wrap(protocol.PktTypeKeepalive, nil)
	for {
		select {
		case <-tick.C:
			e.sendUplink(frame)
		case <-e.stopCh:
			return
		}
	}
}
