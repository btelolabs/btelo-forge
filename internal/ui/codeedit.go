package ui

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// codeView is what a code editor keeps between frames: where it is scrolled to and its undo history.
type codeView struct {
	scrollX, scrollY float32
	caret            int // caret of the last frame, to scroll it into view when it moves
	undo, redo       []codeSnap
	lastTyped        time.Time
}

type codeSnap struct {
	text          []rune
	caret, anchor int
}

const codeUndoLimit = 300

// remember records the text before an edit; runs of typing within a moment are one step.
func (v *codeView) remember(e *editor, typing bool) {
	if typing && time.Since(v.lastTyped) < 800*time.Millisecond && len(v.undo) > 0 {
		v.lastTyped = time.Now()
		return
	}
	if typing {
		v.lastTyped = time.Now()
	} else {
		v.lastTyped = time.Time{}
	}
	v.undo = append(v.undo, codeSnap{append([]rune(nil), e.text...), e.caret, e.anchor})
	if len(v.undo) > codeUndoLimit {
		v.undo = v.undo[1:]
	}
	v.redo = nil
}

func (v *codeView) step(e *editor, from, to *[]codeSnap) bool {
	if len(*from) == 0 {
		return false
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, codeSnap{append([]rune(nil), e.text...), e.caret, e.anchor})
	e.text, e.caret, e.anchor = s.text, s.caret, s.anchor
	v.lastTyped = time.Time{}
	return true
}

// reset forgets the history, for when the text is replaced from outside.
func (v *codeView) reset() { *v = codeView{} }

