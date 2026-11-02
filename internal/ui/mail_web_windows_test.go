package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/win"
)

// realWebHost is the test harness with a real window around it, so that the view is the system's own.
type realWebHost struct {
	*fakeHost
	w *win.Window
}

func (h *realWebHost) OpenWebView(dir string, wake func()) (WebView, error) {
	v, err := h.w.OpenWebView(dir, wake)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// snapshot takes what the window really shows, the view inside it included.
func snapshot(t *testing.T, w *win.Window, path string) *image.RGBA {
	t.Helper()
	width, height := w.Size()
	user32, gdi32 := syscall.NewLazyDLL("user32.dll"), syscall.NewLazyDLL("gdi32.dll")
	screen, _, _ := user32.NewProc("GetDC").Call(0)
	defer user32.NewProc("ReleaseDC").Call(0, screen)
	mem, _, _ := gdi32.NewProc("CreateCompatibleDC").Call(screen)
	defer gdi32.NewProc("DeleteDC").Call(mem)
	bitmap, _, _ := gdi32.NewProc("CreateCompatibleBitmap").Call(screen, uintptr(width), uintptr(height))
	defer gdi32.NewProc("DeleteObject").Call(bitmap)
	gdi32.NewProc("SelectObject").Call(mem, bitmap)
	const clientOnly, fullContent = 1, 2
	if ok, _, _ := user32.NewProc("PrintWindow").Call(w.Handle, mem, clientOnly|fullContent); ok == 0 {
		t.Fatal("PrintWindow failed")
	}
	header := struct {
		Size                   uint32
		Width, Height          int32
		Planes, Bits           uint16
		Compression, ImageSize uint32
		X, Y                   int32
		Used, Important        uint32
	}{Size: 40, Width: int32(width), Height: -int32(height), Planes: 1, Bits: 32}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	gdi32.NewProc("GetDIBits").Call(mem, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(unsafe.Pointer(&header)), 0)
	for i := 0; i+3 < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+2], img.Pix[i+3] = img.Pix[i+2], img.Pix[i], 255
	}
	if path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	return img
}

const realNewsletter = "From: daily.dev <informer@daily.dev>\r\nSubject: Your personal update\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=c\r\n\r\n" +
	"--c\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nThe plain copy, which must not be what is shown.\r\n" +
	"--c\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
	`<html><head><style>.h{color:#fff;background:#6c3bd1;padding:24px;font:bold 24px sans-serif}.card{border:1px solid #ddd;border-radius:8px;padding:16px;margin:12px 0}</style></head>` +
	`<body><div class=h id=head>Here is what developers read</div>` +
	`<div class=card><b>The warning is right, and every fix is wrong</b><p>CSS-Tricks · <a href="https://example.com/read">Read article →</a></p></div>` +
	`<div class=card><b>为什么 Go 适合 AI 辅助的软件工程</b><p>Go Developers</p><img src="https://127.0.0.1:9/tracker.gif" alt="a picture"></div>` +
	`<script>document.getElementById('head').style.background='#ff0000';document.getElementById('head').textContent='SCRIPT RAN'</script></body></html>` + "\r\n--c--\r\n"

// TestRealViewInAWindow puts a message in the system's view, in a real window, and looks at what the window shows:
// the page's own colours are there, its script has not run, and what is drawn over the view shows through it.
// FORGE_REAL_WEB=<folder> runs it and saves the pictures there.
func TestRealViewInAWindow(t *testing.T) {
	dir := os.Getenv("FORGE_REAL_WEB")
	if dir == "" {
		t.Skip("FORGE_REAL_WEB is not set")
	}
	x := newHarness(t)
	w, err := win.Open("mail", 1280, 800, win.Background)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// The browser holds its files past the test's end, which a test's own folder takes as a failure.
	cache, err := os.MkdirTemp("", "forge-web")
	if err != nil {
		t.Fatal(err)
	}
	x.a.cacheDir = cache
	host := &realWebHost{fakeHost: x.h, w: w}
	x.a.host = host
	width, height := w.Size()
	x.c = gfx.NewCanvas(width, height)
	x.scale = w.Scale()
	home, _ := bindTwo(t, x)
	home.Add("daily.dev <informer@daily.dev>", "Your personal update", "").Raw = realNewsletter
	s := &x.a.mail
	x.a.mailSync()
	step := func() {
		w.Poll()
		x.frame(Input{})
		w.Present(x.c.Img.Pix, width, height)
		time.Sleep(10 * time.Millisecond)
	}
	for end := time.Now().Add(10 * time.Second); len(s.list) < 4 && time.Now().Before(end); {
		step()
	}
	for _, m := range s.list {
		if strings.HasPrefix(m.Subject, "Your personal") {
			s.current = m.ID
		}
	}
	for end := time.Now().Add(20 * time.Second); s.web == nil && time.Now().Before(end); {
		step()
	}
	if s.web == nil {
		t.Skip("this system has no view")
	}
	settled := func(d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); {
			step()
		}
	}
	settled(4 * time.Second)
	if err := s.web.Err(); err != nil {
		t.Fatal(err)
	}
	page := snapshot(t, w, filepath.Join(dir, "page.png"))
	view := s.page
	box := s.web.(*win.WebView).Bounds()
	head := page.RGBAAt(box.Min.X+int(60*x.scale), box.Min.Y+int(40*x.scale))
	if !sameColour(head, color.RGBA{0x6c, 0x3b, 0xd1, 255}) {
		t.Errorf("the page's head is %v: its styles did not apply, or its script ran", head)
	}
	if !view.known || view.pictures {
		t.Errorf("page state %+v", view)
	}
	// A menu opened over the page has to show through the view, and the page beside it stays.
	x.a.menu = &popupMenu{id: "over", sub: -1, anchor: gfx.Rect{X: float32(box.Min.X) + 200, Y: float32(box.Min.Y) + 120, W: 1, H: 1},
		items: []menuItem{{label: "Over the page"}, {label: "Second line"}}}
	settled(600 * time.Millisecond)
	over := snapshot(t, w, filepath.Join(dir, "over.png"))
	r := pixels(x.a.menuBelow(x.a.menu.items, x.a.menu.anchor))
	if !r.Overlaps(box) {
		t.Fatalf("the menu at %v is not over the view at %v", r, box)
	}
	if at := over.RGBAAt(r.Max.X-3, r.Min.Y+3); sameColour(at, color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the view hides the menu at %v: %v", r, at)
	}
	if at := over.RGBAAt(r.Max.X+30, r.Min.Y+3); sameColour(at, colSurface) {
		t.Errorf("the view has gone from beside the menu: %v", at)
	}
	x.a.menu = nil
	x.a.settingsOpen = true
	settled(300 * time.Millisecond)
	snapshot(t, w, filepath.Join(dir, "dialog.png"))
	x.a.settingsOpen = false
	settled(300 * time.Millisecond)
	snapshot(t, w, filepath.Join(dir, "back.png"))
	s.web.Close()
	s.web = nil
}

func sameColour(a, b color.RGBA) bool {
	d := func(p, q uint8) int {
		if p > q {
			return int(p - q)
		}
		return int(q - p)
	}
	return d(a.R, b.R)+d(a.G, b.G)+d(a.B, b.B) < 24
}
