package protocol

import (
	"bytes"
	"errors"
	"net"
	"testing"
)

// TestWelcomeV2RoundTrip verifies a v2 welcome (with DataPort) round-trips.
func TestWelcomeV2RoundTrip(t *testing.T) {
	m := WelcomeMsg{
		AssignedIP: net.IPv4(10, 8, 0, 7),
		SlaveIP:    net.IPv4(163, 5, 157, 22),
		SlavePort:  7002,
		DataPort:   54321,
	}
	copy(m.ServerID[:], "master01")

	b := m.Marshal()
	if len(b) != WelcomeV2Size {
		t.Fatalf("size = %d, want %d", len(b), WelcomeV2Size)
	}
	got, err := ParseWelcome(b)
	if err != nil {
		t.Fatalf("ParseWelcome: %v", err)
	}
	if !got.AssignedIP.Equal(m.AssignedIP) || !got.SlaveIP.Equal(m.SlaveIP) ||
		got.SlavePort != m.SlavePort || got.DataPort != m.DataPort || got.ServerID != m.ServerID {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, m)
	}
}

// TestParseWelcomeRejectsOld confirms a v2-only client rejects a short/old welcome.
func TestParseWelcomeRejectsOld(t *testing.T) {
	old := make([]byte, 19) // legacy v1 size
	old[0] = CtrlWelcome
	if _, err := ParseWelcome(old); !errors.Is(err, ErrShortPacket) {
		t.Fatalf("old welcome: got %v, want ErrShortPacket", err)
	}
	wrongVer := make([]byte, WelcomeV2Size)
	wrongVer[0] = CtrlWelcome
	wrongVer[1] = 0x01
	if _, err := ParseWelcome(wrongVer); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("wrong-version welcome: got %v, want ErrBadVersion", err)
	}
}

// TestHelloV2RoundTrip verifies a v2 hello marshals and parses back identically,
// and that the signed prefix is the first helloSignedLen bytes of Marshal.
func TestHelloV2RoundTrip(t *testing.T) {
	m := HelloMsg{Version: HelloVersion, Timestamp: 0x1122334455667788}
	for i := range m.Nonce {
		m.Nonce[i] = byte(i + 1)
	}
	for i := range m.MAC {
		m.MAC[i] = byte(0xA0 + i)
	}

	b := m.Marshal()
	if len(b) != HelloV2Size {
		t.Fatalf("marshaled size = %d, want %d", len(b), HelloV2Size)
	}
	if b[0] != CtrlHello {
		t.Fatalf("subtype = 0x%02x, want CtrlHello", b[0])
	}
	if !bytes.Equal(b[:helloSignedLen], m.MarshalUnsigned()) {
		t.Fatal("Marshal prefix does not equal MarshalUnsigned")
	}

	got, err := ParseHello(b)
	if err != nil {
		t.Fatalf("ParseHello: %v", err)
	}
	if got.Version != m.Version || got.Timestamp != m.Timestamp ||
		got.Nonce != m.Nonce || got.MAC != m.MAC {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, m)
	}
}

// TestParseHelloRejectsOldVersion confirms a v2-only master turns away a v1
// (37-byte token) hello rather than misparsing it: a v1 hello is shorter than a
// v2, so it's rejected as too short. A full-length frame carrying a non-v2
// version byte is rejected with ErrBadVersion.
func TestParseHelloRejectsOldVersion(t *testing.T) {
	v1 := make([]byte, 37)
	v1[0] = CtrlHello
	if _, err := ParseHello(v1); !errors.Is(err, ErrShortPacket) {
		t.Fatalf("v1 hello: got err %v, want ErrShortPacket", err)
	}

	wrongVer := make([]byte, HelloV2Size)
	wrongVer[0] = CtrlHello
	wrongVer[1] = 0x01 // not HelloVersion
	if _, err := ParseHello(wrongVer); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("wrong-version hello: got err %v, want ErrBadVersion", err)
	}
}

