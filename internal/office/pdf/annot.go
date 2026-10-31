package pdf

import (
	"bytes"
	"fmt"
	"math"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
)

// Annotations are real PDF annotations with appearance streams, so that other readers show them as drawn here.

// AnnotSpec describes a new annotation.
type AnnotSpec struct {
	Kind     Name // Highlight, Underline, StrikeOut, Ink, Square, Circle, Line, Text, FreeText
	Rect     Rect
	Quads    [][8]float64 // for text markup, in QuadPoints order
	Ink      [][]float64  // for Ink: x y pairs
	Line     [4]float64   // for Line
	Arrow    bool         // Line ends in an arrow
	Color    [3]float64
	Width    float64
	Contents string
	Size     float64 // of a FreeText's letters
}

// Annot is an annotation found on a page.
type Annot struct {
	Ref      Ref
	Kind     Name
	Rect     Rect
	Contents string
	Dict     Dict
	Page     int
}

// Annots lists a page's annotations, top one last.
func (d *Doc) Annots(index int) []Annot {
	pages := d.Pages()
	if index < 0 || index >= len(pages) {
		return nil
	}
	var out []Annot
	for _, a := range d.Array(pages[index].Dict["Annots"]) {
		dict := d.Dict(a)
		if dict == nil {
			continue
		}
		rect, ok := d.RectOf(dict["Rect"])
		if !ok {
			continue
		}
		an := Annot{Kind: d.Name(dict["Subtype"]), Rect: rect, Dict: dict, Page: index}
		if r, ok := a.(Ref); ok {
			an.Ref = r
		}
		if s, ok := d.Resolve(dict["Contents"]).(String); ok {
			an.Contents = TextString(s)
		}
		out = append(out, an)
	}
	return out
}

// quadsOf reads QuadPoints.
func (d *Doc) quadsOf(dict Dict) [][8]float64 {
	v := d.Floats(dict["QuadPoints"])
	var out [][8]float64
	for i := 0; i+8 <= len(v); i += 8 {
		out = append(out, [8]float64(v[i:i+8]))
	}
	return out
}

func colorOf(d *Doc, o Object, def [3]float64) [3]float64 {
	v := d.Floats(o)
	switch len(v) {
	case 1:
		return [3]float64{v[0], v[0], v[0]}
	case 3:
		return [3]float64{v[0], v[1], v[2]}
	case 4:
		return [3]float64{(1 - v[0]) * (1 - v[3]), (1 - v[1]) * (1 - v[3]), (1 - v[2]) * (1 - v[3])}
	}
	return def
}

func colorArray(c [3]float64) Array { return Reals(c[0], c[1], c[2]) }

func lineWidth(d *Doc, dict Dict) float64 {
	if b := d.Dict(dict["BS"]); b != nil {
		return d.Float(b["W"], 1)
	}
	if b := d.Array(dict["Border"]); len(b) >= 3 {
		return d.Float(b[2], 1)
	}
	return 1
}

