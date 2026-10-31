package codec

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/ccitt"
)

// --- stream building -------------------------------------------------------

func be32(v int) []byte { return binary.BigEndian.AppendUint32(nil, uint32(v)) }
func be16(v int) []byte { return binary.BigEndian.AppendUint16(nil, uint16(v)) }

// segment builds a segment with a short header.
func segment(num, typ int, refs []int, data []byte) []byte {
	out := be32(num)
	out = append(out, byte(typ), byte(len(refs)<<5))
	for _, r := range refs {
		out = append(out, byte(r))
	}
	out = append(out, 1) // page association
	out = append(out, be32(len(data))...)
	return append(out, data...)
}

func pageInfo(w, h int) []byte {
	out := slices.Concat(be32(w), be32(h), be32(0), be32(0))
	return append(out, 0, 0, 0)
}

func regionInfoBytes(w, h, x, y, op int) []byte {
	return append(slices.Concat(be32(w), be32(h), be32(x), be32(y)), byte(op))
}

func atBytes(at []atPixel) []byte {
	var out []byte
	for _, p := range at {
		out = append(out, byte(int8(p.x)), byte(int8(p.y)))
	}
	return out
}

// testBitmap draws something with structure: runs, blocks and a little noise.
func testBitmap(rng *rand.Rand, w, h int) *bitmap {
	bm := newBitmap(w, h)
	for range 1 + w*h/400 {
		x, y, rw, rh := rng.IntN(w), rng.IntN(h), 1+rng.IntN(w), 1+rng.IntN(h/2+1)
		v := byte(rng.IntN(2))
		for j := y; j < min(y+rh, h); j++ {
			for i := x; i < min(x+rw, w); i++ {
				bm.pix[j*w+i] = v
			}
		}
	}
	for range w * h / 50 {
		bm.pix[rng.IntN(w*h)] ^= 1
	}
	return bm
}

func packBitmap(bm *bitmap) []byte {
	stride := (bm.w + 7) / 8
	out := make([]byte, stride*bm.h)
	for y := range bm.h {
		for x := range bm.w {
			out[y*stride+x/8] |= bm.pix[y*bm.w+x] << (7 - x%8)
		}
	}
	return out
}

func checkPage(t *testing.T, data, globals []byte, want *bitmap) {
	t.Helper()
	got, w, h, err := JBIG2(data, globals, 0)
	if err != nil {
		t.Fatal(err)
	}
	if w != want.w || h != want.h {
		t.Fatalf("size %dx%d, want %dx%d", w, h, want.w, want.h)
	}
	if !bytes.Equal(got, packBitmap(want)) {
		t.Fatal("page differs")
	}
}

// --- generic region encoder ------------------------------------------------

// genericContext forms a context pixel by pixel, the slow way, as T.88 draws them.
func genericContext(bm *bitmap, x, y, template int, at []atPixel) int {
	g := func(dx, dy int) int { return bm.at(x+dx, y+dy) }
	a := func(i int) int { return bm.at(x+at[i].x, y+at[i].y) }
	switch template {
	case 0:
		return g(-1, 0) | g(-2, 0)<<1 | g(-3, 0)<<2 | g(-4, 0)<<3 | a(0)<<4 |
			g(2, -1)<<5 | g(1, -1)<<6 | g(0, -1)<<7 | g(-1, -1)<<8 | g(-2, -1)<<9 |
			a(1)<<10 | a(2)<<11 | g(1, -2)<<12 | g(0, -2)<<13 | g(-1, -2)<<14 | a(3)<<15
	case 1:
		return g(-1, 0) | g(-2, 0)<<1 | g(-3, 0)<<2 | a(0)<<3 |
			g(2, -1)<<4 | g(1, -1)<<5 | g(0, -1)<<6 | g(-1, -1)<<7 | g(-2, -1)<<8 |
			g(2, -2)<<9 | g(1, -2)<<10 | g(0, -2)<<11 | g(-1, -2)<<12
	case 2:
		return g(-1, 0) | g(-2, 0)<<1 | a(0)<<2 |
			g(1, -1)<<3 | g(0, -1)<<4 | g(-1, -1)<<5 | g(-2, -1)<<6 |
			g(1, -2)<<7 | g(0, -2)<<8 | g(-1, -2)<<9
	}
	return g(-1, 0) | g(-2, 0)<<1 | g(-3, 0)<<2 | g(-4, 0)<<3 | a(0)<<4 |
		g(1, -1)<<5 | g(0, -1)<<6 | g(-1, -1)<<7 | g(-2, -1)<<8 | g(-3, -1)<<9
}

func encodeGeneric(e *mqEncoder, cx []uint8, bm *bitmap, template int, at []atPixel, tpgd bool, skip *bitmap) {
	ltp := 0
	sltp := [4]int{0x9B25, 0x0795, 0x00E5, 0x0195}[template]
	for y := range bm.h {
		if tpgd {
			same := 1
			for x := range bm.w {
				if bm.at(x, y) != bm.at(x, y-1) {
					same = 0
					break
				}
			}
			e.encode(&cx[sltp], ltp^same)
			ltp = same
			if same != 0 {
				continue
			}
		}
		for x := range bm.w {
			if skip != nil && skip.at(x, y) != 0 {
				continue
			}
			e.encode(&cx[genericContext(bm, x, y, template, at)], bm.at(x, y))
		}
	}
}

func genericSegmentData(bm *bitmap, x, y, op, template int, at []atPixel, tpgd bool) []byte {
	flags := byte(template << 1)
	if tpgd {
		flags |= 8
	}
	e := newMQEncoder()
	encodeGeneric(e, make([]uint8, 1<<16), bm, template, at, tpgd, nil)
	return slices.Concat(regionInfoBytes(bm.w, bm.h, x, y, op), []byte{flags}, atBytes(at), e.flushMarker())
}

func TestGenericRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	custom := [][]atPixel{
		{{-5, -2}, {7, -3}, {-8, 0}, {1, -7}},
		{{-6, -1}}, {{4, -2}}, {{-9, 0}},
	}
	for template := range 4 {
		for _, tpgd := range []bool{false, true} {
			for _, at := range [][]atPixel{genericShapes[template].defaultAT, custom[template]} {
				bm := testBitmap(rng, 97, 61)
				// Repeat some rows so typical prediction has something to do.
				for y := 10; y < 30; y++ {
					copy(bm.row(y), bm.row(9))
				}
				data := slices.Concat(
					segment(0, 48, nil, pageInfo(97, 61)),
					segment(1, 38, nil, genericSegmentData(bm, 0, 0, opOR, template, at, tpgd)),
				)
				checkPage(t, data, nil, bm)
			}
		}
	}
}

func TestPageComposition(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	at := genericShapes[0].defaultAT
	a := testBitmap(rng, 40, 30)
	b := testBitmap(rng, 40, 30)
	for op := opOR; op <= opREPLACE; op++ {
		want := newBitmap(50, 44)
		want.combine(a, 3, 5, opOR)
		want.combine(b, 8, 9, op)
		data := slices.Concat(
			segment(0, 48, nil, pageInfo(50, 44)),
			segment(1, 39, nil, genericSegmentData(a, 3, 5, opOR, 0, at, false)),
			segment(2, 38, nil, genericSegmentData(b, 8, 9, op, 0, at, false)),
			segment(3, 49, nil, nil),
		)
		checkPage(t, data, nil, want)
	}
}

