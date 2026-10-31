package codec

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// BenchmarkJBIG2Generic decodes a page of the size of an A4 scan at 300 dpi.
func BenchmarkJBIG2Generic(b *testing.B) {
	rng := rand.New(rand.NewPCG(41, 42))
	bm := testBitmap(rng, 2480, 3508)
	at := genericShapes[0].defaultAT
	data := slices.Concat(
		segment(0, 48, nil, pageInfo(2480, 3508)),
		segment(1, 38, nil, genericSegmentData(bm, 0, 0, opOR, 0, at, false)),
	)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, _, _, err := JBIG2(data, nil, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJPXLossless decodes a lossless 2 megapixel colour image.
func BenchmarkJPXLossless(b *testing.B) {
	o := baseOptions(1600, 1200, 3)
	o.mct = true
	o.levels = 5
	data := encodeJPX(o)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := JPX(data, 0); err != nil {
			b.Fatal(err)
		}
	}
}
