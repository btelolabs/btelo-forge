package project

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func source(path string, seconds float64) Source {
	return Source{Path: path, Duration: seconds, Width: 1920, Height: 1080, FPS: 30, HasVideo: true, HasAudio: true}
}

func sample() *Project { return New(source(`C:\v\a.mp4`, 10), Export{Quality: Balanced}) }

func TestMainTrackPacksAppendedAndSplitClips(t *testing.T) {
	p := sample()
	b := p.AddSource(source(`C:\v\b.mp4`, 4))
	if again := p.AddSource(source(`C:\v\b.mp4`, 4)); again != b {
		t.Fatal("the same file was registered twice")
	}
	p.Append(b)
	first := p.Video[0].Clips[0].ID
	if !p.SplitClip(first, 4) {
		t.Fatal("split refused")
	}
	second := p.Video[0].Clips[1].ID
	if !p.SetSpeed(second, 2) {
		t.Fatal("speed refused on the main track")
	}
	if got := p.Duration(); !near(got, 4+3+4) {
		t.Fatalf("duration %v, want 11", got)
	}
	starts := []float64{0, 4, 7}
	for i, c := range p.Video[0].Clips {
		if !near(c.Start, starts[i]) {
			t.Fatalf("clip %d starts at %v, want %v", i, c.Start, starts[i])
		}
	}
	c, src, ok := p.Top(5)
	if !ok || c.ID != second || !near(src, 6) {
		t.Fatalf("Top(5) = %+v %v %v", c, src, ok)
	}
	if c, src, ok := p.Top(11); !ok || c.Source != b || !near(src, 4) {
		t.Fatalf("Top at the end = %+v %v %v", c, src, ok)
	}
	if !p.DeleteClip(second) || !near(p.Duration(), 8) || !near(p.Video[0].Clips[1].Start, 4) {
		t.Fatalf("deleting the middle clip should close the gap: %+v", p.Video[0].Clips)
	}
}

func TestTheLastMainClipStays(t *testing.T) {
	p := sample()
	only := p.Video[0].Clips[0].ID
	if p.DeleteClip(only) || p.MoveClip(only, 1, 0) {
		t.Fatal("the last main-track clip was removed")
	}
	if p.SplitClip(only, 0.05) || p.SplitClip(only, 9.97) {
		t.Fatal("split left a sliver")
	}
}

func TestUpperTracksCoverAndKeepClear(t *testing.T) {
	p := sample()
	b := p.AddSource(source(`C:\v\b.mp4`, 3))
	x := p.Place(b, 2)
	y := p.Place(b, 4) // overlaps x, so goes one track higher
	if len(p.Video) != 3 {
		t.Fatalf("%d tracks, want main plus two", len(p.Video))
	}
	if c, _, _ := p.Top(4.5); c.ID != y {
		t.Fatalf("the highest clip should show, got %d", c.ID)
	}
	if c, _, _ := p.Top(2.5); c.ID != x {
		t.Fatalf("got %d at 2.5", c.ID)
	}
	if !p.MoveClip(y, 1, 4) {
		t.Fatal("move refused")
	}
	if c, _ := p.Clip(y); !near(c.Start, 5) || len(p.Video) != 2 {
		t.Fatalf("moving onto a busy track should slide into the free gap and drop the empty track: %+v, %d tracks", c, len(p.Video))
	}
	p.SetIn(y, 0)
	p.SetIn(y, -5)
	if c, _ := p.Clip(y); !near(c.Start, 5) || !near(c.End(), 8) {
		t.Fatalf("SetIn may not run into the clip before: %+v", c)
	}
	p.SetOut(x, 1)
	p.SetIn(y, 0)
	if c, _ := p.Clip(x); !near(c.End(), 3) {
		t.Fatalf("SetOut moved the end to %v", c.End())
	}
	if p.SetSpeed(x, 0.25) {
		t.Fatal("a slower clip that would run into its neighbour was accepted")
	}
	if !p.MoveClip(x, 0, 100) {
		t.Fatal("moving onto the main track refused")
	}
	if last := p.Video[0].Clips[len(p.Video[0].Clips)-1]; last.ID != x || !near(last.Start, 10) {
		t.Fatalf("clip should be inserted at the end of the main track: %+v", p.Video[0].Clips)
	}
}

