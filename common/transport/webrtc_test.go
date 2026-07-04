package transport

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
)

// TestWebRTCRoundTrip verifies every pktType round-trips, with handshake framed
// as STUN and everything else as RTP.
func TestWebRTCRoundTrip(t *testing.T) {
	w := NewWebRTC()
	payload := []byte("encrypted-payload")
	for _, pktType := range []uint8{
		protocol.PktTypeData, protocol.PktTypeControl, protocol.PktTypeKeepalive,
		protocol.PktTypeHandshake, protocol.PktTypePunch,
	} {
		frame := w.Wrap(pktType, payload)
		gotType, got, err := w.Unwrap(frame)
		if err != nil {
			t.Fatalf("pktType 0x%02x: Unwrap: %v", pktType, err)
		}
		if gotType != pktType {
			t.Fatalf("pktType = 0x%02x, want 0x%02x", gotType, pktType)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload mismatch for 0x%02x", pktType)
		}
	}
}

// TestWebRTCHandshakeIsSTUN checks the handshake frame carries the STUN magic
// cookie and the top-2-bits-zero rule; data frames are RTP version 2.
func TestWebRTCHandshakeIsSTUN(t *testing.T) {
	w := NewWebRTC()
	hs := w.Wrap(protocol.PktTypeHandshake, []byte("x"))
	if hs[0]&0xC0 != 0 {
		t.Fatalf("STUN top 2 bits not zero: 0x%02x", hs[0])
	}
	if binary.BigEndian.Uint32(hs[4:8]) != stunMagicCookie {
		t.Fatalf("missing STUN magic cookie")
	}
	data := w.Wrap(protocol.PktTypeData, []byte("x"))
	if data[0] != rtpVersion2 {
		t.Fatalf("RTP byte0 = 0x%02x, want 0x80", data[0])
	}
}

// TestWebRTCRTPSeqIncrements confirms the RTP sequence grows between media packets.
func TestWebRTCRTPSeqIncrements(t *testing.T) {
	w := NewWebRTC()
	seqOf := func(f []byte) uint16 { return binary.BigEndian.Uint16(f[2:4]) }
	f1 := w.Wrap(protocol.PktTypeData, []byte("a"))
	f2 := w.Wrap(protocol.PktTypeData, []byte("b"))
	if seqOf(f2) != seqOf(f1)+1 {
		t.Fatalf("RTP seq not incrementing: %d then %d", seqOf(f1), seqOf(f2))
	}
}

// TestWebRTCDoesntCollide is the demux-safety property: a webrtc frame must not
// parse under cs2/utp and vice versa, so carriers never cross-talk.
func TestWebRTCDoesntCollide(t *testing.T) {
	cs2 := NewCS2()
	utp := NewUTP()
	wrtc := NewWebRTC()
	payload := []byte("payload-bytes-here")

	// RTP data frame and STUN handshake frame.
	rtpFrame := wrtc.Wrap(protocol.PktTypeData, payload)
	stunFrame := wrtc.Wrap(protocol.PktTypeHandshake, payload)

	for _, other := range []Transport{cs2, utp} {
		if _, _, err := other.Unwrap(rtpFrame); err == nil {
			t.Fatalf("%s accepted a webrtc RTP frame", other.Name())
		}
		if _, _, err := other.Unwrap(stunFrame); err == nil {
			t.Fatalf("%s accepted a webrtc STUN frame", other.Name())
		}
	}
	// webrtc must reject cs2 (0xFF...) and utp (low nibble version 1) frames.
	if _, _, err := wrtc.Unwrap(cs2.Wrap(protocol.PktTypeData, payload)); err == nil {
		t.Fatal("webrtc accepted a cs2 frame")
	}
	if _, _, err := wrtc.Unwrap(utp.Wrap(protocol.PktTypeData, payload)); err == nil {
		t.Fatal("webrtc accepted a utp frame")
	}
}
