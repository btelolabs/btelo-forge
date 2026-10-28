package assets

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKindsComeFromTheName(t *testing.T) {
	for name, want := range map[string]Kind{
		"a.PNG": Image, "b.gif": Animation, "c.mkv": Video, "d.flac": Audio, "e" + paint.Ext: Canvas, "f.btcut": Edit,
		"README.md": Markdown, "main.go": Code, "Dockerfile": Code, "notes.txt": Text, "x.zip": Other,
	} {
		if got := KindOf(name); got != want {
			t.Errorf("KindOf(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestScanListsWhatPeopleSee(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "photo.jpg"), []byte("x"))
	write(t, filepath.Join(dir, ".hidden"), []byte("x"))
	write(t, filepath.Join(dir, "desktop.ini"), []byte("x"))
	write(t, filepath.Join(dir, "export.mp4.part"), []byte("x"))
	write(t, filepath.Join(dir, "app.ts"), []byte("export const a = 1\n"))
	write(t, filepath.Join(dir, "clip.ts"), []byte{0x47, 0, 0})
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	got, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]Kind{}
	for _, e := range got {
		kinds[e.Name] = e.Kind
	}
	want := map[string]Kind{"photo.jpg": Image, "app.ts": Code, "clip.ts": Video, "sub": Folder}
	if len(kinds) != len(want) {
		t.Fatalf("%v", kinds)
	}
	for n, k := range want {
		if kinds[n] != k {
			t.Errorf("%s: %v, want %v", n, kinds[n], k)
		}
	}
}

func TestLibraryKeepsTagsAndCategories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assets.json")
	l, err := OpenLibrary(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join("x", "a.png"), filepath.Join("x", "b.png")
	if err := l.AddTag([]string{a, b}, "  Logo  "); err != nil {
		t.Fatal(err)
	}
	if err := l.AddTag([]string{a}, "logo"); err != nil { // the same tag, spelt otherwise
		t.Fatal(err)
	}
	if err := l.AddTag([]string{a}, ""); !errors.Is(err, ErrEmptyName) {
		t.Fatal(err)
	}
	if tags := l.Tags(); len(tags) != 1 || tags[0] != (Count{"Logo", 2}) {
		t.Fatalf("%v", tags)
	}
	if _, err := l.NewCategory("Brand"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.NewCategory("brand"); !errors.Is(err, ErrNameExists) {
		t.Fatal(err)
	}
	if err := l.SetCategory([]string{b}, "brand", true); err != nil {
		t.Fatal(err)
	}
	if err := l.RenameCategory("Brand", "Identity"); err != nil {
		t.Fatal(err)
	}

	again, err := OpenLibrary(path)
	if err != nil {
		t.Fatal(err)
	}
	if it := again.Of(b); !slices.Equal(it.Tags, []string{"Logo"}) || !slices.Equal(it.Categories, []string{"Identity"}) {
		t.Fatalf("%+v", it)
	}
	if c := again.Categories(); len(c) != 1 || c[0] != (Count{"Identity", 1}) {
		t.Fatalf("%v", c)
	}
	// Deleting a category keeps the files; forgetting a file drops what was said about it.
	again.DeleteCategory("Identity")
	again.Forget([]string{a})
	if it := again.Of(a); len(it.Tags) != 0 {
		t.Fatalf("%+v", it)
	}
	if tags := again.Tags(); len(tags) != 1 || tags[0].Files != 1 {
		t.Fatalf("%v", tags)
	}
	again.RenameTag("logo", "Mark")
	if it := again.Of(b); !slices.Equal(it.Tags, []string{"Mark"}) || len(it.Categories) != 0 {
		t.Fatalf("%+v", it)
	}
}

func entry(t *testing.T, path string) Entry {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return Entry{Path: path, Name: filepath.Base(path), Kind: KindOf(path), Size: st.Size(), Mod: st.ModTime()}
}

func TestThumbsAreMadeOnceAndKept(t *testing.T) {
	dir := t.TempDir()
	img := image.NewNRGBA(image.Rect(0, 0, 1600, 400))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	pic := filepath.Join(dir, "wide.png")
	write(t, pic, buf.Bytes())

	th := NewThumbs(filepath.Join(dir, "cache"))
	e := entry(t, pic)
	small, info, err := th.Get(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if info.W != 1600 || info.H != 400 || small.Rect.Dx() != Box.X || small.Rect.Dy() != Box.X/4 {
		t.Fatalf("%+v %v", info, small.Rect)
	}
	// The second time it comes from the cache: the file is not read again, so it may even be gone.
	os.Remove(pic)
	cached, info2, err := th.Get(context.Background(), e)
	if err != nil || info2.W != 1600 || cached == nil || cached.Rect != small.Rect {
		t.Fatalf("%v %+v", err, info2)
	}
	// A changed file gets a new picture.
	write(t, pic, []byte("not a picture"))
	future := time.Now().Add(time.Hour)
	os.Chtimes(pic, future, future)
	if _, info3, _ := th.Get(context.Background(), entry(t, pic)); info3.Problem == "" {
		t.Fatalf("a broken picture should say why: %+v", info3)
	}
}

func TestThumbsOfAnimationsTextsAndCanvases(t *testing.T) {
	dir := t.TempDir()
	th := NewThumbs(filepath.Join(dir, "cache"))
	ctx := context.Background()

	g := &gif.GIF{}
	for i := range 3 {
		f := image.NewPaletted(image.Rect(0, 0, 40, 20), palette.Plan9)
		for k := range f.Pix {
			f.Pix[k] = uint8(i * 40)
		}
		g.Image, g.Delay = append(g.Image, f), append(g.Delay, 10)
	}
	var buf bytes.Buffer
	gif.EncodeAll(&buf, g)
	anim := filepath.Join(dir, "a.gif")
	write(t, anim, buf.Bytes())
	if _, info, err := th.Get(ctx, entry(t, anim)); err != nil || !info.Animated || info.Frames != 3 || info.Duration != 0.3 {
		t.Fatalf("%v %+v", err, info)
	}

	md := filepath.Join(dir, "a.md")
	write(t, md, []byte("\xef\xbb\xbf# Title\r\nbody"))
	if pic, info, err := th.Get(ctx, entry(t, md)); err != nil || pic != nil || info.Snippet != "# Title\nbody" {
		t.Fatalf("%v %+v", err, info)
	}
	u16 := filepath.Join(dir, "w.txt")
	units := utf16.Encode([]rune("中文"))
	data := []byte{0xff, 0xfe}
	for _, u := range units {
		data = append(data, byte(u), byte(u>>8))
	}
	write(t, u16, data)
	if text, _, err := ReadText(u16, 100); err != nil || text != "中文" {
		t.Fatalf("%q %v", text, err)
	}
	bin := filepath.Join(dir, "b.txt")
	write(t, bin, []byte{1, 0, 2})
	if _, info, _ := th.Get(ctx, entry(t, bin)); info.Problem != ErrBinary.Error() {
		t.Fatalf("%+v", info)
	}
	cut := filepath.Join(dir, "cut.txt")
	write(t, cut, []byte("ab中"))
	if text, truncated, err := ReadText(cut, 4); err != nil || !truncated || text != "ab" {
		t.Fatalf("%q %v %v", text, truncated, err)
	}

	d, err := paint.NewFramed(300, 200, "Layer", color.NRGBA{255, 0, 0, 255})
	if err != nil {
		t.Fatal(err)
	}
	canvas := filepath.Join(dir, "c"+paint.Ext)
	f, err := os.Create(canvas)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Snapshot().Save(f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	pic, info, err := th.Get(ctx, entry(t, canvas))
	if err != nil || info.W != 300 || info.H != 200 || pic == nil {
		t.Fatalf("%v %+v", err, info)
	}
	if c := pic.RGBAAt(150, 100); c.R < 250 || c.G > 5 {
		t.Fatalf("the canvas picture is %v, not red", c)
	}
}

func TestThumbsOfVideosAndSounds(t *testing.T) {
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "v.mp4")
	cmd, err := media.Command(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=25:d=2",
		"-f", "lavfi", "-i", "sine=d=2", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-shortest", video)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	sound := filepath.Join(dir, "s.wav")
	cmd, _ = media.Command(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=d=3", sound)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	th := NewThumbs(filepath.Join(dir, "cache"))
	pic, info, err := th.Get(t.Context(), entry(t, video))
	if err != nil || pic == nil || info.W != 320 || info.H != 180 || info.Duration < 1.9 {
		t.Fatalf("%v %+v", err, info)
	}
	pic, info, err = th.Get(t.Context(), entry(t, sound))
	if err != nil || pic != nil || len(info.Peaks) != PeakBins || info.Duration < 2.9 || slices.Max(info.Peaks) < 0.1 {
		t.Fatalf("%v %+v", err, info)
	}
}

func TestLibraryFollowsFilesAndFoldersMovedAndDeleted(t *testing.T) {
	l, err := OpenLibrary(filepath.Join(t.TempDir(), "assets.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("work", "shots")
	inside, beside := filepath.Join(dir, "a.png"), filepath.Join("work", "shots 2", "b.png")
	if err := l.AddTag([]string{inside, beside}, "Keep"); err != nil {
		t.Fatal(err)
	}
	// A folder moved takes its files' tags along; a folder whose name only starts the same keeps its own.
	moved := filepath.Join("done", "shots")
	if err := l.Move(dir, moved); err != nil {
		t.Fatal(err)
	}
	if it := l.Of(filepath.Join(moved, "a.png")); !slices.Equal(it.Tags, []string{"Keep"}) || it.Path != filepath.Join(moved, "a.png") {
		t.Fatalf("moved: %+v", it)
	}
	if it := l.Of(inside); len(it.Tags) != 0 {
		t.Fatalf("the old path keeps %+v", it)
	}
	if it := l.Of(beside); !slices.Equal(it.Tags, []string{"Keep"}) {
		t.Fatalf("the folder beside lost %+v", it)
	}
	// A file renamed keeps its tags.
	renamed := filepath.Join(moved, "c.png")
	l.Move(filepath.Join(moved, "a.png"), renamed)
	if it := l.Of(renamed); !slices.Equal(it.Tags, []string{"Keep"}) {
		t.Fatalf("renamed: %+v", it)
	}
	// A folder moved to the Recycle Bin gives its files' tags up, and gets them back when it comes back.
	taken, err := l.Take([]string{"done"})
	if err != nil || len(taken) != 1 {
		t.Fatalf("took %v: %v", taken, err)
	}
	if tags := l.Tags(); len(tags) != 1 || tags[0].Files != 1 {
		t.Fatalf("%v", tags)
	}
	if err := l.Put(taken); err != nil {
		t.Fatal(err)
	}
	if it := l.Of(renamed); !slices.Equal(it.Tags, []string{"Keep"}) {
		t.Fatalf("back from the bin: %+v", it)
	}
	// A folder deleted for good forgets its files.
	l.Forget([]string{"done"})
	if tags := l.Tags(); len(tags) != 1 || tags[0].Files != 1 {
		t.Fatalf("%v", tags)
	}
}
