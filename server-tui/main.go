// server-tui is an nmtui-style TUI for configuring and managing Mini-Tun Asymmetric Master/Slave nodes.
package main

import (
	"encoding/json"
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

func main() {
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
		AddItem("Master Node", "Configure and manage the Master node", 'm', func() { showMasterMenu() }).
		AddItem("Slave Node", "Configure and manage the Slave node", 's', func() { showSlaveMenu() }).
		AddItem("Status", "View running service status", 't', func() { showStatus() }).
		AddItem("Generate TLS Certs", "Create self-signed TLS certificate pair", 'g', func() { showGenTLS() }).
		AddItem("Quit", "Exit Mini-Tun Asymmetric Server Manager", 'q', func() { app.Stop() })

	menu.SetBorder(true).
		SetTitle(" Mini-Tun Asymmetric Server Manager ").
		SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorDarkCyan)
	menu.SetSelectedBackgroundColor(tcell.ColorDarkCyan)

	pages.AddPage("main", centered(menu, 60, 12), true, true)
	pages.SwitchToPage("main")

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
		AddItem("← Back", "", 'b', func() { pages.SwitchToPage("main") })

	menu.SetBorder(true).SetTitle(" Master Node ").SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorDarkCyan)
	menu.SetSelectedBackgroundColor(tcell.ColorDarkCyan)
	pages.AddPage("master_menu", centered(menu, 60, 14), true, true)
	pages.SwitchToPage("master_menu")
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
		AddButton("Cancel", func() { pages.SwitchToPage("master_menu") })

	form.SetBorder(true).SetTitle(" Edit Master Config ").SetBorderColor(tcell.ColorDarkCyan)
	pages.AddPage("master_form", flex100(form), true, true)
	pages.SwitchToPage("master_form")
}

func saveMasterConfig(cfg *config.MasterConfig) error {
	os.MkdirAll(filepath.Dir(masterCfgPath), 0750)
	f, err := os.Create(masterCfgPath)
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
		AddItem("← Back", "", 0, func() { pages.SwitchToPage("main") })

	menu.SetBorder(true).SetTitle(" Slave Node ").SetTitleAlign(tview.AlignCenter).
		SetBorderColor(tcell.ColorDarkCyan)
	menu.SetSelectedBackgroundColor(tcell.ColorDarkCyan)
	pages.AddPage("slave_menu", centered(menu, 60, 14), true, true)
	pages.SwitchToPage("slave_menu")
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
		AddButton("Cancel", func() { pages.SwitchToPage("slave_menu") })

	form.SetBorder(true).SetTitle(" Edit Slave Config ").SetBorderColor(tcell.ColorDarkCyan)
	pages.AddPage("slave_form", flex100(form), true, true)
	pages.SwitchToPage("slave_form")
}

func saveSlaveConfig(cfg *config.SlaveConfig) error {
	os.MkdirAll(filepath.Dir(slaveCfgPath), 0750)
	f, err := os.Create(slaveCfgPath)
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
	fmt.Fprintf(tv,
		"\n  [yellow]Master Node[-] (mini-tun-asymmetric-master)\n  %s\n\n"+
			"  [yellow]Slave Node[-]  (mini-tun-asymmetric-slave)\n  %s\n",
		colorStatus(masterStatus), colorStatus(slaveStatus))

	tv.SetBorder(true).SetTitle(" Service Status ").SetBorderColor(tcell.ColorDarkCyan)

	back := tview.NewButton("[ Back ]").SetSelectedFunc(func() { pages.SwitchToPage("main") })

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(back, 3, 0, true)
	flex.SetBorder(true).SetTitle(" Mini-Tun Asymmetric Server Manager ").SetBorderColor(tcell.ColorDarkCyan)

	pages.AddPage("status", centered(flex, 64, 14), true, true)
	pages.SwitchToPage("status")
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
		AddButton("Cancel", func() { pages.SwitchToPage("main") })

	form.SetBorder(true).SetTitle(" Generate Self-Signed TLS ").SetBorderColor(tcell.ColorDarkCyan)
	pages.AddPage("gentls", centered(form, 70, 12), true, true)
	pages.SwitchToPage("gentls")
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

	back := tview.NewButton("[ Back ]").SetSelectedFunc(func() { pages.SwitchToPage("main") })
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, true).
		AddItem(back, 3, 0, false)

	pages.AddPage("logs", flex, true, true)
	pages.SwitchToPage("logs")
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
	btn := tview.NewButton("[ OK ]").SetSelectedFunc(func() { pages.SwitchToPage("main") })
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(btn, 3, 0, true)
	pages.AddPage("msg", centered(flex, 70, 14), true, true)
	pages.SwitchToPage("msg")
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
