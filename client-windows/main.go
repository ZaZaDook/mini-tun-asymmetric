// Mini-Tun Asymmetric — desktop VPN client.
// A native WebView2 window (no browser chrome) backed by a local dashboard
// server, plus a system-tray icon. Requires Administrator rights to manage the
// TUN adapter and routing table (self-elevates via UAC on launch).
package main

import (
	_ "embed"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/getlantern/systray"
	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"github.com/ZaZaDook/mini-tun-asymmetric/client-windows/ui"
	"github.com/ZaZaDook/mini-tun-asymmetric/client-windows/vpncore"
	"github.com/ZaZaDook/mini-tun-asymmetric/common/config"
)

const appName = "Mini-Tun Asymmetric"

//go:embed assets/icon.ico
var iconICO []byte

var (
	webURL  string
	engine  *vpncore.Engine
	cfg     *config.ClientConfig
	cfgPath string
)

func main() {
	// TUN + routing need Administrator. Relaunch with a UAC prompt if needed.
	if runtime.GOOS == "windows" && !isAdmin() {
		relaunchElevated()
		return
	}

	dataDir := appDataDir()
	os.MkdirAll(dataDir, 0700)
	// WebView2 keeps its profile here (writable even when running elevated).
	os.Setenv("WEBVIEW2_USER_DATA_FOLDER", filepath.Join(dataDir, "webview"))
	// Disable Chromium's autofill/password-save machinery at the engine level so
	// the input fields don't show browser-style saved-value dropdowns (looks
	// unprofessional for a desktop app). HTML autocomplete="off" alone doesn't
	// fully suppress general autofill in WebView2; these flags do. WebView2 reads
	// this env var as additional browser arguments on startup.
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS",
		"--disable-features=msAutofill,AutofillEnableAccountWalletStorage,PasswordManagerEnabled,AutofillServerCommunication,Translate")
	cfgPath = filepath.Join(dataDir, "config.json")
	setupLogging(filepath.Join(dataDir, "client.log"))

	var err error
	cfg, err = config.LoadClientConfig(cfgPath)
	if err != nil {
		cfg = &config.ClientConfig{}
	}

	engine = vpncore.NewEngine(func(state vpncore.State) {
		setTrayState(state == vpncore.StateConnected)
	})

	srv := ui.NewServer(engine, cfg, cfgPath)
	webURL, err = srv.Start()
	if err != nil {
		log.Fatalf("failed to start web UI: %v", err)
	}
	log.Printf("[%s] UI at %s", appName, webURL)

	// Tray runs on its own OS thread; the window is the primary UI.
	go runTray()

	// Native window on the main thread (blocks until the window is closed).
	runWindow(webURL)

	// Window closed -> shut down cleanly (restores routing/DNS).
	engine.Disconnect()
	systray.Quit()
	os.Exit(0)
}

func appDataDir() string {
	base, _ := os.UserConfigDir()
	return filepath.Join(base, "MiniTunAsymmetric")
}

func setupLogging(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags)
}

// runWindow opens the dashboard in a chromeless native WebView2 window.
func runWindow(url string) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  appName,
			Width:  472,
			Height: 788,
			Center: true,
		},
	})
	if w == nil {
		// WebView2 runtime not installed — fall back to the default browser and
		// keep the process alive for the tray.
		log.Printf("[%s] WebView2 unavailable, opening in browser", appName)
		openBrowser(url)
		select {}
	}
	defer w.Destroy()
	setWindowIcon(w.Window())
	makeFrameless(w)         // strip native white caption/border
	bindWindowControls(w)    // wire title-bar buttons to Win32 (before Navigate)
	w.Navigate(url)
	w.Run()
}

// ── Tray ────────────────────────────────────────────────────────────────────

func runTray() {
	runtime.LockOSThread()
	systray.Run(onTrayReady, func() {})
}

func onTrayReady() {
	systray.SetIcon(iconICO)
	systray.SetTitle("")
	systray.SetTooltip(appName + " — disconnected")

	mConnect := systray.AddMenuItem("Connect", "Connect to the active server")
	mDisconnect := systray.AddMenuItem("Disconnect", "Disconnect")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Exit "+appName)

	go func() {
		for {
			select {
			case <-mConnect.ClickedCh:
				connectActive()
			case <-mDisconnect.ClickedCh:
				engine.Disconnect()
			case <-mQuit.ClickedCh:
				engine.Disconnect()
				os.Exit(0)
			}
		}
	}()
}

func setTrayState(connected bool) {
	suffix := " — disconnected"
	if connected {
		suffix = " — connected"
	}
	systray.SetTooltip(appName + suffix)
}

// connectActive connects to the active profile (or the first one).
func connectActive() {
	if len(cfg.Profiles) == 0 {
		return
	}
	idx := 0
	for i, p := range cfg.Profiles {
		if p.Name == cfg.ActiveProfile {
			idx = i
			break
		}
	}
	p := cfg.Profiles[idx]
	engine.Transport = p.Transport
	engine.CustomPorts = p.CustomPorts
	if err := engine.Connect(p.MasterAddr, p.AuthToken); err != nil {
		log.Printf("[%s] connect: %v", appName, err)
	}
}

// ── Window icon (taskbar + title bar) via WM_SETICON ───────────────────────

func setWindowIcon(hwnd unsafe.Pointer) {
	if hwnd == nil {
		return
	}
	// LoadImageW needs a file path; write the embedded icon out once.
	iconPath := filepath.Join(appDataDir(), "icon.ico")
	if err := os.WriteFile(iconPath, iconICO, 0644); err != nil {
		return
	}
	pathPtr, err := syscall.UTF16PtrFromString(iconPath)
	if err != nil {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	loadImage := user32.NewProc("LoadImageW")
	sendMessage := user32.NewProc("SendMessageW")

	const imageIcon = 1
	const lrLoadFromFile = 0x00000010
	const lrDefaultSize = 0x00000040
	const wmSetIcon = 0x0080
	const iconSmall, iconBig = 0, 1

	big, _, _ := loadImage.Call(0, uintptr(unsafe.Pointer(pathPtr)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
	small, _, _ := loadImage.Call(0, uintptr(unsafe.Pointer(pathPtr)), imageIcon, 32, 32, lrLoadFromFile)
	if big != 0 {
		sendMessage.Call(uintptr(hwnd), wmSetIcon, iconBig, big)
	}
	if small != 0 {
		sendMessage.Call(uintptr(hwnd), wmSetIcon, iconSmall, small)
	}
}

// openBrowser is the fallback when WebView2 is unavailable.
func openBrowser(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
