package docx

import (
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Numbering: lists are defined in numbering.xml as abstract definitions of nine levels, which w:num instances name,
// with overrides. Numbers are worked out by walking the paragraphs in order, because a label depends on all that
// came before it.

type numLevel struct {
	Fmt     string
	Text    string
	Start   int
	Restart int // the level that restarts this one, -1 for the one above, 0 for never
	Legal   bool
	Suff    string
	Jc      string
	Style   string
	PPr     *ox.Node
	RPr     *ox.Node
}

type absNum struct {
	id     string
	levels [9]*numLevel
}

type numDef struct {
	id        int
	abs       *absNum
	overrides [9]*numLevel
	starts    [9]int // the start overrides, -1 where there is none
}

// Numbering holds the definitions.
type Numbering struct {
	Part *Part
	abs  map[string]*absNum
	nums map[int]*numDef
}

func (d *Doc) loadNumbering() *Numbering {
	n := &Numbering{Part: d.optional(relNumbering)}
	n.read()
	return n
}

func (n *Numbering) read() {
	n.abs, n.nums = map[string]*absNum{}, map[int]*numDef{}
	if n.Part == nil {
		return
	}
	root := n.Part.X.Root
	for _, a := range root.Elements("w:abstractNum") {
		def := &absNum{id: a.Attr("w:abstractNumId")}
		for _, l := range a.Elements("w:lvl") {
			if i := atoi(l.Attr("w:ilvl")); i >= 0 && i < 9 {
				def.levels[i] = readLevel(l)
			}
		}
		n.abs[def.id] = def
	}
	for _, e := range root.Elements("w:num") {
		def := &numDef{id: atoi(e.Attr("w:numId")), abs: n.abs[e.Child("w:abstractNumId").Attr("w:val")]}
		for i := range def.starts {
			def.starts[i] = -1
		}
		for _, o := range e.Elements("w:lvlOverride") {
			i := atoi(o.Attr("w:ilvl"))
			if i < 0 || i >= 9 {
				continue
			}
			if s := o.Child("w:startOverride"); s != nil {
				def.starts[i] = atoi(s.Attr("w:val"))
			}
			if l := o.Child("w:lvl"); l != nil {
				def.overrides[i] = readLevel(l)
			}
		}
		if def.abs != nil {
			n.nums[def.id] = def
		}
	}
}

func readLevel(l *ox.Node) *numLevel {
	lvl := &numLevel{Fmt: "decimal", Start: 1, Restart: -1, Suff: "tab", PPr: l.Child("w:pPr"), RPr: l.Child("w:rPr")}
	if v, ok := l.Child("w:start").Lookup("w:val"); ok {
		lvl.Start = atoi(v)
	}
	if v, ok := l.Child("w:numFmt").Lookup("w:val"); ok {
		lvl.Fmt = v
	}
	if v, ok := l.Child("w:lvlRestart").Lookup("w:val"); ok {
		lvl.Restart = atoi(v)
	}
	if n := l.Child("w:isLgl"); n != nil {
		lvl.Legal = toggle(n)
	}
	if v, ok := l.Child("w:suff").Lookup("w:val"); ok {
		lvl.Suff = v
	}
	lvl.Text = l.Child("w:lvlText").Attr("w:val")
	lvl.Jc = l.Child("w:lvlJc").Attr("w:val")
	lvl.Style = l.Child("w:pStyle").Attr("w:val")
	return lvl
}

// level is the level of a list, nil when it has none.
func (n *Numbering) level(id, ilvl int) *numLevel {
	def := n.nums[id]
	if def == nil || ilvl < 0 || ilvl > 8 {
		return nil
	}
	if l := def.overrides[ilvl]; l != nil {
		return l
	}
	return def.abs.levels[ilvl]
}

// levelOfStyle is the level whose w:pStyle is the style.
func (n *Numbering) levelOfStyle(id int, style string) int {
	def := n.nums[id]
	if def == nil {
		return 0
	}
	for i, l := range def.abs.levels {
		if l != nil && l.Style == style {
			return i
		}
	}
	return 0
}

// numCounter counts one abstract list.
type numCounter struct {
	value   [9]int
	started [9]bool
	seen    map[int]bool // the w:num instances already met, whose start overrides have been used
}

// labels works out every numbered paragraph's label, in the order of the story.
func (d *Doc) labels() {
	counters := map[*absNum]*numCounter{}
	for _, b := range d.Body.flat {
		pd := b.para()
		pp := &pd.PP
		label := ""
		var lvl *numLevel
		if pp.NumID != 0 {
			if lvl = d.Num.level(pp.NumID, pp.Ilvl); lvl != nil {
				label = d.Num.next(counters, pp.NumID, pp.Ilvl)
			}
		}
		if label != pd.Label {
			pd.Label = label
			pd.laid = nil
			d.lay.dirty(b)
		}
	}
	for _, st := range d.stories {
		for _, b := range st.flat {
			pd := b.para()
			if pd.PP.NumID != 0 && pd.Label == "" {
				if d.Num.level(pd.PP.NumID, pd.PP.Ilvl) != nil {
					pd.Label = d.Num.next(counters, pd.PP.NumID, pd.PP.Ilvl)
					pd.laid = nil
				}
			}
		}
	}
}

// next counts one paragraph at a level of the list, and is its label.
func (n *Numbering) next(counters map[*absNum]*numCounter, id, ilvl int) string {
	def := n.nums[id]
	c := counters[def.abs]
	if c == nil {
		c = &numCounter{seen: map[int]bool{}}
		counters[def.abs] = c
	}
	if !c.seen[id] {
		c.seen[id] = true
		// A new instance with a start override starts its levels again.
		for i, s := range def.starts {
			if s >= 0 {
				c.value[i], c.started[i] = s-1, true
			}
		}
	}
	lvl := n.level(id, ilvl)
	if !c.started[ilvl] {
		c.value[ilvl], c.started[ilvl] = lvl.Start-1, true
	}
	c.value[ilvl]++
	// Levels below restart, as their own w:lvlRestart allows.
	for i := ilvl + 1; i < 9; i++ {
		if l := n.level(id, i); l != nil && l.Restart != 0 && (l.Restart == -1 || l.Restart > ilvl) {
			c.started[i] = false
		}
	}
	if lvl.Fmt == "none" {
		return ""
	}
	if lvl.Fmt == "bullet" {
		return bulletText(lvl.Text)
	}
	var b strings.Builder
	rs := []rune(lvl.Text)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '%' && i+1 < len(rs) && rs[i+1] >= '1' && rs[i+1] <= '9' {
			k := int(rs[i+1] - '1')
			i++
			at := n.level(id, k)
			if at == nil {
				continue
			}
			v := c.value[k]
			if !c.started[k] {
				v = at.Start
			}
			f := at.Fmt
			if lvl.Legal {
				f = "decimal"
			}
			b.WriteString(formatNumber(v, f))
			continue
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}

// bulletText maps what Symbol and Wingdings keep in the private use area to characters every font has.
func bulletText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 0xf0b7, 0xf0a0, 0xb7:
			b.WriteRune('•')
		case 0xf0a7, 0xf0a8, 0xf06e, 0xf0ae:
			b.WriteRune('▪')
		case 0xf0d8, 0xf0e0, 0xf0e8:
			b.WriteRune('➢')
		case 0xf076:
			b.WriteRune('❖')
		case 0xf0fc:
			b.WriteRune('✓')
		case 0xf0a2, 0xf06f:
			b.WriteRune('□')
		default:
			if r >= 0xf000 && r <= 0xf0ff {
				b.WriteRune('•')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

var (
	cnDigits   = []rune("〇一二三四五六七八九")
	cnLegal    = []rune("零壹贰叁肆伍陆柒捌玖")
	zodiac     = []rune("甲乙丙丁戊己庚辛壬癸")
	circled    = []rune("①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑮⑯⑰⑱⑲⑳")
	romanPairs = []struct {
		v int
		s string
	}{{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"}, {100, "c"}, {90, "xc"}, {50, "l"}, {40, "xl"}, {10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"}}
)

// formatNumber writes a list number in a w:numFmt.
func formatNumber(v int, f string) string {
	switch f {
	case "decimalZero":
		if v >= 0 && v < 10 {
			return "0" + strconv.Itoa(v)
		}
	case "upperRoman":
		return strings.ToUpper(roman(v))
	case "lowerRoman":
		return roman(v)
	case "upperLetter":
		return strings.ToUpper(letters(v))
	case "lowerLetter":
		return letters(v)
	case "ordinal":
		return ordinal(v)
	case "chineseCounting", "chineseCountingThousand", "taiwaneseCounting", "taiwaneseCountingThousand", "japaneseCounting":
		return chinese(v, cnDigits, "十百千")
	case "chineseLegalSimplified":
		return chinese(v, cnLegal, "拾佰仟")
	case "ideographDigital", "taiwaneseDigital", "japaneseDigitalTenThousand":
		var b strings.Builder
		for _, r := range strconv.Itoa(max(v, 0)) {
			b.WriteRune(cnDigits[r-'0'])
		}
		return b.String()
	case "ideographTraditional":
		if v >= 1 {
			return string(zodiac[(v-1)%len(zodiac)])
		}
	case "decimalEnclosedCircle", "decimalEnclosedCircleChinese":
		if v >= 1 && v <= len(circled) {
			return string(circled[v-1])
		}
	case "none":
		return ""
	}
	return strconv.Itoa(v)
}

func roman(v int) string {
	if v <= 0 || v > 39999 {
		return strconv.Itoa(v)
	}
	var b strings.Builder
	for _, p := range romanPairs {
		for v >= p.v {
			b.WriteString(p.s)
			v -= p.v
		}
	}
	return b.String()
}

// letters counts a, b ... z, aa, bb, as Word does.
func letters(v int) string {
	if v <= 0 {
		return strconv.Itoa(v)
	}
	return strings.Repeat(string(rune('a'+(v-1)%26)), min((v-1)/26+1, 64))
}

func ordinal(v int) string {
	s := "th"
	if v%100 < 11 || v%100 > 13 {
		switch v % 10 {
		case 1:
			s = "st"
		case 2:
			s = "nd"
		case 3:
			s = "rd"
		}
	}
	return strconv.Itoa(v) + s
}

// chinese writes a number below ten thousand in counting form: 十二, 二十, 一百零五.
func chinese(v int, digits []rune, units string) string {
	if v <= 0 || v >= 10000 {
		return strconv.Itoa(v)
	}
	u := []rune(units)
	var out []rune
	place := []int{1000, 100, 10, 1}
	zero := false
	for i, p := range place {
		k := v / p % 10
		if k == 0 {
			zero = len(out) > 0
			continue
		}
		if zero {
			out = append(out, digits[0])
			zero = false
		}
		if !(p == 10 && k == 1 && len(out) == 0) { // 十二, not 一十二
			out = append(out, digits[k])
		}
		if i < 3 {
			out = append(out, u[2-i])
		}
	}
	return string(out)
}

// ensureNumbering makes the numbering part when the file has none.
func (d *Doc) ensureNumbering() *Part {
	if d.Num.Part != nil {
		return d.Num.Part
	}
	name := d.Pkg.UniqueName("word/numbering%d.xml")
	d.Pkg.Put(name, ctNumbering, []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+"\n"+`<w:numbering xmlns:w="`+nsW+`"/>`))
	d.addRel(d.Main, relNumbering, relTarget(d.Main.Name, name), false)
	p, err := d.part(name)
	if err != nil {
		return nil // what was just written parses
	}
	d.Num.Part = p
	return p
}

// ListKind is a kind of list the toolbar offers.
type ListKind uint8

const (
	ListBullet ListKind = iota
	ListNumber
	ListMulti // 1. 1.1 1.1.1
	ListChinese
)

var bulletChars = []string{"•", "◦", "▪"}

// listXML is an abstract list's XML.
func listXML(id int, kind ListKind) *ox.Node {
	a := ox.New("w:abstractNum", "w:abstractNumId", strconv.Itoa(id))
	a.Append(ox.New("w:multiLevelType", "w:val", "hybridMultilevel"))
	for i := 0; i < 9; i++ {
		l := ox.New("w:lvl", "w:ilvl", strconv.Itoa(i))
		l.Append(ox.New("w:start", "w:val", "1"))
		format, text := "decimal", "%"+strconv.Itoa(i+1)+"."
		switch kind {
		case ListBullet:
			format, text = "bullet", bulletChars[i%3]
		case ListNumber:
			switch i % 3 {
			case 1:
				format = "lowerLetter"
			case 2:
				format = "lowerRoman"
			}
		case ListMulti:
			var parts []string
			for k := 0; k <= i; k++ {
				parts = append(parts, "%"+strconv.Itoa(k+1))
			}
			text = strings.Join(parts, ".")
		case ListChinese:
			format, text = "chineseCounting", "%"+strconv.Itoa(i+1)+"、"
			if i%3 == 1 {
				format, text = "decimal", "%"+strconv.Itoa(i+1)+"."
			} else if i%3 == 2 {
				format, text = "lowerLetter", "%"+strconv.Itoa(i+1)+")"
			}
		}
		l.Append(ox.New("w:numFmt", "w:val", format), ox.New("w:lvlText", "w:val", text), ox.New("w:lvlJc", "w:val", "left"))
		left := (i + 1) * 360
		ind := ox.New("w:ind", "w:left", strconv.Itoa(left), "w:hanging", "360")
		if kind == ListMulti {
			ind = ox.New("w:ind", "w:left", strconv.Itoa(360+(i+1)*216), "w:hanging", strconv.Itoa(360+i*216))
		}
		l.Append(ox.New("w:pPr").Append(ind))
		a.Append(l)
	}
	return a
}

// newList adds a list definition and returns its w:numId. The new elements go where the schema wants them:
// abstractNums before nums.
func (d *Doc) newList(kind ListKind) int {
	p := d.ensureNumbering()
	if p == nil {
		return 0
	}
	root := p.X.Root
	d.saveRange(p, root, 0, len(root.Children))
	absID, numID := 0, 0
	for _, a := range root.Elements("w:abstractNum") {
		absID = max(absID, atoi(a.Attr("w:abstractNumId"))+1)
	}
	for _, n := range root.Elements("w:num") {
		numID = max(numID, atoi(n.Attr("w:numId")))
	}
	numID++
	abs := listXML(absID, kind)
	at := 0
	for i, c := range root.Children {
		if c.Kind == ox.Element && c.Name == "w:abstractNum" {
			at = i + 1
		}
	}
	root.Insert(at, abs)
	num := ox.New("w:num", "w:numId", strconv.Itoa(numID))
	num.Append(ox.New("w:abstractNumId", "w:val", strconv.Itoa(absID)))
	at = len(root.Children)
	for i, c := range root.Children {
		if c.Kind == ox.Element && c.Name == "w:numIdMacAtCleanup" {
			at = i
			break
		}
	}
	for i := len(root.Children) - 1; i >= 0; i-- {
		if c := root.Children[i]; c.Kind == ox.Element && c.Name == "w:num" {
			at = i + 1
			break
		}
	}
	root.Insert(at, num)
	d.touched(p)
	d.Num.read()
	return numID
}
