package font

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"testing"
)

// t1cs encodes a Type 1 charstring from integers and operators.
func t1cs(items ...any) []byte {
	var out []byte
	for _, it := range items {
		switch v := it.(type) {
		case int:
			if v >= -107 && v <= 107 {
				out = append(out, byte(v+139))
			} else {
				out = binary.BigEndian.AppendUint32(append(out, 255), uint32(int32(v)))
			}
		case csOp:
			if v >= 1200 {
				out = append(out, 12, byte(v-1200))
			} else {
				out = append(out, byte(v))
			}
		}
	}
	return out
}

func encrypt(b []byte, r uint16, lead int) []byte {
	plain := append(make([]byte, lead), b...)
	out := make([]byte, len(plain))
	for i, p := range plain {
		c := p ^ byte(r>>8)
		out[i] = c
		r = (uint16(c)+r)*52845 + 22719
	}
	return out
}

const (
	t1Hsbw      = csOp(13)
	t1Rmoveto   = csOp(21)
	t1Hmoveto   = csOp(22)
	t1Rlineto   = csOp(5)
	t1Closepath = csOp(9)
	t1Endchar   = csOp(14)
	t1Callsubr  = csOp(10)
	t1Return    = csOp(11)
)

var (
	t1Div       = esc(12)
	t1CallOther = esc(16)
	t1Pop       = esc(17)
	t1SetPoint  = esc(33)
	t1Seac      = esc(6)
	t1Sbw       = esc(7)
)

type t1Options struct {
	lenIV    int
	hexEexec bool
	pfb      bool
	encoding string
}

func buildType1(o t1Options) []byte {
	subrs := [][]byte{
		t1cs(3, 0, t1CallOther, t1Pop, t1Pop, t1SetPoint, t1Return),
		t1cs(0, 1, t1CallOther, t1Return),
		t1cs(0, 2, t1CallOther, t1Return),
		t1cs(t1Return),
		t1cs(10, 20, csOp(1), t1Return),
	}
	flexPoint := func(dx, dy int) []any {
		return []any{dx, dy, t1Rmoveto, 2, t1Callsubr}
	}
	flex := []any{20, 700, t1Hsbw, 100, 100, t1Rmoveto, 1, t1Callsubr}
	for _, p := range [][2]int{{50, 0}, {-40, 5}, {20, 5}, {20, 0}, {20, 0}, {20, -5}, {10, -5}} {
		flex = append(flex, flexPoint(p[0], p[1])...)
	}
	flex = append(flex, 50, 220, 100, 0, t1Callsubr, 0, 50, t1Rlineto, t1Closepath, t1Endchar)
	glyphs := []struct {
		name string
		cs   []byte
	}{
		{"A", t1cs(10, 600, t1Hsbw, 0, 100, t1Rmoveto, 100, 0, t1Rlineto, 0, 100, t1Rlineto, t1Closepath, t1Endchar)},
		{".notdef", t1cs(0, 250, t1Hsbw, t1Endchar)},
		{"acute", t1cs(30, 300, t1Hsbw, 0, 500, t1Rmoveto, 50, 50, t1Rlineto, t1Closepath, t1Endchar)},
		{"Aacute", t1cs(10, 600, t1Hsbw, 30, 200, 150, 65, 194, t1Seac)},
		{"flexed", t1cs(flex...)},
		{"divided", t1cs(0, 1000, 2, t1Div, t1Hsbw, 4, 1, 3, t1CallOther, t1Pop, t1Callsubr, 300, 2, t1Div, t1Hmoveto, 7, 8, t1Rlineto, t1Endchar)},
		{"sidebearing", t1cs(5, 6, 400, 0, t1Sbw, 1, 2, t1Rlineto, t1Endchar)},
		{"recursive", t1cs(0, 100, t1Hsbw, 5, t1Callsubr, t1Endchar)},
	}
	subrs = append(subrs, t1cs(5, t1Callsubr, t1Return))
	enc := func(cs []byte) []byte {
		if o.lenIV < 0 {
			return cs
		}
		return encrypt(cs, 4330, o.lenIV)
	}
	var priv bytes.Buffer
	priv.WriteString("dup /Private 8 dict dup begin\n/RD {string currentfile exch readstring pop} executeonly def\n")
	fmt.Fprintf(&priv, "/lenIV %d def\n/Subrs %d array\n", o.lenIV, len(subrs))
	for i, s := range subrs {
		b := enc(s)
		fmt.Fprintf(&priv, "dup %d %d RD ", i, len(b))
		priv.Write(b)
		priv.WriteString(" NP\n")
	}
	fmt.Fprintf(&priv, "ND\n2 index /CharStrings %d dict dup begin\n", len(glyphs))
	for _, g := range glyphs {
		b := enc(g.cs)
		fmt.Fprintf(&priv, "/%s %d RD ", g.name, len(b))
		priv.Write(b)
		priv.WriteString(" ND\n")
	}
	priv.WriteString("end\nend\nreadonly put\nnoaccess put\ndup /FontName get exch definefont pop\nmark currentfile closefile\n")
	cipher := encrypt(priv.Bytes(), 55665, 4)

	header := "%!PS-AdobeFont-1.0: Test\n12 dict begin\n/FontName /Test def\n/FontMatrix [0.002 0 0 0.002 0 0] readonly def\n" +
		o.encoding + "\ncurrentdict end\ncurrentfile eexec\n"
	trailer := strings.Repeat("0", 64) + "\ncleartomark\n"
	if o.pfb {
		seg := func(kind byte, b []byte) []byte {
			out := []byte{0x80, kind}
			out = binary.LittleEndian.AppendUint32(out, uint32(len(b)))
			return append(out, b...)
		}
		out := seg(1, []byte(header))
		out = append(out, seg(2, cipher)...)
		out = append(out, seg(1, []byte(trailer))...)
		return append(out, 0x80, 3)
	}
	var out bytes.Buffer
	out.WriteString(header)
	if o.hexEexec {
		out.WriteString(hex.EncodeToString(cipher))
	} else {
		out.Write(cipher)
	}
	out.WriteString(trailer)
	return out.Bytes()
}

