package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/render"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

// The formats as they print, each in terms of printSource. Spreadsheets, which have the most to work out, have a file
// of their own.

// Word prints its pages as laid out, each at the size its section gives it.

type wordPrint struct {
	e      *wordEditor
	images printing.Images
}

func (s *wordPrint) name() string { return docName(s.e.od.path) }

func (s *wordPrint) options(p *printDialog, x, y, w float32) float32 {
	p.note(p.a.u.t("Each page prints at the size and the way round its section gives it."), x, &y, w)
	return y
}

func (s *wordPrint) count() int {
	s.e.d.Finish()
	return len(s.e.d.Pages())
}

func (s *wordPrint) page(i int) (*printing.Page, error) {
	d := s.e.d
	d.Finish()
	pages := d.Pages()
	if i < 0 || i >= len(pages) {
		return nil, fmt.Errorf("there is no page %d", i+1)
	}
	pg := pages[i]
	out := &printing.Page{W: float64(pg.W), H: float64(pg.H)}
	// The pictures are decoded by a first drawing, which is thrown away, as they may be under way already.
	d.Pics.Sync = true
	d.DrawPage(printing.CanvasWith(&printing.Page{W: out.W, H: out.H}, s.images), pg, 0, 0, printing.Pixels)
	d.Pics.Sync = false
	d.Pics.Wait()
	d.DrawPage(printing.CanvasWith(out, s.images), pg, 0, 0, printing.Pixels)
	return out, nil
}

// PowerPoint prints whole slides, handouts or notes pages.

const (
	slidesWhole = iota
	slidesNotes
	slidesTwo
	slidesFour
	slidesSix
)

type slidesPrint struct {
	e      *slidesEditor
	layout int
	hidden bool
	paper  int
}

func (s *slidesPrint) name() string { return docName(s.e.od.path) }

func (s *slidesPrint) options(p *printDialog, x, y, w float32) float32 {
	u := &p.a.u
	p.choice("slides/layout", u.t("What to print"), []string{u.t("Full slides"), u.t("Notes pages"), u.t("2 slides a page"), u.t("4 slides a page"), u.t("6 slides a page")},
		s.layout, x, &y, w, func(i int) { s.layout = i })
	if s.layout != slidesWhole {
		p.choice("slides/paper", u.t("Paper"), printPaperNames(), s.paper, x, &y, w, func(i int) { s.paper = i })
	}
	p.check("slides/hidden", u.t("Print hidden slides"), &s.hidden, x, &y, w)
	return y
}

func (s *slidesPrint) slides() []*pptx.Slide {
	var out []*pptx.Slide
	for _, k := range s.e.d.Slides {
		if s.hidden || !k.Hidden() {
			out = append(out, k)
		}
	}
	return out
}

func (s *slidesPrint) perPage() int {
	switch s.layout {
	case slidesTwo:
		return 2
	case slidesFour:
		return 4
	case slidesSix:
		return 6
	}
	return 1
}

func (s *slidesPrint) count() int {
	n := len(s.slides())
	k := s.perPage()
	return (n + k - 1) / k
}

// slideOn adds a slide to a page, in the box.
func (s *slidesPrint) slideOn(pg *printing.Page, k *pptx.Slide, x, y, w, h float64) {
	d := s.e.d
	one := &printing.Page{W: d.W, H: d.H}
	s.e.r.PrintSlide(one, k)
	f := min(w/d.W, h/d.H)
	sw, sh := d.W*f, d.H*f
	sx, sy := x+(w-sw)/2, y+(h-sh)/2
	pg.Place(one, printing.Translate(sx, sy).Mul(printing.Scale(f, f)))
	pg.Add(&printing.Stroke{Path: printing.RectPath(sx, sy, sw, sh), Width: 0.5, Color: color.NRGBA{120, 120, 120, 255}})
}