// AddAnnot makes an annotation with its appearance.
func (d *Doc) AddAnnot(index int, a AnnotSpec) (ref Ref, err error) {
	err = d.Do(func() error {
		pageRef, pageDict, err := d.pageObject(index)
		if err != nil {
			return err
		}
		dict := Dict{"Type": Name("Annot"), "Subtype": a.Kind, "Rect": a.Rect.Array(), "F": int64(4), "P": pageRef, "C": colorArray(a.Color)}
		if a.Contents != "" {
			dict["Contents"] = EncodeText(a.Contents)
		}
		w := a.Width
		if w <= 0 {
			w = 1.5
		}
		switch a.Kind {
		case "Highlight", "Underline", "StrikeOut":
			var q Array
			box := Rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
			for _, s := range a.Quads {
				q = append(q, Reals(s[:]...)...)
				for i := 0; i < 8; i += 2 {
					box = box.Union(Rect{s[i], s[i+1], s[i], s[i+1]})
				}
			}
			dict["QuadPoints"] = q
			dict["Rect"] = box.Inset(-1).Array()
		case "Ink":
			list := Array{}
			box := Rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
			for _, s := range a.Ink {
				list = append(list, Reals(s...))
				for i := 0; i+1 < len(s); i += 2 {
					box = box.Union(Rect{s[i], s[i+1], s[i], s[i+1]})
				}
			}
			dict["InkList"] = list
			dict["Rect"] = box.Inset(-w).Array()
			dict["BS"] = Dict{"W": Real(w)}
		case "Square", "Circle":
			dict["BS"] = Dict{"W": Real(w)}
		case "Line":
			dict["L"] = Reals(a.Line[:]...)
			dict["BS"] = Dict{"W": Real(w)}
			if a.Arrow {
				dict["LE"] = Array{Name("None"), Name("OpenArrow")}
			}
			m := w*4 + 2
			dict["Rect"] = Rect{a.Line[0], a.Line[1], a.Line[2], a.Line[3]}.Normal().Inset(-m).Array()
		case "Text":
			dict["Name"] = Name("Note")
			dict["F"] = int64(4 | 8 | 16) // print, no zoom, no rotate
		case "FreeText":
			size := a.Size
			if size <= 0 {
				size = 12
			}
			dict["DA"] = String(fmt.Sprintf("/Helv %s Tf %s rg", fmtNum(size), rgbOps(a.Color)))
			dict["BS"] = Dict{"W": int64(0)}
		}
		ref = d.Add(dict)
		if err := d.setAppearance(ref, dict); err != nil {
			return err
		}
		annots := Array{}
		if old := d.Array(pageDict["Annots"]); old != nil {
			annots = old.Clone()
		}
		pageDict["Annots"] = append(annots, ref)
		d.SetRef(pageRef, pageDict)
		return nil
	})
	return ref, err
}

// setAppearance stores a fresh appearance for an annotation.
func (d *Doc) setAppearance(ref Ref, dict Dict) error {
	s, err := d.buildAppearance(dict, true)
	if err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	out := dict.Clone()
	out["AP"] = Dict{"N": d.Add(s)}
	d.SetRef(ref, out)
	return nil
}

// UpdateAnnot changes an annotation; drawn kinds get a new appearance from the new values.
func (d *Doc) UpdateAnnot(ref Ref, fn func(Dict)) error {
	return d.Do(func() error {
		dict := d.Dict(ref)
		if dict == nil {
			return fmt.Errorf("pdf: no annotation %d", ref.Num)
		}
		c := dict.Clone()
		fn(c)
		d.SetRef(ref, c)
		if d.drawable(c) {
			return d.setAppearance(ref, c)
		}
		return nil
	})
}

func (d *Doc) drawable(dict Dict) bool {
	switch d.Name(dict["Subtype"]) {
	case "Highlight", "Underline", "StrikeOut", "Ink", "Square", "Circle", "Line", "Text", "FreeText":
		return true
	}
	return false
}

// MoveAnnot shifts an annotation; its appearance moves with its Rect, and its coordinates with it.
func (d *Doc) MoveAnnot(ref Ref, dx, dy float64) error {
	return d.Do(func() error {
		dict := d.Dict(ref)
		if dict == nil {
			return fmt.Errorf("pdf: no annotation %d", ref.Num)
		}
		c := dict.Clone()
		shift := func(k Name) {
			if v := d.Floats(c[k]); len(v) >= 2 {
				a := make(Array, len(v))
				for i, x := range v {
					if i%2 == 0 {
						a[i] = Real(x + dx)
					} else {
						a[i] = Real(x + dy)
					}
				}
				c[k] = a
			}
		}
		for _, k := range []Name{"Rect", "QuadPoints", "L", "Vertices", "CL"} {
			shift(k)
		}
		if list := d.Array(c["InkList"]); list != nil {
			out := Array{}
			for _, s := range list {
				v := d.Floats(s)
				a := make(Array, len(v))
				for i, x := range v {
					if i%2 == 0 {
						a[i] = Real(x + dx)
					} else {
						a[i] = Real(x + dy)
					}
				}
				out = append(out, a)
			}
			c["InkList"] = out
		}
		d.SetRef(ref, c)
		return nil
	})
}

