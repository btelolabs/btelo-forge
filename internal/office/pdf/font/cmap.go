package font

import (
	"cmp"
	"container/heap"
	"errors"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Limits on a CMap. Real ones stay far below them.
const (
	maxCMapEntries   = 1 << 20
	maxCodespaces    = 1024
	maxCMapChain     = 16
	cmapMaxCodeBytes = 4
)

// CMap maps character codes to CIDs and, for ToUnicode maps, to text. Ranges stay
// ranges: storage grows with the number of entries, not with what they cover.
type CMap struct {
	name     string
	wmode    int
	identity bool
	base     *CMap
	spaces   []codespace
	cid      [cmapMaxCodeBytes][]cmapSeg
	notdef   [cmapMaxCodeBytes][]cmapSeg
	bf       [cmapMaxCodeBytes][]cmapSeg
	targets  []string
}

type codespace struct {
	lo, hi uint32
	n      int
}

// cmapSeg maps lo..hi, which never overlap inside one slice. For code c the value
// is val + off + (c - lo): off records how far into its source range lo fell.
type cmapSeg struct {
	lo, hi uint32
	val    uint32
	off    uint32
}

// rawRange is an entry as parsed; a later seq overrides an earlier one.
type rawRange struct {
	lo, hi uint32
	val    uint32
	seq    int
}

// rangeHeap keeps the latest defined active range on top.
type rangeHeap []*rawRange

func (h rangeHeap) Len() int           { return len(h) }
func (h rangeHeap) Less(i, j int) bool { return h[i].seq > h[j].seq }
func (h rangeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *rangeHeap) Push(x any)        { *h = append(*h, x.(*rawRange)) }
func (h *rangeHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// flatten sweeps the ranges into sorted disjoint segments in O(n log n), so
// overlaps cost nothing at lookup.
func flatten(raw []rawRange) []cmapSeg {
	if len(raw) == 0 {
		return nil
	}
	slices.SortFunc(raw, func(a, b rawRange) int {
		return cmp.Or(cmp.Compare(a.lo, b.lo), cmp.Compare(a.seq, b.seq))
	})
	var out []cmapSeg
	var active rangeHeap
	next := 0
	pos := uint64(raw[0].lo)
	for {
		for next < len(raw) && uint64(raw[next].lo) <= pos {
			heap.Push(&active, &raw[next])
			next++
		}
		for len(active) > 0 && uint64(active[0].hi) < pos {
			heap.Pop(&active)
		}
		if len(active) == 0 {
			if next == len(raw) {
				return out
			}
			pos = uint64(raw[next].lo)
			continue
		}
		top := active[0]
		end := uint64(top.hi)
		if next < len(raw) && uint64(raw[next].lo)-1 < end {
			end = uint64(raw[next].lo) - 1
		}
		seg := cmapSeg{lo: uint32(pos), hi: uint32(end), val: top.val, off: uint32(pos) - top.lo}
		if n := len(out); n > 0 && out[n-1].val == seg.val && uint64(out[n-1].hi)+1 == pos &&
			out[n-1].off+(seg.lo-out[n-1].lo) == seg.off {
			out[n-1].hi = seg.hi
		} else {
			out = append(out, seg)
		}
		if end == math.MaxUint32 {
			return out
		}
		pos = end + 1
	}
}

func findSeg(segs []cmapSeg, code uint32) (cmapSeg, bool) {
	i := sort.Search(len(segs), func(i int) bool { return segs[i].hi >= code })
	if i < len(segs) && segs[i].lo <= code {
		return segs[i], true
	}
	return cmapSeg{}, false
}

// CMap token kinds.
const (
	ctEOF = iota
	ctNum
	ctHex
	ctName
	ctOp
	ctOpen
	ctClose
)

type cmapToken struct {
	kind int
	num  float64
	hex  []byte
	s    string
}

type cmapLex struct {
	b []byte
	p int
}

func (l *cmapLex) next() cmapToken {
	for l.p < len(l.b) {
		c := l.b[l.p]
		switch {
		case isSpace(c):
			l.p++
		case c == '%':
			for l.p < len(l.b) && l.b[l.p] != '\n' && l.b[l.p] != '\r' {
				l.p++
			}
		case c == '<' && l.p+1 < len(l.b) && l.b[l.p+1] == '<', c == '>':
			l.p++
			if c == '<' || (l.p < len(l.b) && l.b[l.p] == '>') {
				l.p++
			}
		case c == '<':
			l.p++
			return l.hexString()
		case c == '[':
			l.p++
			return cmapToken{kind: ctOpen}
		case c == ']':
			l.p++
			return cmapToken{kind: ctClose}
		case c == '(':
			l.skipString()
		case c == ')', c == '{', c == '}':
			l.p++
		case c == '/':
			l.p++
			return cmapToken{kind: ctName, s: l.word()}
		default:
			w := l.word()
			if w == "" {
				l.p++
				continue
			}
			if v, err := strconv.ParseFloat(w, 64); err == nil {
				return cmapToken{kind: ctNum, num: v}
			}
			return cmapToken{kind: ctOp, s: w}
		}
	}
	return cmapToken{kind: ctEOF}
}

func (l *cmapLex) word() string {
	start := l.p
	for l.p < len(l.b) && !isSpace(l.b[l.p]) && !isPSDelim(l.b[l.p]) {
		l.p++
	}
	return string(l.b[start:l.p])
}

func (l *cmapLex) skipString() {
	depth := 0
	for l.p < len(l.b) {
		switch l.b[l.p] {
		case '\\':
			l.p++
		case '(':
			depth++
		case ')':
			depth--
		}
		l.p++
		if depth == 0 {
			return
		}
	}
}

func (l *cmapLex) hexString() cmapToken {
	var out []byte
	var hi byte
	half := false
	for l.p < len(l.b) {
		c := l.b[l.p]
		l.p++
		if c == '>' {
			break
		}
		if !isHexDigit(c) {
			continue
		}
		if half {
			out = append(out, hi<<4|hexValue(c))
		} else {
			hi = hexValue(c)
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return cmapToken{kind: ctHex, hex: out}
}

func codeValue(b []byte) uint32 {
	var v uint32
	for _, c := range b {
		v = v<<8 | uint32(c)
	}
	return v
}

// cmapParser collects raw entries until they can be flattened.
type cmapParser struct {
	m       *CMap
	l       *cmapLex
	use     func(string) *CMap
	cid     [cmapMaxCodeBytes][]rawRange
	notdef  [cmapMaxCodeBytes][]rawRange
	bf      [cmapMaxCodeBytes][]rawRange
	entries int
	found   bool
}

func (p *cmapParser) full() bool { return p.entries >= maxCMapEntries }

func (p *cmapParser) add(to *[cmapMaxCodeBytes][]rawRange, n int, lo, hi, val uint32) {
	if hi < lo || p.full() {
		return
	}
	p.entries++
	to[n-1] = append(to[n-1], rawRange{lo: lo, hi: hi, val: val, seq: p.entries})
}

// sectionEnd consumes the end operator; any other operator stays for the main
// loop, which is how a missing end is survived.
func (p *cmapParser) sectionEnd(t cmapToken, end string) bool {
	if t.kind == ctEOF {
		return true
	}
	if t.kind == ctOp {
		if t.s != end {
			p.l.p -= len(t.s)
		}
		return true
	}
	return false
}

func validCode(b []byte) bool { return len(b) >= 1 && len(b) <= cmapMaxCodeBytes }

func (p *cmapParser) codespaces() {
	for {
		t := p.l.next()
		if p.sectionEnd(t, "endcodespacerange") {
			return
		}
		if t.kind != ctHex {
			continue
		}
		hi := p.l.next()
		if p.sectionEnd(hi, "endcodespacerange") {
			return
		}
		if hi.kind == ctHex && validCode(t.hex) && len(hi.hex) == len(t.hex) && len(p.m.spaces) < maxCodespaces {
			p.m.spaces = append(p.m.spaces, codespace{lo: codeValue(t.hex), hi: codeValue(hi.hex), n: len(t.hex)})
		}
	}
}

func cidValue(t cmapToken) (uint32, bool) {
	if t.kind == ctHex && len(t.hex) >= 1 && len(t.hex) <= 4 {
		return codeValue(t.hex), true
	}
	if t.kind == ctNum && t.num >= 0 && t.num <= math.MaxUint32 {
		return uint32(t.num), true
	}
	return 0, false
}

// cidRanges reads triples of low code, high code and value.
func (p *cmapParser) cidRanges(to *[cmapMaxCodeBytes][]rawRange, end string) {
	for {
		lo := p.l.next()
		if p.sectionEnd(lo, end) {
			return
		}
		if lo.kind != ctHex {
			continue
		}
		hi := p.l.next()
		if p.sectionEnd(hi, end) {
			return
		}
		v := p.l.next()
		if p.sectionEnd(v, end) {
			return
		}
		cid, ok := cidValue(v)
		if ok && hi.kind == ctHex && validCode(lo.hex) && validCode(hi.hex) {
			p.add(to, len(lo.hex), codeValue(lo.hex), codeValue(hi.hex), cid)
		}
	}
}

// cidChars reads pairs of code and value.
func (p *cmapParser) cidChars(to *[cmapMaxCodeBytes][]rawRange, end string) {
	for {
		code := p.l.next()
		if p.sectionEnd(code, end) {
			return
		}
		if code.kind != ctHex {
			continue
		}
		v := p.l.next()
		if p.sectionEnd(v, end) {
			return
		}
		if cid, ok := cidValue(v); ok && validCode(code.hex) {
			c := codeValue(code.hex)
			p.add(to, len(code.hex), c, c, cid)
		}
	}
}

// decodeTarget turns a bf destination into text. Targets are UTF-16BE; a lone
// byte is taken as a character code.
func decodeTarget(b []byte) string {
	if len(b) == 1 {
		return string(rune(b[0]))
	}
	if len(b)%2 == 1 {
		b = append([]byte{0}, b...)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return string(utf16.Decode(u))
}

func (p *cmapParser) target(s string) uint32 {
	p.m.targets = append(p.m.targets, s)
	return uint32(len(p.m.targets) - 1)
}

// bfTarget reads one destination: hex text, a glyph name, or a number.
func bfTarget(t cmapToken) (string, bool) {
	switch t.kind {
	case ctHex:
		return decodeTarget(t.hex), true
	case ctName:
		if r, ok := NameToRune(t.s); ok {
			return string(r), true
		}
	case ctNum:
		if t.num >= 0 && t.num <= 0x10FFFF {
			return string(rune(t.num)), true
		}
	}
	return "", false
}

func (p *cmapParser) bfChars() {
	for {
		code := p.l.next()
		if p.sectionEnd(code, "endbfchar") {
			return
		}
		if code.kind != ctHex {
			continue
		}
		v := p.l.next()
		if p.sectionEnd(v, "endbfchar") {
			return
		}
		if s, ok := bfTarget(v); ok && validCode(code.hex) && !p.full() {
			c := codeValue(code.hex)
			p.add(&p.bf, len(code.hex), c, c, p.target(s))
		}
	}
}

func (p *cmapParser) bfRanges() {
	for {
		lo := p.l.next()
		if p.sectionEnd(lo, "endbfrange") {
			return
		}
		if lo.kind != ctHex {
			continue
		}
		hi := p.l.next()
		if p.sectionEnd(hi, "endbfrange") {
			return
		}
		v := p.l.next()
		if p.sectionEnd(v, "endbfrange") {
			return
		}
		good := hi.kind == ctHex && validCode(lo.hex) && validCode(hi.hex)
		if v.kind == ctOpen {
			p.bfArray(lo, hi, good)
			continue
		}
		if s, ok := bfTarget(v); ok && good && !p.full() {
			p.add(&p.bf, len(lo.hex), codeValue(lo.hex), codeValue(hi.hex), p.target(s))
		}
	}
}

// bfArray reads the targets of a range one by one, each a separate entry.
func (p *cmapParser) bfArray(lo, hi cmapToken, good bool) {
	var code, last uint64
	if good {
		code, last = uint64(codeValue(lo.hex)), uint64(codeValue(hi.hex))
	}
	for {
		t := p.l.next()
		if t.kind == ctClose || t.kind == ctEOF {
			return
		}
		if t.kind == ctOp {
			p.l.p -= len(t.s)
			return
		}
		if s, ok := bfTarget(t); ok && good && code <= last && !p.full() {
			p.add(&p.bf, len(lo.hex), uint32(code), uint32(code), p.target(s))
		}
		code++
	}
}

func builtinCMap(name string) *CMap {
	switch name {
	case "Identity-H":
		return IdentityCMap(false)
	case "Identity-V":
		return IdentityCMap(true)
	}
	return nil
}

// ParseCMap reads a CMap resource. use resolves usecmap and may be nil, in which
// case only the identity maps resolve. Junk is skipped; a result with no content
// at all is an error.
func ParseCMap(data []byte, use func(name string) *CMap) (m *CMap, err error) {
	defer func() {
		if err != nil {
			m = nil
		}
	}()
	defer recovered(&err)
	m = &CMap{wmode: -1}
	p := &cmapParser{m: m, l: &cmapLex{b: data}, use: use}
	var prev cmapToken
	for {
		t := p.l.next()
		if t.kind == ctEOF {
			break
		}
		switch t.kind {
		case ctName:
			switch t.s {
			case "WMode":
				if v := p.l.next(); v.kind == ctNum {
					m.wmode = int(v.num) & 1
				}
			case "CMapName":
				if v := p.l.next(); v.kind == ctName {
					m.name = v.s
				}
			}
		case ctOp:
			p.operator(t.s, prev)
		}
		prev = t
	}
	if !p.found {
		return nil, errors.New("font: nothing in the CMap")
	}
	for i := range p.cid {
		m.cid[i] = flatten(p.cid[i])
		m.notdef[i] = flatten(p.notdef[i])
		m.bf[i] = flatten(p.bf[i])
	}
	if b := m.base; b != nil {
		if room := maxCodespaces - len(m.spaces); room > 0 {
			m.spaces = append(m.spaces, b.spaces[:min(room, len(b.spaces))]...)
		}
		if m.wmode < 0 {
			m.wmode = b.wmode
		}
	}
	if m.wmode < 0 {
		m.wmode = 0
	}
	return m, nil
}

func (p *cmapParser) operator(op string, prev cmapToken) {
	switch op {
	case "begincodespacerange":
		p.found = true
		p.codespaces()
	case "begincidrange":
		p.found = true
		p.cidRanges(&p.cid, "endcidrange")
	case "begincidchar":
		p.found = true
		p.cidChars(&p.cid, "endcidchar")
	case "beginnotdefrange":
		p.found = true
		p.cidRanges(&p.notdef, "endnotdefrange")
	case "beginnotdefchar":
		p.found = true
		p.cidChars(&p.notdef, "endnotdefchar")
	case "beginbfchar":
		p.found = true
		p.bfChars()
	case "beginbfrange":
		p.found = true
		p.bfRanges()
	case "usecmap":
		if prev.kind == ctName {
			p.found = true
			var base *CMap
			if p.use != nil {
				base = p.use(prev.s)
			}
			if base == nil {
				base = builtinCMap(prev.s)
			}
			if base != nil && base != p.m {
				p.m.base = base
			}
		}
	}
}

// IdentityCMap is the two-byte identity map, Identity-H or Identity-V.
func IdentityCMap(vertical bool) *CMap {
	m := &CMap{name: "Identity-H", identity: true, spaces: []codespace{{0, 0xFFFF, 2}}}
	if vertical {
		m.name, m.wmode = "Identity-V", 1
	}
	return m
}

// Vertical reports WMode 1.
func (m *CMap) Vertical() bool { return m.wmode == 1 }

// Name is the CMapName.
func (m *CMap) Name() string { return m.name }

func (s *codespace) byteMatch(b []byte, i int) bool {
	shift := 8 * (s.n - 1 - i)
	c := uint32(b[i])
	return c >= s.lo>>shift&0xFF && c <= s.hi>>shift&0xFF
}

// matched counts the leading bytes of b inside the range, per byte as the
// specification has it, since <8140> to <9FFC> does not cover 0x8200.
func (s *codespace) matched(b []byte) int {
	n := 0
	for n < s.n && n < len(b) && s.byteMatch(b, n) {
		n++
	}
	return n
}

// Next reads one code from s. A sequence in no codespace is consumed at the length
// of the range it comes closest to, or of the shortest range. n >= 1 unless s is
// empty.
func (m *CMap) Next(s []byte) (code uint32, n int) {
	if len(s) == 0 {
		return 0, 0
	}
	if len(m.spaces) == 0 {
		return uint32(s[0]), 1
	}
	best, bestLen, shortest := 0, 0, cmapMaxCodeBytes
	for i := range m.spaces {
		sp := &m.spaces[i]
		k := sp.matched(s)
		if k == sp.n {
			if bestLen != -1 || sp.n < n {
				n, bestLen = sp.n, -1
			}
			continue
		}
		if bestLen >= 0 && k > best {
			best, bestLen = k, sp.n
		}
		shortest = min(shortest, sp.n)
	}
	if bestLen > 0 {
		n = bestLen
	} else if bestLen == 0 {
		n = shortest
	}
	n = min(n, len(s))
	return codeValue(s[:n]), n
}

// CID looks a code up; n is its byte length. A notdef entry counts as a hit.
func (m *CMap) CID(code uint32, n int) (int, bool) {
	if n < 1 || n > cmapMaxCodeBytes {
		return 0, false
	}
	depth := 0
	for c := m; c != nil && depth < maxCMapChain; c, depth = c.base, depth+1 {
		if c.identity {
			return int(code), true
		}
		if seg, ok := findSeg(c.cid[n-1], code); ok {
			return segValue(seg, code), true
		}
	}
	depth = 0
	for c := m; c != nil && depth < maxCMapChain; c, depth = c.base, depth+1 {
		if seg, ok := findSeg(c.notdef[n-1], code); ok {
			return int(seg.val), true
		}
	}
	return 0, false
}

func segValue(seg cmapSeg, code uint32) int {
	return int(uint64(seg.val) + uint64(seg.off) + uint64(code-seg.lo))
}

// Unicode looks a code up in the bf entries. ToUnicode maps are often sloppy about
// code lengths, so other lengths are tried after n.
func (m *CMap) Unicode(code uint32, n int) (string, bool) {
	depth := 0
	for c := m; c != nil && depth < maxCMapChain; c, depth = c.base, depth+1 {
		if s, ok := c.unicode(code, n); ok {
			return s, true
		}
		for l := 1; l <= cmapMaxCodeBytes; l++ {
			if l != n {
				if s, ok := c.unicode(code, l); ok {
					return s, true
				}
			}
		}
	}
	return "", false
}

func (m *CMap) unicode(code uint32, n int) (string, bool) {
	if n < 1 || n > cmapMaxCodeBytes {
		return "", false
	}
	seg, ok := findSeg(m.bf[n-1], code)
	if !ok || int(seg.val) >= len(m.targets) {
		return "", false
	}
	s := m.targets[seg.val]
	delta := uint64(seg.off) + uint64(code-seg.lo)
	if delta == 0 || s == "" {
		return s, true
	}
	r, size := utf8.DecodeLastRuneInString(s)
	v := uint64(r) + delta
	var b strings.Builder
	b.WriteString(s[:len(s)-size])
	if v > 0x10FFFF || v >= 0xD800 && v <= 0xDFFF {
		b.WriteRune(utf8.RuneError)
	} else {
		b.WriteRune(rune(v))
	}
	return b.String(), true
}
