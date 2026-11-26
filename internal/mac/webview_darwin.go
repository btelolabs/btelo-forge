package mac

/*
#cgo LDFLAGS: -framework WebKit
#include <stdlib.h>
#include "webview.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"slices"
	"strings"
	"unsafe"
)

// WebView shows a page of a stranger inside the window, with the WKWebView that comes with macOS. The page has no
// scripts (the view's own switch and its Content-Security-Policy), a content rule refuses everything it asks of the
// network but pictures, once allowed, and it cannot go anywhere: a link clicked is handed to Links.
type WebView struct {
	window   *Window
	shown    bool
	bounds   image.Rectangle
	holes    []image.Rectangle
	policy   string
	fit      string
	pictures bool
	unloaded bool
	loaded   bool
	navCount int
	err      error
}

var (
	currentWeb *WebView
	webWake    func()
	webLinks   []string
	webTouch   bool
)

func wakeWeb() {
	if webWake != nil {
		webWake()
	}
}

//export goWebLink
func goWebLink(uri *C.char) {
	if s := C.GoString(uri); strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "mailto:") {
		webLinks = append(webLinks, s)
		wakeWeb()
	}
}

//export goWebTouched
func goWebTouched() {
	webTouch = true
	wakeWeb()
}

//export goWebLoaded
func goWebLoaded() {
	if v := currentWeb; v != nil {
		v.loaded = true
		v.unloaded = false
	}
	wakeWeb()
}

//export goWebError
func goWebError(msg *C.char) {
	if v := currentWeb; v != nil {
		v.err = errors.New(C.GoString(msg))
	}
	wakeWeb()
}

// OpenWebView makes the view; wake is called when Links, Touched or Err may have news.
func (w *Window) OpenWebView(wake func()) *WebView {
	webWake = wake
	webLinks = nil
	webTouch = false
	v := &WebView{window: w}
	currentWeb = v
	C.forge_web_open()
	return v
}

// webSwapLimit is the largest page that goes through a fast DOM swap rather than a full navigation.
const webSwapLimit = 4 << 20

// webSwap replaces the document's root element in-place without triggering a full navigation flash, while keeping the
// existing page's CSP and content rule list intact.
const webSwap = `(function(s){var d=new DOMParser().parseFromString(s,'text/html');document.replaceChild(document.adoptNode(d.documentElement),document.documentElement);scrollTo(0,0)})(%s);`

// injectPolicy ensures a raw HTML page without a CSP meta tag still carries policy.
func injectPolicy(page, policy string) string {
	if policy == "" || strings.Contains(strings.ToLower(page), "content-security-policy") {
		return page
	}
	meta := `<meta http-equiv="Content-Security-Policy" content="` + html.EscapeString(policy) + `">`
	low := strings.ToLower(page)
	if strings.HasPrefix(low, "<!doctype ") {
		if end := strings.IndexByte(page, '>'); end >= 0 {
			return page[:end+1] + meta + page[end+1:]
		}
	}
	return "<!doctype html>" + meta + page
}

func (v *WebView) swap(page, policy, fit string, pictures bool) bool {
	if !v.loaded || v.unloaded || policy != v.policy || pictures != v.pictures || len(page) > webSwapLimit {
		return false
	}
	text, err := json.Marshal(page)
	if err != nil {
		return false
	}
	v.fit = fit
	script := fmt.Sprintf(webSwap, text) + fit
	cs := C.CString(script)
	defer C.free(unsafe.Pointer(cs))
	C.forge_web_eval(cs)
	return true
}

// Show loads a page; policy is its Content-Security-Policy, and pictures lets it fetch images. fit is run in the page
// by the view when it has loaded and when the view's width changes.
func (v *WebView) Show(page, policy, fit string, pictures bool) {
	if v.swap(page, policy, fit, pictures) {
		return
	}
	v.navCount++
	v.policy, v.fit, v.pictures, v.unloaded, v.loaded = policy, fit, pictures, true, false
	full := injectPolicy(page, policy)
	p, script := C.CString(full), C.CString(fit)
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
	if v.shown && r == v.bounds && slices.Equal(holes, v.holes) {
		return
	}
	v.shown, v.bounds, v.holes = true, r, slices.Clone(holes)
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

// Bounds is where the view was last placed.
func (v *WebView) Bounds() image.Rectangle { return v.bounds }

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

// Err is what stopped the view, if anything did.
func (v *WebView) Err() error { return v.err }

// title returns the loaded page's document title (used by tests).
func (v *WebView) title() string { return takeString(C.forge_web_title()) }

// Close ends the view.
func (v *WebView) Close() {
	if currentWeb == v {
		currentWeb = nil
	}
	webLinks = nil
	webTouch = false
	C.forge_web_close()
}
