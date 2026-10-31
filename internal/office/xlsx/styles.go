package xlsx

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// The styles part stays a tree. Existing entries are never rewritten or renumbered: a new format finds an equal entry
// or appends one, so every index in every cell keeps meaning what it meant. Appended entries are not taken back by
// undo; they are harmless and unused.

var (
	styleSheetOrder = []string{"numFmts", "fonts", "fills", "borders", "cellStyleXfs", "cellXfs", "cellStyles", "dxfs", "tableStyles", "colors", "extLst"}
	fontOrder       = []string{"b", "i", "strike", "condense", "extend", "outline", "shadow", "u", "vertAlign", "sz", "color", "name", "rFont", "family", "charset", "scheme"}
	xfOrder         = []string{"alignment", "protection", "extLst"}
	borderOrder     = []string{"start", "left", "end", "right", "top", "bottom", "diagonal", "vertical", "horizontal"}
)

// colorRef is a colour as a file writes it.
type colorRef struct {
	rgb     string // AARRGGBB or RRGGBB
	theme   int    // -1 for none
	indexed int    // -1 for none
	tint    float64
	auto    bool
}

func colorOf(e *ox.Node) colorRef {
	c := colorRef{theme: -1, indexed: -1}
	if e == nil {
		return c
	}
	c.rgb = e.Attr("rgb")
	if v, err := strconv.Atoi(e.Attr("theme")); err == nil {
		c.theme = v
	}
	if v, err := strconv.Atoi(e.Attr("indexed")); err == nil {
		c.indexed = v
	}
	if v, err := strconv.ParseFloat(e.Attr("tint"), 64); err == nil {
		c.tint = v
	}
	c.auto = boolAttr(e, "auto")
	return c
}

// Edge is one side of a cell's border.
type Edge struct {
	Style string // thin, medium, thick, dashed, dotted, double, hair and the rest; "" for none
	Color string // RRGGBB, "" for automatic
}

// Style is a cell's format, resolved for drawing.
type Style struct {
	Font      string
	Size      float64
	Bold      bool
	Italic    bool
	Underline bool
	Double    bool // a double underline
	Strike    bool
	Color     string // RRGGBB, "" for automatic
	Fill      string // RRGGBB, "" for none
	Left      Edge
	Right     Edge
	Top       Edge
	Bottom    Edge
	HAlign    string // left, center, right, fill, justify, centerContinuous, distributed; "" for general
	VAlign    string // top, center, bottom, justify, distributed; "" for bottom
	Wrap      bool
	Shrink    bool
	Indent    int
	Rotation  int
	NumFmt    string
	NumFmtID  int
	Format    *formula.Format
}

// Styles is the styles part.
type Styles struct {
	pkg     *opc.Package
	part    string
	doc     *ox.Doc
	changed bool
	theme   [12]string
	indexed []string
	fonts   []*ox.Node
	fills   []*ox.Node
	borders []*ox.Node
	xfs     []*ox.Node
	dxfs    []*ox.Node
	numFmts map[int]string
	cache   []*Style
	keys    [4]map[string]int
	formats map[string]*formula.Format
}

// loadStyles reads the part; a workbook with none gets the smallest valid one, with a relationship from the workbook.
func loadStyles(pkg *opc.Package, part, book string, theme []byte) (*Styles, error) {
	st := &Styles{pkg: pkg, part: part, formats: map[string]*formula.Format{}}
	st.loadTheme(theme)
	if part != "" && pkg.Part(part) != nil {
		doc, err := pkg.Doc(part)
		if err != nil {
			return nil, err
		}
		st.doc = doc
	} else {
		doc, err := ox.Parse([]byte(blankStyles))
		if err != nil {
			return nil, err
		}
		st.doc = doc
		st.part = "xl/styles.xml"
		if pkg.Part(st.part) != nil {
			st.part = pkg.UniqueName("xl/styles%d.xml")
		}
		pkg.Put(st.part, ctStyles, doc.Bytes())
		pkg.AddRel(book, relStyles, opc.RelativeTarget(book, st.part), false)
	}
	if st.doc.Root.Name != "styleSheet" {
		return nil, fmt.Errorf("%s is not a style sheet", st.part)
	}
	st.index()
	return st, nil
}

