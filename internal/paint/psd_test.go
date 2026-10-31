package paint

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/psd"
)

// throughPSD writes a canvas as a Photoshop file and opens it again.
func throughPSD(t *testing.T, d *Doc) (*Doc, PSDLoss) {
	t.Helper()
	f, loss, err := d.ToPSD()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := psd.Encode(&b, f); err != nil {
		t.Fatal(err)
	}
	back, err := psd.Decode(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	out, l2, err := FromPSD(back)
	if err != nil {
		t.Fatal(err)
	}
	loss.Adjustments += l2.Adjustments
	return out, loss
}

func TestACanvasComesBackFromAPhotoshopFile(t *testing.T) {
	d := newDoc(t, 120, 80, color.NRGBA{R: 240, G: 240, B: 240, A: 255})
	group := d.AddGroup("组")
	group.Opacity = 0.5
	ink := d.AddLayer("Ink")
	d.SetBlend(ink.ID, BlendMultiply)
	d.Select(image.Rect(20, 20, 60, 50))
	if err := d.Fill(color.NRGBA{R: 200, G: 40, B: 40, A: 128}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	d.Select(image.Rect(20, 20, 40, 50))
	d.EditMask = true
	if err := d.Fill(color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	d.EditMask = false
	d.Deselect()

	out, loss := throughPSD(t, d)
	if loss.Any() {
		t.Errorf("loss %+v", loss)
	}
	if out.Frame != d.Frame {
		t.Errorf("frame %v", out.Frame)
	}
	a, b := d.Composite(d.Frame), out.Composite(out.Frame)
	for y := 0; y < 80; y += 3 {
		for x := 0; x < 120; x += 3 {
			p, q := a.RGBAAt(x, y), b.RGBAAt(x, y)
			if !near(p.R, q.R, 3) || !near(p.G, q.G, 3) || !near(p.B, q.B, 3) || !near(p.A, q.A, 3) {
				t.Fatalf("at (%d, %d): %v, came back %v", x, y, p, q)
			}
		}
	}
	found := false
	for _, n := range out.Root.Children {
		if n.IsGroup() && n.Name == "组" && len(n.Children) == 1 && near(uint8(n.Opacity*255), 127, 1) {
			c := n.Children[0]
			found = c.Name == "Ink" && c.Blend == BlendMultiply && c.Mask != nil
		}
	}
	if !found {
		t.Errorf("the group did not come back as it was")
	}
	// It is a canvas like any: its history starts here, and it can be painted on.
	if out.CanUndo() {
		t.Error("opening it left a step to undo")
	}
	if out.Node(out.Active) == nil {
		t.Fatal("nothing is chosen")
	}
}

func TestVectorAndTextGoToPixelsAndWhatCannotGoIsCounted(t *testing.T) {
	d := newDoc(t, 100, 100, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	d.AddVectorLayer("Shape", VectorPaint{Kind: PaintSolid, Color: color.NRGBA{R: 255, A: 255}})
	if _, err := d.AddAdjustment(Adjustment{Kind: AdjustInvert}, "Invert"); err != nil {
		t.Fatal(err)
	}
	f, loss, err := d.ToPSD()
	if err != nil {
		t.Fatal(err)
	}
	if loss.Adjustments != 1 {
		t.Errorf("loss %+v", loss)
	}
	if len(f.Layers) != 2 {
		t.Errorf("%d layers", len(f.Layers))
	}
	// The merged picture is the canvas as it looks, with the adjustment.
	if c := f.Merged.NRGBAAt(50, 50); c.R != 0 {
		t.Errorf("merged %v", c)
	}
}

func TestOpeningCountsWhatIsLeftOut(t *testing.T) {
	r := image.Rect(0, 0, 4, 4)
	pix := make([]uint8, 64)
	f := &psd.File{W: 4, H: 4, Layers: []*psd.Layer{
		{Name: "Levels", Adjust: true, Visible: true, Opacity: 255, Blend: "norm"},
		{Name: "Glow", Effects: true, Clipped: true, Bounds: r, Pix: pix, Visible: true, Opacity: 255, Blend: "scrn"},
	}}
	d, loss, err := FromPSD(f)
	if err != nil {
		t.Fatal(err)
	}
	if loss != (PSDLoss{Adjustments: 1, Effects: 1, Clipped: 1}) {
		t.Errorf("loss %+v", loss)
	}
	if len(d.Root.Children) != 1 || d.Root.Children[0].Blend != BlendScreen {
		t.Errorf("layers %+v", d.Root.Children)
	}
}

func TestAnEmptyCanvasHasNoPhotoshopFile(t *testing.T) {
	if _, _, err := New("Empty").ToPSD(); err == nil {
		t.Error("an empty canvas was written")
	}
}
