package ui

import (
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// fileOpsLog is what the fake host's file operations were asked to do.
type fileOpsLog struct {
	copied  []string
	trashed []string
}

var fakeFileOps = map[*fakeHost]*fileOpsLog{}

func (h *fakeHost) ops() *fileOpsLog {
	if fakeFileOps[h] == nil {
		fakeFileOps[h] = &fileOpsLog{}
	}
	return fakeFileOps[h]
}

func (h *fakeHost) SetClipboardFiles(paths []string) error {
	h.ops().copied = slices.Clone(paths)
	return nil
}

func (h *fakeHost) Trash(paths []string) error {
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	h.ops().trashed = append(h.ops().trashed, paths...)
	return nil
}

func writeTestPNG(t *testing.T, path string, w, h int, fill func(x, y int) color.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, fill(x, y))
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// assetFolder fills a folder with one file of every kind the asset manager shows.
func assetFolder(t *testing.T, x *harness) string {
	t.Helper()
	dir := filepath.Join(x.dir, "素材 folder")
	os.MkdirAll(filepath.Join(dir, "sub folder"), 0o755)
	writeTestPNG(t, filepath.Join(dir, "sunset.png"), 960, 540, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(255 - y*120/540), uint8(90 + y*60/540), uint8(60 + x*140/960), 255}
	})
	writeTestPNG(t, filepath.Join(dir, "portrait.png"), 400, 600, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(40 + x/4), uint8(120 + y/8), 200, 255}
	})
	writeTestPNG(t, filepath.Join(dir, "logo.png"), 300, 300, func(x, y int) color.NRGBA {
		dx, dy := x-150, y-150
		if dx*dx+dy*dy < 120*120 {
			return color.NRGBA{255, 140, 40, 255}
		}
		return color.NRGBA{}
	})
	g := &gif.GIF{}
	for i := range 6 {
		f := image.NewPaletted(image.Rect(0, 0, 160, 120), palette.Plan9)
		for k := range f.Pix {
			f.Pix[k] = uint8((k/160 + i*20) % 200)
		}
		g.Image, g.Delay = append(g.Image, f), append(g.Delay, 8)
	}
	gf, _ := os.Create(filepath.Join(dir, "loop.gif"))
	gif.EncodeAll(gf, g)
	gf.Close()
	clip := clipFile(t, dir, "holiday.mp4", "testsrc2=size=640x360", "4")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Project notes\n\nA **bold** start with `code` and a [link](https://example.com).\n\n"+
		"## List\n\n- first item\n- second item, which is long enough to wrap onto a second line in a narrow view\n  1. nested\n\n"+
		"> A quote that matters.\n\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\n\n| Name | 大小 |\n|---|---|\n| a | 1 |\n| 中文 | 22 |\n\n---\n\n中文段落，可以在任意字符处换行，看看排版是否正确。\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\n// main says hello.\nfunc main() {\n\tn := 42 /* the answer */\n\tfmt.Println(\"hello\", n)\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("Plain notes\nsecond line\n"), 0o644)
	d, err := paint.NewFramed(320, 200, "Layer", color.NRGBA{60, 160, 120, 255})
	if err != nil {
		t.Fatal(err)
	}
	cf, _ := os.Create(filepath.Join(dir, "drawing"+paint.Ext))
	d.Snapshot().Save(cf)
	cf.Close()
	src := project.Source{Path: clip, Duration: 4, Width: 640, Height: 360, FPS: 30, HasVideo: true, HasAudio: true}
	if err := project.WriteFile(filepath.Join(dir, "cut"+project.EditExt), project.New(src, project.Export{})); err != nil {
		t.Fatal(err)
	}
	return dir
}

// cardOf is where the picture of the card of the file called name is on the screen, in the first list.
func (x *harness) cardOf(name string) gfx.Rect {
	x.t.Helper()
	u := &x.a.u
	p := x.a.files.panes[0]
	for k, r := range p.rows {
		if p.entries[r].Name == name && k < len(p.g.layout.cards) {
			body := x.a.layout().body
			pad := u.gridPad()
			cr := p.g.layout.cards[k]
			return gfx.Rect{X: body.X + pad + cr.X, Y: body.Y + u.px(38) + p.g.barH + pad + cr.Y - p.g.scroll,
				W: cr.W, H: cr.H - u.assetLabelH()}
		}
	}
	x.t.Fatalf("no card for %s", name)
	return gfx.Rect{}
}

