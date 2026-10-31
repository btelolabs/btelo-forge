package render

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"

	"golang.org/x/image/ccitt"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/codec"
)

const (
	maxImagePixels  = 100_000_000 // a single image's samples
	maxImageKept    = 40_000_000  // what one image may take after reducing, in pixels
	imageCacheBytes = 400 << 20
)

// picture is a decoded image: straight RGBA, possibly reduced from the original.
type picture struct {
	w, h    int
	pix     []uint8
	opaque  bool
	stencil bool // only alpha means anything
	alpha   *picture
	broken  bool
}

// imageSource samples a picture at device pixels.
type imageSource struct {
	p      *picture
	inv    pdf.Matrix // device to the unit square
	smooth bool
	rgb    *[3]uint8 // a stencil's colour; nil with fill set
	fill   source
}

func (s *imageSource) at(x, y int) (r, g, b, a uint8) {
	u, v := s.inv.Apply(float64(x)+0.5, float64(y)+0.5)
	v = 1 - v
	r, g, b, a = s.p.sample(u, v, s.smooth)
	if s.p.stencil {
		if s.fill != nil {
			fr, fg, fb, fa := s.fill.at(x, y)
			return fr, fg, fb, uint8(int(a) * int(fa) / 255)
		}
		return s.rgb[0], s.rgb[1], s.rgb[2], a
	}
	if s.p.alpha != nil {
		_, _, _, ma := s.p.alpha.sample(u, v, true)
		a = uint8(int(a) * int(ma) / 255)
	}
	return r, g, b, a
}

func (p *picture) sample(u, v float64, smooth bool) (r, g, b, a uint8) {
	fx, fy := u*float64(p.w), v*float64(p.h)
	if !smooth {
		x := min(max(int(fx), 0), p.w-1)
		y := min(max(int(fy), 0), p.h-1)
		q := p.pix[(y*p.w+x)*4 : (y*p.w+x)*4+4 : (y*p.w+x)*4+4]
		return q[0], q[1], q[2], q[3]
	}
	fx -= 0.5
	fy -= 0.5
	x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
	tx, ty := int((fx-float64(x0))*256), int((fy-float64(y0))*256)
	x1, y1 := min(max(x0+1, 0), p.w-1), min(max(y0+1, 0), p.h-1)
	x0, y0 = min(max(x0, 0), p.w-1), min(max(y0, 0), p.h-1)
	i00, i10, i01, i11 := (y0*p.w+x0)*4, (y0*p.w+x1)*4, (y1*p.w+x0)*4, (y1*p.w+x1)*4
	mix := func(k int) uint8 {
		top := int(p.pix[i00+k])*(256-tx) + int(p.pix[i10+k])*tx
		bot := int(p.pix[i01+k])*(256-tx) + int(p.pix[i11+k])*tx
		return uint8((top*(256-ty) + bot*ty + 1<<15) >> 16)
	}
	return mix(0), mix(1), mix(2), mix(3)
}

// drawImage paints an image stream through the unit square of the current matrix.
func (r *ctx) drawImage(s *pdf.Stream, num int, res pdf.Dict) {
	if r.vecOn() {
		defer r.vecImage(s, num, res)
	}
	d := r.doc
	gs := r.gs
	w, h := d.Int(firstKey(s.Dict, "Width", "W"), 0), d.Int(firstKey(s.Dict, "Height", "H"), 0)
	devW := math.Hypot(gs.ctm[0], gs.ctm[1])
	devH := math.Hypot(gs.ctm[2], gs.ctm[3])
	if devW <= 0 || devH <= 0 || math.IsNaN(devW+devH) {
		return
	}
	factor := 1
	if w > 0 && h > 0 {
		factor = max(int(min(float64(w)/max(devW, 1), float64(h)/max(devH, 1))), 1)
		for w/factor*(h/factor) > maxImageKept {
			factor++
		}
	}
	if res == nil {
		res = r.res
	}
	pic := r.cache.picture(r, s, num, factor, res)
	if pic == nil || pic.w == 0 || pic.h == 0 {
		return
	}
	inv, ok := gs.ctm.Invert()
	if !ok {
		return
	}
	src := &imageSource{p: pic, inv: inv}
	// A big enlargement shows the pixels as they are, unless the file asks to smooth them.
	src.smooth = float64(pic.w) > devW/2 || d.Bool(s.Dict["Interpolate"], false)
	if pic.stencil {
		src.smooth = true
		src.rgb = &gs.fill.rgb
		if gs.fill.pattern != nil {
			src.fill = r.patternSource(&gs.fill, gs.clip.r.Intersect(r.layer.r))
			if src.fill == nil {
				return
			}
		}
	}
	bounds := gs.clip.r.Intersect(r.layer.r)
	var m *mask
	if gs.ctm[1] == 0 && gs.ctm[2] == 0 {
		// Whole pixels, so strips of an image meet without a seam.
		x0, y0 := gs.ctm.Apply(0, 0)
		x1, y1 := gs.ctm.Apply(1, 1)
		rc := image.Rect(int(math.Round(min(x0, x1))), int(math.Round(min(y0, y1))), int(math.Round(max(x0, x1))), int(math.Round(max(y0, y1))))
		if rc.Dx() == 0 {
			rc.Max.X++
		}
		if rc.Dy() == 0 {
			rc.Max.Y++
		}
		rc = rc.Intersect(bounds)
		if rc.Empty() {
			return
		}
		m = &mask{r: rc, pix: bytes.Repeat([]byte{255}, rc.Dx()*rc.Dy())}
	} else {
		p := path{}
		p.rect(0, 0, 1, 1)
		m = r.ra.fill(p.flatten(gs.ctm), false, bounds, 4)
	}
	if m != nil {
		r.composite(m, [3]uint8{}, src, gs.ca)
	}
}