// ResizeAnnot gives an annotation a new Rect, scaling what it holds inside; another program's appearance stretches
// with the Rect.
func (d *Doc) ResizeAnnot(ref Ref, to Rect) error {
	return d.Do(func() error {
		dict := d.Dict(ref)
		if dict == nil {
			return fmt.Errorf("pdf: no annotation %d", ref.Num)
		}
		from, ok := d.RectOf(dict["Rect"])
		if !ok || from.W() == 0 || from.H() == 0 {
			return nil
		}
		sx, sy := to.W()/from.W(), to.H()/from.H()
		c := dict.Clone()
		mapped := func(v []float64) Array {
			a := make(Array, len(v))
			for i, x := range v {
				if i%2 == 0 {
					a[i] = Real(to.X0 + (x-from.X0)*sx)
				} else {
					a[i] = Real(to.Y0 + (x-from.Y0)*sy)
				}
			}
			return a
		}
		for _, k := range []Name{"QuadPoints", "L", "Vertices"} {
			if v := d.Floats(c[k]); len(v) >= 2 {
				c[k] = mapped(v)
			}
		}
		if list := d.Array(c["InkList"]); list != nil {
			out := Array{}
			for _, s := range list {
				out = append(out, mapped(d.Floats(s)))
			}
			c["InkList"] = out
		}
		c["Rect"] = to.Array()
		d.SetRef(ref, c)
		if d.drawable(c) && d.Dict(c["AP"]) != nil && d.ownAppearance(c) {
			return d.setAppearance(ref, c)
		}
		return nil
	})
}

// ownAppearance is true for a kind whose picture is fully described by its values, so redrawing loses nothing.
func (d *Doc) ownAppearance(dict Dict) bool {
	s, _ := d.Resolve(d.Dict(dict["AP"])["N"]).(*Stream)
	return s != nil && d.Bool(s.Dict["ForgeDrawn"], false)
}

// DeleteAnnot takes an annotation off its page.
func (d *Doc) DeleteAnnot(index int, ref Ref) error {
	return d.Do(func() error {
		pageRef, dict, err := d.pageObject(index)
		if err != nil {
			return err
		}
		var kept Array
		var victim Dict
		for _, a := range d.Array(dict["Annots"]) {
			if r, ok := a.(Ref); ok && r.Num == ref.Num {
				victim = d.Dict(a)
				continue
			}
			kept = append(kept, a)
		}
		// A popup goes with what it belongs to.
		if victim != nil {
			if p, ok := victim["Popup"].(Ref); ok {
				var again Array
				for _, a := range kept {
					if r, ok := a.(Ref); !ok || r.Num != p.Num {
						again = append(again, a)
					}
				}
				kept = again
			}
		}
		if len(kept) == 0 {
			delete(dict, "Annots")
		} else {
			dict["Annots"] = kept
		}
		d.SetRef(pageRef, dict)
		return nil
	})
}

// AppearanceOf is the stream that draws an annotation in its normal state, generated for the kinds that come
// without one; nil when it shows nothing.
func AppearanceOf(d *Doc, dict Dict) *Stream {
	ap := d.Dict(dict["AP"])
	switch n := d.Resolve(ap["N"]).(type) {
	case *Stream:
		return n
	case Dict:
		state := d.Name(dict["AS"])
		if state == "" {
			state = "Off"
		}
		s, _ := d.Resolve(n[state]).(*Stream)
		return s
	}
	s, err := d.buildAppearance(dict, false)
	if err != nil {
		return nil
	}
	return s
}

func form(bbox Rect, res Dict, ops []byte, group bool) *Stream {
	dict := Dict{"Type": Name("XObject"), "Subtype": Name("Form"), "BBox": bbox.Array(), "Filter": Name("FlateDecode"), "ForgeDrawn": true}
	if res != nil {
		dict["Resources"] = res
	}
	if group {
		dict["Group"] = Dict{"S": Name("Transparency")}
	}
	return NewStream(dict, Deflate(ops))
}

