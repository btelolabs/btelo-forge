package ui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Text input, one set of parts for every place words are typed: the form fields (textBox, inlineBox, secretBox),
// the code editor, the canvas's text tool and the mind map's words and cells.
//
//   - beginEdit is the only way editing starts: the editor is shared, so it records which box it belongs to, and a
//     box that finds itself focused with the editor another's starts again from its own words.
//   - editor.key gives every box the same keys: moving by character, word, line and the whole text, choosing with
//     Shift, deleting by character or word, copy, cut, paste, select all, and undo and redo with typing joined into
//     one step.
//   - editPointer gives the boxes the same mouse: a click puts the caret, Shift+click stretches the choice, a drag
//     chooses, a double-click chooses a word.
//   - drawCaret is the one caret: it blinks while the window has the keyboard focus, starts each blink again when
//     the caret moves or the words change, and puts the input method's windows at it.

// editor is the text and caret of the box being typed into; anchor marks the other end of a selection.
type editor struct {
	text          []rune
	caret, anchor int
	multiline     bool
	secret        bool // what is typed may not be copied out, as a stream key's
	rev           int  // counts the changes to the text, so a box can tell it was edited
	undo, redo    []editState
	typing        bool // the last change was typing, which more typing joins as one step to undo
}

// editState is the editor as one undo step left it.
type editState struct {
	text          []rune
	caret, anchor int
}

// editUndoDepth is how many steps of a box's editing can be undone.
const editUndoDepth = 200

// textClipboard is where copy, cut and paste go.
type textClipboard interface {
	clipText() string
	setClipText(string)
}

func (e *editor) selection() (int, int) { return min(e.caret, e.anchor), max(e.caret, e.anchor) }

// remember keeps the text as it is for undo, before a change; typing joins the typing just before it.
func (e *editor) remember(typing bool) {
	if typing && e.typing {
		return
	}
	e.undo = append(e.undo, editState{append([]rune(nil), e.text...), e.caret, e.anchor})
	if len(e.undo) > editUndoDepth {
		e.undo = e.undo[1:]
	}
	e.redo, e.typing = nil, typing
}

// step undoes (or redoes) one change, keeping the text as it is to come back to.
func (e *editor) step(from, to *[]editState) bool {
	if len(*from) == 0 {
		return false
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, editState{e.text, e.caret, e.anchor})
	e.text, e.caret, e.anchor, e.typing = s.text, s.caret, s.anchor, false
	e.rev++
	return true
}

// insert puts s in place of the selection, as one change of its own.
func (e *editor) insert(s string) {
	e.remember(false)
	e.replace(s)
}

// typeText puts typed words in place of the selection, joining the typing before it as one step to undo.
func (e *editor) typeText(s string) {
	e.remember(true)
	e.replace(s)
}

func (e *editor) replace(s string) {
	if !e.multiline {
		s = strings.ReplaceAll(s, "\n", " ")
	}
	a, b := e.selection()
	rs := []rune(s)
	e.text = append(e.text[:a:a], append(rs, e.text[b:]...)...)
	e.caret = a + len(rs)
	e.anchor = e.caret
	e.rev++
}

// remove deletes the text between a and b.
func (e *editor) remove(a, b int) {
	if a >= b {
		return
	}
	e.remember(false)
	e.text = append(e.text[:a:a], e.text[b:]...)
	e.caret, e.anchor = a, a
	e.rev++
}