func TestUnknownHeightAndLength(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	at := genericShapes[0].defaultAT
	a := testBitmap(rng, 64, 20)
	b := testBitmap(rng, 64, 14)
	want := newBitmap(64, 34)
	want.combine(a, 0, 0, opOR)
	want.combine(b, 0, 20, opOR)

	// The second region has no length and declares more rows than it holds.
	body := genericSegmentData(b, 0, 20, opOR, 0, at, false)
	binary.BigEndian.PutUint32(body[4:], 999)
	body = append(body, be32(14)...)
	seg := segment(4, 38, nil, nil)
	binary.BigEndian.PutUint32(seg[len(seg)-4:], jb2UnknownLength)
	seg = append(seg, body...)

	page := pageInfo(64, 0)
	binary.BigEndian.PutUint32(page[4:], jb2UnknownLength)
	binary.BigEndian.PutUint16(page[17:], 0x8000|20)
	data := slices.Concat(
		segment(0, 48, nil, page),
		segment(1, 38, nil, genericSegmentData(a, 0, 0, opOR, 0, at, false)),
		segment(2, 50, nil, be32(19)),
		seg,
		segment(5, 50, nil, be32(33)),
		segment(6, 49, nil, nil),
	)
	checkPage(t, data, nil, want)
}

// --- MMR ------------------------------------------------------------------

type bitWriter struct {
	out  []byte
	free int
}

func (w *bitWriter) write(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.free == 0 {
			w.out = append(w.out, 0)
			w.free = 8
		}
		w.free--
		w.out[len(w.out)-1] |= byte(v>>i&1) << w.free
	}
}

func (w *bitWriter) text(bits string) {
	for _, b := range bits {
		w.write(uint64(b-'0'), 1)
	}
}

func (w *bitWriter) align() { w.free = 0 }

func writeFaxRun(w *bitWriter, black, n int) {
	codes := faxWhite
	if black != 0 {
		codes = faxBlack
	}
	find := func(run int) {
		for _, list := range [][]faxCode{codes, faxShared} {
			for _, c := range list {
				if c.run == run {
					w.text(c.bits)
					return
				}
			}
		}
		panic("no code")
	}
	for n >= 2560 {
		find(2560)
		n -= 2560
	}
	if n >= 64 {
		find(n / 64 * 64)
		n %= 64
	}
	find(n)
}

