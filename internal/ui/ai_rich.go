package ui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The text of an answer is Markdown as models write it: paragraphs, headings, lists, **bold**, `code` and fenced code
// blocks. It is laid out here once for a width and kept until the width or the words change.

type richStyle uint8

const (
	richPlain richStyle = iota
	richBold
	richCode
)

type richSpan struct {
	text  string
	style richStyle
}

// blockKind is a kind of line of an answer.
type blockKind uint8

const (
	blockText blockKind = iota
	blockHeading
	blockBullet
	blockCode
)

type aiBlock struct {
	kind   blockKind
	text   string // the line; for code, all its lines
	marker string // a bullet's mark: "•" or "1."
	level  int    // a heading's depth
	lang   string // a code block's language, as the fence names it
	gap    bool   // a blank line came before
}

// parseBlocks splits an answer into its lines of text, headings, bullets and code blocks. A code block still being
// written, whose closing fence has not come yet, runs to the end.
func parseBlocks(text string) []aiBlock {
	var blocks []aiBlock
	var code []string
	var lang string
	inCode, gap := false, false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				blocks = append(blocks, aiBlock{kind: blockCode, text: strings.Join(code, "\n"), lang: lang, gap: gap})
				code, inCode, gap = nil, false, false
			} else {
				inCode, lang = true, strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			continue
		}
		if inCode {
			code = append(code, strings.TrimRight(line, " \t"))
			continue
		}
		if trimmed == "" {
			gap = true
			continue
		}
		b := aiBlock{kind: blockText, text: trimmed, gap: gap}
		switch rest, level := heading(trimmed); {
		case level > 0:
			b.kind, b.text, b.level = blockHeading, rest, level
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "), strings.HasPrefix(trimmed, "• "):
			b.kind, b.marker, b.text = blockBullet, "•", strings.TrimSpace(trimmed[strings.IndexAny(trimmed, " ")+1:])
		default:
			if n := numberedPrefix(trimmed); n > 0 {
				b.kind, b.marker, b.text = blockBullet, trimmed[:n-1], strings.TrimSpace(trimmed[n:])
			}
		}
		blocks = append(blocks, b)
		gap = false
	}
	if inCode {
		blocks = append(blocks, aiBlock{kind: blockCode, text: strings.Join(code, "\n"), lang: lang, gap: gap})
	}
	return blocks
}

// heading is the words of a "# Title" line and how many marks it has, or 0.
func heading(s string) (string, int) {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || len(s) <= n || s[n] != ' ' {
		return s, 0
	}
	return strings.TrimSpace(s[n:]), n
}

// numberedPrefix is the length of a list item's "12. " start, or 0.
func numberedPrefix(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i > 3 || i+1 >= len(s) || (s[i] != '.' && s[i] != ')') || s[i+1] != ' ' {
		return 0
	}
	return i + 2
}

// parseInline splits a line into plain, **bold** and `code` runs. A mark that is not closed stays as it is.
func parseInline(s string) []richSpan {
	var spans []richSpan
	var cur strings.Builder
	flush := func(style richStyle) {
		if cur.Len() > 0 {
			spans = append(spans, richSpan{cur.String(), style})
			cur.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "**"):
			if end := strings.Index(s[i+2:], "**"); end > 0 {
				flush(richPlain)
				spans = append(spans, richSpan{s[i+2 : i+2+end], richBold})
				i += end + 4
				continue
			}
		case s[i] == '`':
			if end := strings.IndexByte(s[i+1:], '`'); end > 0 {
				flush(richPlain)
				spans = append(spans, richSpan{s[i+1 : i+1+end], richCode})
				i += end + 2
				continue
			}
		}
		cur.WriteByte(s[i])
		i++
	}
	flush(richPlain)
	return spans
}

// richLine is one wrapped line of spans.
type richLine struct{ spans []richSpan }

// unit is what a line may be broken between: a word, a space, or a character of a script without spaces.
type unit struct {
	text  string
	style richStyle
	w     float32
	space bool
}

// breaksAfter reports whether a line may end after r without a space: after a character of Chinese, Japanese or Korean.
func breaksAfter(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		strings.ContainsRune("，。、；：！？）」』》”", r)
}

