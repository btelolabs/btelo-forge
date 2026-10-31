package xlsx

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strconv"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/btelolabs/btelo-forge/internal/office/chart"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// What floats over a sheet's cells: its charts and pictures, read from its drawing part. They are only shown; the part
// stays as it is, apart from the anchors moved with rows and columns.

// maxDrawings bounds what one sheet is taken to hold.
const maxDrawings = 4096

// emuPixel is the EMUs in a pixel at 96 to the inch.
const emuPixel = 9525

// Marker is a corner tied to a cell: the cell, and how far into it, in pixels.
type Marker struct {
	Row, Col int
	DX, DY   float64
}

// Drawing is one thing over the cells.
type Drawing struct {
	From    Marker
	To      Marker  // when Sized is false
	W, H    float64 // from From, in pixels, when Sized
	Sized   bool
	Chart   string     // the chart's part, or
	Picture string     // the picture's part
	Crop    [4]float64 // of the picture: left, top, right and bottom, as shares
}

// Drawings lists what is over the sheet's cells, in the order it is stacked.
func (s *Sheet) Drawings() []Drawing {
	wb := s.wb
	if s.part == "" {
		return nil
	}
	rels, err := wb.pkg.Rels(s.part)
	if err != nil {
		return nil
	}
	var out []Drawing
	for _, r := range rels {
		if r.Type != relDrawing || r.External {
			continue
		}
		name := opc.Resolve(s.part, r.Target)
		doc, err := wb.pkg.Doc(name)
		if err != nil {
			continue
		}
		dr, err := wb.pkg.Rels(name)
		if err != nil {
			dr = nil
		}
		for _, n := range doc.Root.Elements() {
			if len(out) >= maxDrawings {
				return out
			}
			if d, ok := readDrawing(n, name, dr); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

func readMarker(n *ox.Node) Marker {
	at := func(name string) int {
		v, _ := strconv.Atoi(strings.TrimSpace(n.Child(name).InnerText()))
		return min(max(v, 0), MaxRows-1)
	}
	off := func(name string) float64 { return attrFloat(n.Child(name).InnerText(), 0) / emuPixel }
	return Marker{Row: at("xdr:row"), Col: min(at("xdr:col"), MaxCols-1), DX: off("xdr:colOff"), DY: off("xdr:rowOff")}
}

func readDrawing(n *ox.Node, part string, rels []opc.Rel) (Drawing, bool) {
	var d Drawing
	ext := n.Child("xdr:ext")
	switch n.Name {
	case "xdr:twoCellAnchor":
		d.From, d.To = readMarker(n.Child("xdr:from")), readMarker(n.Child("xdr:to"))
	case "xdr:oneCellAnchor":
		d.From, d.Sized = readMarker(n.Child("xdr:from")), true
	case "xdr:absoluteAnchor":
		d.Sized = true
		pos := n.Child("xdr:pos")
		d.From.DX, d.From.DY = attrFloat(pos.Attr("x"), 0)/emuPixel, attrFloat(pos.Attr("y"), 0)/emuPixel
	default:
		return d, false
	}
	if d.Sized {
		d.W, d.H = attrFloat(ext.Attr("cx"), 0)/emuPixel, attrFloat(ext.Attr("cy"), 0)/emuPixel
	}
	target := func(id string) string {
		if r, ok := opc.RelByID(rels, id); ok && !r.External {
			return opc.Resolve(part, r.Target)
		}
		return ""
	}
	if c := n.All("c:chart"); len(c) > 0 {
		d.Chart = target(c[0].Attr("r:id"))
	} else if p := n.Child("xdr:pic"); p != nil {
		fill := p.Child("xdr:blipFill")
		d.Picture = target(fill.Child("a:blip").Attr("r:embed"))
		if c := fill.Child("a:srcRect"); c != nil {
			for i, name := range []string{"l", "t", "r", "b"} {
				d.Crop[i] = min(max(attrFloat(c.Attr(name), 0)/100000, 0), 1)
			}
		}
	}
	return d, d.Chart != "" || d.Picture != ""
}

// DrawingArea is the cells the sheet's drawings cover, ok false when it has none.
func (s *Sheet) DrawingArea() (a formula.Area, ok bool) {
	for _, d := range s.Drawings() {
		to := d.To
		if d.Sized {
			// A size is reached across cells of the sheet's own sizes.
			to = d.From
			for x := d.W + d.From.DX; to.Col < MaxCols-1 && x > 0; to.Col++ {
				x -= s.ColPixels(to.Col)
			}
			for y := d.H + d.From.DY; to.Row < MaxRows-1 && y > 0; to.Row++ {
				y -= s.RowPoints(to.Row) * 4 / 3
			}
		}
		b := formula.Area{R1: int32(d.From.Row), C1: int32(d.From.Col), R2: int32(to.Row), C2: int32(to.Col)}
		if !ok {
			a, ok = b, true
			continue
		}
		a.R1, a.C1, a.R2, a.C2 = min(a.R1, b.R1), min(a.C1, b.C1), max(a.R2, b.R2), max(a.C2, b.C2)
	}
	return a, ok
}

// Picture is a picture of a drawing, cropped, nil for one in a format that cannot be read, such as EMF.
func (wb *Workbook) Picture(d *Drawing) *image.RGBA {
	p := wb.pkg.Part(d.Picture)
	if p == nil {
		return nil
	}
	src, _, err := image.Decode(bytes.NewReader(p.Data))
	if err != nil {
		return nil
	}
	b := src.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	crop := image.Rect(b.Min.X+int(d.Crop[0]*w), b.Min.Y+int(d.Crop[1]*h), b.Max.X-int(d.Crop[2]*w), b.Max.Y-int(d.Crop[3]*h))
	if crop.Empty() {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(out, out.Rect, src, crop.Min, draw.Src)
	return out
}

type chartEntry struct {
	ch      *chart.Chart
	ver     uint64
	pending bool // formulas were being worked out: their results are still to come
}

// Chart is a chart read against the workbook as it is now, so that it follows its cells; nil for one that cannot be
// drawn. The same *chart.Chart comes back until something changes.
func (wb *Workbook) Chart(part string) *chart.Chart {
	if e := wb.charts[part]; e != nil && e.ver == wb.version && !e.pending {
		return e.ch
	}
	if wb.charts == nil {
		wb.charts = map[string]*chartEntry{}
	}
	e := &chartEntry{ver: wb.version, pending: wb.Pending()}
	if p := wb.pkg.Part(part); p != nil {
		if ch, err := chart.Parse(p.Data, chart.Source{Colors: wb.scheme.Lookup, Range: wb.chartRange, Date1904: wb.date}); err == nil {
			e.ch = ch
		}
	}
	wb.charts[part] = e
	return e.ch
}

// chartRange reads a chart's reference: one area on a sheet, or several in brackets.
func (wb *Workbook) chartRange(ref string, hidden bool) ([]chart.Point, bool) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "(") && strings.HasSuffix(ref, ")") {
		ref = ref[1 : len(ref)-1]
	}
	var out []chart.Point
	for _, one := range splitRefs(ref) {
		i := strings.LastIndexByte(one, '!')
		if i <= 0 {
			return nil, false
		}
		name := one[:i]
		if len(name) > 1 && name[0] == '\'' && name[len(name)-1] == '\'' {
			name = strings.ReplaceAll(name[1:len(name)-1], "''", "'")
		}
		s := wb.Sheet(name)
		a, ok := formula.ParseArea(one[i+1:])
		if s == nil || !ok {
			return nil, false
		}
		// A whole column stops where the sheet does.
		used := s.Used()
		a.R2, a.C2 = min(a.R2, max(used.R2, a.R1)), min(a.C2, max(used.C2, a.C1))
		for r := a.R1; r <= a.R2; r++ {
			if !hidden && s.RowAt(int(r)) != nil && s.RowAt(int(r)).Hidden {
				continue
			}
			for c := a.C1; c <= a.C2; c++ {
				if !hidden && s.ColInfo(int(c)).Hidden {
					continue
				}
				out = append(out, wb.chartPoint(s, int(r), int(c)))
			}
		}
	}
	return out, true
}

func (wb *Workbook) chartPoint(s *Sheet, r, c int) chart.Point {
	v := s.ValueAt(r, c)
	switch v.Kind {
	case formula.Blank:
		return chart.Point{Blank: true}
	case formula.Number:
		return chart.Point{Text: s.Shown(r, c).Text, Num: v.Num, Number: true}
	}
	return chart.Point{Text: s.Shown(r, c).Text}
}

// splitRefs splits references at the commas outside quotes.
func splitRefs(s string) []string {
	var out []string
	quoted, start := false, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			quoted = !quoted
		case ',':
			if !quoted {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}
