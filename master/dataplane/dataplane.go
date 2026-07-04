// Package dataplane manages the encrypted UDP data channel between Master and Slave nodes.
// Master listens on a separate UDP port; each Slave registers by SlaveID and then
// the master forwards downlink IP packets through that channel.
package dataplane

import (
	"encoding/binary"
	"log"
	"net"
	"sync"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/crypto"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/nettune"
)

// MsgTypeData is the master→slave downlink message type.
const MsgTypeData = uint8(0xDA)

// MsgTypeUplink is the slave→master uplink relay message type (QUIC symmetric
// mode): the slave forwards a client's still-encrypted uplink to the master.
// Frame: [0xDB][len:2][ per-slave-AEAD( tunnelIP(4) ++ clientCiphertext ) ].
const MsgTypeUplink = uint8(0xDB)

// SlaveDataConn tracks one Slave's UDP data endpoint.
type SlaveDataConn struct {
	addr    *net.UDPAddr
	aead    *crypto.AEAD
	slaveID string
}

// MasterDataPlane is the server-side data plane UDP socket.
type MasterDataPlane struct {
	conn    *net.UDPConn
	mu      sync.RWMutex
	slaves  map[string]*SlaveDataConn // key: slaveID
	byAddr  map[string]*SlaveDataConn // key: slave UDP addr string (for uplink relay)
	masterKey [crypto.KeySize]byte

	// OnUplink, if set, is called for each relayed client uplink packet (QUIC
	// mode): tunnelIP identifies the session, clientCiphertext is still encrypted
	// with the per-session key (the master decrypts it, the slave never did).
	OnUplink func(tunnelIP net.IP, clientCiphertext []byte)
}

func NewMasterDataPlane(listenAddr string, masterKey [crypto.KeySize]byte) (*MasterDataPlane, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	nettune.TuneUDP(conn)
	dp := &MasterDataPlane{
		conn:      conn,
		slaves:    make(map[string]*SlaveDataConn),
		byAddr:    make(map[string]*SlaveDataConn),
		masterKey: masterKey,
	}
	go dp.readLoop()
	return dp, nil
}

// RegisterSlave records where a Slave's data endpoint is.
func (dp *MasterDataPlane) RegisterSlave(slaveID string, addr *net.UDPAddr) {
	salt := []byte(slaveID)
	key := crypto.DeriveKey(dp.masterKey[:], salt)
	aead, _ := crypto.NewAEAD(key)
	sc := &SlaveDataConn{addr: addr, aead: aead, slaveID: slaveID}
	dp.mu.Lock()
	dp.slaves[slaveID] = sc
	dp.byAddr[addr.String()] = sc
	dp.mu.Unlock()
	log.Printf("[dataplane] slave %s registered at %s", slaveID, addr)
}

// SendToSlave encrypts and sends an IP packet to a specific Slave for downlink delivery.
func (dp *MasterDataPlane) SendToSlave(slaveID string, ipPkt []byte) error {
	dp.mu.RLock()
	sc, ok := dp.slaves[slaveID]
	dp.mu.RUnlock()
	if !ok {
		return nil
	}
	ciphertext, serr := sc.aead.Seal(ipPkt)
	if serr != nil {
		return serr
	}
	frame := make([]byte, 1+2+len(ciphertext))
	frame[0] = MsgTypeData
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(ciphertext)))
	copy(frame[3:], ciphertext)
	_, err := dp.conn.WriteToUDP(frame, sc.addr)
	return err
}

func (dp *MasterDataPlane) readLoop() {
	buf := make([]byte, 65535)
	for {
		n, src, err := dp.conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("[dataplane] read error: %v", err)
			return
		}
		// Slave→master uplink relay (QUIC symmetric mode). Other message types are
		// ignored (the master is otherwise send-only on this channel).
		if n < 3 || buf[0] != MsgTypeUplink {
			continue
		}
		length := binary.BigEndian.Uint16(buf[1:3])
		if int(length) > n-3 {
			continue
		}
		dp.mu.RLock()
		sc := dp.byAddr[src.String()]
		dp.mu.RUnlock()
		if sc == nil {
			continue // unknown slave
		}
		plain, derr := sc.aead.Open(buf[3 : 3+length])
		if derr != nil || len(plain) < 4 {
			continue
		}
		tunnelIP := net.IP(append([]byte{}, plain[0:4]...))
		clientCiphertext := append([]byte{}, plain[4:]...)
		if dp.OnUplink != nil {
			go dp.OnUplink(tunnelIP, clientCiphertext)
		}
	}
}

// SendUplink (slave side helper) frames a client's still-encrypted uplink for the
// master: [0xDB][len:2][ per-slave-AEAD( tunnelIP(4) ++ clientCiphertext ) ].
// The slave holds the same per-slave AEAD (DeriveKey(masterKey, slaveID)).
func BuildUplinkFrame(perSlave *crypto.AEAD, tunnelIP net.IP, clientCiphertext []byte) ([]byte, error) {
	inner := make([]byte, 4+len(clientCiphertext))
	copy(inner[0:4], tunnelIP.To4())
	copy(inner[4:], clientCiphertext)
	ct, err := perSlave.Seal(inner)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 3+len(ct))
	frame[0] = MsgTypeUplink
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(ct)))
	copy(frame[3:], ct)
	return frame, nil
}

// SlaveDataPlane is the Slave-side data plane that receives downlink packets from Master.
type SlaveDataPlane struct {
	conn      *net.UDPConn
	aead      *crypto.AEAD
	OnPacket  func(ipPkt []byte) // called with each decrypted IP packet
}

func NewSlaveDataPlane(listenAddr, masterDataAddr string, key [crypto.KeySize]byte) (*SlaveDataPlane, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	nettune.TuneUDP(conn)
	aead, err := crypto.NewAEAD(key)
	if err != nil {
		conn.Close()
		return nil, err
	}
	dp := &SlaveDataPlane{conn: conn, aead: aead}
	go dp.readLoop()
	return dp, nil
}

func (dp *SlaveDataPlane) readLoop() {
	buf := make([]byte, 65535)
	for {
		n, _, err := dp.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 3 || buf[0] != MsgTypeData {
			continue
		}
		length := binary.BigEndian.Uint16(buf[1:3])
		if int(length) > n-3 {
			continue
		}
		plaintext, err := dp.aead.Open(buf[3 : 3+length])
		if err != nil {
			log.Printf("[dataplane] decryption failed: %v", err)
			continue
		}
		if dp.OnPacket != nil {
			pktCopy := make([]byte, len(plaintext))
			copy(pktCopy, plaintext)
			go dp.OnPacket(pktCopy)
		}
	}
}
