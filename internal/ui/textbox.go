package ui

import (
	"strings"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// editor is the text and caret of the focused text box; anchor marks the other end of a selection.
type editor struct {
	text          []rune
	caret, anchor int
	multiline     bool
}

func (e *editor) selection() (int, int) { return min(e.caret, e.anchor), max(e.caret, e.anchor) }

func (e *editor) insert(s string) {
	if !e.multiline {
		s = strings.ReplaceAll(s, "\n", " ")
	}
	a, b := e.selection()
	rs := []rune(s)
	e.text = append(e.text[:a:a], append(rs, e.text[b:]...)...)
	e.caret = a + len(rs)
	e.anchor = e.caret
}

// key applies an editing key; it returns true when the key belongs to the editor.
func (e *editor) key(k Key, paste func() string) bool {
	a, b := e.selection()
	move := func(to int) {
		e.caret = min(max(to, 0), len(e.text))
		if !k.Shift {
			e.anchor = e.caret
		}
	}
	switch {
	case k.Code == keyBack:
		if a == b && a > 0 {
			a--
		}
		e.text = append(e.text[:a:a], e.text[b:]...)
		e.caret, e.anchor = a, a
	case k.Code == keyDelete:
		if a == b && b < len(e.text) {
			b++
		}
		e.text = append(e.text[:a:a], e.text[b:]...)
		e.caret, e.anchor = a, a
	case k.Code == keyLeft:
		if a != b && !k.Shift {
			move(a)
		} else {
			move(e.caret - 1)
		}
	case k.Code == keyRight:
		if a != b && !k.Shift {
			move(b)
		} else {
			move(e.caret + 1)
		}
	case k.Code == keyHome:
		move(e.lineStart(e.caret))
	case k.Code == keyEnd:
		move(e.lineEnd(e.caret))
	case k.Code == keyEnter && e.multiline && !k.Ctrl:
		e.insert("\n")
	case k.Ctrl && k.Code == 'A':
		e.anchor, e.caret = 0, len(e.text)
	case k.Ctrl && k.Code == 'V':
		if s := paste(); s != "" {
			e.insert(s)
		}
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

// textBox edits *value. It returns changed on every edit and done when focus leaves (Enter, Esc, Tab or a click outside).
func (u *ctx) textBox(id string, r gfx.Rect, value *string, placeholder string, multiline bool) (changed, done bool) {
	focused := u.focus == id
	u.focusDrawn = u.focusDrawn || focused
	hot := u.hover(id, r)
	if hot {
		u.cursor = CursorText
	}
	f := u.face(13.5)
	pad := u.px(10)
	lineH := f.Height() * 1.2
	if u.press(id, r) {
		if !focused {
			u.focus, u.focusDrawn = id, true // drawn this frame, so end does not take the focus straight back
			focused = true
			u.edit = editor{text: []rune(*value), multiline: multiline}
		}
		u.edit.caret = u.caretAt(f, r, pad, lineH)
		u.edit.anchor = u.edit.caret
		u.caretBlink = time.Now()
	}
	u.letGo(id)
	if focused && u.pressed && !r.Contains(u.pressX, u.pressY) {
		u.focus, focused, done = "", false, true
	}
	if focused {
		keys := u.keys
		u.keys = nil
		for _, k := range keys {
			if (k.Code == keyEnter && (!multiline || k.Ctrl)) || k.Code == keyEscape || k.Code == keyTab {
				u.focus, focused, done = "", false, true
				break
			}
			if u.edit.key(k, u.paste) {
				changed = true
				u.caretBlink = time.Now()
			}
		}
		if len(u.typed) > 0 {
			u.edit.insert(string(u.typed))
			u.typed = nil
			changed = true
			u.caretBlink = time.Now()
		}
		if changed {
			*value = string(u.edit.text)
		}
	}
	bg := colSurface
	if focused {
		bg = gfx.Mix(colSurface, colBlack, 0.25)
	}
	u.c.RoundRect(r, u.px(6), bg)
	border := colBorder
	if focused {
		border = colAccent
	} else if hot {
		border = colFaint
	}
	u.c.RoundOutline(r, u.px(6), u.px(1.2), border)
	u.c.Push(r.Inset(u.px(2)))
	defer u.c.Pop()
	text := *value
	if focused {
		text = string(u.edit.text)
	}
	if text == "" && !focused {
		u.c.Text(f, r.X+pad, u.lineTop(r, f, lineH, multiline), placeholder, colFaint)
		return changed, done
	}
	lines := strings.Split(text, "\n")
	top := u.lineTop(r, f, lineH, multiline)
	caretLine, caretCol := 0, 0
	if focused {
		caretLine, caretCol = position(u.edit.text, u.edit.caret)
		if overflow := top + float32(caretLine+1)*lineH - (r.Bottom() - pad/2); overflow > 0 {
			top -= overflow
		}
	}
	scroll := float32(0)
	if focused {
		cx := f.Measure(string([]rune(lines[caretLine])[:caretCol]))
		scroll = max(0, cx-(r.W-2*pad-u.px(4)))
	}
	a, b := 0, 0
	if focused {
		a, b = u.edit.selection()
	}
	offset := 0
	for i, line := range lines {
		y := top + float32(i)*lineH
		rs := []rune(line)
		if a != b {
			sa, sb := min(max(a-offset, 0), len(rs)), min(max(b-offset, 0), len(rs))
			if sa < sb {
				x0 := r.X + pad - scroll + f.Measure(string(rs[:sa]))
				x1 := r.X + pad - scroll + f.Measure(string(rs[:sb]))
				u.c.Fill(gfx.Rect{X: x0, Y: y, W: max(x1-x0, u.px(4)), H: f.Height()}, gfx.Alpha(colAccent, 0.45))
			}
		}
		u.c.Text(f, r.X+pad-scroll, y, line, colText)
		offset += len(rs) + 1
	}
	if focused {
		x := r.X + pad - scroll + f.Measure(string([]rune(lines[caretLine])[:caretCol]))
		y := top + float32(caretLine)*lineH
		if time.Since(u.caretBlink)%(1060*time.Millisecond) < 530*time.Millisecond {
			u.c.Fill(gfx.Rect{X: x, Y: y, W: max(1, u.px(1.5)), H: f.Height()}, colText)
		}
		u.imeAt = [3]float32{x, y, f.Height()}
		u.blinking = true
	}
	return changed, done
}

func (u *ctx) lineTop(r gfx.Rect, f *gfx.Face, lineH float32, multiline bool) float32 {
	if multiline {
		return r.Y + u.px(8)
	}
	return r.Y + (r.H-f.Height())/2
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

// caretAt maps the press position to a rune index of the editor text.
func (u *ctx) caretAt(f *gfx.Face, r gfx.Rect, pad, lineH float32) int {
	lines := strings.Split(string(u.edit.text), "\n")
	line := 0
	if u.edit.multiline {
		line = min(max(int((u.pressY-r.Y-u.px(8))/lineH), 0), len(lines)-1)
	}
	index := 0
	for _, l := range lines[:line] {
		index += len([]rune(l)) + 1
	}
	rs := []rune(lines[line])
	x := u.pressX - r.X - pad
	best := len(rs)
	for i := range rs {
		mid := (f.Measure(string(rs[:i])) + f.Measure(string(rs[:i+1]))) / 2
		if x < mid {
			best = i
			break
		}
	}
	return index + best
}

func (u *ctx) paste() string {
	if u.host == nil {
		return ""
	}
	s, err := u.host.Clipboard()
	if err != nil {
		return ""
	}
	return s
}