func firstKey(d pdf.Dict, keys ...pdf.Name) pdf.Object {
	for _, k := range keys {
		if v, ok := d[k]; ok {
			return v
		}
	}
	return nil
}

func placeholder(w, h int) *picture {
	w, h = min(max(w, 1), 64), min(max(h, 1), 64)
	p := &picture{w: w, h: h, pix: make([]uint8, 4*w*h), opaque: true, broken: true}
	for i := range w * h {
		x, y := i%w, i/w
		v := uint8(0xC8)
		if (x+y)%8 < 4 {
			v = 0xB4
		}
		p.pix[i*4], p.pix[i*4+1], p.pix[i*4+2], p.pix[i*4+3] = v, v, v, 255
	}
	return p
}

// samples is what a codec gave: rows of packed samples.
type samples struct {
	w, h, comps, bpc int
	data             []byte
	cs               colorSpace // what the codec knows, when the dictionary says nothing
	alpha            *picture
}

// loadPicture decodes an image.
func (r *ctx) loadPicture(s *pdf.Stream, factor int, res pdf.Dict, depth int) (out *picture) {
	d := r.doc
	w, h := d.Int(firstKey(s.Dict, "Width", "W"), 0), d.Int(firstKey(s.Dict, "Height", "H"), 0)
	defer func() {
		if p := recover(); p != nil {
			out = placeholder(w, h)
		}
	}()
	if depth > 2 {
		return nil
	}
	stencil := d.Bool(firstKey(s.Dict, "ImageMask", "IM"), false)
	bpc := d.Int(firstKey(s.Dict, "BitsPerComponent", "BPC"), 8)
	if stencil {
		bpc = 1
	}
	data, filter, err := d.DecodeImage(s)
	if err != nil && len(data) == 0 {
		return placeholder(w, h)
	}
	in := samples{w: w, h: h, bpc: bpc, data: data}
	if filter != nil {
		ok := false
		in, ok = r.runCodec(filter, s, data, in)
		if !ok {
			return placeholder(w, h)
		}
	}
	if in.w <= 0 || in.h <= 0 || in.w > 1<<20 || in.h > 1<<20 || in.w*in.h > maxImagePixels {
		return placeholder(w, h)
	}
	if factor > 1 && (in.w < factor || in.h < factor) {
		factor = max(min(in.w, in.h), 1)
	}
	decode := d.Floats(firstKey(s.Dict, "Decode", "D"))
	if stencil {
		return reduceStencil(in, decode, factor)
	}
	var cs colorSpace
	if o := firstKey(s.Dict, "ColorSpace", "CS"); o != nil {
		cs = r.parseColorSpace(o, res, 0)
	}
	if cs == nil || cs.comps() == 0 {
		cs = in.cs
	}
	if cs == nil {
		switch in.comps {
		case 3:
			cs = deviceRGB{}
		case 4:
			cs = deviceCMYK{}
		default:
			cs = deviceGray{}
		}
	}
	if in.comps == 0 {
		in.comps = cs.comps()
	}
	if in.comps != cs.comps() {
		// The codec knows better than the dictionary.
		switch in.comps {
		case 1:
			cs = deviceGray{}
		case 3:
			cs = deviceRGB{}
		case 4:
			cs = deviceCMYK{}
		default:
			return placeholder(w, h)
		}
	}
	var key []float64
	var mask *picture
	switch m := d.Resolve(s.Dict["Mask"]).(type) {
	case pdf.Array:
		key = d.Floats(m)
	case *pdf.Stream:
		// A stencil's alpha is already what a mask lets show: a sample that would paint.
		if mask = r.loadPicture(m, 1, res, depth+1); mask != nil && mask.broken {
			mask = nil
		}
	}
	pic := convert(in, cs, decode, key, factor)
	if pic == nil {
		return placeholder(w, h)
	}
	pic.alpha = mask
	if in.alpha != nil {
		pic.alpha = in.alpha
	}
	if sm, ok := d.Resolve(s.Dict["SMask"]).(*pdf.Stream); ok {
		if a := r.loadPicture(sm, 1, res, depth+1); a != nil && !a.broken {
			pic.alpha = luminanceToAlpha(a)
		}
	}
	return pic
}

