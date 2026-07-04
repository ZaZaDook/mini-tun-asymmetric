// Package netstack provides a userspace TCP/IP stack (gVisor) for the master.
//
// The master receives decrypted IPv4 packets from clients and injects them into
// this stack. The stack terminates the client's TCP/UDP connections properly
// (full handshake, seq/ack, windowing) and proxies the byte streams to the real
// internet. Response packets emitted by the stack are handed back to the master
// via the Outbound callback, which routes them to the owning slave for downlink.
package netstack

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	nicID = 1
	// mtu must match the client's TUN MTU. Kept below 1500 so that, after the
	// obfuscation header + AEAD overhead are added on the master→slave and
	// slave→client hops, packets still fit a standard 1500-byte path without
	// fragmentation. Too high and large TCP flows (TLS handshakes, video) stall.
	mtu = 1400
)

// NetStack is a userspace network stack that proxies client traffic to the net.
type NetStack struct {
	stack    *stack.Stack
	ep       *channel.Endpoint
	outbound func([]byte) // called with each outbound IPv4 packet (to a client)
	logf     func(string, ...any)

	// dnsResolver, if set, answers DNS queries addressed to dnsAddr:53 in-tunnel
	// instead of proxying them to the original destination. This lets the client
	// force all DNS through the tunnel, defeating router fake-dns.
	dnsResolver DNSResolver
	dnsAddr     string // tunnel gateway IP (e.g. "10.8.0.1")
}

// DNSResolver answers a raw DNS query (wire format) with a raw DNS response.
type DNSResolver interface {
	Query(query []byte) ([]byte, error)
}

// SetDNS enables in-tunnel DNS: queries to gatewayIP:53 are answered by r.
func (ns *NetStack) SetDNS(gatewayIP string, r DNSResolver) {
	ns.dnsAddr = gatewayIP
	ns.dnsResolver = r
}

// New builds the stack. outbound is invoked for every IP packet the stack emits
// toward a client (a TCP/UDP response); the caller routes it to the right slave.
func New(outbound func([]byte), logf func(string, ...any)) (*NetStack, error) {
	// Register both IPv4 and IPv6 network protocols so the userspace stack can
	// handle packets from either stack. The outbound callback (set by the caller)
	// is responsible for routing IPv6 packets to the owning slave.
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})

	// TCP window auto-tuning. On a tunnel path with ~100ms RTT, a small fixed
	// receive window caps single-stream throughput to window/RTT — which is
	// exactly the "DNS times out / download stalls under load" symptom once the
	// UDP-buffer drops are fixed. Enlarging the window range + enabling moderate
	// (auto) receive buffer lets one flow fill the pipe. (UDP socket buffers are
	// tuned separately via nettune; this is the gVisor TCP layer.)
	{
		rcv := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4 << 10, Default: 512 << 10, Max: 6 << 20}
		s.SetTransportProtocolOption(tcp.ProtocolNumber, &rcv)
		snd := tcpip.TCPSendBufferSizeRangeOption{Min: 4 << 10, Default: 512 << 10, Max: 6 << 20}
		s.SetTransportProtocolOption(tcp.ProtocolNumber, &snd)
		moderate := tcpip.TCPModerateReceiveBufferOption(true)
		s.SetTransportProtocolOption(tcp.ProtocolNumber, &moderate)
	}

	ep := channel.New(1024, mtu, "")
	if err := s.CreateNIC(nicID, ep); err != nil {
		return nil, fmt.Errorf("CreateNIC: %v", err)
	}
	// Accept packets addressed to any IP (clients send to arbitrary internet
	// destinations) and allow spoofed source addresses on the way back.
	s.SetPromiscuousMode(nicID, true)
	s.SetSpoofing(nicID, true)
	s.AddRoute(tcpip.Route{Destination: header.IPv4EmptySubnet, NIC: nicID})
	// Route all IPv6 traffic through the tunnel (outbound callback handles per-session routing).
	s.AddRoute(tcpip.Route{Destination: header.IPv6EmptySubnet, NIC: nicID})

	ns := &NetStack{stack: s, ep: ep, outbound: outbound, logf: logf}

	tcpFwd := tcp.NewForwarder(s, 0, 2048, ns.handleTCP)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)
	udpFwd := udp.NewForwarder(s, ns.handleUDP)
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	go ns.outboundLoop()
	return ns, nil
}

