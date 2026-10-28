package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/media"
)

type fakeHost struct {
	woke       chan struct{}
	cursor     Cursor
	typing     bool
	opened     []string
	clip       string
	pasteImage image.Image
	languages  []string
	minimized  bool
	hidden     bool
	excluded   bool
	hotkeys    []Hotkey
	clipImage  image.Image
	overlaysOn bool           // OpenOverlay works; off, it fails, as on a window without overlays
	overlays   []*fakeOverlay // every overlay opened, closed ones too
	title      string
	chosen     []string // what the open dialog answers
	pasteText  string   // the text and files a paste finds, besides pasteImage
	pasteFiles []string
}

func (h *fakeHost) Wake() {
	select {
	case h.woke <- struct{}{}:
	default:
	}
}
func (h *fakeHost) SetTitle(t string)                  { h.title = t }
func (h *fakeHost) SetCursor(c Cursor)                 { h.cursor = c }
func (h *fakeHost) TextInput(on bool, _, _, _ float32) { h.typing = on }
func (h *fakeHost) Clipboard() (string, error)         { return h.clip, nil }
func (h *fakeHost) SetClipboard(text string) error     { h.clip = text; return nil }
func (h *fakeHost) Paste() (image.Image, []string, string) {
	return h.pasteImage, h.pasteFiles, h.pasteText
}
func (h *fakeHost) ChooseFolder(string) string                   { return "" }
func (h *fakeHost) OpenFiles(string, [][2]string, bool) []string { return h.chosen }
func (h *fakeHost) Reveal(path string) error                     { h.opened = append(h.opened, path); return nil }
func (h *fakeHost) Launch(path string) error                     { h.opened = append(h.opened, path); return nil }
func (h *fakeHost) LaunchURL(string) error                       { return nil }
func (h *fakeHost) Languages() []string                          { return h.languages }
func (h *fakeHost) Minimize()                                    { h.minimized = true }
func (h *fakeHost) Restore()                                     { h.minimized, h.hidden = false, false }
func (h *fakeHost) Hide()                                        { h.hidden = true }
func (h *fakeHost) Shown() bool                                  { return !h.minimized && !h.hidden }
func (h *fakeHost) ExcludeFromCapture(on bool) error             { h.excluded = on; return nil }
func (h *fakeHost) SetClipboardImage(img image.Image) error      { h.clipImage = img; return nil }
func (h *fakeHost) SetHotkeys(keys []Hotkey) []error {
	h.hotkeys = keys
	return make([]error, len(keys))
}

// harness drives the editor headlessly with synthetic input at a fixed size.
type harness struct {
	t      testing.TB
	a      *App
	h      *fakeHost
	c      *gfx.Canvas
	scale  float32
	mx, my float32
	dir    string
}

