//go:build windows

// Frameless-window plumbing for the custom (HTML) title bar.
//
// go-webview2 always creates its host window with WS_OVERLAPPEDWINDOW, i.e. the
// standard white Windows caption + border. We don't want that: the UI draws its
// own gradient title bar that continues the active theme. So after the window
// exists we strip the caption/frame, and we expose four JS-callable functions
// (winDrag/winMin/winMax/winClose) that the HTML title bar buttons invoke.
package main

import (
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	user32c           = windows.NewLazySystemDLL("user32.dll")
	pSetWindowLongPtr = user32c.NewProc("SetWindowLongPtrW")
	pSetWindowPos     = user32c.NewProc("SetWindowPos")
	pGetWindowRect    = user32c.NewProc("GetWindowRect")
	pShowWindow       = user32c.NewProc("ShowWindow")
	pPostMessage      = user32c.NewProc("PostMessageW")
	pSendMessage      = user32c.NewProc("SendMessageW")
	pReleaseCapture   = user32c.NewProc("ReleaseCapture")
	pSysParamsInfo    = user32c.NewProc("SystemParametersInfoW")
)

// GWL_STYLE is -16; as a uintptr that is ^15 in two's complement.
const gwlStyle = ^uintptr(15)

const (
	wsPopup       = 0x80000000
	wsThickFrame  = 0x00040000 // keep -> window stays resizable
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000
	wsClipChild   = 0x02000000
	wsVisible     = 0x10000000

	swpNoMove      = 0x0002
	swpNoSize      = 0x0001
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010
	swpFrameChange = 0x0020

	wmClose         = 0x0010
	wmNCLButtonDown = 0x00A1
	htCaption       = 2

	swMinimize = 6
	swRestore  = 9

	spiGetWorkArea = 0x0030
)

type rect struct{ Left, Top, Right, Bottom int32 }

var (
	maximized bool
	savedRect rect
)

// makeFrameless strips the native caption AND the resize border so only the
// HTML title bar shows. We deliberately drop WS_THICKFRAME: on Windows 11 it
// renders a thick top resize-edge that peeks as a white strip above our
// gradient bar. This is a fixed-size panel window (472x788) where edge-resize
// isn't needed; minimize/maximize/drag don't depend on the frame.
func makeFrameless(w webview2.WebView) {
	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		return
	}
	style := uintptr(wsPopup | wsMinimizeBox | wsMaximizeBox | wsClipChild | wsVisible)
	pSetWindowLongPtr.Call(hwnd, gwlStyle, style)
	// Force a non-client recalc so the old frame is dropped immediately.
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
		uintptr(swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChange))
	// Dropping the caption enlarges the client area, but the embedded browser
	// child only refills on WM_SIZE — which the call above does not raise (the
	// outer size is unchanged). Nudge the size by 1px and back to trigger it.
	var r rect
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	w0, h0 := r.Right-r.Left, r.Bottom-r.Top
	pSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(w0+1), uintptr(h0+1),
		uintptr(swpNoMove|swpNoZOrder|swpNoActivate))
	pSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(w0), uintptr(h0),
		uintptr(swpNoMove|swpNoZOrder|swpNoActivate))
}

// bindWindowControls exposes the title-bar actions to JavaScript. Each handler
// hops onto the UI thread via Dispatch before touching the window.
func bindWindowControls(w webview2.WebView) {
	hwnd := uintptr(w.Window())

	// Drag the frameless window: release the implicit capture from the
	// mouse-down, then ask Windows to run its standard caption-drag loop.
	w.Bind("winDrag", func() {
		w.Dispatch(func() {
			pReleaseCapture.Call()
			pSendMessage.Call(hwnd, wmNCLButtonDown, htCaption, 0)
		})
	})

	w.Bind("winMin", func() {
		w.Dispatch(func() { pShowWindow.Call(hwnd, swMinimize) })
	})

	// Toggle between the work area (taskbar-aware "maximize") and the previous
	// size. We do it by hand instead of SW_MAXIMIZE because a borderless popup
	// would otherwise cover the taskbar.
	w.Bind("winMax", func() {
		w.Dispatch(func() {
			if maximized {
				pSetWindowPos.Call(hwnd, 0,
					uintptr(savedRect.Left), uintptr(savedRect.Top),
					uintptr(savedRect.Right-savedRect.Left), uintptr(savedRect.Bottom-savedRect.Top),
					uintptr(swpNoZOrder|swpNoActivate|swpFrameChange))
				maximized = false
				return
			}
			pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&savedRect)))
			var wa rect
			pSysParamsInfo.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
			pSetWindowPos.Call(hwnd, 0,
				uintptr(wa.Left), uintptr(wa.Top),
				uintptr(wa.Right-wa.Left), uintptr(wa.Bottom-wa.Top),
				uintptr(swpNoZOrder|swpNoActivate|swpFrameChange))
			maximized = true
		})
	})

	w.Bind("winClose", func() {
		w.Dispatch(func() { pPostMessage.Call(hwnd, wmClose, 0, 0) })
	})

	_ = swRestore // reserved if we switch maximize strategy later
}