func luminanceToAlpha(p *picture) *picture {
	out := &picture{w: p.w, h: p.h, pix: make([]uint8, len(p.pix))}
	for i := 0; i+3 < len(p.pix); i += 4 {
		out.pix[i+3] = p.pix[i]
	}
	return out
}

func (r *ctx) runCodec(f *pdf.Filter, s *pdf.Stream, data []byte, in samples) (samples, bool) {
	d := r.doc
	switch f.Name {
	case "DCTDecode":
		return decodeJPEG(data, in)
	case "JPXDecode":
		img, err := codec.JPX(data, maxImagePixels)
		if err != nil {
			return in, false
		}
		in.w, in.h, in.bpc, in.data = img.Width, img.Height, 8, img.Pix
		in.comps = img.Components
		if img.HasAlpha && img.Components > 1 {
			// Keep the colours, and the last channel as the alpha.
			n := img.Components
			colours := make([]byte, img.Width*img.Height*(n-1))
			alpha := make([]byte, img.Width*img.Height*4)
			for i := range img.Width * img.Height {
				copy(colours[i*(n-1):], img.Pix[i*n:i*n+n-1])
				alpha[i*4+3] = img.Pix[i*n+n-1]
			}
			in.data, in.comps = colours, n-1
			if d.Int(s.Dict["SMaskInData"], 0) != 0 {
				in.alpha = &picture{w: img.Width, h: img.Height, pix: alpha}
			}
		}
		switch img.ColorSpace {
		case "sGray":
			in.cs = deviceGray{}
		case "CMYK":
			in.cs = deviceCMYK{}
		case "sYCC":
			ycc(in.data, in.comps)
			in.cs = deviceRGB{}
		case "sRGB":
			in.cs = deviceRGB{}
		}
		return in, true
	case "JBIG2Decode":
		var globals []byte
		if g, ok := d.Resolve(f.Parms["JBIG2Globals"]).(*pdf.Stream); ok {
			globals, _ = d.Decode(g)
		}
		bits, w, h, err := codec.JBIG2(data, globals, maxImagePixels)
		if err != nil {
			return in, false
		}
		for i := range bits {
			bits[i] = ^bits[i]
		}
		in.w, in.h, in.bpc, in.comps, in.data = w, h, 1, 1, bits
		return in, true
	case "CCITTFaxDecode":
		k := d.Int(f.Parms["K"], 0)
		cols := d.Int(f.Parms["Columns"], 1728)
		rows := d.Int(f.Parms["Rows"], 0)
		if rows == 0 {
			rows = in.h
		}
		if rows == 0 {
			rows = ccitt.AutoDetectHeight
		}
		if cols <= 0 || cols > 1<<20 || rows > 1<<20 {
			return in, false
		}
		sub := ccitt.Group3
		if k < 0 {
			sub = ccitt.Group4
		}
		opts := &ccitt.Options{Align: d.Bool(f.Parms["EncodedByteAlign"], false), Invert: !d.Bool(f.Parms["BlackIs1"], false)}
		rd := ccitt.NewReader(bytes.NewReader(data), ccitt.MSB, sub, cols, rows, opts)
		out, err := io.ReadAll(io.LimitReader(rd, maxImagePixels))
		if err != nil && len(out) == 0 {
			return in, false
		}
		// The reader gives black as zero, which with Invert means what a PDF image expects when BlackIs1 is false.
		stride := (cols + 7) / 8
		if in.h == 0 {
			in.h = len(out) / stride
		}
		need := stride * in.h
		if len(out) < need {
			pad := byte(0xFF)
			out = append(out, bytes.Repeat([]byte{pad}, need-len(out))...)
		}
		in.w, in.bpc, in.comps, in.data = cols, 1, 1, out
		return in, true
	}
	return in, false
}

