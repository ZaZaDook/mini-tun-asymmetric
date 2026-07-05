package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/config"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/crypto"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/netfw"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/nettune"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/transport"
	"github.com/ZaZaDook/mini-tun-asymmetric/master/control"
	"github.com/ZaZaDook/mini-tun-asymmetric/master/dataplane"
	dnsresolver "github.com/ZaZaDook/mini-tun-asymmetric/master/dns"
	"github.com/ZaZaDook/mini-tun-asymmetric/master/metrics"
	"github.com/ZaZaDook/mini-tun-asymmetric/master/netstack"
	"github.com/ZaZaDook/mini-tun-asymmetric/master/session"
)

// version is set at build time via -ldflags "-X main.version=$(cat VERSION)".
var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/mini-tun-asymmetric/master.json", "path to master config")
	genConfig := flag.Bool("gen-config", false, "write a default config to stdout and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("mini-tun-asymmetric-master", version)
		return
	}

	if *genConfig {
		cfg := config.DefaultMasterConfig()
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(cfg)
		return
	}

	cfg, err := config.LoadMasterConfig(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	tokenBytes, err := base64.StdEncoding.DecodeString(cfg.AuthToken)
	if err != nil {
		log.Fatalf("invalid auth_token (must be base64): %v", err)
	}

	// Firewall subsystem: detect backend, reconcile any leftovers from a previous
	// (possibly crashed) run — only OUR tagged objects — then ensure a clean table
	// with the dynamic data-port set. Dynamic data ports (port-hopping) are then
	// opened/closed per session instead of exposing the whole ephemeral range.
	fw := netfw.New()
	log.Printf("[master] firewall backend: %s", fw.Name())
	if err := fw.Reconcile(); err != nil {
		log.Printf("[master] firewall reconcile: %v", err)
	}
	if err := fw.Ensure(); err != nil {
		log.Printf("[master] firewall ensure: %v", err)
	}

	// Parse tunnel subnet
	_, subnet, err := net.ParseCIDR(cfg.TunnelSubnet)
	if err != nil {
		log.Fatalf("invalid tunnel_subnet: %v", err)
	}
	gatewayIP := net.ParseIP(cfg.TunnelIP).To4()
	// IPv4-only pool. IPv6 is NOT wired into the data path yet (the netstack
	// drops v6 at outbound), and a .WithIPv6(fd00::/64) here would make the IP
	// pool try to pre-enumerate 2^64 addresses on startup — the master would
	// hang forever before binding any port. Re-enable only once the IP pool
	// allocates IPv6 lazily (IPv6 phase 1).
	ipPool := session.NewIPPool(subnet, gatewayIP)
	sessions := session.NewTable()
	// The reaper-removal hook (ipPool release, socket/fd close, crypto cleanup,
	// metric decrement) is installed once below, after srv/fw exist — see
	// sessions.OnRemove near dp.OnUplink. A single hook avoids the earlier bug
	// where a second assignment silently overwrote the first.

	// Master key derived from auth token (used for data plane encryption)
	masterKey := crypto.DeriveKey(tokenBytes, []byte("master-data-plane"))

	// TLS for control plane — supports Let's Encrypt certs (fullchain.pem + privkey.pem)
	tlsCfg := loadTLS(cfg)

	// Create slave control server (started after the data plane is wired below).
	ctrlSrv := control.NewServer(tlsCfg)

	// Start data plane (UDP) for downlink forwarding to slaves
	dpListenAddr := cfg.ListenDataPlane
	if dpListenAddr == "" {
		dpListenAddr = "0.0.0.0:7003"
	}
	dp, err := dataplane.NewMasterDataPlane(dpListenAddr, masterKey)
	if err != nil {
		log.Fatalf("dataplane listen error: %v", err)
	}
	log.Printf("[master] data plane on %s", dpListenAddr)

	// Wire the control plane to the data plane: when a slave registers, record
	// its routable data-plane endpoint so downlink packets can reach it.
	ctrlSrv.OnSlaveRegister = func(slaveID, dataAddr string) {
		udpAddr, err := net.ResolveUDPAddr("udp", dataAddr)
		if err != nil {
			log.Printf("[master] bad slave data-plane addr %q: %v", dataAddr, err)
			return
		}
		dp.RegisterSlave(slaveID, udpAddr)
	}

	// Now that the hook is set, start accepting slave connections.
	go func() {
		if err := ctrlSrv.Listen(cfg.ListenControl); err != nil {
			log.Fatalf("control server error: %v", err)
		}
	}()

	// Userspace TCP/IP stack (gVisor): terminates client TCP/UDP connections
	// properly and proxies the byte streams to the internet. Response packets it
	// emits are routed to the owning slave for downlink delivery.
	ns, err := netstack.New(func(ipPkt []byte) {
		version := ipPkt[0] >> 4
		if len(ipPkt) < 20 || (version != 4 && version != 6) {
			return
		}
		// Extract destination IP: bytes [16:20] for IPv4, [24:40] for IPv6.
		var dstIP net.IP
		if version == 4 {
			dstIP = net.IP(ipPkt[16:20])
		} else {
			dstIP = net.IP(ipPkt[24:40])
		}
		sess, ok := sessions.ByTunIP(dstIP.String())
		if !ok {
			return
		}
		dp.SendToSlave(sess.Slave(), append([]byte{}, ipPkt...))
	}, log.Printf)
	if err != nil {
		log.Fatalf("netstack init: %v", err)
	}

	// In-tunnel DNS resolver: clients are forced to send DNS to the tunnel
	// gateway, and the master resolves upstream (plain/dot/doh) returning real
	// IPs — so a client-side router fake-dns can't inject fake 198.18.x.x.
	// AAAA (IPv6) is passed through by default; set dns_suppress_aaaa in config
	// to drop it (e.g. when the tunnel is IPv4-only).
	suppressAAAA := false
	if cfg.DNSSuppressAAAA != nil {
		suppressAAAA = *cfg.DNSSuppressAAAA
	}
	resolver, derr := dnsresolver.New(dnsresolver.Config{
		Mode:         dnsresolver.Mode(cfg.DNSMode),
		Upstream:     cfg.DNSUpstream,
		DoHURL:       cfg.DNSUpstreamDoH,
		SuppressAAAA: suppressAAAA,
		Logf:         log.Printf,
	})
	if derr != nil {
		log.Fatalf("dns resolver init: %v", derr)
	}
	ns.SetDNS(gatewayIP.String(), resolver)
	log.Printf("[master] in-tunnel DNS on %s:53 (mode=%s, suppressAAAA=%v)",
		gatewayIP, cfg.DNSMode, suppressAAAA)

	var serverID [8]byte
	copy(serverID[:], cfg.ServerID)

	// Metrics registry (foundation for failover): per-slave health + counters.
	mreg := metrics.New(time.Now())
	mreg.SetSlaveStatFunc(func() []metrics.SlaveStat {
		bySlave := sessions.CountBySlave()
		health := ctrlSrv.Registry.Health()
		now := time.Now()
		out := make([]metrics.SlaveStat, 0, len(health))
		for _, h := range health {
			out = append(out, metrics.SlaveStat{
				SlaveID:     h.SlaveID,
				Connected:   true,
				LastSeenMS:  now.Sub(h.LastSeen).Milliseconds(),
				Sessions:    bySlave[h.SlaveID],
				DataPlaneUp: h.DataPlaneUp,
			})
		}
		return out
	})
	if cfg.MetricsListen != "" {
		if err := mreg.Serve(cfg.MetricsListen, time.Now, log.Printf); err != nil {
			log.Printf("[master] metrics serve error: %v", err)
		}
	}

	srv := &pktServer{
		tokenBytes:    tokenBytes,
		sessions:      sessions,
		ipPool:        ipPool,
		ctrlSrv:       ctrlSrv,
		dp:            dp,
		ns:            ns,
		serverID:      serverID,
		clientCryptos: &clientCryptoMap{},
		replays:       newReplayCache(),
		mreg:          mreg,
		fw:            fw,
		uplinkAEADs:   make(map[string]*crypto.AEAD),
	}
	// QUIC symmetric mode: a slave relays client uplink to the master here.
	dp.OnUplink = srv.handleUplink
	// Close a session's per-session data socket when it's reaped (and release its
	// tunnel IP). Without closing the socket the master leaks an fd per session.
	sessions.OnRemove = func(s *session.Session) {
		ipPool.Release(s.TunnelIP)
		if s.DataConn != nil {
			// Close the dynamic data port in the firewall before closing the
			// socket, so the per-session port stops being accepted once the
			// session ends (port-hopping cleanup — no lingering open ports).
			if la, ok := s.DataConn.LocalAddr().(*net.UDPAddr); ok && la.Port != 0 {
				fw.DelDynPort(la.Port)
			}
			s.DataConn.Close()
		}
		srv.uplinkMu.Lock()
		delete(srv.uplinkAEADs, s.TunnelIP.String())
		srv.uplinkMu.Unlock()
		// Also drop the per-client decrypt cipher keyed by the client's UDP
		// address, else clientCryptos grows by one AEAD per handshake forever.
		if s.ClientAddr != nil {
			srv.clientCryptos.Delete(s.ClientAddr.String())
		}
		// Keep the active-sessions gauge honest: it was only ever set at
		// handshake, so a reaped session left it stale (showed sessions alive
		// long after they were gone). Reflect the real table size on removal.
		srv.mreg.C.SessionsActive.Store(int64(sessions.Len()))
		// Tell every slave the session is over. The session is registered on all
		// slaves at handshake (SendSession), but slaves have no reaper of their
		// own — without this SessionEnd they keep the client's downlink address
		// forever and keep sending to it after the client is gone, which the
		// dead client's OS answers with ICMP port-unreachable (the "pings after
		// close" leak). SendSessionEnd was defined but never called until now.
		for _, sc := range srv.ctrlSrv.Registry.All() {
			sc.SendSessionEnd(s.ID)
		}
	}

	// Build the set of control listeners. Each control port is bound to an
	// transport carrier; the arrival port selects the carrier (demux). If no
	// control_ports are configured, fall back to the single ListenUDP + Transport.
	type ctrlListener struct {
		conn *net.UDPConn
		tr   transport.Transport
	}
	var listeners []ctrlListener
	if len(cfg.ControlPorts) > 0 {
		for _, cp := range cfg.ControlPorts {
			ctr, terr := transport.ByName(cp.Transport)
			if terr != nil {
				log.Fatalf("control port %d: %v", cp.Port, terr)
			}
			ca, err := net.ResolveUDPAddr("udp", fmt.Sprintf("0.0.0.0:%d", cp.Port))
			if err != nil {
				log.Fatalf("control port %d: %v", cp.Port, err)
			}
			cc, err := net.ListenUDP("udp", ca)
			if err != nil {
				log.Fatalf("control port %d listen: %v", cp.Port, err)
			}
			nettune.TuneUDP(cc)
			listeners = append(listeners, ctrlListener{cc, ctr})
			fw.OpenPort(netfw.UDP, cp.Port) // allow this control port inbound
			log.Printf("[master] control port %d → transport %s", cp.Port, ctr.Name())
		}
	} else {
		tr, terr := transport.ByName(cfg.Transport)
		if terr != nil {
			log.Fatalf("invalid transport: %v", terr)
		}
		udpAddr, err := net.ResolveUDPAddr("udp", cfg.ListenUDP)
		if err != nil {
			log.Fatalf("invalid listen_udp: %v", err)
		}
		conn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			log.Fatalf("UDP listen: %v", err)
		}
		if err := nettune.TuneUDP(conn); err != nil {
			log.Printf("[master] UDP buffer tune: %v (throughput may be limited; raise net.core.rmem_max)", err)
		}
		listeners = append(listeners, ctrlListener{conn, tr})
		if la, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			fw.OpenPort(netfw.UDP, la.Port)
		}
		log.Printf("[master] listening UDP on %s (transport %s)", cfg.ListenUDP, tr.Name())
	}

	// Also allow the slave control (TCP) and data-plane (UDP) ports.
	if la, e := net.ResolveUDPAddr("udp", dpListenAddr); e == nil {
		fw.OpenPort(netfw.UDP, la.Port)
	}
	if _, p, e := net.SplitHostPort(cfg.ListenControl); e == nil {
		if pn := atoiSafe(p); pn > 0 {
			fw.OpenPort(netfw.TCP, pn)
		}
	}

	// Graceful teardown: on SIGTERM/SIGINT remove our firewall rules so nothing
	// lingers after shutdown (systemd stop, Ctrl-C). ExecStopPost in the unit is
	// a backstop for kill -9; Reconcile at next start also cleans up.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Printf("[master] shutting down, tearing down firewall rules")
		fw.Teardown()
		os.Exit(0)
	}()

	// Serve every control listener; block on the last one.
	for i := 1; i < len(listeners); i++ {
		l := listeners[i]
		go srv.serve(l.conn, l.tr, true)
	}
	srv.serve(listeners[0].conn, listeners[0].tr, true)
}

