package protocol

import (
	"encoding/binary"
	"net"
)

// HelloVersion is the current HelloMsg wire version.
const HelloVersion uint8 = 0x02

// HelloVersion3 is sent by a client that understands Welcome v3 (slave list /
// nearest-node selection). Same 58-byte layout as v2 — only the version byte
// differs, which the MAC covers, so the signature stays valid. The master uses
// it purely as a capability flag: v2 → Welcome v2, v3 → Welcome v3.
const HelloVersion3 uint8 = 0x03

// HelloV2Size is the full marshaled size of a v2 hello.
//   [0]=CtrlHello [1]=version [2:10]=timestamp [10:26]=nonce [26:58]=HMAC
const HelloV2Size = 58

// helloSignedLen is the length of the prefix that the HMAC is computed over
// (everything except the MAC itself): subtype + version + timestamp + nonce.
const helloSignedLen = 26

// HelloMsg is sent by the client to the master on first connect.
//
// v2 proves possession of the auth token via HMAC instead of carrying the token
// on the wire. The token therefore never appears in a (capturable) handshake,
// and the timestamp + nonce let the master reject stale/replayed handshakes —
// defeating an active probe that captures and replays a hello.
//
// Layout (58 bytes):
//
//	[0]      CtrlHello
//	[1]      version (HelloVersion)
//	[2:10]   timestamp (unix seconds, big-endian)
//	[10:26]  nonce (16 random bytes)
//	[26:58]  HMAC-SHA256(token, bytes[0:26])
type HelloMsg struct {
	Version   uint8
	Timestamp uint64
	Nonce     [16]byte
	MAC       [32]byte
}

// MarshalUnsigned returns the first 26 bytes (subtype..nonce) that the MAC is
// computed over. The caller signs this with crypto.HelloMAC and stores the
// result in MAC before calling Marshal.
func (m HelloMsg) MarshalUnsigned() []byte {
	b := make([]byte, helloSignedLen)
	b[0] = CtrlHello
	b[1] = m.Version
	binary.BigEndian.PutUint64(b[2:10], m.Timestamp)
	copy(b[10:26], m.Nonce[:])
	return b
}

// Marshal serializes the full 58-byte hello (signed prefix + MAC).
func (m HelloMsg) Marshal() []byte {
	b := make([]byte, HelloV2Size)
	copy(b[:helloSignedLen], m.MarshalUnsigned())
	copy(b[helloSignedLen:], m.MAC[:])
	return b
}

func ParseHello(b []byte) (HelloMsg, error) {
	if len(b) < HelloV2Size {
		return HelloMsg{}, ErrShortPacket
	}
	if b[1] != HelloVersion && b[1] != HelloVersion3 {
		return HelloMsg{}, ErrBadVersion
	}
	var m HelloMsg
	m.Version = b[1]
	m.Timestamp = binary.BigEndian.Uint64(b[2:10])
	copy(m.Nonce[:], b[10:26])
	copy(m.MAC[:], b[26:58])
	return m, nil
}

// WelcomeMsg is sent by the master to the client after successful auth.
// Carries the assigned tunnel IP, the slave node endpoint, and the per-session
// master DATA port the client must switch its uplink to (port-hopping).
//
// v3 additionally carries a LIST of all live slave endpoints (Slaves) so the
// client can RTT-probe each and pick the nearest for its downlink. v2 carries a
// single slave (SlaveIP/SlavePort). The master sends v3 only to clients that
// announced support via Hello version 0x03; older clients still get v2.
type WelcomeMsg struct {
	AssignedIP net.IP // 4 bytes (IPv4 inside tunnel)
	SlaveIP    net.IP // 4 bytes — the master's round-robin pick (v2 + v3 fallback)
	SlavePort  uint16
	DataPort   uint16 // master uplink port for this session (0 = keep using control port)
	ServerID   [8]byte
	Slaves     []SlaveEndpoint // v3 only: all candidate slaves for client-side probing
}

// SlaveEndpoint is one slave's downlink UDP endpoint, sent in the Welcome v3 list.
type SlaveEndpoint struct {
	IP   net.IP
	Port uint16
}

// WelcomeVersion is the current Welcome wire version for the single-slave format.
const WelcomeVersion uint8 = 0x02

// WelcomeVersion3 carries the slave list (client-side nearest-node selection).
const WelcomeVersion3 uint8 = 0x03

// WelcomeV2Size is the marshaled size of a v2 welcome.
const WelcomeV2Size = 22

// welcomeV3HeadSize is the fixed header of a v3 welcome, before the slave list:
//   [0]=CtrlWelcome [1]=ver(0x03) [2:6]=AssignedIP [6:10]=SlaveIP(pick)
//   [10:12]=SlavePort [12:14]=DataPort [14:22]=ServerID [22]=slaveCount
// then slaveCount × [4-byte IP + 2-byte port].
const welcomeV3HeadSize = 23

// Marshal serializes the welcome. When Slaves is non-empty it emits v3 (slave
// list appended); otherwise the classic 22-byte v2 format.
//
//	v2: [0]=CtrlWelcome [1]=ver(0x02) [2:6]=AssignedIP [6:10]=SlaveIP
//	    [10:12]=SlavePort [12:14]=DataPort [14:22]=ServerID
//	v3: v2 head with ver=0x03 + [22]=count + count×([4]ip [2]port)
//
// NOTE (history): an earlier IPv6 refactor changed Marshal WITHOUT updating
// ParseWelcome, silently breaking every client. Always change Marshal AND
// ParseWelcome AND the client together, and version the format explicitly.
func (m WelcomeMsg) Marshal() []byte {
	if len(m.Slaves) > 0 {
		return m.marshalV3()
	}
	b := make([]byte, WelcomeV2Size)
	b[0] = CtrlWelcome
	b[1] = WelcomeVersion
	copy(b[2:6], m.AssignedIP.To4())
	copy(b[6:10], m.SlaveIP.To4())
	binary.BigEndian.PutUint16(b[10:12], m.SlavePort)
	binary.BigEndian.PutUint16(b[12:14], m.DataPort)
	copy(b[14:22], m.ServerID[:])
	return b
}

