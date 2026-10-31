package printing

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Recording is a gfx canvas's drawing kept as ops on a page instead of being turned into pixels: Word's pages,
// spreadsheets and mind maps come through their ordinary drawing code this way, at no cost to it.
type Recording struct {
	Page *Page
	// M maps the canvas's pixels onto the page.
	M        Matrix
	pictures Images
	depth    int
}

// Images keeps one copy of each picture that is not premultiplied pixels already, so that a picture on many pages is
// one image in a file. A job shares one across its pages.
type Images map[image.Image]*image.RGBA

// Pixels is how many canvas pixels a recording gives a point. Pixels stand in for points in code written for the
// screen, which rounds to them and snaps lines to them, so they are made fine enough not to show on paper.
const Pixels = 10

// NewCanvas is a w × h canvas that adds what is drawn on it to the page, through m.
func NewCanvas(pg *Page, m Matrix, w, h float64) *gfx.Canvas {
	return gfx.NewRecorder(int(w+1), int(h+1), &Recording{Page: pg, M: m, pictures: Images{}})
}

// Canvas is a canvas over a whole page, at Pixels pixels a point.
func Canvas(pg *Page) *gfx.Canvas { return CanvasWith(pg, Images{}) }

// CanvasWith is a Canvas keeping its pictures in images.
func CanvasWith(pg *Page, images Images) *gfx.Canvas {
	return gfx.NewRecorder(int(pg.W*Pixels+1), int(pg.H*Pixels+1), &Recording{Page: pg, M: Scale(1.0/Pixels, 1.0/Pixels), pictures: images})
}

func nrgba(c color.RGBA) color.NRGBA { return color.NRGBA{c.R, c.G, c.B, c.A} }

func (r *Recording) path(p *Path) *Path { return p.Transformed(r.M) }

// Push narrows the clip.
func (r *Recording) Push(x gfx.Rect) {
	r.Page.Add(&Clip{Path: r.path(RectPath(float64(x.X), float64(x.Y), float64(x.W), float64(x.H)))})
	r.depth++
}

// PushRound narrows the clip to a rounded rectangle.
func (r *Recording) PushRound(x gfx.Rect, radius float32) {
	r.Page.Add(&Clip{Path: r.path(RoundRectPath(float64(x.X), float64(x.Y), float64(x.W), float64(x.H), float64(radius)))})
	r.depth++
}

// Pop widens the clip again.
func (r *Recording) Pop() {
	if r.depth > 0 {
		r.depth--
		r.Page.Add(&Unclip{})
	}
}

// Fill paints a rectangle.
func (r *Recording) Fill(x gfx.Rect, col color.RGBA) {
	if col.A != 0 {
		r.Page.Add(&Fill{Path: r.path(RectPath(float64(x.X), float64(x.Y), float64(x.W), float64(x.H))), Color: nrgba(col)})
	}
}

// RoundRect paints a rounded rectangle.
func (r *Recording) RoundRect(x gfx.Rect, radius float32, col color.RGBA) {
	if col.A != 0 {
		r.Page.Add(&Fill{Path: r.path(RoundRectPath(float64(x.X), float64(x.Y), float64(x.W), float64(x.H), float64(radius))), Color: nrgba(col)})
	}
}

// RoundOutline strokes just inside the edge of a rounded rectangle.
func (r *Recording) RoundOutline(x gfx.Rect, radius, w float32, col color.RGBA) {
	if col.A == 0 {
		return
	}
	in := float64(w) / 2
	p := RoundRectPath(float64(x.X)+in, float64(x.Y)+in, float64(x.W)-2*in, float64(x.H)-2*in, max(float64(radius)-in, 0))
	r.Page.Add(&Stroke{Path: r.path(p), Width: float64(w) * r.M.Size(), Join: JoinRound, Color: nrgba(col)})
}

