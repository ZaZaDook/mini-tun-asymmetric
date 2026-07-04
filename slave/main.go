package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/config"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/crypto"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/netfw"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/nettune"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/transport"
	"github.com/ZaZaDook/mini-tun-asymmetric/master/dataplane"
)

const keepaliveInterval = 15 * time.Second

type clientSession struct {
	mu         sync.Mutex
	ID         [16]byte
	ClientAddr *net.UDPAddr // real downlink address; updated by client punch packets
	TunnelIP   net.IP
	aead       *crypto.AEAD // for encrypting downlink packets to client
}

func (c *clientSession) addr() *net.UDPAddr {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ClientAddr
}

func (c *clientSession) setAddr(a *net.UDPAddr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ClientAddr = a
}

type slaveServer struct {
	cfg      *config.SlaveConfig
	udpConn  *net.UDPConn
	sessions sync.Map // tunnelIP string → *clientSession
	byAddr   sync.Map // client UDP addr string → *clientSession (QUIC uplink lookup)
	masterKey [crypto.KeySize]byte
	token     []byte // raw auth token, for per-session client keys
	tr        transport.Transport // outer transport envelope (client downlink/punch)

	// QUIC symmetric mode: the slave relays client uplink to the master over the
	// data-plane socket. relayConn is the data-plane UDP socket (so the master sees
	// the registered slave source address); relayAEAD is the per-slave key; masterDP
	// is the master's data-plane address.
	relayConn *net.UDPConn
	relayAEAD *crypto.AEAD
	masterDP  *net.UDPAddr
}

// relayToMaster forwards a client's still-encrypted uplink to the master (QUIC
// mode). The master decrypts it with the per-session key and injects into gVisor.
func (s *slaveServer) relayToMaster(tunnelIP net.IP, clientCiphertext []byte) {
	if s.relayConn == nil || s.relayAEAD == nil || s.masterDP == nil {
		return
	}
	frame, err := dataplane.BuildUplinkFrame(s.relayAEAD, tunnelIP, clientCiphertext)
	if err != nil {
		return
	}
	s.relayConn.WriteToUDP(frame, s.masterDP)
}