// wrapRich lays spans out in lines no wider than width; a word wider than a line is cut where it meets the edge.
func wrapRich(spans []richSpan, width float32, face func(richStyle) *gfx.Face) []richLine {
	var units []unit
	add := func(text string, style richStyle, space bool) {
		units = append(units, unit{text: text, style: style, w: face(style).Measure(text), space: space})
	}
	for _, sp := range spans {
		var word strings.Builder
		flushWord := func() {
			if word.Len() > 0 {
				add(word.String(), sp.style, false)
				word.Reset()
			}
		}
		for _, r := range sp.text {
			switch {
			case r == ' ' || r == '\t':
				flushWord()
				add(" ", sp.style, true)
			case breaksAfter(r):
				word.WriteRune(r)
				flushWord()
			default:
				word.WriteRune(r)
			}
		}
		flushWord()
	}
	var lines []richLine
	var cur []richSpan
	var x float32
	push := func(text string, style richStyle) {
		if n := len(cur); n > 0 && cur[n-1].style == style {
			cur[n-1].text += text
			return
		}
		cur = append(cur, richSpan{text, style})
	}
	end := func() {
		// A space at the end of a line is not shown.
		if n := len(cur); n > 0 {
			cur[n-1].text = strings.TrimRight(cur[n-1].text, " ")
			if cur[n-1].text == "" {
				cur = cur[:n-1]
			}
		}
		lines = append(lines, richLine{cur})
		cur, x = nil, 0
	}
	for _, un := range units {
		if un.space && x == 0 {
			continue
		}
		if x+un.w > width && x > 0 && !un.space {
			end()
		}
		if un.w > width && !un.space {
			// A word that cannot fit a line of its own is cut where it meets the edge.
			f := face(un.style)
			var piece strings.Builder
			for _, r := range un.text {
				if x+f.Measure(piece.String()+string(r)) > width && piece.Len() > 0 {
					push(piece.String(), un.style)
					piece.Reset()
					end()
				}
				piece.WriteRune(r)
			}
			push(piece.String(), un.style)
			x += f.Measure(piece.String())
			continue
		}
		push(un.text, un.style)
		x += un.w
	}
	if len(cur) > 0 || len(lines) == 0 {
		end()
	}
	return lines
}

// aiLaid is an answer's text laid out for a width.
type aiLaid struct {
	height float32
	items  []aiItem
}

type itemKind uint8

const (
	itemText itemKind = iota
	itemMarker
	itemCodeBox
	itemCodeLine
	itemCodeHead
)

// aiItem is one thing to draw: positions are from the top left of the text.
type aiItem struct {
	kind  itemKind
	x, y  float32
	w, h  float32
	spans []richSpan
	text  string
	size  float32 // the type size, for a heading
	bold  bool
}

const (
	aiTextSize = 14
	aiCodeSize = 12.5
)

// laidText lays an answer out for a width, or returns what was laid out before.
func (a *App) laidText(text string, width float32) *aiLaid {
	u := &a.u
	key := fmt.Sprintf("%d|%d|%s", int(width), int(u.s*100), text)
	if l, ok := a.ai.laid[key]; ok {
		return l
	}
	if len(a.ai.laid) > 300 {
		a.ai.laid = map[string]*aiLaid{}
	}
	l := &aiLaid{}
	y := float32(0)
	body := u.face(aiTextSize)
	lineH := body.Height() * 1.45
	for i, b := range parseBlocks(text) {
		if i > 0 {
			if b.gap {
				y += u.px(10)
			} else {
				y += u.px(4)
			}
		}
		switch b.kind {
		case blockCode:
			y = a.layCode(l, b, y, width)
		case blockHeading:
			size := float32(aiTextSize+4) - float32(min(b.level, 3))*1.5
			f := u.bold(size)
			face := func(richStyle) *gfx.Face { return f }
			for _, line := range wrapRich(parseInline(b.text), width, face) {
				l.items = append(l.items, aiItem{kind: itemText, x: 0, y: y, spans: line.spans, size: size, bold: true})
				y += f.Height() * 1.4
			}
		default:
			indent := float32(0)
			if b.kind == blockBullet {
				indent = u.px(22)
				l.items = append(l.items, aiItem{kind: itemMarker, x: u.px(4), y: y, text: b.marker})
			}
			for _, line := range wrapRich(parseInline(b.text), width-indent, a.richFace) {
				l.items = append(l.items, aiItem{kind: itemText, x: indent, y: y, spans: line.spans, size: aiTextSize})
				y += lineH
			}
		}
	}
	l.height = y
	a.ai.laid[key] = l
	return l
}

// richFace is the type face of a style of text in an answer.
func (a *App) richFace(s richStyle) *gfx.Face {
	switch s {
	case richBold:
		return a.u.bold(aiTextSize)
	case richCode:
		return a.u.mono(aiCodeSize)
	}
	return a.u.face(aiTextSize)
}

// layCode lays a code block out: a header with its language and a copy button, then its lines, cut where they meet the
// right edge.
func (a *App) layCode(l *aiLaid, b aiBlock, y, width float32) float32 {
	u := &a.u
	f := u.mono(aiCodeSize)
	pad, headH := u.px(10), u.px(26)
	lineH := f.Height() * 1.35
	var rows []string
	room := width - 2*pad
	for _, line := range strings.Split(b.text, "\n") {
		if line == "" {
			rows = append(rows, "")
			continue
		}
		for line != "" {
			cut := len(line)
			for cut > 0 && f.Measure(line[:cut]) > room {
				cut-- // by bytes; a character is never split below, as the loop backs to a boundary
				for cut > 0 && !isRuneStart(line[cut]) {
					cut--
				}
			}
			if cut == 0 {
				_, cut = utf8.DecodeRuneInString(line) // a character wider than the block still takes a line
			}
			rows = append(rows, line[:cut])
			line = line[cut:]
		}
	}
	h := headH + float32(len(rows))*lineH + pad
	l.items = append(l.items,
		aiItem{kind: itemCodeBox, x: 0, y: y, w: width, h: h},
		aiItem{kind: itemCodeHead, x: 0, y: y, w: width, h: headH, text: b.text, spans: []richSpan{{b.lang, richPlain}}})
	for i, row := range rows {
		l.items = append(l.items, aiItem{kind: itemCodeLine, x: pad, y: y + headH + float32(i)*lineH, text: row})
	}
	return y + h
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