func ycc(b []byte, comps int) {
	if comps != 3 {
		return
	}
	for i := 0; i+2 < len(b); i += 3 {
		c := color.YCbCr{Y: b[i], Cb: b[i+1], Cr: b[i+2]}
		b[i], b[i+1], b[i+2], _ = rgba8(c)
	}
}

func rgba8(c color.Color) (r, g, b, a uint8) {
	x, y, z, w := c.RGBA()
	return uint8(x >> 8), uint8(y >> 8), uint8(z >> 8), uint8(w >> 8)
}

func decodeJPEG(data []byte, in samples) (samples, bool) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width*cfg.Height > maxImagePixels {
		return in, false
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || img == nil {
		return in, false
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	in.w, in.h, in.bpc = w, h, 8
	switch v := img.(type) {
	case *image.Gray:
		in.comps = 1
		out := make([]byte, w*h)
		for y := range h {
			copy(out[y*w:y*w+w], v.Pix[y*v.Stride:])
		}
		in.data = out
	case *image.YCbCr:
		in.comps = 3
		out := make([]byte, 3*w*h)
		i := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				yi, ci := v.YOffset(x, y), v.COffset(x, y)
				out[i], out[i+1], out[i+2] = color.YCbCrToRGB(v.Y[yi], v.Cb[ci], v.Cr[ci])
				i += 3
			}
		}
		in.data = out
	case *image.RGBA:
		in.comps = 3
		out := make([]byte, 3*w*h)
		for y := range h {
			for x := range w {
				copy(out[(y*w+x)*3:], v.Pix[y*v.Stride+x*4:y*v.Stride+x*4+3])
			}
		}
		in.data = out
	case *image.CMYK:
		// Go gives ink amounts; the raw samples a Decode array talks about are Adobe's, which are inverted.
		in.comps = 4
		out := make([]byte, 4*w*h)
		for y := range h {
			row := v.Pix[y*v.Stride:]
			for x := range 4 * w {
				out[y*4*w+x] = 255 - row[x]
			}
		}
		in.data = out
	default:
		return in, false
	}
	return in, true
}

// reduceStencil turns a one bit mask into alpha, reduced by averaging.
func reduceStencil(in samples, decode []float64, factor int) *picture {
	w, h := in.w/factor, in.h/factor
	if w == 0 || h == 0 {
		w, h, factor = in.w, in.h, 1
	}
	// A sample of 0 paints, unless Decode is [1 0].
	paintBit := byte(0)
	if len(decode) >= 2 && decode[0] > decode[1] {
		paintBit = 1
	}
	p := &picture{w: w, h: h, pix: make([]uint8, 4*w*h), stencil: true}
	stride := (in.w + 7) / 8
	sums := make([]int, w)
	for y := range h * factor {
		row := in.data[min(y*stride, len(in.data)):]
		for x := range w * factor {
			bit := byte(1) ^ paintBit // what lies past the data is not painted
			if x>>3 < len(row) {
				bit = row[x>>3] >> (7 - x&7) & 1
			}
			if bit == paintBit {
				sums[x/factor]++
			}
		}
		if y%factor == factor-1 {
			oy := y / factor
			for x := range w {
				p.pix[(oy*w+x)*4+3] = uint8(sums[x] * 255 / (factor * factor))
				sums[x] = 0
			}
		}
	}
	return p
}

