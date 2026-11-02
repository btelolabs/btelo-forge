package ui

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// peekFolder is a file manager showing pictures, and a file that is neither a picture nor a 3D file.
func peekFolder(t *testing.T, x *harness) (*filesDoc, string) {
	t.Helper()
	dir := filepath.Join(x.dir, "peek")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.png", "b.png"} {
		writeCheckPNG(t, filepath.Join(dir, name), 320, 240, color.RGBA{200, 40, 40, 255}, color.RGBA{250, 250, 250, 255})
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	x.a.newFilesTab(dir, true)
	x.waitFiles()
	return x.a.files, dir
}

// hoverFor rests the pointer on a card until the small picture is due.
func (x *harness) hoverFor(name string) {
	x.t.Helper()
	r := x.cardOf(name)
	x.mx, x.my = r.X+r.W/2, r.Y+r.H/2
	x.frame(Input{})
	x.a.fm.peek.since = time.Now().Add(-peekDelay)
	x.frame(Input{})
}

// Space opens the preview over the lists, which take nothing while it is open; it holds the tags, and goes with Space,
// Esc, or a click outside it.
func TestSpacePreviewsOverTheLists(t *testing.T) {
	x := newHarness(t)
	a := x.a
	fd, dir := peekFolder(t, x)
	p := fd.panes[0]
	x.hoverKey(x.cardOf("a.png"), keySpace)
	if !fd.preview || a.previewPath(fd) != filepath.Join(dir, "a.png") {
		t.Fatalf("Space: preview %v of %q", fd.preview, a.previewPath(fd))
	}
	x.pump(10*time.Second, func() bool { return a.preview.path != "" && !a.preview.loading })
	x.frame(Input{})
	x.shot("files-preview-overlay")

	// The pointer on the card behind it points at nothing, and a command on what it hides does nothing.
	other := x.cardOf("b.png")
	x.mx, x.my = other.X+other.W/2, other.Y+other.H/2
	x.frame(Input{})
	x.frame(Input{})
	if fd.hovered != "" || p.current().Name != "a.png" {
		t.Fatalf("the list behind the preview took the pointer: %q, on %s", fd.hovered, p.current().Name)
	}
	x.press('D', "")
	if fd.confirm != nil {
		t.Fatal("d asked to delete what the preview shows")
	}

	// t tags what is shown, over the preview.
	x.press('T', "")
	if fd.prompt == nil || fd.prompt.kind != promptTag {
		t.Fatal("t opened no tag prompt over the preview")
	}
	x.press(keyEscape, "")
	if fd.prompt != nil || !fd.preview {
		t.Fatalf("Esc in the prompt: prompt %v, preview %v", fd.prompt != nil, fd.preview)
	}

	// The arrows go through the files with it open.
	p.cursorTo(p.entries[p.rows[0]].Name)
	x.press(keyRight, "")
	if want := p.entries[p.rows[1]].Name; !fd.preview || p.current().Name != want {
		t.Fatalf("the arrow in the preview: preview %v, on %s, want %s", fd.preview, p.current().Name, want)
	}

	// Esc closes it first, whatever else it could do.
	p.marked["a.png"] = true
	x.press(keyEscape, "")
	if fd.preview || len(p.marked) != 1 {
		t.Fatalf("Esc: preview %v, marks %v", fd.preview, p.marked)
	}
	clear(p.marked)

	// A click outside the card closes it; one in the card does not.
	x.mx, x.my = 300, 300
	x.press(keySpace, "")
	if !fd.preview {
		t.Fatal("Space did not open the preview")
	}
	x.frame(Input{})
	x.click(rectAt(300, 300, 1, 1))
	if !fd.preview {
		t.Fatal("a click in the card closed the preview")
	}
	body := a.layout().body
	x.click(rectAt(body.X+2, body.Y+2, 1, 1))
	if fd.preview {
		t.Fatal("a click outside the card did not close the preview")
	}
}

