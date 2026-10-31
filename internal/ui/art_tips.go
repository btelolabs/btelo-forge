package ui

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Brush tips made from pictures, as Photoshop's Define Brush Preset: Layer › Define brush tip (in the layers panel's
// right-click menu too) makes one of the selected part of the chosen layer, or of all of it cut to what it holds,
// and chooses it. Each is kept as a greyscale PNG in the brushes folder next to the settings, offered in the brush's
// options bar after the round, square, flat and chalk tips; a right-click there removes one.

// tipsDir is where the brush tips made from pictures are kept.
func (a *App) tipsDir() string { return filepath.Join(a.stateDir, "brushes") }

// tipShape is the picture tip kept in file name, read once, or nil when it cannot be read.
func (a *App) tipShape(name string) *paint.TipShape {
	if name == "" {
		return nil
	}
	if t, ok := a.tipShapes[name]; ok {
		return t
	}
	t, err := readTip(filepath.Join(a.tipsDir(), name))
	if err != nil {
		t = nil // gone or damaged: the brush falls back to the round tip
	}
	if a.tipShapes == nil {
		a.tipShapes = map[string]*paint.TipShape{}
	}
	a.tipShapes[name] = t
	return t
}

func readTip(path string) (*paint.TipShape, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	g, ok := img.(*image.Gray)
	if !ok {
		b := img.Bounds()
		g = image.NewGray(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				g.Set(x, y, img.At(x, y))
			}
		}
	}
	return paint.TipShapeOf(g)
}

// defineTip makes a brush tip of the chosen layer (within the selection), keeps it, and chooses it.
func (a *App) defineTip(ad *artDoc) {
	u := &a.u
	tip, err := ad.d.TipFromLayer()
	if err == nil {
		err = os.MkdirAll(a.tipsDir(), 0o755)
	}
	name := fmt.Sprintf("tip-%s.png", time.Now().Format("20060102-150405.000"))
	if err == nil {
		err = writePNG(filepath.Join(a.tipsDir(), name), tip.Gray())
	}
	if err != nil {
		a.toast(u.tf("Could not make a brush tip: %s", a.describe(err)), true)
		return
	}
	if a.tipShapes == nil {
		a.tipShapes = map[string]*paint.TipShape{}
	}
	a.tipShapes[name] = tip
	p := &a.settings.Paint
	p.Tips = append(p.Tips, name)
	size := a.brush.Size
	a.chooseTip(name)
	a.brush.Size = float64(max(tip.W, tip.H)) // the tip as it was made
	a.chosenBrush(size)
	a.saveBrush()
	a.toast(u.tf("Made a %d × %d brush tip and chose it", tip.W, tip.H), false)
}

// chooseTip chooses the picture tip kept in file name for the brush.
func (a *App) chooseTip(name string) {
	tip := a.tipShape(name)
	if tip == nil {
		return
	}
	size := a.brush.Size
	a.brush.Tip, a.brush.Shape = paint.TipShaped, tip
	a.settings.Paint.ShapeTip = name
	a.chosenBrush(size)
	a.saveBrush()
}

