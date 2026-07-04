// Package crypto handles all Mini-Tun Asymmetric encryption operations.
// Uses ChaCha20-Poly1305 AEAD with a 32-byte key derived from the auth token.
// Nonce is 12 bytes: [8-byte session ID prefix] + [4-byte counter].
package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	KeySize   = 32
	NonceSize = 12
	TagSize   = 16
	Overhead  = NonceSize + TagSize
)

var ErrDecrypt = errors.New("decryption failed: authentication tag mismatch")
var ErrNonceExhausted = errors.New("nonce counter exhausted: session must be rekeyed")

type AEAD struct {
	aead interface {
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
		NonceSize() int
		Overhead() int
	}
	sessionPrefix [8]byte
	counter       atomic.Uint64
}

// DeriveKey produces a 32-byte session key from an auth token and a salt.
func DeriveKey(token []byte, salt []byte) [KeySize]byte {
	h := sha256.New()
	h.Write(token)
	h.Write([]byte{0x42, 0x61, 0x64, 0x52}) // fixed domain-separation tag ("BadR", the BadRouting protocol) — MUST stay constant; changing it rotates every session key and breaks client/master/slave compat
	h.Write(salt)
	var k [KeySize]byte
	copy(k[:], h.Sum(nil))
	return k
}

// SessionKey derives the per-session AEAD key shared by the client, master, and
// slave for a single tunnel. The only value all three parties hold identically
// is the auth token plus the assigned tunnel IP (the client learns it from the
// Welcome message, the slave from the SlaveSession message), so the key is bound
// to those. This MUST be used identically on all three sides or traffic cannot
// be decrypted.
func SessionKey(token []byte, tunnelIP net.IP) [KeySize]byte {
	salt := make([]byte, 0, len(token)+4)
	salt = append(salt, token...)
	salt = append(salt, tunnelIP.To4()...)
	return DeriveKey(token, salt)
}

// NewAEAD creates an AEAD cipher from a 32-byte key.
func NewAEAD(key [KeySize]byte) (*AEAD, error) {
	c, err := chacha20poly1305.New(key[:])
	if err != nil {
		return nil, err
	}
	var prefix [8]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		return nil, err
	}
	return &AEAD{aead: c, sessionPrefix: prefix}, nil
}

// NewAEADWithPrefix creates an AEAD with a specific session prefix (for slave side).
func NewAEADWithPrefix(key [KeySize]byte, prefix [8]byte) (*AEAD, error) {
	c, err := chacha20poly1305.New(key[:])
	if err != nil {
		return nil, err
	}
	return &AEAD{aead: c, sessionPrefix: prefix}, nil
}

// nonce returns the next unique nonce, or false if the 32-bit on-wire counter
// space is exhausted (continuing would reuse a nonce — catastrophic for AEAD).
func (a *AEAD) nonce() ([]byte, bool) {
	// Reserve a counter value atomically. Add returns the new value, so the first
	// reserved value is 0 (we subtract 1).
	v := a.counter.Add(1) - 1
	if v > 0xFFFFFFFF {
		return nil, false
	}
	n := make([]byte, NonceSize)
	copy(n[0:8], a.sessionPrefix[:])
	binary.BigEndian.PutUint32(n[8:12], uint32(v))
	return n, true
}

// Seal encrypts and authenticates plaintext. The nonce is prepended to the
// output. It returns ErrNonceExhausted if the session has encrypted 2^32
// messages and must be rekeyed (a new session/key is required).
func (a *AEAD) Seal(plaintext []byte) ([]byte, error) {
	n, ok := a.nonce()
	if !ok {
		return nil, ErrNonceExhausted
	}
	out := make([]byte, NonceSize, NonceSize+len(plaintext)+TagSize)
	copy(out, n)
	return a.aead.Seal(out, n, plaintext, nil), nil
}

// Open decrypts and verifies a packet produced by Seal.
func (a *AEAD) Open(data []byte) ([]byte, error) {
	if len(data) < NonceSize+TagSize {
		return nil, ErrDecrypt
	}
	n := data[:NonceSize]
	ct := data[NonceSize:]
	pt, err := a.aead.Open(nil, n, ct, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// RandomSalt generates a random 16-byte salt for key derivation.
func RandomSalt() ([16]byte, error) {
	var s [16]byte
	_, err := rand.Read(s[:])
	return s, err
}

// HelloMAC computes HMAC-SHA256(token, msg). Used to authenticate the client
// handshake (HelloMsg v2) WITHOUT putting the auth token on the wire: the client
// proves it holds the token by signing the version+timestamp+nonce, and the
// master recomputes the tag with its own token. A captured handshake reveals no
// token and (combined with the timestamp window + nonce replay cache on the
// master) cannot be replayed.
func HelloMAC(token, msg []byte) [32]byte {
	mac := hmac.New(sha256.New, token)
	mac.Write(msg)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// VerifyHelloMAC reports whether tag is a valid HMAC-SHA256(token, msg).
// Uses hmac.Equal for constant-time comparison.
func VerifyHelloMAC(token, msg, tag []byte) bool {
	expected := HelloMAC(token, msg)
	return hmac.Equal(expected[:], tag)
}
