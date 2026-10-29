package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"slices"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/mind"
)

// A canvas shown on a mind map is only referred to: as the canvas is drawn on and saved, the map shows the new
// picture, without being opened again, and shows the old one while the new one is read rather than flashing the
// loading picture.
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
	// centre is the middle of the picture the node shows now, once there is one.
	centre := func() (color.RGBA, mind.PictureState) {
		img, state, _ := x.a.mindPictures().get(md.m.Node(id), 300, md.path)
		if img == nil {
			return color.RGBA{}, state
		}
		return img.RGBAAt(img.Rect.Dx()/2, img.Rect.Dy()/2), state
	}
	x.pump(10*time.Second, func() bool { _, state := centre(); return state == mind.PicReady })
	if c, _ := centre(); c.R < 240 || c.G < 240 || c.B < 240 {
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
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, state := centre()
		if state != mind.PicReady {
			t.Fatalf("while the canvas is read again the node shows %v, not the picture it had", state)
		}
		if c.R >= 200 && c.G <= 60 && c.B <= 60 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the canvas was saved red, the map still shows %v", c)
		}
		x.frame(Input{})
		time.Sleep(15 * time.Millisecond)
	}
	// The board's overview, drawn once and kept, shows the new picture as well.
	red := func(img *image.RGBA) bool {
		if img == nil {
			return false
		}
		for i := 0; i+3 < len(img.Pix); i += 4 {
			if img.Pix[i] > 180 && img.Pix[i+1] < 80 && img.Pix[i+2] < 80 {
				return true
			}
		}
		return false
	}
	for !red(x.overviewShown(md)) {
		if time.Now().After(deadline) {
			t.Fatal("the overview still shows the canvas as it was")
		}
		x.frame(Input{})
		time.Sleep(15 * time.Millisecond)
	}
}

// Dragging free nodes about leaves nothing of them behind: the board drawn from the tiles kept is the board drawn
// afresh, pixel for pixel, the shadows too.
func TestMindDragLeavesNoTrace(t *testing.T) {
	x, md := newMindHarness(t)
	m := md.m
	var ids []mind.ID
	for i, s := range []mind.Shape{mind.ShapeRounded, mind.ShapeRect, mind.ShapeCapsule} {
		id, err := m.Add(mind.Node{Text: fmt.Sprintf("free %d", i), Shape: s, X: float64(-300 + i*260), Y: 260}, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	m.Commit()
	md.fit = true
	x.settleMind()
	for i, id := range ids {
		fx, fy := x.nodeCentre(md, id)
		x.mx, x.my = fx, fy
		x.frame(Input{})
		x.frame(Input{Down: true, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}})
		for step := 1; step <= 12; step++ {
			x.mx, x.my = fx+float32(step*23), fy-float32(step*(9+i*4))
			x.frame(Input{Down: true})
		}
		x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	}
	md.selectOnly(0)
	x.settleMind()
	board := x.a.layout().body.Pixels()
	kept := x.c.Img.SubImage(board).(*image.RGBA)
	keptPix := slices.Clone(kept.Pix)
	md.tiles.Clear()
	x.settleMind()
	fresh := x.c.Img.SubImage(board).(*image.RGBA)
	if !bytes.Equal(keptPix, fresh.Pix) {
		diff := 0
		for i := range keptPix {
			if keptPix[i] != fresh.Pix[i] {
				diff++
			}
		}
		x.shot("mind-drag-trace")
		t.Fatalf("%d bytes of the board differ from the board drawn afresh", diff)
	}
}