func (s *slidesPrint) page(i int) (*printing.Page, error) {
	list := s.slides()
	d := s.e.d
	if s.layout == slidesWhole {
		if i >= len(list) {
			return nil, fmt.Errorf("there is no slide %d", i+1)
		}
		pg := &printing.Page{W: d.W, H: d.H, Fit: true}
		s.e.r.PrintSlide(pg, list[i])
		return pg, nil
	}
	paper := printPapers[s.paper]
	pg := &printing.Page{W: min(paper.w, paper.h), H: max(paper.w, paper.h)}
	const margin = 36.0
	w, h := pg.W-2*margin, pg.H-2*margin
	if s.layout == slidesNotes {
		if i >= len(list) {
			return nil, fmt.Errorf("there is no slide %d", i+1)
		}
		s.slideOn(pg, list[i], margin, margin, w, h*0.45)
		font := s.e.a.officeBook()
		paragraph := flow.Para{After: 6, Runs: []flow.Run{{Text: d.Notes(list[i]), Style: flow.Style{Size: 12, Color: color.RGBA{A: 255}}}}}
		laid := flow.Layout(&paragraph, float32(w), font)
		c := printing.Canvas(pg)
		laid.Draw(c, float32(margin*printing.Pixels), float32((margin+h*0.5)*printing.Pixels), printing.Pixels, font, nil, color.RGBA{A: 255})
		return pg, nil
	}
	n := s.perPage()
	cols, rows := 1, 2
	if n == 4 {
		cols = 2
	} else if n == 6 {
		cols, rows = 2, 3
	}
	const gap = 18.0
	cw, ch := (w-gap*float64(cols-1))/float64(cols), (h-gap*float64(rows-1))/float64(rows)
	for k := range n {
		at := i*n + k
		if at >= len(list) {
			break
		}
		s.slideOn(pg, list[at], margin+float64(k%cols)*(cw+gap), margin+float64(k/cols)*(ch+gap), cw, ch)
	}
	return pg, nil
}

// A PDF prints from its own pages, with its annotations or without, at its size or filling the paper.

type pdfPrint struct {
	e      *pdfEditor
	annots bool
	actual bool
}

// apart says the pages can be drawn away from the window: the renderer is made for it, and a page of a PDF can take
// a long time.
func (s *pdfPrint) apart() bool { return true }

// file is the PDF itself, which a viewer can print and which needs no exporting.
func (s *pdfPrint) file() string {
	od := s.e.od
	if od.ed.version() != od.saved {
		od.dirty = true
	}
	if err := s.e.a.saveOffice(od, true); err != nil {
		s.e.a.toast(s.e.a.u.tf("The document could not be saved: %s", s.e.a.describe(err)), true)
	}
	return od.path
}

func (s *pdfPrint) name() string { return docName(s.e.od.path) }

func (s *pdfPrint) options(p *printDialog, x, y, w float32) float32 {
	u := &p.a.u
	p.check("pdf/annots", u.t("Print the annotations"), &s.annots, x, &y, w)
	size := 0
	if s.actual {
		size = 1
	}
	p.choice("pdf/size", u.t("Size"), []string{u.t("Fit to the paper"), u.t("Actual size")}, size, x, &y, w, func(i int) { s.actual = i == 1 })
	return y
}

func (s *pdfPrint) count() int {
	s.e.sync()
	return len(s.e.pages)
}

func (s *pdfPrint) page(i int) (*printing.Page, error) {
	if i < 0 || i >= len(s.e.pages) {
		return nil, fmt.Errorf("there is no page %d", i+1)
	}
	pg, err := s.e.cache.PrintPage(context.Background(), s.e.pages[i], render.PrintOptions{Annots: s.annots, DPI: 300})
	if pg != nil {
		pg.Fit = !s.actual
	}
	return pg, err
}

// A canvas prints what shows on it, on one page or, at its actual size, across as many as that takes.

const (
	canvasPointsPerPixel = 0.75 // a canvas's pixels at actual size: 96 to the inch
	canvasMargin         = 24.0
)

type canvasPrint struct {
	a      *App
	ad     *artDoc
	actual bool
	board  int // 0 is the whole canvas, else an artboard, counted from one
	paper  int
	extent image.Rectangle
}

// newCanvasPrint starts on the artboard being worked on, when there are any.
func newCanvasPrint(a *App, ad *artDoc) *canvasPrint {
	s := &canvasPrint{a: a, ad: ad}
	d := ad.d
	s.extent = d.Extent()
	boards := d.Boards()
	for _, b := range boards {
		s.extent = s.extent.Union(b.Rect)
	}
	if len(boards) > 0 {
		s.board = 1
		active := d.BoardOf(d.Active)
		for i, b := range boards {
			if b.ID == active {
				s.board = i + 1
			}
		}
	}
	return s
}

