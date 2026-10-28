package paint

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"testing"
)

// drawn stands in for the interface's text drawing: a block where the text goes.
func drawn(t Text) *image.RGBA {
	r := image.Rect(int(t.X), int(t.Y), int(t.X)+len(t.Text)*int(t.Size/2), int(t.Y+t.LineHeight()))
	p := image.NewRGBA(r)
	for i := 0; i < len(p.Pix); i += 4 {
		p.Pix[i], p.Pix[i+3] = t.Color.R, 255
	}
	return p
}

func TestTextLayers(t *testing.T) {
	d := newDoc(t, 200, 100, white)
	s, err := d.BeginText(0)
	if err != nil {
		t.Fatal(err)
	}
	txt := Text{Text: "Hello", X: 10, Y: 20, Size: 20, Color: red}
	s.Set(txt, drawn(txt))
	txt.Text = "Hello, world\nsecond line"
	s.Set(txt, drawn(txt))
	s.Commit()
	l := d.Node(d.Active)
	if l.Text == nil || l.Text.Text != txt.Text || l.Name != "Hello, world" || d.UndoName() != "Text" {
		t.Fatalf("the new text layer: %+v named %q (%q)", l.Text, l.Name, d.UndoName())
	}
	if got := d.TextAt(15, 25); got == nil || got.ID != l.ID {
		t.Errorf("TextAt does not find the text")
	}
	// Edited again: one step, and Cancel puts it back.
	s, err = d.BeginText(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	t2 := *l.Text
	t2.Text = "Changed"
	s.Set(t2, drawn(t2))
	s.Cancel()
	if d.Node(l.ID).Text.Text != txt.Text {
		t.Errorf("cancel kept %q", d.Node(l.ID).Text.Text)
	}
	s, _ = d.BeginText(l.ID)
	s.Set(t2, drawn(t2))
	s.Commit()
	if d.Node(l.ID).Text.Text != "Changed" || d.UndoName() != "Edit text" || d.Node(l.ID).Name != "Changed" {
		t.Errorf("edit: %q (%q) named %q", d.Node(l.ID).Text.Text, d.UndoName(), d.Node(l.ID).Name)
	}
	// A layer renamed by hand keeps its name as the text changes.
	d.Rename(l.ID, "Title")
	s, _ = d.BeginText(l.ID)
	t2.Text = "Again"
	s.Set(t2, drawn(t2))
	s.Commit()
	if d.Node(l.ID).Name != "Title" {
		t.Errorf("a renamed text layer is now %q", d.Node(l.ID).Name)
	}
	t2.Text = "Changed"
	s, _ = d.BeginText(l.ID)
	s.Set(t2, drawn(t2))
	s.Commit()
	// A new text left empty leaves nothing behind.
	layers := len(d.Layers())
	s, _ = d.BeginText(0)
	s.Commit()
	if len(d.Layers()) != layers || d.UndoName() != "Edit text" {
		t.Errorf("an empty text was kept: %d layers, %q", len(d.Layers()), d.UndoName())
	}
	if _, err := d.BeginText(d.Layers()[len(d.Layers())-1].ID); !errors.Is(err, ErrNotText) {
		t.Errorf("editing the background as text: %v", err)
	}
	// Moving the whole layer moves the text; nudging keeps it text.
	d.Choose(l.ID)
	if err := d.Translate(5, 7); err != nil {
		t.Fatal(err)
	}
	if lt := d.Node(l.ID).Text; lt == nil || lt.X != 15 || lt.Y != 27 {
		t.Fatalf("moved text %+v", lt)
	}
	// Painting turns it into pixels; undo brings the text back.
	st, _, err := d.BeginStroke(Brush{Size: 5, Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.1, Round: 1, Color: blue}, false, 30, 30)
	if err != nil {
		t.Fatal(err)
	}
	st.End()
	if d.Node(l.ID).Text != nil {
		t.Error("painting kept the layer as text")
	}
	d.Undo()
	if d.Node(l.ID).Text == nil {
		t.Error("undo did not bring the text back")
	}
	// A stroke that paints nothing (outside the selection) leaves the text alone.
	d.Select(image.Rect(150, 80, 160, 90))
	st, _, _ = d.BeginStroke(Brush{Size: 5, Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.1, Round: 1, Color: blue}, false, 30, 30)
	st.End()
	if d.Node(l.ID).Text == nil {
		t.Error("a stroke that painted nothing rasterized the text")
	}
	d.Deselect()
	// Growing the frame on the left moves nothing on the canvas; scaling the picture scales the text with it, about
	// the frame's corner, now 20 to the left.
	if err := d.ResizeCanvas(220, 100, 1, 0); err != nil {
		t.Fatal(err)
	}
	if lt := d.Node(l.ID).Text; lt == nil || lt.X != 15 || d.Frame.Min.X != -20 {
		t.Errorf("canvas size: %+v, frame %v", lt, d.Frame)
	}
	if err := d.ResizeImage(110, 50); err != nil {
		t.Fatal(err)
	}
	if lt := d.Node(l.ID).Text; lt == nil || lt.Size != 10 || lt.X != -20+(15+20)*0.5 || lt.Y != 27*0.5 {
		t.Errorf("image size: %+v", lt)
	}
	// Saved and loaded, the text is still text.
	var buf bytes.Buffer
	if err := d.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if lt := back.Node(l.ID).Text; lt == nil || lt.Text != "Changed" || lt.Color != red {
		t.Errorf("loaded text %+v", lt)
	}
	// Flipping the canvas turns text into pixels, and Rasterize does so on request.
	d.FlipCanvas(true)
	if d.Node(l.ID).Text != nil {
		t.Error("a flipped canvas kept mirrored text as text")
	}
	d.Undo()
	if err := d.RasterizeText(); err != nil || d.Node(l.ID).Text != nil || d.UndoName() != "Rasterize type" {
		t.Errorf("rasterize: %v", err)
	}
	if err := d.RasterizeText(); !errors.Is(err, ErrNotText) {
		t.Errorf("rasterizing pixels: %v", err)
	}
}

func TestDuplicateGivesTheMaskItsOwnPixels(t *testing.T) {
	d := newDoc(t, 20, 20, white)
	l := d.AddLayer("Ink")
	d.Fill(red)
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	c, err := d.Duplicate(" copy")
	if err != nil {
		t.Fatal(err)
	}
	d.EditMask = true
	d.Select(image.Rect(0, 0, 10, 10))
	d.Fill(color.NRGBA{A: 255})
	if v := d.Node(l.ID).Mask.At(5, 5); v != 255 {
		t.Errorf("painting the copy's mask changed the original's to %d", v)
	}
	if v := d.Node(c.ID).Mask.At(5, 5); v != 0 {
		t.Errorf("the copy's mask is %d", v)
	}
}
