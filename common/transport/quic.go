package transport

import (
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
)

// QUIC carrier constants (RFC 9000 / RFC 8999 invariants).
const (
	// First-byte bits common to all QUIC versions (RFC 8999):
	//   0x80 header form (1=long, 0=short), 0x40 fixed bit (always 1).
	quicHeaderForm = 0x80
	quicFixedBit   = 0x40
	// Long-header Initial in QUIC v1 begins 0xC0 (form+fixed+type 00); the next
	// 4 bytes are the version. Short-header (1-RTT data) begins 0x40 (fixed bit,
	// form clear) followed immediately by the destination connection ID.
	quicLongInitial = 0xC0
	quicVersion1    = 0x00000001
	quicCIDLen      = 8 // we use fixed 8-byte connection IDs (typical)

	// Long Initial frame layout we emit:
	//   [0]=0xC0 [1:5]=version [5]=dcidLen=8 [6:14]=DCID [14]=scidLen=8 [15:23]=SCID
	//   [23]=pktType [24:]=payload
	quicLongHdr   = 23
	quicLongMin   = quicLongHdr + 1
	// Short 1-RTT frame layout we emit:
	//   [0]=0x40 [1:9]=DCID(8) [9:13]=packet number (obfuscated, growing)
	//   [13]=pktType [14:]=payload
	quicShortHdr = 13
	quicShortMin = quicShortHdr + 1
)

// quicTransport mimics QUIC (HTTP/3 on UDP:443) — the hardest carrier to block
// because blocking it breaks the legitimate web. QUIC is two-phase like a real
// connection: a long-header Initial packet for the handshake, then short-header
// 1-RTT packets for data. We mirror both.
//
// IMPORTANT: QUIC is point-to-point (one server IP both directions). To stay
// plausible, the QUIC mode uses a SYMMETRIC path (client<->slave both ways),
// unlike the asymmetric carriers — see the slave-front relay. This transport only
// frames bytes; the symmetric routing lives in the client/slave/master wiring.
//
// Stateful on send (connection IDs + growing packet number); stateless parse.
type quicTransport struct {
	dcid [quicCIDLen]byte
	scid [quicCIDLen]byte
	pn   atomic.Uint32 // packet number, grows
}

// NewQUIC returns a fresh QUIC transport with random connection IDs.
func NewQUIC() Transport {
	q := &quicTransport{}
	_, _ = rand.Read(q.dcid[:])
	_, _ = rand.Read(q.scid[:])
	return q
}

func (q *quicTransport) Wrap(pktType uint8, payload []byte) []byte {
	if pktType == 0x04 { // PktTypeHandshake → QUIC long-header Initial
		frame := make([]byte, quicLongMin+len(payload))
		frame[0] = quicLongInitial
		binary.BigEndian.PutUint32(frame[1:5], quicVersion1)
		frame[5] = quicCIDLen
		copy(frame[6:14], q.dcid[:])
		frame[14] = quicCIDLen
		copy(frame[15:23], q.scid[:])
		frame[quicLongHdr] = pktType
		copy(frame[quicLongMin:], payload)
		return frame
	}
	// Everything else → QUIC short-header 1-RTT packet.
	frame := make([]byte, quicShortMin+len(payload))
	frame[0] = quicFixedBit // 0x40: form clear, fixed bit set
	copy(frame[1:9], q.dcid[:])
	pn := q.pn.Add(1)
	binary.BigEndian.PutUint32(frame[9:13], pn)
	frame[quicShortHdr] = pktType
	copy(frame[quicShortMin:], payload)
	return frame
}

func (q *quicTransport) Unwrap(raw []byte) (uint8, []byte, error) {
	if len(raw) == 0 {
		return 0, nil, ErrNotOurs
	}
	b0 := raw[0]
	// Long-header Initial: form+fixed set, version must be v1.
	if b0 == quicLongInitial && len(raw) >= quicLongMin &&
		binary.BigEndian.Uint32(raw[1:5]) == quicVersion1 {
		return raw[quicLongHdr], raw[quicLongMin:], nil
	}
	// Short-header 1-RTT: we emit exactly 0x40 (form clear, fixed set, spin/key/pn
	// bits zero). Matching the exact byte (not the whole 0x40-0x7F range) avoids
	// colliding with µTP's ST_SYN frame (first byte 0x41).
	if b0 == quicFixedBit && len(raw) >= quicShortMin {
		return raw[quicShortHdr], raw[quicShortMin:], nil
	}
	return 0, nil, ErrNotOurs
}

func (q *quicTransport) Name() string { return "quic" }
