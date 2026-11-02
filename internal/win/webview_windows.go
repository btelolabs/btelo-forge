package win

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unsafe"
)

// WebView shows a page of a stranger inside the window, with the WebView2 Runtime that comes with Windows; nothing of a
// browser ships with the program but Microsoft's small loader, which finds the Runtime.
//
// It is a child window, so it hides what the window draws beneath it, and what the window draws over it has to be cut out
// of it with the holes of Place.
//
// Pages come from memory, by way of a request filter that sees everything the page asks for: what is not the page itself
// or, once allowed, a picture is refused. Scripts are off, the page is in a sandbox, and it cannot go anywhere: a link
// clicked is handed to Links, and anything else that tries to navigate is stopped.
type WebView struct {
	parent     *Window
	host       uintptr
	env        *comObject
	controller *comObject
	view       *comObject
	wake       func()
	err        error

	url      string // where the page is said to be
	page     []byte
	policy   string
	fit      string // run in a page when it has loaded and when the view's width changes
	pictures bool
	serial   int
	unloaded bool    // a page waits for the view to be ready
	loaded   bool    // the page is there to run fit in
	ran      uintptr // the handler of what run runs
	going    bool    // the next navigation is the one load asked for

	shown   bool
	bounds  image.Rectangle
	holes   []image.Rectangle
	links   []string
	touched bool

	// Filled in by calls into WebView2: they live here, in memory that does not move while a call runs.
	outObject *comObject
	outText   *uint16
	outFlag   int32
	outKind   uint32
	token     int64
	box       rect
}

// webHost is the address pages are given, which no network has.
const webHost = "https://forge-mail.invalid/"

// Method numbers in the interfaces' tables, from WebView2.idl: each interface begins with IUnknown's three.
const (
	comAddRef  = 1
	comRelease = 2

	envCreateController = 3
	envCreateResponse   = 4

	ctrlPutVisible     = 4
	ctrlAddGotFocus    = 15
	ctrlAddAccelerator = 19
	ctrlNotifyMoved    = 23
	ctrlClose          = 24
	ctrlGetView        = 25

	viewDevTools            = 35
	viewGetSettings         = 3
	viewNavigate            = 5
	viewAddStarting         = 7
	viewAddCompleted        = 15
	viewAddProcessFailed    = 25
	viewExecuteScript       = 29
	viewStop                = 43
	viewAddNewWindow        = 44
	viewGetTitle            = 48
	viewAddResourceRequest  = 55
	viewAddResourceFilter   = 57
	setPutScript            = 4
	setPutWebMessage        = 6
	setPutDialogs           = 8
	setPutStatusBar         = 10
	setPutDevTools          = 12
	setPutContextMenus      = 14
	setPutHostObjects       = 16
	setPutZoom              = 18
	setPutErrorPage         = 20
	reqArgsGetRequest       = 3
	reqArgsPutResponse      = 5
	reqArgsGetContext       = 7
	requestGetURI           = 3
	startArgsGetURI         = 3
	startArgsGetByUser      = 4
	startArgsPutCancel      = 8
	windowArgsGetURI        = 3
	windowArgsPutHandled    = 6
	windowArgsGetByUser     = 8
	keyArgsGetKind          = 3
	keyArgsGetKey           = 4
	keyArgsGetParam         = 5
	keyArgsPutHandled       = 8
	failedArgsGetKind       = 3
	contextAll              = 0
	contextDocument         = 1
	contextImage            = 3
	failedBrowserExited     = 0
	keyDown, keyUp          = 0, 1
	keySystemDown, keySysUp = 2, 3
)

var (
	shlwapi       = syscall.NewLazyDLL("shlwapi.dll")
	memStream     = shlwapi.NewProc("SHCreateMemStream")
	setWindowRgn  = user32.NewProc("SetWindowRgn")
	createRectRgn = gdi32.NewProc("CreateRectRgn")
	combineRgn    = gdi32.NewProc("CombineRgn")
	getStock      = gdi32.NewProc("GetStockObject")
	isChild       = user32.NewProc("IsChild")
	setFocus      = user32.NewProc("SetFocus")
	webClass, _   = syscall.UTF16PtrFromString("BteloForgeWeb")
)

