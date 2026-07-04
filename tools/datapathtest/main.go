// datapathtest exercises the full Mini-Tun Asymmetric data path against a live deployment:
// handshake → NAT punch → an encrypted DNS query through the tunnel → decrypted
// answer back via the slave downlink. Point -dns at 10.8.0.1 to verify the
// in-tunnel resolver (the fake-dns-immunity feature); any other server tests
// plain UDP relay. Run it on a host with a routable address (e.g. the master)
// so the asymmetric downlink isn't blocked by NAT.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/crypto"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/protocol"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/transport"
)

// tr is the outer transport envelope used by this dev tool (set in main from -transport).
var tr transport.Transport = transport.NewCS2()

func main() {
	master := flag.String("master", "203.0.113.10:7000", "master UDP address")
	tokenB64 := flag.String("token", "", "base64 auth token")
	dnsSrv := flag.String("dns", "10.8.0.1", "DNS server to query through the tunnel")
	name := flag.String("name", "ip.sb", "domain to resolve")
	trName := flag.String("transport", "", "transport carrier: cs2 (default) | utp")
	hop := flag.Bool("hop", false, "emulate client port-hopping: send data from a fresh socket to the data port")
	probeRTT := flag.Bool("probe-rtt", false, "measure RTT to the assigned slave (ping/pong) and exit")
	flag.Parse()

	if t, terr := transport.ByName(*trName); terr != nil {
		fmt.Fprintf(os.Stderr, "bad -transport: %v\n", terr)
		os.Exit(2)
	} else {
		tr = t
	}

	tok, err := base64.StdEncoding.DecodeString(*tokenB64)
	must(err, "decode token")
	mAddr, err := net.ResolveUDPAddr("udp", *master)
	must(err, "resolve master")

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	must(err, "listen")
	defer conn.Close()

	// ── handshake (Hello v2: HMAC over version+timestamp+nonce, no token on wire) ──
	hello := protocol.HelloMsg{
		Version:   protocol.HelloVersion3,
		Timestamp: uint64(time.Now().Unix()),
	}
	_, nerr := rand.Read(hello.Nonce[:])
	must(nerr, "nonce")
	hello.MAC = crypto.HelloMAC(tok, hello.MarshalUnsigned())
	sendRaw(conn, mAddr, protocol.PktTypeHandshake, hello.Marshal())
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 65535)
	n, _, err := conn.ReadFromUDP(buf)
	must(err, "welcome")
	_, welcomePayload, uerr := tr.Unwrap(buf[:n])
	must(uerr, "unwrap welcome")
	w, err := protocol.ParseWelcome(welcomePayload)
	must(err, "parse welcome")
	fmt.Printf("✓ handshake: tunIP=%s slave=%s:%d dataPort=%d\n", w.AssignedIP, w.SlaveIP, w.SlavePort, w.DataPort)

	// Port-hopping: uplink data goes to the per-session data port, not the control port.
	dataAddr := mAddr
	if w.DataPort != 0 {
		dataAddr = &net.UDPAddr{IP: mAddr.IP, Port: int(w.DataPort)}
	}

	key := crypto.SessionKey(tok, w.AssignedIP)
	aead, _ := crypto.NewAEAD(key)
	rx, _ := crypto.NewAEAD(key)

	// ── NAT punch toward the slave (open the downlink mapping) ──
	slaveAddr := &net.UDPAddr{IP: w.SlaveIP, Port: int(w.SlavePort)}
	punch := tr.Wrap(protocol.PktTypePunch, w.AssignedIP.To4())
	conn.WriteToUDP(punch, slaveAddr)
	time.Sleep(400 * time.Millisecond)

	// ── RTT probe mode: ping each candidate slave, report min RTT, exit ──
	if *probeRTT {
		buf := make([]byte, 2048)
		probe := func(addr *net.UDPAddr) int64 {
			pn := tr.Wrap(protocol.PktTypePunch, w.AssignedIP.To4())
			for i := 0; i < 4; i++ {
				conn.WriteToUDP(pn, addr)
				time.Sleep(250 * time.Millisecond)
			}
			var best int64 = -1
			for i := 0; i < 5; i++ {
				payload := make([]byte, 12)
				copy(payload[0:4], w.AssignedIP.To4())
				binary.BigEndian.PutUint64(payload[4:12], uint64(time.Now().UnixNano()))
				conn.WriteToUDP(tr.Wrap(protocol.PktTypePing, payload), addr)
				conn.SetReadDeadline(time.Now().Add(time.Second))
				for {
					n, _, err := conn.ReadFromUDP(buf)
					if err != nil {
						break
					}
					pt, pl, uerr := tr.Unwrap(buf[:n])
					if uerr != nil || pt != protocol.PktTypePong || len(pl) < 12 {
						continue
					}
					ns := int64(binary.BigEndian.Uint64(pl[4:12]))
					rtt := (time.Now().UnixNano() - ns) / 1e6
					if rtt >= 0 && (best < 0 || rtt < best) {
						best = rtt
					}
					break
				}
				time.Sleep(150 * time.Millisecond)
			}
			return best
		}
		// v3 welcome carries a slave list; probe each. Otherwise probe the one slave.
		cands := []*net.UDPAddr{slaveAddr}
		if len(w.Slaves) > 0 {
			cands = cands[:0]
			for _, sl := range w.Slaves {
				cands = append(cands, &net.UDPAddr{IP: sl.IP, Port: int(sl.Port)})
			}
			fmt.Printf("=== Welcome v3: %d candidate slaves ===\n", len(cands))
		}
		for _, a := range cands {
			rtt := probe(a)
			if rtt >= 0 {
				fmt.Printf("=== slave %s RTT = %dms (min of 5) ===\n", a.IP, rtt)
			} else {
				fmt.Printf("=== slave %s RTT probe: NO REPLY ===\n", a.IP)
			}
		}
		return
	}

	// QUIC symmetric mode: uplink goes to the SLAVE (same socket as downlink), not
	// the master — the slave relays it onward. The punch above registered us.
	if *trName == "quic" {
		dataAddr = slaveAddr
	}

	// ── encrypted DNS query through the tunnel ──
	dns := buildDNSQuery(*name)
	ipPkt := buildUDPIP(w.AssignedIP, net.ParseIP(*dnsSrv), 40000, 53, dns)
	ct, serr := aead.Seal(ipPkt)
	must(serr, "seal")
	// Emulate the real client's port-hopping: when the master handed out a data
	// port, the client dials a FRESH socket to it (different source port than the
	// handshake). This reproduces the master-side lookup bug if present.
	if *hop && w.DataPort != 0 && *trName != "quic" {
		dconn, e := net.DialUDP("udp", nil, dataAddr)
		must(e, "dial data port")
		dconn.Write(tr.Wrap(protocol.PktTypeData, ct))
		fmt.Printf("→ DNS query %q via NEW socket to data port %d\n", *name, w.DataPort)
	} else {
		sendRaw(conn, dataAddr, protocol.PktTypeData, ct)
		fmt.Printf("→ DNS query %q to %s:53 through tunnel\n", *name, *dnsSrv)
	}

	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Fprintln(os.Stderr, "✗ no downlink (timeout)")
			os.Exit(2)
		}
		_, dlPayload, uerr := tr.Unwrap(buf[:n])
		if uerr != nil {
			continue
		}
		plain, err := rx.Open(dlPayload)
		if err != nil {
			continue
		}
		if ip := parseDNSAnswer(plain); ip != "" {
			fmt.Printf("✓ DNS ANSWER via tunnel: %s → %s\n", *name, ip)
			fmt.Println("\n=== FULL PATH OK: handshake + in-tunnel DNS + downlink ===")
			return
		}
		fmt.Printf("✓ downlink %d bytes (no A record parsed)\n", len(plain))
		return
	}
}

