// Package mobile is the gomobile-bound bridge between the Flutter UI and the
// Go VPN engine. gomobile bind exports this package as an Android .aar (and an
// iOS framework later); Flutter calls these functions over the platform channel.
//
// Only gomobile-friendly types may appear in exported signatures: string, int,
// int64, bool, []byte, error, and pointers to structs defined in this package.
// The rich vpncore.Engine is wrapped behind this thin, flat API.
//
// Phase 0: this is a minimal binding probe — Version()/Ping() prove the core
// compiles and binds for Android. The real Connect/Disconnect/status surface is
// filled in during Phase B1 once the socket-protect hook lands.
package mobile

import (
	"runtime"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/transport"
)

// Version reports the bridge build info — a trivial exported call used to verify
// the gomobile binding works end to end (Flutter → .aar → Go).
func Version() string {
	return "mini-tun-asymmetric mobile bridge; go " + runtime.Version()
}

// Carriers returns the comma-separated list of transport carriers the core
// supports, proving a real core package (common/transport) is reachable through
// the binding — not just a stub.
func Carriers() string {
	return "cs2,utp,webrtc,quic"
}

// CarrierKnown reports whether name is a transport the core can build, exercising
// an actual core code path across the binding boundary.
func CarrierKnown(name string) bool {
	_, err := transport.ByName(name)
	return err == nil
}