// buildAppearance draws the kinds it knows. An embedded font is added to the document only when store is true.
func (d *Doc) buildAppearance(dict Dict, store bool) (*Stream, error) {
	rect, ok := d.RectOf(dict["Rect"])
	if !ok {
		return nil, nil
	}
	kind := d.Name(dict["Subtype"])
	col := colorOf(d, dict["C"], [3]float64{1, 0.85, 0})
	w := lineWidth(d, dict)
	var b bytes.Buffer
	switch kind {
	case "Highlight":
		fmt.Fprintf(&b, "/G gs %s rg\n", rgbOps(col))
		for _, q := range d.quadsOf(dict) {
			fmt.Fprintf(&b, "%s %s m %s %s l %s %s l %s %s l h f\n", fmtNum(q[0]), fmtNum(q[1]), fmtNum(q[2]), fmtNum(q[3]), fmtNum(q[6]), fmtNum(q[7]), fmtNum(q[4]), fmtNum(q[5]))
		}
		res := Dict{"ExtGState": Dict{"G": Dict{"BM": Name("Multiply")}}}
		return form(rect, res, b.Bytes(), true), nil
	case "Underline", "StrikeOut":
		fmt.Fprintf(&b, "%s RG\n", rgbOps(col))
		for _, q := range d.quadsOf(dict) {
			t := 0.08
			if kind == "StrikeOut" {
				t = 0.5
			}
			x0, y0 := q[4]+(q[0]-q[4])*t, q[5]+(q[1]-q[5])*t
			x1, y1 := q[6]+(q[2]-q[6])*t, q[7]+(q[3]-q[7])*t
			fmt.Fprintf(&b, "%s w %s %s m %s %s l S\n", fmtNum(max(math.Hypot(q[0]-q[4], q[1]-q[5])/16, 0.5)), fmtNum(x0), fmtNum(y0), fmtNum(x1), fmtNum(y1))
		}
		return form(rect, nil, b.Bytes(), false), nil
	case "Ink":
		fmt.Fprintf(&b, "%s RG %s w 1 J 1 j\n", rgbOps(col), fmtNum(w))
		for _, s := range d.Array(dict["InkList"]) {
			v := d.Floats(s)
			for i := 0; i+1 < len(v); i += 2 {
				op := "l"
				if i == 0 {
					op = "m"
				}
				fmt.Fprintf(&b, "%s %s %s\n", fmtNum(v[i]), fmtNum(v[i+1]), op)
			}
			b.WriteString("S\n")
		}
		return form(rect, nil, b.Bytes(), false), nil
	case "Square", "Circle":
		fill := d.Floats(dict["IC"])
		fmt.Fprintf(&b, "%s RG %s w\n", rgbOps(col), fmtNum(w))
		op := "S"
		if len(fill) == 3 {
			fmt.Fprintf(&b, "%s rg\n", rgbOps([3]float64{fill[0], fill[1], fill[2]}))
			op = "B"
		}
		in := rect.Inset(w / 2)
		if kind == "Square" {
			fmt.Fprintf(&b, "%s %s %s %s re %s\n", fmtNum(in.X0), fmtNum(in.Y0), fmtNum(in.W()), fmtNum(in.H()), op)
		} else {
			ellipseOps(&b, in)
			b.WriteString(op + "\n")
		}
		return form(rect, nil, b.Bytes(), false), nil
	case "Line":
		l := d.Floats(dict["L"])
		if len(l) < 4 {
			return nil, nil
		}
		fmt.Fprintf(&b, "%s RG %s w 1 J 1 j %s %s m %s %s l S\n", rgbOps(col), fmtNum(w), fmtNum(l[0]), fmtNum(l[1]), fmtNum(l[2]), fmtNum(l[3]))
		if le := d.Array(dict["LE"]); len(le) >= 2 {
			if e := d.Name(le[1]); e != "" && e != "None" {
				arrowOps(&b, l[0], l[1], l[2], l[3], w)
			}
			if e := d.Name(le[0]); e != "" && e != "None" {
				arrowOps(&b, l[2], l[3], l[0], l[1], w)
			}
		}
		return form(rect, nil, b.Bytes(), false), nil
	case "Text":
		x, y, wd, h := rect.X0, rect.Y0, rect.W(), rect.H()
		fmt.Fprintf(&b, "%s rg 0.2 G 0.75 w %s %s %s %s re B\n0.2 g\n", rgbOps(col), fmtNum(x+0.5), fmtNum(y+0.5), fmtNum(wd-1), fmtNum(h-1))
		for i := 1; i <= 3; i++ {
			ly := y + h*float64(i)/4
			fmt.Fprintf(&b, "%s %s %s 0.8 re f\n", fmtNum(x+wd*0.2), fmtNum(ly), fmtNum(wd*0.6))
		}
		return form(rect, nil, b.Bytes(), false), nil
	case "FreeText":
		return d.freeTextForm(dict, rect, store)
	case "Widget":
		return d.widgetForm(dict, rect, store)
	}
	return nil, nil
}