// pktServer holds the shared state for handling client UDP packets across the
// control listeners and the per-session data sockets.
type pktServer struct {
	tokenBytes    []byte
	sessions      *session.Table
	ipPool        *session.IPPool
	ctrlSrv       *control.Server
	dp            *dataplane.MasterDataPlane
	ns            *netstack.NetStack
	serverID      [8]byte
	clientCryptos *clientCryptoMap
	replays       *replayCache
	mreg          *metrics.Registry
	fw            netfw.Firewall // server firewall (dynamic data-port management)

	// uplinkAEADs caches per-session decrypt ciphers for the QUIC slave→master
	// uplink relay, keyed by tunnel IP string (the relay identifies sessions by
	// tunnel IP, not client addr — the client's packets reach us via the slave).
	uplinkMu    sync.Mutex
	uplinkAEADs map[string]*crypto.AEAD
}

// srcMatchesTunnel reports whether a decrypted client IP packet's source address
// equals the session's assigned tunnel IP (reverse-path filter). Without this an
// authenticated client could spoof another session's tunnel IP as the inner src,
// so the master's reply traffic for that flow would be delivered to the spoofed
// IP's owner. Only IPv4 is checked (data-path is IPv4-only); non-IPv4 or truncated
// packets are rejected. ipPkt is a raw IPv4 packet: src address is bytes [12:16].
func srcMatchesTunnel(ipPkt []byte, tunnelIP net.IP) bool {
	if len(ipPkt) < 20 || ipPkt[0]>>4 != 4 {
		return false
	}
	want := tunnelIP.To4()
	if want == nil {
		return false
	}
	src := ipPkt[12:16]
	return src[0] == want[0] && src[1] == want[1] && src[2] == want[2] && src[3] == want[3]
}

