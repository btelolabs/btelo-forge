package docx

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Style resolution: properties are worked out by applying each layer's XML over the last, in Word's order: document
// defaults, table style, paragraph style (with what it is based on), numbering level, character style, direct
// formatting. The layers that many paragraphs share are remembered.

// Schema orders of the property elements, from ECMA-376 part 1 (CT_RPr, CT_PPr and their relatives).
var (
	orderRPr = []string{"w:rStyle", "w:rFonts", "w:b", "w:bCs", "w:i", "w:iCs", "w:caps", "w:smallCaps", "w:strike", "w:dstrike",
		"w:outline", "w:shadow", "w:emboss", "w:imprint", "w:noProof", "w:snapToGrid", "w:vanish", "w:webHidden", "w:color",
		"w:spacing", "w:w", "w:kern", "w:position", "w:sz", "w:szCs", "w:highlight", "w:u", "w:effect", "w:bdr", "w:shd",
		"w:fitText", "w:vertAlign", "w:rtl", "w:cs", "w:em", "w:lang", "w:eastAsianLayout", "w:specVanish", "w:oMath", "w:rPrChange"}
	orderPPr = []string{"w:pStyle", "w:keepNext", "w:keepLines", "w:pageBreakBefore", "w:framePr", "w:widowControl", "w:numPr",
		"w:suppressLineNumbers", "w:pBdr", "w:shd", "w:tabs", "w:suppressAutoHyphens", "w:kinsoku", "w:wordWrap",
		"w:overflowPunct", "w:topLinePunct", "w:autoSpaceDE", "w:autoSpaceDN", "w:bidi", "w:adjustRightInd", "w:snapToGrid",
		"w:spacing", "w:ind", "w:contextualSpacing", "w:mirrorIndents", "w:suppressOverlap", "w:jc", "w:textDirection",
		"w:textAlignment", "w:textboxTightWrap", "w:outlineLvl", "w:divId", "w:cnfStyle", "w:rPr", "w:sectPr", "w:pPrChange"}
	orderTcPr = []string{"w:cnfStyle", "w:tcW", "w:gridSpan", "w:hMerge", "w:vMerge", "w:tcBorders", "w:shd", "w:noWrap", "w:tcMar",
		"w:textDirection", "w:tcFitText", "w:vAlign", "w:hideMark", "w:headers", "w:cellIns", "w:cellDel", "w:cellMerge", "w:tcPrChange"}
	orderTrPr = []string{"w:cnfStyle", "w:divId", "w:gridBefore", "w:gridAfter", "w:wBefore", "w:wAfter", "w:cantSplit", "w:trHeight",
		"w:tblHeader", "w:tblCellSpacing", "w:jc", "w:hidden", "w:ins", "w:del", "w:trPrChange"}
	orderSectPr = []string{"w:headerReference", "w:footerReference", "w:footnotePr", "w:endnotePr", "w:type", "w:pgSz", "w:pgMar",
		"w:paperSrc", "w:pgBorders", "w:lnNumType", "w:pgNumType", "w:cols", "w:formProt", "w:vAlign", "w:noEndnote", "w:titlePg",
		"w:textDirection", "w:bidi", "w:rtlGutter", "w:docGrid", "w:printerSettings", "w:sectPrChange"}
)

// RP are a run's resolved properties.
type RP struct {
	Ascii, HAnsi, East, CS string
	Hint                   string
	Size                   float32 // points
	Bold, Italic           bool
	Strike, DStrike        bool
	Caps, SmallCaps        bool
	Vanish                 bool
	Under                  string // the kind of underline, "" for none
	Color                  color.RGBA
	Highlight              color.RGBA
	Vert                   int8 // 1 superscript, -1 subscript
	Style                  string
}

// Tab is a tab stop.
type Tab struct {
	Pos    float32
	Kind   flow.TabKind
	Leader rune
	Clear  bool
}

// Border is one line of a border.
type Border struct {
	Kind  string // single, double, dashed, dotted...; "" or none for no line
	Width float32
	Space float32
	Color color.RGBA
}

func (b Border) shows() bool { return b.Kind != "" && b.Kind != "none" && b.Kind != "nil" }

