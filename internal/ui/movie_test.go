package ui

import (
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/playback"
)

// playing opens path as a video to watch and waits until it plays.
func (x *harness) playing(path string) *movieDoc {
	x.t.Helper()
	x.a.openAsset(path)
	m := x.a.movie
	if m == nil {
		x.t.Fatalf("%s did not open in the player", path)
	}
	x.pump(30*time.Second, func() bool { return m.problem != "" || m.playing() && m.shown != nil })
	if m.problem != "" {
		x.t.Fatal(m.problem)
	}
	return m
}

// lit counts the pixels of r on the screen that are not black.
func (x *harness) lit(r [4]int) int {
	n := 0
	for y := r[1]; y < r[3]; y += 4 {
		for xx := r[0]; xx < r[2]; xx += 4 {
			o := x.c.Img.PixOffset(xx, y)
			if p := x.c.Img.Pix[o : o+3]; p[0] > 24 || p[1] > 24 || p[2] > 24 {
				n++
			}
		}
	}
	return n
}

// A video opened to be watched plays in a tab of its own at once, its picture fitted to the tab and decoded at that
// size; Space pauses it with the picture kept, the arrows seek, and the place is kept for the next time.
func TestAVideoOpenedPlaysInThePlayer(t *testing.T) {
	x := newHarness(t)
	a := x.a
	path := clipFile(t, x.mediaDir(), "watch.mp4", "testsrc2=size=640x360", "12")
	tabs := len(a.docs)
	m := x.playing(path)
	if len(a.docs) != tabs || a.p != nil || a.doc.title(a) != "watch.mp4" {
		t.Fatalf("the video opened in %d tabs, edit %v, titled %q", len(a.docs), a.p != nil, a.doc.title(a))
	}
	l := a.layout()
	if m.shown.Rect.Dx() < 640 || m.playSize != m.shown.Rect.Size() {
		t.Fatalf("pictures of %v for a view of %v", m.shown.Rect.Size(), m.playSize)
	}
	x.pump(10*time.Second, func() bool { return m.position() > 0.6 })
	x.shot("player-playing")
	mid := l.body.Inset(200)
	if n := x.lit([4]int{int(mid.X), int(mid.Y), int(mid.Right()), int(mid.Bottom())}); n < 1000 {
		t.Fatalf("the picture is black: %d pixels lit", n)
	}

	x.key(keySpace, false)
	if m.playing() {
		t.Fatal("Space did not pause")
	}
	at := m.position()
	time.Sleep(200 * time.Millisecond)
	x.frame(Input{})
	if m.position() != at || m.shown == nil {
		t.Fatalf("paused at %v, now at %v, picture %v", at, m.position(), m.shown != nil)
	}
	x.key(keyRight, false)
	if math.Abs(m.at-(at+movieStep)) > 1e-9 || m.playing() {
		t.Fatalf("→ from %v went to %v, playing %v", at, m.at, m.playing())
	}
	x.pump(10*time.Second, func() bool { return m.still != nil && m.stillAt == m.at && m.stillExact })
	if m.shown != m.still {
		t.Fatal("the picture where it was moved to is not shown")
	}
	x.key(keyPeriod, false)
	if want := (math.Floor((at+movieStep)*30) + 1.5) / 30; math.Abs(m.at-want) > 1e-9 {
		t.Fatalf(". stepped to %v, want %v", m.at, want)
	}
	left := m.at

	// Closed and opened again, it goes on from where it was left.
	a.closeTab(a.current())
	m = x.playing(path)
	if math.Abs(m.position()-left) > 0.5 {
		t.Fatalf("opened again at %v, left at %v", m.position(), left)
	}
	if len(a.toasts) == 0 {
		t.Error("going on from where it was left was not said")
	}
}

// Edit turns the player into the video editor, its playhead on the picture watched.
func TestThePlayersEditButtonEditsFromThePictureWatched(t *testing.T) {
	x := newHarness(t)
	a := x.a
	path := clipFile(t, x.mediaDir(), "edit.mp4", "testsrc2=size=640x360", "8")
	m := x.playing(path)
	x.key(keySpace, false)
	a.seekMovie(m, 3.5)
	x.pump(10*time.Second, func() bool { return m.still != nil && m.stillAt == 3.5 && m.stillExact && m.shown == m.still })
	d := a.doc
	x.shot("player-paused")
	x.click(x.buttonRect("movie/edit"))
	if a.doc != d || a.movie != nil || a.p == nil {
		t.Fatalf("Edit: same tab %v, player %v, edit %v", a.doc == d, a.movie != nil, a.p != nil)
	}
	if math.Abs(a.playhead-3.5) > 1e-6 || a.p.Sources[0].Path != path {
		t.Fatalf("the editor opened %s at %v", a.p.Sources[0].Path, a.playhead)
	}
	x.settle(30 * time.Second)
	x.shot("player-edited")
}

// buttonRect finds the control id on the player's bar, from the right, by pointing along the bar until its hint
// comes up, which names the control and where it is.
func (x *harness) buttonRect(id string) gfx.Rect {
	x.t.Helper()
	l := x.a.layout()
	y := l.body.Bottom() - x.a.u.px(22)
	for px := l.body.Right() - 2; px > l.body.X; px -= 4 {
		x.mx, x.my = px, y
		x.frame(Input{})
		if x.a.u.tip.id == id {
			return x.a.u.tip.at
		}
	}
	x.t.Fatalf("no %s on the bar", id)
	return gfx.Rect{}
}