// hoverKey presses a key with the pointer over a card.
func (x *harness) hoverKey(r gfx.Rect, code uint16) {
	x.mx, x.my = r.X+r.W/2, r.Y+r.H/2
	x.frame(Input{})
	x.frame(Input{Keys: []Key{{Code: code}}})
}

// chipOf is where the chip of a category or tag is, as the last frame drew it.
func (x *harness) chipOf(name string) gfx.Rect {
	x.t.Helper()
	for _, t := range x.a.files.panes[0].g.targets {
		if t.name == name {
			return t.rect
		}
	}
	x.t.Fatalf("no chip %s", name)
	return gfx.Rect{}
}

// openGrid shows dir as pictures and waits for its pictures.
func openGrid(t *testing.T, x *harness, dir string) *filesPane {
	t.Helper()
	a := x.a
	p := a.openAssets(dir)
	x.pump(time.Minute, func() bool {
		return !p.reading && !a.thumbsBusy() && len(a.fileThumbs.wanted) == 0
	})
	x.settle(30 * time.Second)
	return p
}

func TestGridBrowsesTagsAndActsOnHover(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir := assetFolder(t, x)
	p := openGrid(t, x, dir)
	fd := a.files
	if fd == nil || !p.grid || len(fd.panes) != 1 {
		t.Fatal("no file manager tab showing pictures")
	}
	if len(p.rows) != 11 {
		t.Fatalf("%d items: %v", len(p.rows), names(p))
	}
	if !p.entries[p.rows[0]].Dir {
		t.Fatal("the folder is not first")
	}
	x.shot("assets-grid")

	// The list of keys shows what acts on the item under the pointer.
	video := x.cardOf("holiday.mp4")
	x.mx, x.my = video.X+10, video.Y+10
	x.frame(Input{})
	x.frame(Input{})
	play := "space " + a.u.t("play")
	if fd.hovered != filepath.Join(dir, "holiday.mp4") || !slices.Contains(a.itemKeys(fd.hovered), play) {
		t.Fatalf("hovering the video: %q, keys %v", fd.hovered, a.itemKeys(fd.hovered))
	}
	x.shot("assets-hover-keys")

	// t over a card tags it.
	x.hoverKey(x.cardOf("sunset.png"), 'T')
	if fd.prompt == nil || fd.prompt.kind != promptTag {
		t.Fatal("t did not ask for a tag")
	}
	x.typeText("Warm")
	x.press(keyEnter, "")
	if tags := a.assetLibrary().Of(filepath.Join(dir, "sunset.png")).Tags; !slices.Equal(tags, []string{"Warm"}) {
		t.Fatalf("tags %v", tags)
	}
	// The tag in use is offered for the next file.
	x.hoverKey(x.cardOf("portrait.png"), 'T')
	if fd.prompt == nil || !slices.Equal(fd.prompt.matches, []string{"Warm"}) {
		t.Fatalf("the tag in use is not offered: %+v", fd.prompt)
	}
	x.press(keyDown, "")
	x.press(keyEnter, "")
	if tags := a.assetLibrary().Of(filepath.Join(dir, "portrait.png")).Tags; !slices.Equal(tags, []string{"Warm"}) {
		t.Fatalf("picked tag: %v", tags)
	}

	// A category, and a file dragged onto its chip.
	if _, err := a.assetLibrary().NewCategory("Brand"); err != nil {
		t.Fatal(err)
	}
	a.assetLibVersion++
	x.frame(Input{})
	logo := x.cardOf("logo.png")
	chip := x.chipOf("Brand")
	x.drag(logo.X+logo.W/2, logo.Y+logo.H/2, chip.X+chip.W/2, chip.Y+chip.H/2)
	x.frame(Input{})
	if c := a.assetLibrary().Of(filepath.Join(dir, "logo.png")).Categories; !slices.Equal(c, []string{"Brand"}) {
		t.Fatalf("categories %v after the drag", c)
	}
	// The category's chip filters the grid; Esc shows everything again.
	x.click(x.chipOf("Brand"))
	if got := names(p); !slices.Equal(got, []string{"logo.png"}) {
		t.Fatalf("the category shows %v", got)
	}
	x.shot("assets-category")
	x.mx, x.my = 5, 300
	x.press(keyEscape, "")
	if len(p.rows) != 11 {
		t.Fatalf("Esc left %d items", len(p.rows))
	}
	// The tag's chip, and the search by tag.
	x.click(x.chipOf("Warm"))
	if got := names(p); len(got) != 2 || !slices.Contains(got, "portrait.png") || !slices.Contains(got, "sunset.png") {
		t.Fatalf("the tag shows %v", got)
	}
	x.mx, x.my = 5, 300
	x.press(keyEscape, "")
	x.press(keySlash, "")
	if a.u.focus != filterBoxID(p) {
		t.Fatalf("/ did not focus the search: %q", a.u.focus)
	}
	x.typeText("warm")
	if len(p.rows) != 2 {
		t.Fatalf("searching the tag shows %v", names(p))
	}
	a.u.focus = ""
	p.g.search = ""
	a.arrange(p)

	// c copies the file under the pointer, for here and for other programs; y copies its path.
	x.hoverKey(x.cardOf("main.go"), 'C')
	if got := x.h.ops().copied; !slices.Equal(got, []string{filepath.Join(dir, "main.go")}) {
		t.Fatalf("copied %v", got)
	}
	a.toasts = nil
	x.hoverKey(x.cardOf("main.go"), 'Y')
	if x.h.clip != filepath.Join(dir, "main.go") {
		t.Fatalf("clipboard %q", x.h.clip)
	}
	if len(a.toasts) != 1 || a.toasts[0].text != a.u.tf("Copied %s", filepath.Join(dir, "main.go")) {
		t.Fatalf("copying the path said %+v, want one message with the whole path", a.toasts)
	}
	x.press(keyEscape, "")

	// Space previews beside the grid: Markdown, code, a picture, a video, which plays at once.
	for _, name := range []string{"README.md", "main.go", "sunset.png", "holiday.mp4", "loop.gif"} {
		x.hoverKey(x.cardOf(name), keySpace)
		if !fd.preview || a.previewPath(fd) != filepath.Join(dir, name) {
			t.Fatalf("Space did not preview %s", name)
		}
		x.pump(20*time.Second, func() bool { return a.preview.path != "" && !a.preview.loading })
		x.frame(Input{})
		if a.preview.problem != "" {
			t.Fatalf("%s: %s", name, a.preview.problem)
		}
		if name == "holiday.mp4" {
			if !a.previewPlaying() {
				t.Fatal("the video does not play when previewed")
			}
			x.pump(10*time.Second, func() bool { _, ok := a.previewPlayer.Frame(); return ok })
			if _, ok := a.previewPlayer.Frame(); !ok {
				t.Fatal("the video plays no picture")
			}
			x.frame(Input{})
		}
		x.shot("assets-preview-" + strings.ReplaceAll(name, ".", "-"))
		x.press(keySpace, "")
		if fd.preview {
			t.Fatal("Space did not close the preview")
		}
		x.frame(Input{}) // the grid takes the width back
	}

	// q shows the rows, and pictures again, the cursor on the same file.
	p.cursorTo("logo.png")
	x.mx, x.my = 5, 300
	x.press('Q', "")
	if p.grid || p.current().Name != "logo.png" {
		t.Fatalf("q: grid %v, on %s", p.grid, p.current().Name)
	}
	x.press('Q', "")
	if !p.grid || p.current().Name != "logo.png" {
		t.Fatalf("q again: grid %v, on %s", p.grid, p.current().Name)
	}

	// A double click on the folder goes in; Backspace comes out onto it (h, in pictures, is the picture before).
	x.frame(Input{})
	sub := x.cardOf("sub folder")
	x.mx, x.my = sub.X+sub.W/2, sub.Y+sub.H/2
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true, Double: true}}})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	x.waitFiles()
	if filepath.Base(p.dir) != "sub folder" {
		t.Fatalf("in %s", p.dir)
	}
	x.mx, x.my = 5, 300
	x.press(keyBack, "")
	x.waitFiles()
	if p.dir != filepath.Clean(dir) || p.current() == nil || p.current().Name != "sub folder" {
		t.Fatalf("back in %s on %v", p.dir, p.current())
	}
}

