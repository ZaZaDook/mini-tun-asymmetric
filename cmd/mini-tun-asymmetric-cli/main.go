// mini-tun-asymmetric-cli is a headless VPN client for testing and Linux servers.
//
// It drives the same vpncore.Engine the GUI client uses, but with no tray/UI,
// so it can run inside a network namespace for isolated end-to-end testing.
//
// Example (inside a netns):
//
//	mini-tun-asymmetric-cli -master 203.0.113.10:7000 -token <base64> -full
//
// With -full it installs split-default (/1) routes so all traffic in the
// current namespace goes through the tunnel. Without it, only the adapter is
// brought up and the caller adds routes (e.g. a single -route host for a
// contained test).
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ZaZaDook/mini-tun-asymmetric/client-windows/vpncore"
)

func main() {
	master := flag.String("master", "", "master address host:port (UDP)")
	token := flag.String("token", "", "base64 auth token")
	full := flag.Bool("full", false, "full-tunnel: route all traffic through the VPN")
	route := flag.String("route", "", "route only this host IP through the tunnel (implies !full)")
	secureDNS := flag.Bool("secure-dns", true, "force OS DNS to in-tunnel resolver + block IPv6 (full-tunnel only)")
	tr := flag.String("transport", "", "transport carrier: cs2 (default) | utp | webrtc | quic | auto")
	debug := flag.Bool("debug", false, "verbose data-path logging")
	wait := flag.Duration("wait", 0, "auto-disconnect after this duration (0 = run until Ctrl-C)")
	flag.Parse()

	if *master == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "usage: mini-tun-asymmetric-cli -master host:port -token <base64> [-full | -route IP] [-debug] [-wait 30s]")
		os.Exit(2)
	}

	vpncore.Debug = *debug

	ready := make(chan struct{})
	var once bool
	eng := vpncore.NewEngine(func(s vpncore.State) {
		log.Printf("[cli] state: %s", s)
		if s == vpncore.StateConnected && !once {
			once = true
			close(ready)
		}
	})
	// In -route mode we manage routing ourselves, so disable full-tunnel.
	eng.FullTunnel = *full && *route == ""
	// SecureDNS only makes sense in full-tunnel mode (the gateway resolver must
	// be reachable through the tunnel). In -route mode, leave the OS DNS alone.
	eng.SecureDNS = *secureDNS && eng.FullTunnel
	eng.Transport = *tr

	if err := eng.Connect(*master, *token); err != nil {
		log.Fatalf("[cli] connect: %v", err)
	}

	select {
	case <-ready:
		log.Printf("[cli] connected; tunnel IP = %s carrier = %s", eng.TunnelIP(), eng.ActiveTransport())
	case <-time.After(20 * time.Second):
		eng.Disconnect()
		log.Fatalf("[cli] timed out waiting for connection")
	}

	// Single-host route mode: contained test that can't disturb host networking.
	if *route != "" {
		ip := net.ParseIP(*route)
		if ip == nil {
			eng.Disconnect()
			log.Fatalf("[cli] bad -route IP %q", *route)
		}
		if err := eng.AddHostRoute(ip); err != nil {
			log.Printf("[cli] add host route %s: %v", ip, err)
		} else {
			log.Printf("[cli] routing %s through tunnel", ip)
		}
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	if *wait > 0 {
		select {
		case <-stop:
		case <-time.After(*wait):
			log.Printf("[cli] wait elapsed, disconnecting")
		}
	} else {
		<-stop
	}

	eng.Disconnect()
	up, down, dur := eng.Stats().Snapshot()
	log.Printf("[cli] disconnected; up=%dB down=%dB dur=%s", up, down, dur)
}
