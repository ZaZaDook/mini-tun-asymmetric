// Package config defines shared configuration structures for all Mini-Tun Asymmetric components.
package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

// minTokenBytes is the minimum decoded auth-token length. A short or empty token
// yields a predictable HMAC key and predictable derived master/session keys, so
// the master and slave refuse to start without a real one. 16 bytes = 128 bits.
const minTokenBytes = 16

// validateToken rejects empty/short/undecodable auth tokens. The token is a
// base64 string; we check the DECODED length.
func validateToken(tok string) error {
	if tok == "" {
		return fmt.Errorf("auth_token is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return fmt.Errorf("auth_token is not valid base64: %w", err)
	}
	if len(raw) < minTokenBytes {
		return fmt.Errorf("auth_token too short: %d bytes decoded, need >= %d", len(raw), minTokenBytes)
	}
	return nil
}

// MasterConfig is the configuration file for the Master Node.
type MasterConfig struct {
	ListenUDP     string       `json:"listen_udp"`      // e.g. "0.0.0.0:7000"
	ListenControl string       `json:"listen_control"`  // TCP for slave connections, e.g. "0.0.0.0:7001"
	TunnelSubnet  string       `json:"tunnel_subnet"`   // e.g. "10.8.0.0/24"
	TunnelIP      string       `json:"tunnel_ip"`       // master's tunnel IP, e.g. "10.8.0.1"
	AuthToken     string       `json:"auth_token"`      // base64 token clients must present
	ServerID      string       `json:"server_id"`       // 8-char identifier
	Slaves        []SlaveEntry `json:"slaves"`          // pre-configured slaves (optional)
	TLSCertFile   string       `json:"tls_cert_file"`
	TLSKeyFile    string       `json:"tls_key_file"`
	ListenDataPlane string      `json:"listen_data_plane"` // UDP for slave data channel, e.g. "0.0.0.0:7003"

	// In-tunnel DNS resolver. The master runs a resolver on TunnelIP:53 so clients
	// can be forced to send all DNS through the tunnel (defeating router fake-dns).
	// DNSMode selects how the master resolves names upstream:
	//   "plain" — classic UDP/TCP DNS to DNSUpstream (e.g. "8.8.8.8:53")
	//   "dot"   — DNS-over-TLS to DNSUpstream (host:853, e.g. "1.1.1.1:853")
	//   "doh"   — DNS-over-HTTPS to DNSUpstreamDoH (e.g. "https://cloudflare-dns.com/dns-query")
	DNSMode        string `json:"dns_mode"`         // plain | dot | doh (default "plain")
	DNSUpstream    string `json:"dns_upstream"`     // host:port for plain/dot, e.g. "8.8.8.8:53" / "1.1.1.1:853"
	DNSUpstreamDoH string `json:"dns_upstream_doh"` // DoH endpoint URL when DNSMode == "doh"
	// DNSSuppressAAAA drops/​empties AAAA (IPv6) answers. The data path is IPv4-only,
	// so returning AAAA records would make clients try IPv6 routes that can't be
	// tunneled. Default true.
	DNSSuppressAAAA *bool `json:"dns_suppress_aaaa,omitempty"`

	// MetricsListen, if non-empty, starts a JSON metrics endpoint at this address
	// (e.g. "127.0.0.1:9090"). Bind to localhost unless network exposure is
	// intended; the snapshot reveals session counts and slave health.
	MetricsListen string `json:"metrics_listen"`

	// Transport selects the outer transport carrier ("cs2" legacy default, "utp"
	// BitTorrent µTP, ...). Must match the slaves and clients of this deployment.
	Transport string `json:"transport,omitempty"`

	// ControlPorts lists the UDP control ports the master listens on, each bound to
	// an transport carrier. The arrival port selects the carrier (demux): a client
	// mimicking µTP connects to the torrent port (6881), a WebRTC client to the STUN
	// port (3478), etc. After a successful handshake on a control port the master
	// allocates a random ephemeral DATA port for that session and returns it in the
	// Welcome, so heavy traffic never sits on a static port. If empty, the master
	// falls back to the single ListenUDP + Transport above (legacy behavior).
	ControlPorts []ControlPort `json:"control_ports,omitempty"`

	LogLevel      string       `json:"log_level"`
}

// ControlPort binds a UDP listen port to an transport carrier.
type ControlPort struct {
	Port      int    `json:"port"`
	Transport string `json:"transport"` // "cs2" | "utp" | ...
}

// SlaveConfig is the configuration file for the Slave Node.
type SlaveConfig struct {
	MasterControl string `json:"master_control"`  // TCP addr of master control, e.g. "1.2.3.4:7001"
	ListenUDP       string `json:"listen_udp"`        // downlink UDP to clients, e.g. "0.0.0.0:7002"
	ListenDataPlane string `json:"listen_data_plane"` // UDP from master data plane, e.g. "0.0.0.0:7003"
	AuthToken       string `json:"auth_token"`        // must match master's token
	TLSCACertFile string `json:"tls_ca_cert_file"`
	LogLevel      string `json:"log_level"`
	SlaveID       string `json:"slave_id"`
	// Transport must match the master's transport (the transport carrier).
	Transport string `json:"transport,omitempty"`
	// MasterDataPlane is the master's data-plane UDP address (e.g. "1.2.3.4:7003").
	// Used by the QUIC symmetric mode to relay client uplink back to the master.
	// If empty, it's derived from MasterControl's host with the default 7003 port.
	MasterDataPlane string `json:"master_data_plane,omitempty"`
}

// SlaveEntry is a known slave registered on the master.
type SlaveEntry struct {
	ID      string `json:"id"`
	Address string `json:"address"` // public IP:port for UDP downlink
}

// ClientProfile is one "server pair" saved by the Windows/Linux client.
type ClientProfile struct {
	Name       string `json:"name"`
	MasterAddr string `json:"master_addr"` // host only (no port — the carrier picks the port)
	AuthToken  string `json:"auth_token"`  // base64
	AutoStart  bool   `json:"auto_start"`
	// Transport selects the transport carrier; must match the server. Empty = cs2.
	Transport string `json:"transport,omitempty"`
	// CustomPorts overrides the carrier's default control port. When set, the
	// client dials the chosen Transport carrier on these ports instead of the
	// built-in default (utp→6881, webrtc→3478, quic→443, cs2→7000), trying each
	// in order (fallback) until one completes a handshake. The master must be
	// listening for that carrier on the port for it to work.
	CustomPorts []int `json:"custom_ports,omitempty"`
}

// UIPrefs holds the desktop UI's look-and-feel settings. These live in the
// config file (not browser localStorage) because the UI is served on a random
// 127.0.0.1 port each launch — localStorage is keyed by origin (host:port), so
// it would be wiped every restart. Persisting them server-side keeps the chosen
// theme / custom gradient / language across restarts.
type UIPrefs struct {
	Theme        string   `json:"theme,omitempty"`         // dark|light|pink|green|blue|custom
	CustomColors []string `json:"custom_colors,omitempty"` // 2–5 hex stops for the custom gradient
	Lang         string   `json:"lang,omitempty"`          // ru|en
}

// ClientConfig is the full client configuration file.
type ClientConfig struct {
	Profiles      []ClientProfile `json:"profiles"`
	ActiveProfile string          `json:"active_profile"`
	DNSFallback   string          `json:"dns_fallback"`
	LogLevel      string          `json:"log_level"`
	UIPrefs       *UIPrefs        `json:"ui_prefs,omitempty"`
}

func LoadMasterConfig(path string) (*MasterConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c MasterConfig
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, err
	}
	if err := validateToken(c.AuthToken); err != nil {
		return nil, err
	}
	return &c, nil
}

