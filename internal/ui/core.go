package ui

import (
	"image"
	"image/color"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// Cursor is the pointer shape the UI asks for.
type Cursor int

const (
	CursorArrow Cursor = iota
	CursorHand
	CursorText
	CursorResize
	CursorMove
)

// Key codes the UI reacts to (Windows virtual keys).
const (
	keyBack   = 0x08
	keyTab    = 0x09
	keyEnter  = 0x0d
	keyEscape = 0x1b
	keySpace  = 0x20
	keyPageUp = 0x21
	keyPageDn = 0x22
	keyEnd    = 0x23
	keyHome   = 0x24
	keyLeft   = 0x25
	keyUp     = 0x26
	keyRight  = 0x27
	keyDown   = 0x28
	keyDelete = 0x2e
)

// Key is one key press.
type Key struct {
	Code             uint16
	Ctrl, Shift, Alt bool
}

// Pointer is one mouse button transition.
type Pointer struct {
	X, Y   float32
	Right  bool
	Down   bool
	Double bool
}

// Input is everything that happened since the previous frame.
type Input struct {
	X, Y    float32
	Down    bool
	Pointer []Pointer
	Wheel   float32
	Ctrl    bool
	Shift   bool
	Keys    []Key
	Typed   []rune
	Dropped []string
}

// Host is what the UI needs from the window it runs in.
type Host interface {
	Wake()
	SetCursor(Cursor)
	SetTitle(string)
	TextInput(on bool, x, y, height float32)
	Clipboard() (string, error)
	SetClipboard(text string) error
	Paste() (img image.Image, files []string, text string)
	ChooseFolder(title string) string
	OpenFiles(title string, filter [][2]string, multiple bool) []string
	Reveal(path string) error
	Launch(path string) error
	LaunchURL(url string) error
}

// ctx is the per-frame immediate-mode state.
type ctx struct {
	c       *gfx.Canvas
	fonts   gfx.Fonts
	s       float32
	mx, my  float32
	down    bool
	pressed bool
	pressX  float32
	pressY  float32
	double  bool
	rightAt *Pointer
	wheel   float32
	shift   bool
	ctrl    bool
	keys    []Key
	typed   []rune
	active  string
	focus   string
	cursor  Cursor
	blocked bool
	drag    dragState
	host    Host
	lang    Language

	tip        tip
	tipSeen    bool
	edit       editor
	caretBlink time.Time
	imeAt      [3]float32
	blinking   bool
}

// dragState remembers where a drag began and what it started from.
type dragState struct {
	x, y   float32
	moved  bool
	values [4]float64
	id     int
}

func (u *ctx) begin(in Input) {
	u.mx, u.my, u.down = in.X, in.Y, in.Down
	u.pressed, u.double, u.rightAt = false, false, nil
	for i, p := range in.Pointer {
		switch {
		case p.Right && p.Down && u.rightAt == nil:
			u.rightAt = &in.Pointer[i]
		case !p.Right && p.Down && !u.pressed:
			u.pressed, u.pressX, u.pressY, u.double = true, p.X, p.Y, p.Double
		}
	}
	if u.pressed {
		u.drag = dragState{x: u.pressX, y: u.pressY}
	}
	u.wheel, u.ctrl, u.shift, u.keys, u.typed = in.Wheel, in.Ctrl, in.Shift, in.Keys, in.Typed
	u.cursor = CursorArrow
}

// px converts logical units to pixels.
func (u *ctx) px(v float32) float32 { return v * u.s }

// hover reports whether the mouse is over r and the UI is not held by another widget.
func (u *ctx) hover(id string, r gfx.Rect) bool {
	return !u.blocked && (u.active == "" || u.active == id) && r.Contains(u.mx, u.my)
}

// press claims a mouse press inside r for id.
func (u *ctx) press(id string, r gfx.Rect) bool {
	if u.blocked || !u.pressed || u.active != "" || !r.Contains(u.pressX, u.pressY) {
		return false
	}
	u.pressed = false
	u.active = id
	return true
}

// dragging reports whether id still holds the mouse, noting once the pointer has really moved.
func (u *ctx) dragging(id string) bool {
	if u.active != id || !u.down {
		return false
	}
	if !u.drag.moved {
		dx, dy := u.mx-u.drag.x, u.my-u.drag.y
		u.drag.moved = dx*dx+dy*dy > u.px(3)*u.px(3)
	}
	return true
}

// letGo reports once that id's hold ended, and frees the mouse.
func (u *ctx) letGo(id string) bool {
	if u.active != id || u.down {
		return false
	}
	u.active = ""
	return true
}

// clicked handles a full press-release on r.
func (u *ctx) clicked(id string, r gfx.Rect) bool {
	u.press(id, r)
	return u.letGo(id) && r.Contains(u.mx, u.my)
}

// end frees a hold whose widget was not drawn this frame.
func (u *ctx) end() {
	if u.active != "" && !u.down {
		u.active = ""
	}
}

func (u *ctx) face(size float32) *gfx.Face     { return u.fonts.Regular.Face(u.px(size)) }
func (u *ctx) bold(size float32) *gfx.Face     { return u.fonts.Bold.Face(u.px(size)) }
func (u *ctx) mono(size float32) *gfx.Face     { return u.fonts.Mono.Face(u.px(size)) }
func (u *ctx) iconFace(size float32) *gfx.Face { return u.fonts.Icons.Face(u.px(size)) }

// label draws text vertically centred in r, aligned left (align 0), centre (0.5) or right (1).
func (u *ctx) label(f *gfx.Face, r gfx.Rect, s string, col color.RGBA, align float32) {
	s = f.Ellipsize(s, r.W)
	w := f.Measure(s)
	u.c.Text(f, r.X+(r.W-w)*align, r.Y+(r.H-f.Height())/2, s, col)
}

// icon draws a symbol centred in r.
func (u *ctx) icon(glyph string, r gfx.Rect, size float32, col color.RGBA) {
	f := u.iconFace(size)
	w := f.Measure(glyph)
	u.c.Text(f, r.X+(r.W-w)/2, r.Y+(r.H-f.Height())/2, glyph, col)
}
