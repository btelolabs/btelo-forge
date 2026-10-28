package ui

import (
	"image"
	"image/color"
	"testing"
)

// The view shows each canvas pixel where viewPoint says it is, after a pan, a zoom and a change.
func TestViewShowsTheCanvasWhereItIs(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	d.Select(image.Rect(50, 40, 60, 50))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	ad.changedAll()
	check := func(when string) {
		t.Helper()
		x.frame(Input{})
		l := x.a.artLayout(x.a.layout().body)
		rp := l.view.Pixels()
		for _, p := range []struct {
			x, y float64
			want color.RGBA
		}{{55, 45, color.RGBA{255, 0, 0, 255}}, {200, 290, color.RGBA{255, 255, 255, 255}}, {5, 5, color.RGBA{255, 255, 255, 255}}} {
			sx, sy := x.viewPoint(ad, p.x, p.y)
			vx, vy := int(sx)-rp.Min.X, int(sy)-rp.Min.Y
			if got := ad.view.RGBAAt(vx, vy); got != p.want {
				t.Errorf("%s: canvas %v,%v shows %v, want %v", when, p.x, p.y, got, p.want)
			}
		}
	}
	check("at first")
	ad.panY += 37
	ad.fit = false
	check("after a pan")
	ad.zoomAt(x.a.artLayout(x.a.layout().body).view, 700, 500, 2)
	check("zoomed in")
	ad.zoomAt(x.a.artLayout(x.a.layout().body).view, 700, 500, 0.3)
	check("zoomed out")
}
