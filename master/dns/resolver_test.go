package dns

import (
	"encoding/binary"
	"testing"
)

// buildQuery constructs a minimal DNS query for name/qtype with ID 0x1234.
func buildQuery(name string, qtype uint16) []byte {
	msg := make([]byte, 12)
	binary.BigEndian.PutUint16(msg[0:2], 0x1234)
	msg[2] = 0x01 // RD
	binary.BigEndian.PutUint16(msg[4:6], 1) // QDCOUNT
	for _, label := range splitName(name) {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0) // root
	var tc [4]byte
	binary.BigEndian.PutUint16(tc[0:2], qtype)
	binary.BigEndian.PutUint16(tc[2:4], 1) // IN
	msg = append(msg, tc[:]...)
	return msg
}

func splitName(name string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(name[i])
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func TestParseQuestion(t *testing.T) {
	q := buildQuery("Example.COM", qtypeA)
	name, qtype, ok := parseQuestion(q)
	if !ok {
		t.Fatal("parseQuestion failed")
	}
	if name != "example.com." {
		t.Errorf("name = %q, want example.com.", name)
	}
	if qtype != qtypeA {
		t.Errorf("qtype = %d, want %d", qtype, qtypeA)
	}
}

func TestEmptyAnswerForAAAA(t *testing.T) {
	q := buildQuery("ipv6.test", qtypeAAAA)
	resp := emptyAnswer(q)
	if len(resp) != len(q) {
		t.Fatalf("resp len %d, want %d", len(resp), len(q))
	}
	if resp[2]&0x80 == 0 {
		t.Error("QR bit not set on response")
	}
	if binary.BigEndian.Uint16(resp[6:8]) != 0 {
		t.Error("ANCOUNT should be 0")
	}
	// Transaction ID preserved.
	if resp[0] != q[0] || resp[1] != q[1] {
		t.Error("transaction ID not preserved")
	}
}

func TestSuppressAAAAQuery(t *testing.T) {
	r, err := New(Config{Mode: ModePlain, Upstream: "8.8.8.8:53", SuppressAAAA: true})
	if err != nil {
		t.Fatal(err)
	}
	q := buildQuery("example.com", qtypeAAAA)
	resp, err := r.Query(q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if binary.BigEndian.Uint16(resp[6:8]) != 0 {
		t.Error("AAAA query should yield empty answer when suppressed")
	}
}

func TestMinTTL(t *testing.T) {
	// Build a response: header + 1 question + 1 A answer with TTL 300.
	q := buildQuery("example.com", qtypeA)
	resp := append([]byte(nil), q...)
	binary.BigEndian.PutUint16(resp[6:8], 1) // ANCOUNT=1
	// Answer: name pointer to question (0xC00C), type A, class IN, TTL 300, rdlen 4, IP.
	ans := []byte{0xC0, 0x0C, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x01, 0x2C, 0x00, 0x04, 1, 2, 3, 4}
	resp = append(resp, ans...)
	if ttl := minTTL(resp); ttl != 300 {
		t.Errorf("minTTL = %d, want 300", ttl)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{Mode: ModeDoH}); err == nil {
		t.Error("doh without URL should error")
	}
	if _, err := New(Config{Mode: ModePlain}); err == nil {
		t.Error("plain without upstream should error")
	}
	if _, err := New(Config{Mode: "bogus"}); err == nil {
		t.Error("unknown mode should error")
	}
}