func TestGridMovesByRowsAndKeepsTagsThroughRenamesAndTheBin(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir := assetFolder(t, x)
	p := openGrid(t, x, dir)
	fd := a.files
	x.mx, x.my = 5, 300
	// ← and → go along the cards; ↓ and ↑ a row of cards.
	x.press(keyHome, "")
	x.press(keyRight, "")
	if p.cursor != 1 {
		t.Fatalf("→ went to %d", p.cursor)
	}
	x.press(keyLeft, "")
	if p.cursor != 0 {
		t.Fatalf("← went to %d", p.cursor)
	}
	x.press(keyDown, "")
	if g := p.g.layout.cards; p.cursor == 0 || g[p.cursor].Y <= g[0].Y {
		t.Fatalf("↓ stayed in the row: %d", p.cursor)
	}
	x.press(keyUp, "")
	if g := p.g.layout.cards; g[p.cursor].Y != g[0].Y {
		t.Fatalf("↑ did not come back to the first row: %d", p.cursor)
	}

	// A file renamed keeps its tags.
	sunset := filepath.Join(dir, "sunset.png")
	a.addTag([]string{sunset}, "Warm")
	p.cursorTo("sunset.png")
	x.press('R', "")
	if fd.prompt == nil || fd.prompt.kind != promptRename {
		t.Fatal("r did not ask for a name")
	}
	fd.prompt.text = "dusk.png"
	x.press(keyEnter, "")
	x.waitFiles()
	dusk := filepath.Join(dir, "dusk.png")
	if !exists(dusk) || !slices.Equal(a.assetLibrary().Of(dusk).Tags, []string{"Warm"}) {
		t.Fatalf("renamed: there %v, tags %v", exists(dusk), a.assetLibrary().Of(dusk).Tags)
	}

	// To the Recycle Bin and back: the tags go and come back with the file.
	p.cursorTo("dusk.png")
	x.press('D', "")
	if fd.confirm == nil {
		t.Fatal("d did not ask")
	}
	if !fd.confirm.canBin {
		t.Skip("the test folder is on a drive without a Recycle Bin")
	}
	x.press(keyEnter, "")
	x.waitFiles()
	if exists(dusk) || len(a.assetLibrary().Tags()) != 0 {
		t.Fatalf("binned: there %v, tags %v", exists(dusk), a.assetLibrary().Tags())
	}
	x.press('U', "")
	x.waitFiles()
	if !exists(dusk) || !slices.Equal(a.assetLibrary().Of(dusk).Tags, []string{"Warm"}) {
		t.Fatalf("back from the bin: there %v, tags %v", exists(dusk), a.assetLibrary().Of(dusk).Tags)
	}
}