const encodingArray = "/Encoding 256 array\n0 1 255 {1 index exch /.notdef put} for\ndup 65 /A put\ndup 8#102 /acute put\nreadonly def"

func TestType1Variants(t *testing.T) {
	cases := []t1Options{
		{lenIV: 4, encoding: encodingArray},
		{lenIV: 4, hexEexec: true, encoding: encodingArray},
		{lenIV: 0, pfb: true, encoding: encodingArray},
		{lenIV: -1, encoding: encodingArray},
		{lenIV: 1, pfb: true, encoding: encodingArray},
	}
	for _, o := range cases {
		f, err := ParseType1(buildType1(o))
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		checkType1(t, f)
	}
}

func checkType1(t *testing.T, f *Type1) {
	t.Helper()
	if f.NumGlyphs() != 8 {
		t.Fatalf("glyphs %d", f.NumGlyphs())
	}
	if f.GlyphName(0) != ".notdef" {
		t.Errorf("glyph 0 is %s", f.GlyphName(0))
	}
	if m := f.Matrix(); m[0] != 0.002 {
		t.Errorf("matrix %v", m)
	}
	gid := func(name string) int {
		g, ok := f.GIDByName(name)
		if !ok {
			t.Fatalf("no glyph %s", name)
		}
		return g
	}
	if g, ok := f.GIDByCode(65); !ok || g != gid("A") {
		t.Errorf("code 65 = %d %v", g, ok)
	}
	if g, ok := f.GIDByCode(0x42); !ok || g != gid("acute") {
		t.Errorf("radix code = %d %v", g, ok)
	}
	if _, ok := f.GIDByCode(1); ok {
		t.Error("unset code found")
	}
	if g, ok := f.GIDByRune('A'); !ok || g != gid("A") {
		t.Errorf("rune A = %d %v", g, ok)
	}
	if w, ok := f.Advance(gid("A")); !ok || w != 600 {
		t.Errorf("advance %v %v", w, ok)
	}
	want := Path{{Op: 'M', X: 10, Y: 100}, {Op: 'L', X: 110, Y: 100}, {Op: 'L', X: 110, Y: 200}, {Op: 'Z'}}
	if p := f.Outline(gid("A")); !pathsEqual(p, want) {
		t.Errorf("A outline %v", p)
	}
	// The accent's origin is 10 + 200 - 30, plus its own bearing of 30.
	p := f.Outline(gid("Aacute"))
	if len(p) != 7 || p[4] != (Seg{Op: 'M', X: 210, Y: 650}) {
		t.Errorf("seac outline %v", p)
	}
	if w, _ := f.Advance(gid("Aacute")); w != 600 {
		t.Errorf("seac advance %v", w)
	}
	p = f.Outline(gid("flexed"))
	if len(p) != 5 || p[0] != (Seg{Op: 'M', X: 120, Y: 100}) || p[1].Op != 'C' || p[2].Op != 'C' || p[3].Op != 'L' {
		t.Fatalf("flex outline %v", p)
	}
	if p[1].X1 != 130 || p[1].Y1 != 105 || p[1].X != 170 || p[1].Y != 110 || p[2].X != 220 || p[2].Y != 100 {
		t.Errorf("flex points %v", p)
	}
	if p[3].X != 220 || p[3].Y != 150 {
		t.Errorf("line after flex %v", p[3])
	}
	if w, _ := f.Advance(gid("divided")); w != 500 {
		t.Errorf("div advance %v", w)
	}
	want = Path{{Op: 'M', X: 150}, {Op: 'L', X: 157, Y: 8}, {Op: 'Z'}}
	if p := f.Outline(gid("divided")); !pathsEqual(p, want) {
		t.Errorf("div and hint replacement outline %v", p)
	}
	want = Path{{Op: 'M', X: 5, Y: 6}, {Op: 'L', X: 6, Y: 8}, {Op: 'Z'}}
	if p := f.Outline(gid("sidebearing")); !pathsEqual(p, want) {
		t.Errorf("sbw outline %v", p)
	}
	if p := f.Outline(gid("recursive")); p != nil {
		t.Errorf("recursive outline %v", p)
	}
	if p := f.Outline(99); p != nil {
		t.Errorf("bad glyph outline %v", p)
	}
}

