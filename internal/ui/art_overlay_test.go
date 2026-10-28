package ui

import (
	"image"
	"image/color"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/project"
)

func TestDrawingOnTheVideo(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.settings.ExportDir = x.mediaDir()
	x.open(x.fixture())
	a.seek(1)
	x.settle(10 * time.Second)
	video := a.doc
	a.tab = tabOverlays
	x.frame(Input{})
	x.shot("drawing-panel")

	a.addDrawing()
	ad := a.art
	if ad == nil || ad.backdrop == nil {
		t.Fatalf("no canvas over the video's picture: %+v", ad)
	}
	var o project.Overlay
	for _, tr := range video.p.Overlays {
		for _, it := range tr.Items {
			if it.Canvas == ad.path {
				o = it
			}
		}
	}
	w, h := video.p.Canvas.Width, video.p.Canvas.Height
	if o.ID == 0 || o.Size != 1 || ad.d.Frame.Dx() != w || ad.d.Frame.Dy() != h {
		t.Fatalf("the drawing overlay %+v on a %d×%d canvas for a %d×%d frame", o, ad.d.Frame.Dx(), ad.d.Frame.Dy(), w, h)
	}
	// The backdrop is the video's picture, behind a transparent canvas.
	if c := ad.d.Composite(image.Rect(0, 0, w, h)).RGBAAt(w/2, h/2); c.A != 0 {
		t.Errorf("the canvas is not transparent: %v", c)
	}
	if c := ad.backdrop.RGBAAt(w/2, h/2); c == (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("the backdrop shows no picture")
	}
	// Paint, then save: the overlay's picture follows.
	r := image.Rect(w/4, h/4, w/2, h/2)
	ad.d.Select(r)
	ad.d.Fill(color.NRGBA{R: 255, A: 255})
	ad.d.Deselect()
	ad.changedAll()
	x.frame(Input{})
	x.shot("drawing-canvas")
	x.key('S', true)
	f, err := os.Open(o.Image)
	if err != nil {
		t.Fatal(err)
	}
	pic, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if cr, cg, _, ca := pic.At(w*3/8, h*3/8).RGBA(); cr>>8 != 255 || cg != 0 || ca>>8 != 255 {
		t.Errorf("the saved picture is %v at the square", pic.At(w*3/8, h*3/8))
	}
	if _, _, _, ca := pic.At(w-2, h-2).RGBA(); ca != 0 {
		t.Errorf("the saved picture is not transparent around the drawing")
	}
	// Back on the video, the preview and the export show the drawing over the whole frame.
	a.switchTab(slices.Index(a.docs, video))
	x.settle(10 * time.Second)
	img, ok := a.overlayImage(o, w, h)
	if !ok || img.At != (image.Point{}) || img.Bounds().Dx() != w {
		t.Fatalf("the overlay is drawn at %v, %v wide", img.At, img.Bounds().Dx())
	}
	if c := img.RGBAAt(w*3/8, h*3/8); c.R < 250 || c.A < 250 {
		t.Errorf("the overlay shows %v at the square", c)
	}
	a.sel = selection{kind: selOverlay, id: o.ID}
	x.frame(Input{})
	x.shot("drawing-on-video")
	// Drawing in it again brings back its canvas.
	a.editDrawing(o)
	if a.art != ad {
		t.Error("editing the drawing did not show its canvas")
	}
}
