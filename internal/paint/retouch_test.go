package paint

import (
	"image"
	"image/color"
	"testing"
)

// retouchDoc is a 100×100 picture: grey on the left half, a saturated orange on the right.
func retouchDoc(t *testing.T) *Doc {
	t.Helper()
	d, err := NewFramed(100, 100, "Background", color.NRGBA{R: 128, G: 128, B: 128, A: 255})
	if err != nil {
		t.Fatal(err)
	}
	d.Select(image.Rect(50, 0, 100, 100))
	d.Fill(color.NRGBA{R: 230, G: 120, B: 20, A: 255})
	d.Deselect()
	return d
}

func retouchStroke(t *testing.T, d *Doc, rt Retouch, size float64, pts ...[2]float64) {
	t.Helper()
	b := DefaultBrush()
	b.Size, b.Hardness, b.Smooth = size, 1, 0
	s, _, err := d.BeginRetouch(b, rt, pts[0][0], pts[0][1])
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pts[1:] {
		s.To(p[0], p[1])
	}
	s.End()
}

func pixelAt(d *Doc, x, y int) color.RGBA {
	return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y)
}

func TestDodgeBurnAndSponge(t *testing.T) {
	d := retouchDoc(t)
	retouchStroke(t, d, Retouch{Kind: RetouchDodge, Range: RangeMidtones, Strength: 0.5}, 20, [2]float64{25, 20}, [2]float64{25, 30})
	if c := pixelAt(d, 25, 25); c.R <= 150 || c.R != c.G {
		t.Errorf("dodged grey %v", c)
	}
	if steps, _ := d.History(); steps[len(steps)-1] != "Dodge" {
		t.Errorf("steps %v", steps)
	}
	// Going over the same place in one stroke does not go past the strength.
	once := pixelAt(d, 25, 25)
	d.Undo()
	retouchStroke(t, d, Retouch{Kind: RetouchDodge, Range: RangeMidtones, Strength: 0.5}, 20,
		[2]float64{25, 20}, [2]float64{25, 30}, [2]float64{25, 20}, [2]float64{25, 30})
	if c := pixelAt(d, 25, 25); c != once {
		t.Errorf("crossing again dodged %v, once %v", c, once)
	}
	retouchStroke(t, d, Retouch{Kind: RetouchBurn, Range: RangeMidtones, Strength: 0.5}, 20, [2]float64{25, 70}, [2]float64{25, 80})
	if c := pixelAt(d, 25, 75); c.R >= 110 {
		t.Errorf("burnt grey %v", c)
	}
	// Burning the shadows barely touches the highlights.
	before := pixelAt(d, 75, 50)
	retouchStroke(t, d, Retouch{Kind: RetouchBurn, Range: RangeShadows, Strength: 1}, 10, [2]float64{75, 50})
	if c := pixelAt(d, 75, 50); int(before.R)-int(c.R) > 10 {
		t.Errorf("burning the shadows darkened bright orange from %v to %v", before, c)
	}
	// The sponge takes the colour away, leaving the brightness.
	retouchStroke(t, d, Retouch{Kind: RetouchSponge, Desaturate: true, Strength: 1}, 20, [2]float64{75, 20}, [2]float64{75, 30})
	if c := pixelAt(d, 75, 25); c.R != c.G || c.G != c.B {
		t.Errorf("sponged orange %v", c)
	}
	// On a layer mask they refuse.
	d.AddMask()
	d.EditMask = true
	if _, _, err := d.BeginRetouch(DefaultBrush(), Retouch{Kind: RetouchBlur, Strength: 1}, 5, 5); err != ErrRetouchMask {
		t.Errorf("on a mask: %v", err)
	}
}

func TestBlurSharpenAndSmudge(t *testing.T) {
	d := retouchDoc(t)
	edge := func() (int, int) { return int(pixelAt(d, 48, 50).R), int(pixelAt(d, 51, 50).R) }
	l0, r0 := edge()
	retouchStroke(t, d, Retouch{Kind: RetouchBlur, Strength: 1}, 40, [2]float64{50, 40}, [2]float64{50, 60})
	l1, r1 := edge()
	if !(r1-l1 < r0-l0 && l1 > l0) {
		t.Errorf("blur left the edge at %d|%d, was %d|%d", l1, r1, l0, r0)
	}
	if steps, _ := d.History(); steps[len(steps)-1] != "Blur" {
		t.Errorf("steps %v", steps)
	}
	d.Undo()
	retouchStroke(t, d, Retouch{Kind: RetouchSharpen, Strength: 1}, 40, [2]float64{50, 40}, [2]float64{50, 60})
	l2, r2 := edge()
	if !(l2 < l0 && r2 > r0) {
		t.Errorf("sharpen left the edge at %d|%d, was %d|%d", l2, r2, l0, r0)
	}
	// Away from any edge, they change nothing.
	if c := pixelAt(d, 50, 90); c.R != 128 && c.R != 230 {
		t.Errorf("flat colour changed to %v", c)
	}
	d.Undo()
	// Smudge drags the orange into the grey.
	grey := pixelAt(d, 35, 50)
	retouchStroke(t, d, Retouch{Kind: RetouchSmudge, Strength: 0.9}, 16, [2]float64{70, 50}, [2]float64{30, 50})
	if c := pixelAt(d, 45, 50); c.R <= grey.R+20 || c.B >= grey.B {
		t.Errorf("smudge left %v at the grey's edge (grey %v)", c, grey)
	}
	if steps, _ := d.History(); steps[len(steps)-1] != "Smudge" {
		t.Errorf("steps %v", steps)
	}
	d.Undo()
	if c := pixelAt(d, 45, 50); c != grey {
		t.Errorf("undo left %v", c)
	}
}