// handleUplink processes a client uplink packet relayed by a slave (QUIC symmetric
// mode). The payload is still encrypted with the per-session key; we decrypt it
// and inject it into the gVisor stack, exactly as the direct uplink path does.
func (s *pktServer) handleUplink(tunnelIP net.IP, clientCiphertext []byte) {
	sess, ok := s.sessions.ByTunIP(tunnelIP.String())
	if !ok {
		return
	}
	sess.Touch()
	key := tunnelIP.String()
	s.uplinkMu.Lock()
	aead := s.uplinkAEADs[key]
	if aead == nil {
		a, err := crypto.NewAEAD(sess.CryptoKey)
		if err != nil {
			s.uplinkMu.Unlock()
			return
		}
		aead = a
		s.uplinkAEADs[key] = aead
	}
	s.uplinkMu.Unlock()

	ipPkt, err := aead.Open(clientCiphertext)
	if err != nil {
		return
	}
	if !srcMatchesTunnel(ipPkt, tunnelIP) {
		s.mreg.C.SpoofedSrc.Add(1)
		return
	}
	s.mreg.C.ClientPackets.Add(1)
	s.mreg.C.BytesUplink.Add(uint64(len(ipPkt)))
	s.ns.InjectClient(ipPkt)
}

// serve reads packets from one UDP socket framed by transport tr. acceptHandshake
// is true for control sockets (where new sessions begin) and false for per-session
// data sockets (which only carry data/keepalive for an established session).
func (s *pktServer) serve(conn *net.UDPConn, tr transport.Transport, acceptHandshake bool) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed (session reaped) or fatal read error
		}
		pktType, rawPayload, err := tr.Unwrap(buf[:n])
		if err != nil {
			continue // not our protocol
		}

		switch pktType {
		case protocol.PktTypeHandshake:
			if acceptHandshake {
				s.handleHandshake(conn, addr, rawPayload, tr)
			}

		case protocol.PktTypeData:
			sess, ok := s.sessions.ByClient(addr.String())
			if !ok {
				continue
			}
			sess.Touch()
			aead := s.clientCryptos.Get(addr.String())
			if aead == nil {
				continue
			}
			ipPkt, err := aead.Open(rawPayload)
			if err != nil {
				continue
			}
			if !srcMatchesTunnel(ipPkt, sess.TunnelIP) {
				s.mreg.C.SpoofedSrc.Add(1)
				continue
			}
			s.mreg.C.ClientPackets.Add(1)
			s.mreg.C.BytesUplink.Add(uint64(len(ipPkt)))
			s.ns.InjectClient(ipPkt)

		case protocol.PktTypeKeepalive:
			if sess, ok := s.sessions.ByClient(addr.String()); ok {
				sess.Touch()
			}

		case protocol.PktTypeControl:
			// A v3 client's slave choice after RTT-probing the candidate list.
			s.handleSlaveChoice(rawPayload)
		}
	}
}

