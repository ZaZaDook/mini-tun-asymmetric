package transport

import (
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
	"time"
)

// WebRTC carrier constants.
const (
	// stunMagicCookie is the fixed value at bytes [4:8] of every RFC 5389 STUN
	// message. Its presence (plus the top-2-bits-zero rule) is how a real WebRTC
	// endpoint demultiplexes STUN from RTP on a shared port.
	stunMagicCookie = 0x2112A442
	stunBinding     = 0x0001 // Binding Request message type (top 2 bits already 0)
	stunHeaderSize  = 20
	stunFrameMin    = stunHeaderSize + 1

	// rtpVersion2 is byte 0 of an RFC 3550 RTP packet: version=2, no padding,
	// no extension, CSRC count 0.
	rtpVersion2    = 0x80
	rtpPayloadType = 0x60 // dynamic PT 96 (typical WebRTC video), marker bit clear
	rtpHeaderSize  = 12
	rtpFrameMin    = rtpHeaderSize + 1
)

// webrtcTransport mimics WebRTC, which real video apps (Zoom/Meet/Discord/VK) use.
// WebRTC is two-phase: STUN for connection setup (ICE), then media as SRTP. We
// mirror both — the handshake looks like a STUN Binding request (the kind sent to
// a TURN/STUN server on :3478), and all subsequent data looks like an RTP media
// stream (where gigabytes of video are normal, and our asymmetric downlink from a
// different IP looks like a TURN relay / ICE candidate). Wrapping everything as
// STUN would be the same "thousands of identical setup packets" tell that sank the
// CS2 carrier; the RTP data phase avoids it.
//
// Stateful on send (SSRC + growing RTP sequence/timestamp); stateless parse.
type webrtcTransport struct {
	ssrc uint32
	seq  atomic.Uint32 // RTP seq, wraps to uint16 on wire
	tsT0 uint32        // RTP timestamp base
}

// NewWebRTC returns a fresh WebRTC transport with a random SSRC. Create one per
// connection (each tunnel = its own media stream identity).
func NewWebRTC() Transport {
	var b [8]byte
	_, _ = rand.Read(b[:])
	w := &webrtcTransport{
		ssrc: binary.BigEndian.Uint32(b[0:4]),
		tsT0: binary.BigEndian.Uint32(b[4:8]),
	}
	// Start the sequence at a random offset (real senders don't start at 0).
	w.seq.Store(uint32(binary.BigEndian.Uint16(b[0:2])))
	return w
}

func (w *webrtcTransport) Wrap(pktType uint8, payload []byte) []byte {
	if pktType == 0x04 { // PktTypeHandshake → STUN Binding request
		frame := make([]byte, stunFrameMin+len(payload))
		binary.BigEndian.PutUint16(frame[0:2], stunBinding)
		// message length: our carried bytes (pktType + payload), padded view; the
		// real wire just needs the cookie to read as STUN.
		binary.BigEndian.PutUint16(frame[2:4], uint16(1+len(payload)))
		binary.BigEndian.PutUint32(frame[4:8], stunMagicCookie)
		_, _ = rand.Read(frame[8:20]) // transaction ID
		frame[stunHeaderSize] = pktType
		copy(frame[stunFrameMin:], payload)
		return frame
	}
	// Everything else → RTP media packet.
	frame := make([]byte, rtpFrameMin+len(payload))
	frame[0] = rtpVersion2
	frame[1] = rtpPayloadType
	seq := uint16(w.seq.Add(1))
	binary.BigEndian.PutUint16(frame[2:4], seq)
	ts := w.tsT0 + uint32(time.Now().UnixNano()/1e6) // ms-ish, monotonic-ish
	binary.BigEndian.PutUint32(frame[4:8], ts)
	binary.BigEndian.PutUint32(frame[8:12], w.ssrc)
	frame[rtpHeaderSize] = pktType
	copy(frame[rtpFrameMin:], payload)
	return frame
}

func (w *webrtcTransport) Unwrap(raw []byte) (uint8, []byte, error) {
	// RTP: byte 0 == 0x80 (version 2, no pad/ext/cc).
	if len(raw) >= rtpFrameMin && raw[0] == rtpVersion2 {
		return raw[rtpHeaderSize], raw[rtpFrameMin:], nil
	}
	// STUN: top 2 bits zero and the magic cookie present at [4:8].
	if len(raw) >= stunFrameMin && raw[0]&0xC0 == 0 &&
		binary.BigEndian.Uint32(raw[4:8]) == stunMagicCookie {
		return raw[stunHeaderSize], raw[stunFrameMin:], nil
	}
	return 0, nil, ErrNotOurs
}

func (w *webrtcTransport) Name() string { return "webrtc" }
