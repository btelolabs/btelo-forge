package font

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestEncodings(t *testing.T) {
	cases := []struct {
		name string
		enc  *[256]string
		code int
		want string
	}{
		{"standard", &StandardEncoding, 0x27, "quoteright"},
		{"standard", &StandardEncoding, 0x60, "quoteleft"},
		{"standard", &StandardEncoding, 0xC2, "acute"},
		{"win", &WinAnsiEncoding, 0x27, "quotesingle"},
		{"win", &WinAnsiEncoding, 0x80, "Euro"},
		{"win", &WinAnsiEncoding, 0x95, "bullet"},
		{"win", &WinAnsiEncoding, 0x81, "bullet"},
		{"win", &WinAnsiEncoding, 0xA0, "space"},
		{"win", &WinAnsiEncoding, 0xAD, "hyphen"},
		{"win", &WinAnsiEncoding, 0xE9, "eacute"},
		{"win", &WinAnsiEncoding, 0x1F, ""},
		{"mac", &MacRomanEncoding, 0x80, "Adieresis"},
		{"mac", &MacRomanEncoding, 0xCA, "space"},
		{"mac", &MacRomanEncoding, 0xDB, "currency"},
		{"mac", &MacRomanEncoding, 0xBD, "Omega"},
		{"symbol", &SymbolEncoding, 0x61, "alpha"},
		{"symbol", &SymbolEncoding, 0xA0, "Euro"},
		{"zapf", &ZapfDingbatsEncoding, 0x21, "a1"},
		{"zapf", &ZapfDingbatsEncoding, 0x20, "space"},
		{"expert", &MacExpertEncoding, 0x61, "Asmall"},
		{"expert", &MacExpertEncoding, 0x20, "space"},
		{"expert", &MacExpertEncoding, 0x7F, ""},
	}
	for _, c := range cases {
		if got := c.enc[c.code]; got != c.want {
			t.Errorf("%s %#x = %q, want %q", c.name, c.code, got, c.want)
		}
	}
	for name, enc := range map[string]*[256]string{
		"standard": &StandardEncoding, "win": &WinAnsiEncoding, "mac": &MacRomanEncoding,
		"symbol": &SymbolEncoding, "zapf": &ZapfDingbatsEncoding,
	} {
		for code, glyph := range enc {
			if glyph == "" {
				continue
			}
			if _, ok := NameToRune(glyph); !ok {
				t.Errorf("%s %#x: %q is not in the glyph list", name, code, glyph)
			}
		}
	}
	if PDFDocEncoding['A'] != 'A' || PDFDocEncoding[0x80] != 0x2022 || PDFDocEncoding[0xA0] != 0x20AC ||
		PDFDocEncoding[0x18] != 0x02D8 || PDFDocEncoding[0x7F] != 0 || PDFDocEncoding[0xAD] != 0 || PDFDocEncoding[0xFF] != 0xFF {
		t.Error("PDFDocEncoding is wrong")
	}
}

func TestNameToRune(t *testing.T) {
	cases := map[string]rune{
		"A": 'A', "space": ' ', "Euro": 0x20AC, "eacute": 0xE9, "fi": 0xFB01, "alpha": 0x3B1,
		"uni4E2D": 0x4E2D, "uni00410042": 0x41, "u1F600": 0x1F600, "u4E2D": 0x4E2D, "u10FFFF": 0x10FFFF,
		"A.swash": 'A', "a.sc": 'a', "uni0041.alt": 'A', "f_i": 0xFB01, "a1": 0x2701,
		"afii10017": 0x410, "dalethatafpatah": 0x5D3,
	}
	for name, want := range cases {
		if got, ok := NameToRune(name); !ok || got != want {
			t.Errorf("%s = %#x %v", name, got, ok)
		}
	}
	for _, name := range []string{
		"", ".notdef", ".", "nosuchglyph", "uniD800", "u110000", "uni4e2d", "uni00", "uD800", "a_b", "u12", "uni004",
	} {
		if r, ok := NameToRune(name); ok {
			t.Errorf("%q = %#x", name, r)
		}
	}
}