func sendRaw(c *net.UDPConn, dst *net.UDPAddr, typ uint8, payload []byte) {
	pkt := tr.Wrap(typ, payload)
	c.WriteToUDP(pkt, dst)
}

func buildDNSQuery(name string) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[0:], 0x1234)
	binary.BigEndian.PutUint16(b[2:], 0x0100)
	binary.BigEndian.PutUint16(b[4:], 1)
	cur := ""
	emit := func(s string) { b = append(b, byte(len(s))); b = append(b, s...) }
	for _, c := range name {
		if c == '.' {
			emit(cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		emit(cur)
	}
	b = append(b, 0, 0, 1, 0, 1) // root, QTYPE A, QCLASS IN
	return b
}

func parseDNSAnswer(ipPkt []byte) string {
	if len(ipPkt) < 20 {
		return ""
	}
	ihl := int(ipPkt[0]&0x0f) * 4
	if len(ipPkt) < ihl+8 {
		return ""
	}
	dns := ipPkt[ihl+8:]
	if len(dns) < 12 {
		return ""
	}
	an := binary.BigEndian.Uint16(dns[6:8])
	if an == 0 {
		return ""
	}
	p := 12
	for p < len(dns) && dns[p] != 0 {
		p += int(dns[p]) + 1
	}
	p += 5
	for i := 0; i < int(an) && p+12 <= len(dns); i++ {
		if dns[p]&0xc0 == 0xc0 {
			p += 2
		} else {
			for p < len(dns) && dns[p] != 0 {
				p += int(dns[p]) + 1
			}
			p++
		}
		if p+10 > len(dns) {
			return ""
		}
		typ := binary.BigEndian.Uint16(dns[p:])
		rdlen := int(binary.BigEndian.Uint16(dns[p+8:]))
		p += 10
		if typ == 1 && rdlen == 4 && p+4 <= len(dns) {
			return net.IPv4(dns[p], dns[p+1], dns[p+2], dns[p+3]).String()
		}
		p += rdlen
	}
	return ""
}

func buildUDPIP(src, dst net.IP, sp, dp uint16, payload []byte) []byte {
	total := 28 + len(payload)
	pkt := make([]byte, total)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:], uint16(total))
	pkt[8] = 64
	pkt[9] = 17
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	binary.BigEndian.PutUint16(pkt[10:], ipck(pkt[:20]))
	u := pkt[20:]
	binary.BigEndian.PutUint16(u[0:], sp)
	binary.BigEndian.PutUint16(u[2:], dp)
	binary.BigEndian.PutUint16(u[4:], uint16(8+len(payload)))
	copy(u[8:], payload)
	return pkt
}

func ipck(b []byte) uint16 {
	var s uint32
	for i := 0; i < len(b)-1; i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	for s>>16 != 0 {
		s = (s & 0xffff) + (s >> 16)
	}
	return ^uint16(s)
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s: %v\n", ctx, err)
		os.Exit(1)
	}
}