// codeBox edits *value as fixed-width code with line numbers; errLine (1-based, 0 for none) is marked. It returns
// true on every edit.
func (u *ctx) codeBox(id string, r gfx.Rect, value *string, v *codeView, errLine int) (changed bool) {
	f := u.mono(12.5)
	lineH := float32(int(f.Height()*1.3 + 0.5))
	pad := u.px(8)
	focused := u.focus == id
	u.focusDrawn = u.focusDrawn || focused
	text := *value
	if focused {
		text = string(u.edit.text)
	}
	lines := strings.Split(text, "\n")
	digits := len(strconv.Itoa(len(lines)))
	gutter := f.Measure(strings.Repeat("0", max(digits, 2))) + 2*pad
	area := gfx.Rect{X: r.X + gutter, Y: r.Y + pad/2, W: r.W - gutter - u.px(10), H: r.H - pad}
	hot := u.hover(id, r)
	if hot && u.mx >= area.X {
		u.cursor = CursorText
	}
	pos := func(x, y float32) int {
		line := min(max(int((y-area.Y+v.scrollY)/lineH), 0), len(lines)-1)
		index := 0
		for _, l := range lines[:line] {
			index += len([]rune(l)) + 1
		}
		rs := []rune(lines[line])
		dx := x - area.X - pad + v.scrollX
		col := len(rs)
		for i := range rs {
			if dx < (f.Measure(string(rs[:i]))+f.Measure(string(rs[:i+1])))/2 {
				col = i
				break
			}
		}
		return index + col
	}
	if u.press(id, r) {
		if !focused {
			u.focus, u.focusDrawn, focused = id, true, true
			u.edit = editor{text: []rune(*value), multiline: true}
		}
		u.edit.caret = pos(u.pressX, u.pressY)
		if !u.shift {
			u.edit.anchor = u.edit.caret
		}
		if u.double {
			u.edit.anchor, u.edit.caret = wordAround(u.edit.text, u.edit.caret)
		}
		u.caretBlink = time.Now()
	}
	if u.dragging(id) && u.drag.moved && focused {
		u.edit.caret = pos(u.mx, u.my)
		u.caretBlink = time.Now()
	}
	u.letGo(id)
	if focused && u.pressed && !r.Contains(u.pressX, u.pressY) {
		u.focus, focused = "", false
	}
	if focused {
		e := &u.edit
		keys := u.keys
		u.keys = nil
		for _, k := range keys {
			before := string(e.text)
			switch {
			case k.Code == keyEscape:
				u.focus, focused = "", false
			case k.Ctrl && k.Code == 'Z' && !k.Shift, k.Ctrl && k.Code == 'Y', k.Ctrl && k.Shift && k.Code == 'Z':
				if k.Code == 'Y' || k.Shift {
					v.step(e, &v.redo, &v.undo)
				} else {
					v.step(e, &v.undo, &v.redo)
				}
			case k.Ctrl && (k.Code == 'C' || k.Code == 'X'):
				if a, b := e.selection(); a < b && u.host != nil {
					u.host.SetClipboard(string(e.text[a:b]))
					if k.Code == 'X' {
						v.remember(e, false)
						e.insert("")
					}
				}
			case k.Code == keyTab && !k.Shift:
				v.remember(e, false)
				e.insert("  ")
			case k.Code == keyEnter:
				v.remember(e, false)
				start := e.lineStart(min(e.caret, e.anchor))
				indent := 0
				for start+indent < len(e.text) && e.text[start+indent] == ' ' {
					indent++
				}
				e.insert("\n" + strings.Repeat(" ", indent))
			case k.Code == keyUp, k.Code == keyDown, k.Code == keyPageUp, k.Code == keyPageDn:
				n := map[uint16]int{keyUp: -1, keyDown: 1, keyPageUp: -int(area.H / lineH), keyPageDn: int(area.H / lineH)}[k.Code]
				e.caret = e.moveLines(e.caret, n, f)
				if !k.Shift {
					e.anchor = e.caret
				}
			case k.Ctrl && (k.Code == keyHome || k.Code == keyEnd):
				e.caret = 0
				if k.Code == keyEnd {
					e.caret = len(e.text)
				}
				if !k.Shift {
					e.anchor = e.caret
				}
			case k.Code == keyBack || k.Code == keyDelete || (k.Ctrl && k.Code == 'V'):
				v.remember(e, false)
				e.key(k, u.paste)
			default:
				e.key(k, u.paste)
			}
			if string(e.text) != before {
				changed = true
			}
			u.caretBlink = time.Now()
		}
		if focused && len(u.typed) > 0 {
			v.remember(e, true)
			e.insert(string(u.typed))
			u.typed = nil
			changed = true
			u.caretBlink = time.Now()
		}
		if changed {
			*value = string(e.text)
			text = *value
			lines = strings.Split(text, "\n")
		}
	}
	content := float32(len(lines)) * lineH
	if hot && u.wheel != 0 {
		if u.shift {
			v.scrollX -= u.wheel * f.Measure("0000")
		} else {
			v.scrollY -= u.wheel * lineH * 3
		}
		u.wheel = 0
	}
	caretLine, caretCol := 0, 0
	if focused {
		caretLine, caretCol = position(u.edit.text, u.edit.caret)
		if u.edit.caret != v.caret {
			cy := float32(caretLine) * lineH
			v.scrollY = min(max(v.scrollY, cy+lineH-area.H), cy)
			cx := f.Measure(string([]rune(lines[caretLine])[:caretCol]))
			v.scrollX = min(max(v.scrollX, cx+u.px(24)-area.W+2*pad), max(cx-u.px(24), 0))
			v.caret = u.edit.caret
		}
	}
	v.scrollY = min(max(v.scrollY, 0), max(content-area.H, 0))
	v.scrollX = max(v.scrollX, 0)

	u.c.RoundRect(r, u.px(6), gfx.Mix(colSurface, colBlack, 0.3))
	border := colBorder
	if focused {
		border = colAccent
	} else if hot {
		border = colFaint
	}
	u.c.RoundOutline(r, u.px(6), u.px(1.2), border)
	u.c.Push(r.Inset(u.px(1)))
	defer u.c.Pop()
	u.c.Fill(gfx.Rect{X: r.X + gutter - u.px(1), Y: r.Y, W: max(1, u.px(1)), H: r.H}, colBorder)
	a, b := 0, 0
	if focused {
		a, b = u.edit.selection()
	}
	first := int(v.scrollY / lineH)
	offset := 0
	for _, l := range lines[:min(first, len(lines))] {
		offset += len([]rune(l)) + 1
	}
	for i := first; i < len(lines); i++ {
		y := area.Y + float32(i)*lineH - v.scrollY
		if y > area.Bottom() {
			break
		}
		rs := []rune(lines[i])
		ty := y + (lineH-f.Height())/2
		numCol := colFaint
		if i+1 == errLine {
			u.c.Fill(gfx.Rect{X: r.X, Y: y, W: r.W, H: lineH}, gfx.Alpha(gfx.Hex(0xff6b68), 0.16))
			numCol = gfx.Hex(0xffb4b2)
		} else if focused && i == caretLine {
			u.c.Fill(gfx.Rect{X: area.X, Y: y, W: area.W + u.px(10), H: lineH}, gfx.Alpha(colSurfaceHi, 0.45))
		}
		num := strconv.Itoa(i + 1)
		u.c.Text(f, r.X+gutter-pad-f.Measure(num), ty, num, numCol)
		u.c.Push(gfx.Rect{X: area.X, Y: r.Y, W: r.Right() - area.X, H: r.H})
		x := area.X + pad - v.scrollX
		if a != b {
			sa, sb := min(max(a-offset, 0), len(rs)), min(max(b-offset, 0), len(rs)+1)
			if sa < sb {
				x0 := x + f.Measure(string(rs[:sa]))
				x1 := x + f.Measure(string(rs[:min(sb, len(rs))]))
				if sb > len(rs) {
					x1 += f.Measure(" ")
				}
				u.c.Fill(gfx.Rect{X: x0, Y: y, W: max(x1-x0, u.px(2)), H: lineH}, gfx.Alpha(colAccent, 0.4))
			}
		}
		u.drawCode(f, x, ty, rs)
		if focused && i == caretLine && time.Since(u.caretBlink)%(1060*time.Millisecond) < 530*time.Millisecond {
			cx := x + f.Measure(string(rs[:caretCol]))
			u.c.Fill(gfx.Rect{X: cx, Y: y + u.px(2), W: max(1, u.px(1.5)), H: lineH - u.px(4)}, colText)
		}
		if focused && i == caretLine {
			u.imeAt = [3]float32{x + f.Measure(string(rs[:caretCol])), y, lineH}
			u.blinking = true
		}
		u.c.Pop()
		offset += len(rs) + 1
	}
	if content > area.H {
		h := max(area.H*area.H/content, u.px(24))
		y := area.Y + (area.H-h)*v.scrollY/(content-area.H)
		u.c.RoundRect(gfx.Rect{X: r.Right() - u.px(6), Y: y, W: u.px(3), H: h}, u.px(1.5), colSurfaceHi)
	}
	return changed
}