func TestRuneToName(t *testing.T) {
	cases := map[rune]string{
		'A': "A", ' ': "space", 0xE9: "eacute", 0x2022: "bullet", 0x20AC: "Euro", 0x2019: "quoteright",
		0xB5: "mu", 0x2212: "minus", 0x2126: "Omega", 0x2206: "Delta", '-': "hyphen",
	}
	for r, want := range cases {
		if got, ok := RuneToName(r); !ok || got != want {
			t.Errorf("%#x = %q %v", r, got, ok)
		}
	}
	if _, ok := RuneToName(0x4E2D); ok {
		t.Error("a Chinese character has a name")
	}
	if _, ok := RuneToName(-1); ok {
		t.Error("-1 has a name")
	}
}

func TestStandard14(t *testing.T) {
	cases := map[string]string{
		"Helvetica": "Helvetica", "Helvetica-Bold": "Helvetica-Bold", "Helvetica-BoldOblique": "Helvetica-BoldOblique",
		"Arial": "Helvetica", "ArialMT": "Helvetica", "Arial,Bold": "Helvetica-Bold", "Arial-BoldMT": "Helvetica-Bold",
		"Arial,Italic": "Helvetica-Oblique", "Arial-ItalicMT": "Helvetica-Oblique", "Arial,BoldItalic": "Helvetica-BoldOblique",
		"Arial-BoldItalicMT": "Helvetica-BoldOblique", "Arial Bold": "Helvetica-Bold",
		"TimesNewRoman": "Times-Roman", "TimesNewRomanPSMT": "Times-Roman", "TimesNewRoman,Italic": "Times-Italic",
		"TimesNewRomanPS-BoldMT": "Times-Bold", "TimesNewRomanPS-BoldItalicMT": "Times-BoldItalic",
		"Times New Roman": "Times-Roman", "Times-Roman": "Times-Roman", "Times-BoldItalic": "Times-BoldItalic",
		"CourierNew": "Courier", "CourierNewPSMT": "Courier", "CourierNew,Bold": "Courier-Bold",
		"CourierNewPS-BoldItalicMT": "Courier-BoldOblique", "Courier-Oblique": "Courier-Oblique",
		"Symbol": "Symbol", "Symbol,Bold": "Symbol", "ZapfDingbats": "ZapfDingbats",
		"ABCDEF+Arial": "Helvetica", "ABCDEF+TimesNewRomanPSMT": "Times-Roman",
	}
	for name, want := range cases {
		if got, ok := Standard14(name); !ok || got != want {
			t.Errorf("%s = %q %v", name, got, ok)
		}
	}
	for _, name := range []string{"", "Verdana", "Arial Narrow", "Arial-Black", "Helvetica-Light", "abcdef+Arial", "Timesroman2"} {
		if got, ok := Standard14(name); ok {
			t.Errorf("%s = %q", name, got)
		}
	}
	widths := []struct {
		font, glyph string
		want        float64
	}{
		{"Helvetica", "A", 667}, {"Helvetica", "space", 278}, {"Helvetica-Bold", "a", 556},
		{"Times-Roman", "A", 722}, {"Times-Italic", "quoteright", 333}, {"Courier", "bullet", 600},
		{"Symbol", "alpha", 631}, {"Symbol", "universal", 713}, {"ZapfDingbats", "a1", 974}, {"ZapfDingbats", "space", 278},
	}
	for _, w := range widths {
		if got, ok := Standard14Width(w.font, w.glyph); !ok || got != w.want {
			t.Errorf("%s %s = %v %v", w.font, w.glyph, got, ok)
		}
	}
	if _, ok := Standard14Width("Helvetica", "nosuchglyph"); ok {
		t.Error("width of an unknown glyph")
	}
	if _, ok := Standard14Width("Arial", "A"); ok {
		t.Error("width from a name that is not canonical")
	}
	asc, desc, capH, bbox, ok := Standard14Metrics("Helvetica")
	if !ok || asc != 718 || desc != -207 || capH != 718 || bbox != [4]float64{-166, -225, 1000, 931} {
		t.Errorf("metrics %v %v %v %v %v", asc, desc, capH, bbox, ok)
	}
	if _, _, _, _, ok := Standard14Metrics("Verdana"); ok {
		t.Error("metrics of Verdana")
	}
	if asc, _, _, _, ok := Standard14Metrics("Symbol"); !ok || asc != 1010 {
		t.Errorf("Symbol ascent %v", asc)
	}
	seen := 0
	for _, name := range []string{
		"Courier", "Courier-Bold", "Courier-BoldOblique", "Courier-Oblique", "Helvetica", "Helvetica-Bold",
		"Helvetica-BoldOblique", "Helvetica-Oblique", "Times-Roman", "Times-Bold", "Times-BoldItalic", "Times-Italic",
		"Symbol", "ZapfDingbats",
	} {
		if _, _, _, _, ok := Standard14Metrics(name); ok {
			seen++
		}
	}
	if seen != 14 {
		t.Errorf("%d fonts", seen)
	}
}