// removeTip takes away the picture tip kept in file name; a brush using it goes back to the round tip.
func (a *App) removeTip(name string) {
	p := &a.settings.Paint
	p.Tips = slices.DeleteFunc(p.Tips, func(s string) bool { return s == name })
	if err := os.Remove(filepath.Join(a.tipsDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.toast(a.u.tf("Could not remove the brush tip: %s", a.describe(err)), true)
	}
	delete(a.tipShapes, name)
	if p.ShapeTip == name {
		p.ShapeTip = ""
		a.brush.Tip, a.brush.Shape = paint.TipRound, nil
	}
	a.saveBrush()
}

// drawTipButtons are the picture tips in the brush's options bar, from x: a click chooses one, a right-click offers
// to remove it. It returns where the next control goes.
func (a *App) drawTipButtons(x float32, r *gfx.Rect) float32 {
	u := &a.u
	for _, name := range a.settings.Paint.Tips {
		tip := a.tipShape(name)
		if tip == nil {
			continue
		}
		a.optRow(&x, r, u.px(30))
		tr := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		if tr.Right() > r.Right() {
			break
		}
		id := "art/tip/shape/" + name
		bg := colSurface
		switch {
		case a.brush.Tip == paint.TipShaped && a.settings.Paint.ShapeTip == name:
			bg = colSelected
		case u.hover(id, tr):
			bg = colSurfaceHi
		}
		u.c.RoundRect(tr, u.px(6), bg)
		drawTipShape(u, tip, tr.Inset(u.px(4)))
		u.hint(id, u.t("Brush tip made from a picture: right-click to remove it"), tr)
		if u.clicked(id, tr) {
			a.chooseTip(name)
		}
		if u.rightAt != nil && tr.Contains(u.rightAt.X, u.rightAt.Y) && !u.blocked {
			n := name
			a.openMenu(id+"/menu", gfx.Rect{X: u.rightAt.X, Y: u.rightAt.Y, W: 1, H: 1},
				[]menuItem{{label: u.t("Remove the brush tip"), run: func() { a.removeTip(n) }}})
			u.rightAt = nil
		}
		x += tr.W + u.px(4)
	}
	return x
}

// drawTipShape draws a picture tip fitted into r, light on the button.
func drawTipShape(u *ctx, tip *paint.TipShape, r gfx.Rect) {
	img := u.c.Img
	pr := r.Pixels()
	scale := float64(max(tip.W, tip.H)) / float64(min(pr.Dx(), pr.Dy()))
	cx, cy := float64(pr.Min.X+pr.Max.X)/2, float64(pr.Min.Y+pr.Max.Y)/2
	for y := pr.Min.Y; y < pr.Max.Y; y++ {
		for x := pr.Min.X; x < pr.Max.X; x++ {
			tx, ty := int((float64(x)+0.5-cx)*scale+float64(tip.W)/2), int((float64(y)+0.5-cy)*scale+float64(tip.H)/2)
			if tx < 0 || ty < 0 || tx >= tip.W || ty >= tip.H || !(image.Point{x, y}).In(img.Rect) {
				continue
			}
			k := float32(tip.Pix[ty*tip.W+tx]) / 255
			if k == 0 {
				continue
			}
			old := img.RGBAAt(x, y)
			mix := func(o uint8) uint8 { return uint8(float32(o)*(1-k) + 235*k) }
			img.SetRGBA(x, y, color.RGBA{mix(old.R), mix(old.G), mix(old.B), 255})
		}
	}
}

// brushOutline is the outline of the brush's dab at the pointer, as the view shows it: a picture the dab's size,
// clear but for the edge where the dab covers half or more, dark inside light so it shows on any picture. It is
// made again when the brush or the zoom changes.
type brushOutline struct {
	brush paint.Brush
	zoom  float64
	img   *image.RGBA // centred on the pointer
}

// maxOutline is the widest, in screen pixels, a brush's outline is worked out at; a larger brush shows a circle.
const maxOutline = 1600

// drawBrushOutline draws the brush's true shape at the pointer: its tip, size, angle and roundness.
func (a *App) drawBrushOutline(ad *artDoc) {
	u := &a.u
	b := a.brush
	if a.artTool == toolPencil {
		b = a.vectorBrush()
	}
	d := b.Size * ad.zoom
	if d < 4 {
		return // a dot is shown by the pointer itself
	}
	if d > maxOutline {
		r := float32(d / 2)
		ring := gfx.Rect{X: u.mx - r, Y: u.my - r, W: 2 * r, H: 2 * r}
		u.c.RoundOutline(ring, r, max(1, u.px(1)), gfx.Alpha(colBlack, 0.6))
		u.c.RoundOutline(ring.Inset(-1), r+1, max(1, u.px(1)), gfx.Alpha(colWhite, 0.8))
		return
	}
	o := &a.brushShape
	b.Color, b.Opacity, b.Flow = color.NRGBA{}, 1, 1 // the shape alone: these do not change it
	if o.img == nil || o.zoom != ad.zoom || !sameShape(o.brush, b) {
		o.brush, o.zoom, o.img = b, ad.zoom, outlineOf(paint.Footprint(b, ad.zoom))
	}
	r := o.img.Rect
	u.c.Blit(o.img, int(math.Round(float64(u.mx)))+r.Min.X, int(math.Round(float64(u.my)))+r.Min.Y, false)
}

// sameShape reports whether two brushes lay the same dab.
func sameShape(a, b paint.Brush) bool {
	return a.Tip == b.Tip && a.Shape == b.Shape && a.Size == b.Size && a.Angle == b.Angle && a.Round == b.Round && a.Hardness == b.Hardness
}

// outlineOf is the edge of a dab: the pixels covered half or more with a neighbour that is not, dark, with the
// neighbours outside them light.
func outlineOf(f *image.Alpha) *image.RGBA {
	r := f.Rect
	in := func(x, y int) bool { return (image.Point{x, y}).In(r) && f.Pix[f.PixOffset(x, y)] >= 128 }
	edge := func(x, y int) bool { return in(x, y) && !(in(x-1, y) && in(x+1, y) && in(x, y-1) && in(x, y+1)) }
	out := image.NewRGBA(r.Inset(-1))
	dark, light := color.RGBA{0, 0, 0, 160}, color.RGBA{255, 255, 255, 210}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if !edge(x, y) {
				continue
			}
			out.SetRGBA(x, y, dark)
			for _, n := range [4]image.Point{{x - 1, y}, {x + 1, y}, {x, y - 1}, {x, y + 1}} {
				if !in(n.X, n.Y) {
					out.SetRGBA(n.X, n.Y, light)
				}
			}
		}
	}
	return out
}