// TestParseHelloShort confirms a truncated hello is rejected.
func TestParseHelloShort(t *testing.T) {
	if _, err := ParseHello([]byte{CtrlHello, HelloVersion}); !errors.Is(err, ErrShortPacket) {
		t.Fatalf("short hello: got err %v, want ErrShortPacket", err)
	}
}

// TestWelcomeV3RoundTrip verifies the v3 welcome with a slave list round-trips,
// and that v3 also carries the v2 fields (pick + dataport) for fallback.
func TestWelcomeV3RoundTrip(t *testing.T) {
	m := WelcomeMsg{
		AssignedIP: net.IPv4(10, 8, 0, 9),
		SlaveIP:    net.IPv4(163, 5, 157, 22),
		SlavePort:  7002,
		DataPort:   40000,
		Slaves: []SlaveEndpoint{
			{IP: net.IPv4(163, 5, 157, 22), Port: 7002},
			{IP: net.IPv4(151, 243, 180, 87), Port: 7002},
		},
	}
	copy(m.ServerID[:], "master01")
	b := m.Marshal()
	if b[1] != WelcomeVersion3 {
		t.Fatalf("ver = %d, want v3", b[1])
	}
	got, err := ParseWelcome(b)
	if err != nil {
		t.Fatalf("ParseWelcome v3: %v", err)
	}
	if len(got.Slaves) != 2 {
		t.Fatalf("slaves = %d, want 2", len(got.Slaves))
	}
	if !got.Slaves[1].IP.Equal(net.IPv4(151, 243, 180, 87)) || got.Slaves[1].Port != 7002 {
		t.Fatalf("slave[1] mismatch: %+v", got.Slaves[1])
	}
	if !got.AssignedIP.Equal(m.AssignedIP) || got.DataPort != m.DataPort {
		t.Fatalf("v3 base fields mismatch: %+v", got)
	}
}

// TestWelcomeV2StillParses confirms a v2 welcome (no list) still parses fine —
// backward compatibility for old clients.
func TestWelcomeV2StillParses(t *testing.T) {
	m := WelcomeMsg{AssignedIP: net.IPv4(10, 8, 0, 7), SlaveIP: net.IPv4(1, 2, 3, 4), SlavePort: 7002, DataPort: 1}
	b := m.Marshal()
	if b[1] != WelcomeVersion || len(b) != WelcomeV2Size {
		t.Fatalf("expected v2 22-byte, got ver=%d len=%d", b[1], len(b))
	}
	if _, err := ParseWelcome(b); err != nil {
		t.Fatalf("v2 parse: %v", err)
	}
}

// TestSlaveChoiceRoundTrip verifies the client→master slave-choice message.
func TestSlaveChoiceRoundTrip(t *testing.T) {
	m := SlaveChoiceMsg{
		TunnelIP:  net.IPv4(10, 8, 0, 9),
		SlaveIP:   net.IPv4(151, 243, 180, 87),
		SlavePort: 7002,
	}
	got, err := ParseSlaveChoice(m.Marshal())
	if err != nil {
		t.Fatalf("ParseSlaveChoice: %v", err)
	}
	if !got.TunnelIP.Equal(m.TunnelIP) || !got.SlaveIP.Equal(m.SlaveIP) || got.SlavePort != m.SlavePort {
		t.Fatalf("slave-choice mismatch: %+v", got)
	}
}

// TestParseHelloAcceptsV3 confirms the master accepts a v3-capable hello.
func TestParseHelloAcceptsV3(t *testing.T) {
	m := HelloMsg{Version: HelloVersion3, Timestamp: 1700000000}
	b := m.Marshal()
	got, err := ParseHello(b)
	if err != nil {
		t.Fatalf("ParseHello v3: %v", err)
	}
	if got.Version != HelloVersion3 {
		t.Fatalf("version = %d, want v3", got.Version)
	}
}
