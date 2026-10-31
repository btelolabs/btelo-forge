package codec

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// planeOf draws a test plane of the given size and depth.
func planeOf(w, h, depth, seed int) []int32 {
	rng := rand.New(rand.NewPCG(uint64(seed), 99))
	top := 1<<depth - 1
	out := make([]int32, w*h)
	for y := range h {
		for x := range w {
			v := (x*top/max(w-1, 1) + y*top/max(h-1, 1)) / 2
			v += int(float64(top) / 5 * math.Sin(float64(x*seed)/5) * math.Cos(float64(y)/3))
			if x > w/3 && x < w/2 && y > h/4 && y < h/2 {
				v = top - v/8
			}
			v += rng.IntN(1 + top/40)
			out[y*w+x] = int32(min(max(v, 0), top))
		}
	}
	return out
}

// interleaved packs planes of equal size into the 8 bit output order.
func interleaved(planes ...[]int32) []byte {
	n := len(planes)
	out := make([]byte, len(planes[0])*n)
	for c, p := range planes {
		for i, v := range p {
			out[i*n+c] = byte(v)
		}
	}
	return out
}

func baseOptions(w, h, planes int) encOptions {
	o := encOptions{w: w, h: h, depth: 8, levels: 3, xcb: 5, ycb: 5, reversible: true}
	for c := range planes {
		o.comps = append(o.comps, encComponent{1, 1, planeOf(w, h, 8, c+2)})
	}
	return o
}

func (o *encOptions) planes() [][]int32 {
	var out [][]int32
	for _, c := range o.comps {
		out = append(out, c.plane)
	}
	return out
}

// styleCases are the code block styles Pillow cannot ask for.
var styleCases = []struct {
	name  string
	style int
}{
	{"plain", 0},
	{"bypass", styleBypass},
	{"reset", styleReset},
	{"termall", styleTermAll},
	{"vsc", styleVSC},
	{"segsym", styleSegSym},
	{"bypass_termall", styleBypass | styleTermAll},
	{"bypass_reset_vsc", styleBypass | styleReset | styleVSC},
	{"all", styleBypass | styleReset | styleTermAll | styleVSC | styleSegSym},
}

func TestJPXCodeBlockStyles(t *testing.T) {
	for _, c := range styleCases {
		t.Run(c.name, func(t *testing.T) {
			o := baseOptions(61, 53, 1)
			o.style = c.style
			img, err := JPX(encodeJPX(o), 0)
			if err != nil {
				t.Fatal(err)
			}
			if want := interleaved(o.planes()...); !bytes.Equal(img.Pix, want) {
				t.Fatalf("pixels differ, PSNR %.1f", psnr(img.Pix, want))
			}
		})
	}
}

func TestJPXEncoderVariants(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*encOptions)
	}{
		{"rct", func(o *encOptions) { o.mct = true }},
		{"tile parts", func(o *encOptions) { o.parts = 3 }},
		{"tiles and parts", func(o *encOptions) { o.tileW, o.tileH, o.parts = 25, 22, 2 }},
		{"sop eph", func(o *encOptions) { o.sop, o.eph = true, true }},
		{"precincts", func(o *encOptions) { o.precincts = []byte{0x44, 0x44, 0x55, 0x66}; o.xcb, o.ycb = 4, 3 }},
		{"offset", func(o *encOptions) { o.x0, o.y0 = 7, 11; o.tileW, o.tileH = 30, 30 }},
		{"one row at an odd offset", func(o *encOptions) {
			o.x0, o.y0, o.h = 5, 3, 1
			o.comps = o.comps[:1]
			o.comps[0].plane = planeOf(o.w, 1, 8, 3)
		}},
		{"roi", func(o *encOptions) { o.roi = 7; o.mct = true }},
		{"no levels", func(o *encOptions) { o.levels = 0 }},
		{"many levels", func(o *encOptions) { o.levels = 8 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := baseOptions(61, 53, 3)
			c.mod(&o)
			img, err := JPX(encodeJPX(o), 0)
			if err != nil {
				t.Fatal(err)
			}
			if want := interleaved(o.planes()...); !bytes.Equal(img.Pix, want) {
				t.Fatalf("pixels differ, PSNR %.1f", psnr(img.Pix, want))
			}
		})
	}
}

func TestJPXIrreversibleStyles(t *testing.T) {
	for _, style := range []int{0, styleBypass, styleTermAll, styleBypass | styleVSC | styleSegSym} {
		for _, mct := range []bool{false, true} {
			o := baseOptions(64, 48, 3)
			o.reversible = false
			o.style = style
			o.mct = mct
			img, err := JPX(encodeJPX(o), 0)
			if err != nil {
				t.Fatal(err)
			}
			if p := psnr(img.Pix, interleaved(o.planes()...)); p < 50 {
				t.Errorf("style %d mct %v: PSNR %.1f", style, mct, p)
			}
		}
	}
}

