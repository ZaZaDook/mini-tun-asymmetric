package transport

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
)

// TestQUICRoundTrip verifies every pktType round-trips, handshake as a long-header
// Initial and everything else as a short-header 1-RTT packet.
func TestQUICRoundTrip(t *testing.T) {
	q := NewQUIC()
	payload := []byte("encrypted-payload")
	for _, pktType := range []uint8{
		protocol.PktTypeData, protocol.PktTypeControl, protocol.PktTypeKeepalive,
		protocol.PktTypeHandshake, protocol.PktTypePunch,
	} {
		frame := q.Wrap(pktType, payload)
		gotType, got, err := q.Unwrap(frame)
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

// TestQUICHeaderShape checks handshake is a v1 long Initial (0xC0 + version) and
// data is a short header (0x40).
func TestQUICHeaderShape(t *testing.T) {
	q := NewQUIC()
	hs := q.Wrap(protocol.PktTypeHandshake, []byte("x"))
	if hs[0] != quicLongInitial {
		t.Fatalf("handshake byte0 = 0x%02x, want 0xC0", hs[0])
	}
	if binary.BigEndian.Uint32(hs[1:5]) != quicVersion1 {
		t.Fatalf("handshake version != QUIC v1")
	}
	data := q.Wrap(protocol.PktTypeData, []byte("x"))
	if data[0] != quicFixedBit {
		t.Fatalf("data byte0 = 0x%02x, want 0x40", data[0])
	}
}

// TestQUICPacketNumberGrows confirms the short-header packet number increases.
func TestQUICPacketNumberGrows(t *testing.T) {
	q := NewQUIC()
	pnOf := func(f []byte) uint32 { return binary.BigEndian.Uint32(f[9:13]) }
	f1 := q.Wrap(protocol.PktTypeData, []byte("a"))
	f2 := q.Wrap(protocol.PktTypeData, []byte("b"))
	if pnOf(f2) != pnOf(f1)+1 {
		t.Fatalf("packet number not growing: %d then %d", pnOf(f1), pnOf(f2))
	}
}

// TestQUICDoesntCollide is the demux-safety property across all four carriers.
func TestQUICDoesntCollide(t *testing.T) {
	cs2 := NewCS2()
	utp := NewUTP()
	wrtc := NewWebRTC()
	quic := NewQUIC()
	payload := []byte("payload-bytes-here-long-enough")

	quicData := quic.Wrap(protocol.PktTypeData, payload)
	quicHS := quic.Wrap(protocol.PktTypeHandshake, payload)

	// Other carriers must reject QUIC frames.
	for _, other := range []Transport{cs2, utp, wrtc} {
		if _, _, err := other.Unwrap(quicData); err == nil {
			t.Fatalf("%s accepted a QUIC short frame", other.Name())
		}
		if _, _, err := other.Unwrap(quicHS); err == nil {
			t.Fatalf("%s accepted a QUIC Initial frame", other.Name())
		}
	}
	// QUIC must reject the others (incl. µTP ST_SYN first byte 0x41, the near-miss).
	if _, _, err := quic.Unwrap(cs2.Wrap(protocol.PktTypeData, payload)); err == nil {
		t.Fatal("QUIC accepted a cs2 frame")
	}
	if _, _, err := quic.Unwrap(utp.Wrap(protocol.PktTypeHandshake, payload)); err == nil {
		t.Fatal("QUIC accepted a µTP ST_SYN frame")
	}
	if _, _, err := quic.Unwrap(wrtc.Wrap(protocol.PktTypeData, payload)); err == nil {
		t.Fatal("QUIC accepted a webrtc RTP frame")
	}
}