func (m WelcomeMsg) marshalV3() []byte {
	n := len(m.Slaves)
	if n > 255 {
		n = 255
	}
	b := make([]byte, welcomeV3HeadSize+n*6)
	b[0] = CtrlWelcome
	b[1] = WelcomeVersion3
	copy(b[2:6], m.AssignedIP.To4())
	copy(b[6:10], m.SlaveIP.To4())
	binary.BigEndian.PutUint16(b[10:12], m.SlavePort)
	binary.BigEndian.PutUint16(b[12:14], m.DataPort)
	copy(b[14:22], m.ServerID[:])
	b[22] = byte(n)
	off := welcomeV3HeadSize
	for i := 0; i < n; i++ {
		copy(b[off:off+4], m.Slaves[i].IP.To4())
		binary.BigEndian.PutUint16(b[off+4:off+6], m.Slaves[i].Port)
		off += 6
	}
	return b
}

func ParseWelcome(b []byte) (WelcomeMsg, error) {
	if len(b) < WelcomeV2Size {
		return WelcomeMsg{}, ErrShortPacket
	}
	if b[1] != WelcomeVersion && b[1] != WelcomeVersion3 {
		return WelcomeMsg{}, ErrBadVersion
	}
	var m WelcomeMsg
	m.AssignedIP = net.IP(append([]byte{}, b[2:6]...))
	m.SlaveIP = net.IP(append([]byte{}, b[6:10]...))
	m.SlavePort = binary.BigEndian.Uint16(b[10:12])
	m.DataPort = binary.BigEndian.Uint16(b[12:14])
	copy(m.ServerID[:], b[14:22])
	if b[1] == WelcomeVersion3 {
		if len(b) < welcomeV3HeadSize {
			return WelcomeMsg{}, ErrShortPacket
		}
		count := int(b[22])
		off := welcomeV3HeadSize
		for i := 0; i < count; i++ {
			if off+6 > len(b) {
				break // truncated; take what parsed
			}
			m.Slaves = append(m.Slaves, SlaveEndpoint{
				IP:   net.IP(append([]byte{}, b[off:off+4]...)),
				Port: binary.BigEndian.Uint16(b[off+4 : off+6]),
			})
			off += 6
		}
	}
	return m, nil
}

// SlaveChoiceMsg is sent by a v3 client to the master after RTT-probing the
// slave list, telling the master which slave to pin the downlink to. Sent as a
// control packet on the client's uplink socket.
//
//	[0]=CtrlSlaveChoice [1]=ver(0x01) [2:6]=TunnelIP [6:10]=ChosenSlaveIP [10:12]=ChosenSlavePort
type SlaveChoiceMsg struct {
	TunnelIP   net.IP
	SlaveIP    net.IP
	SlavePort  uint16
}

const slaveChoiceSize = 12

func (m SlaveChoiceMsg) Marshal() []byte {
	b := make([]byte, slaveChoiceSize)
	b[0] = CtrlSlaveChoice
	b[1] = 0x01
	copy(b[2:6], m.TunnelIP.To4())
	copy(b[6:10], m.SlaveIP.To4())
	binary.BigEndian.PutUint16(b[10:12], m.SlavePort)
	return b
}

func ParseSlaveChoice(b []byte) (SlaveChoiceMsg, error) {
	if len(b) < slaveChoiceSize {
		return SlaveChoiceMsg{}, ErrShortPacket
	}
	if b[0] != CtrlSlaveChoice {
		return SlaveChoiceMsg{}, ErrBadVersion
	}
	var m SlaveChoiceMsg
	m.TunnelIP = net.IP(append([]byte{}, b[2:6]...))
	m.SlaveIP = net.IP(append([]byte{}, b[6:10]...))
	m.SlavePort = binary.BigEndian.Uint16(b[10:12])
	return m, nil
}

// SlaveSessionMsg is sent by master to slave over the control TCP tunnel
// to inform slave about a new client session.
type SlaveSessionMsg struct {
	SessionID  [16]byte
	ClientIP   net.IP // client's real UDP endpoint IP
	ClientPort uint16
	TunnelIP   net.IP // assigned tunnel IP for this client
}

func (m SlaveSessionMsg) Marshal() []byte {
	b := make([]byte, 16+4+2+4)
	copy(b[0:16], m.SessionID[:])
	copy(b[16:20], m.ClientIP.To4())
	binary.BigEndian.PutUint16(b[20:22], m.ClientPort)
	copy(b[22:26], m.TunnelIP.To4())
	return b
}

func ParseSlaveSession(b []byte) (SlaveSessionMsg, error) {
	if len(b) < 26 {
		return SlaveSessionMsg{}, ErrShortPacket
	}
	var m SlaveSessionMsg
	copy(m.SessionID[:], b[0:16])
	m.ClientIP = net.IP(append([]byte{}, b[16:20]...))
	m.ClientPort = binary.BigEndian.Uint16(b[20:22])
	m.TunnelIP = net.IP(append([]byte{}, b[22:26]...))
	return m, nil
}
