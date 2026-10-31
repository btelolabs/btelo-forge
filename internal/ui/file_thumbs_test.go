package ui

import (
	"bytes"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/mind"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/thumb"
)

func TestACanvasFileEndsWithItsPicture(t *testing.T) {
	x, ad := canvasHarness(t)
	if err := ad.d.Fill(color.NRGBA{R: 200, G: 20, B: 30, A: 255}); err != nil {
		t.Fatal(err)
	}
	ad.dirty = true
	if err := x.a.saveArt(ad, true); err != nil {
		t.Fatal(err)
	}
	img, m, err := thumb.Read(ad.path)
	if err != nil {
		t.Fatal(err)
	}
	if m.W != 400 || m.H != 300 || img.Rect.Dx() != 400 || img.Rect.Dy() != 300 {
		t.Errorf("meta %+v, picture %v", m, img.Rect)
	}
	if c := img.RGBAAt(200, 150); c.R < 170 || c.G > 60 {
		t.Errorf("the picture's middle is %v", c)
	}
	f, err := os.Open(ad.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	if _, err := paint.Load(f, st.Size()); err != nil {
		t.Fatalf("the canvas no longer reads: %v", err)
	}

	// The file manager takes the picture from the file, without opening the canvas.
	store := assets.NewThumbs(filepath.Join(x.dir, "thumbs"))
	got, info, err := store.Get(t.Context(), assets.Entry{Path: ad.path, Kind: assets.Canvas, Size: st.Size(), Mod: st.ModTime()})
	if err != nil || got == nil || info.W != 400 || info.H != 300 {
		t.Errorf("%v %+v %v", got != nil, info, err)
	}
}

func TestAMapFileEndsWithItsPicture(t *testing.T) {
	x, md := newMindHarness(t)
	md.m.Title = "A plan"
	md.dirty = true
	if err := x.a.saveMind(md, true); err != nil {
		t.Fatal(err)
	}
	img, m, err := thumb.Read(md.path)
	if err != nil {
		t.Fatal(err)
	}
	if img.Rect.Dx() == 0 || m.W != img.Rect.Dx() || m.H != img.Rect.Dy() {
		t.Errorf("meta %+v, picture %v", m, img.Rect)
	}
	data, err := os.ReadFile(md.path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mind.Load(bytes.NewReader(data), filepath.Dir(md.path), mind.SimpleMeasurer{}); err != nil || got.Title != "A plan" {
		t.Fatalf("the map no longer reads: %v", err)
	}
	first := append([]byte(nil), md.thumb...)

	// Saves soon after keep the picture; it is drawn again once the changes stop, so the file ends up current.
	x.pump(5*time.Second, func() bool { return !md.saving })
	md.dirty = true
	if err := x.a.saveMind(md, false); err != nil {
		t.Fatal(err)
	}
	x.pump(5*time.Second, func() bool { return !md.saving })
	if !md.thumbStale || !bytes.Equal(md.thumb, first) {
		t.Fatalf("stale %v", md.thumbStale)
	}
	md.thumbAt = time.Now().Add(-mindThumbEvery)
	x.pump(10*time.Second, func() bool { return !md.thumbStale && !md.saving && !md.dirty })
	if time.Since(md.thumbAt) > 10*time.Second {
		t.Error("the picture was not drawn again")
	}
	if _, _, err := thumb.Read(md.path); err != nil {
		t.Error(err)
	}
}

func TestAnEditFileEndsWithItsPicture(t *testing.T) {
	x := newHarness(t)
	x.a.settings.ResourceDir = filepath.Join(x.dir, "out")
	x.open(x.fixture())
	x.pump(20*time.Second, func() bool { return x.a.doc.shown.img != nil })
	path, err := x.a.saveEditFile()
	if err != nil {
		t.Fatal(err)
	}
	img, m, err := thumb.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if img.Rect.Empty() || m.W != x.a.p.Canvas.Width || m.Duration != float64(int64(x.a.p.Duration()*1000))/1000 {
		t.Errorf("meta %+v", m)
	}
	back, err := project.ReadFile(path)
	if err != nil {
		t.Fatalf("the edit no longer reads: %v", err)
	}
	if !project.Equal(back, x.a.p) {
		t.Error("the edit changed")
	}

	// With no frame on the screen the file keeps the picture it has.
	x.a.doc.shown.img = nil
	if _, err := x.a.saveEditFile(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := thumb.Read(path); err != nil {
		t.Error(err)
	}
}