// key applies an editing key; it returns true when the key belongs to the editor.
func (e *editor) key(k Key, clip textClipboard) bool {
	a, b := e.selection()
	move := func(to int) {
		e.caret = min(max(to, 0), len(e.text))
		if !k.Shift {
			e.anchor = e.caret
		}
	}
	switch {
	case k.Code == keyBack:
		switch {
		case a != b:
		case k.Ctrl:
			a = wordStart(e.text, a)
		case a > 0:
			a--
		}
		e.remove(a, b)
	case k.Code == keyDelete:
		switch {
		case a != b:
		case k.Ctrl:
			b = wordEnd(e.text, b)
		case b < len(e.text):
			b++
		}
		e.remove(a, b)
	case k.Code == keyLeft:
		switch {
		case k.Ctrl:
			move(wordStart(e.text, e.caret))
		case a != b && !k.Shift:
			move(a)
		default:
			move(e.caret - 1)
		}
	case k.Code == keyRight:
		switch {
		case k.Ctrl:
			move(wordEnd(e.text, e.caret))
		case a != b && !k.Shift:
			move(b)
		default:
			move(e.caret + 1)
		}
	case k.Code == keyHome && k.Ctrl:
		move(0)
	case k.Code == keyEnd && k.Ctrl:
		move(len(e.text))
	case k.Code == keyHome:
		move(e.lineStart(e.caret))
	case k.Code == keyEnd:
		move(e.lineEnd(e.caret))
	case k.Code == keyEnter && e.multiline && !k.Ctrl:
		e.insert("\n")
	case k.Ctrl && k.Code == 'A':
		e.anchor, e.caret = 0, len(e.text)
	case k.Ctrl && (k.Code == 'C' || k.Code == 'X'):
		if a < b && !e.secret {
			clip.setClipText(string(e.text[a:b]))
			if k.Code == 'X' {
				e.remove(a, b)
			}
		}
	case k.Ctrl && k.Code == 'V':
		if s := clip.clipText(); s != "" {
			e.insert(s)
		}
	case k.Ctrl && k.Code == 'Z' && !k.Shift:
		e.step(&e.undo, &e.redo)
	case k.Ctrl && (k.Code == 'Y' || k.Code == 'Z' && k.Shift):
		e.step(&e.redo, &e.undo)
	default:
		return false
	}
	return true
}

func (e *editor) lineStart(i int) int {
	for i > 0 && e.text[i-1] != '\n' {
		i--
	}
	return i
}

func (e *editor) lineEnd(i int) int {
	for i < len(e.text) && e.text[i] != '\n' {
		i++
	}
	return i
}

// isWordRune reports whether r is part of a word, for moving and deleting by words and choosing one.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

// wordStart is where the word before i starts, past the spaces before i: where Ctrl+Left goes.
func wordStart(text []rune, i int) int {
	for i > 0 && !isWordRune(text[i-1]) {
		i--
	}
	for i > 0 && isWordRune(text[i-1]) {
		i--
	}
	return i
}

// wordEnd is where the word after i ends, past the spaces after i: where Ctrl+Right goes.
func wordEnd(text []rune, i int) int {
	for i < len(text) && !isWordRune(text[i]) {
		i++
	}
	for i < len(text) && isWordRune(text[i]) {
		i++
	}
	return i
}

// wordAround returns the bounds of the word at i.
func wordAround(text []rune, i int) (int, int) {
	a, b := i, i
	for a > 0 && isWordRune(text[a-1]) {
		a--
	}
	for b < len(text) && isWordRune(text[b]) {
		b++
	}
	return a, b
}

// beginEdit starts typing into box id: it takes the keyboard, and the editor holds text with the selection from
// anchor to caret. Every box starts editing through it, so the editor never keeps another box's words.
func (u *ctx) beginEdit(id, text string, multiline bool, anchor, caret int) {
	u.setFocus(id)
	rs := []rune(text)
	u.edit = editor{text: rs, multiline: multiline, anchor: min(max(anchor, 0), len(rs)), caret: min(max(caret, 0), len(rs))}
	u.editOwner, u.editScroll = id, [2]float32{}
	u.caretBlink = time.Now()
}

// editing reports whether box id is being typed into: it has the keyboard and the editor holds its words.
func (u *ctx) editing(id string) bool { return u.focus == id && u.editOwner == id }

// focusEdit puts the caret in text box id before it is drawn, with all of text chosen, so typing replaces it.
func (u *ctx) focusEdit(id, text string, multiline bool) {
	u.beginEdit(id, text, multiline, 0, utf8.RuneCountInString(text))
}

// editPointer is the mouse on box id at r, whose words are text: a press puts the caret where pos says (starting
// to edit the box when it was not being edited), Shift+press stretches the choice to there, a double-click chooses
// the word, and a drag chooses from the press. It reports whether the box is being typed into.
func (u *ctx) editPointer(id string, r gfx.Rect, text string, multiline bool, pos func(x, y float32) int) bool {
	if u.press(id, r) {
		if !u.editing(id) {
			u.beginEdit(id, text, multiline, 0, 0)
		}
		at := pos(u.pressX, u.pressY)
		e := &u.edit
		switch {
		case u.double:
			e.anchor, e.caret = wordAround(e.text, at)
		case u.shift:
			e.caret = at
		default:
			e.caret, e.anchor = at, at
		}
		u.editDrag = !u.double
	}
	if u.editing(id) && u.editDrag && u.dragging(id) && u.drag.moved {
		u.edit.caret = pos(u.mx, u.my)
	}
	u.letGo(id)
	return u.editing(id)
}

