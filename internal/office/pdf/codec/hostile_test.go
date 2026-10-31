package codec

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"
	"time"
)

// TestJPXTruncatedKeepsTiles shows that a cut stream still gives the tiles it holds.
func TestJPXTruncatedKeepsTiles(t *testing.T) {
	o := baseOptions(64, 64, 1)
	o.tileW, o.tileH = 32, 32
	cs := encodeJPX(o)
	img, err := JPX(cs[:len(cs)*3/4], 0)
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != 64 || img.Height != 64 {
		t.Fatalf("%dx%d", img.Width, img.Height)
	}
	want := interleaved(o.planes()...)
	for y := range 32 {
		if !bytes.Equal(img.Pix[y*64:][:32], want[y*64:][:32]) {
			t.Fatal("the first tile was damaged by the cut")
		}
	}
}

// TestJPXEmptyLargeImage declares the largest image with no packets at all.
func TestJPXEmptyLargeImage(t *testing.T) {
	o := baseOptions(16, 16, 1)
	cs := encodeJPX(o)
	big := bytes.Clone(cs)
	const size = 10000
	for _, off := range []int{8, 12, 24, 28} { // Xsiz, Ysiz, XTsiz, YTsiz
		binary.BigEndian.PutUint32(big[off:], size)
	}
	start := time.Now()
	sot := bytes.Index(big, []byte{0xFF, 0x90})
	if _, err := JPX(big[:sot+14], 0); err == nil {
		t.Error("an image with no packets decoded")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

// TestJPXPassBudget claims far more tier 1 work than the image can justify.
func TestJPXPassBudget(t *testing.T) {
	d := &jpxDecoder{t1Work: 1000}
	cod := jpxCoding{xcb: 6, ycb: 6, levels: 0, reversible: true}
	cb := jpxCblk{x1: 64, y1: 64, numbps: 20, segs: []jpxSeg{{passes: 58}}}
	band := jpxBand{precs: []jpxPrecinct{{cblks: []jpxCblk{cb}}}}
	tl := &tile{d: d, p: &jpxParams{roi: []int{0}}, comps: []jpxTileComp{{
		x1: 64, y1: 64, coding: &cod, res: []jpxRes{{bands: []jpxBand{band}}},
	}}}
	if err := tl.tier1(); err == nil {
		t.Error("no work limit in tier 1")
	}
}

// TestJBIG2HeldMemory stacks intermediate regions, which must not add up without bound.
func TestJBIG2HeldMemory(t *testing.T) {
	d := newJB2(1000)
	if err := d.hold(1500); err != nil {
		t.Fatal(err)
	}
	if err := d.hold(600); err == nil {
		t.Error("no limit on held pixels")
	}
}

// TestRandomInput throws plain noise and noise after valid headers at both decoders.
func TestRandomInput(t *testing.T) {
	rng := rand.New(rand.NewPCG(51, 52))
	o := baseOptions(32, 32, 1)
	head := encodeJPX(o)
	head = head[:bytes.Index(head, []byte{0xFF, 0x90})+14]
	page := segment(0, 48, nil, pageInfo(64, 64))
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; i < 20000 && time.Now().Before(deadline); i++ {
		noise := make([]byte, rng.IntN(400))
		for j := range noise {
			noise[j] = byte(rng.Uint32())
		}
		JPX(noise, 1<<16)
		JPX(append(head[:len(head):len(head)], noise...), 1<<16)
		JBIG2(noise, nil, 1<<16)
		JBIG2(append(page[:len(page):len(page)], noise...), noise, 1<<16)
	}
}