// encodeMMR writes T.6 with all three modes.
func encodeMMR(bm *bitmap, eofb bool) []byte {
	w := &bitWriter{}
	next := func(row []byte, from, colour int) int {
		// The first position at or after from whose colour is not colour.
		for i := max(from, 0); i < bm.w; i++ {
			if int(row[i]) != colour {
				return i
			}
		}
		return bm.w
	}
	ref := make([]byte, bm.w)
	for y := range bm.h {
		row := bm.row(y)
		a0, colour := -1, 0
		for a0 < bm.w {
			a1 := next(row, a0+boolInt(a0 >= 0), colour)
			if a0 < 0 {
				a1 = next(row, 0, 0)
			}
			// b1: first changing element right of a0 of colour opposite to colour.
			b1 := max(a0+1, 0)
			for b1 < bm.w {
				left := 0
				if b1 > 0 {
					left = int(ref[b1-1])
				}
				if int(ref[b1]) != left && int(ref[b1]) != colour {
					break
				}
				b1++
			}
			b2 := bm.w
			if b1 < bm.w {
				b2 = next(ref, b1, 1-colour)
			}
			switch d := a1 - b1; {
			case b2 < a1:
				w.text("0001")
				a0 = b2
			case d >= -3 && d <= 3:
				w.text([]string{"0000010", "000010", "010", "1", "011", "000011", "0000011"}[d+3])
				a0 = a1
				colour = 1 - colour
			default:
				a2 := next(row, a1, 1-colour)
				w.text("001")
				writeFaxRun(w, colour, a1-max(a0, 0))
				writeFaxRun(w, 1-colour, a2-a1)
				a0 = a2
			}
		}
		ref = row
	}
	if eofb {
		w.text("000000000001000000000001")
	}
	return w.out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestMMRAgainstCCITT checks the code tables and the coder against x/image/ccitt.
func TestMMRAgainstCCITT(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	seen := map[[2]int]bool{}
	for _, size := range [][2]int{{31, 17}, {800, 30}, {2700, 12}, {5300, 6}} {
		bm := testBitmap(rng, size[0], size[1])
		// Runs of every length, so that every code in the tables is used.
		rows := []*bitmap{bm}
		for r := range 3 {
			long := newBitmap(size[0], 4)
			for y := range 4 {
				x, colour := 0, r%2
				for x < long.w {
					n := 1 + rng.IntN(70)
					if rng.IntN(3) == 0 {
						n = 64 * (1 + rng.IntN(45))
					}
					for i := x; i < min(x+n, long.w); i++ {
						long.pix[y*long.w+i] = byte(colour)
					}
					seen[[2]int{colour, min(n, long.w-x)}] = true
					x += n
					colour = 1 - colour
				}
			}
			rows = append(rows, long)
		}
		for _, in := range rows {
			coded := encodeMMR(in, true)
			work := budget{left: 1 << 30}
			got, used, err := decodeMMR(coded, in.w, in.h, &work)
			if err != nil {
				t.Fatal(err)
			}
			if used != len(coded) {
				t.Errorf("used %d of %d bytes", used, len(coded))
			}
			if !bytes.Equal(got.pix, in.pix) {
				t.Fatalf("own MMR decoder differs at %dx%d", in.w, in.h)
			}
			packed := make([]byte, ((in.w+7)/8)*in.h)
			r := ccitt.NewReader(bytes.NewReader(coded), ccitt.MSB, ccitt.Group4, in.w, in.h, &ccitt.Options{Invert: true, Align: false})
			n := 0
			for n < len(packed) {
				m, err := r.Read(packed[n:])
				n += m
				if err != nil {
					break
				}
			}
			want := packBitmap(in)
			if !bytes.Equal(packed, want) {
				t.Fatalf("x/image/ccitt reads the stream differently at %dx%d", in.w, in.h)
			}
		}
	}
}

// TestFaxTablesPrefixFree guards the tables typed by hand.
func TestFaxTablesPrefixFree(t *testing.T) {
	for _, list := range [][]faxCode{slices.Concat(faxWhite, faxShared), slices.Concat(faxBlack, faxShared)} {
		if len(list) != 64+27+13 {
			t.Fatalf("%d codes", len(list))
		}
		var kraft float64
		for i, a := range list {
			kraft += 1 / float64(int(1)<<len(a.bits))
			for j, b := range list {
				if i != j && strings.HasPrefix(b.bits, a.bits) {
					t.Errorf("%s is a prefix of %s", a.bits, b.bits)
				}
			}
		}
		if kraft >= 1 {
			t.Errorf("code space used %v", kraft)
		}
	}
}

func TestGenericMMRSegment(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	bm := testBitmap(rng, 123, 45)
	body := slices.Concat(regionInfoBytes(123, 45, 0, 0, opOR), []byte{1}, encodeMMR(bm, false))
	data := slices.Concat(segment(0, 48, nil, pageInfo(123, 45)), segment(1, 38, nil, body))
	checkPage(t, data, nil, bm)
}

// --- integer coding, symbols and text ---------------------------------------

func encodeInt(e *mqEncoder, cx *intContext, v int, oob bool) {
	prev := 1
	bit := func(b int) {
		e.encode(&cx[prev], b)
		if prev < 256 {
			prev = prev<<1 | b
		} else {
			prev = (prev<<1|b)&511 | 256
		}
	}
	bitsOf := func(x, n int) {
		for i := n - 1; i >= 0; i-- {
			bit(x >> i & 1)
		}
	}
	s, m := 0, v
	if v < 0 || oob {
		s, m = 1, -v
	}
	bit(s)
	switch {
	case m < 4:
		bit(0)
		bitsOf(m, 2)
	case m < 20:
		bit(1)
		bit(0)
		bitsOf(m-4, 4)
	case m < 84:
		bitsOf(6, 3)
		bitsOf(m-20, 6)
	case m < 340:
		bitsOf(14, 4)
		bitsOf(m-84, 8)
	case m < 4436:
		bitsOf(30, 5)
		bitsOf(m-340, 12)
	default:
		bitsOf(31, 5)
		bitsOf(m-4436, 32)
	}
}

func encodeIAID(e *mqEncoder, cx []uint8, id, n int) {
	prev := 1
	for i := n - 1; i >= 0; i-- {
		b := id >> i & 1
		e.encode(&cx[prev], b)
		prev = prev<<1 | b
	}
}

func TestIntegerCoding(t *testing.T) {
	values := []int{0, 1, -1, 3, 4, -4, 19, 20, 83, 84, 339, 340, 4435, 4436, -70000, 1 << 31}
	e := newMQEncoder()
	var cx intContext
	for _, v := range values {
		encodeInt(e, &cx, v, false)
	}
	encodeInt(e, &cx, 0, true)
	var d mqDecoder
	d.init(e.flushMarker())
	var dx intContext
	for _, v := range values {
		if got, oob := dx.decode(&d); got != v || oob {
			t.Errorf("got %d, want %d", got, v)
		}
	}
	if _, oob := dx.decode(&d); !oob {
		t.Error("no out-of-band value")
	}
}

// glyphs makes a set of symbols of mixed sizes in height order.
func glyphs(rng *rand.Rand, n int) []*bitmap {
	var out []*bitmap
	for i := range n {
		h := 6 + i/3
		out = append(out, testBitmap(rng, 4+rng.IntN(9), h))
	}
	return out
}

// arithDictionary encodes an arithmetic symbol dictionary exporting all of its symbols.
func arithDictionary(syms []*bitmap, template int) []byte {
	at := genericShapes[template].defaultAT
	e := newMQEncoder()
	var iadh, iadw, iaex intContext
	gb := make([]uint8, 1<<16)
	height := 0
	for i := 0; i < len(syms); {
		h := syms[i].h
		encodeInt(e, &iadh, h-height, false)
		height = h
		width := 0
		for ; i < len(syms) && syms[i].h == h; i++ {
			encodeInt(e, &iadw, syms[i].w-width, false)
			width = syms[i].w
			encodeGeneric(e, gb, syms[i], template, at, false, nil)
		}
		encodeInt(e, &iadw, 0, true)
	}
	encodeInt(e, &iaex, 0, false)
	encodeInt(e, &iaex, len(syms), false)
	flags := template << 10
	return slices.Concat(be16(flags), atBytes(at), be32(len(syms)), be32(len(syms)), e.flushMarker())
}

// instance is a symbol placed with its top left corner at x, y.
type instance struct {
	id, x, y int
	refine   *bitmap // replaces the symbol when set
	rdx, rdy int
}

// textOptions are the parameters of the text region tests.
type textOptions struct {
	corner     int
	transposed bool
	logStrips  int
	dsOffset   int
	op         int
	defPixel   int
	refine     bool
	rTemplate  int
}

// stValues gives the S value coded for an instance, its T, and the advance along S.
// The decoder adds the extent before drawing for right and bottom corners and after
// drawing for the others, so the coded S is always the low edge.
func stValues(in instance, b *bitmap, o textOptions) (s, t, along int) {
	wi, hi := b.w, b.h
	right := o.corner == cornerTopRight || o.corner == cornerBottomRight
	bottom := o.corner == cornerBottomLeft || o.corner == cornerBottomRight
	if o.transposed {
		t = in.x
		if right {
			t += wi - 1
		}
		return in.y, t, hi - 1
	}
	t = in.y
	if bottom {
		t += hi - 1
	}
	return in.x, t, wi - 1
}

// arithText encodes a text region. Instances are sorted into strips by T, then by S.
func arithText(w, h int, syms []*bitmap, list []instance, o textOptions) []byte {
	strips := 1 << o.logStrips
	type placed struct {
		instance
		bm         *bitmap
		s, t, wide int
	}
	var items []placed
	for _, in := range list {
		b := syms[in.id]
		if in.refine != nil {
			b = in.refine
		}
		s, ty, wide := stValues(in, b, o)
		items = append(items, placed{in, b, s, ty, wide})
	}
	floorDiv := func(a, b int) int {
		q := a / b
		if a%b != 0 && a < 0 {
			q--
		}
		return q
	}
	slices.SortStableFunc(items, func(a, b placed) int {
		if d := floorDiv(a.t, strips) - floorDiv(b.t, strips); d != 0 {
			return d
		}
		return a.s - b.s
	})
	codeLen := symCodeLength(len(syms))
	e := newMQEncoder()
	cx := newTextContexts(codeLen, nil)
	encodeInt(e, &cx.iadt, 0, false)
	stripT, firstS := 0, 0
	for i := 0; i < len(items); {
		strip := floorDiv(items[i].t, strips)
		encodeInt(e, &cx.iadt, strip-stripT/strips, false)
		stripT = strip * strips
		curS := 0
		for start := i; i < len(items) && floorDiv(items[i].t, strips) == strip; i++ {
			it := items[i]
			if i == start {
				encodeInt(e, &cx.iafs, it.s-firstS, false)
				firstS = it.s
			} else {
				encodeInt(e, &cx.iads, it.s-curS-o.dsOffset, false)
			}
			if strips > 1 {
				encodeInt(e, &cx.iait, it.t-stripT, false)
			}
			encodeIAID(e, cx.iaid, it.id, codeLen)
			curS = it.s
			if o.refine {
				if it.refine == nil {
					encodeInt(e, &cx.iari, 0, false)
				} else {
					base := syms[it.id]
					encodeInt(e, &cx.iari, 1, false)
					rdw, rdh := it.refine.w-base.w, it.refine.h-base.h
					encodeInt(e, &cx.iardw, rdw, false)
					encodeInt(e, &cx.iardh, rdh, false)
					encodeInt(e, &cx.iardx, it.rdx, false)
					encodeInt(e, &cx.iardy, it.rdy, false)
					encodeRefinement(e, cx.gr, it.refine, o.rTemplate, base, rdw>>1+it.rdx, rdh>>1+it.rdy, false, []atPixel{{-1, -1}, {-1, -1}})
				}
			}
			curS += it.wide
		}
		encodeInt(e, &cx.iads, 0, true)
	}
	flags := boolInt(o.refine)<<1 | o.logStrips<<2 | o.corner<<4 | boolInt(o.transposed)<<6 |
		o.op<<7 | o.defPixel<<9 | (o.dsOffset&31)<<10 | o.rTemplate<<15
	out := slices.Concat(regionInfoBytes(w, h, 0, 0, opOR), be16(flags))
	if o.refine && o.rTemplate == 0 {
		out = append(out, atBytes([]atPixel{{-1, -1}, {-1, -1}})...)
	}
	return slices.Concat(out, be32(len(items)), e.flushMarker())
}

func expectedText(w, h int, syms []*bitmap, list []instance, o textOptions) *bitmap {
	want := newBitmap(w, h)
	want.fill(byte(o.defPixel))
	for _, in := range list {
		b := syms[in.id]
		if in.refine != nil {
			b = in.refine
		}
		want.combine(b, in.x, in.y, o.op)
	}
	return want
}

func TestSymbolsAndText(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 14))
	syms := glyphs(rng, 12)
	var list []instance
	for range 40 {
		list = append(list, instance{id: rng.IntN(len(syms)), x: rng.IntN(180) - 5, y: rng.IntN(120) - 5})
	}
	for corner := range 4 {
		for _, transposed := range []bool{false, true} {
			for _, logStrips := range []int{0, 2, 3} {
				for _, dso := range []int{0, 5, -7} {
					o := textOptions{corner: corner, transposed: transposed, logStrips: logStrips, dsOffset: dso}
					data := slices.Concat(
						segment(0, 48, nil, pageInfo(200, 140)),
						segment(1, 0, nil, arithDictionary(syms, 0)),
						segment(2, 6, []int{1}, arithText(200, 140, syms, list, o)),
					)
					checkPage(t, data, nil, expectedText(200, 140, syms, list, o))
				}
			}
		}
	}
}