func ellipseOps(b *bytes.Buffer, r Rect) {
	const k = 0.5522847
	cx, cy := (r.X0+r.X1)/2, (r.Y0+r.Y1)/2
	rx, ry := r.W()/2, r.H()/2
	n := fmtNum
	fmt.Fprintf(b, "%s %s m\n", n(cx+rx), n(cy))
	fmt.Fprintf(b, "%s %s %s %s %s %s c\n", n(cx+rx), n(cy+k*ry), n(cx+k*rx), n(cy+ry), n(cx), n(cy+ry))
	fmt.Fprintf(b, "%s %s %s %s %s %s c\n", n(cx-k*rx), n(cy+ry), n(cx-rx), n(cy+k*ry), n(cx-rx), n(cy))
	fmt.Fprintf(b, "%s %s %s %s %s %s c\n", n(cx-rx), n(cy-k*ry), n(cx-k*rx), n(cy-ry), n(cx), n(cy-ry))
	fmt.Fprintf(b, "%s %s %s %s %s %s c h\n", n(cx+k*rx), n(cy-ry), n(cx+rx), n(cy-k*ry), n(cx+rx), n(cy))
}

func arrowOps(b *bytes.Buffer, x0, y0, x1, y1, w float64) {
	a := math.Atan2(y1-y0, x1-x0)
	l := 4*w + 4
	for _, s := range []float64{-1, 1} {
		t := a + math.Pi - s*0.45
		fmt.Fprintf(b, "%s %s m %s %s l S\n", fmtNum(x1+l*math.Cos(t)), fmtNum(y1+l*math.Sin(t)), fmtNum(x1), fmtNum(y1))
	}
}

// parseDA reads a default appearance string.
func parseDA(da string) (fontName Name, size float64, ops string) {
	l := NewLexer([]byte(da))
	var args []Object
	var keep []string
	for {
		o, ok := l.Next()
		if !ok {
			break
		}
		kw, isKw := o.(Keyword)
		if !isKw {
			args = append(args, o)
			continue
		}
		if kw == "Tf" && len(args) >= 2 {
			fontName, _ = args[0].(Name)
			size, _ = Num(args[1])
		} else if kw == "g" || kw == "rg" || kw == "k" {
			var parts []string
			for _, a := range args {
				if v, ok := Num(a); ok {
					parts = append(parts, fmtNum(v))
				}
			}
			keep = append(keep, strings.Join(parts, " ")+" "+string(kw))
		}
		args = nil
	}
	return fontName, size, strings.Join(keep, " ")
}

// textRun writes lines of text with whichever font fits: a Latin standard font while the text allows, else an embedded
// subset of a system font.
type textRun struct {
	res   Dict
	name  Name
	enc   func(string) string
	width func(string) float64 // in ems
	asc   float64
	desc  float64
}

