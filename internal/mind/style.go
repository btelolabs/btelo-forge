package mind

import (
	"image/color"
	"strings"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The theme, after MindNode on macOS in its dark appearance: a plain dark board; a tree's root is a rounded bubble
// with a soft shadow; every other topic is its words resting on a line of its branch's colour, and the branch
// flows into that line, tapering from the parent to the child. Free nodes with a shape of their own are a
// flowchart's boxes, cards like the pictures and tables.

// Branch colours, one per main branch of a tree, in turn: bright but soft, as MindNode's.
var branchColors = []color.RGBA{
	gfx.Hex(0x5aa9f8), gfx.Hex(0xff9f5a), gfx.Hex(0x63d297), gfx.Hex(0xa98bfa),
	gfx.Hex(0xff6f7d), gfx.Hex(0x45d0c8), gfx.Hex(0xffd25e), gfx.Hex(0xf78cc4),
}

// The board's colours.
var (
	Paper      = gfx.Hex(0x1c1c1f) // the board
	inkLight   = gfx.Hex(0xf2f2f5)
	inkDim     = gfx.Hex(0xa1a1a8)
	boxFill    = gfx.Hex(0x2c2c31) // a flowchart box
	boxLine    = gfx.Hex(0x48484f)
	rootFill   = gfx.Hex(0xf2f2f5)
	rootInk    = gfx.Hex(0x1c1c1f)
	edgeColor  = gfx.Hex(0x8e8e96)
	mediaFrame = gfx.Hex(0x2c2c31)
	warnFill   = gfx.Hex(0x3a2f17)
	warnLine   = gfx.Hex(0xffcf6b)
)

// BranchColor is the colour of main branch i.
func BranchColor(i int) color.RGBA { return branchColors[i%len(branchColors)] }

// branchWidth is how thick the line under a topic at depth is, in board units; the branch to it tapers to it from
// its parent's.
func branchWidth(depth int) float64 {
	switch depth {
	case 0:
		return 7
	case 1:
		return 4.5
	case 2:
		return 3
	case 3:
		return 2.25
	}
	return 1.75
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
	shadow   bool    // it floats over the board
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

// lookOf works out how n looks at depth in its tree, its main branch being branch, its tree growing in direction
// dir. Words over a line suit trees that grow sideways; in an org chart, growing up or down, a topic is a soft
// bubble of its branch's colour instead, which branches can reach from above and below.
func lookOf(n *Node, depth int, branch int, dir Layout) look {
	l := look{shape: n.Shape, bold: n.Bold, maxTextW: maxTopicW, radius: 10}
	bc := BranchColor(branch)
	if l.shape == ShapeAuto {
		switch {
		case n.Kind != Topic:
			l.shape = ShapeRounded
		case depth == 0, dir == LayoutDown || dir == LayoutUp:
			l.shape = ShapeRounded
		default:
			l.shape = ShapeUnderline
		}
	}
	switch {
	case n.Kind != Topic:
		l.size = cellSize
		l.fill, l.ink, l.radius, l.shadow = mediaFrame, inkLight, 10, true
		if depth > 0 {
			l.line, l.lineW = bc, 1.5
		}
	case n.Shape == ShapeAuto && depth == 0:
		l.size, l.bold = rootSize, true
		l.fill, l.ink, l.radius, l.shadow = rootFill, rootInk, 16, true
		l.padX, l.padY = 28, 16
	case n.Shape == ShapeAuto && (dir == LayoutDown || dir == LayoutUp):
		l.size = leafSize
		if depth == 1 {
			l.size = branchSize - 2
		}
		l.fill, l.line, l.ink, l.lineW, l.radius = gfx.Mix(Paper, bc, 0.22), bc, inkLight, 1.5, 12
		l.padX, l.padY = 14, 8
	case l.shape == ShapeUnderline:
		l.size = leafSize
		if depth == 1 {
			l.size = branchSize
		}
		l.line, l.ink, l.lineW = bc, inkLight, branchWidth(depth)
		l.padX, l.padY = 6, 7
	case l.shape == ShapeNone:
		l.size = freeSize
		l.ink = inkLight
		l.padX, l.padY = 2, 2
	default:
		l.size = freeSize
		if depth > 0 {
			l.size = leafSize
		}
		l.fill, l.line, l.ink, l.lineW, l.shadow = boxFill, boxLine, inkLight, 1.25, true
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