func TestTextOptions(t *testing.T) {
	rng := rand.New(rand.NewPCG(15, 16))
	syms := glyphs(rng, 9)
	var list []instance
	for range 25 {
		list = append(list, instance{id: rng.IntN(len(syms)), x: rng.IntN(90), y: rng.IntN(60)})
	}
	for _, template := range []int{0, 1, 2, 3} {
		for op := opOR; op <= opXNOR; op++ {
			for def := range 2 {
				o := textOptions{corner: cornerTopLeft, op: op, defPixel: def}
				data := slices.Concat(
					segment(0, 48, nil, pageInfo(100, 70)),
					segment(1, 0, nil, arithDictionary(syms, template)),
					segment(2, 7, []int{1}, arithText(100, 70, syms, list, o)),
				)
				checkPage(t, data, nil, expectedText(100, 70, syms, list, o))
			}
		}
	}
}

func TestGlobalsAndTwoDictionaries(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 18))
	a := glyphs(rng, 6)
	b := glyphs(rng, 5)
	all := slices.Concat(a, b)
	var list []instance
	for range 20 {
		list = append(list, instance{id: rng.IntN(len(all)), x: rng.IntN(70), y: rng.IntN(50)})
	}
	o := textOptions{corner: cornerTopLeft}
	globals := segment(0, 0, nil, arithDictionary(a, 0))
	data := slices.Concat(
		segment(1, 48, nil, pageInfo(80, 60)),
		segment(2, 0, nil, arithDictionary(b, 0)),
		segment(3, 6, []int{0, 2}, arithText(80, 60, all, list, o)),
	)
	checkPage(t, data, globals, expectedText(80, 60, all, list, o))
	if _, _, _, err := JBIG2(data, nil, 0); err == nil {
		t.Error("a missing dictionary went unnoticed")
	}
}

// --- refinement --------------------------------------------------------------

func refinementContext(bm, ref *bitmap, x, y, rx, ry, template int, at []atPixel) int {
	g := func(dx, dy int) int { return bm.at(x+dx, y+dy) }
	r := func(dx, dy int) int { return ref.at(rx+dx, ry+dy) }
	if template == 0 {
		return g(-1, 0) | g(1, -1)<<1 | g(0, -1)<<2 | g(at[0].x, at[0].y)<<3 |
			r(1, 1)<<4 | r(0, 1)<<5 | r(-1, 1)<<6 | r(1, 0)<<7 | r(0, 0)<<8 | r(-1, 0)<<9 |
			r(1, -1)<<10 | r(0, -1)<<11 | r(at[1].x, at[1].y)<<12
	}
	return g(-1, 0) | g(1, -1)<<1 | g(0, -1)<<2 | g(-1, -1)<<3 |
		r(1, 1)<<4 | r(0, 1)<<5 | r(1, 0)<<6 | r(0, 0)<<7 | r(-1, 0)<<8 | r(0, -1)<<9
}

func encodeRefinement(e *mqEncoder, cx []uint8, bm *bitmap, template int, ref *bitmap, dx, dy int, tpgr bool, at []atPixel) {
	uniform := func(rx, ry int) (int, bool) {
		v := ref.at(rx, ry)
		for j := -1; j <= 1; j++ {
			for i := -1; i <= 1; i++ {
				if ref.at(rx+i, ry+j) != v {
					return 0, false
				}
			}
		}
		return v, true
	}
	sltp := 0x100
	if template == 1 {
		sltp = 0x80
	}
	ltp := 0
	for y := range bm.h {
		typical := 0
		if tpgr {
			typical = 1
			for x := range bm.w {
				if v, ok := uniform(x-dx, y-dy); ok && v != bm.at(x, y) {
					typical = 0
					break
				}
			}
			e.encode(&cx[sltp], ltp^typical)
			ltp = typical
		}
		for x := range bm.w {
			if typical != 0 {
				if _, ok := uniform(x-dx, y-dy); ok {
					continue
				}
			}
			e.encode(&cx[refinementContext(bm, ref, x, y, x-dx, y-dy, template, at)], bm.at(x, y))
		}
	}
}

func refinementData(bm, ref *bitmap, x, y, op, template int, tpgr bool, at []atPixel) []byte {
	e := newMQEncoder()
	encodeRefinement(e, make([]uint8, 1<<13), bm, template, ref, 0, 0, tpgr, at)
	flags := byte(template)
	if tpgr {
		flags |= 2
	}
	out := append(regionInfoBytes(bm.w, bm.h, x, y, op), flags)
	if template == 0 {
		out = append(out, atBytes(at)...)
	}
	return append(out, e.flushMarker()...)
}

func TestRefinementRegion(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 20))
	at0 := genericShapes[0].defaultAT
	ats := []atPixel{{-1, -1}, {-1, -1}}
	for template := range 2 {
		for _, tpgr := range []bool{false, true} {
			base := testBitmap(rng, 70, 50)
			fine := newBitmap(70, 50)
			copy(fine.pix, base.pix)
			for range 150 {
				fine.pix[rng.IntN(len(fine.pix))] ^= 1
			}
			// With an intermediate region as the reference.
			data := slices.Concat(
				segment(0, 48, nil, pageInfo(70, 50)),
				segment(1, 36, nil, genericSegmentData(base, 0, 0, opOR, 0, at0, false)),
				segment(2, 42, []int{1}, refinementData(fine, base, 0, 0, opREPLACE, template, tpgr, ats)),
			)
			checkPage(t, data, nil, fine)
			// With the page as the reference.
			data = slices.Concat(
				segment(0, 48, nil, pageInfo(70, 50)),
				segment(1, 38, nil, genericSegmentData(base, 0, 0, opOR, 0, at0, false)),
				segment(2, 43, nil, refinementData(fine, base, 0, 0, opREPLACE, template, tpgr, ats)),
			)
			checkPage(t, data, nil, fine)
		}
	}
}

