package docx

import (
	"image"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
)

// Drawing a page. Everything is on white paper at scale pixels a point; the paper itself is the caller's, as is
// anything over it that is not the document, such as the caret.

// DrawPage draws a page's content with its top-left at (x, y).
func (d *Doc) DrawPage(c *gfx.Canvas, pg *Page, x, y, scale float32) {
	for i := range pg.Anchors {
		if pg.Anchors[i].A.Behind {
			d.drawPlaced(c, &pg.Anchors[i], x, y, scale)
		}
	}
	head := d.pageStack(pg, "header", &pg.head)
	foot := d.pageStack(pg, "footer", &pg.foot)
	if head != nil {
		d.drawStackAnchors(c, pg, head.stack, x, y, scale, pg.Sect.ML, pg.Sect.Hdr)
		d.drawStack(c, head.stack, x+pg.Sect.ML*scale, y+pg.Sect.Hdr*scale, scale)
	}
	if foot != nil {
		top := pg.H - pg.Sect.Ftr - foot.stack.H
		d.drawStackAnchors(c, pg, foot.stack, x, y, scale, pg.Sect.ML, top)
		d.drawStack(c, foot.stack, x+pg.Sect.ML*scale, y+top*scale, scale)
	}
	d.drawStack(c, &pg.Stack, x, y, scale)
	for i := range pg.Anchors {
		if !pg.Anchors[i].A.Behind {
			d.drawPlaced(c, &pg.Anchors[i], x, y, scale)
		}
	}
}

// drawStackAnchors draws the floating things of a header or footer: the paragraph that holds them is at dy.
func (d *Doc) drawStackAnchors(c *gfx.Canvas, pg *Page, s *Stack, x, y, scale, dx, dy float32) {
	for i := range s.Pieces {
		p := &s.Pieces[i]
		if p.From != 0 {
			continue
		}
		for _, a := range p.B.para().Anchors {
			pl := Placed{A: a, Rect: a.place(pg, pg.Sect, dx, pg.Sect.TextW(), dy+p.Y)}
			d.drawPlaced(c, &pl, x, y, scale)
		}
	}
}

// drawStack draws decorations and then the words over them.
func (d *Doc) drawStack(c *gfx.Canvas, s *Stack, x, y, scale float32) {
	for i := range s.Decor {
		drawDecor(c, &s.Decor[i], x, y, scale)
	}
	for i := range s.Pieces {
		d.drawPiece(c, &s.Pieces[i], x, y, scale)
	}
}

func drawDecor(c *gfx.Canvas, e *Decor, x, y, scale float32) {
	r := gfx.Rect{X: x + e.Rect.X*scale, Y: y + e.Rect.Y*scale, W: e.Rect.W * scale, H: e.Rect.H * scale}
	if !e.Line {
		c.Fill(r, e.Col)
		return
	}
	// A line stays at least a pixel wide, and on whole pixels, or it would fade at small zoom.
	across := r.H
	along := r.W
	flat := e.Rect.W >= e.Rect.H
	if !flat {
		across, along = r.W, r.H
	}
	across = max(across, 1)
	snap := func(v float32) float32 { return float32(int(v + 0.5)) }
	r.X, r.Y = snap(r.X), snap(r.Y)
	if flat {
		r.H = snap(across)
	} else {
		r.W = snap(across)
	}
	switch e.Kind {
	case "double", "thinThickSmallGap", "thickThinSmallGap", "thinThickMediumGap", "thickThinMediumGap":
		gap := max(across*2, 2)
		c.Fill(r, e.Col)
		if flat {
			r.Y += gap
		} else {
			r.X += gap
		}
		c.Fill(r, e.Col)
	case "dashed", "dashSmallGap", "dotDash", "dotDotDash", "dotted":
		dash, gap := 4*max(scale, 0.5), 3*max(scale, 0.5)
		if e.Kind == "dotted" {
			dash, gap = max(across, 1), 2*max(across, 1)
		}
		for at := float32(0); at < along; at += dash + gap {
			piece := r
			if flat {
				piece.X += at
				piece.W = min(dash, along-at)
			} else {
				piece.Y += at
				piece.H = min(dash, along-at)
			}
			c.Fill(piece, e.Col)
		}
	default:
		c.Fill(r, e.Col)
	}
}

func (d *Doc) drawPiece(c *gfx.Canvas, p *Piece, x, y, scale float32) {
	l := p.L
	px := x + p.X*scale
	py := y + (p.Y-l.Lines[p.From].Y)*scale
	var pics flow.Pictures = func(key string) image.Image { return d.Pics.Get(key) }
	black := gfx.Hex(0)
	l.DrawLines(c, p.From, p.To, px, py, scale, d.Fonts, pics, black)
	para := p.B.P
	if p.From == 0 && para != nil && para.marker != nil && len(l.Lines) > 0 {
		m := para.marker
		line := &l.Lines[0]
		m.DrawLines(c, 0, 1, px+para.markerX*scale, py+(line.Y+line.Base-m.Lines[0].Base)*scale, scale, d.Fonts, nil, black)
	}
	// What has no picture to show says what it is.
	for n := p.From; n < p.To; n++ {
		line := &l.Lines[n]
		for k := range line.Frags {
			f := &line.Frags[k]
			if f.Inline == nil || d.Pics.Get(f.Inline.Key) != nil {
				continue
			}
			r := gfx.Rect{X: px + f.X*scale, Y: py + (line.Y+line.Base-f.Inline.H)*scale, W: f.Inline.W * scale, H: f.Inline.H * scale}
			d.placeholder(c, r, f.Inline.Key, scale)
		}
	}
}

// placeholder is a box in place of what cannot be drawn. Its words are what the file calls it.
func (d *Doc) placeholder(c *gfx.Canvas, r gfx.Rect, key string, scale float32) {
	if d.drawChart(c, r, key, scale) {
		return
	}
	label := "picture"
	if len(key) > 1 && key[0] == '?' {
		label, _, _ = strings.Cut(key[1:], "\x00")
	}
	c.Fill(r, gfx.Hex(0xf0f1f4))
	c.Outline(r, max(1, scale/2), gfx.Hex(0xb6bac4))
	face := d.Fonts.Font("Segoe UI", false, false).Face(max(9*scale, 6))
	text := face.Ellipsize(label, r.W-4*scale)
	if r.H > face.Height() {
		c.Text(face, r.X+(r.W-face.Measure(text))/2, r.Y+(r.H-face.Height())/2, text, gfx.Hex(0x6a6f7c))
	}
}

func (d *Doc) drawPlaced(c *gfx.Canvas, pl *Placed, x, y, scale float32) {
	a := pl.A
	r := gfx.Rect{X: x + pl.Rect.X*scale, Y: y + pl.Rect.Y*scale, W: pl.Rect.W * scale, H: pl.Rect.H * scale}
	if a.Box != nil {
		if a.Fill.A != 0 {
			c.Fill(r, a.Fill)
		}
		if a.Line.A != 0 {
			c.Outline(r, max(1, scale*0.75), a.Line)
		}
		if a.stack == nil {
			a.stack = d.stackOf(a.Box.Root, max(a.W-a.Inset[1]-a.Inset[3], 8), 0)
		}
		c.Push(r)
		d.drawStack(c, a.stack, r.X+a.Inset[1]*scale, r.Y+a.Inset[0]*scale, scale)
		c.Pop()
		return
	}
	if img := d.Pics.Get(a.Key); img != nil {
		c.Image(img, r)
		return
	}
	d.placeholder(c, r, a.Key, scale)
}
