package font

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pathsEqual(a, b Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func windowsFont(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(`C:\Windows\Fonts`, name))
	if err != nil {
		t.Skip("no ", name)
	}
	return b
}

func TestArial(t *testing.T) {
	f, err := ParseTrueType(windowsFont(t, "arial.ttf"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.UnitsPerEm() != 2048 || f.NumGlyphs() < 1000 || f.IsCFF() {
		t.Fatalf("upem %d glyphs %d cff %v", f.UnitsPerEm(), f.NumGlyphs(), f.IsCFF())
	}
	if f.PostScriptName() != "ArialMT" {
		t.Errorf("name %q", f.PostScriptName())
	}
	if f.Ascent() != 1854 || f.Descent() != -434 || f.CapHeight() != 1467 {
		t.Errorf("metrics %v %v %v", f.Ascent(), f.Descent(), f.CapHeight())
	}
	if m := f.Matrix(); m[0] != 1.0/2048 {
		t.Errorf("matrix %v", m)
	}
	if b := f.BBox(); b[2] <= b[0] || b[3] <= b[1] {
		t.Errorf("bbox %v", b)
	}
	if f.ItalicAngle() != 0 {
		t.Errorf("italic %v", f.ItalicAngle())
	}
	if !f.HasCmap(3, 1) || f.HasCmap(9, 9) || len(f.Cmaps()) == 0 {
		t.Errorf("cmaps %v", f.Cmaps())
	}
	gid, ok := f.GIDByCmap(3, 1, 'A')
	if !ok {
		t.Fatal("no A")
	}
	if g, ok := f.GIDByRune('A'); !ok || g != gid {
		t.Errorf("rune %d %v", g, ok)
	}
	if _, ok := f.GIDByRune(0x10FFFF); ok {
		t.Error("found a glyph for a private code point")
	}
	if w, ok := f.Advance(gid); !ok || w != 1366 {
		t.Errorf("advance %v %v", w, ok)
	}
	p := f.Outline(gid)
	if len(p) < 8 || p[0].Op != 'M' || p[len(p)-1].Op != 'Z' {
		t.Fatalf("outline %v", p)
	}
	for _, s := range p {
		if s.X < -10 || s.X > 1400 || s.Y < -10 || s.Y > 1500 {
			t.Fatalf("point out of the glyph: %v", s)
		}
	}
	// An O has curves only, two contours.
	o, _ := f.GIDByRune('O')
	moves, quads := 0, 0
	for _, s := range f.Outline(o) {
		switch s.Op {
		case 'M':
			moves++
		case 'Q':
			quads++
		}
	}
	if moves != 2 || quads < 8 {
		t.Errorf("O has %d contours, %d curves", moves, quads)
	}
	for _, gid := range []int{-1, f.NumGlyphs(), 1 << 40} {
		if f.Outline(gid) != nil {
			t.Errorf("outline of %d", gid)
		}
		if _, ok := f.Advance(gid); ok {
			t.Errorf("advance of %d", gid)
		}
	}
	space, _ := f.GIDByRune(' ')
	if f.Outline(space) != nil {
		t.Error("the space has an outline")
	}
}

func TestCollections(t *testing.T) {
	for _, name := range []string{"simsun.ttc", "msyh.ttc"} {
		data := windowsFont(t, name)
		for index := 0; index < 2; index++ {
			f, err := ParseTrueType(data, index)
			if err != nil {
				t.Fatalf("%s %d: %v", name, index, err)
			}
			g, ok := f.GIDByRune('中')
			if !ok || len(f.Outline(g)) == 0 {
				t.Errorf("%s %d: no outline for a Chinese character", name, index)
			}
			if adv, ok := f.Advance(g); !ok || adv <= 0 {
				t.Errorf("%s %d: advance %v", name, index, adv)
			}
			if f.PostScriptName() == "" {
				t.Errorf("%s %d: no name", name, index)
			}
		}
		if _, err := ParseTrueType(data, 99); err == nil {
			t.Errorf("%s: font 99 exists", name)
		}
		if _, err := ParseTrueType(data, -1); err == nil {
			t.Errorf("%s: font -1 exists", name)
		}
	}
}

func TestOpenTypeFonts(t *testing.T) {
	files, _ := filepath.Glob(`C:\Windows\Fonts\*.otf`)
	found := false
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, err := ParseTrueType(data, 0)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		g, ok := f.GIDByRune('A')
		if !ok || len(f.Outline(g)) == 0 {
			t.Errorf("%s: no A", path)
		}
		if adv, ok := f.Advance(g); !ok || adv <= 0 {
			t.Errorf("%s: advance %v", path, adv)
		}
		if f.IsCFF() {
			found = true
			if _, _, err := f.Subset([]int{g}); err == nil {
				t.Errorf("%s: subset of CFF outlines", path)
			}
		}
	}
	if !found {
		t.Log("no CFF OpenType font installed; the embedded path is covered by TestOpenTypeWrappedCFF")
	}
}

func sfntWith(tables map[string][]byte) []byte { return writeSfnt(tables) }

func TestOpenTypeWrappedCFF(t *testing.T) {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000)
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], 2)
	hmtx := make([]byte, 8)
	binary.BigEndian.PutUint16(hmtx[0:], 500)
	binary.BigEndian.PutUint16(hmtx[4:], 700)
	f, err := ParseTrueType(sfntWith(map[string][]byte{
		"CFF ": nameKeyedCFF(), "head": head, "hhea": hhea, "hmtx": hmtx,
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsCFF() || f.NumGlyphs() != 7 {
		t.Fatalf("cff %v glyphs %d", f.IsCFF(), f.NumGlyphs())
	}
	if g, ok := f.GIDByName("A"); !ok || g != 1 {
		t.Errorf("name %d %v", g, ok)
	}
	if g, ok := f.GIDByRune('A'); !ok || g != 1 {
		t.Errorf("rune %d %v", g, ok)
	}
	if len(f.Outline(1)) != 5 {
		t.Errorf("outline %v", f.Outline(1))
	}
	if w, ok := f.Advance(1); !ok || math.Abs(w-700) > 1e-9 {
		t.Errorf("advance %v %v", w, ok)
	}
	if f.PostScriptName() != "Test" {
		t.Errorf("name %q", f.PostScriptName())
	}
	if _, _, err := f.Subset([]int{1}); err == nil {
		t.Error("subset of CFF outlines")
	}
}

func TestMinimalSubsetFont(t *testing.T) {
	// A glyph with one triangle, no cmap, name, post or OS/2, the way PDF subsets come.
	glyph := []byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 1, 1, 1, 0, 10, 0, 10, 0, 0, 0, 0, 0, 0, 0, 20, 0}
	loca := []byte{0, 0, 0, 0, 0, 15, 0, 15}
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:], 3)
	f, err := ParseTrueType(sfntWith(map[string][]byte{"glyf": glyph, "loca": loca, "maxp": maxp}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.NumGlyphs() != 3 || f.UnitsPerEm() != 1000 {
		t.Fatalf("glyphs %d upem %d", f.NumGlyphs(), f.UnitsPerEm())
	}
	if f.Outline(0) != nil {
		t.Error("empty glyph has an outline")
	}
	want := Path{{Op: 'M', X: 10, Y: 0}, {Op: 'L', X: 20, Y: 0}, {Op: 'L', X: 20, Y: 20}, {Op: 'Z'}}
	if p := f.Outline(1); !pathsEqual(p, want) {
		t.Errorf("outline %v", p)
	}
	if _, ok := f.GIDByRune('A'); ok {
		t.Error("rune found without a cmap")
	}
	if _, ok := f.Advance(1); ok {
		t.Error("advance without hmtx")
	}
	data, newGID, err := f.Subset([]int{1})
	if err != nil {
		t.Fatal(err)
	}
	g, err := ParseTrueType(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !pathsEqual(g.Outline(newGID[1]), want) {
		t.Errorf("subset outline %v", g.Outline(newGID[1]))
	}
}

// compositeFont builds glyphs: empty, a square, a composite using it twice with
// a scale, a cycle, and a chain deeper than the limit.
func compositeFont(t *testing.T) *TrueType {
	t.Helper()
	square := []byte{
		0, 1, 0, 0, 0, 0, 0, 10, 0, 10,
		0, 3, 0, 0,
		0x31, 0x21, 0x31, 0x21, // on, x same/short...
		0, 0, 0, 0,
	}
	square = square[:14]
	square = append(square, 1, 1, 1, 1) // Flags: on curve, full-size deltas.
	for _, d := range []int16{0, 10, 0, -10} {
		square = binary.BigEndian.AppendUint16(square, uint16(d))
	}
	for _, d := range []int16{0, 0, 10, 0} {
		square = binary.BigEndian.AppendUint16(square, uint16(d))
	}
	head := []byte{0xFF, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0}
	comp := func(flags, gid int, args ...byte) []byte {
		b := binary.BigEndian.AppendUint16(nil, uint16(flags))
		b = binary.BigEndian.AppendUint16(b, uint16(gid))
		return append(b, args...)
	}
	composite := append([]byte{}, head...)
	composite = append(composite, comp(compXY|compMore, 1, 5, 6)...)
	composite = append(composite, comp(compXY|compScale, 1, 20, 0, 0x20, 0)...) // Half size.
	cycle := append(append([]byte{}, head...), comp(compXY, 3, 0, 0)...)
	var chain [][]byte
	for i := 0; i < 8; i++ {
		chain = append(chain, append(append([]byte{}, head...), comp(compXY, 5+i, 0, 0)...))
	}
	glyphs := append([][]byte{nil, square, composite, cycle}, chain...)
	glyphs = append(glyphs, square, append(append([]byte{}, head...), comp(compXY|compUseMetrics, 1, 0, 0)...))
	var glyf, loca []byte
	for _, g := range glyphs {
		loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf)))
		glyf = append(glyf, g...)
		for len(glyf)%2 != 0 {
			glyf = append(glyf, 0)
		}
	}
	loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf)))
	h := make([]byte, 54)
	binary.BigEndian.PutUint16(h[18:], 100)
	binary.BigEndian.PutUint16(h[50:], 1)
	maxp := make([]byte, 32)
	binary.BigEndian.PutUint16(maxp[4:], uint16(len(glyphs)))
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], 2)
	hmtx := []byte{0, 50, 0, 0, 0, 60, 0, 3} // The square's bearing moves it when it stands alone.
	for range glyphs[2:] {
		hmtx = append(hmtx, 0, 0)
	}
	f, err := ParseTrueType(sfntWith(map[string][]byte{
		"glyf": glyf, "loca": loca, "head": h, "maxp": maxp, "hhea": hhea, "hmtx": hmtx,
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestComposites(t *testing.T) {
	f := compositeFont(t)
	if p := f.Outline(1); len(p) != 5 || p[0].X != 3 {
		t.Fatalf("square %v", p)
	}
	if p := f.Outline(13); len(p) != 5 || p[0].X != 3 {
		t.Errorf("USE_MY_METRICS component %v", p)
	}
	p := f.Outline(2)
	if len(p) != 10 {
		t.Fatalf("composite %v", p)
	}
	if p[0] != (Seg{Op: 'M', X: 5, Y: 6}) || p[5] != (Seg{Op: 'M', X: 20}) || p[6] != (Seg{Op: 'L', X: 25}) {
		t.Errorf("composite %v", p)
	}
	if f.Outline(3) != nil {
		t.Error("a composite cycle gave an outline")
	}
	if f.Outline(4) != nil {
		t.Error("a chain past the depth limit gave an outline")
	}
	if f.Outline(10) == nil {
		t.Error("a chain within the depth limit gave nothing")
	}
}

func TestSubsetKeepsComponents(t *testing.T) {
	f := compositeFont(t)
	data, newGID, err := f.Subset([]int{2})
	if err != nil {
		t.Fatal(err)
	}
	if newGID[2] != 1 || newGID[1] != 2 || len(newGID) != 3 {
		t.Fatalf("map %v", newGID)
	}
	g, err := ParseTrueType(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if g.NumGlyphs() != 3 {
		t.Fatalf("glyphs %d", g.NumGlyphs())
	}
	if !pathsEqual(g.Outline(1), f.Outline(2)) {
		t.Errorf("composite changed: %v", g.Outline(1))
	}
	if w, ok := g.Advance(2); !ok || w != 60 {
		t.Errorf("advance %v %v", w, ok)
	}
	// A cycle must finish and stay a cycle rather than hang.
	if _, _, err := f.Subset([]int{3}); err != nil {
		t.Error(err)
	}
	if _, m, err := f.Subset([]int{-1, 9999, 1, 1}); err != nil || len(m) != 2 {
		t.Errorf("odd ids: %v %v", m, err)
	}
}

func TestSubsetArial(t *testing.T) {
	f, err := ParseTrueType(windowsFont(t, "arial.ttf"), 0)
	if err != nil {
		t.Fatal(err)
	}
	var gids []int
	for _, r := range "AbgÁéñ€中" {
		if g, ok := f.GIDByRune(r); ok {
			gids = append(gids, g)
		}
	}
	data, newGID, err := f.Subset(gids)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 200_000 {
		t.Errorf("subset is %d bytes", len(data))
	}
	s, err := ParseTrueType(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.UnitsPerEm() != f.UnitsPerEm() || s.NumGlyphs() != len(newGID) {
		t.Fatalf("upem %d glyphs %d", s.UnitsPerEm(), s.NumGlyphs())
	}
	for i, g := range gids {
		if newGID[g] != i+1 {
			t.Errorf("glyph %d became %d", g, newGID[g])
		}
	}
	composites := 0
	for old, gid := range newGID {
		if !pathsEqual(f.Outline(old), s.Outline(gid)) {
			t.Errorf("outline of %d changed", old)
		}
		a, ok1 := f.Advance(old)
		b, ok2 := s.Advance(gid)
		if a != b || ok1 != ok2 {
			t.Errorf("advance of %d: %v, %v", old, a, b)
		}
		if len(compositeParts(f.glyphData(old))) > 0 {
			composites++
		}
	}
	if composites == 0 {
		t.Error("no composite glyph in the test")
	}
	// The whole file's checksum comes to the sfnt magic number.
	if sum := sfntChecksum(data); sum != 0xB1B0AFBA {
		t.Errorf("checksum %#x", sum)
	}
	if !pathsEqual(s.Outline(0), f.Outline(0)) {
		t.Error(".notdef changed")
	}
	for _, tag := range []string{"cvt ", "fpgm", "prep"} {
		if len(s.tables[tag]) == 0 {
			t.Errorf("%q was dropped", tag)
		}
	}
}

// exercise touches a program the way a renderer would.
func exercise(p Program, glyphs int) {
	n := min(p.NumGlyphs(), glyphs)
	for g := 0; g < n; g++ {
		p.Outline(g)
		p.Advance(g)
	}
	p.Outline(p.NumGlyphs() - 1)
	p.Matrix()
	p.GIDByName("A")
	p.GIDByRune('A')
}

func exerciseTrueType(data []byte, glyphs int) {
	f, err := ParseTrueType(data, 0)
	if err != nil {
		return
	}
	exercise(f, glyphs)
	f.GIDByCmap(3, 1, 'A')
	f.GIDByCmap(3, 10, 0x4E2D)
	f.Cmaps()
	f.Subset([]int{1, 2, 3, 40})
}

func exerciseCFF(data []byte) {
	if c, err := ParseCFF(data); err == nil {
		exercise(c, 32)
		for code := range 256 {
			c.GIDByCode(byte(code))
		}
		c.GIDByCID(5)
		c.GlyphName(3)
	}
}

func exerciseType1(data []byte) {
	if f, err := ParseType1(data); err == nil {
		exercise(f, 32)
		f.GIDByCode('A')
		f.GlyphName(3)
	}
}

func TestTruncation(t *testing.T) {
	start := time.Now()
	data := windowsFont(t, "arial.ttf")
	step := len(data)/300 + 1
	for n := 0; n < len(data); n += step {
		exerciseTrueType(data[:n], 16)
	}
	// Short prefixes hit the header paths one byte at a time.
	for n := 0; n < 1024; n++ {
		exerciseTrueType(data[:n], 4)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestByteFlips(t *testing.T) {
	start := time.Now()
	data := windowsFont(t, "arial.ttf")
	rng := rand.New(rand.NewSource(1))
	buf := make([]byte, len(data))
	// The directory, the tables near it, then bytes anywhere.
	for i := 0; i < 400; i++ {
		copy(buf, data)
		for range 20 {
			buf[rng.Intn(4096)] = byte(rng.Intn(256))
		}
		for range 2000 {
			buf[rng.Intn(len(buf))] = byte(rng.Intn(256))
		}
		exerciseTrueType(buf, 64)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestSubsetSurvivesBadFonts(t *testing.T) {
	f := compositeFont(t)
	data, _, err := f.Subset([]int{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(2))
	buf := make([]byte, len(data))
	for range 3000 {
		copy(buf, data)
		for range 8 {
			buf[rng.Intn(len(buf))] = byte(rng.Intn(256))
		}
		exerciseTrueType(buf, 16)
	}
}

func TestHandMadeFontsSurviveDamage(t *testing.T) {
	fonts := map[string]struct {
		data []byte
		run  func([]byte)
	}{
		"cff":   {nameKeyedCFF(), exerciseCFF},
		"cid":   {cidCFF(true), exerciseCFF},
		"type1": {buildType1(t1Options{lenIV: 4, encoding: encodingArray}), exerciseType1},
		"pfb":   {buildType1(t1Options{lenIV: 0, pfb: true, encoding: encodingArray}), exerciseType1},
		"hex":   {buildType1(t1Options{lenIV: 4, hexEexec: true, encoding: encodingArray}), exerciseType1},
	}
	rng := rand.New(rand.NewSource(3))
	for name, f := range fonts {
		for n := range len(f.data) {
			f.run(f.data[:n])
		}
		buf := make([]byte, len(f.data))
		for range 8000 {
			copy(buf, f.data)
			for range 1 + rng.Intn(8) {
				buf[rng.Intn(len(buf))] = byte(rng.Intn(256))
			}
			f.run(buf)
		}
		t.Log(name, "survived")
	}
}

func TestHostileSfntHeaders(t *testing.T) {
	// A table directory claiming every table at a huge offset, and one cmap with
	// thousands of subtables pointing at one large format 12 table.
	bad := make([]byte, 12+16*2)
	binary.BigEndian.PutUint16(bad[4:], 0xFFFF)
	for i := 12; i < len(bad); i++ {
		bad[i] = 0xFF
	}
	if _, err := ParseTrueType(bad, 0); err == nil {
		t.Error("accepted a font with no tables")
	}
	ttc := []byte("ttcf\x00\x01\x00\x00\xff\xff\xff\xff\xff\xff\xff\xff")
	if _, err := ParseTrueType(ttc, 0); err == nil {
		t.Error("accepted a collection with a bad offset")
	}
	if got := parseCmaps(cmapBomb()); len(got) > maxCmapTables {
		t.Errorf("%d subtables", len(got))
	}
}

func cmapBomb() []byte {
	b := []byte{0, 0, 0xFF, 0xFF}
	for range 0xFFFF {
		b = append(b, 0, 3, 0, 10, 0, 7, 0xFF, 0xFC)
	}
	sub := []byte{0, 12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	sub = binary.BigEndian.AppendUint32(sub, 1<<20)
	return append(b, sub...)
}

func TestCmapFormats(t *testing.T) {
	// Format 0.
	f0 := make([]byte, 262)
	f0[6+65] = 3
	// Format 6: codes 100..102.
	f6 := []byte{0, 6, 0, 16, 0, 0, 0, 100, 0, 3, 0, 1, 0, 2, 0, 3}
	// Format 4: 65-66 by delta, 70-71 by glyph array, end marker.
	f4 := []byte{
		0, 4, 0, 48, 0, 0, 0, 6, 0, 4, 0, 1, 0, 2,
		0, 66, 0, 71, 0xFF, 0xFF, 0, 0,
		0, 65, 0, 70, 0xFF, 0xFF,
		0xFF, 0xC4, 0, 0, 0, 1,
		0, 0, 0, 4, 0, 0,
		0, 7, 0, 8,
	}
	// Format 12: a plane 1 range.
	f12 := []byte{0, 12, 0, 0, 0, 0, 0, 28, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 0, 1, 0, 9, 0, 0, 0, 5}
	var out []byte
	out = append(out, 0, 0, 0, 4)
	offsets := [][3]int{{1, 0, 0}, {1, 1, 0}, {3, 1, 0}, {3, 10, 0}}
	base := 4 + 8*len(offsets)
	tables := [][]byte{f0, f6, f4, f12}
	for i, o := range offsets {
		out = binary.BigEndian.AppendUint16(out, uint16(o[0]))
		out = binary.BigEndian.AppendUint16(out, uint16(o[1]))
		out = binary.BigEndian.AppendUint32(out, uint32(base))
		base += len(tables[i])
	}
	for _, b := range tables {
		out = append(out, b...)
	}
	f := &TrueType{numGlyphs: 100, cmaps: parseCmaps(out)}
	cases := []struct {
		p, e int
		code uint32
		want int
	}{
		{1, 0, 65, 3}, {1, 0, 66, 0}, {1, 0, 300, 0},
		{1, 1, 100, 1}, {1, 1, 102, 3}, {1, 1, 103, 0}, {1, 1, 99, 0},
		{3, 1, 65, 5}, {3, 1, 66, 6}, {3, 1, 70, 7}, {3, 1, 71, 8}, {3, 1, 69, 0}, {3, 1, 0x10000, 0},
		{3, 10, 0x10000, 5}, {3, 10, 0x10009, 14}, {3, 10, 0x1000A, 0}, {3, 10, 65, 0},
	}
	for _, c := range cases {
		got, ok := f.GIDByCmap(c.p, c.e, c.code)
		if got != c.want || ok != (c.want != 0) {
			t.Errorf("(%d,%d) %#x = %d %v, want %d", c.p, c.e, c.code, got, ok, c.want)
		}
	}
	if len(f.Cmaps()) != 4 {
		t.Errorf("cmaps %v", f.Cmaps())
	}
	if g, ok := f.GIDByRune(0x10000); !ok || g != 5 {
		t.Errorf("plane 1 rune = %d %v", g, ok)
	}
}

func TestMacGlyphNames(t *testing.T) {
	if len(macGlyphNames) != 258 || macGlyphNames[257] != "dcroat" || macGlyphNames[36] != "A" {
		t.Errorf("%d names", len(macGlyphNames))
	}
}
