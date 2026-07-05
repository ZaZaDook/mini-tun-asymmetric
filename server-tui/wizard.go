package main

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"crypto/ecdsa"
	"crypto/elliptic"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/config"
)

const tlsDir = "/etc/mini-tun-asymmetric/tls"

// showWizard is the first-run guided setup: pick a role, then fill the minimum
// fields, and the wizard generates the token/TLS, writes the config, opens the
// firewall, and enables+starts the service. Existing menus stay for management.
func showWizard() {
	menu := tview.NewList().
		AddItem("Master Node", "This server is the entry + internet egress", 'm', wizardMaster).
		AddItem("Slave Node", "This server relays downlink to clients", 's', wizardSlave).
		AddItem("← Back", "", 'b', func() { pages.SwitchToPage("main") })
	menu.SetBorder(true).SetTitle(" Quick Setup — choose role ").SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorDarkCyan)
	menu.SetSelectedBackgroundColor(tcell.ColorDarkCyan)
	pages.AddPage("wizard", centered(menu, 64, 10), true, true)
	pages.SwitchToPage("wizard")
}

// ── Master wizard ────────────────────────────────────────────────────────────

func wizardMaster() {
	cfg := config.DefaultMasterConfig()
	// Sensible fresh-install defaults (single control port on the classic UDP
	// port; multi-carrier control_ports can be added later by editing the config).
	if cfg.ListenUDP == "" {
		cfg.ListenUDP = "0.0.0.0:7000"
	}
	if cfg.ListenControl == "" {
		cfg.ListenControl = "0.0.0.0:7001"
	}
	if cfg.ListenDataPlane == "" {
		cfg.ListenDataPlane = "0.0.0.0:7003"
	}
	if cfg.ServerID == "" {
		cfg.ServerID = "master01"
	}
	cfg.TLSCertFile = filepath.Join(tlsDir, "fullchain.pem")
	cfg.TLSKeyFile = filepath.Join(tlsDir, "privkey.pem")

	tokenField := tview.NewInputField().SetLabel("Auth Token (base64)").SetText(cfg.AuthToken).SetFieldWidth(48)

	form := tview.NewForm()
	form.AddFormItem(tokenField).
		AddButton("Generate Token", func() {
			t, err := genToken()
			if err != nil {
				showMessage("Error", "token generation failed: "+err.Error())
				return
			}
			tokenField.SetText(t)
		}).
		AddInputField("Server ID", cfg.ServerID, 16, nil, func(v string) { cfg.ServerID = v }).
		AddInputField("Control port (TCP)", cfg.ListenControl, 20, nil, func(v string) { cfg.ListenControl = v }).
		AddButton("Install & Start", func() {
			cfg.AuthToken = strings.TrimSpace(tokenField.GetText())
			if err := validateTokenLen(cfg.AuthToken); err != nil {
				showMessage("Error", err.Error())
				return
			}
			steps, err := runMasterInstall(cfg)
			if err != nil {
				showMessage("Setup failed", steps+"\n[red]"+err.Error()+"[-]")
				return
			}
			showMessage("Master ready", steps+
				"\n[yellow]Share this token with slaves and clients:[-]\n  "+cfg.AuthToken)
		}).
		AddButton("Cancel", func() { pages.SwitchToPage("wizard") })

	form.SetBorder(true).SetTitle(" Master Quick Setup ").SetBorderColor(tcell.ColorDarkCyan)
	pages.AddPage("wiz_master", flex100(form), true, true)
	pages.SwitchToPage("wiz_master")
}

// runMasterInstall performs the full master bring-up and returns a step log.
func runMasterInstall(cfg *config.MasterConfig) (string, error) {
	var log strings.Builder
	step := func(s string) { log.WriteString("  ✓ " + s + "\n") }

	// 1. TLS: generate a real self-signed cert (the binary's built-in fallback is
	//    a stub without a certificate, so a master needs one on disk).
	if err := genSelfSignedTLS(cfg.TLSCertFile, cfg.TLSKeyFile, cfg.ServerID); err != nil {
		return log.String(), fmt.Errorf("TLS generation: %w", err)
	}
	step("TLS certificate generated in " + tlsDir)

	// 2. Config.
	if err := saveMasterConfig(cfg); err != nil {
		return log.String(), fmt.Errorf("write config: %w", err)
	}
	step("config written: " + masterCfgPath)

	// 3. Firewall: open control + data-plane ports. Ephemeral data-port range is
	//    opened too (port-hopping assigns per-session data ports in that range).
	openFirewall([]string{
		portOf(cfg.ListenUDP) + "/udp",
		portOf(cfg.ListenDataPlane) + "/udp",
		"32768-60999/udp",
	}, []string{portOf(cfg.ListenControl) + "/tcp"})
	step("firewall: control/data ports opened")

	// 4. Enable + start.
	if out, err := enableStart("mini-tun-asymmetric-master"); err != nil {
		return log.String(), fmt.Errorf("service start: %s", out)
	}
	step("service enabled and started")
	return log.String(), nil
}

// ── Slave wizard ─────────────────────────────────────────────────────────────

