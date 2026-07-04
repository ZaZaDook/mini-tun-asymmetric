package transport

import "github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"

// cs2Transport mimics a Source Engine / CS2 connectionless UDP datagram. It is a
// thin wrapper over the legacy protocol.BuildHeader/ParseHeader so the wire bytes
// are byte-for-byte identical to the pre-Transport format — this lets the refactor
// ship without changing anything on the wire.
//
// NOTE: this carrier is the weakest of the planned set (a static FF FF FF FF 41
// prefix on every datagram is a DPI tell — real CS2 only uses it for connection
// setup, not gameplay). It remains the default for backward compatibility; µTP /
// WebRTC / QUIC carriers replace it for users who need real cover traffic.
type cs2Transport struct{}

// NewCS2 returns the legacy CS2 obfuscation transport.
func NewCS2() Transport { return cs2Transport{} }

func (cs2Transport) Wrap(pktType uint8, payload []byte) []byte {
	hdr := protocol.BuildHeader(pktType, 0)
	frame := make([]byte, protocol.HeaderSize+len(payload))
	copy(frame[:protocol.HeaderSize], hdr[:])
	copy(frame[protocol.HeaderSize:], payload)
	return frame
}

func (cs2Transport) Unwrap(raw []byte) (uint8, []byte, error) {
	hdr, err := protocol.ParseHeader(raw)
	if err != nil {
		return 0, nil, ErrNotOurs
	}
	return hdr.PktType(), raw[protocol.HeaderSize:], nil
}

func (cs2Transport) Name() string { return "cs2" }
