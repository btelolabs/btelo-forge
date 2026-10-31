package docx

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Find and replace, the numbers in the word count, the outline, and the clipboard.

// Match is a place words were found.
type Match struct {
	B    *Block
	A, C int
}

// FindAll finds query in the body, in reading order. Words found do not cross paragraphs.
func (d *Doc) FindAll(query string, matchCase bool) []Match {
	return d.findIn(d.Body, query, matchCase)
}

func (d *Doc) findIn(st *Story, query string, matchCase bool) []Match {
	if query == "" {
		return nil
	}
	var out []Match
	needle := []rune(query)
	if !matchCase {
		needle = []rune(strings.ToLower(query))
	}
	for _, b := range st.flat {
		text := []rune(b.Text())
		if !matchCase {
			for i := range text {
				text[i] = unicode.ToLower(text[i])
			}
		}
		for i := 0; i+len(needle) <= len(text); {
			ok := true
			for k := range needle {
				if text[i+k] != needle[k] {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, Match{b, i, i + len(needle)})
				i += len(needle)
			} else {
				i++
			}
		}
	}
	return out
}

// lockedIn reports that a stretch has locked text in it.
func (p *Para) lockedIn(a, c int) bool {
	for i := range p.Segs {
		s := &p.Segs[i]
		if s.Locked && s.End() > a && s.Start < c {
			return true
		}
	}
	return false
}

// replaceIn puts text in place of places a to c, in the look of the first character replaced.
func (d *Doc) replaceIn(b *Block, a, c int, text string) {
	d.saveBlock(b)
	p := b.para()
	_, right := p.segAt(a)
	if right != nil && right.Kind == SegText && !right.Locked && text != "" {
		rs := []rune(right.S)
		k := a - right.Start
		setT(right.T, string(rs[:k])+text+string(rs[k:]))
		d.reread(b)
		n := utf8.RuneCountInString(text)
		d.deleteIn(b, a+n, c+n)
	} else {
		d.deleteIn(b, a, c)
		if text != "" {
			d.insertPlain(b, a, text)
		}
	}
	d.finish(b)
}

// ReplaceMatch replaces one match and selects what took its place.
func (d *Doc) ReplaceMatch(m Match, text string) bool {
	if m.B.para().lockedIn(m.A, m.C) || m.B.In.Story.ReadOnly {
		return false
	}
	text = cleanLine(text)
	d.Begin("")
	defer d.Commit()
	d.replaceIn(m.B, m.A, m.C, text)
	n := utf8.RuneCountInString(text)
	d.Sel = Selection{Anchor: Pos{B: m.B, Off: m.A}, Caret: Pos{B: m.B, Off: m.A + n}}
	return true
}

// ReplaceAll replaces every match in the body, as one step, and says how many it replaced.
func (d *Doc) ReplaceAll(query, text string, matchCase bool) int {
	ms := d.FindAll(query, matchCase)
	if len(ms) == 0 {
		return 0
	}
	text = cleanLine(text)
	d.Begin("")
	defer d.Commit()
	n := 0
	// Last first, so that earlier offsets hold.
	for i := len(ms) - 1; i >= 0; i-- {
		m := ms[i]
		if m.B.para().lockedIn(m.A, m.C) {
			continue
		}
		d.replaceIn(m.B, m.A, m.C, text)
		n++
	}
	return n
}

// Counts are the numbers of the word count.
type Counts struct {
	Words, Chars, NoSpaces, Paragraphs, Pages int
}

// Count counts the words: a Latin word is one, as is each Chinese, Japanese or Korean character, the way Word does.
func (d *Doc) Count() Counts {
	var c Counts
	for _, b := range d.Body.flat {
		text := b.Text()
		if text != "" {
			c.Paragraphs++
		}
		inWord := false
		for _, r := range text {
			if r == 0xfffc {
				continue
			}
			c.Chars++
			if !unicode.IsSpace(r) {
				c.NoSpaces++
			}
			switch {
			case eastAsian(r) && unicode.IsLetter(r):
				c.Words++
				inWord = false
			case unicode.IsSpace(r):
				inWord = false
			case !inWord:
				c.Words++
				inWord = true
			}
		}
	}
	c.Pages = len(d.lay.pages)
	return c
}

// Heading is a line of the outline.
type Heading struct {
	B     *Block
	Level int // 0 for the top
	Text  string
}

// Outline lists the headings in order.
func (d *Doc) Outline() []Heading {
	var out []Heading
	for _, b := range d.Body.flat {
		p := b.para()
		if p.PP.Outline < 0 || b.In.Cell != nil {
			continue
		}
		text := strings.TrimSpace(strings.ReplaceAll(b.Text(), "\n", " "))
		if text == "" {
			continue
		}
		if p.Label != "" {
			text = p.Label + " " + text
		}
		out = append(out, Heading{b, p.PP.Outline, text})
	}
	return out
}

// clipboard is what was copied here, kept with its formatting: a paste of the same words puts it back.
type clipboard struct {
	text   string
	blocks []*ox.Node // whole paragraphs and tables; the first and last hold only the copied part
}