// handleSlaveChoice re-pins a session's downlink slave to the one the client
// picked (nearest by RTT). The session was pre-registered on all slaves at
// handshake; here we just record which slave the master should send downlink
// through. Validated against the registry so a client can't point us at junk.
func (s *pktServer) handleSlaveChoice(payload []byte) {
	if len(payload) == 0 || payload[0] != protocol.CtrlSlaveChoice {
		return
	}
	msg, err := protocol.ParseSlaveChoice(payload)
	if err != nil {
		return
	}
	sess, ok := s.sessions.ByTunIP(msg.TunnelIP.String())
	if !ok {
		return
	}
	// Find the slave whose UDP endpoint matches the client's chosen IP:port.
	for _, sc := range s.ctrlSrv.Registry.All() {
		ua, e := net.ResolveUDPAddr("udp", sc.UDPAddr)
		if e != nil {
			continue
		}
		if ua.IP.Equal(msg.SlaveIP) && uint16(ua.Port) == msg.SlavePort {
			if sess.Slave() != sc.SlaveID {
				sess.SetSlave(sc.SlaveID)
				log.Printf("[master] session tunIP=%s → slave pinned to %s (client RTT choice)",
					msg.TunnelIP, sc.SlaveID)
			}
			return
		}
	}
}

// serveData reads a per-session data socket (allocated at handshake for
// port-hopping). It is bound to ONE session and decrypts with that session's
// AEAD directly — it does NOT look up by source address, because the client's
// uplink arrives from a different source port than the handshake used (the client
// dials a fresh socket to the data port). Handshakes are ignored here.
func (s *pktServer) serveData(conn *net.UDPConn, tr transport.Transport, sess *session.Session, aead *crypto.AEAD) {
	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed (session reaped)
		}
		pktType, rawPayload, err := tr.Unwrap(buf[:n])
		if err != nil {
			continue
		}
		switch pktType {
		case protocol.PktTypeData:
			sess.Touch()
			ipPkt, err := aead.Open(rawPayload)
			if err != nil {
				continue
			}
			if !srcMatchesTunnel(ipPkt, sess.TunnelIP) {
				s.mreg.C.SpoofedSrc.Add(1)
				continue
			}
			s.mreg.C.ClientPackets.Add(1)
			s.mreg.C.BytesUplink.Add(uint64(len(ipPkt)))
			s.ns.InjectClient(ipPkt)
		case protocol.PktTypeKeepalive:
			sess.Touch()
		}
	}
}

