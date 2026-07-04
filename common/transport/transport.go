// Package transport provides the pluggable transport-framing layer for Mini-Tun Asymmetric.
//
// A Transport wraps and unwraps the OUTER UDP envelope of every datagram on the
// client<->master and slave<->client hops. It does NOT encrypt — the payload it
// receives is already AEAD-sealed by the caller. Its only job is to make the
// datagram look like some carrier protocol so a DPI box misclassifies it.
//
// The first implementation (cs2) mimics a Source Engine / CS2 connectionless
// datagram, preserving the legacy 9-byte wire header exactly. Future carriers
// (µTP/BitTorrent, WebRTC/TURN, QUIC) plug in here without touching the core
// (crypto, sessions, netstack, the master<->slave data plane, or the asymmetric
// routing). Only the outer framing changes — never the route.
package transport

import (
	"errors"
	"fmt"
)

// ErrNotOurs is returned by Unwrap when a datagram is not a valid frame for this
// carrier (wrong magic, too short, etc). Callers drop such packets silently.
var ErrNotOurs = errors.New("datagram is not a valid frame for this transport")

// Transport wraps/unwraps the obfuscation envelope. Implementations must be safe
// for concurrent use: Wrap and Unwrap are called from multiple goroutines.
type Transport interface {
	// Wrap frames an outgoing datagram carrying our pktType + payload (already
	// encrypted). The returned slice is a fresh buffer owned by the caller.
	Wrap(pktType uint8, payload []byte) []byte

	// Unwrap parses an incoming datagram, returning the carried pktType and the
	// payload (still encrypted). It returns ErrNotOurs if raw isn't a valid frame
	// for this carrier. The returned payload may alias raw; callers that retain it
	// must copy.
	Unwrap(raw []byte) (pktType uint8, payload []byte, err error)

	// Name identifies the carrier ("cs2", "utp", ...) for logs and metrics.
	Name() string
}

// ByName returns the transport with the given name. An empty name defaults to
// "cs2" (the legacy Source Engine obfuscation). Future carriers register here.
func ByName(name string) (Transport, error) {
	switch name {
	case "", "cs2":
		return NewCS2(), nil
	case "utp":
		return NewUTP(), nil
	case "webrtc":
		return NewWebRTC(), nil
	case "quic":
		return NewQUIC(), nil
	default:
		return nil, fmt.Errorf("unknown transport %q", name)
	}
}
