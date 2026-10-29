package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Text pasted names files when every line is a full path to something there, quoted or not, or a file:// address;
// anything else is words.
func TestPastedPaths(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a b.mp4"), filepath.Join(dir, "c.mov")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	address := "file://" + filepath.ToSlash(a)
	if runtime.GOOS == "windows" {
		address = "file:///" + filepath.ToSlash(a)
	}
	address = strings.ReplaceAll(address, " ", "%20")
	for _, c := range []struct {
		text string
		want []string
	}{
		{a, []string{a}},
		{`"` + a + `"`, []string{a}},
		{"  '" + b + "'  \r\n", []string{b}},
		{`"` + a + "\"\r\n\"" + b + `"`, []string{a, b}},
		{address, []string{a}},
		{dir, []string{dir}},
		{"a caption", nil},
		{a + "\nand words", nil},
		{filepath.Join(dir, "gone.mp4"), nil},
		{"c.mov", nil},
		{"", nil},
	} {
		if got := pastedPaths(c.text); !slices.Equal(got, c.want) {
			t.Errorf("%q gave %q, want %q", c.text, got, c.want)
		}
	}
}

// The editor with no video lists the folders and videos of a folder, nothing else: a click on a folder goes into
// it, the button up comes back, a click on a video edits it. The folder is kept for the next time.
func TestEmptyEditorListsVideos(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir := x.mediaDir()
	inner := filepath.Join(dir, "Holiday")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	clipFile(t, inner, "beach.mp4", "testsrc2=size=640x360", "2")
	for _, name := range []string{"notes.txt", "song.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	other := clipFile(t, dir, "intro.mp4", "testsrc2=size=640x360", "2")
	a.settings.Files.VideoDir = dir
	a.newEditor()
	shown := func() []string {
		p := a.videoListPane()
		var out []string
		for _, r := range p.rows {
			out = append(out, p.entries[r].Name)
		}
		return out
	}
	x.pump(10*time.Second, func() bool { x.frame(Input{}); return len(shown()) == 2 })
	if got := shown(); !slices.Equal(got, []string{"Holiday", "intro.mp4"}) {
		t.Fatalf("the list shows %q", got)
	}
	x.frame(Input{})
	x.shot(string(a.u.lang) + "-editor-videos")
	card := func(name string) gfx.Rect {
		t.Helper()
		e := a.emptyEditorLayout(a.layout())
		p := a.videoListPane()
		g := a.layoutGrid(p, e.list.W-a.u.px(22))
		for k, r := range p.rows {
			if p.entries[r].Name == name {
				c := g.cards[k]
				return gfx.Rect{X: e.list.X + a.u.px(8) + c.X, Y: e.list.Y + a.u.px(8) + c.Y - p.g.scroll, W: c.W, H: c.H}
			}
		}
		t.Fatalf("no card for %s", name)
		return gfx.Rect{}
	}
	x.click(card("Holiday")) // the first card: the ones after it are not drawn from the folder left
	x.pump(10*time.Second, func() bool { x.frame(Input{}); return slices.Equal(shown(), []string{"beach.mp4"}) })
	if crashes, _ := os.ReadDir(a.crashDir()); len(crashes) > 0 {
		report, _ := os.ReadFile(filepath.Join(a.crashDir(), crashes[0].Name()))
		t.Fatalf("the app failed:\n%s", report)
	}
	if a.settings.Files.VideoDir != inner {
		t.Errorf("the folder kept is %q", a.settings.Files.VideoDir)
	}
	x.click(a.emptyEditorLayout(a.layout()).up)
	x.pump(10*time.Second, func() bool { x.frame(Input{}); return len(shown()) == 2 })
	if a.videoListPane().dir != dir || a.settings.Files.VideoDir != dir {
		t.Fatalf("up went to %q", a.videoListPane().dir)
	}
	x.click(card("intro.mp4"))
	x.pump(time.Minute, func() bool { return a.p != nil && a.opening == "" })
	if a.p == nil || len(a.p.Sources) != 1 || a.p.Sources[0].Path != other {
		t.Fatalf("the click did not edit the video: %v", a.p)
	}
}

// Ctrl+V in the editor with no video opens the video whose path was copied, as text.
func TestPastingAVideoPathOpensIt(t *testing.T) {
	x := newHarness(t)
	a := x.a
	clip := x.fixture()
	a.newEditor()
	x.frame(Input{})
	x.h.pasteText = `"` + clip + `"`
	x.press('V', "c")
	x.pump(time.Minute, func() bool { return a.p != nil && a.opening == "" })
	if a.p == nil || len(a.p.Sources) != 1 || a.p.Sources[0].Path != clip {
		t.Fatalf("the pasted path did not open: %v", a.p)
	}
}
