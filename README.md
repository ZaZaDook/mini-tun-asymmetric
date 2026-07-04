# Mini-Tun Asymmetric

A self-written **asymmetric tunnel** and network-research project, built on the
**BadRouting** protocol. Three roles — **client** (Flutter desktop + Linux CLI),
**master** (entry + internet egress) and **slave** (return-path downlink) — where the
uplink and downlink take *different* network paths (hence "Asymmetric"). It's an
experiment in split-path transport and flow separation — the kind of thing you build
to *understand* how networks look at traffic. Purely academic, of course. 😉

```
                 uplink
   Client ----------------> Master ----> internet
      ^                       |
      |                       |
      +------- Slave <--------+
             downlink
```

Uplink and downlink never share a path: the client sends *to* the master, but
receives *from* a slave. The two directions are separate flows on separate hops —
that's the whole idea.

> [!WARNING]
> **Early alpha — proof of concept.** This is a working proof of concept, published
> to share the *idea and architecture* — not a finished product. It exists for
> research and educational purposes: exploring transport-protocol design, NAT
> traversal, and asymmetric routing. Expect rough edges, breaking changes, and
> unaudited security; don't rely on it for anything real. The question this release
> asks is simply: **does the approach make sense?**
>
> Use it only on networks and systems you own or are authorized to test, and in
> accordance with the laws of your jurisdiction. The author provides it as-is, with
> no warranty, and takes no responsibility for how others use it. What you take away
> from the design is entirely up to you. 🙂

## Highlights

- **Asymmetric routing** — uplink `client → master`, downlink `master → slave → client`
  via UDP NAT hole-punching. The return path leaves from a *different* IP than the
  entry, so the two directions are separate flows rather than one bidirectional
  session — the core research idea of the project.
- **Nearest-node selection** — the master hands the client the full list of live
  slaves; the client RTT-probes each (authenticated ping/pong) and pins the closest
  for its downlink, cutting the latency of the triple-hop path. Falls back to
  round-robin for older clients (versioned handshake, backward-compatible).
- **Pluggable transport carriers** — every UDP packet is framed according to a
  selectable carrier format; the carrier is chosen per session (client-side
  auto-race + fallback, or manual). An experiment in how interchangeable the
  on-wire framing of a transport can be — and, purely coincidentally, in how much
  a packet on the wire ends up looking like a game session, a torrent, or a video
  call. 🙃
  - `cs2` — Source Engine / CS2 game datagram framing
  - `utp` — BitTorrent µTP (BEP-29) framing
  - `webrtc` — STUN + RTP framing
  - `quic` — QUIC / HTTP3 framing on :443 (symmetric variant)
- **Custom ports** — the carrier's native control port is the default, but a profile
  can dial any operator-configured port, with comma-separated fallback. The master
  demuxes the carrier by arrival port.
- **Dynamic firewall (server)** — an nftables-backed subsystem (`common/netfw`,
  falls back to firewalld/ufw) opens each ephemeral per-session data port on demand
  and closes it when the session ends — so the box never exposes a wide port range.
  It owns a tagged table and reconciles its own leftovers on startup.
- **In-tunnel DNS** — a DNS resolver on the gateway plus a client-side DNS
  kill-switch and IPv6 block, so name resolution stays consistent inside the tunnel
  regardless of whatever *creative* answers the local network might otherwise decide
  to hand back. 🙂
- **Authenticated handshake** — the auth token never appears on the wire (HMAC
  over a timestamped nonce); stale/replayed handshakes are rejected silently.
- **Encryption** — ChaCha20-Poly1305 AEAD, per-session key bound to the tunnel IP.
- **gVisor netstack egress** — the master terminates client TCP/UDP in a userspace
  stack and proxies byte streams to the internet, so any TCP works.

## Layout

| Path | What |
|---|---|
| `client-flutter/` | Flutter desktop client (Windows; Android scaffold) — the UI |
| `agent/` + `cmd/mini-tun-asymmetric-agent/` | privileged local sidecar the GUI drives over loopback (owns the VPN engine, self-elevates via UAC) |
| `client-windows/` | legacy WebView2 GUI client + the shared `vpncore` engine |
| `cmd/mini-tun-asymmetric-cli/` | headless Linux CLI client |
| `master/` | master node — netstack egress, in-tunnel DNS, sessions, control, metrics |
| `slave/` | slave node — downlink relay |
| `mobile/` | gomobile binding of the core for Android |
| `server-tui/` | nmtui-style server manager TUI |
| `common/` | shared packages — transport carriers, crypto, protocol, tun, config, netfw |
| `tools/datapathtest/` | end-to-end data-path smoke test (handshake, RTT probe, full path) |

## Build

```sh
make                 # build all server/CLI targets into ./dist
# or individually:
make master slave client-windows cli server-tui
```

Server/CLI targets cross-compile from any OS (`CGO_ENABLED=0`). The legacy Windows
client needs `wintun.dll` (from <https://www.wintun.net/>) alongside the executable.

The Flutter client builds with the Flutter SDK (`flutter build windows`); the Go
core is exposed to Android via gomobile (`gomobile bind ./mobile`).

## Status

Working prototype, deployed and verified end-to-end on a 3-node setup (1 master,
2 slaves). Done: multi-carrier transport framing, custom ports, nearest-node
selection, nft-backed dynamic firewall, Flutter desktop client.
Roadmap: graceful-shutdown audit, raw (plain) carrier, IPv6 data-path,
Noise/mTLS authentication, Android release.

