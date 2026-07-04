// Package agent is the privileged local sidecar that the Flutter GUI drives over
// loopback HTTP. It owns the vpncore.Engine (TUN, routing, DNS — needs admin),
// while the GUI runs unprivileged and talks to it via 127.0.0.1 + a bearer token
// (same model as WireGuard/Clash Verge). JSON-only: no embedded HTML, so it has
// no dependency on the legacy WebView client and survives its removal (Phase A2).
package agent

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/ZaZaDook/mini-tun-asymmetric/client-windows/vpncore"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/config"
)

// Server is the loopback JSON API around the VPN engine.
type Server struct {
	engine  *vpncore.Engine
	cfg     *config.ClientConfig
	cfgPath string
	token   string
	port    int
}

func NewServer(engine *vpncore.Engine, cfg *config.ClientConfig, cfgPath string) *Server {
	return &Server{engine: engine, cfg: cfg, cfgPath: cfgPath}
}

// Token returns the bearer token the GUI must present (generated on Start).
func (s *Server) Token() string { return s.token }

// Port returns the bound loopback port (valid after Start).
func (s *Server) Port() int { return s.port }

// Start binds 127.0.0.1 on a random port, generates a bearer token, and serves
// the API. The URL is returned; the caller writes {url,token} to an endpoint
// file the GUI reads.
func (s *Server) Start() (string, error) {
	var tb [24]byte
	if _, err := rand.Read(tb[:]); err != nil {
		return "", err
	}
	s.token = base64.RawURLEncoding.EncodeToString(tb[:])

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", s.handlePing) // unauthenticated liveness probe
	mux.HandleFunc("/api/status", s.auth(s.handleStatus))
	mux.HandleFunc("/api/profiles", s.auth(s.handleProfiles))
	mux.HandleFunc("/api/uiprefs", s.auth(s.handleUIPrefs))
	mux.HandleFunc("/api/connect", s.auth(s.handleConnect))
	mux.HandleFunc("/api/disconnect", s.auth(s.handleDisconnect))

	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Printf("[agent] server error: %v", err)
		}
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d", s.port)
	log.Printf("[agent] listening on %s", url)
	return url, nil
}

// auth wraps a handler with constant-time bearer-token verification.
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	want := "Bearer " + s.token
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	up, down, dur := s.engine.Stats().Snapshot()
	json.NewEncoder(w).Encode(map[string]any{
		"state":        s.engine.State().String(),
		"tunnel_ip":    s.engine.TunnelIP(),
		"transport":    s.engine.ActiveTransport(),
		"up_bytes":     up,
		"dn_bytes":     down,
		"uptime_s":     int(dur.Seconds()),
		"slave_rtt_ms": s.engine.SlaveRTT(),
	})
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(s.cfg.Profiles)
	case http.MethodPost:
		var p config.ClientProfile
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.cfg.Profiles = append(s.cfg.Profiles, p)
		config.SaveClientConfig(s.cfgPath, s.cfg)
		json.NewEncoder(w).Encode(s.cfg.Profiles)
	case http.MethodPut:
		var req struct {
			Index   int                  `json:"index"`
			Profile config.ClientProfile `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.Index >= 0 && req.Index < len(s.cfg.Profiles) {
			s.cfg.Profiles[req.Index] = req.Profile
			config.SaveClientConfig(s.cfgPath, s.cfg)
		}
		json.NewEncoder(w).Encode(s.cfg.Profiles)
	case http.MethodDelete:
		var req struct {
			Index int `json:"index"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.Index >= 0 && req.Index < len(s.cfg.Profiles) {
			s.cfg.Profiles = append(s.cfg.Profiles[:req.Index], s.cfg.Profiles[req.Index+1:]...)
			config.SaveClientConfig(s.cfgPath, s.cfg)
		}
		json.NewEncoder(w).Encode(s.cfg.Profiles)
	}
}

func (s *Server) handleUIPrefs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		if s.cfg.UIPrefs == nil {
			json.NewEncoder(w).Encode(&config.UIPrefs{})
			return
		}
		json.NewEncoder(w).Encode(s.cfg.UIPrefs)
	case http.MethodPut:
		var p config.UIPrefs
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.cfg.UIPrefs = &p
		config.SaveClientConfig(s.cfgPath, s.cfg)
		json.NewEncoder(w).Encode(s.cfg.UIPrefs)
	default:
		http.Error(w, "GET or PUT only", 405)
	}
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var req struct {
		Index int `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.Index < 0 || req.Index >= len(s.cfg.Profiles) {
		http.Error(w, "invalid profile index", 400)
		return
	}
	p := s.cfg.Profiles[req.Index]
	s.engine.Transport = p.Transport
	s.engine.CustomPorts = p.CustomPorts
	if err := s.engine.Connect(p.MasterAddr, p.AuthToken); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	s.engine.Disconnect()
	w.WriteHeader(http.StatusNoContent)
}
