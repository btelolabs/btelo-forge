package win

import "testing"

func TestToBGRASwapsRedAndBlue(t *testing.T) {
	src := []byte{1, 2, 3, 4, 250, 251, 252, 0}
	dst := make([]byte, len(src))
	toBGRA(dst, src)
	want := []byte{3, 2, 1, 255, 252, 251, 250, 255}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("got %v, want %v", dst, want)
		}
	}
}

func BenchmarkToBGRA(b *testing.B) {
	src := make([]byte, 1800*1125*4)
	dst := make([]byte, len(src))
	for i := 0; i < b.N; i++ {
		toBGRA(dst, src)
	}
}

func TestDecodeDIBReadsBottomUpRowsAndZeroAlpha(t *testing.T) {
	// 2×2, 32 bits, bottom-up, alpha left at zero: the first stored row is the bottom one.
	header := make([]byte, 40)
	put := func(at int, v uint32) {
		header[at], header[at+1], header[at+2], header[at+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	put(0, 40)
	put(4, 2)
	put(8, 2)
	header[12], header[14] = 1, 32
	pixels := []byte{
		255, 0, 0, 0, 0, 255, 0, 0, // bottom row: blue, green
		0, 0, 255, 0, 255, 255, 255, 0, // top row: red, white
	}
	img, err := decodeDIB(append(header, pixels...))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		x, y       int
		r, g, b, a uint32
	}{{0, 0, 255, 0, 0, 255}, {1, 0, 255, 255, 255, 255}, {0, 1, 0, 0, 255, 255}, {1, 1, 0, 255, 0, 255}} {
		r, g, b, a := img.At(c.x, c.y).RGBA()
		if r>>8 != c.r || g>>8 != c.g || b>>8 != c.b || a>>8 != c.a {
			t.Fatalf("pixel %d,%d = %d %d %d %d", c.x, c.y, r>>8, g>>8, b>>8, a>>8)
		}
	}
	if _, err := decodeDIB(header[:20]); err == nil {
		t.Fatal("short bitmap accepted")
	}
}