// index lists the entries of the tree.
func (st *Styles) index() {
	r := st.doc.Root
	st.fonts = r.Child("fonts").Elements("font")
	st.fills = r.Child("fills").Elements("fill")
	st.borders = r.Child("borders").Elements("border")
	st.xfs = r.Child("cellXfs").Elements("xf")
	st.dxfs = r.Child("dxfs").Elements("dxf")
	st.numFmts = map[int]string{}
	for _, e := range r.Child("numFmts").Elements("numFmt") {
		if id, err := strconv.Atoi(e.Attr("numFmtId")); err == nil {
			st.numFmts[id] = e.Attr("formatCode")
		}
	}
	st.indexed = nil
	for _, e := range r.Path("colors", "indexedColors").Elements("rgbColor") {
		st.indexed = append(st.indexed, e.Attr("rgb"))
	}
	if len(st.xfs) == 0 {
		// Cells' index 0 has to mean something.
		x := ox.New("xf", "numFmtId", "0", "fontId", "0", "fillId", "0", "borderId", "0")
		n := r.Ensure("cellXfs", styleSheetOrder)
		n.Append(x)
		n.SetAttr("count", "1")
		st.xfs = []*ox.Node{x}
		st.changed = true
	}
	st.cache = make([]*Style, len(st.xfs))
}

func (st *Styles) loadTheme(data []byte) {
	st.theme = [12]string{"FFFFFF", "000000", "E7E6E6", "44546A", "4472C4", "ED7D31", "A5A5A5", "FFC000", "5B9BD5", "70AD47", "0563C1", "954F72"}
	if data == nil {
		return
	}
	doc, err := ox.Parse(data)
	if err != nil {
		return
	}
	scheme := doc.Root.Path("a:themeElements", "a:clrScheme")
	// Theme index 0 is lt1 and 1 is dk1, the reverse of the file's order.
	for i, name := range []string{"a:lt1", "a:dk1", "a:lt2", "a:dk2", "a:accent1", "a:accent2", "a:accent3", "a:accent4", "a:accent5", "a:accent6", "a:hlink", "a:folHlink"} {
		e := scheme.Child(name)
		if s := e.Child("a:srgbClr"); s != nil {
			st.theme[i] = strings.ToUpper(s.Attr("val"))
		} else if s := e.Child("a:sysClr"); s != nil && s.Attr("lastClr") != "" {
			st.theme[i] = strings.ToUpper(s.Attr("lastClr"))
		}
	}
}

// ThemeColor is an entry of the theme's palette with a tint, for the colour pickers.
func (st *Styles) ThemeColor(i int, tint float64) string {
	return st.resolve(colorRef{theme: i, indexed: -1, tint: tint})
}

// indexedPalette is the legacy palette.
var indexedPalette = [...]string{
	"000000", "FFFFFF", "FF0000", "00FF00", "0000FF", "FFFF00", "FF00FF", "00FFFF", "000000", "FFFFFF", "FF0000", "00FF00", "0000FF", "FFFF00", "FF00FF", "00FFFF",
	"800000", "008000", "000080", "808000", "800080", "008080", "C0C0C0", "808080", "9999FF", "993366", "FFFFCC", "CCFFFF", "660066", "FF8080", "0066CC", "CCCCFF",
	"000080", "FF00FF", "FFFF00", "00FFFF", "800080", "800000", "008080", "0000FF", "00CCFF", "CCFFFF", "CCFFCC", "FFFF99", "99CCFF", "FF99CC", "CC99FF", "FFCC99",
	"3366FF", "33CCCC", "99CC00", "FFCC00", "FF9900", "FF6600", "666699", "969696", "003366", "339966", "003300", "333300", "993300", "993366", "333399", "333333",
}

// resolve is the colour as RRGGBB, "" for automatic.
func (st *Styles) resolve(c colorRef) string {
	hex := ""
	switch {
	case c.rgb != "":
		hex = strings.ToUpper(c.rgb)
		if len(hex) == 8 {
			hex = hex[2:]
		}
		if len(hex) != 6 {
			return ""
		}
	case c.theme >= 0 && c.theme < len(st.theme):
		hex = st.theme[c.theme]
	case c.indexed >= 0:
		switch {
		case c.indexed < len(st.indexed):
			hex = strings.ToUpper(st.indexed[c.indexed])
			if len(hex) == 8 {
				hex = hex[2:]
			}
		case c.indexed < len(indexedPalette):
			hex = indexedPalette[c.indexed]
		default:
			return "" // 64 and 65 are the system's foreground and background
		}
	default:
		return ""
	}
	if c.tint != 0 {
		return tinted(hex, c.tint)
	}
	return hex
}

