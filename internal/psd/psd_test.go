package psd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"strings"
	"testing"
)

func solid(r image.Rectangle, c [4]uint8) []uint8 {
	out := make([]uint8, r.Dx()*r.Dy()*4)
	for i := range out {
		out[i] = c[i%4]
	}
	return out
}

func sample() *File {
	back := image.Rect(0, 0, 40, 30)
	dot := image.Rect(5, 6, 15, 12)
	mask := image.NewAlpha(image.Rect(4, 5, 16, 13))
	for i := range mask.Pix {
		mask.Pix[i] = uint8(i)
	}
	merged := image.NewNRGBA(back)
	copy(merged.Pix, solid(back, [4]uint8{10, 20, 30, 255}))
	return &File{W: 40, H: 30, Merged: merged, Layers: []*Layer{
		{Name: "图层 上", Group: true, Open: true, Visible: true, Opacity: 128, Blend: "pass", Children: []*Layer{
			{Name: "Dot", Bounds: dot, Pix: solid(dot, [4]uint8{200, 100, 50, 180}), Visible: true, Opacity: 255, Blend: "mul ", Mask: mask, Fill: 255},
			{Name: "Hidden", Bounds: dot, Pix: solid(dot, [4]uint8{1, 2, 3, 4}), Visible: false, Opacity: 90, Blend: "norm"},
		}},
		{Name: "Background", Bounds: back, Pix: solid(back, [4]uint8{10, 20, 30, 255}), Visible: true, Opacity: 255, Blend: "norm"},
	}}
}

func TestARoundTripKeepsLayers(t *testing.T) {
	var b bytes.Buffer
	if err := Encode(&b, sample()); err != nil {
		t.Fatal(err)
	}
	f, err := Decode(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if f.W != 40 || f.H != 30 || len(f.Layers) != 2 {
		t.Fatalf("%dx%d, %d layers", f.W, f.H, len(f.Layers))
	}
	g := f.Layers[0]
	if !g.Group || !g.Open || g.Name != "图层 上" || g.Opacity != 128 || g.Blend != "pass" || len(g.Children) != 2 {
		t.Fatalf("the group: %+v", g)
	}
	dot := g.Children[0]
	if dot.Name != "Dot" || dot.Blend != "mul " || dot.Bounds != image.Rect(5, 6, 15, 12) || !bytes.Equal(dot.Pix, sample().Layers[0].Children[0].Pix) {
		t.Errorf("the top layer: %q %q %v", dot.Name, dot.Blend, dot.Bounds)
	}
	if dot.Mask == nil || dot.Mask.Rect != image.Rect(4, 5, 16, 13) || dot.Fill != 255 || !bytes.Equal(dot.Mask.Pix, sample().Layers[0].Children[0].Mask.Pix) {
		t.Errorf("the mask: %+v", dot.Mask)
	}
	if h := g.Children[1]; h.Visible || h.Opacity != 90 {
		t.Errorf("the hidden layer: %+v", h)
	}
	if f.Merged == nil || f.Merged.NRGBAAt(3, 3).B != 30 {
		t.Errorf("the merged picture: %+v", f.Merged)
	}
}

func TestPackBitsRoundTrips(t *testing.T) {
	cases := [][]byte{{}, {7}, {1, 2, 3, 4, 5}, bytes.Repeat([]byte{9}, 500), append(bytes.Repeat([]byte{1}, 3), 2, 3, 3, 3, 3, 4), []byte(strings.Repeat("ab", 200))}
	for _, row := range cases {
		got, err := unpackBits(nil, packBits(nil, row), len(row))
		if err != nil || !bytes.Equal(got, row) {
			t.Errorf("%d bytes: %v", len(row), err)
		}
	}
}

// flat is a file with no layers: the header, then its merged picture uncompressed, of the given depth.
func flat(w, h, channels, depth, mode int, body []byte) []byte {
	var b bytes.Buffer
	b.WriteString("8BPS")
	b.Write([]byte{0, 1, 0, 0, 0, 0, 0, 0})
	for _, v := range []any{uint16(channels), uint32(h), uint32(w), uint16(depth), uint16(mode), uint32(0), uint32(0), uint32(0), uint16(0)} {
		binary.Write(&b, binary.BigEndian, v)
	}
	b.Write(body)
	return b.Bytes()
}

func TestAFlatFileIsOneLayer(t *testing.T) {
	f, err := Decode(bytes.NewReader(flat(2, 1, 3, 8, 3, []byte{1, 2, 3, 4, 5, 6})))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Layers) != 1 || f.Layers[0].Name != "Background" {
		t.Fatalf("%+v", f.Layers)
	}
	if p := f.Layers[0].Pix; !bytes.Equal(p, []uint8{1, 3, 5, 255, 2, 4, 6, 255}) {
		t.Errorf("pixels %v", p)
	}
}

func TestSixteenBitsAndGrey(t *testing.T) {
	f, err := Decode(bytes.NewReader(flat(1, 1, 1, 16, 1, []byte{200, 77})))
	if err != nil {
		t.Fatal(err)
	}
	if p := f.Layers[0].Pix; !bytes.Equal(p, []uint8{200, 200, 200, 255}) {
		t.Errorf("pixels %v", p)
	}
}

func TestWhatCannotBeReadSaysSo(t *testing.T) {
	if _, err := Decode(strings.NewReader("not a file at all")); !errors.Is(err, ErrNotPSD) {
		t.Errorf("garbage: %v", err)
	}
	if _, err := Decode(bytes.NewReader(flat(1, 1, 4, 8, 4, []byte{1, 2, 3, 4}))); !errors.Is(err, ErrUnsupported) {
		t.Errorf("CMYK: %v", err)
	}
	if _, err := Decode(bytes.NewReader(flat(1, 1, 3, 32, 3, make([]byte, 12)))); !errors.Is(err, ErrUnsupported) {
		t.Errorf("32 bits: %v", err)
	}
	var good bytes.Buffer
	if err := Encode(&good, sample()); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < good.Len(); n += 7 {
		f, err := Decode(bytes.NewReader(good.Bytes()[:n]))
		if err == nil && (len(f.Layers) == 0) {
			t.Fatalf("cut at %d: no error and no layers", n)
		}
	}
}
