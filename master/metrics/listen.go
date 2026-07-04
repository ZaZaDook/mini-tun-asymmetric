package metrics

import "net"

// listen opens a TCP listener on addr. Factored out so the bind point is
// explicit and easy to adjust (e.g. to enforce localhost-only in the future).
func listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