// Resting on a picture shows it small; nothing else does, it takes no click, and it goes with the pointer.
func TestPointerRestingOnAPictureShowsItSmall(t *testing.T) {
	x := newHarness(t)
	a := x.a
	fd, dir := peekFolder(t, x)
	x.hoverFor("notes.txt")
	if a.fm.peek.path != "" {
		t.Fatalf("a text showed a small picture: %q", a.fm.peek.path)
	}
	x.hoverFor("a.png")
	if a.fm.peek.path != filepath.Join(dir, "a.png") {
		t.Fatalf("resting on the picture: %q", a.fm.peek.path)
	}
	x.pump(10*time.Second, func() bool { x.frame(Input{}); return !a.thumbsBusy() })
	x.frame(Input{})
	x.shot("files-hover-peek")
	if fd.preview {
		t.Fatal("resting on a file opened the preview")
	}
	if a.peekWait() != 0 {
		t.Fatal("the small picture is shown and still waited for")
	}

	x.mx, x.my = 2, float32(x.c.Img.Rect.Dy()-2)
	x.frame(Input{})
	if a.fm.peek.path != "" {
		t.Fatal("the small picture stayed after the pointer left")
	}

	// It does not show over the preview, which has the file in full.
	x.hoverKey(x.cardOf("b.png"), keySpace)
	x.frame(Input{})
	if a.fm.peek.path != "" {
		t.Fatal("the small picture showed under the preview")
	}
}

// A 3D file that cannot be read shows its symbol and says nothing more, as the pointer only passed over it.
func TestBrokenModelPeekIsQuiet(t *testing.T) {
	x := newHarness(t)
	a := x.a
	_, dir := peekFolder(t, x)
	if err := os.WriteFile(filepath.Join(dir, "broken.glb"), []byte("not a model"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.refreshFiles(a.files)
	x.waitFiles()
	x.pump(10*time.Second, func() bool {
		a.files.panes[0].cursorTo("broken.glb")
		return len(a.files.panes[0].g.layout.cards) == 4
	})
	x.frame(Input{})
	x.hoverFor("broken.glb")
	x.pump(10*time.Second, func() bool { x.frame(Input{}); return a.fm.peek.failed })
	if len(a.toasts) != 0 {
		t.Fatalf("passing over a broken file said %+v", a.toasts)
	}

	// Its preview says there is none rather than loading for ever.
	x.press(keySpace, "")
	x.pump(10*time.Second, func() bool { x.frame(Input{}); return a.preview.path != "" && !a.preview.loading })
	x.frame(Input{})
	if a.modelPictureComing() {
		t.Fatal("the preview of a broken file still waits for its picture")
	}
}

// A real 3D file is drawn by the card and kept as its picture. Set FORGE_MODEL and FORGE_SHOTS to look at it.
func TestRealCardPeeksAModel(t *testing.T) {
	path, shots := os.Getenv("FORGE_MODEL"), os.Getenv("FORGE_SHOTS")
	if path == "" || shots == "" {
		t.Skip("set FORGE_MODEL and FORGE_SHOTS to look at a model")
	}
	realCard(t)
	x := newHarness(t)
	a := x.a
	_, dir := peekFolder(t, x)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(copied, data, 0o644); err != nil {
		t.Fatal(err)
	}
	a.refreshFiles(a.files)
	x.waitFiles()
	x.pump(10*time.Second, func() bool { return len(a.files.panes[0].g.layout.cards) == 4 })
	x.frame(Input{})
	x.hoverFor(filepath.Base(path))
	settleFrames(t, x, "the picture", func() bool {
		img, _, _ := a.fileThumb(copied)
		return img != nil
	})
	x.frame(Input{})
	x.shot("files-hover-peek-model")
	x.mx, x.my = 2, float32(x.c.Img.Rect.Dy()-2)
	x.frame(Input{})
	x.press(keySpace, "")
	settleFrames(t, x, "the preview", func() bool { return a.preview.img != nil })
	x.frame(Input{})
	x.shot("files-preview-model")
	x.press(keySpace, "")
	x.a.leaveFiles(x.a.doc, false)
	if a.models.requests != nil {
		t.Fatal("the card stayed open with no 3D tab")
	}
}
