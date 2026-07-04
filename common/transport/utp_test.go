package transport

import (
	"bytes"
	"testing"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
)

// TestUTPRoundTrip verifies Unwrap recovers our pktType + payload for every type.
func TestUTPRoundTrip(t *testing.T) {
	u := NewUTP()
	payload := []byte("encrypted-payload")
	for _, pktType := range []uint8{
		protocol.PktTypeData, protocol.PktTypeControl, protocol.PktTypeKeepalive,
		protocol.PktTypeHandshake, protocol.PktTypePunch,
	} {
		frame := u.Wrap(pktType, payload)
		gotType, got, err := u.Unwrap(frame)
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

// TestUTPSeqIncrements confirms the on-wire seq_nr grows between sends (a fixed
// seq would be a DPI anomaly for a real torrent flow).
func TestUTPSeqIncrements(t *testing.T) {
	u := NewUTP()
	seqOf := func(frame []byte) uint16 {
		return uint16(frame[16])<<8 | uint16(frame[17])
	}
	f1 := u.Wrap(protocol.PktTypeData, []byte("a"))
	f2 := u.Wrap(protocol.PktTypeData, []byte("b"))
	f3 := u.Wrap(protocol.PktTypeData, []byte("c"))
	if seqOf(f2) != seqOf(f1)+1 || seqOf(f3) != seqOf(f2)+1 {
		t.Fatalf("seq not incrementing: %d %d %d", seqOf(f1), seqOf(f2), seqOf(f3))
	}
}

// TestUTPHeaderShape checks the version nibble and the cover type mapping.
func TestUTPHeaderShape(t *testing.T) {
	u := NewUTP()
	// handshake -> ST_SYN (type high nibble = 4), version low nibble = 1
	hs := u.Wrap(protocol.PktTypeHandshake, nil)
	if hs[0]&0x0f != utpVersion {
		t.Fatalf("version nibble = %d, want %d", hs[0]&0x0f, utpVersion)
	}
	if hs[0]>>4 != utpStSyn {
		t.Fatalf("handshake µTP type = %d, want ST_SYN(%d)", hs[0]>>4, utpStSyn)
	}
	ka := u.Wrap(protocol.PktTypeKeepalive, nil)
	if ka[0]>>4 != utpStState {
		t.Fatalf("keepalive µTP type = %d, want ST_STATE(%d)", ka[0]>>4, utpStState)
	}
	data := u.Wrap(protocol.PktTypeData, nil)
	if data[0]>>4 != utpStData {
		t.Fatalf("data µTP type = %d, want ST_DATA(%d)", data[0]>>4, utpStData)
	}
}

// TestCarriersDontCollide is the key safety property: a cs2 frame must not parse
// as µTP and vice versa, so a client and server on different carriers stay silent
// to each other (no accidental cross-talk).
func TestCarriersDontCollide(t *testing.T) {
	cs2 := NewCS2()
	utp := NewUTP()
	payload := []byte("payload-bytes-here")

	cs2Frame := cs2.Wrap(protocol.PktTypeData, payload)
	utpFrame := utp.Wrap(protocol.PktTypeData, payload)

	// A µTP receiver should reject a cs2 frame. cs2 byte 0 is 0xFF; its low
	// nibble is 0xF != utpVersion(1), so Unwrap returns ErrNotOurs.
	if _, _, err := utp.Unwrap(cs2Frame); err == nil {
		t.Fatal("µTP accepted a cs2 frame")
	}
	// A cs2 receiver should reject a µTP frame (µTP byte 0 isn't 0xFF).
	if _, _, err := cs2.Unwrap(utpFrame); err == nil {
		t.Fatal("cs2 accepted a µTP frame")
	}
}