// caretSeen is the editor as the caret was last drawn, so a move or an edit starts its blink again.
type caretSeen struct {
	owner              string
	caret, anchor, rev int
}

// caretBlinkPeriod is a whole blink of the caret, shown for the first half.
const caretBlinkPeriod = 1060 * time.Millisecond

// drawCaret draws the caret of the text being edited at (x, y), h high, and puts the input method's windows
// there. It blinks while the window has the keyboard focus, shown again whenever the caret moves or the words
// change; onCanvas draws it light with a dark edge, so it shows over any picture.
func (u *ctx) drawCaret(x, y, h float32, onCanvas bool) {
	seen := caretSeen{u.editOwner, u.edit.caret, u.edit.anchor, u.edit.rev}
	if seen != u.caretSeen {
		u.caretSeen, u.caretBlink = seen, time.Now()
	}
	if u.inactive || time.Since(u.caretBlink)%caretBlinkPeriod < caretBlinkPeriod/2 {
		w := max(1, u.px(1.5))
		if onCanvas {
			u.c.Fill(gfx.Rect{X: x - 1, Y: y, W: w + 2, H: h}, gfx.Alpha(colBlack, 0.5))
			u.c.Fill(gfx.Rect{X: x, Y: y, W: w, H: h}, colWhite)
		} else {
			u.c.Fill(gfx.Rect{X: x, Y: y, W: w, H: h}, colText)
		}
	}
	u.imeAt = [3]float32{x, y, h}
	u.blinking = true
}

// textBox edits *value. It returns changed on every edit and done when focus leaves (Enter, Esc, Tab or a click outside).
func (u *ctx) textBox(id string, r gfx.Rect, value *string, placeholder string, multiline bool) (changed, done bool) {
	return u.editBox(id, r, value, placeholder, multiline, false, boxStyle{})
}

// boxStyle is how a text box sets out its words: its type size and the space before them; zero values are a form
// field's, 13.5 with 10 pixels.
type boxStyle struct{ size, pad float32 }

// inlineBox edits *value in place of a label drawn in face size at r, as renaming a layer does: the words stay where
// the label had them, the box reaching pad before them. It returns as textBox does.
func (u *ctx) inlineBox(id string, r gfx.Rect, value *string, size, pad float32) (changed, done bool) {
	r.X, r.W = r.X-pad, r.W+pad
	return u.editBox(id, r, value, "", false, false, boxStyle{size: size, pad: pad})
}

// secretBox edits a secret, such as a stream key, on one line, showing a dot for each character; it cannot be
// copied out.
func (u *ctx) secretBox(id string, r gfx.Rect, value *string, placeholder string) (changed, done bool) {
	return u.editBox(id, r, value, placeholder, false, true, boxStyle{})
}