// A full window hides the tabs, F and Esc go between them, and the volume keys set the volume kept for the next video.
func TestThePlayerFillsTheWindowAndKeepsTheVolume(t *testing.T) {
	x := newHarness(t)
	a := x.a
	m := x.playing(clipFile(t, x.mediaDir(), "full.mp4", "testsrc2=size=640x360", "6"))
	x.key('F', false)
	if !m.full {
		t.Fatal("F did not fill the window")
	}
	x.frame(Input{})
	x.shot("player-full")
	if n := x.lit([4]int{0, 0, x.c.Img.Rect.Dx(), int(x.a.u.px(30))}); n > 0 {
		// The picture is 16:9 in a 16:10 window: its top is black, where the tabs were.
		t.Fatalf("the tabs still show over a full window: %d pixels lit", n)
	}
	x.key(keyEscape, false)
	if m.full {
		t.Fatal("Esc did not give the tabs back")
	}
	// The bar over a full window takes its own clicks: the picture under it does not.
	x.key('F', false)
	playing := m.playing()
	x.click(x.buttonRect("movie/full"))
	if m.full || m.playing() != playing {
		t.Fatalf("a click on the bar's button in a full window: full %v, playing %v (was %v)", m.full, m.playing(), playing)
	}
	a.settings.Player.Volume = 0.5
	x.key(keyDown, false)
	if v := a.settings.Player.Volume; math.Abs(v-0.45) > 1e-9 || m.player.Volume() != v {
		t.Fatalf("↓ set the volume to %v, the player's to %v", v, m.player.Volume())
	}
	x.key('M', false)
	if !a.settings.Player.Muted || m.player.Volume() != 0 {
		t.Fatal("M did not mute")
	}
	a.loadSettings()
	if !a.settings.Player.Muted || math.Abs(a.settings.Player.Volume-0.45) > 1e-9 {
		t.Fatalf("the volume kept is %+v", a.settings.Player)
	}
	x.frame(Input{Keys: []Key{{Code: keyPeriod, Shift: true}}})
	if m.currentSpeed() != 1.25 || !m.playing() {
		t.Fatalf("> set the speed to %v, playing %v", m.currentSpeed(), m.playing())
	}
}

// A video dropped on an empty tab plays; dropped on an edit, it goes into the edit; a pinned player opens again.
func TestADroppedVideoPlaysUnlessAnEditTakesIt(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir := x.mediaDir()
	one := clipFile(t, dir, "one.mp4", "testsrc2=size=320x180", "3")
	two := clipFile(t, dir, "two.mp4", "testsrc2=size=320x180", "3")
	x.frame(Input{Dropped: []string{one}})
	if a.movie == nil || a.movie.path != one {
		t.Fatal("a video dropped on an empty tab did not play")
	}
	if s, ok := tabSpec(a.doc); !ok || s.Kind != tabPlayer || s.Path != one {
		t.Fatalf("a player tab is kept as %+v", s)
	}
	a.newTab()
	x.open(two)
	clips := len(a.p.Video[0].Clips)
	x.frame(Input{Dropped: []string{one}})
	x.settle(30 * time.Second)
	if len(a.p.Video[0].Clips) != clips+1 || a.movie != nil {
		t.Fatalf("a video dropped on an edit: %d clips, player %v", len(a.p.Video[0].Clips), a.movie != nil)
	}
	if filepath.Base(a.p.Sources[len(a.p.Sources)-1].Path) != "one.mp4" {
		t.Fatal("the video dropped is not in the edit")
	}
}

// A real video plays at its own frame rate in a window the size of the test's: FORGE_VIDEO names it, and the pictures
// shown in a second are counted against the file's. With FORGE_SHOTS the window is saved as it plays.
func TestRealVideoPlaysAtItsFrameRate(t *testing.T) {
	path := os.Getenv("FORGE_VIDEO")
	if path == "" {
		t.Skip("FORGE_VIDEO names no video")
	}
	x := newHarness(t)
	m := x.playing(path)
	var shown []*image.RGBA
	var draws int
	begun := time.Now()
	var drawing time.Duration
	for time.Since(begun) < 3*time.Second && m.playing() {
		d := time.Now()
		x.frame(Input{})
		drawing += time.Since(d)
		draws++
		if len(shown) == 0 || shown[len(shown)-1] != m.shown {
			shown = append(shown, m.shown)
		}
		if wait := x.a.movieWait(); wait > 0 {
			time.Sleep(wait)
		}
	}
	took := time.Since(begun).Seconds()
	rate := float64(len(shown)) / took
	t.Logf("%s: %d×%d at %.2f pictures a second, shown at %d×%d: %.1f pictures a second over %.1f s, %d frames drawn, %.1f ms a frame",
		filepath.Base(path), m.info.Width, m.info.Height, m.info.FPS, m.shown.Rect.Dx(), m.shown.Rect.Dy(), rate, took, draws,
		float64(drawing.Milliseconds())/float64(draws))
	x.shot("player-real")
	if want := min(m.info.FPS, playback.MaxFileFPS); rate < want*0.9 {
		t.Fatalf("%.1f pictures a second shown of the file's %.2f", rate, want)
	}
}