// atoiSafe parses a port string, returning 0 on error.
func atoiSafe(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func (s *pktServer) handleHandshake(conn *net.UDPConn, addr *net.UDPAddr, payload []byte,
	tr transport.Transport) {

	if len(payload) < 1 || payload[0] != protocol.CtrlHello {
		return
	}
	hello, err := protocol.ParseHello(payload)
	if err != nil {
		// Wrong version / too short / malformed. Stay silent (anti-probe).
		log.Printf("[master] bad hello from %s: %v", addr, err)
		return
	}

	// Anti-replay / anti-probe. The token never travels on the wire; the client
	// signs version+timestamp+nonce with HMAC(token). We verify the tag, reject
	// stale timestamps (clock-skew window) and replayed nonces, and on ANY miss
	// we stay silent so an active probe learns nothing about this server.
	now := time.Now().Unix()
	skew := now - int64(hello.Timestamp)
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(helloWindow/time.Second) {
		s.mreg.C.AuthFailures.Add(1)
		log.Printf("[master] hello skew from %s: %ds out of window", addr, skew)
		return
	}
	if !crypto.VerifyHelloMAC(s.tokenBytes, hello.MarshalUnsigned(), hello.MAC[:]) {
		s.mreg.C.AuthFailures.Add(1)
		log.Printf("[master] hello bad mac from %s", addr)
		return
	}
	if !s.replays.checkAndAdd(hello.Nonce, now) {
		s.mreg.C.AuthFailures.Add(1)
		log.Printf("[master] hello replay from %s", addr)
		return
	}

	slave := s.ctrlSrv.Registry.Pick()
	if slave == nil {
		log.Printf("[master] no slave available for %s", addr)
		return
	}

	tunIP, ok := s.ipPool.Allocate()
	if !ok {
		log.Printf("[master] IP pool exhausted")
		return
	}

	id, err := session.NewSessionID()
	if err != nil {
		s.ipPool.Release(tunIP)
		return
	}

	// Derive per-session AEAD key bound to the tunnel IP. The client and slave
	// derive the identical key via crypto.SessionKey, so all three sides agree.
	sessionKey := crypto.SessionKey(s.tokenBytes, tunIP)
	aead, err := crypto.NewAEAD(sessionKey)
	if err != nil {
		s.ipPool.Release(tunIP)
		return
	}
	s.clientCryptos.Set(addr.String(), aead)

	// Port-hopping: allocate a fresh ephemeral DATA socket for this session and
	// serve it. The client switches its uplink there (told via Welcome.DataPort),
	// so heavy traffic leaves the well-known control port. The socket is closed
	// when the session is reaped (Table.OnRemove) — otherwise we'd leak an fd.
	var dataPort uint16
	var dataConn *net.UDPConn
	if dc, derr := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0}); derr == nil {
		nettune.TuneUDP(dc)
		dataConn = dc
		dataPort = uint16(dc.LocalAddr().(*net.UDPAddr).Port)
		// Open this exact data port in the firewall for the lifetime of the
		// session (closed in Table.OnRemove). Replaces exposing the whole
		// ephemeral range — only active sessions' ports are ever open.
		if s.fw != nil {
			s.fw.AddDynPort(int(dataPort))
		}
	}

	sess := &session.Session{
		ID:         id,
		ClientAddr: addr,
		TunnelIP:   tunIP,
		LastSeen:   time.Now(),
		SlaveID:    slave.SlaveID,
		DataConn:   dataConn,
	}
	copy(sess.CryptoKey[:], sessionKey[:])
	s.sessions.Add(sess)
	s.mreg.C.SessionsCreated.Add(1)
	s.mreg.C.SessionsActive.Store(int64(s.sessions.Len()))

	// Serve the data socket after the session is registered. It belongs to THIS
	// session, so it decrypts with this session's AEAD directly — it must NOT look
	// the session up by source address, because port-hopping means the client's
	// uplink arrives from a different source port than the handshake used.
	if dataConn != nil {
		go s.serveData(dataConn, tr, sess, aead)
	}

	// Notify slave about new session. For a v3 (nearest-node-capable) client we
	// register the session on ALL slaves so each will echo the client's RTT pings
	// and accept its downlink; the client probes them and pins one via SlaveChoice.
	// For v2 clients only the round-robin pick is notified (classic behaviour).
	v3 := hello.Version == protocol.HelloVersion3
	slaveUDPAddr, _ := net.ResolveUDPAddr("udp", slave.UDPAddr)
	sessMsg := protocol.SlaveSessionMsg{
		SessionID:  id,
		ClientIP:   addr.IP.To4(),
		ClientPort: uint16(addr.Port),
		TunnelIP:   tunIP.To4(),
	}
	slaveJSON, _ := json.Marshal(sessMsg)
	if v3 {
		for _, sc := range s.ctrlSrv.Registry.All() {
			sc.SendSession(slaveJSON)
		}
	} else {
		slave.SendSession(slaveJSON)
	}

	// Build welcome packet. v3 carries the full candidate list (client picks the
	// nearest by RTT); v2 carries only the round-robin pick.
	welcome := protocol.WelcomeMsg{
		AssignedIP: tunIP,
		SlaveIP:    slaveUDPAddr.IP.To4(),
		SlavePort:  uint16(slaveUDPAddr.Port),
		DataPort:   dataPort,
		ServerID:   s.serverID,
	}
	if v3 {
		for _, sc := range s.ctrlSrv.Registry.All() {
			if ua, e := net.ResolveUDPAddr("udp", sc.UDPAddr); e == nil {
				welcome.Slaves = append(welcome.Slaves, protocol.SlaveEndpoint{
					IP: ua.IP.To4(), Port: uint16(ua.Port),
				})
			}
		}
	}
	resp := tr.Wrap(protocol.PktTypeControl, welcome.Marshal())
	conn.WriteToUDP(resp, addr)

	log.Printf("[master] session %x: %s → tunIP=%s slave=%s dataPort=%d v3=%v slaves=%d",
		id[:4], addr, tunIP, slave.SlaveID, dataPort, v3, len(welcome.Slaves))
}