// editBox is a text box; masked shows a dot in place of each character.
func (u *ctx) editBox(id string, r gfx.Rect, value *string, placeholder string, multiline, masked bool, style boxStyle) (changed, done bool) {
	hot := u.hover(id, r)
	if hot {
		u.cursor = CursorText
	}
	if style.size == 0 {
		style = boxStyle{size: 13.5, pad: u.px(10)}
	}
	f := u.face(style.size)
	pad := style.pad
	lineH := f.Height() * 1.2
	shown := func(rs []rune) string {
		if masked {
			return strings.Repeat("•", len(rs))
		}
		return string(rs)
	}
	top := func() float32 {
		if multiline {
			return r.Y + u.px(8) - u.editScroll[1]
		}
		return r.Y + (r.H-f.Height())/2
	}
	pos := func(x, y float32) int {
		lines := strings.Split(shown(u.edit.text), "\n")
		line := 0
		if multiline {
			line = min(max(int((y-top())/lineH), 0), len(lines)-1)
		}
		index := 0
		for _, l := range lines[:line] {
			index += utf8.RuneCountInString(l) + 1
		}
		rs := []rune(lines[line])
		dx := x - r.X - pad + u.editScroll[0]
		for i := range rs {
			if dx < (f.Measure(string(rs[:i]))+f.Measure(string(rs[:i+1])))/2 {
				return index + i
			}
		}
		return index + len(rs)
	}
	focused := u.editPointer(id, r, *value, multiline, pos)
	if u.focus == id && !focused {
		// The keyboard was given to the box from outside without its words: it starts from them.
		u.focusEdit(id, *value, multiline)
		focused = true
	}
	u.focusDrawn = u.focusDrawn || focused
	if focused && u.pressed && !r.Contains(u.pressX, u.pressY) {
		u.focus, focused, done = "", false, true
	}
	if focused {
		e := &u.edit
		e.secret = masked
		rev := e.rev
		keys := u.keys
		u.keys = nil
		for _, k := range keys {
			switch {
			case (k.Code == keyEnter && (!multiline || k.Ctrl)) || k.Code == keyEscape || k.Code == keyTab:
				u.focus, focused, done = "", false, true
			case multiline && (k.Code == keyUp || k.Code == keyDown):
				n := 1
				if k.Code == keyUp {
					n = -1
				}
				e.caret = e.moveLines(e.caret, n, f)
				if !k.Shift {
					e.anchor = e.caret
				}
			default:
				e.key(k, u)
			}
			if !focused {
				break
			}
		}
		if focused && len(u.typed) > 0 {
			e.typeText(string(u.typed))
			u.typed = nil
		}
		if e.rev != rev {
			changed = true
			*value = string(e.text)
		}
	}
	bg := colSurface
	if focused {
		bg = colPanel
	}
	u.c.Fill(r, bg)
	border := colBorder
	if focused {
		border = colAccent
	} else if hot {
		border = colBorderHi
	}
	if focused {
		u.c.Outline(r.Inset(-u.px(2)), u.px(2), gfx.Alpha(colAccent, 0.25))
	}
	u.c.Outline(r, max(1, u.px(1)), border)
	u.c.Push(r.Inset(u.px(2)))
	defer u.c.Pop()
	if !focused {
		text := shown([]rune(*value))
		y := r.Y + (r.H-f.Height())/2
		if multiline {
			y = r.Y + u.px(8)
		}
		if text == "" {
			u.c.Text(f, r.X+pad, y, placeholder, colFaint)
			return changed, done
		}
		for i, line := range strings.Split(text, "\n") {
			u.c.Text(f, r.X+pad, y+float32(i)*lineH, line, colText)
		}
		return changed, done
	}
	// Scrolled so the caret shows: sideways on one line, up and down on several.
	lines := strings.Split(shown(u.edit.text), "\n")
	caretLine, caretCol := position(u.edit.text, u.edit.caret)
	cx := f.Measure(string([]rune(lines[caretLine])[:caretCol]))
	if room := r.W - 2*pad - u.px(4); !multiline {
		u.editScroll[0] = min(max(u.editScroll[0], cx-room), cx)
	}
	if multiline {
		room := r.H - u.px(8) - pad/2
		cy := float32(caretLine) * lineH
		u.editScroll[1] = max(min(max(u.editScroll[1], cy+lineH-room), cy), 0)
	}
	x0, y0 := r.X+pad-u.editScroll[0], top()
	a, b := u.edit.selection()
	offset := 0
	for i, line := range lines {
		y := y0 + float32(i)*lineH
		rs := []rune(line)
		if sa, sb := min(max(a-offset, 0), len(rs)), min(max(b-offset, 0), len(rs)); sa < sb {
			x1 := x0 + f.Measure(string(rs[:sa]))
			x2 := x0 + f.Measure(string(rs[:sb]))
			u.c.Fill(gfx.Rect{X: x1, Y: y, W: max(x2-x1, u.px(4)), H: f.Height()}, gfx.Alpha(colAccent, 0.45))
		}
		u.c.Text(f, x0, y, line, colText)
		offset += len(rs) + 1
	}
	u.drawCaret(x0+cx, y0+float32(caretLine)*lineH, f.Height(), false)
	return changed, done
}

func position(text []rune, caret int) (line, col int) {
	for _, c := range text[:caret] {
		if c == '\n' {
			line, col = line+1, 0
		} else {
			col++
		}
	}
	return line, col
}

func (u *ctx) clipText() string {
	if u.host == nil {
		return ""
	}
	s, err := u.host.Clipboard()
	if err != nil {
		return ""
	}
	return s
}

func (u *ctx) setClipText(s string) {
	if u.host != nil {
		u.host.SetClipboard(s)
	}
}