const sampleCMap = `%!PS-Adobe-3.0 Resource-CMap
/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo 3 dict dup begin /Registry (Adobe) def /Ordering (Japan1) def /Supplement 2 def end def
/CMapName /Test-H def
/WMode 1 def
3 begincodespacerange
<00> <80>
<8140> <9FFC>
<E0000000> <E0FFFFFF>
endcodespacerange
2 begincidrange
<20> <7E> 1
<8140> <817E> 633
endcidrange
1 begincidchar
<80> 97
endcidchar
1 beginnotdefrange
<00> <1F> 7
endnotdefrange
endcmap
`

func TestCMapCID(t *testing.T) {
	m, err := ParseCMap([]byte(sampleCMap), nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name() != "Test-H" || !m.Vertical() {
		t.Errorf("name %q vertical %v", m.Name(), m.Vertical())
	}
	cases := []struct {
		code uint32
		n    int
		cid  int
		ok   bool
	}{
		{0x20, 1, 1, true}, {0x7E, 1, 95, true}, {0x80, 1, 97, true}, {0x8140, 2, 633, true},
		{0x817E, 2, 695, true}, {0x7F, 1, 0, false}, {0x10, 1, 7, true}, {0x20, 2, 0, false}, {0x20, 9, 0, false},
	}
	for _, c := range cases {
		if cid, ok := m.CID(c.code, c.n); cid != c.cid || ok != c.ok {
			t.Errorf("code %#x/%d = %d %v", c.code, c.n, cid, ok)
		}
	}
	nexts := []struct {
		in   []byte
		code uint32
		n    int
	}{
		{nil, 0, 0},
		{[]byte{0x41, 0x42}, 0x41, 1},
		{[]byte{0x81, 0x40, 0x41}, 0x8140, 2},
		{[]byte{0xE0, 1, 2, 3, 4}, 0xE0010203, 4},
		{[]byte{0x81, 0x20}, 0x8120, 2},       // Partial match: first byte fits a two-byte range.
		{[]byte{0x81}, 0x81, 1},               // Cut short.
		{[]byte{0xE0, 1}, 0xE001, 2},          // Cut short inside a four-byte range.
		{[]byte{0xF5, 0x41}, 0xF5, 1},         // No range: the shortest length.
		{[]byte{0x90, 0xFE, 0x41}, 0x90FE, 2}, // Second byte out of range.
	}
	for _, c := range nexts {
		if code, n := m.Next(c.in); code != c.code || n != c.n {
			t.Errorf("Next(%x) = %#x %d", c.in, code, n)
		}
	}
}

const sampleToUnicode = `/CIDInit /ProcSet findresource begin 12 dict begin begincmap
/CMapName /Adobe-Identity-UCS def
1 begincodespacerange <0000> <FFFF> endcodespacerange
4 beginbfchar
<0001> <0041>
<0002> <00660069>
<0003> <D83DDE00>
<0004> /eacute
endbfchar
3 beginbfrange
<0010> <0012> <0061>
<0020> <0022> [<0078> <D83DDE01> <00410042>]
<0100> <0102> <D83DDE00>
endbfrange
endcmap`

func TestCMapToUnicode(t *testing.T) {
	m, err := ParseCMap([]byte(sampleToUnicode), nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[uint32]string{
		1: "A", 2: "fi", 3: "\U0001F600", 4: "é", 0x10: "a", 0x12: "c", 0x20: "x", 0x21: "\U0001F601", 0x22: "AB",
		0x100: "\U0001F600", 0x101: "\U0001F601", 0x102: "\U0001F602",
	}
	for code, want := range cases {
		if got, ok := m.Unicode(code, 2); !ok || got != want {
			t.Errorf("%#x = %q %v", code, got, ok)
		}
	}
	if got, ok := m.Unicode(1, 1); !ok || got != "A" {
		t.Errorf("sloppy code length = %q %v", got, ok)
	}
	if _, ok := m.Unicode(0x13, 2); ok {
		t.Error("found a code past the range")
	}
	if _, ok := m.CID(1, 2); ok {
		t.Error("bf entry found as CID")
	}
}

func TestCMapUseAndIdentity(t *testing.T) {
	base, err := ParseCMap([]byte(sampleCMap), nil)
	if err != nil {
		t.Fatal(err)
	}
	asked := ""
	child, err := ParseCMap([]byte(`/CMapName /Child def /Test-H usecmap 1 begincidchar <21> 500 endcidchar`),
		func(name string) *CMap {
			asked = name
			return base
		})
	if err != nil {
		t.Fatal(err)
	}
	if asked != "Test-H" {
		t.Errorf("asked for %q", asked)
	}
	if c, _ := child.CID(0x21, 1); c != 500 {
		t.Errorf("override = %d", c)
	}
	if c, _ := child.CID(0x22, 1); c != 3 {
		t.Errorf("inherited = %d", c)
	}
	if !child.Vertical() {
		t.Error("WMode not inherited")
	}
	if code, n := child.Next([]byte{0x81, 0x40}); code != 0x8140 || n != 2 {
		t.Errorf("codespace not inherited: %#x %d", code, n)
	}
	// A chain looping on itself ends at the limit.
	var loop *CMap
	loop = &CMap{}
	loop.base = loop
	if _, ok := loop.CID(1, 1); ok {
		t.Error("loop found a CID")
	}
	if _, ok := loop.Unicode(1, 1); ok {
		t.Error("loop found text")
	}

	id := IdentityCMap(false)
	if c, ok := id.CID(0x1234, 2); !ok || c != 0x1234 || id.Vertical() || id.Name() != "Identity-H" {
		t.Errorf("identity %d %v", c, ok)
	}
	if code, n := id.Next([]byte{0x12, 0x34, 0x56}); code != 0x1234 || n != 2 {
		t.Errorf("identity Next %#x %d", code, n)
	}
	if code, n := id.Next([]byte{0x12}); code != 0x12 || n != 1 {
		t.Errorf("identity Next at the end %#x %d", code, n)
	}
	if !IdentityCMap(true).Vertical() {
		t.Error("Identity-V is not vertical")
	}
	viaUse, err := ParseCMap([]byte(`/Identity-V usecmap`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := viaUse.CID(7, 2); !ok || c != 7 || !viaUse.Vertical() {
		t.Errorf("usecmap identity %d %v", c, ok)
	}
}

func TestCMapOverlaps(t *testing.T) {
	m, err := ParseCMap([]byte(`begincidrange
<0000> <00FF> 1000
<0010> <0020> 5
<0015> <0016> 900
endcidrange`), nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[uint32]int{0: 1000, 0xF: 1015, 0x10: 5, 0x14: 9, 0x15: 900, 0x16: 901, 0x17: 12, 0x20: 21, 0x21: 1033, 0xFF: 1255}
	for code, want := range cases {
		if got, ok := m.CID(code, 2); !ok || got != want {
			t.Errorf("%#x = %d %v, want %d", code, got, ok, want)
		}
	}
}

func TestCMapHostile(t *testing.T) {
	start := time.Now()
	m, err := ParseCMap([]byte(`1 begincidrange <00000000> <FFFFFFFF> 0 endcidrange
1 beginbfrange <00000000> <FFFFFFFF> <0041> endbfrange`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.cid[3]) != 1 || len(m.bf[3]) != 1 {
		t.Errorf("segments %d %d", len(m.cid[3]), len(m.bf[3]))
	}
	if c, ok := m.CID(0xFFFFFFFF, 4); !ok || c != 0xFFFFFFFF {
		t.Errorf("top of the range = %d %v", c, ok)
	}
	if s, ok := m.Unicode(0xFFFFFFFF, 4); !ok || s != "�" {
		t.Errorf("text past Unicode = %q %v", s, ok)
	}
	if s, ok := m.Unicode(0xD800-0x41, 4); !ok || s != "�" {
		t.Errorf("surrogate = %q %v", s, ok)
	}

	// Many overlapping ranges must not slow lookups or blow up.
	var b strings.Builder
	b.WriteString("begincidrange\n")
	for i := range 300000 {
		fmt.Fprintf(&b, "<00000000> <FFFFFF%02X> %d\n", i&0xFF, i)
	}
	b.WriteString("endcidrange\n")
	m, err = ParseCMap([]byte(b.String()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.cid[3]) > 512 {
		t.Errorf("%d segments", len(m.cid[3]))
	}
	for i := range 1000000 {
		m.CID(uint32(i)*4099, 4)
	}

	// More entries than the limit stops collecting, not the program.
	b.Reset()
	b.WriteString("begincidchar\n")
	for i := range maxCMapEntries + 1000 {
		fmt.Fprintf(&b, "<%08X> 1\n", i)
	}
	b.WriteString("endcidchar\n")
	m, err = ParseCMap([]byte(b.String()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := m.CID(5, 4); !ok || c != 1 {
		t.Errorf("first entries lost: %d %v", c, ok)
	}

	// Junk, unbalanced sections, missing ends.
	for _, s := range []string{
		"", "junk", "<<<<", "((((", "<", "begincidrange", "begincidrange <00", "beginbfrange <00> <01> [",
		"begincodespacerange <00> endcodespacerange", "beginbfchar <00> <0041",
		"begincidrange <00> <01> begincidchar <02> 3 endcidchar",
		"begincodespacerange <0000000000> <FFFFFFFFFF> endcodespacerange",
		"/CMapName", "/WMode", "/x usecmap", "usecmap",
	} {
		ParseCMap([]byte(s), nil)
	}
	if _, err := ParseCMap([]byte("nothing here"), nil); err == nil {
		t.Error("accepted a text with no CMap in it")
	}
	if m, err := ParseCMap([]byte(`begincidrange <0001> <0002> 3 begincidchar <0005> 9 endcidchar`), nil); err != nil {
		t.Error(err)
	} else if c, _ := m.CID(5, 2); c != 9 {
		t.Errorf("recovery after a missing end: %d", c)
	}
	rng := rand.New(rand.NewSource(4))
	src := []byte(sampleCMap + sampleToUnicode)
	buf := make([]byte, len(src))
	for range 20000 {
		copy(buf, src)
		for range 1 + rng.Intn(6) {
			buf[rng.Intn(len(buf))] = byte(rng.Intn(256))
		}
		if m, err := ParseCMap(buf, nil); err == nil {
			m.Next(buf[:8])
			m.CID(0x41, 1)
			m.Unicode(1, 2)
		}
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("took %v", d)
	}
}
