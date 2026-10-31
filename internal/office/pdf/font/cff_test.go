package font

import (
	"encoding/binary"
	"math"
	"testing"
)

type csOp int

func esc(n int) csOp { return csOp(1200 + n) }

// t2 encodes a Type 2 charstring from integers, floats, operators and raw bytes.
func t2(items ...any) []byte {
	var out []byte
	for _, it := range items {
		switch v := it.(type) {
		case int:
			out = append(out, 28, byte(v>>8), byte(v))
		case float64:
			out = binary.BigEndian.AppendUint32(append(out, 255), uint32(int32(v*65536)))
		case csOp:
			if v >= 1200 {
				out = append(out, 12, byte(v-1200))
			} else {
				out = append(out, byte(v))
			}
		case []byte:
			out = append(out, v...)
		}
	}
	return out
}

func cffIndexBytes(items ...[]byte) []byte {
	out := binary.BigEndian.AppendUint16(nil, uint16(len(items)))
	if len(items) == 0 {
		return out
	}
	out = append(out, 4)
	off := uint32(1)
	out = binary.BigEndian.AppendUint32(out, off)
	for _, it := range items {
		off += uint32(len(it))
		out = binary.BigEndian.AppendUint32(out, off)
	}
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

// dictOp encodes integer operands in the fixed five-byte form, then the operator.
func dictOp(op int, args ...int) []byte {
	var out []byte
	for _, a := range args {
		out = binary.BigEndian.AppendUint32(append(out, 29), uint32(int32(a)))
	}
	if op >= 1200 {
		return append(out, 12, byte(op-1200))
	}
	return append(out, byte(op))
}

func dictReal(s string) []byte {
	nib := []byte{}
	for _, c := range s {
		switch c {
		case '.':
			nib = append(nib, 10)
		case '-':
			nib = append(nib, 14)
		default:
			nib = append(nib, byte(c-'0'))
		}
	}
	nib = append(nib, 15)
	if len(nib)%2 == 1 {
		nib = append(nib, 15)
	}
	out := []byte{30}
	for i := 0; i < len(nib); i += 2 {
		out = append(out, nib[i]<<4|nib[i+1])
	}
	return out
}

type cffBlock struct {
	name string
	data func(off map[string]int) []byte
}

// assembleCFF lays the file out twice, the second time with real offsets; the
// fixed-size integers keep every size the same.
func assembleCFF(strs [][]byte, gsubrs [][]byte, top func(off map[string]int) []byte, blocks []cffBlock) []byte {
	off := map[string]int{}
	var out []byte
	for pass := 0; pass < 2; pass++ {
		out = []byte{1, 0, 4, 4}
		out = append(out, cffIndexBytes([]byte("Test"))...)
		out = append(out, cffIndexBytes(top(off))...)
		out = append(out, cffIndexBytes(strs...)...)
		out = append(out, cffIndexBytes(gsubrs...)...)
		for _, b := range blocks {
			off[b.name] = len(out)
			out = append(out, b.data(off)...)
		}
	}
	return out
}

func constBlock(name string, b []byte) cffBlock {
	return cffBlock{name, func(map[string]int) []byte { return b }}
}

// nameKeyedCFF has A, acute, flexy, Aacute (seac), caller (subrs), looper.
func nameKeyedCFF() []byte {
	glyphs := [][]byte{
		t2(csOp(14)),
		t2(-100, 10, 20, csOp(1), 100, 200, csOp(21), 50, csOp(6), 60, csOp(7), -50, 0, csOp(5), csOp(14)),
		t2(1, 2, csOp(18), 3, 4, csOp(19), []byte{0xC0}, 10, csOp(22), 1, 2, 3, 4, 5, 6, csOp(8), csOp(14)),
		t2(0, 0, csOp(21), 10, 1, 10, 2, 10, 0, 10, 0, 10, -2, 10, -1, 0, esc(35), csOp(14)),
		t2(30, 40, 65, 194, csOp(14)),
		t2(0, 0, csOp(21), -107, csOp(10), -107, csOp(29), csOp(14)),
		t2(-106, csOp(10), csOp(14)),
	}
	private := []byte{}
	private = append(private, dictOp(opDefaultW, 450)...)
	private = append(private, dictOp(opNominalW, 600)...)
	private = append(private, dictOp(opSubrs, 0)...)
	subrOff := len(private)
	private = append(dictOp(opDefaultW, 450), dictOp(opNominalW, 600)...)
	private = append(private, dictOp(opSubrs, subrOff)...)
	subrs := cffIndexBytes(
		t2(7, 8, csOp(5), csOp(11)),
		t2(-106, csOp(10), csOp(11)),
	)
	charset := []byte{0}
	for _, sid := range []int{34, 125, 391, 171, 392, 393} {
		charset = binary.BigEndian.AppendUint16(charset, uint16(sid))
	}
	// Format 0 encoding with a supplement giving code 200 to acute too.
	encoding := []byte{0x80, 3, 65, 194, 120, 1, 200, 0, 125}
	top := func(off map[string]int) []byte {
		var d []byte
		d = append(d, dictOp(opCharset, off["charset"])...)
		d = append(d, dictOp(opEncoding, off["encoding"])...)
		d = append(d, dictOp(opCharStrings, off["chars"])...)
		d = append(d, dictOp(opPrivate, len(private), off["private"])...)
		return d
	}
	return assembleCFF(
		[][]byte{[]byte("flexy"), []byte("caller"), []byte("looper")},
		[][]byte{t2(9, 0, csOp(5), csOp(11))},
		top,
		[]cffBlock{
			constBlock("charset", charset),
			constBlock("encoding", encoding),
			constBlock("chars", cffIndexBytes(glyphs...)),
			constBlock("private", private),
			constBlock("subrs", subrs),
		})
}

func TestCFFNameKeyed(t *testing.T) {
	c, err := ParseCFF(nameKeyedCFF())
	if err != nil {
		t.Fatal(err)
	}
	if c.NumGlyphs() != 7 || c.IsCID() {
		t.Fatalf("glyphs %d cid %v", c.NumGlyphs(), c.IsCID())
	}
	if m := c.Matrix(); m != [6]float64{0.001, 0, 0, 0.001, 0, 0} {
		t.Fatalf("matrix %v", m)
	}
	for gid, want := range []string{".notdef", "A", "acute", "flexy", "Aacute", "caller", "looper"} {
		if got := c.GlyphName(gid); got != want {
			t.Errorf("name %d = %q", gid, got)
		}
		if g, ok := c.GIDByName(want); !ok || g != gid {
			t.Errorf("GIDByName(%s) = %d %v", want, g, ok)
		}
	}
	for code, want := range map[byte]int{65: 1, 194: 2, 120: 3, 200: 2} {
		if g, ok := c.GIDByCode(code); !ok || g != want {
			t.Errorf("code %d = %d %v", code, g, ok)
		}
	}
	if _, ok := c.GIDByCode(1); ok {
		t.Error("unmapped code found")
	}
	if g, ok := c.GIDByRune('A'); !ok || g != 1 {
		t.Errorf("rune A = %d %v", g, ok)
	}
	if g, ok := c.GIDByRune(0xC1); !ok || g != 4 {
		t.Errorf("rune Aacute = %d %v", g, ok)
	}
	if g, ok := c.GIDByCID(3); !ok || g != 3 {
		t.Errorf("name-keyed CID = %d %v", g, ok)
	}

	if w, ok := c.Advance(1); !ok || w != 500 {
		t.Errorf("width with an argument = %v %v", w, ok)
	}
	if w, ok := c.Advance(2); !ok || w != 450 {
		t.Errorf("default width = %v %v", w, ok)
	}
	want := Path{
		{Op: 'M', X: 100, Y: 200}, {Op: 'L', X: 150, Y: 200}, {Op: 'L', X: 150, Y: 260},
		{Op: 'L', X: 100, Y: 260}, {Op: 'Z'},
	}
	if p := c.Outline(1); !pathsEqual(p, want) {
		t.Errorf("outline %v", p)
	}
	// hintmask after implicit vstems: one mask byte, then a curve.
	p := c.Outline(2)
	if len(p) != 3 || p[0] != (Seg{Op: 'M', X: 10}) || p[1].X != 19 || p[1].Y != 12 {
		t.Errorf("hintmask outline %v", p)
	}
	p = c.Outline(3)
	if len(p) != 4 || p[1].Op != 'C' || p[2].X != 60 || p[2].Y != 0 {
		t.Errorf("flex outline %v", p)
	}
	// The accent starts at the seac offset: the acute glyph's 10 + 30.
	p = c.Outline(4)
	if len(p) != 8 || p[5] != (Seg{Op: 'M', X: 40, Y: 40}) {
		t.Errorf("seac outline %v", p)
	}
	want = Path{{Op: 'M'}, {Op: 'L', X: 7, Y: 8}, {Op: 'L', X: 16, Y: 8}, {Op: 'Z'}}
	if p := c.Outline(5); !pathsEqual(p, want) {
		t.Errorf("subrs outline %v", p)
	}
	if p := c.Outline(6); p != nil {
		t.Errorf("recursive subroutine gave %v", p)
	}
	for _, gid := range []int{-1, 7, 1 << 40} {
		if p := c.Outline(gid); p != nil {
			t.Errorf("outline of %d", gid)
		}
		if _, ok := c.Advance(gid); ok {
			t.Errorf("advance of %d", gid)
		}
	}
}

func TestCFFFlexVariants(t *testing.T) {
	cases := []struct {
		name string
		cs   []byte
		x, y float64
	}{
		{"hflex", t2(1, 2, 3, 4, 5, 6, 7, esc(34)), 25, 0},
		{"hflex1", t2(1, 2, 3, 4, 5, 6, 7, 8, 9, esc(36)), 31, 0},
		{"flex1 horizontal", t2(10, 1, 10, 1, 10, 1, 10, 1, 10, 1, 7, esc(37)), 57, 0},
		{"flex1 vertical", t2(1, 10, 1, 10, 1, 10, 1, 10, 1, 10, 7, esc(37)), 0, 57},
		{"hhcurveto", t2(1, 2, 3, 4, 5, 6, 7, 8, 9, csOp(27)), 32, 13},
		{"vvcurveto", t2(1, 2, 3, 4, 5, csOp(26)), 4, 11},
		{"hvcurveto", t2(1, 2, 3, 4, 5, csOp(31)), 8, 7},
		{"vhcurveto", t2(1, 2, 3, 4, csOp(30)), 6, 4},
		{"rcurveline", t2(1, 1, 1, 1, 1, 1, 2, 3, csOp(24)), 5, 6},
		{"rlinecurve", t2(2, 3, 1, 1, 1, 1, 1, 1, csOp(25)), 5, 6},
		{"arithmetic", t2(10, 3, esc(10), 2, esc(24), 0, csOp(5)), 26, 0},
		{"stack ops", t2(1, 2, 3, 3, 1, esc(30), esc(18), 0, esc(29), esc(10), 0, csOp(5)), 3, 2},
	}
	for _, tc := range cases {
		s := t2Run{c: &CFF{}, pr: &cffPrivate{}}
		s.haveWidth = true
		if err := s.run(tc.cs, 0); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if s.x != tc.x || s.y != tc.y {
			t.Errorf("%s: pen at %v,%v, want %v,%v", tc.name, s.x, s.y, tc.x, tc.y)
		}
	}
}

// cidCFF has two FDs with different local subrs, nominal widths and matrices.
func cidCFF(topMatrix bool) []byte {
	glyphs := [][]byte{
		t2(csOp(14)),
		t2(50, 0, 0, csOp(21), -107, csOp(10), csOp(14)),
		t2(50, 0, 0, csOp(21), -107, csOp(10), csOp(14)),
		t2(csOp(14)),
	}
	makePrivate := func(nominal, dx int) ([]byte, []byte) {
		body := dictOp(opNominalW, nominal)
		off := len(body) + len(dictOp(opSubrs, 0))
		return append(body, dictOp(opSubrs, off)...), cffIndexBytes(t2(dx, 0, csOp(5), csOp(11)))
	}
	p0, s0 := makePrivate(100, 11)
	p1, s1 := makePrivate(200, 22)
	matrix := func(s string) []byte {
		var d []byte
		for _, v := range []string{s, "0", "0", s, "0", "0"} {
			d = append(d, dictReal(v)...)
		}
		return append(d, 12, 7)
	}
	fontDict := func(priv []byte, off int, m string) []byte {
		return append(matrix(m), dictOp(opPrivate, len(priv), off)...)
	}
	// FD 1 has a larger em, so its glyphs are scaled by half into FD 0's space.
	fdArray := func(off map[string]int) []byte {
		return cffIndexBytes(fontDict(p0, off["p0"], "0.001"), fontDict(p1, off["p1"], "0.0005"))
	}
	charset := []byte{2}
	charset = binary.BigEndian.AppendUint16(charset, 100)
	charset = binary.BigEndian.AppendUint16(charset, 2)
	fdSelect := []byte{3, 0, 2, 0, 0, 0, 0, 2, 1, 0, 4}
	top := func(off map[string]int) []byte {
		d := dictOp(opROS, 391, 392, 0)
		if topMatrix {
			d = append(d, matrix("1")...)
		}
		d = append(d, dictOp(opCharset, off["charset"])...)
		d = append(d, dictOp(opCharStrings, off["chars"])...)
		d = append(d, dictOp(opFDArray, off["fdarray"])...)
		d = append(d, dictOp(opFDSelect, off["fdselect"])...)
		return d
	}
	return assembleCFF([][]byte{[]byte("Adobe"), []byte("Identity")}, nil, top, []cffBlock{
		constBlock("charset", charset),
		constBlock("fdselect", fdSelect),
		constBlock("chars", cffIndexBytes(glyphs...)),
		constBlock("p0", p0), constBlock("s0", s0),
		constBlock("p1", p1), constBlock("s1", s1),
		{"fdarray", fdArray},
	})
}

func TestCFFCID(t *testing.T) {
	for _, topMatrix := range []bool{false, true} {
		c, err := ParseCFF(cidCFF(topMatrix))
		if err != nil {
			t.Fatal(err)
		}
		if !c.IsCID() {
			t.Fatal("not CID")
		}
		for cid, gid := range map[int]int{0: 0, 100: 1, 101: 2, 102: 3} {
			if g, ok := c.GIDByCID(cid); !ok || g != gid {
				t.Errorf("CID %d = %d %v", cid, g, ok)
			}
		}
		if _, ok := c.GIDByCID(5); ok {
			t.Error("CID 5 found")
		}
		if c.GlyphName(1) != "" {
			t.Error("CID font glyph has a name")
		}
		if w, _ := c.Advance(1); w != 150 {
			t.Errorf("FD 0 width %v", w)
		}
		if w, _ := c.Advance(2); w != 250 {
			t.Errorf("FD 1 width %v", w)
		}
		if m := c.Matrix(); math.Abs(m[0]-0.001) > 1e-12 {
			t.Errorf("matrix %v", m)
		}
		if p := c.Outline(1); len(p) != 3 || p[1].X != 11 {
			t.Errorf("FD 0 outline %v", p)
		}
		if p := c.Outline(2); len(p) != 3 || math.Abs(p[1].X-11) > 1e-9 {
			t.Errorf("FD 1 outline %v", p)
		}
	}
}

func TestCFFPredefinedTables(t *testing.T) {
	if cffStandardStrings[390] != "Semibold" || cffStandardStrings[378] != "Ydieresissmall" {
		t.Error("standard strings are out of step")
	}
	if len(expertCharset) != 166 || len(expertSubsetCharset) != 87 || len(isoAdobeCharset) != 229 {
		t.Errorf("charsets %d %d %d", len(expertCharset), len(expertSubsetCharset), len(isoAdobeCharset))
	}
	check := map[int]string{0x30: "zerooldstyle", 0x56: "ff", 0x61: "Asmall", 0xFF: "Ydieresissmall", 0xBD: "onehalf"}
	for code, want := range check {
		if got := cffStandardStrings[expertEncoding[code]]; got != want {
			t.Errorf("expert %#x = %s", code, got)
		}
	}
}

func TestCFFBudget(t *testing.T) {
	// Two subroutines each calling the next ten times would take 10^16 steps.
	calls := func(n int) []byte {
		var b []byte
		for i := 0; i < 10; i++ {
			b = append(b, t2(n, csOp(10))...)
		}
		return append(b, 11)
	}
	var subrs [][]byte
	for i := 0; i < 16; i++ {
		subrs = append(subrs, calls(i+1-107))
	}
	subrs = append(subrs, []byte{11})
	x := cffIndexBytes(subrs...)
	idx, _, err := readIndex(x, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := t2Run{c: &CFF{}, pr: &cffPrivate{subrs: idx}}
	if err := s.run(t2(-107, csOp(10)), 0); err == nil {
		t.Error("budget not enforced")
	}
}

func TestCFFRejects(t *testing.T) {
	for _, data := range [][]byte{nil, {1}, {1, 0, 4, 1}, {1, 0, 200, 4, 0, 0}, make([]byte, 100)} {
		if _, err := ParseCFF(data); err == nil {
			t.Errorf("accepted %v", data)
		}
	}
}
