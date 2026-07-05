// mini-tun-asymmetric-agent is the privileged sidecar driving the VPN engine for
// the Flutter GUI. It self-elevates (UAC) for TUN/routing, serves the loopback
// JSON API (agent package), and writes an endpoint file {url,token} the GUI
// reads. It exits when the --owner-pid process (the GUI) goes away, so closing
// the GUI tears the tunnel down cleanly.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ZaZaDook/mini-tun-asymmetric/agent"
	"github.com/ZaZaDook/mini-tun-asymmetric/client-windows/vpncore"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/config"
)

const appName = "Mini-Tun Asymmetric Agent"

// version is set at build time via -ldflags "-X main.version=$(cat VERSION)".
var version = "dev"

func appDataDir() string {
	base, _ := os.UserConfigDir()
	return filepath.Join(base, "MiniTunAsymmetric")
}

func main() {
	ownerPID := flag.Int("owner-pid", 0, "exit when this process (the GUI) is gone")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("mini-tun-asymmetric-agent", version)
		return
	}

	// TUN + routing need Administrator. Relaunch with a UAC prompt if needed.
	if runtime.GOOS == "windows" && !isAdmin() {
		relaunchElevated()
		return
	}

	dataDir := appDataDir()
	os.MkdirAll(dataDir, 0700)
	setupLogging(filepath.Join(dataDir, "agent.log"))

	cfgPath := filepath.Join(dataDir, "config.json")
	cfg, err := config.LoadClientConfig(cfgPath)
	if err != nil {
		cfg = &config.ClientConfig{}
	}

	engine := vpncore.NewEngine(func(state vpncore.State) {
		log.Printf("[%s] state: %s", appName, state)
	})

	srv := agent.NewServer(engine, cfg, cfgPath)
	url, err := srv.Start()
	if err != nil {
		log.Fatalf("[%s] failed to start API: %v", appName, err)
	}

	// Write the endpoint file the GUI reads to find us + authenticate.
	if err := writeEndpoint(dataDir, url, srv.Token()); err != nil {
		log.Printf("[%s] write endpoint: %v", appName, err)
	}
	log.Printf("[%s] ready at %s", appName, url)

	// Lifecycle: exit when the GUI process disappears (so the tunnel never
	// outlives the window). If no owner given, run until killed.
	watchOwner(*ownerPID, func() {
		engine.Disconnect()
		removeEndpoint(dataDir)
		os.Exit(0)
	})

	select {} // serve forever (watchOwner exits the process)
}

type endpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func endpointPath(dataDir string) string {
	return filepath.Join(dataDir, "agent-endpoint.json")
}

func writeEndpoint(dataDir, url, token string) error {
	b, _ := json.Marshal(endpoint{URL: url, Token: token, PID: os.Getpid()})
	// 0600: the token is a local credential; keep it user-only.
	return os.WriteFile(endpointPath(dataDir), b, 0600)
}

func removeEndpoint(dataDir string) { os.Remove(endpointPath(dataDir)) }

// watchOwner polls the GUI pid; when it's gone, runs onGone. No-op if pid<=0.
func watchOwner(pid int, onGone func()) {
	if pid <= 0 {
		return
	}
	go func() {
		for {
			time.Sleep(2 * time.Second)
			if !processAlive(pid) {
				log.Printf("[%s] owner pid %d gone, shutting down", appName, pid)
				onGone()
				return
			}
		}
	}()
}

func setupLogging(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags)
}
