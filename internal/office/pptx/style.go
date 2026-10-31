package pptx

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// RunProps is how a run of words is set, as far as the XML has said.
type RunProps struct {
	Size     float64 // points
	Bold     bool
	Italic   bool
	Under    bool
	Strike   bool
	Baseline int // thousandths of a percent of the size, up
	Cap      string
	Color    color.RGBA
	HasColor bool
	Latin    string
	EA       string
	Sym      string
	Mark     color.RGBA // a highlight, where its alpha is not zero
}

// ParaProps is how a paragraph is set.
type ParaProps struct {
	MarL, MarR, Indent float64 // points
	Align              string  // the file's: l, ctr, r, just, dist
	LinePct, LinePts   float64
	BeforePct          float64
	BeforePts          float64
	AfterPct           float64
	AfterPts           float64
	BuKind             string // "", "none", "char", "auto" or "blip"
	BuChar             string
	BuFont             string
	BuAuto             string
	BuStart            int
	BuColor            color.RGBA
	HasBuColor         bool
	BuPct              float64
	BuPts              float64
	Tabs               []float64
	Run                RunProps
}

func (r *RunProps) apply(n *ox.Node, x *scope) {
	if n == nil {
		return
	}
	if _, ok := n.Lookup("sz"); ok {
		r.Size = attrFloat(n, "sz", 1800) / 100
	}
	r.Bold = attrBool(n, "b", r.Bold)
	r.Italic = attrBool(n, "i", r.Italic)
	if u, ok := n.Lookup("u"); ok {
		r.Under = u != "none"
	}
	if s, ok := n.Lookup("strike"); ok {
		r.Strike = s != "noStrike"
	}
	if _, ok := n.Lookup("baseline"); ok {
		r.Baseline = int(attrInt(n, "baseline", 0))
	}
	if c, ok := n.Lookup("cap"); ok {
		r.Cap = c
	}
	if f := x.readFill(fillChild(n)); f != nil && !f.None {
		r.Color, r.HasColor = f.Color, true
	}
	if h := n.Child("a:highlight"); h != nil {
		if c, ok := x.col.resolve(first(h)); ok {
			r.Mark = c
		}
	}
	if v := n.Child("a:latin"); v != nil {
		r.Latin = v.Attr("typeface")
	}
	if v := n.Child("a:ea"); v != nil {
		r.EA = v.Attr("typeface")
	}
	if v := n.Child("a:sym"); v != nil {
		r.Sym = v.Attr("typeface")
	}
}

func (p *ParaProps) apply(n *ox.Node, x *scope) {
	if n == nil {
		return
	}
	if _, ok := n.Lookup("marL"); ok {
		p.MarL = attrFloat(n, "marL", 0) / emuPerPt
	}
	if _, ok := n.Lookup("marR"); ok {
		p.MarR = attrFloat(n, "marR", 0) / emuPerPt
	}
	if _, ok := n.Lookup("indent"); ok {
		p.Indent = attrFloat(n, "indent", 0) / emuPerPt
	}
	if a := n.Attr("algn"); a != "" {
		p.Align = a
	}
	spacing := func(c *ox.Node, pct, pts *float64) {
		if c == nil {
			return
		}
		if v := c.Child("a:spcPct"); v != nil {
			*pct, *pts = attrFloat(v, "val", 100000)/100000, 0
		}
		if v := c.Child("a:spcPts"); v != nil {
			*pct, *pts = 0, attrFloat(v, "val", 0)/100
		}
	}
	spacing(n.Child("a:lnSpc"), &p.LinePct, &p.LinePts)
	spacing(n.Child("a:spcBef"), &p.BeforePct, &p.BeforePts)
	spacing(n.Child("a:spcAft"), &p.AfterPct, &p.AfterPts)
	if n.Child("a:buClrTx") != nil {
		p.HasBuColor = false
	}
	if c := n.Child("a:buClr"); c != nil {
		if v, ok := x.col.resolve(first(c)); ok {
			p.BuColor, p.HasBuColor = v, true
		}
	}
	if n.Child("a:buSzTx") != nil {
		p.BuPct, p.BuPts = 0, 0
	}
	if v := n.Child("a:buSzPct"); v != nil {
		p.BuPct, p.BuPts = attrFloat(v, "val", 100000)/100000, 0
	}
	if v := n.Child("a:buSzPts"); v != nil {
		p.BuPct, p.BuPts = 0, attrFloat(v, "val", 1800)/100
	}
	if n.Child("a:buFontTx") != nil {
		p.BuFont = ""
	}
	if v := n.Child("a:buFont"); v != nil {
		p.BuFont = v.Attr("typeface")
	}
	switch {
	case n.Child("a:buNone") != nil:
		p.BuKind = "none"
	case n.Child("a:buChar") != nil:
		p.BuKind, p.BuChar = "char", n.Child("a:buChar").Attr("char")
	case n.Child("a:buAutoNum") != nil:
		v := n.Child("a:buAutoNum")
		p.BuKind, p.BuAuto, p.BuStart = "auto", v.Attr("type"), int(attrInt(v, "startAt", 1))
	case n.Child("a:buBlip") != nil:
		p.BuKind, p.BuChar = "blip", "•"
	}
	if t := n.Child("a:tabLst"); t != nil {
		p.Tabs = nil
		for _, k := range t.Elements("a:tab") {
			p.Tabs = append(p.Tabs, attrFloat(k, "pos", 0)/emuPerPt)
		}
	}
	p.Run.apply(n.Child("a:defRPr"), x)
}

// listRef is a list style with the scope it was written in.
type listRef struct {
	list *ox.Node
	x    *scope
}