func LoadSlaveConfig(path string) (*SlaveConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c SlaveConfig
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, err
	}
	if err := validateToken(c.AuthToken); err != nil {
		return nil, err
	}
	return &c, nil
}

func LoadClientConfig(path string) (*ClientConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c ClientConfig
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func SaveClientConfig(path string, c *ClientConfig) error {
	// 0600: the client config holds the auth token. On POSIX os.Create would use
	// 0666&umask (often 0644, world-readable); force owner-only. On Windows the
	// perm is advisory (AppData is ACL-protected), so this is harmless there.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(c)
}

func DefaultMasterConfig() *MasterConfig {
	suppressAAAA := true
	return &MasterConfig{
		ListenUDP:     "0.0.0.0:7000",
		ListenControl: "0.0.0.0:7001",
		TunnelSubnet:  "10.8.0.0/24",
		TunnelIP:      "10.8.0.1",
		DNSMode:       "plain",
		DNSUpstream:   "8.8.8.8:53",
		DNSSuppressAAAA: &suppressAAAA,
		MetricsListen: "127.0.0.1:9090",
		LogLevel:      "info",
	}
}

func DefaultSlaveConfig() *SlaveConfig {
	return &SlaveConfig{
		ListenUDP:       "0.0.0.0:7002",
		ListenDataPlane: "0.0.0.0:7004",
		LogLevel:        "info",
	}
}
