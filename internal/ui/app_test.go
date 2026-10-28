package ui

import (
	"context"
	"image"
	"image/color"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
)

func TestEditorCutsSpeedsCaptionsAndExports(t *testing.T) {
	x := newHarness(t)
	x.frame(Input{})
	x.shot("01-empty")
	src := x.fixture()
	x.open(src)
	x.shot("02-opened")
	l := x.a.layout()
	tr := x.a.tracks(l.timeline)

	x.click(gfx.Rect{X: tr.x(2), Y: tr.ruler.Y, W: 1, H: tr.ruler.H})
	if math.Abs(x.a.playhead-2) > 0.05 {
		t.Fatalf("ruler click put the playhead at %v", x.a.playhead)
	}
	x.key('S', false)
	if len(x.a.p.Video[0].Clips) != 2 {
		t.Fatalf("split made %d clips", len(x.a.p.Video[0].Clips))
	}
	main, _ := tr.lane(groupVideo, 0)
	x.click(gfx.Rect{X: tr.x(5), Y: main.rect.Y + 10, W: 1, H: 1})
	second := x.a.p.Video[0].Clips[1].ID
	if x.a.sel.kind != selClip || x.a.sel.id != second {
		t.Fatalf("clicking the second clip selected %+v", x.a.sel)
	}
	x.click(x.chipRect(l.transport, "2×"))
	if c, _ := x.a.p.Clip(second); c.Speed != 2 || math.Abs(x.a.p.Duration()-5) > 0.01 {
		t.Fatalf("speed %v duration %v", c.Speed, x.a.p.Duration())
	}
	tr = x.a.tracks(x.a.layout().timeline)
	y := main.rect.Y + main.rect.H/2
	x.drag(tr.x(0)+2, y, tr.x(0.5)+2, y)
	if in := x.a.p.Video[0].Clips[0].In; in < 0.3 || in > 0.7 {
		t.Fatalf("left trim moved In to %v", in)
	}

	x.a.seek(1)
	x.key('T', false)
	if x.a.sel.kind != selOverlay || !x.h.typing {
		t.Fatalf("T selected %+v, typing=%v", x.a.sel, x.h.typing)
	}
	x.frame(Input{Typed: []rune("你好，世界")})
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if o, _ := x.a.p.Overlay(x.a.sel.id); o.Body != "你好，世界" {
		t.Fatalf("caption body %q", o.Body)
	}
	x.settle(10 * time.Second)
	x.shot("03-edited")
	id := x.a.sel.id
	x.key('Z', true)
	if o, _ := x.a.p.Overlay(id); o.Body == "你好，世界" {
		t.Fatal("undo kept the typed text")
	}
	x.key('Y', true)
	if o, _ := x.a.p.Overlay(id); o.Body != "你好，世界" {
		t.Fatal("redo lost the typed text")
	}

	x.key('E', true)
	if x.a.export == nil {
		t.Fatal("Ctrl+E did not start an export")
	}
	x.shot("04-exporting")
	x.pump(60*time.Second, func() bool { return x.a.export == nil })
	out := strings.TrimSuffix(src, ".mp4") + ".cut.mp4"
	info, err := media.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("export result: %v (toasts %+v)", err, x.a.toasts)
	}
	if math.Abs(info.Duration-x.a.p.Duration()) > 0.15 {
		t.Fatalf("exported %v s, want %v", info.Duration, x.a.p.Duration())
	}
	if got := x.a.exports(); len(got) != 1 || got[0].Path != out {
		t.Fatalf("export not recorded: %+v", got)
	}
	x.shot("05-exported")
}

func TestAppendingStackingAndTrackVolume(t *testing.T) {
	x := newHarness(t)
	dir := x.mediaDir()
	x.open(clipFile(t, dir, "a.mp4", "testsrc2=size=640x360", "4"))
	x.frame(Input{Dropped: []string{clipFile(t, dir, "b.mp4", "color=c=blue:size=360x360", "2")}})
	x.settle(30 * time.Second)
	if len(x.a.p.Video[0].Clips) != 2 || math.Abs(x.a.p.Duration()-6) > 0.05 {
		t.Fatalf("dropping a second video should append it: %d clips, %v s", len(x.a.p.Video[0].Clips), x.a.p.Duration())
	}
	appended := x.a.p.Video[0].Clips[1].ID
	l := x.a.layout()
	tr := x.a.tracks(l.timeline)
	main, _ := tr.lane(groupVideo, 0)
	over, _ := tr.lane(groupOverlay, 0)
	// Drag the appended clip up above the main track: it moves onto a new upper video track.
	x.drag(tr.x(5), main.rect.Y+main.rect.H/2, tr.x(2), over.rect.Bottom()+x.a.u.px(10))
	k, _, _ := x.a.p.FindClip(appended)
	if k != 1 || len(x.a.p.Video) != 2 || math.Abs(x.a.p.Duration()-4) > 0.1 {
		t.Fatalf("clip on track %d of %d, duration %v", k, len(x.a.p.Video), x.a.p.Duration())
	}
	if c, _ := x.a.p.Clip(appended); math.Abs(c.Start-1) > 0.15 {
		t.Fatalf("upper clip starts at %v", c.Start)
	}
	x.a.seek(1.5)
	x.settle(10 * time.Second)
	x.shot("06-stacked")
	if top, _, _ := x.a.p.Top(1.5); top.ID != appended {
		t.Fatal("the upper clip should be on top")
	}

	tr = x.a.tracks(x.a.layout().timeline)
	hdr, _ := tr.lane(groupVideo, 0)
	x.click(gfx.Rect{X: hdr.header.X + 20, Y: hdr.header.Y + 6, W: 1, H: 1})
	if x.a.sel.kind != selTrack || x.a.sel.group != groupVideo || x.a.sel.track != 0 {
		t.Fatalf("header click selected %+v", x.a.sel)
	}
	x.frame(Input{})
	tr = x.a.tracks(x.a.layout().timeline)
	hdr, _ = tr.lane(groupVideo, 0)
	slider := gfx.Rect{X: hdr.header.X + x.a.u.px(4), Y: hdr.header.Y + x.a.u.px(22), W: hdr.header.W - x.a.u.px(42), H: x.a.u.px(20)}
	x.drag(slider.X+slider.W/2, slider.Y+slider.H/2, slider.X+x.a.u.px(7)+1, slider.Y+slider.H/2)
	if v := x.a.p.Video[0].Volume; v > 0.05 {
		t.Fatalf("dragging the track slider to the left left the volume at %v", v)
	}
	x.key('Z', true)
	if v := x.a.p.Video[0].Volume; math.Abs(v-1) > 1e-9 {
		t.Fatalf("one undo should restore the volume, got %v", v)
	}
}