func TestJPXSubsampling(t *testing.T) {
	const w, h = 64, 48
	o := baseOptions(w, h, 1)
	o.comps = append(o.comps,
		encComponent{2, 2, planeOf(w/2, h/2, 8, 5)},
		encComponent{2, 1, planeOf(w/2, h, 8, 6)})
	img, err := JPX(encodeJPX(o), 0)
	if err != nil {
		t.Fatal(err)
	}
	for y := range h {
		for x := range w {
			got := img.Pix[(y*w+x)*3:][:3]
			want := []byte{
				byte(o.comps[0].plane[y*w+x]),
				byte(o.comps[1].plane[y/2*(w/2)+x/2]),
				byte(o.comps[2].plane[y*(w/2)+x/2]),
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("at %d,%d got %v want %v", x, y, got, want)
			}
		}
	}
}

func TestJPXDepths(t *testing.T) {
	for _, depth := range []int{1, 4, 7, 12, 16, 20} {
		const w, h = 40, 33
		plane := planeOf(w, h, depth, 3)
		o := encOptions{w: w, h: h, depth: depth, levels: 2, xcb: 4, ycb: 4, reversible: true,
			comps: []encComponent{{1, 1, plane}}}
		img, err := JPX(encodeJPX(o), 0)
		if err != nil {
			t.Fatal(depth, err)
		}
		if img.BitDepth != depth {
			t.Errorf("bit depth %d, want %d", img.BitDepth, depth)
		}
		for i, v := range plane {
			want := scaleTo8(uint64(v), depth)
			if depth > 16 {
				want = scaleTo8(uint64(v)>>(depth-16), 16)
			}
			if img.Pix[i] != want {
				t.Fatalf("depth %d sample %d: %d, want %d", depth, i, img.Pix[i], want)
			}
		}
	}
}

