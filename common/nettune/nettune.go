// Package nettune centralizes socket performance tuning shared by all Mini-Tun Asymmetric
// components. The single biggest throughput killer for a UDP-based tunnel on a
// fast link is an undersized socket buffer: at gigabit speeds the kernel's
// default receive buffer (~208 KiB on Linux) overflows in bursts, silently
// dropping datagrams. The tunneled TCP flows interpret those drops as
// congestion, collapse their window, and throughput craters — often presenting
// as "a few Mbit then it stalls". Enlarging SO_RCVBUF/SO_SNDBUF fixes it.
package nettune

import "net"

// UDPBufferBytes is the target size for UDP socket send/receive buffers.
// 2 MiB is large enough to absorb gigabit bursts (the default ~208 KiB overflows
// and the kernel drops datagrams — visible as UdpRcvbufErrors), but small enough
// to avoid the bufferbloat that an oversized 8 MiB buffer caused (huge queue ->
// latency spikes -> stalls). Tuned against the UdpRcvbufErrors counter.
const UDPBufferBytes = 2 << 20 // 2 MiB

// TuneUDP enlarges the send and receive buffers on a UDP socket. Errors are
// returned for logging but are non-fatal: the socket still works at a reduced
// rate, and the OS may clamp silently regardless.
func TuneUDP(c *net.UDPConn) error {
	if c == nil {
		return nil
	}
	if err := c.SetReadBuffer(UDPBufferBytes); err != nil {
		return err
	}
	if err := c.SetWriteBuffer(UDPBufferBytes); err != nil {
		return err
	}
	return nil
}
