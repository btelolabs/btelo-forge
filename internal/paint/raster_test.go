package paint

import (
	"image"
	"image/color"
	"testing"
)

func solid(r image.Rectangle, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(r)
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func TestRasterIsSparseAndUnbounded(t *testing.T) {
	r := NewRaster()
	red := color.RGBA{255, 0, 0, 255}
	// Far from the origin and across tile edges, on both sides of zero.
	area := image.Rect(-300, -10, 20, 270)
	r.Write(solid(area, red))
	if r.At(-300, -10) != red || r.At(19, 269) != red || r.At(20, 0) != (color.RGBA{}) || r.At(-301, 0) != (color.RGBA{}) {
		t.Fatalf("pixels %v %v %v %v", r.At(-300, -10), r.At(19, 269), r.At(20, 0), r.At(-301, 0))
	}
	if n := len(r.p.tiles); n != 9 { // tile columns −2, −1, 0 and rows −1, 0, 1: only those touched
		t.Errorf("tiles: %d", n)
	}
	if b := r.Bounds(); b != area {
		t.Errorf("bounds %v, want %v", b, area)
	}
	got := r.Read(image.Rect(-310, -20, 30, 280))
	if got.RGBAAt(-305, 0).A != 0 || got.RGBAAt(0, 0) != red {
		t.Error("read across the edge")
	}
	// A very distant write makes one tile, not a canvas between.
	r.Write(solid(image.Rect(1_000_000, 1_000_000, 1_000_001, 1_000_001), red))
	if r.At(1_000_000, 1_000_000) != red || len(r.p.tiles) > 10 {
		t.Errorf("a distant pixel: %v with %d tiles", r.At(1_000_000, 1_000_000), len(r.p.tiles))
	}
	// Writing transparency over a whole tile drops it.
	before := len(r.p.tiles)
	r.Write(image.NewRGBA(image.Rect(1_000_000-TileSize, 1_000_000-TileSize, 1_000_000+TileSize, 1_000_000+TileSize)))
	if len(r.p.tiles) != before-1 {
		t.Errorf("clearing a tile left %d tiles, from %d", len(r.p.tiles), before)
	}
	// Moving is free and keeps the pixels.
	r.Move(1000, -50)
	if r.At(700, -60) != red || r.Bounds() != area.Add(image.Pt(1000, -50)) {
		t.Errorf("moved: %v, %v", r.At(700, -60), r.Bounds())
	}
}

func TestRasterSnapshotsShareTiles(t *testing.T) {
	r := NewRaster()
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	r.Write(solid(image.Rect(0, 0, 600, 600), red))
	s := r.Snapshot()
	if !r.Same(s) {
		t.Fatal("a fresh snapshot is not the same")
	}
	// Writing one pixel copies one tile; the snapshot keeps the old pixel.
	r.Write(solid(image.Rect(300, 300, 301, 301), blue))
	if r.At(300, 300) != blue || s.At(300, 300) != red {
		t.Fatalf("copy on write: %v %v", r.At(300, 300), s.At(300, 300))
	}
	if r.Same(s) {
		t.Error("changed raster is still the same")
	}
	shared := 0
	for k, tl := range r.p.tiles {
		if s.p.tiles[k] == tl {
			shared++
		}
	}
	if shared != len(r.p.tiles)-1 {
		t.Errorf("%d of %d tiles shared after changing one", shared, len(r.p.tiles))
	}
	// Writing through Tiles also copies first.
	r.Tiles(image.Rect(0, 0, 10, 10), func(pix []uint8, part image.Rectangle, corner image.Point) {
		pix[0] = 7
	})
	if s.At(0, 0) != red {
		t.Error("Tiles wrote into a snapshot's tile")
	}
}

func TestRasterLevelsHalve(t *testing.T) {
	r := NewRaster()
	// A checker of single pixels averages to grey; a solid block stays itself.
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := range 512 {
		for x := range 512 {
			if (x+y)%2 == 0 {
				img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
	r.Write(img)
	r.Write(solid(image.Rect(512, 0, 1024, 512), color.RGBA{0, 255, 0, 255}))
	half := r.ReadLevel(1, image.Rect(0, 0, 512, 256))
	if c := half.RGBAAt(10, 10); c.R < 126 || c.R > 129 || c.A < 126 || c.A > 129 {
		t.Errorf("halved checker %v", c)
	}
	if c := half.RGBAAt(300, 10); c != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("halved block %v", c)
	}
	quarter := r.ReadLevel(2, image.Rect(0, 0, 256, 128))
	if c := quarter.RGBAAt(200, 50); c != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("quartered block %v", c)
	}
	// A change below drops the level tile above it, which is made again.
	r.Write(solid(image.Rect(512, 0, 1024, 512), color.RGBA{0, 0, 255, 255}))
	if c := r.ReadLevel(2, image.Rect(0, 0, 256, 128)).RGBAAt(200, 50); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("level after a change %v", c)
	}
	if LevelFor(1) != 0 || LevelFor(0.5) != 1 || LevelFor(0.3) != 1 || LevelFor(0.2) != 2 {
		t.Errorf("levels for zooms: %d %d %d %d", LevelFor(1), LevelFor(0.5), LevelFor(0.3), LevelFor(0.2))
	}
}

func TestMaskFillAndInvert(t *testing.T) {
	m := NewMask(0)
	sel := image.NewAlpha(image.Rect(10, 10, 20, 20))
	for i := range sel.Pix {
		sel.Pix[i] = 255
	}
	m.Write(sel)
	if m.At(15, 15) != 255 || m.At(-5000, 9000) != 0 || m.Bounds() != image.Rect(10, 10, 20, 20) {
		t.Fatalf("mask %d %d %v", m.At(15, 15), m.At(-5000, 9000), m.Bounds())
	}
	// Inverted, it is everything but the square, everywhere.
	m.Invert()
	if m.At(15, 15) != 0 || m.At(-5000, 9000) != 255 || m.Fill() != 255 || m.Bounds() != image.Rect(10, 10, 20, 20) {
		t.Errorf("inverted %d %d fill %d bounds %v", m.At(15, 15), m.At(-5000, 9000), m.Fill(), m.Bounds())
	}
	if a := m.Read(image.Rect(0, 0, 30, 30)); a.AlphaAt(0, 0).A != 255 || a.AlphaAt(12, 12).A != 0 {
		t.Error("read of the inverted mask")
	}
	// A mask with a white fill keeps tiles only where it is not white.
	w := NewMask(255)
	w.Write(image.NewAlpha(image.Rect(0, 0, 4, 4)))
	if w.At(1, 1) != 0 || w.At(100, 100) != 255 || len(w.p.tiles) != 1 {
		t.Errorf("white mask %d %d with %d tiles", w.At(1, 1), w.At(100, 100), len(w.p.tiles))
	}
}
