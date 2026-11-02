package win

import (
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// A view is real here, so these tests need the system's WebView2 Runtime and skip where it is not installed.
func openTestView(t *testing.T) (*Window, *WebView) {
	t.Helper()
	return openTestViewAt(t, Background)
}

func openTestViewAt(t *testing.T, at Placement) (*Window, *WebView) {
	t.Helper()
	w, err := Open("view", 640, 480, at)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	// The browser and the loaded loader hold their files for a moment past the end, which t.TempDir takes as a failure.
	dir, err := os.MkdirTemp("", "webview")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	v, err := w.OpenWebView(dir, func() {})
	if err == ErrNoWebView {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	v.Place(image.Rect(0, 0, 600, 400), nil)
	settle(t, w, v, func() bool { return v.view != nil })
	return w, v
}

func settle(t *testing.T, w *Window, v *WebView, done func() bool) {
	t.Helper()
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); {
		w.Poll()
		if v.Err() != nil {
			t.Fatal(v.Err())
		}
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the view did not get there in time")
}

// pause keeps the window's messages going while a page has its chance to do something.
func pause(w *Window, d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		w.Poll()
		time.Sleep(5 * time.Millisecond)
	}
}

func (v *WebView) title() string { return v.text(v.view, viewGetTitle) }

func completed(t *testing.T, v *WebView) *atomic.Bool {
	t.Helper()
	var done atomic.Bool
	if hr := v.subscribe(v.view, viewAddCompleted, func(*comObject) uintptr {
		done.Store(true)
		return 0
	}); failed(hr) {
		t.Fatal(hr)
	}
	return &done
}

const strictPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:"

// Everything a hostile message could try at once; each attempt calls the test's server, which must hear nothing.
func hostile(url string) string {
	return fmt.Sprintf(`<!doctype html><title>safe</title>
<script>document.title='script ran';fetch(%[1]q+'/fetch');new Image().src=%[1]q+'/script-image'</script>
<script src=%[1]q></script>
<img src=%[1]q onerror="document.title='handler ran'">
<body onload="document.title='onload ran'">
<iframe src=%[1]q></iframe><iframe srcdoc="<script>parent.document.title='frame ran'</script>"></iframe>
<link rel=stylesheet href=%[1]q><object data=%[1]q></object><video src=%[1]q></video>
<svg onload="document.title='svg ran'"></svg><a href="javascript:document.title='link ran'" id=x>x</a>
<form action=%[1]q id=f><input name=a></form><meta http-equiv=refresh content="0;url=%[1]q/refresh">`, url)
}

func TestNoScriptRunsAndNothingLeavesAPage(t *testing.T) {
	var heard atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { heard.Add(1) }))
	defer server.Close()
	w, v := openTestView(t)
	done := completed(t, v)
	v.Show(hostile(server.URL), strictPolicy, "", false)
	settle(t, w, v, done.Load)
	pause(w, 1500*time.Millisecond)
	if got := v.title(); got != "safe" {
		t.Errorf("the page's title is %q: something in it ran", got)
	}
	if n := heard.Load(); n != 0 {
		t.Errorf("the page reached the outside %d times", n)
	}
	if links := v.Links(); len(links) != 0 {
		t.Errorf("links opened with no click: %v", links)
	}
}

// The program's own step runs where a message's scripts do not, and opens nothing for them.
func TestTheFitRunsAndThePageStillCannotAct(t *testing.T) {
	var heard atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { heard.Add(1) }))
	defer server.Close()
	w, v := openTestView(t)
	v.Show(hostile(server.URL), strictPolicy, "document.title+=' fitted'", false)
	settle(t, w, v, func() bool { return v.title() == "safe fitted" })
	pause(w, 500*time.Millisecond)
	if n := heard.Load(); n != 0 {
		t.Errorf("the page reached the outside %d times", n)
	}
	v.Place(image.Rect(0, 0, 500, 400), nil)
	settle(t, w, v, func() bool { return v.title() == "safe fitted fitted" })
}

// The next message goes into the document showing, past the navigation filter: it must be able to do no more for that.
func TestAPagePutInPlaceOfAnotherCannotActEither(t *testing.T) {
	var heard atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { heard.Add(1) }))
	defer server.Close()
	w, v := openTestView(t)
	done := completed(t, v)
	v.Show("<title>first</title>", strictPolicy, "", false)
	settle(t, w, v, done.Load)
	at := v.url
	v.Show(hostile(server.URL), strictPolicy, "document.title+=' fitted'", false)
	settle(t, w, v, func() bool { return strings.Contains(v.title(), "fitted") })
	pause(w, 1500*time.Millisecond)
	if got := v.title(); got != "safe fitted" {
		t.Errorf("the page's title is %q: something in it ran", got)
	}
	if n := heard.Load(); n != 0 {
		t.Errorf("the page reached the outside %d times", n)
	}
	if links := v.Links(); len(links) != 0 {
		t.Errorf("links opened with no click: %v", links)
	}
	if v.url != at {
		t.Error("a page with the same rules was navigated to")
	}
	v.Show("<title>with pictures</title>", strictPolicy+"; img-src data: http:", "", true)
	if v.url == at {
		t.Error("a page with other rules went into a document held to the first ones")
	}
}

