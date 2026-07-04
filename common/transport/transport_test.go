package transport

import (
	"bytes"
	"testing"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
)

// TestCS2RoundTrip verifies Unwrap inverts Wrap for the CS2 carrier.
func TestCS2RoundTrip(t *testing.T) {
	tr := NewCS2()
	payload := []byte("encrypted-payload-bytes")
	frame := tr.Wrap(protocol.PktTypeData, payload)

	pktType, got, err := tr.Unwrap(frame)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if pktType != protocol.PktTypeData {
		t.Fatalf("pktType = 0x%02x, want PktTypeData", pktType)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q want %q", got, payload)
	}
}

// TestCS2GoldenBytes is the key regression guard: the CS2 carrier must emit
// byte-for-byte the same frame as the legacy protocol.BuildHeader++payload, so
// the Transport refactor changes nothing on the wire.
func TestCS2GoldenBytes(t *testing.T) {
	tr := NewCS2()
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02}

	for _, pktType := range []uint8{
		protocol.PktTypeData, protocol.PktTypeKeepalive,
		protocol.PktTypeHandshake, protocol.PktTypePunch, protocol.PktTypeControl,
	} {
		hdr := protocol.BuildHeader(pktType, 0)
		legacy := append(append([]byte{}, hdr[:]...), payload...)
		got := tr.Wrap(pktType, payload)
		if !bytes.Equal(got, legacy) {
			t.Fatalf("pktType 0x%02x: Wrap bytes differ from legacy\n got=%x\nwant=%x",
				pktType, got, legacy)
		}
	}
}

// TestCS2RejectsForeign confirms a datagram without the CS2 magic is rejected.
func TestCS2RejectsForeign(t *testing.T) {
	tr := NewCS2()
	if _, _, err := tr.Unwrap([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}); err == nil {
		t.Fatal("expected ErrNotOurs for non-CS2 datagram")
	}
	if _, _, err := tr.Unwrap([]byte{0x01, 0x02}); err == nil {
		t.Fatal("expected error for too-short datagram")
	}
}

// TestByName checks the registry resolves cs2 and the empty default, rejects unknown.
func TestByName(t *testing.T) {
	for _, name := range []string{"", "cs2"} {
		tr, err := ByName(name)
		if err != nil || tr.Name() != "cs2" {
			t.Fatalf("ByName(%q) = %v, %v", name, tr, err)
		}
	}
	for _, name := range []string{"utp", "webrtc", "quic"} {
		tr, err := ByName(name)
		if err != nil || tr.Name() != name {
			t.Fatalf("ByName(%q) = %v, %v", name, tr, err)
		}
	}
	if _, err := ByName("nope"); err == nil {
		t.Fatal("expected error for unknown transport")
	}
}