// comObject is a COM interface pointer: its first word points at the method table.
type comObject struct{ table *[64]uintptr }

// call invokes method i, whose first argument is the object itself, and returns the HRESULT.
func (o *comObject) call(i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.table[i], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

// comHandler is an object WebView2 calls back. All of them have one method, and two arguments after themselves: a
// result and an object, or a sender and the event's details.
type comHandler struct {
	table *[4]uintptr
	run   func(first uintptr, second *comObject) uintptr
}

var handlerTable = [4]uintptr{
	syscall.NewCallback(func(this *comHandler, _ uintptr, out **comHandler) uintptr {
		*out = this
		return 0
	}),
	syscall.NewCallback(func(*comHandler) uintptr { return 1 }),
	syscall.NewCallback(func(*comHandler) uintptr { return 1 }),
	syscall.NewCallback(func(this *comHandler, first uintptr, second *comObject) uintptr { return this.run(first, second) }),
}

// handlers keeps every handler for the life of the process: WebView2 may hold one past the view's end, and Go must not
// collect what it can still call.
var handlers []*comHandler

func handler(run func(first uintptr, second *comObject) uintptr) uintptr {
	h := &comHandler{table: &handlerTable, run: run}
	handlers = append(handlers, h)
	return uintptr(unsafe.Pointer(h))
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

func wide(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

func installLoader(dir string) (string, error) {
	sum := sha256.Sum256(webviewLoaderDLL)
	path := filepath.Join(dir, "WebView2Loader-"+hex.EncodeToString(sum[:6])+".dll")
	if st, err := os.Stat(path); err == nil && st.Size() == int64(len(webviewLoaderDLL)) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, webviewLoaderDLL, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// ErrNoWebView means the system has no WebView2 Runtime, which Windows 11 always has and Windows 10 gets with Edge.
var ErrNoWebView = errors.New("this system has no WebView2 Runtime")

// OpenWebView starts a view in the window; dir keeps its files. It is ready some time later, and what it is told until
// then waits. wake is called, on the window's thread, when Links, Touched or Err may have news.
func (w *Window) OpenWebView(dir string, wake func()) (*WebView, error) {
	const apartment, changedMode = 2, 0x80010106
	if hr, _, _ := coInitialize.Call(0, apartment); uint32(hr) == changedMode {
		return nil, errors.New("WebView2 needs a single-threaded apartment on the window's thread")
	}
	loader, err := installLoader(dir)
	if err != nil {
		return nil, err
	}
	create := syscall.NewLazyDLL(loader).NewProc("CreateCoreWebView2EnvironmentWithOptions")
	if err := create.Find(); err != nil {
		return nil, err
	}
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	white, _, _ := getStock.Call(0)
	class := &windowClass{Proc: defWindowProc.Addr(), Instance: instance, Background: white, Name: webClass}
	class.Size = uint32(unsafe.Sizeof(*class))
	registerClass.Call(uintptr(unsafe.Pointer(class))) // it fails only for being there, from a view before this one
	const styleChild, clipChildren, styleIndex = 0x40000000, 0x02000000, ^uintptr(15)
	style, _, _ := getWindowLong.Call(w.Handle, styleIndex)
	setWindowLong.Call(w.Handle, styleIndex, style|clipChildren) // Present must not draw over the child
	host, _, e := createWindow.Call(0, uintptr(unsafe.Pointer(webClass)), 0, styleChild|clipChildren, 0, 0, 0, 0, w.Handle, 0, instance, 0)
	if host == 0 {
		return nil, fmt.Errorf("CreateWindowExW: %w", e)
	}
	v := &WebView{parent: w, host: host, wake: wake}
	hr, _, _ := create.Call(0, uintptr(unsafe.Pointer(wide(filepath.Join(dir, "data")))), 0, handler(v.environmentReady))
	if failed(hr) {
		destroyWindow.Call(host)
		if uint32(hr)&0xffff == uint32(syscall.ERROR_FILE_NOT_FOUND) {
			return nil, ErrNoWebView
		}
		return nil, fmt.Errorf("CreateCoreWebView2Environment: 0x%08X", uint32(hr))
	}
	w.web = v
	return v, nil
}

func (v *WebView) fail(op string, hr uintptr) uintptr {
	v.err = fmt.Errorf("%s: 0x%08X", op, uint32(hr))
	v.wake()
	return 0
}

func (v *WebView) environmentReady(hr uintptr, env *comObject) uintptr {
	if failed(hr) {
		return v.fail("WebView2 environment", hr)
	}
	if v.host == 0 {
		return 0 // closed meanwhile
	}
	env.call(comAddRef)
	v.env = env
	if hr := env.call(envCreateController, v.host, handler(v.controllerReady)); failed(hr) {
		return v.fail("WebView2 controller", hr)
	}
	return 0
}

func (v *WebView) controllerReady(hr uintptr, controller *comObject) uintptr {
	if failed(hr) {
		return v.fail("WebView2 controller", hr)
	}
	if v.host == 0 {
		controller.call(ctrlClose)
		return 0
	}
	controller.call(comAddRef)
	v.controller = controller
	if hr := controller.call(ctrlGetView, uintptr(unsafe.Pointer(&v.outObject))); failed(hr) {
		return v.fail("WebView2 view", hr)
	}
	v.view = v.outObject
	if hr := v.lockDown(); failed(hr) {
		return v.fail("WebView2 settings", hr)
	}
	if hr := v.listen(); failed(hr) {
		return v.fail("WebView2 events", hr)
	}
	v.applyPlace()
	v.load()
	return 0
}

// lockDown turns off all a page could use to act. The scripts go first and a failure stops the view: a page is never
// shown with them on.
func (v *WebView) lockDown() uintptr {
	if hr := v.view.call(viewGetSettings, uintptr(unsafe.Pointer(&v.outObject))); failed(hr) {
		return hr
	}
	settings := v.outObject
	defer settings.call(comRelease)
	for _, s := range []struct {
		method int
		on     uintptr
	}{
		{setPutScript, 0}, {setPutWebMessage, 0}, {setPutDialogs, 0}, {setPutDevTools, 0}, {setPutContextMenus, 0},
		{setPutHostObjects, 0}, {setPutZoom, 0}, {setPutErrorPage, 0},
		{setPutStatusBar, 1}, // it shows where a link goes when the pointer is on it
	} {
		if hr := settings.call(s.method, s.on); failed(hr) {
			return hr
		}
	}
	return 0
}

func (v *WebView) listen() uintptr {
	all := wide("*")
	for _, call := range []func() uintptr{
		func() uintptr { return v.view.call(viewAddResourceFilter, uintptr(unsafe.Pointer(all)), contextAll) },
		func() uintptr { return v.subscribe(v.view, viewAddResourceRequest, v.request) },
		func() uintptr { return v.subscribe(v.view, viewAddStarting, v.starting) },
		func() uintptr { return v.subscribe(v.view, viewAddNewWindow, v.newWindow) },
		func() uintptr { return v.subscribe(v.view, viewAddCompleted, v.completed) },
		func() uintptr { return v.subscribe(v.view, viewAddProcessFailed, v.processFailed) },
		func() uintptr { return v.subscribe(v.controller, ctrlAddAccelerator, v.accelerator) },
		func() uintptr { return v.subscribe(v.controller, ctrlAddGotFocus, v.gotFocus) },
	} {
		if hr := call(); failed(hr) {
			return hr
		}
	}
	return 0
}

func (v *WebView) subscribe(o *comObject, method int, run func(args *comObject) uintptr) uintptr {
	h := handler(func(_ uintptr, args *comObject) uintptr { return run(args) })
	return o.call(method, h, uintptr(unsafe.Pointer(&v.token)))
}

// text reads a string out of a call and frees it.
func (v *WebView) text(o *comObject, method int) string {
	v.outText = nil
	if failed(o.call(method, uintptr(unsafe.Pointer(&v.outText)))) || v.outText == nil {
		return ""
	}
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(v.outText), 2*n)) != 0 {
		n++
	}
	s := syscall.UTF16ToString(unsafe.Slice(v.outText, n))
	coTaskMemFree.Call(uintptr(unsafe.Pointer(v.outText)))
	return s
}

func (v *WebView) flag(o *comObject, method int) bool {
	v.outFlag = 0
	return !failed(o.call(method, uintptr(unsafe.Pointer(&v.outFlag)))) && v.outFlag != 0
}

// request sees everything a page asks for. A request left alone goes to the network, so only a picture the reader
// allowed is; anything else gets an answer from here.
func (v *WebView) request(args *comObject) uintptr {
	if failed(args.call(reqArgsGetRequest, uintptr(unsafe.Pointer(&v.outObject)))) {
		return 0
	}
	request := v.outObject
	defer request.call(comRelease)
	uri := v.text(request, requestGetURI)
	v.outKind = contextAll
	args.call(reqArgsGetContext, uintptr(unsafe.Pointer(&v.outKind)))
	kind := v.outKind
	if v.pictures && kind == contextImage && (strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://")) {
		return 0
	}
	if uri == v.url && kind == contextDocument {
		headers := "Content-Type: text/html; charset=utf-8\r\nCache-Control: no-store\r\nX-DNS-Prefetch-Control: off\r\n" +
			"Content-Security-Policy: " + v.policy + "; sandbox allow-popups"
		v.respond(args, v.page, 200, "OK", headers)
		return 0
	}
	v.respond(args, nil, 403, "Blocked", "")
	return 0
}

func (v *WebView) respond(args *comObject, body []byte, status uintptr, reason, headers string) {
	var stream uintptr
	if len(body) > 0 {
		stream, _, _ = memStream.Call(uintptr(unsafe.Pointer(&body[0])), uintptr(len(body)))
		if stream == 0 {
			status, reason, headers = 500, "No memory", ""
		}
	}
	hr := v.env.call(envCreateResponse, stream, status, uintptr(unsafe.Pointer(wide(reason))), uintptr(unsafe.Pointer(wide(headers))), uintptr(unsafe.Pointer(&v.outObject)))
	if stream != 0 {
		releaseStream(stream)
	}
	if failed(hr) {
		// With no answer to give, the request must still not go out: a view that cannot refuse has no page.
		v.fail("WebView2 response", hr)
		v.view.call(viewStop)
		return
	}
	response := v.outObject
	args.call(reqArgsPutResponse, uintptr(unsafe.Pointer(response)))
	response.call(comRelease)
}

// starting lets the page itself load; every other navigation stops here, and one the reader's click began opens outside.
func (v *WebView) starting(args *comObject) uintptr {
	uri := v.text(args, startArgsGetURI)
	byUser := v.flag(args, startArgsGetByUser)
	if v.going && uri == v.url { // WebView2 calls a Navigate of the program's a user's act too
		v.going = false
		return 0
	}
	if byUser && strings.HasPrefix(uri, v.url+"#") {
		return 0
	}
	args.call(startArgsPutCancel, 1)
	if byUser {
		v.link(uri)
	}
	return 0
}

func (v *WebView) newWindow(args *comObject) uintptr {
	uri := v.text(args, windowArgsGetURI)
	args.call(windowArgsPutHandled, 1)
	if v.flag(args, windowArgsGetByUser) {
		v.link(uri)
	}
	return 0
}

// completed fits the page that has just loaded to the view.
func (v *WebView) completed(*comObject) uintptr {
	v.loaded = true
	v.runFit()
	return 0
}

func (v *WebView) runFit() {
	if v.loaded && v.fit != "" {
		v.run(v.fit)
	}
}

// run runs a script of the program's in the page, which the view allows with the page's own scripts off.
func (v *WebView) run(script string) {
	if v.ran == 0 {
		v.ran = handler(func(uintptr, *comObject) uintptr { return 0 })
	}
	v.view.call(viewExecuteScript, uintptr(unsafe.Pointer(wide(script))), v.ran)
}

// webSwapLimit is the largest page that goes through a script: one with large pictures in it loads as fast the usual way.
const webSwapLimit = 4 << 20

// webSwap puts a page in the place of the one showing. The parser it goes through runs and fetches nothing, and the
// document stays the one its policy and sandbox were given to, so the page can do no more than a page loaded the usual
// way. A navigation takes 70 to 170 ms with the view blank for part of it; this takes one paint, the fit with it.
const webSwap = `(function(s){var d=new DOMParser().parseFromString(s,'text/html');document.replaceChild(document.adoptNode(d.documentElement),document.documentElement);scrollTo(0,0)})(%s);`

// swap shows a page without a navigation, when the document showing has the same rules.
func (v *WebView) swap(page, policy, fit string, pictures bool) bool {
	if !v.loaded || v.unloaded || policy != v.policy || pictures != v.pictures || len(page) > webSwapLimit {
		return false
	}
	text, err := json.Marshal(page)
	if err != nil {
		return false
	}
	v.page, v.fit = []byte(page), fit // a page that crashes comes back from here
	v.run(fmt.Sprintf(webSwap, text) + fit)
	return true
}

func (v *WebView) link(uri string) {
	if strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "mailto:") {
		v.links = append(v.links, uri)
		v.wake()
	}
}

// processFailed takes a crashed page back up, and tells of a browser that went away.
func (v *WebView) processFailed(args *comObject) uintptr {
	v.outKind = failedBrowserExited
	args.call(failedArgsGetKind, uintptr(unsafe.Pointer(&v.outKind)))
	if v.outKind == failedBrowserExited {
		v.err = errors.New("the WebView2 browser process ended")
		v.wake()
		return 0
	}
	v.unloaded = true
	v.load()
	return 0
}

// accelerator passes on the keys of the program's own shortcuts, which the view would otherwise keep; copying and
// selecting stay with the page.
func (v *WebView) accelerator(args *comObject) uintptr {
	v.outKind = 0
	args.call(keyArgsGetKind, uintptr(unsafe.Pointer(&v.outKind)))
	kind := v.outKind
	v.outKind = 0
	args.call(keyArgsGetKey, uintptr(unsafe.Pointer(&v.outKind)))
	key := v.outKind
	const vkEscape, vkF1, vkF24 = 0x1b, 0x70, 0x87
	ctrl, alt := held(0x11), held(0x12)
	if !ctrl && !alt && key != vkEscape && (key < vkF1 || key > vkF24) || ctrl && (key == 'C' || key == 'A') {
		return 0
	}
	v.outFlag = 0
	args.call(keyArgsGetParam, uintptr(unsafe.Pointer(&v.outFlag)))
	const wmKeyDown, wmKeyUp = 0x0100, 0x0101
	message := uintptr(0)
	switch kind {
	case keyDown, keySystemDown:
		message = wmKeyDown
	case keyUp, keySysUp:
		message = wmKeyUp
	}
	if message != 0 {
		args.call(keyArgsPutHandled, 1)
		postMessage.Call(v.parent.Handle, message, uintptr(key), uintptr(v.outFlag))
	}
	return 0
}

func (v *WebView) gotFocus(*comObject) uintptr {
	v.touched = true
	v.wake()
	return 0
}

// releaseStream lets go of a stream known only by its number, as SHCreateMemStream gives it.
func releaseStream(stream uintptr) {
	var table *[3]uintptr
	moveMemory.Call(uintptr(unsafe.Pointer(&table)), stream, unsafe.Sizeof(table))
	syscall.SyscallN(table[comRelease], stream)
}

func (v *WebView) load() {
	if v.view == nil || !v.unloaded {
		return
	}
	v.unloaded, v.going = false, true
	if hr := v.view.call(viewNavigate, uintptr(unsafe.Pointer(wide(v.url)))); failed(hr) {
		v.fail("WebView2 navigate", hr)
	}
}

// Show loads a page; policy is its Content-Security-Policy, and pictures allows it to fetch images. fit is run in the page
// by the view, which is allowed with the page's own scripts off, when it has loaded and when the view's width changes.
func (v *WebView) Show(page, policy, fit string, pictures bool) {
	if v.swap(page, policy, fit, pictures) {
		return
	}
	v.serial++
	v.url = fmt.Sprintf("%s%d", webHost, v.serial)
	v.page, v.policy, v.fit, v.pictures, v.unloaded, v.loaded = []byte(page), policy, fit, pictures, true, false
	v.load()
}

// Place shows the view over r of the window, in pixels, with holes cut out of it for what is drawn over it.
func (v *WebView) Place(r image.Rectangle, holes []image.Rectangle) {
	if v.shown && r == v.bounds && slices.Equal(holes, v.holes) {
		return
	}
	wider := r.Dx() != v.bounds.Dx()
	v.shown, v.bounds, v.holes = true, r, slices.Clone(holes)
	v.applyPlace()
	if wider {
		v.runFit()
	}
}

func (v *WebView) applyPlace() {
	if !v.shown {
		return
	}
	const noZOrder, noActivate, showWindowFlag = 0x0004, 0x0010, 0x0040
	r := v.bounds
	setWindowPos.Call(v.host, 0, uintptr(r.Min.X), uintptr(r.Min.Y), uintptr(r.Dx()), uintptr(r.Dy()), noZOrder|noActivate|showWindowFlag)
	var region uintptr
	if len(v.holes) > 0 {
		region, _, _ = createRectRgn.Call(0, 0, uintptr(r.Dx()), uintptr(r.Dy()))
		const difference = 4
		for _, h := range v.holes {
			h = h.Sub(r.Min)
			cut, _, _ := createRectRgn.Call(uintptr(h.Min.X), uintptr(h.Min.Y), uintptr(h.Max.X), uintptr(h.Max.Y))
			combineRgn.Call(region, region, cut, difference)
			deleteObject.Call(cut)
		}
	}
	setWindowRgn.Call(v.host, region, 1) // the window owns the region from here on
	if v.controller != nil {
		v.box = rect{0, 0, int32(r.Dx()), int32(r.Dy())}
		v.putBounds()
		v.controller.call(ctrlPutVisible, 1)
	}
}

// Bounds is where the view was last placed.
func (v *WebView) Bounds() image.Rectangle { return v.bounds }

// Hide takes the view off the window; its page stays loaded.
func (v *WebView) Hide() {
	if !v.shown {
		return
	}
	v.shown = false
	const hide = 0
	showWindow.Call(v.host, hide)
	if v.controller != nil {
		v.controller.call(ctrlPutVisible, 0)
	}
}

// Links gives the links clicked since it was last asked.
func (v *WebView) Links() []string {
	out := v.links
	v.links = nil
	return out
}

// Touched reports whether the reader went into the view since it was last asked.
func (v *WebView) Touched() bool {
	t := v.touched
	v.touched = false
	return t
}

// Err is what stopped the view, if anything did.
func (v *WebView) Err() error { return v.err }

// Close ends the view.
func (v *WebView) Close() {
	if v.host == 0 {
		return
	}
	if v.parent.web == v {
		v.parent.web = nil
	}
	if v.controller != nil {
		v.controller.call(ctrlClose)
	}
	for _, o := range []*comObject{v.view, v.controller, v.env} {
		if o != nil {
			o.call(comRelease)
		}
	}
	v.view, v.controller, v.env = nil, nil, nil
	destroyWindow.Call(v.host)
	v.host = 0
}

// webMessage keeps the parts of a window that a view in it needs up to date, for its messages.
func (w *Window) webMessage(msg uint32) {
	const wmMove, wmLeftDown, wmRightDown, wmMiddleDown = 0x0003, 0x0201, 0x0204, 0x0207
	switch msg {
	case wmMove:
		if c := w.web.controller; c != nil {
			c.call(ctrlNotifyMoved)
		}
	case wmLeftDown, wmRightDown, wmMiddleDown:
		setFocus.Call(w.Handle) // a click beside the view takes the keys back from it
	}
}

// hasFocus reports whether the keyboard focus is in the window, a view in it included.
func (w *Window) hasFocus(focus uintptr) bool {
	if focus == w.Handle {
		return true
	}
	if w.web == nil || focus == 0 {
		return false
	}
	in, _, _ := isChild.Call(w.Handle, focus)
	return in != 0
}
