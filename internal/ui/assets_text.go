package ui

import (
	"image/color"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Texts in the preview: Markdown laid out as a page (headings, lists, quotes, code blocks, tables, bold, inline
// code and links), code in a fixed-width font with line numbers and its words coloured, and plain text wrapped.
// The page is laid out once for a width and drawn a line at a time, only where it shows.

// textPage is a text laid out for one width.
type textPage struct {
	width, scale float32
	lines        []pageLine
	height       float32
	gutter       float32 // the line numbers' column of code
	scrollY      float32
	scrollX      float32
	wide         float32 // the widest line of code, which scrolls sideways
}

// pageLine is one line of the page: its pieces of text, and what is drawn behind them.
type pageLine struct {
	y, h  float32
	runs  []pageRun
	bg    color.RGBA // a code block's or a table's band
	bar   bool       // a quote's bar at the left
	rule  bool       // a horizontal rule
	num   int        // a line of code's number
	inset float32
}

type pageRun struct {
	x    float32
	text string
	face *gfx.Face
	col  color.RGBA
	box  bool // inline code, on a box of its own
}

// Colours of the text preview.
var (
	codeKeyword = gfx.Hex(0xc792ea)
	codeString  = gfx.Hex(0xc3e88d)
	codeNumber  = gfx.Hex(0xf78c6c)
	codeComment = gfx.Hex(0x6b7394)
	codeBand    = gfx.Hex(0x16171c)
	linkColour  = gfx.Hex(0x6ab0ff)
)

// drawTextPreview draws the previewed text in r, scrolled with the wheel (with Shift, sideways for code).
func (a *App) drawTextPreview(id string, r gfx.Rect) {
	u := &a.u
	p := &a.preview
	pg := &p.page
	pad := u.px(24)
	if p.kind == assets.Code {
		pad = u.px(10)
	}
	inner := gfx.Rect{X: r.X + pad, Y: r.Y + pad/2, W: r.W - 2*pad, H: r.H - pad}
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	if pg.width != inner.W || pg.scale != u.s {
		scroll := pg.scrollY
		*pg = a.layoutText(p.text, p.kind, filepath.Ext(p.path), inner.W)
		pg.scrollY = scroll
	}
	u.c.Fill(r, colPanel)
	if u.hover(id+"/text", r) && u.wheel != 0 {
		if u.shift && p.kind == assets.Code {
			pg.scrollX -= u.wheel * u.px(80)
		} else {
			pg.scrollY -= u.wheel * u.px(72)
		}
		u.wheel = 0
	}
	pg.scrollY = min(max(pg.scrollY, 0), max(pg.height-inner.H+pad, 0))
	pg.scrollX = min(max(pg.scrollX, 0), max(pg.wide-inner.W+pg.gutter, 0))
	u.c.Push(r)
	first := sort.Search(len(pg.lines), func(i int) bool { return pg.lines[i].y+pg.lines[i].h >= pg.scrollY })
	for _, l := range pg.lines[first:] {
		y := inner.Y + l.y - pg.scrollY
		if y > r.Bottom() {
			break
		}
		if l.bg.A != 0 {
			u.c.Fill(gfx.Rect{X: inner.X + l.inset - u.px(8), Y: y, W: inner.W - l.inset + u.px(16), H: l.h}, l.bg)
		}
		if l.bar {
			u.c.Fill(gfx.Rect{X: inner.X + l.inset - u.px(14), Y: y, W: u.px(3), H: l.h}, colBorderHi)
		}
		if l.rule {
			u.c.Fill(gfx.Rect{X: inner.X, Y: y + l.h/2, W: inner.W, H: max(1, u.px(1))}, colBorder)
		}
		dx := inner.X
		if p.kind == assets.Code {
			n := strconv.Itoa(l.num)
			f := u.mono(12)
			u.c.Text(f, inner.X+pg.gutter-u.px(14)-f.Measure(n), y+(l.h-f.Height())/2, n, colFaint)
			dx = inner.X + pg.gutter - pg.scrollX
			u.c.Push(gfx.Rect{X: inner.X + pg.gutter - u.px(6), Y: r.Y, W: r.Right() - inner.X - pg.gutter + u.px(6), H: r.H})
		}
		// The runs share one baseline, the line's tallest face centred in it, whatever their fonts.
		var ascent, height float32
		for _, run := range l.runs {
			ascent, height = max(ascent, run.face.Ascent), max(height, run.face.Height())
		}
		baseline := y + (l.h-height)/2 + ascent
		for _, run := range l.runs {
			ty := baseline - run.face.Ascent
			if run.box {
				w := run.face.Measure(run.text)
				u.c.Fill(gfx.Rect{X: dx + run.x - u.px(3), Y: ty - u.px(1), W: w + u.px(6), H: run.face.Height() + u.px(2)}, colSurfaceHi)
			}
			u.c.Text(run.face, dx+run.x, ty, run.text, run.col)
		}
		if p.kind == assets.Code {
			u.c.Pop()
		}
	}
	if p.truncated && len(pg.lines) > 0 {
		last := pg.lines[len(pg.lines)-1]
		y := inner.Y + last.y + last.h + u.px(12) - pg.scrollY
		u.label(u.face(12), gfx.Rect{X: inner.X, Y: y, W: inner.W, H: u.px(20)}, u.tf("Only the first %s is shown", assets.FormatSize(maxPreviewText)), colFaint, 0)
	}
	u.c.Pop()
	if view := inner.H + pad; pg.height > view {
		h := max(r.H*r.H/(pg.height+pad), u.px(24))
		y := r.Y + (r.H-h)*pg.scrollY/(pg.height-inner.H+pad)
		u.c.Fill(gfx.Rect{X: r.Right() - u.px(5), Y: y, W: u.px(3), H: h}, colSurfaceHi)
	}
}

// layoutText lays out text of kind for width.
func (a *App) layoutText(text string, kind assets.Kind, ext string, width float32) textPage {
	pg := textPage{width: width, scale: a.u.s}
	switch kind {
	case assets.Markdown:
		a.layoutMarkdown(&pg, text, width)
	case assets.Code:
		a.layoutCode(&pg, text, strings.ToLower(ext))
	default:
		a.layoutPlain(&pg, text, width)
	}
	return pg
}

func (a *App) layoutPlain(pg *textPage, text string, width float32) {
	u := &a.u
	f := u.face(13.5)
	lh := f.Height() * 1.45
	y := float32(0)
	for _, para := range strings.Split(text, "\n") {
		para = strings.ReplaceAll(para, "\t", "    ")
		wrapped := f.Wrap(para, width)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, l := range wrapped {
			pg.lines = append(pg.lines, pageLine{y: y, h: lh, runs: []pageRun{{text: l, face: f, col: colText}}})
			y += lh
		}
	}
	pg.height = y
}

// layoutCode lays out code a line to a line, coloured by the conventions of its language.
func (a *App) layoutCode(pg *textPage, text, ext string) {
	u := &a.u
	f := u.mono(12.5)
	lh := float32(int(f.Height()*1.4 + 0.5))
	lines := strings.Split(text, "\n")
	pg.gutter = f.Measure(strings.Repeat("0", max(len(strconv.Itoa(len(lines))), 2))) + u.px(26)
	lang := codeLanguage(ext)
	inBlock := false
	for i, line := range lines {
		line = strings.ReplaceAll(line, "\t", "    ")
		var runs []pageRun
		x := float32(0)
		for _, t := range highlight(line, lang, &inBlock) {
			runs = append(runs, pageRun{x: x, text: t.text, face: f, col: t.col})
			x += f.Measure(t.text)
		}
		pg.wide = max(pg.wide, x)
		pg.lines = append(pg.lines, pageLine{y: float32(i) * lh, h: lh, runs: runs, num: i + 1})
	}
	pg.height = float32(len(lines)) * lh
}

// language is how a kind of code writes its comments and which words it keeps.
type language struct {
	line         []string // line comment openers
	open, close  string   // a block comment's ends
	keywords     map[string]bool
	backquotes   bool // ` quotes strings
	caseInsensed bool // keywords in any case (SQL)
}

var commonKeywords = words(`break case catch class const continue default defer do else enum export extends false
final finally for func function go goto if implements import in interface let new nil null package private protected
public return static struct super switch this throw true try type typeof var void while yield async await from as
def elif except lambda pass raise with not and or is None True False self fn pub use mod impl match where mut ref
crate trait loop unsafe select chan map range fallthrough string int bool float64 int64 byte rune error any let
local then end elseif repeat until echo fi esac done foreach param begin process`)

var sqlKeywords = words(`select from where insert into values update set delete create table index view drop alter
add column primary key foreign references join left right inner outer on group by order having limit offset union
all distinct as and or not null is in like between case when then else end exists default constraint unique`)

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// codeLanguage picks the comment and keyword conventions for a file extension.
func codeLanguage(ext string) language {
	cLike := language{line: []string{"//"}, open: "/*", close: "*/", keywords: commonKeywords}
	switch ext {
	case ".py", ".rb", ".sh", ".bash", ".zsh", ".ps1", ".psm1", ".yaml", ".yml", ".toml", ".r", ".pl", ".conf",
		".dockerfile", ".gitignore", ".gitattributes", ".editorconfig", ".ex", ".exs", ".tf", ".mod", ".graphql", ".nim":
		return language{line: []string{"#"}, keywords: commonKeywords}
	case ".ini", ".cfg":
		return language{line: []string{";", "#"}}
	case ".sql":
		return language{line: []string{"--"}, open: "/*", close: "*/", keywords: sqlKeywords, caseInsensed: true}
	case ".lua", ".hs":
		return language{line: []string{"--"}, keywords: commonKeywords}
	case ".html", ".htm", ".xml", ".svg", ".vue", ".svelte":
		return language{open: "<!--", close: "-->"}
	case ".json":
		return language{keywords: words("true false null")}
	case ".bat", ".cmd":
		return language{line: []string{"REM ", "rem ", "::"}, keywords: commonKeywords}
	case ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx":
		cLike.backquotes = true
		return cLike
	case ".go":
		cLike.backquotes = true
		return cLike
	}
	return cLike
}

type codeToken struct {
	text string
	col  color.RGBA
}

// highlight splits a line of code into coloured tokens; inBlock carries an open block comment to the next line.
func highlight(line string, lang language, inBlock *bool) []codeToken {
	var out []codeToken
	emit := func(s string, col color.RGBA) {
		if s != "" {
			out = append(out, codeToken{s, col})
		}
	}
	i := 0
	for i < len(line) {
		rest := line[i:]
		if *inBlock {
			end := strings.Index(rest, lang.close)
			if end < 0 {
				emit(rest, codeComment)
				return out
			}
			emit(rest[:end+len(lang.close)], codeComment)
			i += end + len(lang.close)
			*inBlock = false
			continue
		}
		if lang.open != "" && strings.HasPrefix(rest, lang.open) {
			*inBlock = true
			emit(lang.open, codeComment)
			i += len(lang.open)
			continue
		}
		comment := false
		for _, c := range lang.line {
			if strings.HasPrefix(rest, c) {
				comment = true
			}
		}
		if comment {
			emit(rest, codeComment)
			return out
		}
		c := line[i]
		switch {
		case c == '"' || c == '\'' || c == '`' && lang.backquotes:
			j := i + 1
			for j < len(line) && line[j] != c {
				if line[j] == '\\' && c != '`' {
					j++
				}
				j++
			}
			j = min(j+1, len(line))
			emit(line[i:j], codeString)
			i = j
		case c >= '0' && c <= '9':
			j := i + 1
			for j < len(line) && (isWordByte(line[j]) || line[j] == '.') {
				j++
			}
			emit(line[i:j], codeNumber)
			i = j
		case isWordByte(c) || c >= utf8.RuneSelf:
			j := i
			for j < len(line) {
				r, n := utf8.DecodeRuneInString(line[j:])
				if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '$' {
					break
				}
				j += n
			}
			if j == i {
				_, n := utf8.DecodeRuneInString(line[i:])
				j = i + n
			}
			w := line[i:j]
			col := colText
			if lang.keywords[w] || lang.caseInsensed && lang.keywords[strings.ToLower(w)] {
				col = codeKeyword
			}
			emit(w, col)
			i = j
		default:
			j := i + 1
			for j < len(line) && !isWordByte(line[j]) && line[j] < utf8.RuneSelf && !strings.ContainsRune("\"'`/#-;<", rune(line[j])) && (line[j] < '0' || line[j] > '9') {
				j++
			}
			emit(line[i:j], colDim)
			i = j
		}
	}
	return out
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// mdKind is a kind of Markdown block.
type mdKind int

const (
	mdParagraph mdKind = iota
	mdHeading
	mdItem
	mdQuote
	mdCode
	mdRule
	mdTable
)

// mdBlock is a block of a Markdown document.
type mdBlock struct {
	kind   mdKind
	level  int    // a heading's level, a list item's depth
	marker string // a list item's bullet or number
	lang   string // a code block's language, from its fence
	text   string
	lines  []string // a code block's or table's lines
}

// parseMarkdown splits a document into its blocks.
func parseMarkdown(text string) []mdBlock {
	var out []mdBlock
	lines := strings.Split(text, "\n")
	para := func(kind mdKind, s string) {
		if n := len(out); n > 0 && out[n-1].kind == kind && (kind == mdParagraph || kind == mdQuote) {
			out[n-1].text += " " + s
			return
		}
		out = append(out, mdBlock{kind: kind, text: s})
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trim := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trim)
		switch {
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			fence := trim[:3]
			b := mdBlock{kind: mdCode, lang: strings.ToLower(strings.TrimSpace(trim[3:]))}
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimLeft(lines[i], " \t"), fence); i++ {
				b.lines = append(b.lines, strings.ReplaceAll(lines[i], "\t", "    "))
			}
			out = append(out, b)
		case trim == "":
			out = append(out, mdBlock{kind: -1}) // ends the paragraph
		case strings.HasPrefix(trim, "#"):
			level := len(trim) - len(strings.TrimLeft(trim, "#"))
			if level <= 6 && (len(trim) == level || trim[level] == ' ') {
				out = append(out, mdBlock{kind: mdHeading, level: level, text: strings.TrimSpace(strings.TrimRight(trim[level:], "# "))})
			} else {
				para(mdParagraph, trim)
			}
		case isRule(trim):
			out = append(out, mdBlock{kind: mdRule})
		case strings.HasPrefix(trim, ">"):
			para(mdQuote, strings.TrimSpace(strings.TrimLeft(trim, ">")))
		case strings.HasPrefix(trim, "|"):
			b := mdBlock{kind: mdTable}
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				b.lines = append(b.lines, strings.TrimSpace(lines[i]))
			}
			i--
			out = append(out, tableBlock(b))
		default:
			if marker, rest, ok := listItem(trim); ok {
				out = append(out, mdBlock{kind: mdItem, level: indent / 2, marker: marker, text: rest})
			} else if n := len(out); n > 0 && out[n-1].kind == mdItem && indent > 0 {
				out[n-1].text += " " + trim // a list item going on
			} else {
				para(mdParagraph, trim)
			}
		}
	}
	return slices.DeleteFunc(out, func(b mdBlock) bool { return b.kind < 0 })
}

