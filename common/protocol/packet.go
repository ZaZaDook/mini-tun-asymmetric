// Package protocol defines the Mini-Tun Asymmetric wire format.
//
// Obfuscation: packets mimic CS2 (Source Engine) connectionless UDP datagrams.
// The first 4 bytes are always 0xFF 0xFF 0xFF 0xFF (Source Engine marker),
// followed by a fake "S2C_CHALLENGE" header byte, then our encrypted payload.
// This makes DPI fingerprint the traffic as Source Engine game traffic.
package protocol

import (
	"encoding/binary"
	"errors"
)

const (
	// Source Engine connectionless header — mimics CS2 UDP packets
	ObfsHeader0 = 0xFF
	ObfsHeader1 = 0xFF
	ObfsHeader2 = 0xFF
	ObfsHeader3 = 0xFF
	// Fake Source Engine packet type: S2C_CHALLENGE
	ObfsFakeType = 0x41

	// Our real packet types (encrypted, after obfs layer)
	PktTypeData     uint8 = 0x01 // Tunneled IP packet
	PktTypeControl  uint8 = 0x02 // Control message (client ↔ master)
	PktTypeKeepalive uint8 = 0x03
	PktTypeHandshake uint8 = 0x04
	// PktTypePunch is sent by the client to the slave's downlink port to open a
	// NAT mapping for the asymmetric downlink and to register the client's real
	// downlink address. Payload: 4-byte assigned tunnel IP.
	PktTypePunch uint8 = 0x05

	// PktTypePing / PktTypePong: client↔slave RTT probe. The client sends a Ping
	// to a slave's downlink port; the slave echoes it back as a Pong ONLY if the
	// client's tunnel IP already has a session (registered via SlaveSessionMsg) —
	// so an unauthenticated active probe gets no reply (anti-probe safe). Used to
	// measure the slave→client leg and pick the nearest slave.
	// Payload: [4]=tunnelIP [4:12]=client timestamp (echoed back verbatim).
	PktTypePing uint8 = 0x06
	PktTypePong uint8 = 0x07

	// Control subtypes
	CtrlHello      uint8 = 0x01 // Client → Master: auth + hello
	CtrlWelcome    uint8 = 0x02 // Master → Client: assigned IP + slave endpoint
	CtrlDisconnect uint8 = 0x03
	CtrlSlaveInfo  uint8 = 0x04 // Master → Slave: session info
	CtrlSlaveChoice uint8 = 0x05 // Client → Master: chosen slave after RTT probe (v3)

	MaxPacketSize = 1400 // Stay under typical MTU with room for headers
	HeaderSize    = 9    // 4 obfs + 1 fake type + 1 pkt type + 2 seq + 1 reserved
)

var ErrShortPacket = errors.New("packet too short")
var ErrBadObfsHeader = errors.New("bad obfuscation header")
var ErrBadVersion = errors.New("unsupported message version")

// Packet is an in-memory representation of a Mini-Tun Asymmetric UDP packet (post-decrypt).
type Packet struct {
	Type    uint8
	Seq     uint16
	Payload []byte
}

// WireHeader is the 9-byte unencrypted header prepended to every UDP datagram.
// Layout: [0xFF 0xFF 0xFF 0xFF] [0x41] [PktType] [Seq Hi] [Seq Lo] [0x00]
type WireHeader [HeaderSize]byte

func BuildHeader(pktType uint8, seq uint16) WireHeader {
	var h WireHeader
	h[0], h[1], h[2], h[3] = ObfsHeader0, ObfsHeader1, ObfsHeader2, ObfsHeader3
	h[4] = ObfsFakeType
	h[5] = pktType
	binary.BigEndian.PutUint16(h[6:8], seq)
	h[8] = 0x00
	return h
}

func ParseHeader(b []byte) (WireHeader, error) {
	if len(b) < HeaderSize {
		return WireHeader{}, ErrShortPacket
	}
	if b[0] != ObfsHeader0 || b[1] != ObfsHeader1 || b[2] != ObfsHeader2 || b[3] != ObfsHeader3 {
		return WireHeader{}, ErrBadObfsHeader
	}
	var h WireHeader
	copy(h[:], b[:HeaderSize])
	return h, nil
}

func (h WireHeader) PktType() uint8  { return h[5] }
func (h WireHeader) Seq() uint16     { return binary.BigEndian.Uint16(h[6:8]) }