func main() {
	cfgPath := flag.String("config", "/etc/mini-tun-asymmetric/slave.json", "path to slave config")
	genConfig := flag.Bool("gen-config", false, "write a default config to stdout and exit")
	flag.Parse()

	if *genConfig {
		cfg := config.DefaultSlaveConfig()
		enc := json.NewEncoder(log.Writer())
		enc.SetIndent("", "  ")
		enc.Encode(cfg)
		return
	}

	cfg, err := config.LoadSlaveConfig(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	tokenBytes, err := base64.StdEncoding.DecodeString(cfg.AuthToken)
	if err != nil {
		log.Fatalf("invalid auth_token: %v", err)
	}
	masterKey := crypto.DeriveKey(tokenBytes, []byte("master-data-plane"))

	// Bind downlink UDP socket (sends to clients)
	udpAddr, err := net.ResolveUDPAddr("udp", cfg.ListenUDP)
	if err != nil {
		log.Fatalf("invalid listen_udp: %v", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("UDP listen: %v", err)
	}
	if err := nettune.TuneUDP(udpConn); err != nil {
		log.Printf("[slave] UDP buffer tune: %v (raise net.core.rmem_max)", err)
	}
	log.Printf("[slave] UDP downlink on %s", cfg.ListenUDP)

	srv := &slaveServer{cfg: cfg, udpConn: udpConn, masterKey: masterKey, token: tokenBytes}
	srvTr, terr := transport.ByName(cfg.Transport)
	if terr != nil {
		log.Fatalf("invalid transport: %v", terr)
	}
	srv.tr = srvTr
	log.Printf("[slave] transport carrier: %s", srv.tr.Name())

	// Listen for encrypted downlink packets from master data plane
	dataAddr, err := net.ResolveUDPAddr("udp", cfg.ListenDataPlane)
	if err != nil {
		log.Fatalf("invalid listen_data_plane: %v", err)
	}
	dataConn, err := net.ListenUDP("udp", dataAddr)
	if err != nil {
		log.Fatalf("data plane listen: %v", err)
	}
	nettune.TuneUDP(dataConn)
	log.Printf("[slave] data plane on %s", cfg.ListenDataPlane)

	// QUIC symmetric mode: prepare the uplink relay back to the master. Send from
	// the data-plane socket (so the master recognizes our registered source addr),
	// keyed with the same per-slave key the downlink uses.
	relaySalt := []byte(cfg.SlaveID)
	relayAEAD, raerr := crypto.NewAEAD(crypto.DeriveKey(masterKey[:], relaySalt))
	if raerr == nil {
		srv.relayConn = dataConn
		srv.relayAEAD = relayAEAD
		mdp := cfg.MasterDataPlane
		if mdp == "" {
			// Derive from the control address host + default data-plane port 7003.
			if host, _, e := net.SplitHostPort(cfg.MasterControl); e == nil {
				mdp = net.JoinHostPort(host, "7003")
			}
		}
		if mdp != "" {
			if a, e := net.ResolveUDPAddr("udp", mdp); e == nil {
				srv.masterDP = a
				log.Printf("[slave] uplink relay target (master data plane): %s", a)
			}
		}
	}

	go srv.dataPlaneLoop(dataConn)
	go srv.clientPunchLoop()
	go srv.connectMaster()

	// Firewall: reconcile leftovers from a prior run, then open our static ports
	// (downlink + data-plane). Tear down on SIGTERM/SIGINT so nothing lingers.
	fw := netfw.New()
	log.Printf("[slave] firewall backend: %s", fw.Name())
	fw.Reconcile()
	fw.Ensure()
	if la, e := net.ResolveUDPAddr("udp", cfg.ListenUDP); e == nil {
		fw.OpenPort(netfw.UDP, la.Port)
	}
	if la, e := net.ResolveUDPAddr("udp", cfg.ListenDataPlane); e == nil {
		fw.OpenPort(netfw.UDP, la.Port)
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	log.Printf("[slave] shutting down, tearing down firewall rules")
	fw.Teardown()
}

// dataPlaneLoop receives downlink IP packets from master and forwards to clients.
func (s *slaveServer) dataPlaneLoop(conn *net.UDPConn) {
	salt := []byte(s.cfg.SlaveID)
	key := crypto.DeriveKey(s.masterKey[:], salt)
	aead, err := crypto.NewAEAD(key)
	if err != nil {
		log.Fatalf("data plane aead: %v", err)
	}

	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("[slave] data plane read: %v", err)
			return
		}
		if n < 3 || buf[0] != 0xDA {
			continue
		}
		length := binary.BigEndian.Uint16(buf[1:3])
		if int(length) > n-3 {
			continue
		}
		ipPkt, err := aead.Open(buf[3 : 3+length])
		if err != nil {
			continue
		}
		s.sendToClient(ipPkt)
	}
}

// clientPunchLoop reads the downlink UDP socket for client punch packets. Each
// punch opens a NAT mapping toward the client and tells us the client's real
// downlink address, which we record so downlink packets reach it even when the
// client sits behind restrictive/symmetric NAT.
func (s *slaveServer) clientPunchLoop() {
	buf := make([]byte, 2048)
	for {
		n, src, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("[slave] punch read: %v", err)
			return
		}
		pktType, payload, uerr := s.tr.Unwrap(buf[:n])
		if uerr != nil {
			continue
		}

		// QUIC symmetric mode: the client sends its uplink data to the slave (same
		// socket as downlink), and we relay it to the master. Identify the session
		// by the client's source address (registered by the punch below).
		if pktType == protocol.PktTypeData {
			if val, ok := s.byAddr.Load(src.String()); ok {
				sess := val.(*clientSession)
				s.relayToMaster(sess.TunnelIP, append([]byte{}, payload...))
			}
			continue
		}

		// RTT probe: echo a Ping back as a Pong, but ONLY to a client whose tunnel
		// IP we already have a session for. An unauthenticated prober gets silence
		// (anti-probe), and the echo lets the client measure the slave→client leg.
		// Payload: [0:4]=tunnelIP [4:12]=client timestamp (echoed verbatim).
		if pktType == protocol.PktTypePing {
			if len(payload) < 12 {
				continue
			}
			tunnelIP := net.IP(append([]byte{}, payload[:4]...))
			if _, ok := s.sessions.Load(tunnelIP.String()); !ok {
				continue // no session for this IP → don't reveal ourselves
			}
			pong := s.tr.Wrap(protocol.PktTypePong, append([]byte{}, payload...))
			s.udpConn.WriteToUDP(pong, src)
			continue
		}

		if pktType != protocol.PktTypePunch {
			continue
		}
		if len(payload) < 4 {
			continue
		}
		tunnelIP := net.IP(append([]byte{}, payload[:4]...))
		if val, ok := s.sessions.Load(tunnelIP.String()); ok {
			sess := val.(*clientSession)
			// Always map this source addr → session for QUIC uplink lookup, even if
			// the downlink addr was already known (it may have been pre-populated
			// from the master's SlaveSessionMsg, so it won't "change" here).
			s.byAddr.Store(src.String(), sess)
			if a := sess.addr(); a == nil || a.String() != src.String() {
				sess.setAddr(src)
				log.Printf("[slave] client downlink addr for %s → %s", tunnelIP, src)
			}
		}
	}
}