func isRule(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 3 {
		return false
	}
	return strings.Trim(s, "-") == "" || strings.Trim(s, "*") == "" || strings.Trim(s, "_") == ""
}

// listItem reads a list item's marker: a bullet, a number, or a task box.
func listItem(s string) (marker, rest string, ok bool) {
	if len(s) >= 2 && strings.ContainsRune("-*+", rune(s[0])) && s[1] == ' ' {
		rest = strings.TrimSpace(s[2:])
		switch {
		case strings.HasPrefix(rest, "[ ] "):
			return "☐", rest[4:], true
		case strings.HasPrefix(rest, "[x] "), strings.HasPrefix(rest, "[X] "):
			return "☑", rest[4:], true
		}
		return "•", rest, true
	}
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j > 0 && j < 10 && j+1 < len(s) && (s[j] == '.' || s[j] == ')') && s[j+1] == ' ' {
		return s[:j+1], strings.TrimSpace(s[j+2:]), true
	}
	return "", "", false
}

// tableBlock lines a table's cells up in columns, without the line under the header.
func tableBlock(b mdBlock) mdBlock {
	var rows [][]string
	for _, l := range b.lines {
		cells := strings.Split(strings.Trim(l, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(inlinePlain(cells[i]))
		}
		if strings.Trim(strings.Join(cells, ""), "-: ") == "" {
			continue
		}
		rows = append(rows, cells)
	}
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], displayWidth(c))
		}
	}
	b.lines = b.lines[:0]
	for _, r := range rows {
		var sb strings.Builder
		for i, c := range r {
			sb.WriteString(c)
			if i < len(r)-1 {
				sb.WriteString(strings.Repeat(" ", widths[i]-displayWidth(c)+3))
			}
		}
		b.lines = append(b.lines, sb.String())
	}
	return b
}