func TestRefinedTextInstances(t *testing.T) {
	rng := rand.New(rand.NewPCG(21, 22))
	syms := glyphs(rng, 6)
	for template := range 2 {
		var list []instance
		for i := range 12 {
			in := instance{id: rng.IntN(len(syms)), x: 5 + rng.IntN(70), y: 5 + rng.IntN(40)}
			if i%3 != 0 {
				in.refine = testBitmap(rng, syms[in.id].w+rng.IntN(5)-2, syms[in.id].h+rng.IntN(5)-2)
				in.rdx, in.rdy = rng.IntN(5)-2, rng.IntN(5)-2
			}
			list = append(list, in)
		}
		o := textOptions{corner: cornerTopLeft, refine: true, rTemplate: template}
		data := slices.Concat(
			segment(0, 48, nil, pageInfo(90, 60)),
			segment(1, 0, nil, arithDictionary(syms, 0)),
			segment(2, 6, []int{1}, arithText(90, 60, syms, list, o)),
		)
		checkPage(t, data, nil, expectedText(90, 60, syms, list, o))
	}
}

// aggregateDictionary refines symbols of an earlier dictionary one by one.
func aggregateDictionary(in []*bitmap, out []*bitmap, ids []int, rTemplate int) []byte {
	at := genericShapes[0].defaultAT
	rAT := []atPixel{{-1, -1}, {-1, -1}}
	e := newMQEncoder()
	var iadh, iadw, iaai, iaex intContext
	codeLen := symCodeLength(len(in) + len(out))
	cx := newTextContexts(codeLen, nil)
	height := 0
	for i := 0; i < len(out); {
		h := out[i].h
		encodeInt(e, &iadh, h-height, false)
		height = h
		width := 0
		for ; i < len(out) && out[i].h == h; i++ {
			encodeInt(e, &iadw, out[i].w-width, false)
			width = out[i].w
			encodeInt(e, &iaai, 1, false)
			encodeIAID(e, cx.iaid, ids[i], codeLen)
			encodeInt(e, &cx.iardx, 1, false)
			encodeInt(e, &cx.iardy, -1, false)
			encodeRefinement(e, cx.gr, out[i], rTemplate, in[ids[i]], 1, -1, false, rAT)
		}
		encodeInt(e, &iadw, 0, true)
	}
	encodeInt(e, &iaex, len(in), false)
	encodeInt(e, &iaex, len(out), false)
	flags := 2 | rTemplate<<12
	hdr := slices.Concat(be16(flags), atBytes(at))
	if rTemplate == 0 {
		hdr = append(hdr, atBytes(rAT)...)
	}
	return slices.Concat(hdr, be32(len(out)), be32(len(out)), e.flushMarker())
}

func TestAggregateDictionary(t *testing.T) {
	rng := rand.New(rand.NewPCG(23, 24))
	base := glyphs(rng, 5)
	for template := range 2 {
		var out []*bitmap
		var ids []int
		for i := range 4 {
			ids = append(ids, i)
			out = append(out, testBitmap(rng, base[i].w+1, 9))
		}
		var list []instance
		for i := range 12 {
			list = append(list, instance{id: i % 4, x: i * 6, y: 2 + i%3})
		}
		o := textOptions{corner: cornerTopLeft}
		data := slices.Concat(
			segment(0, 48, nil, pageInfo(90, 30)),
			segment(1, 0, nil, arithDictionary(base, 0)),
			segment(2, 0, []int{1}, aggregateDictionary(base, out, ids, template)),
			segment(3, 6, []int{2}, arithText(90, 30, out, list, o)),
		)
		checkPage(t, data, nil, expectedText(90, 30, out, list, o))
	}
}

// --- halftone ----------------------------------------------------------------

func patternData(pats []*bitmap, template int, mmr bool) []byte {
	pw, ph := pats[0].w, pats[0].h
	sheet := newBitmap(pw*len(pats), ph)
	for i, p := range pats {
		sheet.combine(p, i*pw, 0, opREPLACE)
	}
	flags := byte(template<<1) | byte(boolInt(mmr))
	head := append([]byte{flags, byte(pw), byte(ph)}, be32(len(pats)-1)...)
	if mmr {
		return append(head, encodeMMR(sheet, false)...)
	}
	at := append([]atPixel{{-pw, 0}}, genericShapes[0].defaultAT[1:]...)
	if template != 0 {
		at = at[:1]
	}
	e := newMQEncoder()
	encodeGeneric(e, make([]uint8, 1<<16), sheet, template, at, false, nil)
	return append(head, e.flushMarker()...)
}

func TestHalftone(t *testing.T) {
	rng := rand.New(rand.NewPCG(25, 26))
	const w, h, gw, gh = 96, 80, 13, 11
	pats := make([]*bitmap, 7)
	for i := range pats {
		pats[i] = newBitmap(8, 8)
		for range i * 9 {
			pats[i].pix[rng.IntN(64)] = 1
		}
	}
	const gx, gy, rx, ry = 256 * 2, 256 * 40, 5 * 256, 3 * 256
	for _, mmr := range []bool{false, true} {
		for _, skipOn := range []bool{false, true} {
			if mmr && skipOn {
				continue
			}
			for template := range 4 {
				gray := make([]int, gw*gh)
				for i := range gray {
					gray[i] = rng.IntN(len(pats))
				}
				want := newBitmap(w, h)
				skip := newBitmap(gw, gh)
				for m := range gh {
					for n := range gw {
						x := (gx + m*ry + n*rx) >> 8
						y := (gy + m*rx - n*ry) >> 8
						want.combine(pats[gray[m*gw+n]], x, y, opOR)
						if x+8 <= 0 || x >= w || y+8 <= 0 || y >= h {
							skip.pix[m*gw+n] = 1
							if skipOn {
								gray[m*gw+n] = 0
							}
						}
					}
				}
				at := append([]atPixel{{[4]int{3, 3, 2, 2}[template], -1}}, genericShapes[0].defaultAT[1:]...)
				if template != 0 {
					at = at[:1]
				}
				e := newMQEncoder()
				cx := make([]uint8, 1<<16)
				var mmrData []byte
				const bpp = 3
				var prev *bitmap
				for j := bpp - 1; j >= 0; j-- {
					plane := newBitmap(gw, gh)
					for i, v := range gray {
						plane.pix[i] = byte(v >> j & 1)
					}
					coded := newBitmap(gw, gh)
					copy(coded.pix, plane.pix)
					if prev != nil {
						for i := range coded.pix {
							coded.pix[i] ^= prev.pix[i]
						}
					}
					prev = plane
					if mmr {
						mmrData = append(mmrData, encodeMMR(coded, true)...)
						continue
					}
					mask := skip
					if !skipOn {
						mask = nil
					}
					encodeGeneric(e, cx, coded, template, at, false, mask)
				}
				flags := byte(template<<1) | byte(boolInt(mmr)) | byte(boolInt(skipOn))<<3
				body := slices.Concat(regionInfoBytes(w, h, 0, 0, opOR), []byte{flags},
					be32(gw), be32(gh), be32(gx), be32(gy), be16(rx), be16(ry))
				if mmr {
					body = append(body, mmrData...)
				} else {
					body = append(body, e.flushMarker()...)
				}
				data := slices.Concat(
					segment(0, 48, nil, pageInfo(w, h)),
					segment(1, 16, nil, patternData(pats, template, mmr)),
					segment(2, 22, []int{1}, body),
				)
				checkPage(t, data, nil, want)
			}
		}
	}
}