func TestType1Encodings(t *testing.T) {
	f, err := ParseType1(buildType1(t1Options{lenIV: 4, encoding: "/Encoding StandardEncoding def"}))
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := f.GIDByCode(194); !ok || f.GlyphName(g) != "acute" {
		t.Errorf("standard encoding: %d %v", g, ok)
	}
	f, err = ParseType1(buildType1(t1Options{lenIV: 4, encoding: "/Encoding [/.notdef /A /acute] def"}))
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := f.GIDByCode(2); !ok || f.GlyphName(g) != "acute" {
		t.Errorf("literal encoding: %d %v", g, ok)
	}
}

func TestType1Unencrypted(t *testing.T) {
	src := "%!\n/FontMatrix [0.001 0 0 0.001 0 0] def\n/lenIV -1 def\n/CharStrings 1 dict dup begin\n/A 2 RD "
	data := append([]byte(src), t1cs(t1Endchar, t1Endchar)...)
	data = append(data, " ND\nend\n"...)
	f, err := ParseType1(data)
	if err != nil {
		t.Fatal(err)
	}
	if f.NumGlyphs() != 1 {
		t.Fatalf("glyphs %d", f.NumGlyphs())
	}
}

func TestType1Rejects(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("%!PS"), []byte("currentfile eexec "), {0x80, 1, 255, 255, 255, 255}} {
		if _, err := ParseType1(data); err == nil {
			t.Errorf("accepted %q", data)
		}
	}
}

func TestType1LoopBudget(t *testing.T) {
	// Each level calls the next many times, and div keeps the stack busy.
	var subrs [][]byte
	for i := 0; i < 16; i++ {
		var b []byte
		for j := 0; j < 12; j++ {
			b = append(b, t1cs(i+1, t1Callsubr)...)
		}
		subrs = append(subrs, append(b, byte(t1Return)))
	}
	subrs = append(subrs, t1cs(t1Return))
	f := &Type1{subrs: subrs}
	s := t1Run{f: f}
	err := s.run(t1cs(0, t1Callsubr), 0)
	if err == nil {
		t.Error("budget not enforced")
	}
	if math.IsNaN(s.x) {
		t.Error("bad state")
	}
}