func (s *canvasPrint) name() string { return docName(s.ad.path) }

// area is what is printed, and the artboard it is, if any.
func (s *canvasPrint) area() (image.Rectangle, int) {
	d := s.ad.d
	boards := d.Boards()
	if s.board > 0 && s.board <= len(boards) {
		return boards[s.board-1].Rect, boards[s.board-1].ID
	}
	return s.extent, 0
}

// sheet lays the area out: the paper, the scale in points a pixel, and how many pages it covers.
func (s *canvasPrint) sheet() (w, h, k float64, nx, ny int) {
	r, _ := s.area()
	cw, ch := float64(r.Dx()), float64(r.Dy())
	w, h = fitPaper(printPapers[s.paper], cw, ch)
	availW, availH := w-2*canvasMargin, h-2*canvasMargin
	if s.actual {
		k, nx, ny = tileScale(canvasPointsPerPixel, cw, ch, availW, availH)
		return w, h, k, nx, ny
	}
	if cw <= 0 || ch <= 0 {
		return w, h, 1, 1, 1
	}
	return w, h, min(availW/cw, availH/ch), 1, 1
}

func (s *canvasPrint) options(p *printDialog, x, y, w float32) float32 {
	u := &p.a.u
	if boards := s.ad.d.Boards(); len(boards) > 0 {
		names := []string{u.t("The whole canvas")}
		for _, b := range boards {
			names = append(names, b.Name)
		}
		p.choice("canvas/board", u.t("What to print"), names, s.board, x, &y, w, func(i int) { s.board = i })
	}
	size := 0
	if s.actual {
		size = 1
	}
	p.choice("canvas/size", u.t("Size"), []string{u.t("Fit to the paper"), u.t("Actual size")}, size, x, &y, w, func(i int) { s.actual = i == 1 })
	p.choice("canvas/paper", u.t("Paper"), printPaperNames(), s.paper, x, &y, w, func(i int) { s.paper = i })
	_, _, k, _, _ := s.sheet()
	if k > 0 {
		p.note(u.tf("Pictures print at %d dots an inch.", int(72/k+0.5)), x, &y, w)
	}
	return y
}

func (s *canvasPrint) count() int {
	_, _, _, nx, ny := s.sheet()
	return nx * ny
}

func (s *canvasPrint) page(i int) (*printing.Page, error) {
	r, board := s.area()
	w, h, k, nx, ny := s.sheet()
	pg := &printing.Page{W: w, H: h}
	availW, availH := w-2*canvasMargin, h-2*canvasMargin
	col, row := i%nx, i/nx
	if row >= ny {
		return nil, fmt.Errorf("there is no page %d", i+1)
	}
	var m printing.Matrix
	if nx == 1 && ny == 1 {
		// In the middle of the page.
		m = printing.Translate((w-float64(r.Dx())*k)/2, (h-float64(r.Dy())*k)/2)
	} else {
		m = printing.Translate(canvasMargin-float64(col)*availW, canvasMargin-float64(row)*availH)
	}
	m = m.Mul(printing.Scale(k, k)).Mul(printing.Translate(float64(-r.Min.X), float64(-r.Min.Y)))
	if nx*ny > 1 {
		pg.ClipRect(canvasMargin, canvasMargin, availW, availH)
		defer pg.Add(&printing.Unclip{})
	}
	s.ad.d.Print(r, board, &artPrinter{a: s.a, pg: pg, m: m})
	return pg, nil
}

// artPrinter puts a canvas's layers on a page.
type artPrinter struct {
	a  *App
	pg *printing.Page
	m  printing.Matrix // from canvas pixels to points
}

func (p *artPrinter) Picture(img *image.RGBA) {
	r := img.Rect
	p.pg.Add(&printing.Picture{Img: img, M: p.m.Mul(printing.Matrix{float64(r.Dx()), 0, 0, float64(r.Dy()), float64(r.Min.X), float64(r.Min.Y)})})
}

