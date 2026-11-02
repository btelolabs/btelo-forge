package mac

/*
#cgo LDFLAGS: -framework WebKit
#include <stdlib.h>
#include "webview.h"
*/
import "C"

import (
	"image"
	"strings"
	"unsafe"
)

// WebView shows a page of a stranger inside the window, with the WKWebView that comes with macOS. The page has no
// scripts (the view's own switch and its Content-Security-Policy), a content rule refuses everything it asks of the
// network but pictures, once allowed, and it cannot go anywhere: a link clicked is handed to Links.
type WebView struct {
	window *Window
	shown  bool
	bounds image.Rectangle
	holes  []image.Rectangle
}

var (
	webWake  func()
	webLinks []string
	webTouch bool
)

//export goWebLink
func goWebLink(uri *C.char) {
	if s := C.GoString(uri); strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "mailto:") {
		webLinks = append(webLinks, s)
		webWake()
	}
}

//export goWebTouched
func goWebTouched() {
	webTouch = true
	webWake()
}

// OpenWebView makes the view; wake is called when Links or Touched may have news.
func (w *Window) OpenWebView(wake func()) *WebView {
	webWake = wake
	C.forge_web_open()
	return &WebView{window: w}
}

// Show loads a page; its policy is in the page itself, and pictures lets it fetch images. fit is run in the page by the
// view, when it has loaded and when the view's width changes.
func (v *WebView) Show(page, _, fit string, pictures bool) {
	p, script := C.CString(page), C.CString(fit)
	defer C.free(unsafe.Pointer(p))
	defer C.free(unsafe.Pointer(script))
	allow := C.int(0)
	if pictures {
		allow = 1
	}
	C.forge_web_show(p, script, allow)
}

// Place shows the view over r of the window, in pixels, with holes cut out of it for what is drawn over it.
func (v *WebView) Place(r image.Rectangle, holes []image.Rectangle) {
	v.shown, v.bounds, v.holes = true, r, holes
	flat := make([]C.double, 0, len(holes)*4)
	for _, h := range holes {
		flat = append(flat, C.double(h.Min.X-r.Min.X), C.double(h.Min.Y-r.Min.Y), C.double(h.Dx()), C.double(h.Dy()))
	}
	var first *C.double
	if len(flat) > 0 {
		first = &flat[0]
	}
	C.forge_web_place(C.double(r.Min.X), C.double(r.Min.Y), C.double(r.Dx()), C.double(r.Dy()), first, C.int(len(holes)))
}

// Hide takes the view off the window; its page stays loaded.
func (v *WebView) Hide() {
	if v.shown {
		v.shown = false
		C.forge_web_hide()
	}
}

// Links gives the links clicked since it was last asked.
func (v *WebView) Links() []string {
	out := webLinks
	webLinks = nil
	return out
}

// Touched reports whether the reader went into the view since it was last asked.
func (v *WebView) Touched() bool {
	t := webTouch
	webTouch = false
	return t
}

// Err is always nil: WKWebView is part of the system, and starts its pages again itself.
func (v *WebView) Err() error { return nil }

// Close ends the view.
func (v *WebView) Close() { C.forge_web_close() }