// InjectClient feeds a decrypted client IP packet into the stack. The caller
// must ensure ipPkt[0]>>4 yields 4 (IPv4) or 6 (IPv6); other versions are
// silently dropped.
func (ns *NetStack) InjectClient(ipPkt []byte) {
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(ipPkt),
	})
	switch ipPkt[0] >> 4 {
	case 4:
		ns.ep.InjectInbound(ipv4.ProtocolNumber, pkt)
	case 6:
		ns.ep.InjectInbound(ipv6.ProtocolNumber, pkt)
	default:
		pkt.DecRef()
	}
}

// outboundLoop reads packets the stack wants to send to clients and forwards
// them to the caller's outbound callback.
func (ns *NetStack) outboundLoop() {
	for {
		pkt := ns.ep.ReadContext(context.Background())
		if pkt == nil {
			return
		}
		buf := pkt.ToBuffer()
		ns.outbound(buf.Flatten())
		pkt.DecRef()
	}
}

func (ns *NetStack) handleTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	dst := net.JoinHostPort(net.IP(id.LocalAddress.AsSlice()).String(), fmt.Sprint(id.LocalPort))

	upstream, err := net.DialTimeout("tcp", dst, 10*time.Second)
	if err != nil {
		ns.logf("[netstack] tcp dial %s: %v", dst, err)
		r.Complete(true) // send RST to client
		return
	}

	var wq waiter.Queue
	ep, tcperr := r.CreateEndpoint(&wq)
	if tcperr != nil {
		ns.logf("[netstack] tcp CreateEndpoint %s: %v", dst, tcperr)
		upstream.Close()
		r.Complete(true)
		return
	}
	r.Complete(false)

	client := gonet.NewTCPConn(&wq, ep)
	go proxyConn(client, upstream)
}

func (ns *NetStack) handleUDP(r *udp.ForwarderRequest) bool {
	id := r.ID()
	dstIP := net.IP(id.LocalAddress.AsSlice())
	dst := net.JoinHostPort(dstIP.String(), fmt.Sprint(id.LocalPort))

	var wq waiter.Queue
	ep, tcperr := r.CreateEndpoint(&wq)
	if tcperr != nil {
		ns.logf("[netstack] udp CreateEndpoint %s: %v", dst, tcperr)
		return true
	}
	client := gonet.NewUDPConn(&wq, ep)

	// In-tunnel DNS: answer queries to gateway:53 with our resolver instead of
	// proxying to the (fake) destination the client packet carries.
	if ns.dnsResolver != nil && id.LocalPort == 53 && dstIP.String() == ns.dnsAddr {
		go ns.serveDNS(client)
		return true
	}

	upstream, err := net.Dial("udp", dst)
	if err != nil {
		ns.logf("[netstack] udp dial %s: %v", dst, err)
		client.Close()
		return true
	}
	go proxyUDP(client, upstream)
	return true
}

// serveDNS reads DNS queries from a tunneled UDP "connection" and answers them
// via the resolver. gVisor's UDP forwarder gives one endpoint per client
// 5-tuple, so a stub resolver's repeated queries arrive on the same conn.
func (ns *NetStack) serveDNS(client net.Conn) {
	defer client.Close()
	buf := make([]byte, 4096)
	for {
		client.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := client.Read(buf)
		if err != nil {
			return
		}
		query := append([]byte(nil), buf[:n]...)
		go func() {
			resp, err := ns.dnsResolver.Query(query)
			if err != nil {
				ns.logf("[netstack] dns query: %v", err)
				return
			}
			client.SetWriteDeadline(time.Now().Add(5 * time.Second))
			client.Write(resp)
		}()
	}
}

// proxyConn pipes a TCP stream both ways and closes when either side ends.
func proxyConn(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
}

// proxyUDP relays datagrams both ways with an idle timeout.
func proxyUDP(client, upstream net.Conn) {
	const idle = 2 * time.Minute
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		buf := make([]byte, 65535)
		for {
			src.SetReadDeadline(time.Now().Add(idle))
			n, err := src.Read(buf)
			if n > 0 {
				dst.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go cp(client, upstream)
	go cp(upstream, client)
	<-done
	client.Close()
	upstream.Close()
}