// convert brings samples to RGBA through the colour space, reduced by factor.
func convert(in samples, cs colorSpace, decode, key []float64, factor int) *picture {
	n := in.comps
	bpc := in.bpc
	if n <= 0 || n > 32 || bpc != 1 && bpc != 2 && bpc != 4 && bpc != 8 && bpc != 16 {
		return nil
	}
	w, h := in.w/factor, in.h/factor
	if w == 0 || h == 0 {
		w, h, factor = in.w, in.h, 1
	}
	stride := (in.w*n*bpc + 7) / 8
	top := float64(int(1)<<bpc - 1)
	lo := make([]float64, n)
	span := make([]float64, n)
	defaultDecode := true
	for i := range n {
		a, b := cs.decode(i, bpc)
		if 2*i+1 < len(decode) {
			if decode[2*i] != a || decode[2*i+1] != b {
				defaultDecode = false
			}
			a, b = decode[2*i], decode[2*i+1]
		}
		lo[i], span[i] = a, b-a
	}
	_, isRGB := cs.(deviceRGB)
	_, isGray := cs.(deviceGray)
	fast := defaultDecode && bpc == 8 && len(key) == 0
	var lut [][4]uint8
	if n == 1 {
		size := 1 << min(bpc, 8)
		lut = make([][4]uint8, size)
		for i := range size {
			raw := float64(i)
			if bpc == 16 {
				raw = float64(i) * 257
			}
			v := lo[0] + raw/top*span[0]
			c := toBytes(cs.rgb([]float64{v}))
			lut[i] = [4]uint8{c[0], c[1], c[2], 255}
			if len(key) >= 2 && float64(i) >= key[0] && float64(i) <= key[1] {
				lut[i][3] = 0
			}
		}
	}
	p := &picture{w: w, h: h, pix: make([]uint8, 4*w*h), opaque: len(key) == 0}
	row := make([]uint8, 4*in.w)
	sums := make([]int, 4*w)
	comps := make([]float64, n)
	raw := make([]int, n)
	lastRaw := make([]int, n)
	var lastRGB [4]uint8
	lastRaw[0] = -1
	maxRow := h * factor
	for y := range maxRow {
		start := y * stride
		var src []byte
		if start < len(in.data) {
			src = in.data[start:min(start+stride, len(in.data))]
		}
		switch {
		case fast && isRGB:
			for x := range in.w {
				i := x * 3
				q := row[x*4 : x*4+4 : x*4+4]
				if i+2 < len(src) {
					q[0], q[1], q[2], q[3] = src[i], src[i+1], src[i+2], 255
				} else {
					q[0], q[1], q[2], q[3] = 255, 255, 255, 255
				}
			}
		case fast && isGray:
			for x := range in.w {
				v := uint8(255)
				if x < len(src) {
					v = src[x]
				}
				q := row[x*4 : x*4+4 : x*4+4]
				q[0], q[1], q[2], q[3] = v, v, v, 255
			}
		default:
			bit := 0
			for x := range in.w {
				for c := range n {
					v := 0
					switch bpc {
					case 8:
						if at := bit >> 3; at < len(src) {
							v = int(src[at])
						}
					case 16:
						if at := bit >> 3; at+1 < len(src) {
							v = int(src[at])<<8 | int(src[at+1])
						}
					default:
						if at := bit >> 3; at < len(src) {
							v = int(src[at]>>(8-bpc-bit&7)) & (1<<bpc - 1)
						}
					}
					raw[c] = v
					bit += bpc
				}
				if lut != nil {
					v := raw[0]
					if bpc == 16 {
						v >>= 8
					}
					l := lut[v]
					copy(row[x*4:x*4+4], l[:])
					continue
				}
				same := true
				for c := range n {
					same = same && raw[c] == lastRaw[c]
				}
				if !same {
					for c := range n {
						comps[c] = lo[c] + float64(raw[c])/top*span[c]
					}
					col := toBytes(cs.rgb(comps))
					lastRGB = [4]uint8{col[0], col[1], col[2], 255}
					if len(key) >= 2*n {
						hit := true
						for c := range n {
							hit = hit && float64(raw[c]) >= key[2*c] && float64(raw[c]) <= key[2*c+1]
						}
						if hit {
							lastRGB[3] = 0
						}
					}
					copy(lastRaw, raw)
				}
				copy(row[x*4:x*4+4], lastRGB[:])
			}
		}
		if factor == 1 {
			copy(p.pix[y*w*4:(y+1)*w*4], row)
			continue
		}
		for x := range w * factor {
			o := x / factor * 4
			sums[o] += int(row[x*4])
			sums[o+1] += int(row[x*4+1])
			sums[o+2] += int(row[x*4+2])
			sums[o+3] += int(row[x*4+3])
		}
		if y%factor == factor-1 {
			oy := y / factor
			area := factor * factor
			for i, v := range sums {
				p.pix[oy*w*4+i] = uint8(v / area)
				sums[i] = 0
			}
		}
	}
	return p
}