func wizardSlave() {
	cfg := config.DefaultSlaveConfig()
	if cfg.ListenUDP == "" {
		cfg.ListenUDP = "0.0.0.0:7002"
	}
	if cfg.ListenDataPlane == "" {
		cfg.ListenDataPlane = "0.0.0.0:7004"
	}
	if cfg.SlaveID == "" {
		cfg.SlaveID = "slave01"
	}
	masterHost := tview.NewInputField().SetLabel("Master host (IP or domain)").SetFieldWidth(40)
	tokenField := tview.NewInputField().SetLabel("Auth Token (base64)").SetFieldWidth(48)

	form := tview.NewForm()
	form.AddFormItem(masterHost).
		AddFormItem(tokenField).
		AddInputField("Slave ID", cfg.SlaveID, 16, nil, func(v string) { cfg.SlaveID = v }).
		AddInputField("Transport (utp/cs2/webrtc/quic)", "utp", 12, nil, func(v string) { cfg.Transport = v }).
		AddButton("Install & Start", func() {
			host := strings.TrimSpace(masterHost.GetText())
			if host == "" {
				showMessage("Error", "master host is required")
				return
			}
			cfg.AuthToken = strings.TrimSpace(tokenField.GetText())
			if err := validateTokenLen(cfg.AuthToken); err != nil {
				showMessage("Error", err.Error())
				return
			}
			if cfg.Transport == "" {
				cfg.Transport = "utp"
			}
			cfg.MasterControl = net.JoinHostPort(host, "7001")
			cfg.MasterDataPlane = net.JoinHostPort(host, "7003")
			cfg.TLSCACertFile = "" // slave skips CA verification by default
			steps, err := runSlaveInstall(cfg)
			if err != nil {
				showMessage("Setup failed", steps+"\n[red]"+err.Error()+"[-]")
				return
			}
			showMessage("Slave ready", steps)
		}).
		AddButton("Cancel", func() { pages.SwitchToPage("wizard") })

	form.SetBorder(true).SetTitle(" Slave Quick Setup ").SetBorderColor(tcell.ColorDarkCyan)
	pages.AddPage("wiz_slave", flex100(form), true, true)
	pages.SwitchToPage("wiz_slave")
}

func runSlaveInstall(cfg *config.SlaveConfig) (string, error) {
	var log strings.Builder
	step := func(s string) { log.WriteString("  ✓ " + s + "\n") }

	if err := saveSlaveConfig(cfg); err != nil {
		return log.String(), fmt.Errorf("write config: %w", err)
	}
	step("config written: " + slaveCfgPath)

	openFirewall([]string{
		portOf(cfg.ListenUDP) + "/udp",
		portOf(cfg.ListenDataPlane) + "/udp",
	}, nil)
	step("firewall: downlink/data ports opened")

	if out, err := enableStart("mini-tun-asymmetric-slave"); err != nil {
		return log.String(), fmt.Errorf("service start: %s", out)
	}
	step("service enabled and started")
	return log.String(), nil
}

// ── Helpers: token, TLS, firewall, service ────────────────────────────────────

// genToken returns a fresh 32-byte base64 auth token (crypto/rand).
func genToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// validateTokenLen mirrors config.validateToken (>=16 decoded bytes, valid base64).
func validateTokenLen(tok string) error {
	if tok == "" {
		return fmt.Errorf("auth token is empty — generate or paste one")
	}
	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return fmt.Errorf("auth token is not valid base64")
	}
	if len(raw) < 16 {
		return fmt.Errorf("auth token too short (%d bytes, need >= 16)", len(raw))
	}
	return nil
}

// genSelfSignedTLS writes an ECDSA P-256 self-signed cert+key to the given paths.
// Slaves connect with InsecureSkipVerify by default, so a self-signed pair is
// sufficient for the master's control listener.
func genSelfSignedTLS(certFile, keyFile, cn string) error {
	if err := os.MkdirAll(filepath.Dir(certFile), 0700); err != nil {
		return err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certOut, err := os.OpenFile(certFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	return pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// openFirewall opens the given udp/tcp ports via firewall-cmd (firewalld) or ufw,
// whichever is present. Failures are non-fatal (a node may filter externally).
func openFirewall(udpPorts, tcpPorts []string) {
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		for _, p := range udpPorts {
			exec.Command("firewall-cmd", "--permanent", "--add-port="+p).Run()
		}
		for _, p := range tcpPorts {
			exec.Command("firewall-cmd", "--permanent", "--add-port="+p).Run()
		}
		exec.Command("firewall-cmd", "--reload").Run()
		return
	}
	if _, err := exec.LookPath("ufw"); err == nil {
		for _, p := range udpPorts {
			exec.Command("ufw", "allow", strings.Replace(p, "/", "/", 1)).Run()
		}
		for _, p := range tcpPorts {
			exec.Command("ufw", "allow", p).Run()
		}
	}
}

// enableStart enables the unit on boot and starts it now.
func enableStart(svc string) (string, error) {
	if out, err := exec.Command("systemctl", "enable", svc).CombinedOutput(); err != nil {
		return string(out), err
	}
	out, err := exec.Command("systemctl", "restart", svc).CombinedOutput()
	return string(out), err
}

// portOf extracts the port from a "host:port" listen string.
func portOf(hostport string) string {
	_, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return port
}