func TestPastedImagesAndTabs(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	x.h.pasteImage = img
	x.a.seek(2)
	x.key('V', true)
	if x.a.sel.kind != selOverlay {
		t.Fatalf("paste selected %+v", x.a.sel)
	}
	o, _ := x.a.p.Overlay(x.a.sel.id)
	if o.Kind != project.ImageOverlay || math.Abs(o.Aspect-2) > 1e-9 || math.Abs(o.Start-2) > 1e-9 || math.Abs(o.End-7) > 1e-9 {
		t.Fatalf("pasted overlay %+v", o)
	}
	if _, err := os.Stat(o.Image); err != nil {
		t.Fatalf("pasted image not stored: %v", err)
	}
	x.settle(10 * time.Second)
	x.a.tab = tabOverlays
	x.frame(Input{})
	x.shot("07-image")

	x.key('T', true)
	if len(x.a.docs) != 2 || x.a.p != nil {
		t.Fatal("Ctrl+T should open an empty tab")
	}
	x.open(clipFile(t, x.mediaDir(), "other.mp4", "testsrc2=size=320x240", "3"))
	if x.a.docs[0].p == x.a.docs[1].p || x.a.p.Canvas.Width != 320 {
		t.Fatal("the new tab should have its own edit")
	}
	x.shot("08-tabs")
	x.a.closeTab(1)
	if len(x.a.docs) != 1 || x.a.p == nil || x.a.p.Canvas.Width != 640 {
		t.Fatal("closing the tab should return to the first edit")
	}
}

// chipRect finds the speed chip labelled label in the transport bar by repeating the transport layout.
func (x *harness) chipRect(r gfx.Rect, label string) gfx.Rect {
	u := &x.a.u
	cx := r.X + u.px(12) + u.px(38) + u.px(12) + u.bold(13.5).Measure("00:00.00") + u.px(6) + u.face(12).Measure("/ 00:00.00") + u.px(14)
	cx += 4*u.px(36) + u.px(10) + u.px(44)
	right := r.Right() - u.px(12)
	w := min(right-u.px(200)-(cx-u.px(44))-u.px(60), u.px(8*44))
	n := float32(len(speedLabels))
	gap := u.px(4)
	cw := (w - gap*(n-1)) / n
	for i, l := range speedLabels {
		if l == label {
			return gfx.Rect{X: cx + float32(i)*(cw+gap), Y: r.Y + (r.H-u.px(32))/2 + u.px(3), W: cw, H: u.px(26)}
		}
	}
	x.t.Fatalf("no chip %q", label)
	return gfx.Rect{}
}

func TestClosingStopsBackgroundDecoders(t *testing.T) {
	x := newHarness(t)
	dir := x.mediaDir()
	path := clipFile(t, dir, "long.mp4", "testsrc2=size=1280x720", "60")
	x.frame(Input{Dropped: []string{path}})
	x.pump(20*time.Second, func() bool { return x.a.p != nil })
	if x.a.media.settled(x.a.p) {
		t.Skip("the thumbnails were already decoded; nothing left to stop")
	}
	start := time.Now()
	x.a.Close()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("a decoder still holds the video after Close: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Close took %v", took)
	}
	x.a.Close()
}

func TestVideosDroppedBeforeFFmpegIsReadyOpenOnceItIs(t *testing.T) {
	x := newHarness(t)
	clip := x.fixture()
	x.a.prepareTools()
	if x.a.tools.ready {
		t.Fatal("preparing again should start from not ready")
	}
	x.frame(Input{Dropped: []string{clip}})
	if len(x.a.tools.waiting) != 1 || x.a.p != nil {
		t.Fatalf("the drop was not queued: %v", x.a.tools.waiting)
	}
	x.pump(time.Minute, func() bool { return x.a.p != nil })
	if x.a.p.Sources[0].Path != clip || len(x.a.tools.waiting) != 0 {
		t.Fatal("the queued video did not open")
	}
	if state := x.a.state(); !state.FFmpeg.Ready || state.FFmpeg.Version == "" {
		t.Fatalf("%+v", state.FFmpeg)
	}
}