// --- Huffman -----------------------------------------------------------------

// putHuff writes value v (or the out-of-band code) with table t.
func putHuff(w *bitWriter, t *huffTable, v int, oob bool) {
	for i := range t.lines {
		l := &t.lines[i]
		hit := false
		var extra uint64
		switch l.kind {
		case huffOOB:
			hit = oob
		case huffLower:
			if !oob && v <= l.low {
				hit, extra = true, uint64(l.low-v)
			}
		case huffUpper:
			if !oob && v >= l.low {
				hit, extra = true, uint64(v-l.low)
			}
		default:
			if !oob && v >= l.low && v < l.low+1<<l.rlen {
				hit, extra = true, uint64(v-l.low)
			}
		}
		if hit {
			off := uint32(i) - t.offset[l.plen]
			w.write(t.first[l.plen]+uint64(off), l.plen)
			w.write(extra, l.rlen)
			return
		}
	}
	panic("value not in table")
}

// The printed prefix codes of annex B, in line order.
var huffPrinted = [15][]int{
	{0x0, 0x2, 0x6, 0x7},
	{0x0, 0x2, 0x6, 0xe, 0x1e, 0x3e, 0x3f},
	{0xfe, 0x0, 0x2, 0x6, 0xe, 0x1e, 0xff, 0x7e, 0x3e},
	{0x0, 0x2, 0x6, 0xe, 0x1e, 0x1f},
	{0x7e, 0x0, 0x2, 0x6, 0xe, 0x1e, 0x7f, 0x3e},
	{0x1c, 0x8, 0x9, 0xa, 0x1d, 0x1e, 0xb, 0x0, 0x2, 0x3, 0xc, 0xd, 0x3e, 0x3f},
	{0x8, 0x0, 0x9, 0x1a, 0x1b, 0xa, 0xb, 0x1c, 0x1d, 0xc, 0x1, 0x2, 0x3, 0x1e, 0x1f},
	{0xfc, 0x1fc, 0xfd, 0x1fd, 0x7c, 0xa, 0x0, 0x1a, 0x3a, 0x4, 0x3b, 0xb, 0xc, 0x1b, 0x1c, 0x3c, 0x7d, 0x3d, 0x1fe, 0x1ff, 0x1},
	{0xfc, 0x1fc, 0xfd, 0x1fd, 0x7c, 0xa, 0x2, 0x3, 0x1a, 0x3a, 0x4, 0x3b, 0xb, 0xc, 0x1b, 0x1c, 0x3c, 0x7d, 0x3d, 0x1fe, 0x1ff, 0x0},
	{0x7a, 0xfc, 0x7b, 0x18, 0x0, 0x19, 0x36, 0x7c, 0xfd, 0x1, 0x1a, 0x37, 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x7d, 0xfe, 0xff, 0x2},
	{0x0, 0x2, 0xc, 0xd, 0x1c, 0x1d, 0x3c, 0x7a, 0x7b, 0x7c, 0x7d, 0x7e, 0x7f},
	{0x0, 0x2, 0x6, 0x1c, 0x1d, 0x3c, 0x7a, 0x7b, 0x7c, 0x7d, 0x7e, 0xfe, 0xff},
	{0x0, 0x4, 0xc, 0x1c, 0xd, 0x5, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x7e, 0x7f},
	{0x4, 0x5, 0x0, 0x6, 0x7},
	{0x7c, 0x3c, 0x1c, 0xc, 0x4, 0x0, 0x5, 0xd, 0x1d, 0x3d, 0x7d, 0x7e, 0x7f},
}

func TestHuffmanStandardTables(t *testing.T) {
	for n, lines := range huffStandard {
		tbl := standardTable(n + 1)
		var kraft float64
		for i, l := range lines {
			kraft += 1 / float64(int(1)<<l.plen)
			// Find the line in the sorted table to read its code.
			pos := slices.IndexFunc(tbl.lines, func(x huffLine) bool { return x == l })
			code := tbl.first[l.plen] + uint64(uint32(pos)-tbl.offset[l.plen])
			if int(code) != huffPrinted[n][i] {
				t.Errorf("table B.%d line %d: code %#x, printed %#x", n+1, i, code, huffPrinted[n][i])
			}
		}
		if kraft != 1 {
			t.Errorf("table B.%d is not complete: %v", n+1, kraft)
		}
		// Round trip of the edges of every line.
		w := &bitWriter{}
		var values []int
		for _, l := range lines {
			switch l.kind {
			case huffNormal:
				values = append(values, l.low, l.low+1<<l.rlen-1)
			case huffLower:
				values = append(values, l.low, l.low-77777)
			case huffUpper:
				values = append(values, l.low, l.low+77777)
			}
		}
		for _, v := range values {
			putHuff(w, tbl, v, false)
		}
		r := bitReader{data: w.out}
		for _, v := range values {
			got, oob, err := tbl.decode(&r)
			if err != nil || oob || got != v {
				t.Fatalf("table B.%d: got %d %v %v, want %d", n+1, got, oob, err, v)
			}
		}
	}
}

func TestHuffmanRejectsOversubscribed(t *testing.T) {
	if _, err := newHuffTable([]huffLine{{0, 1, 0, 0}, {1, 1, 0, 0}, {2, 1, 0, 0}}); err == nil {
		t.Error("accepted an over-subscribed code")
	}
}

// putSymbolIDCodes writes the code length table of 7.4.3.1.7 with a mix of run codes,
// and returns the table of the symbol IDs.
func putSymbolIDCodes(w *bitWriter, lengths []int) *huffTable {
	// Run code lengths: every code gets 6 bits, which is a valid incomplete code.
	runs := make([]huffLine, 35)
	for i := range runs {
		runs[i] = huffLine{low: i, plen: 6}
		w.write(6, 4)
	}
	rt, err := newHuffTable(runs)
	if err != nil {
		panic(err)
	}
	for i := 0; i < len(lengths); {
		j := i
		for j < len(lengths) && lengths[j] == lengths[i] {
			j++
		}
		n := j - i
		switch {
		case lengths[i] == 0 && n >= 11:
			n = min(n, 138)
			putHuff(w, rt, 34, false)
			w.write(uint64(n-11), 7)
		case lengths[i] == 0 && n >= 3:
			n = min(n, 10)
			putHuff(w, rt, 33, false)
			w.write(uint64(n-3), 3)
		case i > 0 && lengths[i-1] == lengths[i] && n >= 3:
			n = min(n, 6)
			putHuff(w, rt, 32, false)
			w.write(uint64(n-3), 2)
		default:
			n = 1
			putHuff(w, rt, lengths[i], false)
		}
		i += n
	}
	w.align()
	lines := make([]huffLine, len(lengths))
	for i, l := range lengths {
		lines[i] = huffLine{low: i, plen: l}
	}
	t, err := newHuffTable(lines)
	if err != nil {
		panic(err)
	}
	return t
}