// tinted applies a tint the way Office does, on the lightness in HLS.
func tinted(hex string, tint float64) string {
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return hex
	}
	r, g, b := float64(v>>16&255)/255, float64(v>>8&255)/255, float64(v&255)/255
	hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l := (hi + lo) / 2
	var h, s float64
	if hi != lo {
		d := hi - lo
		if l > 0.5 {
			s = d / (2 - hi - lo)
		} else {
			s = d / (hi + lo)
		}
		switch hi {
		case r:
			h = (g - b) / d
			if g < b {
				h += 6
			}
		case g:
			h = (b-r)/d + 2
		default:
			h = (r-g)/d + 4
		}
		h /= 6
	}
	if tint < 0 {
		l *= 1 + tint
	} else {
		l = l*(1-tint) + tint
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	ch := func(t float64) int {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			t = p + (q-p)*6*t
		case t < 0.5:
			t = q
		case t < 2.0/3:
			t = p + (q-p)*(2.0/3-t)*6
		default:
			t = p
		}
		return int(math.Round(t * 255))
	}
	return fmt.Sprintf("%02X%02X%02X", ch(h+1.0/3), ch(h), ch(h-1.0/3))
}

// Get is the style of an index; a wrong index is the default style.
func (st *Styles) Get(i int32) *Style {
	if i < 0 || int(i) >= len(st.xfs) {
		i = 0
	}
	if s := st.cache[i]; s != nil {
		return s
	}
	x := st.xfs[i]
	s := &Style{Font: "Calibri", Size: 11}
	if id, err := strconv.Atoi(x.Attr("fontId")); err == nil && id >= 0 && id < len(st.fonts) {
		f := st.fonts[id]
		s.Bold, s.Italic, s.Strike = present(f, "b"), present(f, "i"), present(f, "strike")
		if u := f.Child("u"); u != nil {
			v := u.Attr("val")
			s.Underline = v != "none"
			s.Double = v == "double" || v == "doubleAccounting"
		}
		if v, err := strconv.ParseFloat(f.Child("sz").Attr("val"), 64); err == nil && v > 0 && v <= 409 {
			s.Size = v
		}
		if n := f.Child("name").Attr("val"); n != "" {
			s.Font = n
		}
		s.Color = st.resolve(colorOf(f.Child("color")))
	}
	if id, err := strconv.Atoi(x.Attr("fillId")); err == nil && id >= 0 && id < len(st.fills) {
		s.Fill = st.fillColor(st.fills[id])
	}
	if id, err := strconv.Atoi(x.Attr("borderId")); err == nil && id >= 0 && id < len(st.borders) {
		b := st.borders[id]
		s.Left, s.Right = st.edge(b, "left", "start"), st.edge(b, "right", "end")
		s.Top, s.Bottom = st.edge(b, "top", ""), st.edge(b, "bottom", "")
	}
	if a := x.Child("alignment"); a != nil {
		s.HAlign, s.VAlign = a.Attr("horizontal"), a.Attr("vertical")
		if s.HAlign == "general" {
			s.HAlign = ""
		}
		s.Wrap, s.Shrink = boolAttr(a, "wrapText"), boolAttr(a, "shrinkToFit")
		s.Indent, _ = strconv.Atoi(a.Attr("indent"))
		s.Indent = min(max(s.Indent, 0), 250)
		s.Rotation, _ = strconv.Atoi(a.Attr("textRotation"))
	}
	s.NumFmtID, _ = strconv.Atoi(x.Attr("numFmtId"))
	code, ok := st.numFmts[s.NumFmtID]
	if !ok {
		code = formula.BuiltinFormat(s.NumFmtID)
	}
	if code == "" {
		code = "General"
	}
	s.NumFmt = code
	s.Format = st.format(code)
	st.cache[i] = s
	return s
}

// format is the parsed code; a file repeats few.
func (st *Styles) format(code string) *formula.Format {
	f := st.formats[code]
	if f == nil {
		f = formula.ParseFormat(code)
		st.formats[code] = f
	}
	return f
}