func (p *artPrinter) Rect(r image.Rectangle, c color.NRGBA) {
	path := printing.RectPath(float64(r.Min.X), float64(r.Min.Y), float64(r.Dx()), float64(r.Dy()))
	p.pg.Add(&printing.Fill{Path: path.Transformed(p.m), Color: c})
}

func (p *artPrinter) PushRect(r image.Rectangle) {
	path := printing.RectPath(float64(r.Min.X), float64(r.Min.Y), float64(r.Dx()), float64(r.Dy()))
	p.pg.Add(&printing.Clip{Path: path.Transformed(p.m)})
}

func (p *artPrinter) PushShape(v *paint.Vector) {
	p.pg.Add(&printing.Clip{Path: vectorPath(v).Transformed(p.m), EvenOdd: v.Rule == paint.EvenOdd})
}

func (p *artPrinter) Pop() { p.pg.Add(&printing.Unclip{}) }

func (p *artPrinter) Words(t paint.Text, opacity float64) {
	f := p.a.textFace(t)
	col := color.RGBA{t.Color.R, t.Color.G, t.Color.B, uint8(float64(t.Color.A)*opacity + 0.5)}
	c := printing.NewCanvas(p.pg, p.m, 1, 1)
	for i, line := range t.Lines() {
		c.Text(f, float32(p.a.lineX(t, f, line)), float32(t.Y+float64(i)*t.LineHeight()), line, col)
	}
}

// affine is a vector's matrix in the list's terms.
func affine(m paint.Affine) printing.Matrix { return printing.Matrix{m.A, m.D, m.B, m.E, m.C, m.F} }

// vectorPath is an object's outline in canvas pixels, shapes combined.
func vectorPath(v *paint.Vector) *printing.Path {
	out := &printing.Path{}
	for _, s := range v.Combined() {
		if len(s.Nodes) == 0 {
			continue
		}
		first := s.Nodes[0]
		out.MoveTo(first.P.X, first.P.Y)
		prev := first
		for _, n := range s.Nodes[1:] {
			out.CurveTo(prev.Out.X, prev.Out.Y, n.In.X, n.In.Y, n.P.X, n.P.Y)
			prev = n
		}
		if s.Closed {
			out.CurveTo(prev.Out.X, prev.Out.Y, first.In.X, first.In.Y, first.P.X, first.P.Y)
			out.Close()
		}
	}
	return out.Transformed(affine(v.M))
}

// paintStops are a gradient's stops, and whether they are all opaque and the object can go as a gradient.
func paintStops(p paint.VectorPaint) ([]printing.Stop, bool) {
	out := make([]printing.Stop, len(p.Stops))
	for i, s := range p.Stops {
		if s.Color.A != 255 {
			return nil, false
		}
		out[i] = printing.Stop{At: s.At, Color: s.Color}
	}
	return out, len(out) > 1
}

// shade is a paint over a path on the page, nil for one that cannot be.
func (p *artPrinter) shade(v *paint.Vector, vp paint.VectorPaint, path *printing.Path, evenOdd bool, opacity float64) *printing.Shade {
	stops, ok := paintStops(vp)
	if !ok {
		return nil
	}
	s := &printing.Shade{Path: path, EvenOdd: evenOdd, Stops: stops, Extend: [2]bool{true, true}, M: p.m.Mul(affine(v.M))}
	if opacity < 1 {
		s.Alpha = max(opacity, 1e-3)
	}
	switch vp.Kind {
	case paint.PaintLinear:
		s.X0, s.Y0, s.X1, s.Y1 = vp.From.X, vp.From.Y, vp.To.X, vp.To.Y
	case paint.PaintRadial:
		// A unit circle, carried by a matrix onto the gradient's ellipse.
		to, across := vp.To.Sub(vp.From), vp.Across().Sub(vp.From)
		s.Radial, s.R1 = true, 1
		s.M = s.M.Mul(printing.Matrix{to.X, to.Y, across.X, across.Y, vp.From.X, vp.From.Y})
	default:
		return nil
	}
	return s
}

// layPaint lays a fill or a stroke's paint over a path; false means it must go as pixels.
func (p *artPrinter) layPaint(v *paint.Vector, vp paint.VectorPaint, path *printing.Path, evenOdd bool, opacity float64) bool {
	switch {
	case vp.Kind == paint.PaintNone:
		return true
	case vp.Kind == paint.PaintSolid:
		c := vp.Color
		c.A = uint8(float64(c.A)*opacity + 0.5)
		p.pg.Add(&printing.Fill{Path: path, EvenOdd: evenOdd, Color: c})
		return true
	}
	if s := p.shade(v, vp, path, evenOdd, opacity); s != nil {
		p.pg.Add(s)
		return true
	}
	return false
}