// levelOf reads a level's list properties.
func levelOf(list *ox.Node, level int) *ox.Node {
	if list == nil {
		return nil
	}
	return list.Child(fmt.Sprintf("a:lvl%dpPr", level+1))
}

// styleOf works out the paragraph properties of a level through the lists, least specific first.
func styleOf(lists []listRef, level int) ParaProps {
	p := ParaProps{Run: RunProps{Size: 18}}
	for _, l := range lists {
		p.apply(levelOf(l.list, level), l.x)
	}
	return p
}

// textChain lists the list styles a shape's paragraphs inherit: the presentation's or the master's style by the kind
// of placeholder, then the placeholders' own, each in the scope of the part it is in.
func (d *Doc) textChain(sh *Shape, x *scope, l *Layout) []listRef {
	m := l.Master
	var out []listRef
	mx := masterScope(m)
	if sh.Ph == nil {
		out = append(out, listRef{d.defaultText, x})
	} else {
		st := m.Doc.Root.Child("p:txStyles")
		switch family(sh.Ph.Type) {
		case "title":
			out = append(out, listRef{st.Child("p:titleStyle"), mx})
		case "body", "pic":
			out = append(out, listRef{st.Child("p:bodyStyle"), mx})
		default:
			out = append(out, listRef{st.Child("p:otherStyle"), mx})
		}
		for i := len(sh.Chain) - 1; i >= 0; i-- {
			k := sh.Chain[i]
			out = append(out, listRef{k.Node.Path("p:txBody", "a:lstStyle"), chainScope(k, l)})
		}
	}
	return out
}

// bodyProps merges a shape's a:bodyPr attributes over its placeholders'.
type bodyProps struct {
	chain []*ox.Node
}

func (b bodyProps) attr(name string) (string, bool) {
	for _, n := range b.chain {
		if v, ok := n.Lookup(name); ok {
			return v, true
		}
	}
	return "", false
}

func (b bodyProps) num(name string, fallback float64) float64 {
	if v, ok := b.attr(name); ok {
		n := &ox.Node{Attrs: []ox.Attr{{Name: "v", Value: v}}}
		return attrFloat(n, "v", fallback)
	}
	return fallback
}

// fit is the autofit element in force: "none", "norm" or "shape", with what norm says.
func (b bodyProps) fit() (kind string, fontScale, lineCut float64) {
	for _, n := range b.chain {
		switch {
		case n.Child("a:noAutofit") != nil:
			return "none", 1, 0
		case n.Child("a:spAutoFit") != nil:
			return "shape", 1, 0
		case n.Child("a:normAutofit") != nil:
			f := n.Child("a:normAutofit")
			return "norm", attrFloat(f, "fontScale", 100000) / 100000, attrFloat(f, "lnSpcReduction", 0) / 100000
		}
	}
	return "none", 1, 0
}

// fontName puts a real name in place of a theme font's token.
func fontName(name string, t *Theme, major bool) string {
	switch {
	case name == "+mj-lt":
		return t.Major.Latin
	case name == "+mn-lt":
		return t.Minor.Latin
	case name == "+mj-ea":
		return t.Major.EA
	case name == "+mn-ea":
		return t.Minor.EA
	case name == "+mj-cs":
		return t.Major.CS
	case name == "+mn-cs":
		return t.Minor.CS
	case name == "":
		if major {
			return t.Major.Latin
		}
		return t.Minor.Latin
	}
	return name
}

// symbolChars are the marks symbol fonts use for bullets, as what they look like.
var symbolChars = map[string]map[rune]string{
	"wingdings": {'§': "▪", 'l': "●", 'n': "■", 'q': "❑", 'ü': "✓", 'Ø': "➢", 'v': "❖", 'u': "◆", 'o': "□", 'à': "→", 'è': "➔", 'þ': "☑", '¨': "◻"},
	"symbol":    {'·': "•", '§': "♣", 'Ø': "¬"},
}

// bulletText is a bullet's character, moved out of a symbol font; it reports whether it was.
func bulletText(ch, font string) (string, bool) {
	if m := symbolChars[strings.ToLower(font)]; m != nil {
		rs := []rune(ch)
		if len(rs) == 1 {
			if s, ok := m[rs[0]]; ok {
				return s, true
			}
		}
	}
	return ch, false
}

// autoNumber is what a list numbered in the scheme shows for n.
func autoNumber(scheme string, n int) string {
	var body string
	lower := strings.Contains(scheme, "Lc")
	switch {
	case strings.HasPrefix(scheme, "alpha"):
		body = alphaNumber(n)
		if lower {
			body = strings.ToLower(body)
		}
	case strings.HasPrefix(scheme, "roman"):
		body = romanNumber(n)
		if lower {
			body = strings.ToLower(body)
		}
	case strings.HasPrefix(scheme, "circleNum"):
		if n >= 1 && n <= 20 {
			return string(rune(0x2460 + n - 1))
		}
		body = fmt.Sprint(n)
	default:
		body = fmt.Sprint(n)
	}
	switch {
	case strings.HasSuffix(scheme, "ParenBoth"):
		return "(" + body + ")"
	case strings.HasSuffix(scheme, "ParenR"):
		return body + ")"
	case strings.HasSuffix(scheme, "Plain"):
		return body
	}
	return body + "."
}

func alphaNumber(n int) string {
	if n <= 0 {
		return ""
	}
	// A, B, … Z, AA, BB, as Office counts.
	return strings.Repeat(string(rune('A'+(n-1)%26)), (n-1)/26+1)
}

func romanNumber(n int) string {
	if n <= 0 || n >= 4000 {
		return fmt.Sprint(n)
	}
	var b strings.Builder
	for _, v := range []struct {
		n int
		s string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}} {
		for n >= v.n {
			b.WriteString(v.s)
			n -= v.n
		}
	}
	return b.String()
}
