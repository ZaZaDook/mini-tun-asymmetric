package crypto

import (
	"encoding/binary"
	"sync"
	"testing"
)

// TestHelloMAC verifies the handshake MAC authenticates with the right token and
// rejects a wrong token or a tampered message (anti-probe / anti-replay basis).
func TestHelloMAC(t *testing.T) {
	token := []byte("the-real-auth-token")
	msg := []byte{0x01, 0x02, 0xde, 0xad, 0xbe, 0xef}

	tag := HelloMAC(token, msg)
	if !VerifyHelloMAC(token, msg, tag[:]) {
		t.Fatal("valid MAC rejected")
	}
	if VerifyHelloMAC([]byte("wrong-token"), msg, tag[:]) {
		t.Fatal("MAC accepted under wrong token")
	}
	tampered := append([]byte{}, msg...)
	tampered[0] ^= 0xFF
	if VerifyHelloMAC(token, tampered, tag[:]) {
		t.Fatal("MAC accepted for tampered message")
	}
	badTag := tag
	badTag[0] ^= 0xFF
	if VerifyHelloMAC(token, msg, badTag[:]) {
		t.Fatal("tampered tag accepted")
	}
}

// TestNonceUniqueConcurrent verifies that concurrent Seal calls never reuse a
// nonce (the previous non-atomic counter had a data race that could).
func TestNonceUniqueConcurrent(t *testing.T) {
	key := DeriveKey([]byte("token"), []byte("salt"))
	a, err := NewAEAD(key)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	const perG = 1000
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[uint32]bool, goroutines*perG)
	dup := 0

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				ct, err := a.Seal([]byte("hello"))
				if err != nil {
					t.Errorf("Seal: %v", err)
					return
				}
				ctr := binary.BigEndian.Uint32(ct[8:12]) // counter portion of nonce
				mu.Lock()
				if seen[ctr] {
					dup++
				}
				seen[ctr] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if dup != 0 {
		t.Fatalf("found %d duplicate nonces (nonce reuse)", dup)
	}
	if len(seen) != goroutines*perG {
		t.Fatalf("expected %d unique nonces, got %d", goroutines*perG, len(seen))
	}
}

// TestSealOpenRoundTrip verifies a sealed message decrypts correctly with a
// matching key.
func TestSealOpenRoundTrip(t *testing.T) {
	key := DeriveKey([]byte("tok"), []byte("s"))
	sealer, _ := NewAEAD(key)
	// Opener must share the same key and the sender's session prefix; here we
	// reuse the same AEAD instance to decrypt, mirroring how Open reads the
	// prepended nonce.
	opener, _ := NewAEADWithPrefix(key, sealer.sessionPrefix)

	msg := []byte("the quick brown fox")
	ct, err := sealer.Seal(msg)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := opener.Open(ct)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(pt) != string(msg) {
		t.Fatalf("round trip mismatch: %q != %q", pt, msg)
	}
}