// loadTLS loads a TLS config from cert/key files.
// Supports Let's Encrypt style: fullchain.pem + privkey.pem.
func loadTLS(cfg *config.MasterConfig) *tls.Config {
	certFile := cfg.TLSCertFile
	keyFile := cfg.TLSKeyFile
	// Support LE naming convention
	if certFile == "" {
		certFile = "/etc/mini-tun-asymmetric/tls/fullchain.pem"
	}
	if keyFile == "" {
		keyFile = "/etc/mini-tun-asymmetric/tls/privkey.pem"
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Printf("[master] TLS cert load failed (%v) — using self-signed", err)
		return selfSignedTLS()
	}
	log.Printf("[master] TLS loaded: %s", certFile)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
	}
}

func selfSignedTLS() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
	}
}

// clientCryptoMap maps client UDP address → per-session AEAD.
type clientCryptoMap struct {
	mu   sync.RWMutex
	m    map[string]*crypto.AEAD
}

func (c *clientCryptoMap) Set(addr string, aead *crypto.AEAD) {
	c.mu.Lock()
	if c.m == nil {
		c.m = make(map[string]*crypto.AEAD)
	}
	c.m[addr] = aead
	c.mu.Unlock()
}

func (c *clientCryptoMap) Get(addr string) *crypto.AEAD {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[addr]
}