// displayWidth counts a wide (CJK) character as two columns of a fixed-width font.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		n++
		if wideRune(r) {
			n++
		}
	}
	return n
}

func wideRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || r >= 0xff00 && r <= 0xffef || r >= 0x3000 && r <= 0x303f
}

// span is a piece of inline Markdown.
type span struct {
	text       string
	bold, code bool
	link       bool
}

// inline reads emphasis, inline code and links.
func inline(s string) []span {
	var out []span
	var cur strings.Builder
	bold := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, span{text: cur.String(), bold: bold})
			cur.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case s[i] == '\\' && i+1 < len(s) && strings.ContainsRune("\\`*_[]()#+-.!|>~", rune(s[i+1])):
			cur.WriteByte(s[i+1])
			i += 2
		case s[i] == '`':
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				cur.WriteByte('`')
				i++
				continue
			}
			flush()
			out = append(out, span{text: s[i+1 : i+1+end], code: true})
			i += end + 2
		case strings.HasPrefix(s[i:], "**") || strings.HasPrefix(s[i:], "__"):
			flush()
			bold = !bold
			i += 2
		case s[i] == '!' && i+1 < len(s) && s[i+1] == '[':
			if text, n, ok := linkAt(s[i+1:]); ok {
				flush()
				out = append(out, span{text: "[" + text + "]", link: true})
				i += 1 + n
				continue
			}
			cur.WriteByte('!')
			i++
		case s[i] == '[':
			if text, n, ok := linkAt(s[i:]); ok {
				flush()
				out = append(out, span{text: text, link: true, bold: bold})
				i += n
				continue
			}
			cur.WriteByte('[')
			i++
		case s[i] == '*' && i+1 < len(s) && s[i+1] != ' ' && strings.Contains(s[i+1:], "*"):
			i++ // emphasis: the font has no italics, so its marks are dropped
		default:
			cur.WriteByte(s[i])
			i++
		}
	}
	flush()
	return out
}

