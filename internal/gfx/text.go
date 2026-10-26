package gfx

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Font is a parsed typeface whose sized faces and glyphs are cached; it is safe for concurrent use.
type Font struct {
	sfnt  *sfnt.Font
	mu    sync.Mutex
	buf   sfnt.Buffer
	faces map[float32]*Face

	// fallbacks are font files searched, in order, for characters this font lacks; they load on first need.
	fallbacks []string
	loaded    []*Font
}

// Face is one size of a font.
type Face struct {
	font    *Font
	face    font.Face
	Size    float32
	Ascent  float32
	Descent float32
	glyphs  map[rune]*glyph
}

type glyph struct {
	mask    *image.Alpha
	x, y    int // mask origin relative to the pen on the baseline
	advance float32
}

// ParseFont reads a font from memory.
func ParseFont(data []byte) (*Font, error) {
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil, err
	}
	return &Font{sfnt: f, faces: map[float32]*Face{}}, nil
}

// LoadFont opens the first file in candidates that exists: a .ttf or .otf, or a font of a .ttc named "file.ttc#n"
// (the first one without a number).
func LoadFont(candidates ...string) (*Font, error) {
	var errs []error
	for _, candidate := range candidates {
		path, index := candidate, 0
		if at := strings.LastIndexByte(candidate, '#'); at > 0 {
			if n, err := strconv.Atoi(candidate[at+1:]); err == nil {
				path, index = candidate[:at], n
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var f *sfnt.Font
		if strings.EqualFold(filepath.Ext(path), ".ttc") {
			coll, cerr := sfnt.ParseCollection(data)
			switch {
			case cerr != nil:
				err = cerr
			case index >= coll.NumFonts():
				err = fmt.Errorf("the collection has %d fonts", coll.NumFonts())
			default:
				f, err = coll.Font(index)
			}
		} else {
			f, err = sfnt.Parse(data)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		return &Font{sfnt: f, faces: map[float32]*Face{}}, nil
	}
	return nil, fmt.Errorf("no usable font: %w", errors.Join(errs...))
}

// Face returns the font at a pixel size, rounded to half pixels.
func (f *Font) Face(size float32) *Face {
	size = float32(math.Max(1, math.Round(float64(size)*2)/2))
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.faceLocked(size)
}

func (f *Font) faceLocked(size float32) *Face {
	if face, ok := f.faces[size]; ok {
		return face
	}
	of, err := opentype.NewFace(f.sfnt, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err) // NewFace only fails for invalid options
	}
	m := of.Metrics()
	face := &Face{font: f, face: of, Size: size, Ascent: fix(m.Ascent), Descent: fix(m.Descent), glyphs: map[rune]*glyph{}}
	f.faces[size] = face
	return face
}

func fix(v fixed.Int26_6) float32 { return float32(v) / 64 }

// has reports whether the font itself draws r; the caller holds f.mu.
func (f *Font) has(r rune) bool {
	x, err := f.sfnt.GlyphIndex(&f.buf, r)
	return err == nil && x != 0
}

// fallback returns the first fallback font that draws r, loading fallbacks as needed; the caller holds f.mu.
func (f *Font) fallback(r rune) *Font {
	for i, path := range f.fallbacks {
		if i >= len(f.loaded) {
			fb, err := LoadFont(path)
			if err != nil {
				fb = nil
			}
			f.loaded = append(f.loaded, fb)
		}
		if fb := f.loaded[i]; fb != nil {
			fb.mu.Lock()
			ok := fb.has(r)
			fb.mu.Unlock()
			if ok {
				return fb
			}
		}
	}
	return nil
}

// WithFallbacks sets the font files searched for characters the font lacks.
func (f *Font) WithFallbacks(paths ...string) *Font {
	f.mu.Lock()
	f.fallbacks, f.loaded = paths, nil
	f.mu.Unlock()
	return f
}

func (fc *Face) glyph(r rune) *glyph {
	f := fc.font
	f.mu.Lock()
	if g, ok := fc.glyphs[r]; ok {
		f.mu.Unlock()
		return g
	}
	if !f.has(r) && r >= 0x20 {
		if fb := f.fallback(r); fb != nil {
			f.mu.Unlock()
			fb.mu.Lock()
			face := fb.faceLocked(fc.Size)
			fb.mu.Unlock()
			g := face.glyph(r)
			f.mu.Lock()
			fc.glyphs[r] = g
			f.mu.Unlock()
			return g
		}
	}
	defer f.mu.Unlock()
	g := &glyph{}
	dr, mask, mp, adv, ok := fc.face.Glyph(fixed.P(0, 0), r)
	if !ok {
		dr, mask, mp, adv, ok = fc.face.Glyph(fixed.P(0, 0), '□')
	}
	g.advance = fix(adv)
	if ok && !dr.Empty() {
		a := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
		for y := 0; y < dr.Dy(); y++ {
			for x := 0; x < dr.Dx(); x++ {
				_, _, _, alpha := mask.At(mp.X+x, mp.Y+y).RGBA()
				a.Pix[y*a.Stride+x] = uint8(alpha >> 8)
			}
		}
		g.mask, g.x, g.y = a, dr.Min.X, dr.Min.Y
	}
	fc.glyphs[r] = g
	return g
}

// Height is the line height.
func (fc *Face) Height() float32 { return fc.Ascent + fc.Descent }

// Measure returns the advance width of s.
func (fc *Face) Measure(s string) float32 {
	w := float32(0)
	for _, r := range s {
		w += fc.glyph(r).advance
	}
	return w
}

// Text draws s with its top-left at (x, y); it returns the pen position after the last glyph.
func (c *Canvas) Text(fc *Face, x, y float32, s string, col color.RGBA) float32 {
	baseline := y + fc.Ascent
	for _, r := range s {
		g := fc.glyph(r)
		if g.mask != nil {
			c.mask(g.mask, int(math.Round(float64(x)))+g.x, int(math.Round(float64(baseline)))+g.y, col)
		}
		x += g.advance
	}
	return x
}

func (c *Canvas) mask(m *image.Alpha, ox, oy int, col color.RGBA) {
	b := image.Rect(ox, oy, ox+m.Rect.Dx(), oy+m.Rect.Dy()).Intersect(c.clip)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := m.Pix[(y-oy)*m.Stride:]
		i := c.Img.PixOffset(b.Min.X, y)
		for x := b.Min.X; x < b.Max.X; x, i = x+1, i+4 {
			if a := row[x-ox]; a != 0 {
				c.blend(i, col, float32(a)/255)
			}
		}
	}
}

// Ellipsize shortens s with "…" so it fits within width.
func (fc *Face) Ellipsize(s string, width float32) string {
	if fc.Measure(s) <= width {
		return s
	}
	rs := []rune(s)
	dots := fc.Measure("…")
	w := float32(0)
	for i, r := range rs {
		w += fc.glyph(r).advance
		if w+dots > width {
			return string(rs[:i]) + "…"
		}
	}
	return s
}

// Wrap breaks s into lines no wider than width, at spaces where possible and between any two CJK characters;
// explicit newlines are kept.
func (fc *Face) Wrap(s string, width float32) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		rs := []rune(para)
		start, lastBreak := 0, -1
		w := float32(0)
		for i := 0; i < len(rs); i++ {
			r := rs[i]
			if unicode.IsSpace(r) || isCJK(r) {
				lastBreak = i
			}
			w += fc.glyph(r).advance
			if w > width && i > start {
				cut := i
				if lastBreak > start {
					cut = lastBreak
				}
				lines = append(lines, strings.TrimRightFunc(string(rs[start:cut]), unicode.IsSpace))
				for cut < len(rs) && unicode.IsSpace(rs[cut]) {
					cut++
				}
				start, lastBreak = cut, -1
				i = cut - 1
				w = 0
			}
		}
		lines = append(lines, string(rs[start:]))
	}
	return lines
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef)
}