func (c *clientCryptoMap) Delete(addr string) {
	c.mu.Lock()
	delete(c.m, addr)
	c.mu.Unlock()
}

// helloWindow is the maximum clock skew tolerated between a client's hello
// timestamp and the master's clock. A hello outside this window is rejected
// (silently). Nonces are kept in the replay cache for twice this long.
const helloWindow = 60 * time.Second

// replayCache tracks recently-seen hello nonces to reject replayed handshakes.
// A captured v2 hello cannot be re-used: its nonce lands here on first sight, and
// any repeat within the retention window is dropped. Entries older than the
// window are swept lazily so the map can't grow without bound.
type replayCache struct {
	mu   sync.Mutex
	seen map[[16]byte]int64 // nonce -> unix seconds first seen
}

func newReplayCache() *replayCache {
	return &replayCache{seen: make(map[[16]byte]int64)}
}

// checkAndAdd reports whether nonce is fresh (not seen within the window). If
// fresh it records the nonce and returns true; a replay returns false. It also
// sweeps expired entries opportunistically.
func (r *replayCache) checkAndAdd(nonce [16]byte, now int64) bool {
	cutoff := now - int64(2*helloWindow/time.Second)
	r.mu.Lock()
	defer r.mu.Unlock()
	// Sweep expired entries first so a nonce older than the window doesn't count
	// as a replay (it can legitimately reappear once it has aged out).
	for n, ts := range r.seen {
		if ts < cutoff {
			delete(r.seen, n)
		}
	}
	if _, ok := r.seen[nonce]; ok {
		return false
	}
	r.seen[nonce] = now
	return true
}