// huffDictionary encodes a Huffman symbol dictionary with collective bitmaps.
func huffDictionary(syms []*bitmap, mmr bool) []byte {
	w := &bitWriter{}
	dh, dw, bm, ex := standardTable(4), standardTable(3), standardTable(1), standardTable(1)
	height := 0
	for i := 0; i < len(syms); {
		h := syms[i].h
		putHuff(w, dh, h-height, false)
		height = h
		width, total := 0, 0
		first := i
		for ; i < len(syms) && syms[i].h == h; i++ {
			putHuff(w, dw, syms[i].w-width, false)
			width = syms[i].w
			total += width
		}
		putHuff(w, dw, 0, true)
		sheet := newBitmap(total, h)
		x := 0
		for _, s := range syms[first:i] {
			sheet.combine(s, x, 0, opREPLACE)
			x += s.w
		}
		var body []byte
		if mmr {
			body = encodeMMR(sheet, false)
			putHuff(w, bm, len(body), false)
		} else {
			body = packBitmap(sheet)
			putHuff(w, bm, 0, false)
		}
		w.align()
		w.out = append(w.out, body...)
	}
	putHuff(w, ex, 0, false)
	putHuff(w, ex, len(syms), false)
	return slices.Concat(be16(1|1<<4), be32(len(syms)), be32(len(syms)), w.out)
}

// huffText encodes a Huffman text region, top left corner, one strip height.
func huffText(wd, ht int, syms []*bitmap, list []instance, tables []*huffTable, flagsHuff int) []byte {
	lengths := make([]int, len(syms))
	for i := range lengths {
		lengths[i] = symCodeLength(len(syms)) + 1
	}
	lengths[0] = 1 // a run of equal lengths and a single different one
	w := &bitWriter{}
	ids := putSymbolIDCodes(w, lengths)
	fs, ds, dt := tables[0], tables[1], tables[2]
	sorted := slices.Clone(list)
	slices.SortStableFunc(sorted, func(a, b instance) int {
		if a.y != b.y {
			return a.y - b.y
		}
		return a.x - b.x
	})
	putHuff(w, dt, 1, false) // initial STRIPT of -1, in a table that starts at 1
	stripT, firstS := -1, 0
	for i := 0; i < len(sorted); {
		y := sorted[i].y
		putHuff(w, dt, y-stripT, false)
		stripT = y
		curS := 0
		for start := i; i < len(sorted) && sorted[i].y == y; i++ {
			it := sorted[i]
			if i == start {
				putHuff(w, fs, it.x-firstS, false)
				firstS = it.x
			} else {
				putHuff(w, ds, it.x-curS, false)
			}
			putHuff(w, ids, it.id, false)
			curS = it.x + syms[it.id].w - 1
		}
		putHuff(w, ds, 0, true)
	}
	flags := 1 | cornerTopLeft<<4
	return slices.Concat(regionInfoBytes(wd, ht, 0, 0, opOR), be16(flags), be16(flagsHuff), be32(len(sorted)), w.out)
}

func TestHuffmanSymbolsAndText(t *testing.T) {
	rng := rand.New(rand.NewPCG(27, 28))
	for _, mmr := range []bool{false, true} {
		syms := glyphs(rng, 11)
		var list []instance
		for i := range 30 {
			list = append(list, instance{id: rng.IntN(len(syms)), x: rng.IntN(150), y: 2 + i%9*11})
		}
		o := textOptions{corner: cornerTopLeft}
		tables := []*huffTable{standardTable(6), standardTable(8), standardTable(11)}
		data := slices.Concat(
			segment(0, 48, nil, pageInfo(170, 120)),
			segment(1, 0, nil, huffDictionary(syms, mmr)),
			segment(2, 6, []int{1}, huffText(170, 120, syms, list, tables, 0)),
		)
		checkPage(t, data, nil, expectedText(170, 120, syms, list, o))

		// The same with the other standard tables.
		tables = []*huffTable{standardTable(7), standardTable(9), standardTable(12)}
		data = slices.Concat(
			segment(0, 48, nil, pageInfo(170, 120)),
			segment(1, 0, nil, huffDictionary(syms, mmr)),
			segment(2, 6, []int{1}, huffText(170, 120, syms, list, tables, 1|1<<2|1<<4)),
		)
		checkPage(t, data, nil, expectedText(170, 120, syms, list, o))
	}
}

// customTable encodes a table segment with the lines of a small DT table.
func customTable() ([]byte, *huffTable) {
	lines := []huffLine{
		{1, 2, 3, 0}, {9, 2, 4, 0}, {25, 3, 5, 0}, {57, 3, 7, 0},
		{0, 3, 32, huffLower}, {185, 3, 32, huffUpper},
	}
	w := &bitWriter{}
	for _, l := range lines[:4] {
		w.write(uint64(l.plen), 4)
		w.write(uint64(l.rlen), 4)
	}
	w.write(3, 4)
	w.write(3, 4)
	head := slices.Concat([]byte{3<<1 | 3<<4}, be32(1), be32(185))
	t, err := newHuffTable(lines)
	if err != nil {
		panic(err)
	}
	return append(head, w.out...), t
}

func TestCustomHuffmanTable(t *testing.T) {
	rng := rand.New(rand.NewPCG(29, 30))
	syms := glyphs(rng, 7)
	var list []instance
	for i := range 20 {
		list = append(list, instance{id: rng.IntN(len(syms)), x: rng.IntN(100), y: 2 + i%5*13})
	}
	seg, table := customTable()
	tables := []*huffTable{standardTable(6), standardTable(8), table}
	o := textOptions{corner: cornerTopLeft}
	data := slices.Concat(
		segment(0, 48, nil, pageInfo(120, 80)),
		segment(1, 0, nil, huffDictionary(syms, true)),
		segment(2, 53, nil, seg),
		segment(3, 6, []int{1, 2}, huffText(120, 80, syms, list, tables, 3<<4)),
	)
	checkPage(t, data, nil, expectedText(120, 80, syms, list, o))
}

// --- hostile input -----------------------------------------------------------