// fillColor is what a fill looks like as one colour. Patterns are shown as a mix of their two colours.
func (st *Styles) fillColor(f *ox.Node) string {
	p := f.Child("patternFill")
	if p == nil {
		if g := f.Child("gradientFill"); g != nil {
			if stops := g.Elements("stop"); len(stops) > 0 {
				return st.resolve(colorOf(stops[0].Child("color")))
			}
		}
		return ""
	}
	switch p.Attr("patternType") {
	case "", "none":
		return ""
	case "solid":
		if c := st.resolve(colorOf(p.Child("fgColor"))); c != "" {
			return c
		}
		return st.resolve(colorOf(p.Child("bgColor")))
	}
	fg, bg := st.resolve(colorOf(p.Child("fgColor"))), st.resolve(colorOf(p.Child("bgColor")))
	if fg == "" {
		fg = "000000"
	}
	if bg == "" {
		bg = "FFFFFF"
	}
	a, _ := strconv.ParseUint(fg, 16, 32)
	b, _ := strconv.ParseUint(bg, 16, 32)
	mix := func(sh uint) uint64 { return (a>>sh&255 + 3*(b>>sh&255)) / 4 }
	return fmt.Sprintf("%02X%02X%02X", mix(16), mix(8), mix(0))
}

func (st *Styles) edge(b *ox.Node, name, alt string) Edge {
	e := b.Child(name)
	if e == nil && alt != "" {
		e = b.Child(alt)
	}
	if e == nil {
		return Edge{}
	}
	return Edge{Style: e.Attr("style"), Color: st.resolve(colorOf(e.Child("color")))}
}

// Change is a change of format: what is nil stays.
type Change struct {
	Font      *string
	Size      *float64
	Bold      *bool
	Italic    *bool
	Underline *bool
	Strike    *bool
	Color     *string // RRGGBB, "" automatic
	Fill      *string // RRGGBB, "" none
	Left      *Edge
	Right     *Edge
	Top       *Edge
	Bottom    *Edge
	HAlign    *string
	VAlign    *string
	Wrap      *bool
	Indent    *int
	NumFmt    *string
}

func key(n *ox.Node) string {
	d := ox.Doc{Decl: "v", Root: n}
	return string(d.Bytes())
}

// entry finds a node equal to n in list, appending it to the parent with the name otherwise. which picks the index.
func (st *Styles) entry(which int, list *[]*ox.Node, parent string, n *ox.Node) int {
	if st.keys[which] == nil {
		st.keys[which] = make(map[string]int, len(*list))
		for i, e := range *list {
			if _, ok := st.keys[which][key(e)]; !ok {
				st.keys[which][key(e)] = i
			}
		}
	}
	k := key(n)
	if i, ok := st.keys[which][k]; ok {
		return i
	}
	p := st.doc.Root.Ensure(parent, styleSheetOrder)
	p.Append(n)
	i := len(*list)
	*list = append(*list, n)
	st.keys[which][k] = i
	p.SetAttr("count", strconv.Itoa(len(*list)))
	st.changed = true
	return i
}

func rgbAttr(hex string) string { return "FF" + strings.ToUpper(hex) }

