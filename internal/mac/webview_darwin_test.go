package mac

import (
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const strictPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:"

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

func openTestWebView(t *testing.T) (*Window, *WebView) {
	t.Helper()
	w, err := Open("webview test", 640, 480, true)
	if err != nil {
		t.Fatal(err)
	}
	v := w.OpenWebView(w.Wake)
	v.Place(image.Rect(0, 0, 600, 400), nil)
	return w, v
}

func settleWeb(t *testing.T, w *Window, v *WebView, done func() bool) {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); {
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

func pauseWeb(w *Window, d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		w.Poll()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWebViewNoScriptRunsAndNothingLeavesAPage(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		var heard atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { heard.Add(1) }))
		defer server.Close()
		w, v := openTestWebView(t)
		defer func() { v.Close(); w.Close() }()

		v.Show(hostile(server.URL), strictPolicy, "", false)
		settleWeb(t, w, v, func() bool { return v.loaded })
		pauseWeb(w, 800*time.Millisecond)
		if got := v.title(); got != "safe" {
			t.Errorf("the page's title is %q: something in it ran", got)
		}
		if n := heard.Load(); n != 0 {
			t.Errorf("the page reached the outside %d times", n)
		}
		if links := v.Links(); len(links) != 0 {
			t.Errorf("links opened with no click: %v", links)
		}
	})
}

func TestWebViewFitRunsAndPageStillCannotAct(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		var heard atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { heard.Add(1) }))
		defer server.Close()
		w, v := openTestWebView(t)
		defer func() { v.Close(); w.Close() }()

		v.Show(hostile(server.URL), strictPolicy, "document.title+=' fitted'", false)
		settleWeb(t, w, v, func() bool { return v.title() == "safe fitted" })
		pauseWeb(w, 400*time.Millisecond)
		if n := heard.Load(); n != 0 {
			t.Errorf("the page reached the outside %d times", n)
		}
		v.Place(image.Rect(0, 0, 500, 400), nil)
		settleWeb(t, w, v, func() bool { return v.title() == "safe fitted fitted" })
	})
}

func TestWebViewFastSwapCannotActEither(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		var heard atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { heard.Add(1) }))
		defer server.Close()
		w, v := openTestWebView(t)
		defer func() { v.Close(); w.Close() }()

		v.Show("<title>first</title>", strictPolicy, "", false)
		settleWeb(t, w, v, func() bool { return v.loaded })
		navs := v.navCount

		v.Show(hostile(server.URL), strictPolicy, "document.title+=' fitted'", false)
		settleWeb(t, w, v, func() bool { return strings.Contains(v.title(), "fitted") })
		pauseWeb(w, 800*time.Millisecond)
		if got := v.title(); got != "safe fitted" {
			t.Errorf("the page's title is %q: something in it ran", got)
		}
		if n := heard.Load(); n != 0 {
			t.Errorf("the page reached the outside %d times", n)
		}
		if v.navCount != navs {
			t.Error("a page with the same rules triggered a full navigation instead of fast swap")
		}

		// Changing rules must trigger a full navigation.
		v.Show("<title>with pictures</title>", strictPolicy+"; img-src data: http:", "", true)
		if v.navCount == navs {
			t.Error("a page with different rules reused the previous document")
		}
	})
}

func TestWebViewPicturesAloneAreLetOutWhenAllowed(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		var picture, other atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/p.png") {
				picture.Add(1)
			} else {
				other.Add(1)
			}
		}))
		defer server.Close()
		w, v := openTestWebView(t)
		defer func() { v.Close(); w.Close() }()

		page := fmt.Sprintf(`<img src="%[1]s/p.png"><link rel=stylesheet href="%[1]s/s.css"><script src="%[1]s/s.js"></script>`, server.URL)
		policy := "default-src 'none'; style-src 'unsafe-inline' http:; script-src http:; img-src data: http:"
		v.Show(page, policy, "", true)
		settleWeb(t, w, v, func() bool { return v.loaded && picture.Load() > 0 })
		pauseWeb(w, 400*time.Millisecond)
		if n := other.Load(); n != 0 {
			t.Errorf("%d requests other than the picture went out", n)
		}
	})
}

func TestWebViewPageThatMovesItselfStays(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		w, v := openTestWebView(t)
		defer func() { v.Close(); w.Close() }()

		v.Show(`<title>stays</title><meta http-equiv=refresh content="0;url=https://example.com/by-itself">`, strictPolicy, "", false)
		settleWeb(t, w, v, func() bool { return v.loaded })
		pauseWeb(w, 800*time.Millisecond)
		if links := v.Links(); len(links) != 0 {
			t.Errorf("a navigation nobody clicked is offered as a link: %v", links)
		}
		if got := v.title(); got != "stays" {
			t.Errorf("the page left: title %q", got)
		}
	})
}

func TestWebViewPlaceHolesAndLargePage(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		w, v := openTestWebView(t)
		defer func() { v.Close(); w.Close() }()

		holes := []image.Rectangle{image.Rect(50, 50, 200, 180)}
		v.Place(image.Rect(10, 20, 610, 420), holes)
		// Second identical Place is a cached no-op.
		v.Place(image.Rect(10, 20, 610, 420), holes)
		if v.Bounds() != image.Rect(10, 20, 610, 420) {
			t.Fatalf("Bounds = %v", v.Bounds())
		}

		v.Show("<title>large</title><img src=\"data:image/gif;base64,"+strings.Repeat("R0lGODlh", 1<<20)+"\">", strictPolicy, "", false)
		settleWeb(t, w, v, func() bool { return v.loaded })
		if got := v.title(); got != "large" {
			t.Errorf("title %q", got)
		}
	})
}
