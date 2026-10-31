package ui

import (
	"image"
	"image/color"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Cursor is the pointer shape the UI asks for.
type Cursor int

const (
	CursorArrow Cursor = iota
	CursorHand
	CursorText
	CursorResize
	CursorMove
	CursorCross
	CursorResizeCorner // a bottom-right corner, dragged to resize
)

// Key codes the UI reacts to (Windows virtual keys).
const (
	keyBack     = 0x08
	keyTab      = 0x09
	keyEnter    = 0x0d
	keyEscape   = 0x1b
	keySpace    = 0x20
	keyPageUp   = 0x21
	keyPageDn   = 0x22
	keyEnd      = 0x23
	keyHome     = 0x24
	keyLeft     = 0x25
	keyUp       = 0x26
	keyRight    = 0x27
	keyDown     = 0x28
	keyDelete   = 0x2e
	keyNum0     = 0x60 // the number pad's 0; its 1 to 9 follow
	keyNumPlus  = 0x6b
	keyNumMinus = 0x6d
	keyEquals   = 0xbb // = and +
	keyMinus    = 0xbd // - and _
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
	Middle bool
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
	Alt     bool
	Keys    []Key
	Up      []uint16 // the keys let go, by code, for keys that act while they are held
	Typed   []rune
	Dropped []string
	Opened  []string // files opened from outside, as from Explorer, while the app runs
	Hotkeys []int    // the hotkey actions pressed, system-wide
	// Inactive is set while the window does not have the keyboard focus; nothing then animates for the eye alone.
	Inactive bool
}

// acted reports whether the input carries anything the frame may act on, as opposed to only a pointer position.
func (in Input) acted() bool {
	return len(in.Pointer) > 0 || in.Wheel != 0 || len(in.Keys) > 0 || len(in.Up) > 0 || len(in.Typed) > 0 || len(in.Dropped) > 0 || len(in.Opened) > 0 || len(in.Hotkeys) > 0
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
	Languages() []string // the system's preferred languages as BCP 47 tags, most preferred first
}

// ctx is the per-frame immediate-mode state.
type ctx struct {
	c          *gfx.Canvas
	fonts      gfx.Fonts
	s          float32
	mx, my     float32
	down       bool
	pressed    bool
	pressX     float32
	pressY     float32
	double     bool
	rightAt    *Pointer
	rightDown  bool // the right button is held
	middleDown bool // the middle button is held
	wheel      float32
	shift      bool
	ctrl       bool
	alt        bool
	inactive   bool // the window does not have the keyboard focus
	keys       []Key
	held       []uint16 // the keys held down, by code, as their presses and releases came
	heldWas    []uint16 // those held as the frame began, before its presses and releases
	up         []uint16 // those let go this frame
	typed      []rune
	active     string
	focus      string
	cursor     Cursor
	blocked    bool
	drag       dragState
	host       Host
	lang       Language

	tip        tip
	tipSeen    bool
	edit       editor
	editOwner  string     // the box the editor holds the words of (beginEdit)
	editScroll [2]float32 // how far that box's words are scrolled, sideways and down
	editDrag   bool       // a press in that box chooses as it is dragged
	focusDrawn bool       // the focused text box was drawn this frame
	caretBlink time.Time
	caretSeen  caretSeen // the caret as last drawn, so moving it shows it again
	imeAt      [3]float32
	imeFont    float32 // the pixel size of the words at the caret, where a document says; zero goes by imeAt's height
	imeFamily  string  // and their typeface
	blinking   bool
}

// isHeld reports whether the key with code is held down.
func (u *ctx) isHeld(code uint16) bool { return slices.Contains(u.held, code) }

// repeated reports whether a press is the keyboard repeating a key held since before this frame.
func (u *ctx) repeated(k Key) bool {
	return slices.Contains(u.heldWas, k.Code) && !slices.Contains(u.up, k.Code)
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
		if p.Middle {
			u.middleDown = p.Down
			continue
		}
		if p.Right {
			u.rightDown = p.Down
		}
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
	u.wheel, u.ctrl, u.shift, u.alt, u.keys, u.typed, u.inactive = in.Wheel, in.Ctrl, in.Shift, in.Alt, in.Keys, in.Typed, in.Inactive
	u.heldWas, u.up = append(u.heldWas[:0], u.held...), in.Up
	for _, k := range in.Keys {
		if !slices.Contains(u.held, k.Code) {
			u.held = append(u.held, k.Code)
		}
	}
	for _, c := range in.Up {
		u.held = slices.DeleteFunc(u.held, func(h uint16) bool { return h == c })
	}
	if in.Inactive {
		u.held = u.held[:0] // keys let go in another window are never reported
	}
	u.cursor = CursorArrow
	u.imeFont, u.imeFamily = 0, ""
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

// setFocus gives the keyboard to text box id from outside it; it keeps the focus until the box is next drawn.
func (u *ctx) setFocus(id string) {
	u.focus, u.focusDrawn = id, true
}

// end frees a hold, and the keyboard focus, of a widget that was not drawn this frame.
func (u *ctx) end() {
	if u.active != "" && !u.down {
		u.active = ""
	}
	if !u.focusDrawn {
		u.focus = ""
	}
	u.focusDrawn = false
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
