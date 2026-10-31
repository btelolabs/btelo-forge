package pptx

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sync"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
)

// maxPicSide is the longest side a picture is kept at in memory.
const maxPicSide = 8192

// picBudget is how many bytes of decoded pictures are kept; the least recently used go first.
const picBudget = 1 << 30

// picStore decodes the package's pictures, once each, for the window and the thumbnails alike. The bytes come from
// the parts as they were read or added, which are never changed in place.
type picStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
	mips  map[picKey]*picEntry
	used  int64
	clock uint64
}

type picKey struct {
	part  string
	level int
}

type picEntry struct {
	img  *image.RGBA // nil when it could not be decoded
	size int64
	seen uint64
}

func newPicStore(pkg *opc.Package) *picStore {
	s := &picStore{blobs: map[string][]byte{}, mips: map[picKey]*picEntry{}}
	for _, n := range pkg.Names() {
		s.blobs[n] = pkg.Part(n).Data
	}
	return s
}

// add makes a part's bytes known.
func (s *picStore) add(part string, data []byte) {
	s.mu.Lock()
	s.blobs[part] = data
	s.mu.Unlock()
}

// base is the picture at full size, nil when its format is one this cannot read (EMF, WMF and SVG among them).
func (s *picStore) base(part string) *image.RGBA { return s.level(part, 0) }

// level is the picture halved level times.
func (s *picStore) level(part string, level int) *image.RGBA {
	key := picKey{part, level}
	s.mu.Lock()
	if e, ok := s.mips[key]; ok {
		s.clock++
		e.seen = s.clock
		s.mu.Unlock()
		return e.img
	}
	data := s.blobs[part]
	s.mu.Unlock()
	var img *image.RGBA
	if level == 0 {
		img = decode(data)
	} else if up := s.level(part, level-1); up != nil {
		img = halve(up)
	}
	e := &picEntry{img: img}
	if img != nil {
		e.size = int64(len(img.Pix))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock++
	e.seen = s.clock
	s.mips[key] = e
	s.used += e.size
	for s.used > picBudget && len(s.mips) > 1 {
		var oldest picKey
		var at uint64
		found := false
		for k, v := range s.mips {
			if k != key && (!found || v.seen < at) && v.img != nil {
				oldest, at, found = k, v.seen, true
			}
		}
		if !found {
			break
		}
		s.used -= s.mips[oldest].size
		delete(s.mips, oldest)
	}
	return img
}

// decode reads a picture into premultiplied pixels, scaled down when it is enormous. A file's header is looked at
// first, so that a tiny file claiming a vast picture costs nothing.
func decode(data []byte) *image.RGBA {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 1<<29 {
		return nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, src, b.Min, draw.Src)
	for max(out.Rect.Dx(), out.Rect.Dy()) > maxPicSide {
		out = halve(out)
	}
	return out
}

// halve averages each block of four pixels.
func halve(src *image.RGBA) *image.RGBA {
	w, h := max(src.Rect.Dx()/2, 1), max(src.Rect.Dy()/2, 1)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	for y := 0; y < h; y++ {
		y0, y1 := y*2, min(y*2+1, sh-1)
		r0 := src.Pix[y0*src.Stride:]
		r1 := src.Pix[y1*src.Stride:]
		o := out.Pix[y*out.Stride:]
		for x := 0; x < w; x++ {
			x0, x1 := x*8, min(x*2+1, sw-1)*4
			for k := 0; k < 4; k++ {
				o[x*4+k] = uint8((int(r0[x0+k]) + int(r0[x1+k]) + int(r1[x0+k]) + int(r1[x1+k]) + 2) / 4)
			}
		}
	}
	return out
}

// Halved is the picture at half its size, each block of four pixels averaged.
func Halved(src *image.RGBA) *image.RGBA { return halve(src) }

// PictureSize is the size of a picture in pixels, as read from its header.
func PictureSize(data []byte) (w, h int, ok bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}