// drawCode draws one line of JSON with strings, numbers and keywords coloured.
func (u *ctx) drawCode(f *gfx.Face, x, y float32, rs []rune) {
	str, num, word := gfx.Hex(0xb5e2a8), gfx.Hex(0xf2c879), gfx.Hex(0x8fc7ff)
	i := 0
	for i < len(rs) {
		j, col := i+1, colText
		switch c := rs[i]; {
		case c == '"':
			for j < len(rs) && rs[j] != '"' {
				if rs[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(rs))
			col = str
			k := j
			for k < len(rs) && rs[k] == ' ' {
				k++
			}
			if k < len(rs) && rs[k] == ':' {
				col = word
			}
		case c == '-' || unicode.IsDigit(c):
			for j < len(rs) && (unicode.IsDigit(rs[j]) || strings.ContainsRune(".eE+-", rs[j])) {
				j++
			}
			col = num
		case unicode.IsLetter(c):
			for j < len(rs) && unicode.IsLetter(rs[j]) {
				j++
			}
			col = num
		case strings.ContainsRune("{}[],:", c):
			col = colDim
		}
		s := string(rs[i:j])
		u.c.Text(f, x, y, s, col)
		x += f.Measure(s)
		i = j
	}
}

// moveLines moves the caret n lines up or down, keeping its horizontal position.
func (e *editor) moveLines(caret, n int, f *gfx.Face) int {
	start := e.lineStart(caret)
	x := f.Measure(string(e.text[start:caret]))
	for ; n < 0 && start > 0; n++ {
		start = e.lineStart(start - 1)
	}
	for ; n > 0; n-- {
		end := e.lineEnd(start)
		if end >= len(e.text) {
			break
		}
		start = end + 1
	}
	end := e.lineEnd(start)
	for i := start; i < end; i++ {
		if f.Measure(string(e.text[start:i+1])) > x+0.5 {
			if f.Measure(string(e.text[start:i+1]))-x < x-f.Measure(string(e.text[start:i])) {
				return i + 1
			}
			return i
		}
	}
	return end
}

// wordAround returns the bounds of the word at i.
func wordAround(text []rune, i int) (int, int) {
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' }
	a, b := i, i
	for a > 0 && isWord(text[a-1]) {
		a--
	}
	for b < len(text) && isWord(text[b]) {
		b++
	}
	return a, b
}