// linkAt reads [text](target) at the start of s: its text and how much of s it takes.
func linkAt(s string) (string, int, bool) {
	close := strings.IndexByte(s, ']')
	if close < 0 || close+1 >= len(s) || s[close+1] != '(' {
		return "", 0, false
	}
	end := strings.IndexByte(s[close+1:], ')')
	if end < 0 {
		return "", 0, false
	}
	return s[1:close], close + 1 + end + 1, true
}

// inlinePlain is inline Markdown as plain text.
func inlinePlain(s string) string {
	var sb strings.Builder
	for _, sp := range inline(s) {
		sb.WriteString(sp.text)
	}
	return sb.String()
}

// layoutMarkdown lays the blocks of a document out as a page.
func (a *App) layoutMarkdown(pg *textPage, text string, width float32) {
	u := &a.u
	y := float32(0)
	body := u.face(14)
	gap := body.Height() * 0.7
	for k, b := range parseMarkdown(text) {
		if k > 0 {
			y += gap
		}
		switch b.kind {
		case mdHeading:
			size := []float32{0, 24, 20, 17, 15.5, 14.5, 14}[b.level]
			if k > 0 {
				y += gap * 0.6
			}
			y = a.flow(pg, inline(b.text), u.bold(size), colText, 0, width, y, 1.3)
			if b.level <= 2 {
				pg.lines = append(pg.lines, pageLine{y: y, h: u.px(10), rule: true})
				y += u.px(10)
			}
		case mdParagraph:
			y = a.flow(pg, inline(b.text), body, colText, 0, width, y, 1.55)
		case mdQuote:
			from := len(pg.lines)
			y = a.flow(pg, inline(b.text), body, colDim, u.px(16), width, y, 1.55)
			for i := from; i < len(pg.lines); i++ {
				pg.lines[i].bar = true
			}
		case mdItem:
			inset := u.px(22) * float32(b.level+1)
			from := len(pg.lines)
			y = a.flow(pg, inline(b.text), body, colText, inset, width, y, 1.5)
			if from < len(pg.lines) {
				f := body
				pg.lines[from].runs = append(pg.lines[from].runs, pageRun{x: inset - u.px(16) - f.Measure(b.marker) + f.Measure("•"), text: b.marker, face: f, col: colDim})
			}
			y -= gap * 0.65 // list items sit close together
		case mdCode, mdTable:
			f := u.mono(12.5)
			lh := float32(int(f.Height()*1.45 + 0.5))
			pg.lines = append(pg.lines, pageLine{y: y, h: u.px(8), bg: codeBand})
			y += u.px(8)
			lang := codeLanguage("." + b.lang)
			inBlock := false
			for i, l := range b.lines {
				var runs []pageRun
				if b.kind == mdTable {
					col := colText
					if i == 0 {
						col = colDim // the header row
					}
					runs = []pageRun{{text: f.Ellipsize(l, width-u.px(8)), face: f, col: col}}
				} else {
					x := float32(0)
					for _, t := range highlight(l, lang, &inBlock) {
						if x > width {
							break
						}
						runs = append(runs, pageRun{x: x, text: t.text, face: f, col: t.col})
						x += f.Measure(t.text)
					}
				}
				pg.lines = append(pg.lines, pageLine{y: y, h: lh, bg: codeBand, runs: runs})
				y += lh
			}
			pg.lines = append(pg.lines, pageLine{y: y, h: u.px(8), bg: codeBand})
			y += u.px(8)
		case mdRule:
			pg.lines = append(pg.lines, pageLine{y: y, h: u.px(12), rule: true})
			y += u.px(12)
		}
	}
	pg.height = y
}