func (p *artPrinter) Vector(v *paint.Vector, opacity float64) {
	path := vectorPath(v).Transformed(p.m)
	evenOdd := v.Rule == paint.EvenOdd && !v.Compound()
	simple := func(q paint.VectorPaint) bool {
		if q.Kind == paint.PaintNone || q.Kind == paint.PaintSolid {
			return true
		}
		_, ok := paintStops(q)
		return ok && q.Kind != paint.PaintConical
	}
	s := v.Stroke
	if !simple(v.Fill) || !simple(s.Paint) {
		p.vectorPixels(v, opacity)
		return
	}
	p.layPaint(v, v.Fill, path, evenOdd, opacity)
	if s.Paint.Kind == paint.PaintNone || s.Width <= 0 {
		return
	}
	k := p.m.Size()
	width := s.Width * k
	align := s.Align
	closed := true
	for _, sp := range v.Combined() {
		closed = closed && sp.Closed
	}
	if !closed {
		align = paint.AlignCentre
	}
	if align != paint.AlignCentre {
		width *= 2
		if align == paint.AlignInside {
			p.pg.Add(&printing.Clip{Path: path, EvenOdd: evenOdd})
		} else {
			// Everything but the shape.
			outside := printing.RectPath(-1e6, -1e6, 2e6, 2e6)
			outside.Append(path)
			p.pg.Add(&printing.Clip{Path: outside, EvenOdd: true})
		}
		defer p.pg.Add(&printing.Unclip{})
	}
	st := &printing.Stroke{Path: path, Width: width, Miter: s.Miter}
	if st.Miter <= 0 {
		st.Miter = 4
	}
	st.Cap = printing.Cap(s.Cap)
	st.Join = printing.Join(s.Join)
	for _, d := range s.Dash {
		st.Dash = append(st.Dash, d*width)
	}
	if s.Paint.Kind == paint.PaintSolid {
		st.Color = s.Paint.Color
		st.Color.A = uint8(float64(st.Color.A)*opacity + 0.5)
		p.pg.Add(st)
		return
	}
	p.layPaint(v, s.Paint, printing.StrokeOutline(st, 4), false, opacity)
}

// vectorPixels draws an object as a picture finer than the page needs, for what a page has no gradient for.
func (p *artPrinter) vectorPixels(v *paint.Vector, opacity float64) {
	const scale = 2.0
	r := v.PixelBounds()
	box := image.Rect(int(float64(r.Min.X)*scale), int(float64(r.Min.Y)*scale), int(float64(r.Max.X)*scale), int(float64(r.Max.Y)*scale))
	if box.Empty() || box.Dx()*box.Dy() > 1<<27 {
		return
	}
	img := image.NewRGBA(box)
	v.Render(img, box, scale)
	if opacity < 1 {
		img = gfx.Fade(img, opacity)
	}
	p.pg.Add(&printing.Picture{Img: img, M: p.m.Mul(printing.Matrix{float64(r.Dx()), 0, 0, float64(r.Dy()), float64(r.Min.X), float64(r.Min.Y)})})
}

// A mind map prints fitted to one page, or across several at its own size.

const (
	mindMargin      = 24.0
	mindActualScale = 0.75 // points a board unit, tiled
	mindPrintTheme  = "paper"
)

type mindPrint struct {
	a     *App
	md    *mindDoc
	tiled bool
	paper int
	light bool      // a dark board prints in the light theme, on white
	copy  *mind.Map // the map in that theme
}

// newMindPrint saves the ink: a dark board starts out light.
func newMindPrint(a *App, md *mindDoc) *mindPrint {
	return &mindPrint{a: a, md: md, light: !md.m.Theme().Light}
}

// board is the map as it prints.
func (s *mindPrint) board() *mind.Map {
	if !s.light {
		return s.md.m
	}
	if s.copy == nil {
		s.copy = s.md.m.Themed(mindPrintTheme)
	}
	return s.copy
}