// sendToClient finds which client owns this IP packet (by dst IP) and forwards it.
func (s *slaveServer) sendToClient(ipPkt []byte) {
	if len(ipPkt) < 20 {
		return
	}
	dstIP := net.IP(ipPkt[16:20]).String()

	val, ok := s.sessions.Load(dstIP)
	if !ok {
		return
	}
	sess := val.(*clientSession)

	// Encrypt the downlink packet for the client
	var ciphertext []byte
	if sess.aead != nil {
		ct, serr := sess.aead.Seal(ipPkt)
		if serr != nil {
			log.Printf("[slave] seal downlink for %s: %v", dstIP, serr)
			return
		}
		ciphertext = ct
	} else {
		ciphertext = ipPkt
	}

	pkt := s.tr.Wrap(protocol.PktTypeData, ciphertext)

	dst := sess.addr()
	if dst == nil {
		return
	}
	if _, err := s.udpConn.WriteToUDP(pkt, dst); err != nil {
		log.Printf("[slave] send to client %s: %v", dst, err)
	}
}

func (s *slaveServer) connectMaster() {
	backoff := time.Second
	for {
		log.Printf("[slave] connecting to master control %s", s.cfg.MasterControl)
		if err := s.runControlLoop(); err != nil {
			log.Printf("[slave] control error: %v — retry in %s", err, backoff)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// generateClientCert creates an ephemeral self-signed certificate the slave
// presents to the master to satisfy its RequireAnyClientCert policy.
func generateClientCert(cn string) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "mini-tun-asymmetric-slave-" + cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

func (s *slaveServer) runControlLoop() error {
	// The master requires a client certificate (mTLS, RequireAnyClientCert).
	// Generate an ephemeral self-signed client cert to present.
	clientCert, err := generateClientCert(s.cfg.SlaveID)
	if err != nil {
		return err
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: s.cfg.TLSCACertFile == "", // use CA if provided
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{clientCert},
	}
	if s.cfg.TLSCACertFile != "" {
		// Load Let's Encrypt / custom CA cert
		tlsCfg.InsecureSkipVerify = false
	}

	conn, err := tls.Dial("tcp", s.cfg.MasterControl, tlsCfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("[slave] control connected to master")

	// Register: send SlaveInfo
	info := struct {
		SlaveID     string `json:"slave_id"`
		UDPAddr     string `json:"udp_addr"`
		DataPlane   string `json:"data_plane"`
	}{
		SlaveID:   s.cfg.SlaveID,
		UDPAddr:   s.cfg.ListenUDP,
		DataPlane: s.cfg.ListenDataPlane,
	}
	infoJSON, _ := json.Marshal(info)
	if err := writeFrame(conn, 0x04, infoJSON); err != nil {
		return err
	}

	// Keepalive sender
	go func() {
		tick := time.NewTicker(keepaliveInterval)
		defer tick.Stop()
		for range tick.C {
			if err := writeFrame(conn, 0x01, nil); err != nil {
				return
			}
		}
	}()

	// Read control messages from master
	for {
		conn.SetReadDeadline(time.Now().Add(keepaliveInterval * 3))
		msgType, payload, err := readFrame(conn)
		if err != nil {
			return err
		}
		switch msgType {
		case 0x01: // keepalive
		case 0x02: // new session
			s.handleNewSession(payload)
		case 0x03: // session ended
			s.handleSessionEnd(payload)
		}
	}
}

func (s *slaveServer) handleNewSession(payload []byte) {
	var msg protocol.SlaveSessionMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("[slave] bad session msg: %v", err)
		return
	}

	// Build per-session AEAD bound to the tunnel IP — identical to the key the
	// client derives, so the client can decrypt our downlink packets.
	sessionKey := crypto.SessionKey(s.token, msg.TunnelIP)
	aead, _ := crypto.NewAEAD(sessionKey)

	sess := &clientSession{
		ID:         msg.SessionID,
		ClientAddr: &net.UDPAddr{IP: msg.ClientIP, Port: int(msg.ClientPort)},
		TunnelIP:   msg.TunnelIP,
		aead:       aead,
	}
	s.sessions.Store(msg.TunnelIP.String(), sess)
	log.Printf("[slave] session registered: tunIP=%s → %s", msg.TunnelIP, sess.ClientAddr)
}

func (s *slaveServer) handleSessionEnd(payload []byte) {
	if len(payload) < 16 {
		return
	}
	var id [16]byte
	copy(id[:], payload[:16])
	s.sessions.Range(func(k, v any) bool {
		if v.(*clientSession).ID == id {
			s.sessions.Delete(k)
			log.Printf("[slave] session ended %x", id[:4])
			return false
		}
		return true
	})
}

func writeFrame(w io.Writer, msgType uint8, payload []byte) error {
	frame := make([]byte, 3+len(payload))
	frame[0] = msgType
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(payload)))
	copy(frame[3:], payload)
	_, err := w.Write(frame)
	return err
}

func readFrame(r io.Reader) (uint8, []byte, error) {
	hdr := make([]byte, 3)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint16(hdr[1:3])
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	return hdr[0], payload, nil
}
