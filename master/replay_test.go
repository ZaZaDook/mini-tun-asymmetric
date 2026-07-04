package main

import "testing"

// TestReplayCache verifies a nonce is accepted once and rejected on replay, and
// that expired entries are swept so the cache can't grow unbounded.
func TestReplayCache(t *testing.T) {
	r := newReplayCache()
	now := int64(1_000_000)

	var n1 [16]byte
	n1[0] = 0x01
	if !r.checkAndAdd(n1, now) {
		t.Fatal("first sight of nonce should be accepted")
	}
	if r.checkAndAdd(n1, now) {
		t.Fatal("replayed nonce should be rejected")
	}

	// A different nonce is fine.
	var n2 [16]byte
	n2[0] = 0x02
	if !r.checkAndAdd(n2, now) {
		t.Fatal("distinct nonce should be accepted")
	}

	// Far in the future, both old entries are swept; the same nonce is fresh again.
	future := now + int64(4*helloWindow.Seconds())
	if !r.checkAndAdd(n1, future) {
		t.Fatal("nonce should be accepted again after window expiry")
	}
	if len(r.seen) != 1 {
		t.Fatalf("expired entries not swept: %d remain", len(r.seen))
	}
}
