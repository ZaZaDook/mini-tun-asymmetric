// Package control manages the TCP control plane between Master and Slave nodes.
// Uses mTLS for mutual authentication. The master acts as server; slaves connect to it.
package control

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	keepaliveInterval = 15 * time.Second
	MsgTypeKeepalive  = uint8(0x01)
	MsgTypeSession    = uint8(0x02) // master → slave: new client session
	MsgTypeSessionEnd = uint8(0x03) // master → slave: session ended
	MsgTypeSlaveInfo  = uint8(0x04) // slave → master: slave registration info
)

// SlaveConn represents one connected slave node.
type SlaveConn struct {
	conn     net.Conn
	SlaveID  string
	UDPAddr  string // slave's public UDP endpoint for downlink
	mu       sync.Mutex
	lastSeen time.Time
}

func (sc *SlaveConn) send(msgType uint8, payload []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	frame := make([]byte, 3+len(payload))
	frame[0] = msgType
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(payload)))
	copy(frame[3:], payload)
	_, err := sc.conn.Write(frame)
	return err
}

func (sc *SlaveConn) SendSession(sessionJSON []byte) error {
	return sc.send(MsgTypeSession, sessionJSON)
}

func (sc *SlaveConn) SendSessionEnd(sessionID [16]byte) error {
	return sc.send(MsgTypeSessionEnd, sessionID[:])
}

// SlaveRegistry keeps track of all connected slave nodes.
type SlaveRegistry struct {
	mu     sync.RWMutex
	slaves map[string]*SlaveConn // key: SlaveID
	robin  int                   // round-robin index for load balancing
}

func NewSlaveRegistry() *SlaveRegistry {
	return &SlaveRegistry{slaves: make(map[string]*SlaveConn)}
}

func (r *SlaveRegistry) Register(sc *SlaveConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slaves[sc.SlaveID] = sc
	log.Printf("[control] slave registered: %s @ %s", sc.SlaveID, sc.UDPAddr)
}

func (r *SlaveRegistry) Unregister(slaveID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.slaves, slaveID)
	log.Printf("[control] slave disconnected: %s", slaveID)
}

// Pick selects a slave for a new session (round-robin).
func (r *SlaveRegistry) Pick() *SlaveConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.slaves) == 0 {
		return nil
	}
	list := make([]*SlaveConn, 0, len(r.slaves))
	for _, sc := range r.slaves {
		list = append(list, sc)
	}
	sc := list[r.robin%len(list)]
	r.robin++
	return sc
}

func (r *SlaveRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.slaves)
}

// All returns a snapshot of all connected slaves (for the Welcome v3 candidate
// list the client RTT-probes). Order is map-arbitrary but stable enough per call.
func (r *SlaveRegistry) All() []*SlaveConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*SlaveConn, 0, len(r.slaves))
	for _, sc := range r.slaves {
		out = append(out, sc)
	}
	return out
}

// ByID returns the slave with the given ID, or nil.
func (r *SlaveRegistry) ByID(id string) *SlaveConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.slaves[id]
}

// SlaveHealth is a point-in-time health view of one connected slave.
type SlaveHealth struct {
	SlaveID     string
	LastSeen    time.Time
	DataPlaneUp bool
}

// Health returns current health for all connected slaves.
func (r *SlaveRegistry) Health() []SlaveHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SlaveHealth, 0, len(r.slaves))
	for _, sc := range r.slaves {
		sc.mu.Lock()
		out = append(out, SlaveHealth{
			SlaveID:     sc.SlaveID,
			LastSeen:    sc.lastSeen,
			DataPlaneUp: sc.UDPAddr != "",
		})
		sc.mu.Unlock()
	}
	return out
}

// SlaveInfoMsg is the first message a slave sends after connecting.
type SlaveInfoMsg struct {
	SlaveID   string `json:"slave_id"`
	UDPAddr   string `json:"udp_addr"`
	DataPlane string `json:"data_plane"`
}

