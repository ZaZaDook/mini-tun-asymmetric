// server-tui is an nmtui-style TUI for configuring and managing Mini-Tun Asymmetric Master/Slave nodes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/ZaZaDook/mini-tun-asymmetric/common/config"
)

const (
	masterCfgPath = "/etc/mini-tun-asymmetric/master.json"
	slaveCfgPath  = "/etc/mini-tun-asymmetric/slave.json"
)

var (
	app   *tview.Application
	pages *tview.Pages
)

// version is set at build time via -ldflags "-X main.version=$(cat VERSION)".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("mta-setup", version)
		return
	}

	app = tview.NewApplication()
	pages = tview.NewPages()

	showMainMenu()

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ── Main menu ──────────────────────────────────────────────────────────────

func showMainMenu() {
	menu := tview.NewList().
		AddItem("Quick Setup Wizard", "First-run: pick role, generate token, install & start", 'w', func() { showWizard() }).
		AddItem("Master Node", "Configure and manage the Master node", 'm', func() { showMasterMenu() }).
		AddItem("Slave Node", "Configure and manage the Slave node", 's', func() { showSlaveMenu() }).
		AddItem("Overview", "Sessions, transport, config, connected slaves", 't', func() { showStatus() }).
		AddItem("Generate TLS Certs", "Create self-signed TLS certificate pair", 'g', func() { showGenTLS() }).
		AddItem("Quit", "Exit Mini-Tun Asymmetric Server Manager", 'q', func() { app.Stop() })

	menu.SetBorder(true).
		SetTitle(" Mini-Tun Asymmetric Server Manager ").
		SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorDarkCyan)
	menu.SetSelectedBackgroundColor(tcell.ColorDarkCyan)

	addPage("main", centered(menu, 62, 16), menu)
	goPage("main")

	app.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyCtrlC {
			app.Stop()
		}
		return e
	})
}

// ── Master ─────────────────────────────────────────────────────────────────

func showMasterMenu() {
	menu := tview.NewList().
		AddItem("Edit Config", "Edit master.json", 'e', func() { showMasterConfigForm() }).
		AddItem("Start Service", "systemctl start mini-tun-asymmetric-master", 's', func() { runSystemctl("start", "mini-tun-asymmetric-master") }).
		AddItem("Stop Service", "systemctl stop mini-tun-asymmetric-master", 'x', func() { runSystemctl("stop", "mini-tun-asymmetric-master") }).
		AddItem("Restart Service", "systemctl restart mini-tun-asymmetric-master", 'r', func() { runSystemctl("restart", "mini-tun-asymmetric-master") }).
		AddItem("Enable on Boot", "systemctl enable mini-tun-asymmetric-master", 'b', func() { runSystemctl("enable", "mini-tun-asymmetric-master") }).
		AddItem("View Logs", "journalctl -u mini-tun-asymmetric-master -n 50", 'l', func() { showLogs("mini-tun-asymmetric-master") }).
		AddItem("Show Join Info", "Reveal the token + how to connect a slave", 'j', func() { showJoinInfo() }).
		AddItem("← Back", "Return to the main menu (or press Esc)", 'k', func() { goPage("main") })

	menu.SetBorder(true).SetTitle(" Master Node ").SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorDarkCyan)
	menu.SetSelectedBackgroundColor(tcell.ColorDarkCyan)
	menu.SetInputCapture(escToPage("main"))
	addPage("master_menu", centered(menu, 62, 20), menu)
	goPage("master_menu")
}