func (s *mindPrint) name() string { return docName(s.md.path) }

func (s *mindPrint) options(p *printDialog, x, y, w float32) float32 {
	u := &p.a.u
	layout := 0
	if s.tiled {
		layout = 1
	}
	p.choice("mind/layout", u.t("Layout"), []string{u.t("Fit the whole board on one page"), u.t("Across several pages, at its own size")}, layout, x, &y, w, func(i int) { s.tiled = i == 1 })
	p.choice("mind/paper", u.t("Paper"), printPaperNames(), s.paper, x, &y, w, func(i int) { s.paper = i })
	if !s.md.m.Theme().Light {
		p.check("mind/light", u.t("Print on white, in the light theme"), &s.light, x, &y, w)
	}
	return y
}

// sheet is the paper, the points a board unit, and how many pages.
func (s *mindPrint) sheet() (b mind.Rect, w, h, k float64, nx, ny int) {
	m := s.board()
	m.Layout()
	b = m.Bounds()
	if b.Empty() {
		b = mind.R(0, 0, 100, 100)
	}
	b = b.Grow(16)
	w, h = fitPaper(printPapers[s.paper], b.W(), b.H())
	availW, availH := w-2*mindMargin, h-2*mindMargin
	if s.tiled {
		k, nx, ny = tileScale(mindActualScale, b.W(), b.H(), availW, availH)
		return b, w, h, k, nx, ny
	}
	return b, w, h, min(availW/b.W(), availH/b.H(), 1.5), 1, 1
}

func (s *mindPrint) count() int {
	_, _, _, _, nx, ny := s.sheet()
	return nx * ny
}

func (s *mindPrint) page(i int) (*printing.Page, error) {
	b, w, h, k, nx, ny := s.sheet()
	col, row := i%nx, i/nx
	if row >= ny {
		return nil, fmt.Errorf("there is no page %d", i+1)
	}
	pg := &printing.Page{W: w, H: h}
	m := s.board()
	if !s.light {
		th := m.Theme()
		pg.Add(&printing.Fill{Path: printing.RectPath(0, 0, w, h), Color: color.NRGBA{th.Paper.R, th.Paper.G, th.Paper.B, 255}})
	}
	const px = printing.Pixels
	availW, availH := w-2*mindMargin, h-2*mindMargin
	var ox, oy float64
	if nx == 1 && ny == 1 {
		ox, oy = (w-b.W()*k)/2, (h-b.H()*k)/2
	} else {
		ox, oy = mindMargin-float64(col)*availW, mindMargin-float64(row)*availH
		pg.ClipRect(mindMargin, mindMargin, availW, availH)
		defer pg.Add(&printing.Unclip{})
	}
	c := printing.Canvas(pg)
	z := k * px
	view := mind.View{Zoom: z, OX: (ox - b.X0*k) * px, OY: (oy - b.Y0*k) * px}
	mind.Render(c, m, view, &mind.Options{Fonts: s.a.u.fonts, Pictures: mindExportPictures{s.a, s.md.path}, Labels: s.a.mindLabels()})
	return pg, nil
}

// printOffice opens the Print dialog for an Office tab.
func (a *App) printOffice(od *officeDoc) {
	if src := a.officePrintSource(od); src != nil {
		a.openPrint(src)
	}
}

func (a *App) officePrintSource(od *officeDoc) printSource {
	switch e := od.ed.(type) {
	case *wordEditor:
		return &wordPrint{e: e, images: printing.Images{}}
	case *sheetEd:
		if e.wb == nil {
			return nil
		}
		e.finishEdit()
		return newSheetPrint(e)
	case *slidesEditor:
		e.endText()
		return &slidesPrint{e: e}
	case *pdfEditor:
		if e.doc == nil {
			return nil
		}
		return &pdfPrint{e: e, annots: true}
	}
	return nil
}

// printCanvas opens the Print dialog for a canvas.
func (a *App) printCanvas(ad *artDoc) { a.openPrint(newCanvasPrint(a, ad)) }

// printMind opens the Print dialog for a mind map.
func (a *App) printMind(md *mindDoc) {
	a.commitMindText(md)
	a.commitMindCell(md)
	a.openPrint(newMindPrint(a, md))
}