// Server listens for incoming slave connections over mTLS.
type Server struct {
	Registry *SlaveRegistry
	tlsCfg   *tls.Config

	// OnSlaveRegister, if set, is called when a slave registers, with its ID
	// and its routable data-plane address (real IP + reported data-plane port).
	// The master uses this to wire the downlink data channel.
	OnSlaveRegister   func(slaveID, dataPlaneAddr string)
	OnSlaveUnregister func(slaveID string)
}

func NewServer(tlsCfg *tls.Config) *Server {
	return &Server{
		Registry: NewSlaveRegistry(),
		tlsCfg:   tlsCfg,
	}
}

func (s *Server) Listen(addr string) error {
	ln, err := tls.Listen("tcp", addr, s.tlsCfg)
	if err != nil {
		return err
	}
	log.Printf("[control] listening for slaves on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[control] accept error: %v", err)
			continue
		}
		go s.handleSlave(conn)
	}
}

// routableAddr replaces a wildcard/unspecified host (e.g. "0.0.0.0:7002") with
// the slave's real IP as observed on its control connection, keeping the port.
// Returns the input unchanged if it is already routable or unparseable.
func routableAddr(reported, remoteHost string) string {
	if reported == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(reported)
	if err != nil {
		return reported
	}
	if ip := net.ParseIP(host); (ip == nil || ip.IsUnspecified()) && remoteHost != "" {
		return net.JoinHostPort(remoteHost, port)
	}
	return reported
}

func (s *Server) handleSlave(conn net.Conn) {
	defer conn.Close()

	// Read slave registration message
	msgType, payload, err := readFrame(conn)
	if err != nil || msgType != MsgTypeSlaveInfo {
		log.Printf("[control] bad slave handshake: %v", err)
		return
	}
	var info SlaveInfoMsg
	if err := json.Unmarshal(payload, &info); err != nil {
		log.Printf("[control] bad slave info: %v", err)
		return
	}

	// The slave reports its UDP listen address, which is typically a wildcard
	// (0.0.0.0:7002). Clients need a routable IP, so derive the real slave IP
	// from the control connection's remote address and keep the reported port.
	remoteHost, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	udpAddr := routableAddr(info.UDPAddr, remoteHost)
	dataAddr := routableAddr(info.DataPlane, remoteHost)

	sc := &SlaveConn{
		conn:     conn,
		SlaveID:  info.SlaveID,
		UDPAddr:  udpAddr,
		lastSeen: time.Now(),
	}
	s.Registry.Register(sc)
	defer s.Registry.Unregister(sc.SlaveID)

	if s.OnSlaveRegister != nil && dataAddr != "" {
		s.OnSlaveRegister(sc.SlaveID, dataAddr)
	}
	if s.OnSlaveUnregister != nil {
		defer s.OnSlaveUnregister(sc.SlaveID)
	}

	// Send keepalives to the slave so its read loop stays alive. Without
	// outbound traffic the slave's read deadline expires and it reconnects
	// every ~45s. Stop when the connection closes.
	stopKA := make(chan struct{})
	defer close(stopKA)
	go func() {
		tick := time.NewTicker(keepaliveInterval)
		defer tick.Stop()
		for {
			select {
			case <-stopKA:
				return
			case <-tick.C:
				if err := sc.send(MsgTypeKeepalive, nil); err != nil {
					return
				}
			}
		}
	}()

	// Keepalive loop — just drain incoming keepalives
	conn.SetReadDeadline(time.Now().Add(keepaliveInterval * 3))
	for {
		msgType, _, err := readFrame(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("[control] slave %s read error: %v", sc.SlaveID, err)
			}
			return
		}
		if msgType == MsgTypeKeepalive {
			sc.mu.Lock()
			sc.lastSeen = time.Now()
			sc.mu.Unlock()
			conn.SetReadDeadline(time.Now().Add(keepaliveInterval * 3))
		}
	}
}

func readFrame(r io.Reader) (uint8, []byte, error) {
	hdr := make([]byte, 3)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	msgType := hdr[0]
	length := binary.BigEndian.Uint16(hdr[1:3])
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	return msgType, payload, nil
}