func TestPicturesAloneAreLetOutWhenAllowed(t *testing.T) {
	var picture, other atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/p.png") {
			picture.Add(1)
		} else {
			other.Add(1)
		}
	}))
	defer server.Close()
	w, v := openTestView(t)
	page := fmt.Sprintf(`<img src="%[1]s/p.png"><link rel=stylesheet href="%[1]s/s.css"><script src="%[1]s/s.js"></script>`, server.URL)
	policy := "default-src 'none'; style-src 'unsafe-inline' http:; script-src http:; img-src data: http:"
	done := completed(t, v)
	v.Show(page, policy, "", true)
	settle(t, w, v, func() bool { return done.Load() && picture.Load() > 0 })
	pause(w, 500*time.Millisecond)
	if n := other.Load(); n != 0 {
		t.Errorf("%d requests other than the picture went out, though the page's policy let them", n)
	}
}

func TestAPageThatMovesItselfStays(t *testing.T) {
	w, v := openTestView(t)
	done := completed(t, v)
	v.Show(`<title>stays</title><meta http-equiv=refresh content="0;url=https://example.com/by-itself">`, strictPolicy, "", false)
	settle(t, w, v, done.Load)
	pause(w, 1500*time.Millisecond)
	if links := v.Links(); len(links) != 0 {
		t.Errorf("a navigation nobody clicked is offered as a link: %v", links)
	}
	if got := v.title(); got != "stays" {
		t.Errorf("the page left: title %q", got)
	}
}

// click presses the real mouse on the view, and puts the pointer back. A click only the system's input makes counts as the
// reader's to the browser, and one that nothing but this window would receive is the only one sent.
func (v *WebView) click(t *testing.T, w *Window, x, y int) {
	t.Helper()
	const noSize, noMove, topMost = 0x0001, 0x0002, ^uintptr(0)
	setWindowPos.Call(w.Handle, topMost, 0, 0, 0, 0, noSize|noMove)
	pause(w, 300*time.Millisecond)
	type point struct{ X, Y int32 }
	var was point
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&was)))
	var box rect
	user32.NewProc("GetWindowRect").Call(v.host, uintptr(unsafe.Pointer(&box)))
	at := point{box.Left + int32(x), box.Top + int32(y)}
	under, _, _ := user32.NewProc("WindowFromPoint").Call(uintptr(uint32(at.X)) | uintptr(uint32(at.Y))<<32)
	root, _, _ := user32.NewProc("GetAncestor").Call(under, 2)
	if root != w.Handle {
		t.Skipf("window %x, not this one, is at %+v, and would get the click", root, at)
	}
	const leftDown, leftUp = 0x0002, 0x0004
	move, press := user32.NewProc("SetCursorPos"), user32.NewProc("mouse_event")
	move.Call(uintptr(at.X), uintptr(at.Y))
	pause(w, 100*time.Millisecond)
	press.Call(leftDown, 0, 0, 0, 0)
	press.Call(leftUp, 0, 0, 0, 0)
	pause(w, 100*time.Millisecond)
	move.Call(uintptr(was.X), uintptr(was.Y))
}

// FORGE_REAL_WEB moves the real pointer for a moment, so this test runs only when asked.
func TestAClickedLinkOpensOutsideAndThePageStays(t *testing.T) {
	if os.Getenv("FORGE_REAL_WEB") == "" {
		t.Skip("FORGE_REAL_WEB is not set")
	}
	EnableDPI() // the pointer and the window must count in the same pixels
	for _, target := range []string{"", "_blank"} {
		w, v := openTestViewAt(t, Foreground)
		done := completed(t, v)
		v.Show(fmt.Sprintf(`<title>stays</title><a target=%q href="https://example.com/x" style="display:block;font-size:80px">link</a>`, target), strictPolicy+"; sandbox allow-popups", "", false)
		settle(t, w, v, done.Load)
		v.click(t, w, 60, 60)
		var links []string
		settle(t, w, v, func() bool {
			links = append(links, v.Links()...)
			return len(links) > 0
		})
		if links[0] != "https://example.com/x" {
			t.Errorf("target %q: links %v", target, links)
		}
		pause(w, 300*time.Millisecond)
		if got := v.title(); got != "stays" {
			t.Errorf("target %q: the page left: title %q", target, got)
		}
	}
}

func TestALargePageLoads(t *testing.T) {
	w, v := openTestView(t)
	done := completed(t, v)
	v.Show("<title>large</title><img src=\"data:image/gif;base64,"+strings.Repeat("R0lGODlh", 1<<20)+"\">", strictPolicy, "", false)
	settle(t, w, v, done.Load)
	if got := v.title(); got != "large" {
		t.Errorf("title %q", got)
	}
}
