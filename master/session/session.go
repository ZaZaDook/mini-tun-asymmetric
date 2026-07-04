// Package session manages per-client VPN sessions on the Master Node.
package session

import (
	"crypto/rand"
	"net"
	"sync"
	"time"
)

const sessionTimeout = 3 * time.Minute

// Session represents a connected VPN client.
type Session struct {
	ID         [16]byte
	ClientAddr *net.UDPAddr // real UDP endpoint of the client
	TunnelIP   net.IP       // assigned inner tunnel IP
	CryptoKey  [32]byte     // per-session ChaCha20 key
	LastSeen   time.Time
	SlaveID    string       // which slave handles downlink for this session

	// DataConn is the per-session UDP socket the master listens on for this
	// client's uplink (port-hopping: a random ephemeral port allocated at
	// handshake and sent to the client in the Welcome). Closed when the session
	// is removed (see Table.OnRemove) so the socket/fd isn't leaked.
	DataConn *net.UDPConn

	mu sync.Mutex
}

func (s *Session) Touch() {
	s.mu.Lock()
	s.LastSeen = time.Now()
	s.mu.Unlock()
}

// Slave returns the slave ID currently handling this session's downlink
// (thread-safe — it can be re-pinned at runtime by a v3 client's RTT choice).
func (s *Session) Slave() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.SlaveID
}

// SetSlave re-pins the downlink slave for this session.
func (s *Session) SetSlave(id string) {
	s.mu.Lock()
	s.SlaveID = id
	s.mu.Unlock()
}

func (s *Session) Expired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.LastSeen) > sessionTimeout
}

// Table is a thread-safe map of active sessions.
type Table struct {
	mu       sync.RWMutex
	byID     map[[16]byte]*Session
	byTunIP  map[string]*Session
	byClient map[string]*Session // key: client UDP addr string

	// OnRemove, if set, is called (outside the lock) whenever a session is
	// removed — used to release its tunnel IP back to the pool. Without it the
	// pool leaks an address per session and eventually exhausts.
	OnRemove func(*Session)
}

func NewTable() *Table {
	t := &Table{
		byID:     make(map[[16]byte]*Session),
		byTunIP:  make(map[string]*Session),
		byClient: make(map[string]*Session),
	}
	go t.reaper()
	return t
}

func (t *Table) Add(s *Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byID[s.ID] = s
	t.byTunIP[s.TunnelIP.String()] = s
	t.byClient[s.ClientAddr.String()] = s
}

func (t *Table) Remove(id [16]byte) {
	t.mu.Lock()
	s, ok := t.byID[id]
	if !ok {
		t.mu.Unlock()
		return
	}
	delete(t.byID, id)
	delete(t.byTunIP, s.TunnelIP.String())
	delete(t.byClient, s.ClientAddr.String())
	hook := t.OnRemove
	t.mu.Unlock()
	if hook != nil {
		hook(s) // release the tunnel IP (outside the lock)
	}
}

func (t *Table) ByClient(addr string) (*Session, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s, ok := t.byClient[addr]
	return s, ok
}

func (t *Table) ByTunIP(ip string) (*Session, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s, ok := t.byTunIP[ip]
	return s, ok
}

func (t *Table) All() []*Session {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*Session, 0, len(t.byID))
	for _, s := range t.byID {
		out = append(out, s)
	}
	return out
}

// CountBySlave returns the number of active sessions per slave ID.
func (t *Table) CountBySlave() map[string]int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m := make(map[string]int)
	for _, s := range t.byID {
		m[s.Slave()]++
	}
	return m
}

// Len returns the total number of active sessions.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.byID)
}

func (t *Table) reaper() {
	tick := time.NewTicker(30 * time.Second)
	for range tick.C {
		for _, s := range t.All() {
			if s.Expired() {
				t.Remove(s.ID)
			}
		}
	}
}

// NewSessionID generates a random 16-byte session identifier.
func NewSessionID() ([16]byte, error) {
	var id [16]byte
	_, err := rand.Read(id[:])
	return id, err
}

// IPPool manages allocation of tunnel IPs from both IPv4 and IPv6 subnets.
type IPPool struct {
	mu        sync.Mutex
	ipv4      *ipSubnet // may be nil
	ipv6      *ipSubnet // may be nil
}

// NewIPPool creates a pool from the given IPv4 subnet. The reservedGateway is
// excluded (usually the tunnel gateway, e.g. 10.8.0.1).
func NewIPPool(subnet *net.IPNet, reservedGateway net.IP) *IPPool {
	return &IPPool{ipv4: newSubnet(subnet, reservedGateway)}
}

// WithIPv6 adds an IPv6 ULA subnet to the pool. Callers typically pass
// fd00::/64 (or any /64) and reserve the first address as the gateway.
func (p *IPPool) WithIPv6(ula *net.IPNet, ulaGateway net.IP) *IPPool {
	p.ipv6 = newSubnet(ula, ulaGateway)
	return p
}

func (p *IPPool) Allocate() (net.IP, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Try IPv4 first (legacy clients), then IPv6.
	if p.ipv4 != nil {
		if ip, ok := p.ipv4.allocate(); ok {
			return ip, true
		}
	}
	if p.ipv6 != nil {
		if ip, ok := p.ipv6.allocate(); ok {
			return ip, true
		}
	}
	return nil, false
}

func (p *IPPool) Release(ip net.IP) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Return the address to the subnet it came from, chosen by family.
	if ip.To4() != nil {
		if p.ipv4 != nil {
			p.ipv4.release(ip)
		}
		return
	}
	if p.ipv6 != nil {
		p.ipv6.release(ip)
	}
}

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

type ipSubnet struct {
	available []net.IP
}

func (s *ipSubnet) allocate() (net.IP, bool) {
	if len(s.available) == 0 {
		return nil, false
	}
	ip := s.available[0]
	s.available = s.available[1:]
	return ip, true
}

func (s *ipSubnet) release(ip net.IP) {
	s.available = append(s.available, cloneIP(ip))
}

// maxPoolSize caps how many addresses a subnet pre-enumerates. A /24 yields
// ~254; this guard exists so an over-large prefix (e.g. an IPv6 /64 with 2^64
// addresses) can never make the pool spin forever and hang startup.
const maxPoolSize = 1 << 16

func newSubnet(subnet *net.IPNet, reservedGateway net.IP) *ipSubnet {
	var ips []net.IP
	// Skip the network address, the reserved gateway, and (for IPv4) the
	// broadcast address; everything else is allocatable.
	network := cloneIP(subnet.IP.Mask(subnet.Mask))
	var bcast net.IP
	if network.To4() != nil {
		bcast = broadcastIP(subnet)
	}
	for ip := cloneIP(network); subnet.Contains(ip); incrementIP(ip) {
		if len(ips) >= maxPoolSize {
			break
		}
		if ip.Equal(network) || ip.Equal(reservedGateway) {
			continue
		}
		if bcast != nil && ip.Equal(bcast) {
			continue
		}
		ips = append(ips, cloneIP(ip))
	}
	return &ipSubnet{available: ips}
}

func broadcastIP(n *net.IPNet) net.IP {
	b := cloneIP(n.IP.To4())
	mask := n.Mask
	for i := range b {
		b[i] = b[i] | ^mask[i]
	}
	return b
}