func showMasterConfigForm() {
	cfg := config.DefaultMasterConfig()
	if data, err := os.ReadFile(masterCfgPath); err == nil {
		json.Unmarshal(data, cfg)
	}

	form := tview.NewForm()
	form.AddInputField("Listen UDP", cfg.ListenUDP, 30, nil, func(v string) { cfg.ListenUDP = v }).
		AddInputField("Listen Control (TCP)", cfg.ListenControl, 30, nil, func(v string) { cfg.ListenControl = v }).
		AddInputField("Listen Data Plane (UDP)", cfg.ListenDataPlane, 30, nil, func(v string) { cfg.ListenDataPlane = v }).
		AddInputField("Tunnel Subnet", cfg.TunnelSubnet, 20, nil, func(v string) { cfg.TunnelSubnet = v }).
		AddInputField("Tunnel IP (gateway)", cfg.TunnelIP, 20, nil, func(v string) { cfg.TunnelIP = v }).
		AddPasswordField("Auth Token (base64)", cfg.AuthToken, 40, '*', func(v string) { cfg.AuthToken = v }).
		AddInputField("Server ID", cfg.ServerID, 16, nil, func(v string) { cfg.ServerID = v }).
		AddInputField("TLS Cert (fullchain.pem)", cfg.TLSCertFile, 50, nil, func(v string) { cfg.TLSCertFile = v }).
		AddInputField("TLS Key (privkey.pem)", cfg.TLSKeyFile, 50, nil, func(v string) { cfg.TLSKeyFile = v }).
		AddInputField("DNS Upstream", cfg.DNSUpstream, 25, nil, func(v string) { cfg.DNSUpstream = v }).
		AddButton("Save", func() {
			if err := saveMasterConfig(cfg); err != nil {
				showMessage("Error", err.Error())
			} else {
				showMessage("Saved", masterCfgPath+" written successfully")
			}
		}).
		AddButton("Cancel", func() { goPage("master_menu") })

	form.SetBorder(true).SetTitle(" Edit Master Config ").SetBorderColor(tcell.ColorDarkCyan)
	addPage("master_form", flex100(form), form)
	goPage("master_form")
}