func TestMusicFindsRoomAndTrimsWithinItsNeighbours(t *testing.T) {
	p := sample()
	a := p.AddMusic("m.mp3", "m", "", 60, 2)
	if k, i, _ := p.FindMusic(a); !near(p.Music[k].Clips[i].Length, 8) {
		t.Fatal("music should fill to the video end")
	}
	b := p.AddMusic("n.mp3", "n", "", 60, 4)
	if k, _, _ := p.FindMusic(b); k != 1 {
		t.Fatalf("overlapping music went to track %d", k)
	}
	if !p.SplitMusic(a, 5) {
		t.Fatal("split refused")
	}
	k, _, _ := p.FindMusic(a)
	right := p.Music[k].Clips[1]
	if !near(right.Offset, 3) || !near(right.Start, 5) {
		t.Fatalf("right half %+v", right)
	}
	p.TrimMusicEnd(a, 9)
	if k, i, _ := p.FindMusic(a); !near(p.Music[k].Clips[i].End(), 5) {
		t.Fatal("trimming may not run into the next clip")
	}
	p.TrimMusicStart(right.ID, 0)
	if k, i, _ := p.FindMusic(right.ID); !near(p.Music[k].Clips[i].Start, 5) {
		t.Fatalf("left trim crossed the previous clip: %+v", p.Music[k].Clips[i])
	}
	p.DeleteMusic(b)
	if len(p.Music) != 1 {
		t.Fatalf("empty music track kept: %d", len(p.Music))
	}
}

func TestOverlaysStackOnTracksAndCaptionsGetTheirOwn(t *testing.T) {
	p := sample()
	a := p.AddText("a", 1)
	img := p.AddImage("x.png", 2, 2)
	if k, _, _ := p.FindOverlay(img); k != 1 {
		t.Fatalf("overlapping image went to track %d", k)
	}
	if got := p.Visible(2.5); len(got) != 2 || got[0].ID != a || got[1].ID != img {
		t.Fatalf("visible %+v", got)
	}
	p.SetOverlayRange(a, -1, 100)
	if o, _ := p.Overlay(a); !near(o.Start, 0) || !near(o.End, 100) {
		t.Fatalf("range %+v", o)
	}
	p.UpdateOverlay(a, func(o *Overlay) { o.Body, o.X, o.Start = "b", 3, 50 })
	if o, _ := p.Overlay(a); o.Body != "b" || o.X != 1 || o.Start != 0 {
		t.Fatalf("update %+v", o)
	}
	k := p.AddCaptions([]Overlay{{Body: "hi", Start: 1, End: 2}, {Body: "x", Start: 3, End: 3.01}})
	if k != len(p.Overlays)-1 || len(p.Overlays[k].Items) != 1 || !p.Overlays[k].Items[0].Box {
		t.Fatalf("captions track %+v", p.Overlays[k])
	}
	if end := p.AddText("late", 9.5); true {
		if o, _ := p.Overlay(end); !near(o.End, 10) || !near(o.Start, 9.5) {
			t.Fatalf("a caption near the end stops with the video: %+v", o)
		}
	}
}

func TestTrackVolumeAndClone(t *testing.T) {
	p := sample()
	c := p.Clone()
	p.SetTrackVolume(false, 0, 5, true)
	p.SetTrackVolume(true, 0, 0.5, false)
	if p.Video[0].Volume != 2 || !p.Video[0].Muted || p.Music[0].Volume != 0.5 {
		t.Fatalf("track volume %+v %+v", p.Video[0], p.Music[0])
	}
	if c.Video[0].Muted || Equal(c, p) {
		t.Fatal("clone shares state")
	}
}

func TestHistoryUndoesAndRedoes(t *testing.T) {
	p := sample()
	var h History
	before := p.Clone()
	p.SplitClip(p.Video[0].Clips[0].ID, 5)
	h.Commit(before)
	back := h.Undo(p)
	if len(back.Video[0].Clips) != 1 {
		t.Fatal("undo did not restore one clip")
	}
	if fwd := h.Redo(back); len(fwd.Video[0].Clips) != 2 || h.CanRedo() {
		t.Fatal("redo did not restore the split")
	}
}

func TestSaveLoadRoundTripsAndRejectsAChangedSource(t *testing.T) {
	p := sample()
	p.SplitClip(p.Video[0].Clips[0].ID, 3)
	p.Place(p.AddSource(source(`C:\v\b.mp4`, 2)), 1)
	p.AddText("你好", 1)
	p.AddImage("x.png", 1.5, 2)
	p.AddMusic("m.mp3", "m", "credit", 30, 0)
	path := SavePath(t.TempDir(), p.Sources[0].Path)
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, p.Sources[0])
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(got, p) {
		t.Fatalf("round trip changed the project:\n%+v\n%+v", got, p)
	}
	changed := p.Sources[0]
	changed.Duration = 12
	if _, err := Load(path, changed); err == nil {
		t.Fatal("loaded an edit for a different source")
	}
}