// flow wraps spans from inset to width starting at y, and returns the y under them.
func (a *App) flow(pg *textPage, spans []span, face *gfx.Face, col color.RGBA, inset, width, y, spacing float32) float32 {
	u := &a.u
	lh := face.Height() * spacing
	line := pageLine{y: y, h: lh, inset: inset}
	x := inset
	newLine := func() {
		pg.lines = append(pg.lines, line)
		y += lh
		line = pageLine{y: y, h: lh, inset: inset}
		x = inset
	}
	for _, sp := range spans {
		f, c := face, col
		switch {
		case sp.code:
			f = u.mono(12.5)
		case sp.bold:
			f = u.fonts.Bold.Face(face.Size) // the same size, in bold
		}
		if sp.link {
			c = linkColour
		}
		for _, w := range wrapWords(sp.text) {
			ww := f.Measure(w)
			if x+ww > width && x > inset && strings.TrimSpace(w) != "" {
				newLine()
			}
			if x == inset && strings.TrimSpace(w) == "" {
				continue // no space at the start of a line
			}
			if ww > width-inset { // a word longer than the line is cut where it must
				w = f.Ellipsize(w, width-inset)
				ww = f.Measure(w)
			}
			line.runs = append(line.runs, pageRun{x: x, text: w, face: f, col: c, box: sp.code})
			x += ww
		}
	}
	if len(line.runs) > 0 {
		newLine()
	}
	return y
}

// wrapWords splits text where a line may break: after spaces, and between wide characters.
func wrapWords(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		switch {
		case wideRune(r):
			if i > start {
				out = append(out, s[start:i])
			}
			out = append(out, string(r))
			start = i + utf8.RuneLen(r)
		case r == ' ':
			if i > start {
				out = append(out, s[start:i])
			}
			out = append(out, " ")
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