// PP are a paragraph's resolved properties.
type PP struct {
	Style                 string
	Align                 flow.Align
	Left, Right, First    float32
	LeftCh, RightCh, Hang int // hundredths of a character, overriding the lengths where not zero
	FirstCh               int
	Before, After         float32
	BeforeLn, AfterLn     int // hundredths of a line
	Line                  float32
	Rule                  flow.LineRule
	KeepNext, KeepLines   bool
	PageBefore, Widow     bool
	Contextual            bool
	NumID, Ilvl           int
	HasIlvl               bool
	Tabs                  []Tab
	Fill                  color.RGBA
	Top, Bottom, Lft, Rgt Border
	Between               Border
	Outline               int  // the outline level, -1 for body text
	NoGrid                bool // stays out of the section's line grid
}

// Base is the properties a layer leaves for the next.
type Base struct {
	R RP
	P PP
}

// Style is one style of the document.
type Style struct {
	ID, Name, Type string
	BasedOn, Next  string
	Link           string
	Default        bool
	Hidden, Quick  bool
	UIPriority     int
	Node           *ox.Node
}

type baseKey struct {
	over *Base
	id   string
}

// Styles are the document's styles.
type Styles struct {
	d        *Doc
	Part     *Part // nil when the file has none
	ByID     map[string]*Style
	List     []*Style
	defaults Base
	paraDef  string // the default paragraph style
	charDef  string
	tableDef string
	memo     map[baseKey]*Base
	cond     map[string]*Base
}

func (d *Doc) loadStyles() *Styles {
	s := &Styles{d: d, ByID: map[string]*Style{}, memo: map[baseKey]*Base{}, cond: map[string]*Base{}, Part: d.optional(relStyles)}
	s.defaults.R.Size = 10 // the schema's default where a file names none
	s.defaults.R.Color = color.RGBA{}
	s.defaults.P.Widow = true
	s.defaults.P.Outline = -1
	if s.Part != nil {
		s.read()
	}
	return s
}

// read takes the styles from the part's tree.
func (s *Styles) read() {
	root := s.Part.X.Root
	if dd := root.Child("w:docDefaults"); dd != nil {
		s.applyR(&s.defaults.R, dd.Path("w:rPrDefault", "w:rPr"))
		s.applyP(&s.defaults.P, dd.Path("w:pPrDefault", "w:pPr"))
	}
	for _, n := range root.Elements("w:style") {
		st := &Style{ID: n.Attr("w:styleId"), Type: n.Attr("w:type"), Node: n, Default: on(n.Attr("w:default")) && n.Attr("w:default") != ""}
		if st.Type == "" {
			st.Type = "paragraph"
		}
		st.Name = n.Child("w:name").Attr("w:val")
		st.BasedOn = n.Child("w:basedOn").Attr("w:val")
		st.Next = n.Child("w:next").Attr("w:val")
		st.Link = n.Child("w:link").Attr("w:val")
		st.Hidden = n.Child("w:semiHidden") != nil || n.Child("w:hidden") != nil
		st.Quick = n.Child("w:qFormat") != nil
		st.UIPriority = 99
		if p := n.Child("w:uiPriority"); p != nil {
			st.UIPriority = atoi(p.Attr("w:val"))
		}
		if st.ID == "" {
			continue
		}
		if _, dup := s.ByID[st.ID]; dup {
			continue
		}
		s.ByID[st.ID] = st
		s.List = append(s.List, st)
		if st.Default {
			switch st.Type {
			case "paragraph":
				s.paraDef = st.ID
			case "character":
				s.charDef = st.ID
			case "table":
				s.tableDef = st.ID
			}
		}
	}
}

// reset forgets what was worked out, after the styles part changed.
func (s *Styles) reset() {
	s.ByID, s.List = map[string]*Style{}, nil
	s.memo, s.cond = map[baseKey]*Base{}, map[string]*Base{}
	s.defaults = Base{}
	s.defaults.R.Size = 10
	s.defaults.P.Widow = true
	s.defaults.P.Outline = -1
	if s.Part != nil {
		s.read()
	}
}