// Line draws a straight line with round ends.
func (r *Recording) Line(x0, y0, x1, y1, w float32, col color.RGBA) {
	if col.A == 0 {
		return
	}
	p := &Path{}
	p.MoveTo(float64(x0), float64(y0))
	p.LineTo(float64(x1), float64(y1))
	r.Page.Add(&Stroke{Path: r.path(p), Width: float64(w) * r.M.Size(), Cap: CapRound, Join: JoinRound, Color: nrgba(col)})
}

// Polygon fills closed contours by the non-zero rule.
func (r *Recording) Polygon(contours [][][2]float32, col color.RGBA) {
	if col.A == 0 {
		return
	}
	p := &Path{}
	for _, ct := range contours {
		if len(ct) < 3 {
			continue
		}
		p.MoveTo(float64(ct[0][0]), float64(ct[0][1]))
		for _, q := range ct[1:] {
			p.LineTo(float64(q[0]), float64(q[1]))
		}
		p.Close()
	}
	if !p.Empty() {
		r.Page.Add(&Fill{Path: r.path(p), Color: nrgba(col)})
	}
}

// outlines keeps each character's outline as the list holds it.
var outlines sync.Map // *gfx.Outline to *Outline

func outlineOf(o *gfx.Outline) *Outline {
	if v, ok := outlines.Load(o); ok {
		return v.(*Outline)
	}
	p := &Path{Ops: o.Ops, Pts: make([]Point, len(o.Pts))}
	for i, q := range o.Pts {
		p.Pts[i] = Point{q[0], q[1]}
	}
	v, _ := outlines.LoadOrStore(o, &Outline{Path: p, Advance: o.Advance})
	return v.(*Outline)
}

// Text adds a run of characters with its top-left at (x, y), and gives the pen's place after it.
func (r *Recording) Text(fc *gfx.Face, x, y float32, s string, col color.RGBA) float32 {
	size := float64(fc.Size)
	pen := 0.0
	t := &Text{Color: nrgba(col), M: r.M.Mul(Matrix{size, 0, 0, -size, float64(x), float64(y + fc.Ascent)})}
	for _, c := range s {
		o := fc.Outline(c)
		if o == nil {
			continue
		}
		g := outlineOf(o)
		t.Glyphs = append(t.Glyphs, Glyph{Outline: g, X: pen, Text: string(c)})
		pen += g.Advance
	}
	if col.A != 0 && len(t.Glyphs) > 0 {
		r.Page.Add(t)
	}
	return x + float32(pen*size)
}

// TextTurned adds a run of characters turned clockwise by angle radians about its top-left at (x, y), and gives its length.
func (r *Recording) TextTurned(fc *gfx.Face, x, y float32, s string, col color.RGBA, angle float64) float32 {
	size := float64(fc.Size)
	sin, cos := math.Sincos(angle)
	turn := Matrix{cos, sin, -sin, cos, float64(x), float64(y)}
	t := &Text{Color: nrgba(col), M: r.M.Mul(turn).Mul(Matrix{size, 0, 0, -size, 0, float64(fc.Ascent)})}
	pen := 0.0
	for _, c := range s {
		o := fc.Outline(c)
		if o == nil {
			continue
		}
		g := outlineOf(o)
		t.Glyphs = append(t.Glyphs, Glyph{Outline: g, X: pen, Text: string(c)})
		pen += g.Advance
	}
	if col.A != 0 && len(t.Glyphs) > 0 {
		r.Page.Add(t)
	}
	return float32(pen * size)
}

// rgba is src as premultiplied pixels the job can keep: the picture itself when it is that already.
func (r *Recording) rgba(src image.Image) *image.RGBA {
	if img, ok := src.(*image.RGBA); ok {
		return img
	}
	if img, ok := r.pictures[src]; ok {
		return img
	}
	b := src.Bounds()
	img := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(img, img.Rect, src, b.Min, draw.Src)
	r.pictures[src] = img
	return img
}

// Image lays a picture into a rectangle, at all the pixels it has.
func (r *Recording) Image(src image.Image, dst gfx.Rect) {
	r.Page.Add(&Picture{Img: r.rgba(src), M: r.M.Mul(Matrix{float64(dst.W), 0, 0, float64(dst.H), float64(dst.X), float64(dst.Y)})})
}