func saveMasterConfig(cfg *config.MasterConfig) error {
	os.MkdirAll(filepath.Dir(masterCfgPath), 0750)
	// 0600: the config holds the auth token.
	f, err := os.OpenFile(masterCfgPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

// ── Slave ──────────────────────────────────────────────────────────────────

func showSlaveMenu() {
	menu := tview.NewList().
		AddItem("Edit Config", "Edit slave.json", 'e', func() { showSlaveConfigForm() }).
		AddItem("Start Service", "systemctl start mini-tun-asymmetric-slave", 's', func() { runSystemctl("start", "mini-tun-asymmetric-slave") }).
		AddItem("Stop Service", "systemctl stop mini-tun-asymmetric-slave", 'x', func() { runSystemctl("stop", "mini-tun-asymmetric-slave") }).
		AddItem("Restart Service", "systemctl restart mini-tun-asymmetric-slave", 'r', func() { runSystemctl("restart", "mini-tun-asymmetric-slave") }).
		AddItem("Enable on Boot", "systemctl enable mini-tun-asymmetric-slave", 'b', func() { runSystemctl("enable", "mini-tun-asymmetric-slave") }).
		AddItem("View Logs", "journalctl -u mini-tun-asymmetric-slave -n 50", 'l', func() { showLogs("mini-tun-asymmetric-slave") }).
		AddItem("← Back", "Return to the main menu (or press Esc)", 'k', func() { goPage("main") })

	menu.SetBorder(true).SetTitle(" Slave Node ").SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorDarkCyan)
	menu.SetSelectedBackgroundColor(tcell.ColorDarkCyan)
	menu.SetInputCapture(escToPage("main"))
	addPage("slave_menu", centered(menu, 62, 18), menu)
	goPage("slave_menu")
}

func showSlaveConfigForm() {
	cfg := config.DefaultSlaveConfig()
	if data, err := os.ReadFile(slaveCfgPath); err == nil {
		json.Unmarshal(data, cfg)
	}

	form := tview.NewForm()
	form.AddInputField("Master Control (IP:Port)", cfg.MasterControl, 30, nil, func(v string) { cfg.MasterControl = v }).
		AddInputField("Listen UDP (downlink to clients)", cfg.ListenUDP, 25, nil, func(v string) { cfg.ListenUDP = v }).
		AddInputField("Listen Data Plane (from master)", cfg.ListenDataPlane, 25, nil, func(v string) { cfg.ListenDataPlane = v }).
		AddPasswordField("Auth Token (base64)", cfg.AuthToken, 40, '*', func(v string) { cfg.AuthToken = v }).
		AddInputField("Slave ID", cfg.SlaveID, 16, nil, func(v string) { cfg.SlaveID = v }).
		AddInputField("TLS CA Cert (ca.crt / empty=skip)", cfg.TLSCACertFile, 50, nil, func(v string) { cfg.TLSCACertFile = v }).
		AddButton("Save", func() {
			if err := saveSlaveConfig(cfg); err != nil {
				showMessage("Error", err.Error())
			} else {
				showMessage("Saved", slaveCfgPath+" written successfully")
			}
		}).
		AddButton("Cancel", func() { goPage("slave_menu") })

	form.SetBorder(true).SetTitle(" Edit Slave Config ").SetBorderColor(tcell.ColorDarkCyan)
	addPage("slave_form", flex100(form), form)
	goPage("slave_form")
}

func saveSlaveConfig(cfg *config.SlaveConfig) error {
	os.MkdirAll(filepath.Dir(slaveCfgPath), 0750)
	// 0600: the config holds the auth token.
	f, err := os.OpenFile(slaveCfgPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

// ── Status ─────────────────────────────────────────────────────────────────

func showStatus() {
	masterStatus := systemctlStatus("mini-tun-asymmetric-master")
	slaveStatus := systemctlStatus("mini-tun-asymmetric-slave")

	tv := tview.NewTextView().SetDynamicColors(true)
	var b strings.Builder

	// Master role.
	fmt.Fprintf(&b, "  [yellow]Master service[-] (mini-tun-asymmetric-master)\n  %s\n",
		colorStatus(masterStatus))
	if masterStatus == "active" {
		// Config summary: transport(s) the master mimics + control ports.
		if carriers := masterCarriers(); carriers != "" {
			fmt.Fprintf(&b, "  [yellow]mimicking:[-] %s\n", carriers)
		}
		sessCreated, sessActive := masterSessions()
		fmt.Fprintf(&b, "  [yellow]sessions:[-] %d active, %d total since start\n",
			sessActive, sessCreated)
		slaves := masterConnectedSlaves()
		if slaves == "" {
			b.WriteString("  [gray]no slaves connected yet[-]\n")
		} else {
			b.WriteString("  [yellow]connected slaves:[-]\n" + slaves)
		}
	}
	b.WriteString("\n")

	// Slave role.
	fmt.Fprintf(&b, "  [yellow]Slave service[-]  (mini-tun-asymmetric-slave)\n  %s\n",
		colorStatus(slaveStatus))
	if slaveStatus == "active" {
		if master, connected := slaveLinkStatus(); connected {
			fmt.Fprintf(&b, "  [green]● connected to master %s[-]\n", master)
		} else {
			fmt.Fprintf(&b, "  [yellow]target master %s — check logs if traffic fails[-]\n", master)
		}
	}

	// Explain the common confusion: each host normally runs ONE role, so the
	// other role showing "unknown" is expected, not a fault.
	if masterStatus != "active" || slaveStatus != "active" {
		b.WriteString("\n  [gray]\"unknown\" = that role isn't installed on this host (normal:\n" +
			"  a host is either a master or a slave, not both).[-]\n")
	}

	tv.SetText("\n" + b.String())
	tv.SetBorder(true).SetTitle(" Service Status ").SetBorderColor(tcell.ColorDarkCyan)
	tv.SetScrollable(true)

	back := tview.NewButton("[ Back ]").SetSelectedFunc(func() { goPage("main") })

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(back, 3, 0, true)
	flex.SetBorder(true).SetTitle(" Mini-Tun Asymmetric Server Manager ").SetBorderColor(tcell.ColorDarkCyan)
	flex.SetInputCapture(escToPage("main"))

	addPage("status", centered(flex, 68, 20), back)
	goPage("status")
}

// masterCarriers reads master.json and returns a human summary of the transport
// carriers + ports the master listens on (what it mimics).
func masterCarriers() string {
	data, err := os.ReadFile(masterCfgPath)
	if err != nil {
		return ""
	}
	cfg := config.DefaultMasterConfig()
	if json.Unmarshal(data, cfg) != nil {
		return ""
	}
	if len(cfg.ControlPorts) > 0 {
		parts := make([]string, 0, len(cfg.ControlPorts))
		for _, cp := range cfg.ControlPorts {
			parts = append(parts, fmt.Sprintf("%s:%d", cp.Transport, cp.Port))
		}
		return strings.Join(parts, ", ")
	}
	tr := cfg.Transport
	if tr == "" {
		tr = "cs2"
	}
	return fmt.Sprintf("%s (%s)", tr, cfg.ListenUDP)
}

// masterSessions reads sessions_created + sessions_active from /metrics.
func masterSessions() (created, active int64) {
	listen := "127.0.0.1:9090"
	if data, err := os.ReadFile(masterCfgPath); err == nil {
		cfg := config.DefaultMasterConfig()
		if json.Unmarshal(data, cfg) == nil && cfg.MetricsListen != "" {
			listen = cfg.MetricsListen
		}
	}
	out, err := exec.Command("curl", "-s", "--max-time", "3", "http://"+listen+"/metrics").Output()
	if err != nil {
		return 0, 0
	}
	var snap struct {
		SessionsCreated int64 `json:"sessions_created"`
		SessionsActive  int64 `json:"sessions_active"`
	}
	if json.Unmarshal(out, &snap) != nil {
		return 0, 0
	}
	return snap.SessionsCreated, snap.SessionsActive
}

// masterConnectedSlaves queries the master's local /metrics endpoint and returns
// a formatted list of connected slaves. Empty string if none / unreachable.
func masterConnectedSlaves() string {
	listen := "127.0.0.1:9090"
	if data, err := os.ReadFile(masterCfgPath); err == nil {
		cfg := config.DefaultMasterConfig()
		if json.Unmarshal(data, cfg) == nil && cfg.MetricsListen != "" {
			listen = cfg.MetricsListen
		}
	}
	out, err := exec.Command("curl", "-s", "--max-time", "3", "http://"+listen+"/metrics").Output()
	if err != nil {
		return ""
	}
	var snap struct {
		Slaves []struct {
			SlaveID     string `json:"slave_id"`
			Connected   bool   `json:"connected"`
			DataPlaneUp bool   `json:"data_plane_up"`
			LastSeenMS  int64  `json:"last_seen_ms"`
		} `json:"slaves"`
	}
	if json.Unmarshal(out, &snap) != nil {
		return ""
	}
	var sb strings.Builder
	for _, s := range snap.Slaves {
		mark := "[green]●[-]"
		if !s.Connected {
			mark = "[red]✗[-]"
		}
		dp := "data-plane up"
		if !s.DataPlaneUp {
			dp = "[yellow]data-plane down[-]"
		}
		fmt.Fprintf(&sb, "    %s %s — %s (last seen %ds ago)\n",
			mark, s.SlaveID, dp, s.LastSeenMS/1000)
	}
	return sb.String()
}

// slaveLinkStatus returns the slave's target master address and whether its
// control connection is currently up (per the most recent journal line).
func slaveLinkStatus() (string, bool) {
	master := "?"
	if data, err := os.ReadFile(slaveCfgPath); err == nil {
		cfg := config.DefaultSlaveConfig()
		if json.Unmarshal(data, cfg) == nil {
			master = cfg.MasterControl
		}
	}
	// "control connected to master" is logged on connect; a later disconnect
	// logs an error. Check the last relevant line.
	out, _ := exec.Command("journalctl", "-u", "mini-tun-asymmetric-slave",
		"-n", "50", "--no-pager").Output()
	connected := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "control connected to master") {
			connected = true
		} else if strings.Contains(line, "control error") ||
			strings.Contains(line, "connecting to master control") {
			connected = false
		}
	}
	return master, connected
}

func systemctlStatus(svc string) string {
	out, err := exec.Command("systemctl", "is-active", svc).Output()
	s := strings.TrimSpace(string(out))
	if err != nil || s == "" {
		return "unknown"
	}
	return s
}

func colorStatus(s string) string {
	switch s {
	case "active":
		return "[green]● active[-]"
	case "inactive":
		return "[gray]○ inactive[-]"
	case "failed":
		return "[red]✗ failed[-]"
	}
	return "[yellow]" + s + "[-]"
}

// ── TLS generation ─────────────────────────────────────────────────────────

func showGenTLS() {
	outDir := "/etc/mini-tun-asymmetric/tls"
	cnEntry := tview.NewInputField().SetLabel("Common Name (domain/IP): ").SetText("mini-tun-asymmetric-server").SetFieldWidth(40)
	outEntry := tview.NewInputField().SetLabel("Output directory:        ").SetText(outDir).SetFieldWidth(40)

	form := tview.NewForm()
	form.AddFormItem(cnEntry).
		AddFormItem(outEntry).
		AddButton("Generate", func() {
			cn := cnEntry.GetText()
			dir := outEntry.GetText()
			os.MkdirAll(dir, 0700)
			keyFile := filepath.Join(dir, "privkey.pem")
			certFile := filepath.Join(dir, "fullchain.pem")
			cmd := exec.Command("openssl", "req", "-x509",
				"-newkey", "rsa:4096",
				"-keyout", keyFile,
				"-out", certFile,
				"-days", "3650",
				"-nodes",
				"-subj", "/CN="+cn,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				showMessage("Error", string(out)+"\n"+err.Error())
			} else {
				showMessage("Done",
					fmt.Sprintf("Certificate written:\n  %s\n  %s\n\nUse these paths in your config.", certFile, keyFile))
			}
		}).
		AddButton("Cancel", func() { goPage("main") })

	form.SetBorder(true).SetTitle(" Generate Self-Signed TLS ").SetBorderColor(tcell.ColorDarkCyan)
	addPage("gentls", centered(form, 70, 12), form)
	goPage("gentls")
}

// ── Logs ───────────────────────────────────────────────────────────────────

func showLogs(svc string) {
	out, err := exec.Command("journalctl", "-u", svc, "-n", "60", "--no-pager").Output()
	text := string(out)
	if err != nil {
		text = "journalctl not available or service not found.\n" + err.Error()
	}

	tv := tview.NewTextView().SetText(text).SetScrollable(true)
	tv.SetBorder(true).SetTitle(fmt.Sprintf(" Logs: %s ", svc)).SetBorderColor(tcell.ColorDarkCyan)
	tv.ScrollToEnd()

	back := tview.NewButton("[ Back ]").SetSelectedFunc(func() { goPage("main") })
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, true).
		AddItem(back, 3, 0, false)

	addPage("logs", flex, back)
	goPage("logs")
}