// chain is a style and those it is based on, the deepest first.
func (s *Styles) chain(id string) []*Style {
	var out []*Style
	for depth := 0; depth < 32 && id != ""; depth++ {
		st := s.ByID[id]
		if st == nil {
			break
		}
		out = append(out, st)
		id = st.BasedOn
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Para is the base for a paragraph in style id, over (nil for the document's defaults). An unknown style is the
// default paragraph style, as Word treats it.
func (s *Styles) Para(over *Base, id string) *Base {
	if id == "" || s.ByID[id] == nil {
		id = s.paraDef
	}
	key := baseKey{over, id}
	if b := s.memo[key]; b != nil {
		return b
	}
	b := s.defaults
	if over != nil {
		b = *over
	}
	b.P.Tabs = append([]Tab(nil), b.P.Tabs...)
	for _, st := range s.chain(id) {
		if st.Type != "paragraph" {
			continue
		}
		s.applyP(&b.P, st.Node.Child("w:pPr"))
		s.applyR(&b.R, st.Node.Child("w:rPr"))
	}
	b.P.Style = id
	if st := s.ByID[id]; st != nil && b.P.NumID != 0 {
		// A style that carries numbering names the level through the numbering definition.
		if !b.P.HasIlvl {
			b.P.Ilvl = s.d.Num.levelOfStyle(b.P.NumID, id)
		}
	}
	s.memo[key] = &b
	return &b
}

// Char applies character style id over rp.
func (s *Styles) Char(rp *RP, id string) {
	for _, st := range s.chain(id) {
		if st.Type == "character" {
			s.applyR(rp, st.Node.Child("w:rPr"))
		}
	}
	rp.Style = id
}

// themeFont is the name a theme font attribute stands for.
func (s *Styles) themeFont(v string) string {
	t := s.d.Theme
	switch v {
	case "majorHAnsi", "majorAscii", "majorBidi":
		return t.MajorLatin
	case "minorHAnsi", "minorAscii", "minorBidi":
		return t.MinorLatin
	case "majorEastAsia":
		return t.MajorEast
	case "minorEastAsia":
		return t.MinorEast
	}
	return ""
}

// applyR puts the properties in an rPr over rp.
func (s *Styles) applyR(rp *RP, n *ox.Node) {
	if n == nil {
		return
	}
	for _, c := range n.Children {
		if c.Kind != ox.Element {
			continue
		}
		switch c.Name {
		case "w:rStyle":
			s.Char(rp, c.Attr("w:val"))
		case "w:rFonts":
			s.applyFonts(rp, c)
		case "w:b":
			rp.Bold = toggle(c)
		case "w:i":
			rp.Italic = toggle(c)
		case "w:strike":
			rp.Strike = toggle(c)
		case "w:dstrike":
			rp.DStrike = toggle(c)
		case "w:caps":
			rp.Caps = toggle(c)
		case "w:smallCaps":
			rp.SmallCaps = toggle(c)
		case "w:vanish":
			rp.Vanish = toggle(c)
		case "w:color":
			rp.Color = s.d.Theme.color(c, rp.Color)
		case "w:sz":
			if v := atoi(c.Attr("w:val")); v > 0 && v <= 3276 {
				rp.Size = float32(v) / 2
			}
		case "w:highlight":
			rp.Highlight = highlightColor(c.Attr("w:val"))
		case "w:shd":
			if f := s.d.Theme.fill(c); f.A != 0 {
				rp.Highlight = f
			}
		case "w:u":
			rp.Under = c.Attr("w:val")
			if rp.Under == "none" {
				rp.Under = ""
			}
		case "w:vertAlign":
			switch c.Attr("w:val") {
			case "superscript":
				rp.Vert = 1
			case "subscript":
				rp.Vert = -1
			default:
				rp.Vert = 0
			}
		}
	}
}

func (s *Styles) applyFonts(rp *RP, c *ox.Node) {
	set := func(field *string, plain, theme string) {
		if v := c.Attr(theme); v != "" {
			if name := s.themeFont(v); name != "" {
				*field = name
				return
			}
		}
		if v := c.Attr(plain); v != "" {
			*field = v
		}
	}
	set(&rp.Ascii, "w:ascii", "w:asciiTheme")
	set(&rp.HAnsi, "w:hAnsi", "w:hAnsiTheme")
	set(&rp.East, "w:eastAsia", "w:eastAsiaTheme")
	set(&rp.CS, "w:cs", "w:cstheme")
	if v := c.Attr("w:hint"); v != "" {
		rp.Hint = v
	}
}

// applyP puts the properties in a pPr over pp.
func (s *Styles) applyP(pp *PP, n *ox.Node) {
	if n == nil {
		return
	}
	t := s.d.Theme
	for _, c := range n.Children {
		if c.Kind != ox.Element {
			continue
		}
		switch c.Name {
		case "w:pStyle":
			pp.Style = c.Attr("w:val")
		case "w:keepNext":
			pp.KeepNext = toggle(c)
		case "w:keepLines":
			pp.KeepLines = toggle(c)
		case "w:pageBreakBefore":
			pp.PageBefore = toggle(c)
		case "w:widowControl":
			pp.Widow = toggle(c)
		case "w:contextualSpacing":
			pp.Contextual = toggle(c)
		case "w:snapToGrid":
			pp.NoGrid = !toggle(c)
		case "w:numPr":
			if v, ok := c.Child("w:numId").Lookup("w:val"); ok {
				pp.NumID = atoi(v)
			}
			if v, ok := c.Child("w:ilvl").Lookup("w:val"); ok {
				pp.Ilvl, pp.HasIlvl = min(max(atoi(v), 0), 8), true
			}
		case "w:jc":
			pp.Align = alignOf(c.Attr("w:val"))
		case "w:outlineLvl":
			pp.Outline = min(max(atoi(c.Attr("w:val")), 0), 9)
			if pp.Outline == 9 {
				pp.Outline = -1
			}
		case "w:shd":
			pp.Fill = t.fill(c)
		case "w:spacing":
			if v, ok := c.Lookup("w:before"); ok {
				pp.Before = measure(v)
			}
			if v, ok := c.Lookup("w:after"); ok {
				pp.After = measure(v)
			}
			if v, ok := c.Lookup("w:beforeLines"); ok {
				pp.BeforeLn = atoi(v)
			}
			if v, ok := c.Lookup("w:afterLines"); ok {
				pp.AfterLn = atoi(v)
			}
			if v, ok := c.Lookup("w:line"); ok {
				rule := c.Attr("w:lineRule")
				if rule == "" {
					rule = "auto"
				}
				switch rule {
				case "exact":
					pp.Line, pp.Rule = measure(v), flow.Exact
				case "atLeast":
					pp.Line, pp.Rule = measure(v), flow.AtLeast
				default:
					pp.Line, pp.Rule = float32(atoi(v))/240, flow.Multiple
				}
			}
		case "w:ind":
			if v, ok := firstAttr(c, "w:left", "w:start"); ok {
				pp.Left, pp.LeftCh = measure(v), 0
			}
			if v, ok := firstAttr(c, "w:right", "w:end"); ok {
				pp.Right, pp.RightCh = measure(v), 0
			}
			if v, ok := c.Lookup("w:firstLine"); ok {
				pp.First, pp.FirstCh, pp.Hang = measure(v), 0, 0
			}
			if v, ok := c.Lookup("w:hanging"); ok {
				pp.First, pp.FirstCh, pp.Hang = -measure(v), 0, 0
			}
			if v, ok := firstAttr(c, "w:leftChars", "w:startChars"); ok {
				pp.LeftCh = atoi(v)
			}
			if v, ok := firstAttr(c, "w:rightChars", "w:endChars"); ok {
				pp.RightCh = atoi(v)
			}
			if v, ok := c.Lookup("w:firstLineChars"); ok {
				pp.FirstCh, pp.Hang = atoi(v), 0
			}
			if v, ok := c.Lookup("w:hangingChars"); ok {
				pp.Hang, pp.FirstCh = atoi(v), 0
			}
		case "w:tabs":
			pp.Tabs = mergeTabs(pp.Tabs, c)
		case "w:pBdr":
			for _, b := range c.Elements() {
				line := t.border(b)
				switch b.Name {
				case "w:top":
					pp.Top = line
				case "w:bottom":
					pp.Bottom = line
				case "w:left":
					pp.Lft = line
				case "w:right":
					pp.Rgt = line
				case "w:between":
					pp.Between = line
				}
			}
		}
	}
}

func firstAttr(n *ox.Node, names ...string) (string, bool) {
	for _, name := range names {
		if v, ok := n.Lookup(name); ok {
			return v, true
		}
	}
	return "", false
}

func alignOf(v string) flow.Align {
	switch v {
	case "center":
		return flow.Center
	case "right", "end":
		return flow.Right
	case "both", "distribute", "lowKashida", "mediumKashida", "highKashida":
		return flow.Justify
	}
	return flow.Left
}

func mergeTabs(tabs []Tab, n *ox.Node) []Tab {
	out := append([]Tab(nil), tabs...)
	for _, c := range n.Elements("w:tab") {
		t := Tab{Pos: measure(c.Attr("w:pos"))}
		switch c.Attr("w:val") {
		case "clear":
			t.Clear = true
		case "center":
			t.Kind = flow.TabCenter
		case "right", "end":
			t.Kind = flow.TabRight
		case "decimal":
			t.Kind = flow.TabDecimal
		}
		switch c.Attr("w:leader") {
		case "dot":
			t.Leader = '.'
		case "hyphen":
			t.Leader = '-'
		case "underscore", "heavy":
			t.Leader = '_'
		case "middleDot":
			t.Leader = '·'
		}
		at := -1
		for i := range out {
			if out[i].Pos == t.Pos {
				at = i
				break
			}
		}
		switch {
		case t.Clear && at >= 0:
			out = append(out[:at], out[at+1:]...)
		case t.Clear:
		case at >= 0:
			out[at] = t
		default:
			i := 0
			for i < len(out) && out[i].Pos < t.Pos {
				i++
			}
			out = append(out, Tab{})
			copy(out[i+1:], out[i:])
			out[i] = t
		}
	}
	return out
}

var highlightNames = map[string]uint32{
	"black": 0x000000, "blue": 0x0000ff, "cyan": 0x00ffff, "green": 0x00ff00, "magenta": 0xff00ff, "red": 0xff0000,
	"yellow": 0xffff00, "white": 0xffffff, "darkBlue": 0x000080, "darkCyan": 0x008080, "darkGreen": 0x008000,
	"darkMagenta": 0x800080, "darkRed": 0x800000, "darkYellow": 0x808000, "darkGray": 0x808080, "lightGray": 0xc0c0c0,
}

func highlightColor(name string) color.RGBA {
	if v, ok := highlightNames[name]; ok {
		return gfx.Hex(v)
	}
	return color.RGBA{}
}

// Theme is what the theme part gives: its colours and fonts.
type Theme struct {
	Colors                 map[string]color.RGBA
	MajorLatin, MinorLatin string
	MajorEast, MinorEast   string
}

// themeNames maps the names in w:themeColor to the theme's own.
var themeNames = map[string]string{
	"dark1": "dk1", "light1": "lt1", "dark2": "dk2", "light2": "lt2", "text1": "dk1", "background1": "lt1", "text2": "dk2",
	"background2": "lt2", "accent1": "accent1", "accent2": "accent2", "accent3": "accent3", "accent4": "accent4",
	"accent5": "accent5", "accent6": "accent6", "hyperlink": "hlink", "followedHyperlink": "folHlink",
}

func (d *Doc) loadTheme() *Theme {
	t := &Theme{Colors: map[string]color.RGBA{
		"dk1": gfx.Hex(0), "lt1": gfx.Hex(0xffffff), "dk2": gfx.Hex(0x44546a), "lt2": gfx.Hex(0xe7e6e6),
		"accent1": gfx.Hex(0x4472c4), "accent2": gfx.Hex(0xed7d31), "accent3": gfx.Hex(0xa5a5a5), "accent4": gfx.Hex(0xffc000),
		"accent5": gfx.Hex(0x5b9bd5), "accent6": gfx.Hex(0x70ad47), "hlink": gfx.Hex(0x0563c1), "folHlink": gfx.Hex(0x954f72),
	}, MajorLatin: "Calibri Light", MinorLatin: "Calibri", MajorEast: "等线 Light", MinorEast: "等线"}
	p := d.optional(relTheme)
	if p == nil {
		return t
	}
	el := p.X.Root.Child("a:themeElements")
	for _, c := range el.Child("a:clrScheme").Elements() {
		name := strings.TrimPrefix(c.Name, "a:")
		if v := c.Child("a:srgbClr").Attr("val"); v != "" {
			t.Colors[name] = hexColor(v, t.Colors[name])
		} else if v := c.Child("a:sysClr").Attr("lastClr"); v != "" {
			t.Colors[name] = hexColor(v, t.Colors[name])
		}
	}
	read := func(font *ox.Node, latin, east *string) {
		if font == nil {
			return
		}
		if v := font.Child("a:latin").Attr("typeface"); v != "" {
			*latin = v
		}
		if v := font.Child("a:ea").Attr("typeface"); v != "" {
			*east = v
			return
		}
		for _, f := range font.Elements("a:font") {
			if f.Attr("script") == "Hans" && f.Attr("typeface") != "" {
				*east = f.Attr("typeface")
			}
		}
	}
	read(el.Path("a:fontScheme", "a:majorFont"), &t.MajorLatin, &t.MajorEast)
	read(el.Path("a:fontScheme", "a:minorFont"), &t.MinorLatin, &t.MinorEast)
	return t
}

// hexColor reads RRGGBB; or is what a bad value gives.
func hexColor(s string, or color.RGBA) color.RGBA {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		return or
	}
	return gfx.Hex(uint32(v))
}

// color reads a colour with its theme reference from element c; auto is no colour, which means the text's default.
func (t *Theme) color(c *ox.Node, or color.RGBA) color.RGBA {
	return t.colorAttrs(c, "w:val", "w:themeColor", "w:themeTint", "w:themeShade", or)
}

func (t *Theme) colorAttrs(c *ox.Node, val, theme, tint, shade string, or color.RGBA) color.RGBA {
	if name := c.Attr(theme); name != "" {
		key := themeNames[name]
		if key == "" {
			key = name
		}
		if col, ok := t.Colors[key]; ok {
			if v := c.Attr(tint); v != "" {
				f, _ := strconv.ParseUint(v, 16, 16)
				return gfx.Mix(gfx.Hex(0xffffff), col, float32(f)/255)
			}
			if v := c.Attr(shade); v != "" {
				f, _ := strconv.ParseUint(v, 16, 16)
				return gfx.Mix(gfx.Hex(0), col, float32(f)/255)
			}
			return col
		}
	}
	v, ok := c.Lookup(val)
	if !ok {
		return or
	}
	if v == "auto" {
		return color.RGBA{}
	}
	return hexColor(v, or)
}

// fill reads a w:shd's background; patterns are drawn as their fill.
func (t *Theme) fill(c *ox.Node) color.RGBA {
	if c.Attr("w:val") == "nil" {
		return color.RGBA{}
	}
	return t.colorAttrs(c, "w:fill", "w:themeFill", "w:themeFillTint", "w:themeFillShade", color.RGBA{})
}

// border reads a border element.
func (t *Theme) border(c *ox.Node) Border {
	b := Border{Kind: c.Attr("w:val"), Width: float32(atoi(c.Attr("w:sz"))) / 8, Space: float32(atoi(c.Attr("w:space")))}
	b.Width = min(max(b.Width, 0.25), 12)
	b.Color = t.color(c, color.RGBA{})
	return b
}

// loadSettings reads the settings the layout cares about.
func (d *Doc) loadSettings() {
	p := d.optional(relSettings)
	if p == nil {
		return
	}
	root := p.X.Root
	if v := measure(root.Child("w:defaultTabStop").Attr("w:val")); v >= 1 {
		d.defTab = v
	}
	if n := root.Child("w:evenAndOddHeaders"); n != nil {
		d.evenOdd = toggle(n)
	}
	d.lang = root.Child("w:themeFontLang").Attr("w:eastAsia")
}

// StyleList are the paragraph styles to offer, the usual ones first.
func (s *Styles) StyleList() []*Style {
	var out []*Style
	for _, st := range s.List {
		if st.Type == "paragraph" && !st.Hidden {
			out = append(out, st)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].UIPriority < out[j-1].UIPriority; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ensureStylesPart makes the styles part of a file that has none.
func (d *Doc) ensureStylesPart() *Part {
	if d.Styles.Part != nil {
		return d.Styles.Part
	}
	name := d.Pkg.UniqueName("word/styles%d.xml")
	d.Pkg.Put(name, ctStyles, []byte(blankStyles))
	d.addRel(d.Main, relStyles, opc.RelativeTarget(d.Main.Name, name), false)
	p, err := d.part(name)
	if err != nil {
		return nil // what was just written parses
	}
	d.Styles.Part = p
	d.Styles.reset()
	return p
}

// EnsureStyle adds one of the built-in styles (Normal, Heading1...) to a document that lacks it, from the set a new
// document has, and says whether it is there now.
func (d *Doc) EnsureStyle(id string) bool {
	if d.Styles.ByID[id] != nil {
		return true
	}
	x, err := ox.Parse([]byte(blankStyles))
	if err != nil {
		return false
	}
	var src *ox.Node
	for _, n := range x.Root.Elements("w:style") {
		if n.Attr("w:styleId") == id {
			src = n
		}
	}
	if src == nil {
		return false
	}
	p := d.ensureStylesPart()
	if p == nil {
		return false
	}
	if d.Styles.ByID[id] != nil {
		return true
	}
	d.saveRange(p, p.X.Root, len(p.X.Root.Children), 0)
	clone := src.Clone()
	// What it is based on comes along, or the style would float free.
	p.X.Root.Append(clone)
	d.touched(p)
	d.Styles.reset()
	d.allChanged()
	if base := src.Child("w:basedOn").Attr("w:val"); base != "" {
		d.EnsureStyle(base)
	}
	if next := src.Child("w:next").Attr("w:val"); next != "" {
		d.EnsureStyle(next)
	}
	if link := src.Child("w:link").Attr("w:val"); link != "" {
		d.EnsureStyle(link)
	}
	return true
}
