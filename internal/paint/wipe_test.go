package paint

import (
	"image"
	"image/color"
	"testing"
)

func TestWipe(t *testing.T) {
	d, err := NewFramed(64, 64, "", color.NRGBA{})
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := d.BeginStroke(Brush{Size: 10, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{R: 255, A: 255}}, false, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	s.To(50, 10)
	s.End()
	d.AddLayer("Second")
	d.Select(image.Rect(0, 0, 20, 20))
	painted := func() bool { return d.Composite(image.Rect(30, 10, 31, 11)).RGBAAt(30, 10).A == 255 }
	if !painted() || len(d.Layers()) != 2 {
		t.Fatalf("before: painted %v, %d layers", painted(), len(d.Layers()))
	}
	d.Wipe("Fresh")
	if painted() || len(d.Layers()) != 1 || d.Layers()[0].Name != "Fresh" || d.Active != d.Layers()[0].ID || d.Selection != nil {
		t.Errorf("after: painted %v, layers %d, selection %v", painted(), len(d.Layers()), d.Selection != nil)
	}
	d.Undo()
	if !painted() || len(d.Layers()) != 2 || d.Selection == nil {
		t.Errorf("undone: painted %v, layers %d, selection %v", painted(), len(d.Layers()), d.Selection != nil)
	}
}
