package mind

import (
	"image/color"
	"strings"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Themes: how a map looks, chosen for each map and kept in its file. The first is after MindNode on macOS in its
// dark appearance: a plain dark board; a tree's root is a rounded bubble with a soft shadow; every other topic is
// its words resting on a line of its branch's colour, and the branch flows into that line, tapering from the parent
// to the child. Others are light, coloured or have every topic in a bubble. Free nodes with a shape of their own
// are a flowchart's boxes, flat as the pictures and tables are, with rounded corners and no shadow.

// Theme is a map's look.
type Theme struct {
	ID       string // as kept in the file and named to tools
	Name     string // as shown, in English (the window translates it)
	Paper    color.RGBA
	Ink      color.RGBA // words
	InkDim   color.RGBA // captions, placeholders
	BoxFill  color.RGBA // a flowchart box
	BoxLine  color.RGBA
	RootFill color.RGBA // a tree's root
	RootInk  color.RGBA
	Edge     color.RGBA // connectors
	Media    color.RGBA // the card under a picture, a video or a nested map
	WarnFill color.RGBA
	WarnLine color.RGBA
	Branches []color.RGBA // one per main branch, in turn
	Bubbles  bool         // every topic is a bubble of its branch's colour, rather than words over a line
	Width    float64      // branches' thickness, against the first theme's
	Light    bool         // a light board
}

// Branch is the colour of main branch i.
func (t *Theme) Branch(i int) color.RGBA { return t.Branches[i%len(t.Branches)] }

// Themes are the looks a map can take, the default first.
var Themes = []*Theme{
	{ID: "midnight", Name: "Midnight", Paper: gfx.Hex(0x1c1c1f), Ink: gfx.Hex(0xf2f2f5), InkDim: gfx.Hex(0xa1a1a8),
		BoxFill: gfx.Hex(0x2c2c31), BoxLine: gfx.Hex(0x48484f), RootFill: gfx.Hex(0xf2f2f5), RootInk: gfx.Hex(0x1c1c1f),
		Edge: gfx.Hex(0x8e8e96), Media: gfx.Hex(0x2c2c31), WarnFill: gfx.Hex(0x3a2f17), WarnLine: gfx.Hex(0xffcf6b), Width: 1,
		Branches: []color.RGBA{gfx.Hex(0x5aa9f8), gfx.Hex(0xff9f5a), gfx.Hex(0x63d297), gfx.Hex(0xa98bfa),
			gfx.Hex(0xff6f7d), gfx.Hex(0x45d0c8), gfx.Hex(0xffd25e), gfx.Hex(0xf78cc4)}},
	{ID: "paper", Name: "Paper", Light: true, Paper: gfx.Hex(0xfbfaf7), Ink: gfx.Hex(0x23262e), InkDim: gfx.Hex(0x6b6f78),
		BoxFill: gfx.Hex(0xffffff), BoxLine: gfx.Hex(0xd3d6dc), RootFill: gfx.Hex(0x23262e), RootInk: gfx.Hex(0xffffff),
		Edge: gfx.Hex(0x8a8f99), Media: gfx.Hex(0xefeeea), WarnFill: gfx.Hex(0xfff2d4), WarnLine: gfx.Hex(0xc28400), Width: 1,
		Branches: []color.RGBA{gfx.Hex(0x2f80ed), gfx.Hex(0xf2994a), gfx.Hex(0x27ae60), gfx.Hex(0x9b51e0),
			gfx.Hex(0xeb5757), gfx.Hex(0x00a3a3), gfx.Hex(0xe2b007), gfx.Hex(0xe0559a)}},
	{ID: "pastel", Name: "Pastel", Light: true, Bubbles: true, Paper: gfx.Hex(0xfff8f1), Ink: gfx.Hex(0x3a3530),
		InkDim: gfx.Hex(0x8a8078), BoxFill: gfx.Hex(0xffffff), BoxLine: gfx.Hex(0xe8dccf), RootFill: gfx.Hex(0x3a3530),
		RootInk: gfx.Hex(0xfff8f1), Edge: gfx.Hex(0xb3a79b), Media: gfx.Hex(0xf6ede3), WarnFill: gfx.Hex(0xfff0cc),
		WarnLine: gfx.Hex(0xc28400), Width: 1.25,
		Branches: []color.RGBA{gfx.Hex(0x86bdfb), gfx.Hex(0xffb088), gfx.Hex(0x93dbac), gfx.Hex(0xc4b0ff),
			gfx.Hex(0xff98a6), gfx.Hex(0x84d9d1), gfx.Hex(0xffd27f), gfx.Hex(0xf5aed3)}},
	{ID: "ocean", Name: "Ocean", Paper: gfx.Hex(0x0e1726), Ink: gfx.Hex(0xe8f1ff), InkDim: gfx.Hex(0x8fa3bf),
		BoxFill: gfx.Hex(0x16233a), BoxLine: gfx.Hex(0x2a3c5c), RootFill: gfx.Hex(0xe8f1ff), RootInk: gfx.Hex(0x0e1726),
		Edge: gfx.Hex(0x7f93b2), Media: gfx.Hex(0x16233a), WarnFill: gfx.Hex(0x33301a), WarnLine: gfx.Hex(0xffd36b), Width: 1,
		Branches: []color.RGBA{gfx.Hex(0x4fc3f7), gfx.Hex(0x26c6da), gfx.Hex(0x7c8cff), gfx.Hex(0x4dd0a6),
			gfx.Hex(0xb39dff), gfx.Hex(0x81d4fa), gfx.Hex(0x64ffda), gfx.Hex(0x90a8f9)}},
	{ID: "sepia", Name: "Sepia", Light: true, Paper: gfx.Hex(0xf3ebdc), Ink: gfx.Hex(0x3e3227), InkDim: gfx.Hex(0x8c7a66),
		BoxFill: gfx.Hex(0xfbf6ec), BoxLine: gfx.Hex(0xd8cbb4), RootFill: gfx.Hex(0x5b4636), RootInk: gfx.Hex(0xfbf6ec),
		Edge: gfx.Hex(0xa08c74), Media: gfx.Hex(0xeadfca), WarnFill: gfx.Hex(0xf8e7c0), WarnLine: gfx.Hex(0xa86c00), Width: 1.1,
		Branches: []color.RGBA{gfx.Hex(0xc0632b), gfx.Hex(0x7a8b3a), gfx.Hex(0x3e7c8c), gfx.Hex(0xa0522d),
			gfx.Hex(0x8e6c3a), gfx.Hex(0x5e7a5a), gfx.Hex(0xb5833a), gfx.Hex(0x8c4b6a)}},
	{ID: "neon", Name: "Neon", Bubbles: true, Paper: gfx.Hex(0x0b0b12), Ink: gfx.Hex(0xf5f5ff), InkDim: gfx.Hex(0x9a9ab5),
		BoxFill: gfx.Hex(0x161626), BoxLine: gfx.Hex(0x3a3a5c), RootFill: gfx.Hex(0xf5f5ff), RootInk: gfx.Hex(0x0b0b12),
		Edge: gfx.Hex(0x8a8ab0), Media: gfx.Hex(0x161626), WarnFill: gfx.Hex(0x2c2410), WarnLine: gfx.Hex(0xffc93c), Width: 0.9,
		Branches: []color.RGBA{gfx.Hex(0x00e5ff), gfx.Hex(0xff2e88), gfx.Hex(0xb4ff39), gfx.Hex(0xffb800),
			gfx.Hex(0x9d6bff), gfx.Hex(0x00ffa3), gfx.Hex(0xff6a00), gfx.Hex(0x4d7cff)}},
}

// ThemeByID is the theme named id, or the default for one not known (such as a theme a newer version made).
func ThemeByID(id string) *Theme {
	for _, t := range Themes {
		if t.ID == id {
			return t
		}
	}
	return Themes[0]
}

// branchWidth is how thick the line under a topic at depth is, in board units, in theme th; the branch to it
// tapers to it from its parent's.
func branchWidth(depth int, th *Theme) float64 {
	w := 1.75
	switch depth {
	case 0:
		w = 7
	case 1:
		w = 4.5
	case 2:
		w = 3
	case 3:
		w = 2.25
	}
	return w * th.Width
}

// look is how a node is drawn, from its own choices and its place.
type look struct {
	shape    Shape
	size     float32
	bold     bool
	fill     color.RGBA
	line     color.RGBA
	ink      color.RGBA
	lineW    float64
	padX     float64
	padY     float64
	maxTextW float64
	radius   float64 // the corners of a box, in board units
	shadow   bool    // it floats over the board: only a tree's root, the one bubble on it
}

// Text sizes, by place.
const (
	rootSize   = 24
	branchSize = 18
	leafSize   = 15
	freeSize   = 16
	cellSize   = 13.5
	maxTopicW  = 320 // a topic's words wrap at this width unless it was given one
)

// lookOf works out how n looks in theme th, at depth in its tree, its main branch being branch, its tree growing
// in direction dir. Words over a line suit trees that grow sideways; in an org chart, growing up or down, or in a
// theme of bubbles, a topic is a soft bubble of its branch's colour instead, which branches reach at its side.
func lookOf(n *Node, depth int, branch int, dir Layout, th *Theme) look {
	l := look{shape: n.Shape, bold: n.Bold, maxTextW: maxTopicW, radius: 10}
	bc := th.Branch(branch)
	bubbles := dir == LayoutDown || dir == LayoutUp || th.Bubbles
	if l.shape == ShapeAuto {
		switch {
		case n.Kind != Topic:
			l.shape = ShapeRounded
		case depth == 0, bubbles:
			l.shape = ShapeRounded
		default:
			l.shape = ShapeUnderline
		}
	}
	switch {
	case n.Kind != Topic:
		l.size = cellSize
		l.fill, l.ink, l.radius = th.Media, th.Ink, 10
		if depth > 0 {
			l.line, l.lineW = bc, 1.5
		}
	case n.Shape == ShapeAuto && depth == 0:
		l.size, l.bold = rootSize, true
		l.fill, l.ink, l.radius, l.shadow = th.RootFill, th.RootInk, 16, true
		l.padX, l.padY = 28, 16
	case n.Shape == ShapeAuto && bubbles:
		l.size = leafSize
		if depth == 1 {
			l.size = branchSize - 2
		}
		// A light theme's bubbles are pale tints with dark words; a dark one's deep tints ringed in the colour.
		share := 0.22
		if th.Light {
			share = 0.45
			if depth > 1 {
				share = 0.25
			}
		} else if th.Bubbles && depth == 1 {
			share = 0.3
		}
		l.fill, l.line, l.ink, l.lineW, l.radius = gfx.Mix(th.Paper, bc, float32(share)), bc, th.Ink, 1.5, 12
		if th.Light {
			l.line, l.lineW = color.RGBA{}, 0
		}
		l.padX, l.padY = 14, 8
	case l.shape == ShapeUnderline:
		l.size = leafSize
		if depth == 1 {
			l.size = branchSize
		}
		l.line, l.ink, l.lineW = bc, th.Ink, branchWidth(depth, th)
		l.padX, l.padY = 6, 7
	case l.shape == ShapeNone:
		l.size = freeSize
		l.ink = th.Ink
		l.padX, l.padY = 2, 2
	default:
		l.size = freeSize
		if depth > 0 {
			l.size = leafSize
		}
		l.fill, l.line, l.ink, l.lineW = th.BoxFill, th.BoxLine, th.Ink, 1.25
		if depth > 0 {
			l.line, l.lineW = bc, 1.75
		}
		l.padX, l.padY = 18, 11
		if l.shape == ShapeRect {
			l.radius = 3
		}
	}
	if n.Size > 0 {
		l.size = n.Size
	}
	if n.Fill.A != 0 {
		l.fill = n.Fill
	}
	if n.Line.A != 0 {
		l.line = n.Line
		if l.lineW == 0 {
			l.lineW = 1.5
		}
	}
	if n.Ink.A != 0 {
		l.ink = n.Ink
	}
	return l
}

// Measurer measures words for the layout: lines wrapped to a width, how wide a line is and how tall lines are, at a
// size in board units. It must be safe for use from several goroutines.
type Measurer interface {
	Wrap(text string, size float32, bold bool, width float64) []string
	Width(text string, size float32, bold bool) float64
	LineHeight(size float32) float64
}

// FontMeasurer measures with fonts, and is what the board is drawn with.
type FontMeasurer struct{ Fonts gfx.Fonts }

func (f FontMeasurer) font(bold bool) *gfx.Font {
	if bold && f.Fonts.Bold != nil {
		return f.Fonts.Bold
	}
	return f.Fonts.Regular
}

// Wrap breaks text into lines no wider than width.
func (f FontMeasurer) Wrap(text string, size float32, bold bool, width float64) []string {
	if text == "" {
		return nil
	}
	return f.font(bold).Face(size).Wrap(text, float32(width))
}

// Width is how wide a line is.
func (f FontMeasurer) Width(text string, size float32, bold bool) float64 {
	return float64(f.font(bold).Face(size).Measure(text))
}

// LineHeight is the distance from one line to the next.
func (f FontMeasurer) LineHeight(size float32) float64 {
	return float64(f.Fonts.Regular.Face(size).Height()) * 1.18
}

// SimpleMeasurer measures without fonts, every character a fixed share of the size wide (twice that for wide
// scripts); it is for tests and tools with no fonts loaded.
type SimpleMeasurer struct{}

func runeW(r rune, size float32) float64 {
	if r >= 0x2e80 {
		return float64(size)
	}
	return float64(size) * 0.55
}

func (SimpleMeasurer) Width(text string, size float32, bold bool) float64 {
	w := 0.0
	for _, r := range text {
		w += runeW(r, size)
	}
	return w
}

func (s SimpleMeasurer) Wrap(text string, size float32, bold bool, width float64) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		line, w := []rune{}, 0.0
		for _, r := range para {
			rw := runeW(r, size)
			if w+rw > width && len(line) > 0 {
				out = append(out, string(line))
				line, w = line[:0], 0
			}
			line, w = append(line, r), w+rw
		}
		out = append(out, string(line))
	}
	return out
}

func (SimpleMeasurer) LineHeight(size float32) float64 { return float64(size) * 1.3 }

// wrapCache keeps wrapped lines, which measuring a large map asks for again and again with the same words.
type wrapCache struct {
	sync.Mutex
	m map[wrapKey][]string
}

type wrapKey struct {
	text  string
	size  float32
	bold  bool
	width float64
}

// wrapLimit bounds the cache; it is emptied when full.
const wrapLimit = 1 << 16

func (c *wrapCache) wrap(ms Measurer, k wrapKey) []string {
	c.Lock()
	if v, ok := c.m[k]; ok {
		c.Unlock()
		return v
	}
	c.Unlock()
	v := ms.Wrap(k.text, k.size, k.bold, k.width)
	c.Lock()
	if c.m == nil || len(c.m) >= wrapLimit {
		c.m = map[wrapKey][]string{}
	}
	c.m[k] = v
	c.Unlock()
	return v
}