// Apply is the index of base's format with the change made: found, or appended.
func (st *Styles) Apply(base int32, ch *Change) int32 {
	if base < 0 || int(base) >= len(st.xfs) {
		base = 0
	}
	x := st.xfs[base].Clone()
	id := func(name string) int {
		n, _ := strconv.Atoi(x.Attr(name))
		return n
	}
	if ch.Font != nil || ch.Size != nil || ch.Bold != nil || ch.Italic != nil || ch.Underline != nil || ch.Strike != nil || ch.Color != nil {
		var f *ox.Node
		if i := id("fontId"); i >= 0 && i < len(st.fonts) {
			f = st.fonts[i].Clone()
		} else {
			f = ox.New("font")
		}
		flag := func(name string, on *bool) {
			if on == nil {
				return
			}
			if e := f.Child(name); e != nil {
				e.Remove()
			}
			if *on {
				f.InsertOrdered(ox.New(name), fontOrder)
			}
		}
		flag("b", ch.Bold)
		flag("i", ch.Italic)
		flag("u", ch.Underline)
		flag("strike", ch.Strike)
		if ch.Size != nil {
			f.Ensure("sz", fontOrder).SetAttr("val", strconv.FormatFloat(*ch.Size, 'g', -1, 64))
		}
		if ch.Font != nil {
			f.Ensure("name", fontOrder).SetAttr("val", *ch.Font)
			if e := f.Child("scheme"); e != nil {
				e.Remove() // the theme's font would win over the name
			}
		}
		if ch.Color != nil {
			if e := f.Child("color"); e != nil {
				e.Remove()
			}
			if *ch.Color != "" {
				f.InsertOrdered(ox.New("color", "rgb", rgbAttr(*ch.Color)), fontOrder)
			}
		}
		x.SetAttr("fontId", strconv.Itoa(st.entry(0, &st.fonts, "fonts", f)))
		x.SetAttr("applyFont", "1")
	}
	if ch.Fill != nil {
		if *ch.Fill == "" {
			x.SetAttr("fillId", "0")
		} else {
			f := ox.New("fill")
			p := ox.New("patternFill", "patternType", "solid")
			p.Append(ox.New("fgColor", "rgb", rgbAttr(*ch.Fill)), ox.New("bgColor", "indexed", "64"))
			f.Append(p)
			x.SetAttr("fillId", strconv.Itoa(st.entry(1, &st.fills, "fills", f)))
		}
		x.SetAttr("applyFill", "1")
	}
	if ch.Left != nil || ch.Right != nil || ch.Top != nil || ch.Bottom != nil {
		var b *ox.Node
		if i := id("borderId"); i >= 0 && i < len(st.borders) {
			b = st.borders[i].Clone()
		} else {
			b = ox.New("border")
		}
		side := func(name, alt string, e *Edge) {
			if e == nil {
				return
			}
			for _, nm := range []string{name, alt} {
				if c := b.Child(nm); c != nil {
					c.Remove()
				}
			}
			n := ox.New(name)
			if e.Style != "" {
				n.SetAttr("style", e.Style)
				col := ox.New("color", "auto", "1")
				if e.Color != "" {
					col = ox.New("color", "rgb", rgbAttr(e.Color))
				}
				n.Append(col)
			}
			b.InsertOrdered(n, borderOrder)
		}
		side("left", "start", ch.Left)
		side("right", "end", ch.Right)
		side("top", "", ch.Top)
		side("bottom", "", ch.Bottom)
		for _, nm := range []string{"left", "right", "top", "bottom", "diagonal"} {
			if b.Child(nm) == nil && (nm != "left" || b.Child("start") == nil) && (nm != "right" || b.Child("end") == nil) {
				b.InsertOrdered(ox.New(nm), borderOrder)
			}
		}
		x.SetAttr("borderId", strconv.Itoa(st.entry(2, &st.borders, "borders", b)))
		x.SetAttr("applyBorder", "1")
	}
	if ch.HAlign != nil || ch.VAlign != nil || ch.Wrap != nil || ch.Indent != nil {
		a := x.Ensure("alignment", xfOrder)
		set := func(name, v string) {
			if v == "" {
				a.DelAttr(name)
			} else {
				a.SetAttr(name, v)
			}
		}
		if ch.HAlign != nil {
			set("horizontal", *ch.HAlign)
		}
		if ch.VAlign != nil {
			set("vertical", *ch.VAlign)
		}
		if ch.Wrap != nil {
			v := ""
			if *ch.Wrap {
				v = "1"
			}
			set("wrapText", v)
		}
		if ch.Indent != nil {
			v := ""
			if *ch.Indent > 0 {
				v = strconv.Itoa(*ch.Indent)
			}
			set("indent", v)
		}
		x.SetAttr("applyAlignment", "1")
	}
	if ch.NumFmt != nil {
		x.SetAttr("numFmtId", strconv.Itoa(st.numFmtID(*ch.NumFmt)))
		x.SetAttr("applyNumberFormat", "1")
	}
	i := st.entry(3, &st.xfs, "cellXfs", x)
	if i >= len(st.cache) {
		st.cache = append(st.cache, make([]*Style, i+1-len(st.cache))...)
	}
	return int32(i)
}

// numFmtID is the id of a format code: a builtin's, one in the file's, or a new one.
func (st *Styles) numFmtID(code string) int {
	if code == "" || strings.EqualFold(code, "General") {
		return 0
	}
	for id := 1; id < 50; id++ {
		if formula.BuiltinFormat(id) == code {
			return id
		}
	}
	next := 164
	for id, c := range st.numFmts {
		if c == code {
			return id
		}
		next = max(next, id+1)
	}
	st.numFmts[next] = code
	r := st.doc.Root.Ensure("numFmts", styleSheetOrder)
	r.Append(ox.New("numFmt", "numFmtId", strconv.Itoa(next), "formatCode", code))
	r.SetAttr("count", strconv.Itoa(len(r.Elements("numFmt"))))
	st.changed = true
	return next
}

const blankStyles = `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="1"><font><sz val="11"/><color theme="1"/><name val="Calibri"/><family val="2"/><scheme val="minor"/></font></fonts><fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills><borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles><dxfs count="0"/><tableStyles count="0" defaultTableStyle="TableStyleMedium2" defaultPivotStyle="PivotStyleLight16"/></styleSheet>`