func TestJPXSigned(t *testing.T) {
	const w, h = 30, 20
	plane := planeOf(w, h, 8, 2)
	for i := range plane {
		plane[i] -= 128
	}
	o := encOptions{w: w, h: h, depth: 8, signed: true, levels: 2, xcb: 4, ycb: 4, reversible: true,
		comps: []encComponent{{1, 1, plane}}}
	img, err := JPX(encodeJPX(o), 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range plane {
		if int(img.Pix[i]) != int(v)+128 {
			t.Fatalf("sample %d: %d, want %d", i, img.Pix[i], v+128)
		}
	}
}

func colrBox(enum int) []byte {
	return boxBytes("colr", append([]byte{1, 0, 0}, be32(enum)...))
}

func TestJP2Palette(t *testing.T) {
	const w, h = 24, 16
	index := make([]int32, w*h)
	for i := range index {
		index[i] = int32(i % 5)
	}
	o := encOptions{w: w, h: h, depth: 8, levels: 1, xcb: 4, ycb: 4, reversible: true,
		comps: []encComponent{{1, 1, index}}}
	palette := [][3]byte{{1, 2, 3}, {40, 50, 60}, {200, 100, 0}, {0, 0, 255}, {9, 9, 9}}
	pclr := slices.Concat(be16(len(palette)), []byte{3, 7, 7, 7})
	for _, e := range palette {
		pclr = append(pclr, e[:]...)
	}
	cmap := []byte{0, 0, 1, 0, 0, 0, 1, 1, 0, 0, 1, 2}
	data := wrapJP2(encodeJPX(o), boxBytes("pclr", pclr), boxBytes("cmap", cmap), colrBox(16))
	img, err := JPX(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if img.Components != 3 || img.ColorSpace != "sRGB" || img.BitDepth != 8 {
		t.Fatalf("%d components, %q, depth %d", img.Components, img.ColorSpace, img.BitDepth)
	}
	for i, v := range index {
		if !bytes.Equal(img.Pix[i*3:][:3], palette[v][:]) {
			t.Fatalf("pixel %d is % X", i, img.Pix[i*3:][:3])
		}
	}
}

func TestJP2ChannelDefinition(t *testing.T) {
	o := baseOptions(20, 10, 4)
	cs := encodeJPX(o)
	// Channels stored as alpha, B, G, R.
	cdef := be16(4)
	for _, e := range [][3]int{{0, 1, 0}, {1, 0, 3}, {2, 0, 2}, {3, 0, 1}} {
		cdef = slices.Concat(cdef, be16(e[0]), be16(e[1]), be16(e[2]))
	}
	img, err := JPX(wrapJP2(cs, colrBox(16), boxBytes("cdef", cdef)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !img.HasAlpha || img.Components != 4 {
		t.Fatalf("alpha %v, %d components", img.HasAlpha, img.Components)
	}
	p := o.planes()
	if !bytes.Equal(img.Pix, interleaved(p[3], p[2], p[1], p[0])) {
		t.Error("channels are not in cdef order")
	}
	for enum, name := range map[int]string{12: "CMYK", 18: "sYCC", 17: "sGray", 14: ""} {
		img, err := JPX(wrapJP2(cs, colrBox(enum)), 0)
		if err != nil || img.ColorSpace != name {
			t.Errorf("enumeration %d: %q, %v", enum, img.ColorSpace, err)
		}
	}
}

func TestJPXRefuses(t *testing.T) {
	o := baseOptions(16, 16, 1)
	cs := encodeJPX(o)
	afterSIZ := 4 + int(binary.BigEndian.Uint16(cs[4:]))
	insert := func(extra []byte) []byte {
		return slices.Concat(cs[:afterSIZ], extra, cs[afterSIZ:])
	}
	htRsiz := bytes.Clone(cs)
	htRsiz[6] = 0x40
	cases := map[string][]byte{
		"empty":        nil,
		"junk":         []byte("not an image at all"),
		"no boxes":     cs[2:],
		"no jp2c":      wrapJP2(nil)[:24],
		"PPM":          insert(msegment(mPPM, []byte{0, 0, 0, 0, 0})),
		"PPT":          insert(msegment(mPPT, []byte{0, 0})),
		"HT in Rsiz":   htRsiz,
		"bad wavelet":  insert(msegment(mCOD, []byte{0, 0, 0, 1, 0, 1, 3, 3, 0, 9})),
		"bad progress": insert(msegment(mCOD, []byte{0, 9, 0, 1, 0, 1, 3, 3, 0, 1})),
		"big blocks":   insert(msegment(mCOD, []byte{0, 0, 0, 1, 0, 1, 8, 8, 0, 1})),
		"HT blocks":    insert(msegment(mCOD, []byte{0, 0, 0, 1, 0, 1, 3, 3, 64, 1})),
	}
	for name, data := range cases {
		if _, err := JPX(data, 0); err == nil {
			t.Errorf("%s: no error", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
}

func TestJPXHostileSizes(t *testing.T) {
	head := func(x, y, tw, th, comps int) []byte {
		b := be16(0)
		for _, v := range []int{x, y, 0, 0, tw, th, 0, 0} {
			b = append(b, be32(v)...)
		}
		b = append(b, be16(comps)...)
		for range comps {
			b = append(b, 7, 1, 1)
		}
		return slices.Concat([]byte{0xFF, 0x4F}, msegment(mSIZ, b))
	}
	for name, data := range map[string][]byte{
		"huge":       head(1<<31, 1<<31, 1<<31, 1<<31, 1),
		"zero":       head(0, 10, 10, 10, 1),
		"tiny tiles": head(60000, 60000, 1, 1, 1),
		"zero tile":  head(10, 10, 0, 0, 1),
		"many comps": head(10, 10, 10, 10, 16384),
	} {
		if _, err := JPX(data, 0); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestJPXStyleFuzz(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for _, c := range styleCases {
		o := baseOptions(40, 32, 3)
		o.style = c.style
		o.mct = true
		o.precincts = []byte{0x44, 0x44, 0x55, 0x55}
		fuzzJPX(t, encodeJPX(o), 200, rng)
	}
}

// TestJPXDump writes the encoder's output so OpenJPEG can check it, see check_encoder.py.
func TestJPXDump(t *testing.T) {
	dir := os.Getenv("JPX_DUMP")
	if dir == "" {
		t.Skip("JPX_DUMP is not set")
	}
	write := func(name string, o encOptions) {
		if err := os.WriteFile(filepath.Join(dir, name+".j2k"), encodeJPX(o), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".plain"), interleaved(o.planes()...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range styleCases {
		o := baseOptions(61, 53, 1)
		o.style = c.style
		write("styles_"+c.name, o)
	}
	o := baseOptions(61, 53, 3)
	o.mct = true
	o.tileW, o.tileH, o.parts = 25, 22, 2
	o.sop, o.eph = true, true
	o.precincts = []byte{0x44, 0x44, 0x55, 0x66}
	write("styles_parts", o)
	o = baseOptions(61, 53, 3)
	o.mct = true
	o.reversible = false
	o.style = styleBypass | styleTermAll
	write("styles_lossy", o)
	o = baseOptions(61, 53, 1)
	o.x0, o.y0 = 7, 11
	o.comps = append(o.comps, encComponent{1, 1, planeOf(61, 53, 8, 9)}, encComponent{1, 1, planeOf(61, 53, 8, 10)})
	o.tileW, o.tileH = 30, 30
	write("styles_offset", o)
	// Subsampled chroma: what the decoder gives is compared with what OpenJPEG gives.
	o = baseOptions(64, 48, 1)
	o.comps = append(o.comps, encComponent{2, 2, planeOf(32, 24, 8, 5)}, encComponent{2, 2, planeOf(32, 24, 8, 6)})
	cs := encodeJPX(o)
	img, err := JPX(cs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "styles_sub.j2k"), cs, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "styles_sub.plain"), img.Pix, 0o644); err != nil {
		t.Fatal(err)
	}
}
