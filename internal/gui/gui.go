// Package gui shows the web UI in a native GTK window with WebKitGTK. There
// is no web server: JavaScript posts messages to Go through a WebKit script
// message handler, and Go answers by evaluating JavaScript in the page.
package gui

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
#cgo CFLAGS: -Wno-deprecated-declarations
#include <stdlib.h>
#include "webview.h"
*/
import "C"

import (
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

func init() {
	// GTK must stay on the thread that initialised it: the main thread.
	runtime.LockOSThread()
}

var (
	onMessage  func(string)
	dispatchMu sync.Mutex
	dispatchID uintptr
	dispatched = map[uintptr]func(){}
)

type Options struct {
	AppID     string // Wayland app_id / X11 WM_CLASS, matching the .desktop file
	Icon      []byte // SVG or PNG
	Title     string
	Width     int
	Height    int
	HTML      string
	BridgeJS  string       // injected before the page's own scripts
	DevTools  bool         // right-click → Inspect
	Theme     string       // "dark" or "light"
	OnMessage func(string) // called on the main thread for each postMessage
}

func setTheme(theme string) {
	if theme == "light" {
		C.vh_set_theme(0)
	} else {
		C.vh_set_theme(1)
	}
}

// SetTheme sets the window's colour scheme ("dark" or "light"). Safe to call
// from any goroutine.
func SetTheme(theme string) { Dispatch(func() { setTheme(theme) }) }

// Run opens the window and blocks until it is closed. Call from main().
func Run(o Options) {
	onMessage = o.OnMessage
	appID, title, bridge := C.CString(o.AppID), C.CString(o.Title), C.CString(o.BridgeJS)
	defer C.free(unsafe.Pointer(appID))
	defer C.free(unsafe.Pointer(title))
	defer C.free(unsafe.Pointer(bridge))
	var icon unsafe.Pointer
	if len(o.Icon) > 0 {
		icon = C.CBytes(o.Icon)
		defer C.free(icon)
	}
	dev := C.int(0)
	if o.DevTools {
		dev = 1
	}
	setTheme(o.Theme)
	C.vh_init(appID, title, C.int(o.Width), C.int(o.Height), bridge, dev, icon, C.int(len(o.Icon)))

	theme := "dark"
	if o.Theme == "light" {
		theme = "light"
	}
	page := strings.Replace(o.HTML, "<html ", `<html data-theme="`+theme+`" `, 1)
	html, base := C.CString(page), C.CString("http://videohub-control.localhost/")
	C.vh_load_html(html, base)
	C.free(unsafe.Pointer(html))
	C.free(unsafe.Pointer(base))

	C.vh_run()
}

// Quit closes the window. Safe from any goroutine.
func Quit() { C.vh_quit() }

// Dispatch runs f on the GTK main thread. Safe from any goroutine.
func Dispatch(f func()) {
	dispatchMu.Lock()
	dispatchID++
	id := dispatchID
	dispatched[id] = f
	dispatchMu.Unlock()
	C.vh_dispatch(C.uintptr_t(id))
}

// Eval runs JavaScript in the page. Safe from any goroutine.
func Eval(js string) {
	Dispatch(func() {
		cs := C.CString(js)
		C.vh_eval(cs)
		C.free(unsafe.Pointer(cs))
	})
}

// SaveDialog asks for a destination file. Must run on the main thread
// (inside Dispatch). Returns "" if cancelled.
func SaveDialog(suggested string) string {
	cs := C.CString(suggested)
	defer C.free(unsafe.Pointer(cs))
	p := C.vh_save_dialog(cs)
	if p == nil {
		return ""
	}
	defer C.vh_free(unsafe.Pointer(p))
	return C.GoString(p)
}

//export goOnMessage
func goOnMessage(msg *C.char) {
	if onMessage != nil {
		onMessage(C.GoString(msg))
	}
}

//export goDispatch
func goDispatch(id C.uintptr_t) {
	dispatchMu.Lock()
	f := dispatched[uintptr(id)]
	delete(dispatched, uintptr(id))
	dispatchMu.Unlock()
	if f != nil {
		f()
	}
}
