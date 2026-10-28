package ui

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/mind"
)

// A canvas shown on a mind map is only referred to: as the canvas is drawn on and saved, the map shows the new
// picture, without being opened again.
func TestMindMapFollowsACanvasItShows(t *testing.T) {
	x, ad := canvasHarness(t)
	canvasTab := x.a.current()
	if err := x.a.newMindMap(); err != nil {
		t.Fatal(err)
	}
	md := x.a.board
	id, err := x.a.mindAddCentred(md, mind.Node{Kind: mind.Picture, Ref: ad.path}, x.a.mindViewCentre(md))
	if err != nil {
		t.Fatal(err)
	}
	x.settleMind()
	centre := func() color.RGBA {
		var img *image.RGBA
		x.pump(10*time.Second, func() bool {
			var state mind.PictureState
			img, state, _ = x.a.mindPictures().get(md.m.Node(id), 300, md.path)
			return state == mind.PicReady && img != nil
		})
		return img.RGBAAt(img.Rect.Dx()/2, img.Rect.Dy()/2)
	}
	if c := centre(); c.R < 240 || c.G < 240 || c.B < 240 {
		t.Fatalf("before: the centre is %v, want the canvas's white", c)
	}

	x.a.switchTab(canvasTab)
	ad.d.Select(image.Rect(0, 0, 400, 300))
	ad.d.Fill(color.NRGBA{R: 220, G: 30, B: 30, A: 255})
	ad.d.Deselect()
	ad.changedAll()
	if err := x.a.saveArt(ad, true); err != nil {
		t.Fatal(err)
	}
	x.a.switchTab(canvasTab + 1)
	x.settleMind()
	if c := centre(); c.R < 200 || c.G > 60 || c.B > 60 {
		t.Fatalf("after the canvas was saved red, the map still shows %v", c)
	}
}
