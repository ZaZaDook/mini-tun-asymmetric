package transport

import (
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
	"time"
)

// µTP (micro Transport Protocol, BEP-29) packet types. The high nibble of byte 0
// carries the type, the low nibble the version (1).
const (
	utpStData  = 0 // regular data packet (carries payload)
	utpStFin   = 1 // connection close
	utpStState = 2 // ACK-only state packet
	utpStReset = 3 // forced termination
	utpStSyn   = 4 // connection initiation
	utpVersion = 1

	utpHeaderSize = 20 // BEP-29 fixed header
	// utpFrameMin is the smallest valid frame: header + 1 byte carrying our pktType.
	utpFrameMin = utpHeaderSize + 1
)

// utpTransport mimics BitTorrent's µTP. Unlike cs2 (a static prefix on every
// datagram — a DPI tell), µTP traffic carries a per-connection id and a growing
// sequence number, so a long high-volume flow looks like a torrent download
// (where gigabytes and a swarm of peer IPs are normal — and our asymmetric
// downlink from a different IP looks like just another peer).
//
// It is STATEFUL on send: each instance holds a random connection_id and an
// incrementing seq_nr (a fixed seq would itself be an anomaly). Parsing is
// stateless — the receiver doesn't care about the sender's conn_id, it only
// recovers our pktType (stored as the first payload byte) and the payload.
//
// Our pktType doesn't map onto µTP types semantically, so we keep the real type
// as payload[0] (truth) and set the µTP type field to a plausible value (cover):
// handshake→ST_SYN, keepalive→ST_STATE, everything else→ST_DATA.
type utpTransport struct {
	connID uint16
	seq    atomic.Uint32 // wraps to uint16 on the wire
}

// NewUTP returns a fresh µTP transport with a random connection id. Create a new
// instance per connection (each tunnel session = its own conn_id + seq stream).
func NewUTP() Transport {
	var b [2]byte
	// rand.Read only fails on a broken OS RNG; fall back to a fixed id rather
	// than panicking in that pathological case.
	_, _ = rand.Read(b[:])
	return &utpTransport{connID: binary.BigEndian.Uint16(b[:])}
}

// utpType picks a plausible µTP packet type for cover, given our pktType.
func utpType(pktType uint8) uint8 {
	switch pktType {
	case 0x04: // PktTypeHandshake — looks like a new connection
		return utpStSyn
	case 0x03: // PktTypeKeepalive — looks like an ACK-only state packet
		return utpStState
	default: // data/control/punch — regular data
		return utpStData
	}
}

func (u *utpTransport) Wrap(pktType uint8, payload []byte) []byte {
	frame := make([]byte, utpFrameMin+len(payload))
	frame[0] = utpType(pktType)<<4 | utpVersion
	frame[1] = 0 // extension: none
	binary.BigEndian.PutUint16(frame[2:4], u.connID)
	ts := uint32(time.Now().UnixNano() / 1000)
	binary.BigEndian.PutUint32(frame[4:8], ts)
	binary.BigEndian.PutUint32(frame[8:12], 0) // timestamp_difference: no samples
	binary.BigEndian.PutUint32(frame[12:16], 1<<20) // wnd_size: 1 MiB receive window
	seq := uint16(u.seq.Add(1))
	binary.BigEndian.PutUint16(frame[16:18], seq)
	binary.BigEndian.PutUint16(frame[18:20], 0) // ack_nr
	frame[utpHeaderSize] = pktType               // our real type (truth)
	copy(frame[utpFrameMin:], payload)
	return frame
}

func (u *utpTransport) Unwrap(raw []byte) (uint8, []byte, error) {
	if len(raw) < utpFrameMin {
		return 0, nil, ErrNotOurs
	}
	if raw[0]&0x0f != utpVersion {
		return 0, nil, ErrNotOurs
	}
	return raw[utpHeaderSize], raw[utpFrameMin:], nil
}

func (u *utpTransport) Name() string { return "utp" }