func (d *Doc) textRunFor(family Name, fontDict Object, text string, store bool) (*textRun, error) {
	if latinOnly(text) && (fontDict != nil || family != "") {
		canon, _ := font.Standard14(string(d.Name(d.Dict(fontDict)["BaseFont"])))
		if canon == "" {
			canon = "Helvetica"
		}
		dict := d.Dict(fontDict)
		var fo Object = fontDict
		if dict == nil {
			fo = Dict{"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"), "Encoding": Name("WinAnsiEncoding")}
			dict = d.Dict(fo)
		}
		if family == "" {
			family = "F1"
		}
		widths := d.Floats(dict["Widths"])
		first := d.Int(dict["FirstChar"], 0)
		return &textRun{res: Dict{"Font": Dict{family: fo}}, name: family, asc: 0.75, desc: -0.21,
			enc: func(s string) string {
				var b strings.Builder
				b.WriteByte('<')
				for _, c := range winAnsiBytes(s) {
					fmt.Fprintf(&b, "%02X", c)
				}
				b.WriteByte('>')
				return b.String()
			},
			width: func(s string) float64 {
				total := 0.0
				for _, c := range winAnsiBytes(s) {
					if i := int(c) - first; i >= 0 && i < len(widths) {
						total += widths[i] / 1000
					} else if v, ok := font.Standard14Width(canon, font.WinAnsiEncoding[c]); ok {
						total += v / 1000
					} else {
						total += 0.5
					}
				}
				return total
			}}, nil
	}
	if !store {
		return nil, nil // a preview of text that needs embedding waits for it to be saved
	}
	f, err := d.face("", false, false, text)
	if err != nil {
		return nil, err
	}
	em, err := d.Embed(f, text)
	if err != nil {
		return nil, err
	}
	return &textRun{res: Dict{"Font": Dict{"FF1": em.Ref}}, name: "FF1", enc: em.Encode, width: f.Measure, asc: f.Ascent(), desc: f.Descent()}, nil
}

func latinOnly(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 0x20 || r >= 0x7F && !winAnsiHas(r) {
			return false
		}
	}
	return true
}

func winAnsiHas(r rune) bool {
	name, ok := font.RuneToName(r)
	if !ok {
		return false
	}
	for _, n := range font.WinAnsiEncoding {
		if n == name {
			return true
		}
	}
	return false
}

func winAnsiBytes(s string) []byte {
	var out []byte
	for _, r := range s {
		if r < 0x80 {
			out = append(out, byte(r))
			continue
		}
		name, ok := font.RuneToName(r)
		if !ok {
			out = append(out, '?')
			continue
		}
		c := byte('?')
		for i, n := range font.WinAnsiEncoding {
			if n == name {
				c = byte(i)
				break
			}
		}
		out = append(out, c)
	}
	return out
}

func (d *Doc) freeTextForm(dict Dict, rect Rect, store bool) (*Stream, error) {
	da, _ := d.Resolve(dict["DA"]).(String)
	name, size, ops := parseDA(string(da))
	if size <= 0 {
		size = 12
	}
	if name == "" {
		name = "Helv"
	}
	if ops == "" {
		ops = "0 g"
	}
	text := ""
	if s, ok := d.Resolve(dict["Contents"]).(String); ok {
		text = TextString(s)
	}
	run, err := d.textRunFor(name, nil, text, store)
	if err != nil || run == nil {
		return nil, err
	}
	var b bytes.Buffer
	pad := 2.0
	lines := wrapText(text, (rect.W()-2*pad)/size, run.width)
	fmt.Fprintf(&b, "q %s %s %s %s re W n BT /%s %s Tf %s %s TL %s %s Td\n", fmtNum(rect.X0+1), fmtNum(rect.Y0+1), fmtNum(rect.W()-2), fmtNum(rect.H()-2),
		run.name, fmtNum(size), ops, fmtNum(size*1.2), fmtNum(rect.X0+pad), fmtNum(rect.Y1-pad-size))
	for _, l := range lines {
		fmt.Fprintf(&b, "%s Tj T*\n", run.enc(l))
	}
	b.WriteString("ET Q\n")
	return form(rect, run.res, b.Bytes(), false), nil
}