func TestPreviewGoesOnWhereTheVideoWasLeft(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir := t.TempDir()
	clip := clipFile(t, dir, "long.mp4", "testsrc2=size=320x180", "12")
	fd := a.newFilesTab(dir, false)
	x.waitFiles()
	fd.pane().cursorTo("long.mp4")
	x.mx, x.my = 5, 5
	open := func() {
		x.press(keySpace, "")
		x.pump(20*time.Second, func() bool { return a.preview.path == clip && !a.preview.loading })
		x.frame(Input{})
	}
	open()
	if !a.previewPlaying() {
		t.Fatal("the video does not play when previewed")
	}
	a.seekPreview(6)
	x.press(keySpace, "") // closes the preview, keeping the place
	open()
	if at := a.previewPosition(); at < 5.5 || at > 8 {
		t.Fatalf("the video goes on from %.2f, want about 6", at)
	}
	// Kept across runs, in the state folder.
	x.press(keySpace, "")
	a.positions = nil
	if at := a.resumeAt(clip, 12); at < 5.5 || at > 8 {
		t.Fatalf("read back %.2f", at)
	}
	// Seen to the end, it starts over.
	open()
	a.seekPreview(11.5)
	x.press(keySpace, "")
	if at := a.resumeAt(clip, 12); at != 0 {
		t.Fatalf("after the end it starts at %.2f", at)
	}
}