func TestExportReadsTheOldCodecField(t *testing.T) {
	var e Export
	if err := json.Unmarshal([]byte(`{"Height":720,"Quality":2,"HEVC":true}`), &e); err != nil {
		t.Fatal(err)
	}
	if e != (Export{Height: 720, Quality: Small, Codec: HEVC}) {
		t.Fatalf("%+v", e)
	}
	data, _ := json.Marshal(Export{Codec: AV1, FPS: 30, TargetMB: 9.5, Audio: AudioVoice})
	var back Export
	if err := json.Unmarshal(data, &back); err != nil || back != (Export{Codec: AV1, FPS: 30, TargetMB: 9.5, Audio: AudioVoice}) {
		t.Fatalf("%s → %+v %v", data, back, err)
	}
	if (Export{Quality: 9}).Valid() || (Export{TargetMB: -1}).Valid() || !(Export{}).Valid() {
		t.Fatal("Valid")
	}
}

func TestBatchEditsSkipWhatDoesNotApply(t *testing.T) {
	p := New(Source{Path: "a.mp4", Duration: 10, Width: 640, Height: 360, FPS: 30, HasVideo: true, HasAudio: true}, Export{})
	main := p.Video[0].Clips[0].ID
	p.SplitClip(main, 5)
	second := p.Video[0].Clips[1].ID
	text := p.AddText("hi", 1)
	music := p.AddMusic("m.mp3", "m", "", 20, 0)
	if n := p.SetVolumes([]int{main, text, music}, 0.4); n != 2 {
		t.Fatalf("volume changed %d items", n)
	}
	if v, _ := p.Volume(music); v != 0.4 {
		t.Fatal(v)
	}
	if n := p.SetOpacity([]int{main, text, music}, 1.7); n != 2 {
		t.Fatalf("opacity changed %d items", n)
	}
	if o, _ := p.Opacity(text); o != 1 {
		t.Fatalf("opacity not clamped: %v", o)
	}
	p.SetOpacity([]int{text}, 0.25)
	if o, _ := p.Overlay(text); o.Opacity != 0.25 {
		t.Fatal(o.Opacity)
	}
	if _, err := p.DeleteItems([]int{main, second, text}); !errors.Is(err, ErrMainTrackEmpty) || !p.Has(text) {
		t.Fatalf("deleting the whole main track: %v", err)
	}
	if n, err := p.DeleteItems([]int{second, text, music, music, 999}); err != nil || n != 3 || p.Has(text) || p.Has(music) || !p.Has(main) {
		t.Fatalf("deleted %d: %v", n, err)
	}
}

func TestVersionTwoEditsLoadOpaque(t *testing.T) {
	src := Source{Path: "a.mp4", Duration: 10, Width: 640, Height: 360, FPS: 30, HasVideo: true}
	p := New(src, Export{})
	p.AddText("hi", 1)
	for k := range p.Video[0].Clips {
		p.Video[0].Clips[k].Opacity = 0
	}
	p.Overlays[0].Items[0].Opacity = 0
	data, _ := json.Marshal(saved{Version: 2, Project: p})
	path := filepath.Join(t.TempDir(), "v2.json")
	os.WriteFile(path, data, 0o644)
	got, err := Load(path, src)
	if err != nil {
		t.Fatal(err)
	}
	if got.Video[0].Clips[0].Opacity != 1 || got.Overlays[0].Items[0].Opacity != 1 {
		t.Fatal("a version 2 edit did not load opaque")
	}
}

func TestDrawingsCoverTheFrameAndKeepTheirCanvas(t *testing.T) {
	p := sample()
	id := p.AddDrawing(`C:\d\Drawing.png`, `C:\d\Drawing.btcanvas`, 16.0/9, 2)
	o, ok := p.Overlay(id)
	if !ok || o.Kind != ImageOverlay || o.Canvas != `C:\d\Drawing.btcanvas` || o.Size != 1 || o.X != 0.5 || o.Y != 0.5 {
		t.Fatalf("drawing %+v", o)
	}
	// Moving or resizing it keeps the link to its canvas.
	p.UpdateOverlay(id, func(o *Overlay) { o.X, o.Size = 0.4, 0.5 })
	if o, _ := p.Overlay(id); o.Canvas == "" {
		t.Fatal("an update dropped the canvas")
	}
	path := filepath.Join(t.TempDir(), "p.json")
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, p.Sources[0])
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := got.Overlay(id); o.Canvas != `C:\d\Drawing.btcanvas` {
		t.Errorf("loaded drawing %+v", o)
	}
	// A plain image has no canvas, and its saved form says nothing of one.
	img := p.AddImage("x.png", 1, 8)
	if o, _ := p.Overlay(img); o.Canvas != "" {
		t.Errorf("an image has a canvas %q", o.Canvas)
	}
	data, _ := json.Marshal(p.Overlays)
	if n := strings.Count(string(data), `"Canvas"`); n != 1 {
		t.Errorf("Canvas is written %d times", n)
	}
}