func TestJBIG2Refuses(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 32))
	at := genericShapes[0].defaultAT
	bm := testBitmap(rng, 20, 10)
	page := segment(0, 48, nil, pageInfo(20, 10))
	good := genericSegmentData(bm, 0, 0, opOR, 0, at, false)

	colour := slices.Clone(good)
	colour[16] |= 8
	extended := slices.Clone(good)
	extended[17] |= 16
	cases := map[string][]byte{
		"no page":             segment(0, 49, nil, nil),
		"region first":        segment(0, 38, nil, good),
		"unknown segment":     slices.Concat(page, segment(1, 30, nil, nil)),
		"colour extension":    slices.Concat(page, segment(1, 38, nil, colour)),
		"extended template":   slices.Concat(page, segment(1, 38, nil, extended)),
		"necessary extension": slices.Concat(page, segment(1, 62, nil, be32(0x40000000))),
		"random access":       append(slices.Clone(jb2Magic), 0, 0, 0, 0, 1),
		"huge page":           segment(0, 48, nil, pageInfo(1<<20, 1<<20)),
		"zero page":           segment(0, 48, nil, pageInfo(0, 10)),
		"missing table":       slices.Concat(page, segment(1, 0, nil, slices.Concat(be16(3<<2|1), be32(0), be32(0)))),
	}
	for name, data := range cases {
		if _, _, _, err := JBIG2(data, nil, 0); err == nil {
			t.Errorf("%s: no error", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
}

func TestJBIG2PixelLimit(t *testing.T) {
	data := segment(0, 48, nil, pageInfo(100, 100))
	if _, _, _, err := JBIG2(data, nil, 9999); err == nil {
		t.Error("page over the limit accepted")
	}
	got, w, h, err := JBIG2(data, nil, 10000)
	if err != nil || w != 100 || h != 100 || len(got) != 1300 {
		t.Errorf("%d %d %d %v", len(got), w, h, err)
	}
}

// TestJBIG2WorkBudget feeds streams that claim far more work than their bytes pay for.
func TestJBIG2WorkBudget(t *testing.T) {
	page := segment(0, 48, nil, pageInfo(3000, 3000))
	// One small generic region repeated tens of thousands of times over the whole page.
	body := slices.Concat(regionInfoBytes(3000, 3000, 0, 0, opOR), []byte{0}, atBytes(genericShapes[0].defaultAT))
	var data []byte
	data = append(data, page...)
	for i := 1; i < 256; i++ {
		data = append(data, segment(i, 38, nil, body)...)
	}
	start := time.Now()
	if _, _, _, err := JBIG2(data, nil, 9_000_000); err == nil {
		t.Error("no work limit")
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("took %v", d)
	}
	// A text region with four billion instances of nothing.
	text := slices.Concat(regionInfoBytes(10, 10, 0, 0, opOR), be16(0), be32(0xFFFFFFFF), make([]byte, 40))
	dict := segment(1, 0, nil, slices.Concat(be16(0), atBytes(genericShapes[0].defaultAT), be32(1), be32(1), make([]byte, 40)))
	data = slices.Concat(segment(0, 48, nil, pageInfo(10, 10)), dict, segment(2, 6, []int{1}, text))
	start = time.Now()
	JBIG2(data, nil, 1000) // any end will do
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v", d)
	}
}

// jbig2Samples returns one stream of each kind for the damage tests.
func jbig2Samples() [][]byte {
	rng := rand.New(rand.NewPCG(33, 34))
	at := genericShapes[0].defaultAT
	syms := glyphs(rng, 8)
	var list []instance
	for i := range 16 {
		list = append(list, instance{id: rng.IntN(len(syms)), x: rng.IntN(60), y: 2 + i%4*11})
	}
	o := textOptions{corner: cornerTopLeft}
	bm := testBitmap(rng, 64, 48)
	refined := testBitmap(rng, 64, 48)
	pats := make([]*bitmap, 4)
	for i := range pats {
		pats[i] = testBitmap(rng, 6, 6)
	}
	seg, table := customTable()
	gray := slices.Concat(regionInfoBytes(64, 48, 0, 0, opOR), []byte{0}, be32(8), be32(6), be32(0), be32(0), be16(1536), be16(0))
	e := newMQEncoder()
	encodeGeneric(e, make([]uint8, 1<<16), newBitmap(8, 6), 0, at, false, nil)
	encodeGeneric(e, make([]uint8, 1<<16), testBitmap(rng, 8, 6), 0, at, false, nil)
	gray = append(gray, e.flushMarker()...)
	return [][]byte{
		slices.Concat(segment(0, 48, nil, pageInfo(64, 48)), segment(1, 38, nil, genericSegmentData(bm, 0, 0, opOR, 0, at, true))),
		slices.Concat(segment(0, 48, nil, pageInfo(64, 48)), segment(1, 38, nil, slices.Concat(regionInfoBytes(64, 48, 0, 0, opOR), []byte{1}, encodeMMR(bm, true)))),
		slices.Concat(segment(0, 48, nil, pageInfo(80, 60)), segment(1, 0, nil, arithDictionary(syms, 0)), segment(2, 6, []int{1}, arithText(80, 60, syms, list, o))),
		slices.Concat(segment(0, 48, nil, pageInfo(80, 60)), segment(1, 0, nil, huffDictionary(syms, true)), segment(2, 53, nil, seg),
			segment(3, 6, []int{1, 2}, huffText(80, 60, syms, list, []*huffTable{standardTable(6), standardTable(8), table}, 3<<4))),
		slices.Concat(segment(0, 48, nil, pageInfo(64, 48)), segment(1, 36, nil, genericSegmentData(bm, 0, 0, opOR, 0, at, false)),
			segment(2, 42, []int{1}, refinementData(refined, bm, 0, 0, opREPLACE, 0, true, []atPixel{{-1, -1}, {-1, -1}}))),
		slices.Concat(segment(0, 48, nil, pageInfo(64, 48)), segment(1, 16, nil, patternData(pats, 0, false)), segment(2, 22, []int{1}, gray)),
	}
}

func TestJBIG2Fuzz(t *testing.T) {
	rng := rand.New(rand.NewPCG(35, 36))
	check := func(data []byte) {
		start := time.Now()
		out, w, h, err := JBIG2(data, nil, 1<<20)
		if err == nil && len(out) != (w+7)/8*h {
			t.Fatalf("wrong buffer size for %dx%d", w, h)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("one decode took %v", d)
		}
	}
	for _, seed := range jbig2Samples() {
		for i := range seed {
			check(seed[:i])
		}
		for range 3000 {
			data := bytes.Clone(seed)
			for range 1 + rng.IntN(6) {
				data[rng.IntN(len(data))] = byte(rng.Uint32())
			}
			check(data)
		}
	}
}

// TestAnnexH1 decodes the first page of the worked example of T.88 annex H.1, typed from
// memory: a Huffman dictionary with "P", an arithmetic one with "c" and "a", and a Huffman
// text region spelling "caPac". Random errors in the bytes could not produce these glyphs.
func TestAnnexH1(t *testing.T) {
	segments := []string{
		"00000000 00 01 00 00000018 0001 00000001 00000001 E9CBF40026AF04BFF0782FE00040",
		"00000001 30 00 01 00000013 00000040 00000038 00000000 00000000 01 0000",
		"00000002 00 01 01 0000001B 0800 02FF 00000002 00000002 4FE78C200E1DC7CF0111C4B26FFFAC",
		"00000003 07 42 00 02 01 00000031 00000025 00000008 00000004 00000001 00 0C09 0010 00000005" +
			" 0110 000000000000000000000000000000 0C40 0708 7041 D0",
	}
	var data []byte
	for _, s := range segments {
		b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, b...)
	}
	want := []string{
		" ####    ####   ####    ####    #### ",
		"#    #       #  #   #       #  #    #",
		"#        #####  #   #   #####  #     ",
		"#       #    #  #   #  #    #  #     ",
		"#    #  #    #  ####   #    #  #    #",
		" ####    #####  #       #####   #### ",
		"                #                    ",
		"                #                    ",
	}
	page := newBitmap(64, 56)
	for y, row := range want {
		for x, c := range row {
			if c == '#' {
				page.pix[(y+1)*64+x+4] = 1
			}
		}
	}
	checkPage(t, data, nil, page)
}
