package pptx

import (
	"image/color"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/dml"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Theme is a theme's colours and fonts, and its lists of fills, lines, effects and background fills.
type Theme struct {
	Part        string
	Name        string
	Colors      map[string]color.RGBA // dk1, lt1, dk2, lt2, accent1..6, hlink, folHlink
	Major       Fonts
	Minor       Fonts
	Fills       []*ox.Node
	Lines       []*ox.Node
	Effects     []*ox.Node
	Backgrounds []*ox.Node
	Rels        []opc.Rel
}

// Fonts are a font scheme's fonts by script.
type Fonts struct {
	Latin, EA, CS string
}

func loadTheme(pkg *opc.Package, name string) (*Theme, error) {
	doc, err := parsePart(pkg, name)
	if err != nil {
		return nil, err
	}
	t := &Theme{Part: name, Name: doc.Root.Attr("name"), Colors: map[string]color.RGBA{}}
	t.Rels, _ = pkg.Rels(name)
	t.Colors = dml.DefaultScheme()
	te := doc.Root.Child("a:themeElements")
	ctx := &colorCtx{theme: t}
	for _, n := range te.Child("a:clrScheme").Elements() {
		slot := strings.TrimPrefix(n.Name, "a:")
		if els := n.Elements(); len(els) > 0 {
			if c, ok := ctx.resolve(els[0]); ok {
				t.Colors[slot] = c
			}
		}
	}
	readFonts := func(n *ox.Node) Fonts {
		return Fonts{Latin: n.Child("a:latin").Attr("typeface"), EA: n.Child("a:ea").Attr("typeface"), CS: n.Child("a:cs").Attr("typeface")}
	}
	fs := te.Child("a:fontScheme")
	t.Major, t.Minor = readFonts(fs.Child("a:majorFont")), readFonts(fs.Child("a:minorFont"))
	fm := te.Child("a:fmtScheme")
	t.Fills = fm.Child("a:fillStyleLst").Elements()
	t.Lines = fm.Child("a:lnStyleLst").Elements()
	t.Effects = fm.Child("a:effectStyleLst").Elements()
	t.Backgrounds = fm.Child("a:bgFillStyleLst").Elements()
	return t, nil
}

// Palette is the theme's colours as a colour picker offers them.
func (t *Theme) Palette() []color.RGBA {
	var out []color.RGBA
	for _, name := range []string{"lt1", "dk1", "lt2", "dk2", "accent1", "accent2", "accent3", "accent4", "accent5", "accent6"} {
		out = append(out, t.Colors[name])
	}
	return out
}

// clrMapOf reads a p:clrMap or a:overrideClrMapping.
func clrMapOf(n *ox.Node) map[string]string {
	m := map[string]string{"bg1": "lt1", "tx1": "dk1", "bg2": "lt2", "tx2": "dk2"}
	if n == nil {
		return m
	}
	for _, at := range n.Attrs {
		if !strings.HasPrefix(at.Name, "xmlns") {
			m[at.Name] = at.Value
		}
	}
	return m
}

// colorCtx is what a colour in a part means: the theme, the colour map in force, and the colour phClr stands for.
type colorCtx struct {
	theme *Theme
	cmap  map[string]string
	ph    *color.RGBA
}

// withPh is the context with phClr standing for c.
func (x *colorCtx) withPh(c color.RGBA) *colorCtx {
	k := *x
	k.ph = &c
	return &k
}

// resolve is the colour of a colour element (a:srgbClr, a:schemeClr and so on), with its transforms applied.
func (x *colorCtx) resolve(n *ox.Node) (color.RGBA, bool) { return dml.Resolve(n, x.scheme) }

func (x *colorCtx) scheme(name string) (color.RGBA, bool) {
	if name == "phClr" {
		if x.ph != nil {
			return *x.ph, true
		}
		return color.RGBA{0, 0, 0, 255}, true
	}
	if m, ok := x.cmap[name]; ok {
		name = m
	}
	switch name { // with no map in force, as in the theme's own elements
	case "bg1":
		name = "lt1"
	case "tx1":
		name = "dk1"
	case "bg2":
		name = "lt2"
	case "tx2":
		name = "dk2"
	}
	if x.theme == nil {
		return color.RGBA{}, false
	}
	c, ok := x.theme.Colors[name]
	return c, ok
}

// Hex is the colour as the file writes it.
func Hex(c color.RGBA) string {
	const digits = "0123456789ABCDEF"
	b := []byte{digits[c.R>>4], digits[c.R&15], digits[c.G>>4], digits[c.G&15], digits[c.B>>4], digits[c.B&15]}
	return string(b)
}

// The colour arithmetic is shared with the other formats.
var (
	parseHex  = dml.ParseHex
	gammaByte = dml.GammaByte
	toHSL     = dml.ToHSL
	hslColor  = dml.HSL
	transform = dml.Transform
)
