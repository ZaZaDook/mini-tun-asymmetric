// Package ui provides a local web UI served at 127.0.0.1 for the VPN client.
// The tray icon opens the browser to this UI. No CGO required.
package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/ZaZaDook/mini-tun-asymmetric/client-windows/vpncore"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/config"
)

//go:embed index.html
var indexHTML []byte

// carrierIcons holds the transport-carrier logos shown next to each profile.
// Embedded from the client assets so they ship inside the exe. Keys are the URL
// path suffix (/assets/<key>) → file bytes; served by handleAssets.
//
//go:embed assets/utorrent.svg assets/webrtc.svg assets/quic.svg assets/cs.svg
var carrierAssets embed.FS

// Server serves the web UI and JSON API for the Windows client.
type Server struct {
	engine  *vpncore.Engine
	cfg     *config.ClientConfig
	cfgPath string
	port    int
}

func NewServer(engine *vpncore.Engine, cfg *config.ClientConfig, cfgPath string) *Server {
	return &Server{engine: engine, cfg: cfg, cfgPath: cfgPath}
}

func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/assets/", http.FileServer(http.FS(carrierAssets)))
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/uiprefs", s.handleUIPrefs)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)

	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Printf("[webui] server error: %v", err)
		}
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d", s.port)
	log.Printf("[webui] listening on %s", url)
	return url, nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
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
		var req struct{ Index int `json:"index"` }
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

// handleUIPrefs persists the UI look-and-feel (theme/custom gradient/language)
// in the config file. localStorage can't be relied on: the UI is served on a
// random 127.0.0.1 port each launch, so its origin — and thus its localStorage
// — changes every restart. GET returns the saved prefs; PUT replaces them.
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