func newHarness(t testing.TB) *harness {
	t.Helper()
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	fonts, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skip(err)
	}
	h := &fakeHost{woke: make(chan struct{}, 1)}
	dir := t.TempDir()
	// The app downloads its own ffmpeg; tests use the one on PATH instead, so that none reaches the internet.
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	system := bytes.Replace(media.DefaultToolsJSON, []byte(`"system": false`), []byte(`"system": true`), 1)
	if err := os.WriteFile(filepath.Join(dir, "state", "ffmpeg.json"), system, 0o644); err != nil {
		t.Fatal(err)
	}
	// The app keeps the user's work (canvases, screenshots, recordings) in their home folder's Videos, and the file
	// manager starts at home: a test's home is its own folder, so nothing it makes lands among the user's files.
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	a := New(h, fonts, filepath.Join(dir, "cache"), filepath.Join(dir, "state"))
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "pieces.json") {
			w.Write([]byte(`[{"title":"Calm Piano","filename":"Calm Piano.mp3","length":"2:10","genre":"22","feel":"Calming"},
{"title":"Epic Drums","filename":"Epic Drums.mp3","length":"3:05","genre":"19","feel":"Epic, Driving"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	a.lib.SetConfig(sourcesAt(t, catalog.URL))
	t.Cleanup(func() {
		a.Close()
		catalog.Close()
	})
	x := &harness{t: t, a: a, h: h, c: gfx.NewCanvas(1600, 1000), scale: 1, dir: dir}
	x.pump(time.Minute, func() bool { return a.tools.ready || a.tools.err != nil })
	if a.tools.err != nil {
		t.Fatalf("ffmpeg: %v", a.tools.err)
	}
	return x
}

// sourcesAt is the built-in music configuration with every site moved to a test server, so no test reaches the
// internet.
func sourcesAt(t testing.TB, base string) *library.Config {
	t.Helper()
	data := strings.NewReplacer(
		"https://incompetech.com/music/royalty-free/", base+"/",
		"https://api.openverse.org/v1/audio/", base+"/ov/",
		"https://ccmixter.org/", base+"/cc/",
		"https://archive.org/", base+"/ia/",
	).Replace(string(library.DefaultJSON))
	c, err := library.ParseConfig([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// frame draws a frame with the input; its keys are tapped, let go in the same frame, unless Up is given (empty to
// keep them held).
func (x *harness) frame(in Input) {
	in.X, in.Y = x.mx, x.my
	if in.Up == nil {
		for _, k := range in.Keys {
			in.Up = append(in.Up, k.Code)
		}
	}
	x.a.Frame(in, x.c, x.scale)
}

// pump draws frames until done reports true, failing after timeout.
func (x *harness) pump(timeout time.Duration, done func() bool) {
	x.t.Helper()
	deadline := time.Now().Add(timeout)
	for !done() {
		if time.Now().After(deadline) {
			x.t.Fatalf("gave up after %v", timeout)
		}
		x.frame(Input{})
		select {
		case <-x.h.woke:
		case <-time.After(15 * time.Millisecond):
		}
	}
	x.frame(Input{})
}

func (x *harness) settle(timeout time.Duration) {
	x.t.Helper()
	quiet := 0
	x.pump(timeout, func() bool {
		if x.a.Idle() {
			quiet++
		} else {
			quiet = 0
		}
		return quiet > 3
	})
}

func (x *harness) click(r gfx.Rect) {
	x.mx, x.my = r.X+r.W/2, r.Y+r.H/2
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
}

func (x *harness) drag(fromX, fromY, toX, toY float32) {
	x.mx, x.my = fromX, fromY
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: fromX, Y: fromY, Down: true}}})
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		x.mx, x.my = fromX+(toX-fromX)*f, fromY+(toY-fromY)*f
		x.frame(Input{Down: true})
	}
	x.frame(Input{Pointer: []Pointer{{X: toX, Y: toY}}})
}

func (x *harness) key(code uint16, ctrl bool) { x.frame(Input{Keys: []Key{{Code: code, Ctrl: ctrl}}}) }

func (x *harness) shot(name string) {
	dir := os.Getenv("FORGE_SHOTS")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		x.t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, x.c.Img)
}

// clipFile writes a test video: a moving test pattern (or a solid colour) with a tone.
func clipFile(t testing.TB, dir, name, pattern string, seconds string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	cmd, err := media.Command(context.Background(), "ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", pattern+":rate=30:duration="+seconds,
		"-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000:duration="+seconds,
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	if err != nil {
		t.Skip(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return path
}

// mediaDir is a folder for test videos that is removed only after the editor has closed, since the editor's
// decoders may still be reading them until then.
func (x *harness) mediaDir() string {
	x.t.Helper()
	dir, err := os.MkdirTemp(x.dir, "media-*")
	if err != nil {
		x.t.Fatal(err)
	}
	return dir
}

func (x *harness) fixture() string {
	return clipFile(x.t, x.mediaDir(), "clip.mp4", "testsrc2=size=640x360", "8")
}

func (x *harness) open(path string) {
	x.t.Helper()
	x.frame(Input{Dropped: []string{path}})
	x.settle(30 * time.Second)
	if x.a.p == nil {
		x.t.Fatalf("%s did not open; toasts %+v", path, x.a.toasts)
	}
}

func (h *fakeHost) OpenOverlay(o OverlayOptions) (Overlay, error) {
	if !h.overlaysOn {
		return nil, errors.New("no overlays here")
	}
	f := &fakeOverlay{opts: o, bounds: o.Bounds, clickThrough: o.ClickThrough}
	h.overlays = append(h.overlays, f)
	return f, nil
}

// open lists the overlays that are still open.
func (h *fakeHost) open() []*fakeOverlay {
	var out []*fakeOverlay
	for _, o := range h.overlays {
		if !o.closed {
			out = append(out, o)
		}
	}
	return out
}

// fakeOverlay keeps what was presented and hands out input the test queued.
type fakeOverlay struct {
	opts         OverlayOptions
	bounds       image.Rectangle
	queued       []Input
	mx, my       float32
	down         bool
	shown        *image.RGBA
	presents     int
	clickThrough bool
	keyboard     bool
	cursor       Cursor
	closed       bool
}

func (o *fakeOverlay) Bounds() image.Rectangle { return o.bounds }
func (o *fakeOverlay) Scale() float32          { return 1 }
func (o *fakeOverlay) Input() Input {
	if len(o.queued) == 0 {
		return Input{X: o.mx, Y: o.my, Down: o.down}
	}
	in := o.queued[0]
	o.queued = o.queued[1:]
	return in
}
func (o *fakeOverlay) Present(img *image.RGBA, dirty image.Rectangle) error {
	if o.shown == nil || o.shown.Rect != img.Rect {
		o.shown = image.NewRGBA(img.Rect)
	}
	copy(o.shown.Pix, img.Pix)
	o.presents++
	return nil
}
func (o *fakeOverlay) SetClickThrough(on bool) { o.clickThrough = on }
func (o *fakeOverlay) TakeKeyboard(on bool)    { o.keyboard = on }
func (o *fakeOverlay) SetCursor(c Cursor)      { o.cursor = c }
func (o *fakeOverlay) Move(r image.Rectangle)  { o.bounds = r }
func (o *fakeOverlay) Closed() bool            { return false }
func (o *fakeOverlay) Close()                  { o.closed = true }

// drag queues a press at (x0, y0), moves to (x1, y1) and a release there, in the overlay's pixels.
func (o *fakeOverlay) drag(x0, y0, x1, y1 float32) {
	o.queued = append(o.queued, Input{X: x0, Y: y0}, Input{X: x0, Y: y0, Down: true, Pointer: []Pointer{{X: x0, Y: y0, Down: true}}})
	for i := 1; i <= 6; i++ {
		f := float32(i) / 6
		o.queued = append(o.queued, Input{X: x0 + (x1-x0)*f, Y: y0 + (y1-y0)*f, Down: true})
	}
	o.queued = append(o.queued, Input{X: x1, Y: y1, Pointer: []Pointer{{X: x1, Y: y1}}})
	o.mx, o.my = x1, y1
}

// click queues a press and release at (x, y).
func (o *fakeOverlay) click(x, y float32) { o.drag(x, y, x, y) }