// SelectedText is the words chosen, cells apart by tabs and paragraphs by new lines.
func (d *Doc) SelectedText() string {
	if d.Sel.Empty() {
		return ""
	}
	a, c := d.Sel.Range()
	st := a.B.In.Story
	var b strings.Builder
	for i := a.B.Flat; i <= c.B.Flat; i++ {
		block := st.flat[i]
		rs := []rune(block.Text())
		from, to := 0, len(rs)
		if block == a.B {
			from = a.Off
		}
		if block == c.B {
			to = c.Off
		}
		for _, r := range rs[from:to] {
			if r != 0xfffc {
				b.WriteRune(r)
			}
		}
		if i < c.B.Flat {
			next := st.flat[i+1]
			if block.In.Cell != nil && next.In.Cell != nil && block.In.Cell != next.In.Cell && block.In.Cell.Row == next.In.Cell.Row {
				b.WriteByte('\t')
			} else {
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// Copy keeps the selection with its formatting and returns its words for the system's clipboard.
func (d *Doc) Copy() string {
	text := d.SelectedText()
	if text == "" {
		return ""
	}
	a, c := d.Sel.Range()
	cl := &clipboard{text: text}
	if a.B == c.B {
		cl.blocks = []*ox.Node{d.cutClone(a.B, a.Off, c.Off)}
	} else if a.B.In == c.B.In {
		bs := a.B.In.Blocks
		for i := a.B.Index; i <= c.B.Index; i++ {
			b := bs[i]
			switch {
			case b == a.B:
				cl.blocks = append(cl.blocks, d.cutClone(b, a.Off, b.para().Len))
			case b == c.B:
				cl.blocks = append(cl.blocks, d.cutClone(b, 0, c.Off))
			default:
				n := b.Node.Clone()
				stripCopied(n)
				cl.blocks = append(cl.blocks, n)
			}
		}
	}
	d.clip = cl
	return text
}

// cutClone is a copy of a paragraph holding only the places from a to c. It works on a copy of the block: the real
// paragraph is not touched.
func (d *Doc) cutClone(b *Block, a, c int) *ox.Node {
	n := b.Node.Clone()
	tmp := &Block{Node: n, In: b.In, Flat: b.Flat}
	d.buildPara(tmp, nil)
	d.deleteIn(tmp, c, tmp.para().Len)
	d.deleteIn(tmp, 0, a)
	stripCopied(n)
	return n
}

// stripCopied takes from a copy what must be unique in a document, or belongs to the place it was copied from.
func stripCopied(n *ox.Node) {
	stripIDs(n)
	var marks []*ox.Node
	n.Walk(func(e *ox.Node) bool {
		switch e.Name {
		case "w:bookmarkStart", "w:bookmarkEnd", "w:commentRangeStart", "w:commentRangeEnd", "w:commentReference", "w:proofErr", "w:lastRenderedPageBreak":
			marks = append(marks, e)
		}
		return true
	})
	for _, e := range marks {
		e.Remove()
	}
}

// renumber gives pasted drawings ids of their own: a part must not have two the same.
func renumber(root, pasted *ox.Node) {
	id := nextDrawingID(root)
	pasted.Walk(func(e *ox.Node) bool {
		if e.Name == "wp:docPr" {
			e.SetAttr("id", strconv.Itoa(id))
			id++
		}
		return true
	})
}

// pasteRich puts what was copied at the caret, and says whether the words match what is on the system's clipboard.
func (d *Doc) pasteRich(text string) bool {
	cl := d.clip
	if cl == nil || cl.text != text || len(cl.blocks) == 0 {
		return false
	}
	if !d.Sel.Empty() {
		d.deleteSelection()
	}
	c := d.Sel.Caret
	b := c.B
	st := b.In.Story
	if len(cl.blocks) == 1 && cl.blocks[0].Name == "w:p" {
		d.saveBlock(b)
		at := d.cutAt(b, c.Off)
		n := 0
		for _, k := range cl.blocks[0].Clone().Children {
			if k.Kind == ox.Element && k.Name == "w:pPr" {
				continue
			}
			renumber(st.Part.X.Root, k)
			b.Node.Insert(at+n, k)
			n++
		}
		d.finish(b)
		end := Pos{B: b, Off: c.Off + utf8.RuneCountInString(cl.text)}
		d.Sel = Selection{Anchor: end, Caret: end}
		return true
	}
	// Several blocks: the paragraph opens, the first and last join its two halves.
	d.splitParagraph()
	tail := d.Sel.Caret.B
	head := st.flat[max(tail.Flat-1, 0)]
	headNode, tailNode := head.Node, tail.Node
	parent := tailNode.Parent
	d.saveNode(st.Part, headNode)
	d.saveNode(st.Part, tailNode)
	blocks := cl.blocks
	first, last := 0, len(blocks)
	if blocks[0].Name == "w:p" {
		for _, k := range blocks[0].Clone().Children {
			if k.Kind != ox.Element || k.Name != "w:pPr" {
				renumber(st.Part.X.Root, k)
				headNode.Append(k)
			}
		}
		first = 1
	}
	if last > first && blocks[last-1].Name == "w:p" {
		last--
		i := 0
		if tailNode.Child("w:pPr") != nil {
			i = 1
		}
		for _, k := range blocks[last].Clone().Children {
			if k.Kind != ox.Element || k.Name != "w:pPr" {
				renumber(st.Part.X.Root, k)
				tailNode.Insert(i, k)
				i++
			}
		}
	}
	at := parent.Index(tailNode)
	for i := first; i < last; i++ {
		copied := blocks[i].Clone()
		renumber(st.Part.X.Root, copied)
		parent.Insert(at, copied)
		at++
	}
	d.structural(tail)
	d.rebuild()
	d.dirtyNodes(headNode, tailNode)
	d.lay.restart()
	if nb := st.byNode[tailNode]; nb != nil {
		at := Pos{B: nb, Off: 0}
		d.Sel = Selection{Anchor: at, Caret: at}
	}
	return true
}

// Paste puts words from the system's clipboard at the caret: with their formatting when they are what was last copied
// here.
func (d *Doc) Paste(text string) {
	if !d.editable() || text == "" {
		return
	}
	d.Begin("")
	defer d.Commit()
	if !d.pasteRich(text) {
		d.insertAtCaret(text)
	}
}