// ── Systemctl helper ───────────────────────────────────────────────────────

func runSystemctl(action, svc string) {
	out, err := exec.Command("systemctl", action, svc).CombinedOutput()
	msg := fmt.Sprintf("systemctl %s %s\n\n", action, svc)
	if err != nil {
		msg += "[red]Error:[-] " + string(out) + "\n" + err.Error()
	} else {
		msg += "[green]Success[-]"
		if len(out) > 0 {
			msg += "\n" + string(out)
		}
	}
	showMessage("Result", msg)
}

// ── Helpers ────────────────────────────────────────────────────────────────

func showMessage(title, msg string) {
	tv := tview.NewTextView().SetDynamicColors(true).SetText("\n  " + strings.ReplaceAll(msg, "\n", "\n  "))
	tv.SetBorder(true).SetTitle(" "+title+" ").SetBorderColor(tcell.ColorDarkCyan)
	btn := tview.NewButton("[ OK ]").SetSelectedFunc(func() { goPage("main") })
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(btn, 3, 0, true)
	addPage("msg", centered(flex, 70, 14), btn)
	goPage("msg")
}

// focusReg maps a page name to the primitive that should receive keyboard focus
// when shown. tview's SwitchToPage does NOT move focus to the widget inside a
// page, so arrow keys were dead until the user clicked with the mouse (a blocker
// over VNC / keyboard-only). goPage switches the page AND focuses its widget.
var focusReg = map[string]tview.Primitive{}

// goPage shows a page and moves keyboard focus to its registered widget.
func goPage(page string) {
	pages.SwitchToPage(page)
	if p, ok := focusReg[page]; ok && p != nil {
		app.SetFocus(p)
	}
}

// addPage registers a page and the widget that should get keyboard focus.
func addPage(name string, root tview.Primitive, focus tview.Primitive) {
	pages.AddPage(name, root, true, true)
	focusReg[name] = focus
}

// escToPage returns an input handler that switches to the given page when Esc is
// pressed, so every submenu is escapable without Ctrl+C. Other keys pass through.
func escToPage(page string) func(*tcell.EventKey) *tcell.EventKey {
	return func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			goPage(page)
			return nil
		}
		return e
	}
}

func centered(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 0, true).
			AddItem(nil, 0, 1, false), width, 0, true).
		AddItem(nil, 0, 1, false)
}

func flex100(p tview.Primitive) tview.Primitive {
	return tview.NewFlex().AddItem(p, 0, 1, true)
}
