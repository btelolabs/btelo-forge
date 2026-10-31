package codec

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/bits"
	"slices"
)

// This file is a small JPEG 2000 encoder for the tests. It takes its geometry from the
// decoder's own tile.build, but everything it writes has also been read back by OpenJPEG:
// the outputs are kept in testdata as styles_*.j2k with what OpenJPEG decoded them to.

// encComponent is an input plane at its own resolution.
type encComponent struct {
	dx, dy int
	plane  []int32
}

type encOptions struct {
	w, h         int
	x0, y0       int // image offset on the reference grid
	depth        int
	signed       bool
	comps        []encComponent
	levels       int
	xcb, ycb     int // exponents
	style        int
	reversible   bool
	mct          bool
	roi          int
	precincts    []byte
	tileW, tileH int
	parts        int
	sop, eph     bool
}

// marker segments

func msegment(m int, body []byte) []byte {
	out := binary.BigEndian.AppendUint16(nil, uint16(m))
	out = binary.BigEndian.AppendUint16(out, uint16(len(body)+2))
	return append(out, body...)
}

func (o *encOptions) sizBody() []byte {
	tw, th := o.tileW, o.tileH
	if tw == 0 {
		tw, th = o.x0+o.w, o.y0+o.h
	}
	b := be16(0)
	for _, v := range []int{o.x0 + o.w, o.y0 + o.h, o.x0, o.y0, tw, th, 0, 0} {
		b = append(b, be32(v)...)
	}
	b = append(b, be16(len(o.comps))...)
	for _, c := range o.comps {
		s := byte(o.depth - 1)
		if o.signed {
			s |= 0x80
		}
		b = append(b, s, byte(c.dx), byte(c.dy))
	}
	return b
}

func (o *encOptions) codBody() []byte {
	scod := byte(0)
	if o.precincts != nil {
		scod |= 1
	}
	if o.sop {
		scod |= 2
	}
	if o.eph {
		scod |= 4
	}
	b := []byte{scod, 0, 0, 1, byte(boolInt(o.mct)),
		byte(o.levels), byte(o.xcb - 2), byte(o.ycb - 2), byte(o.style), byte(boolInt(o.reversible))}
	return append(b, o.precincts...)
}

func (o *encOptions) qcdBody() []byte {
	const guard = 2
	bands := 3*o.levels + 1
	gains := func(i int) int {
		if i == 0 {
			return 0
		}
		return [3]int{1, 1, 2}[(i-1)%3]
	}
	if o.reversible {
		b := []byte{guard << 5}
		for i := range bands {
			b = append(b, byte(o.depth+gains(i)+1)<<3)
		}
		return b
	}
	b := []byte{guard<<5 | 2}
	for i := range bands {
		b = append(b, be16((o.depth+gains(i)+4)<<11)...)
	}
	return b
}

// forward 1D transforms, the exact opposites of lift53 and lift97

func analyse53(b []int32, cas int) {
	n := len(b)
	if n == 1 {
		if cas == 1 {
			b[0] *= 2
		}
		return
	}
	for i := 1 - cas; i < n; i += 2 {
		l, r := neighbours(i, n)
		b[i] -= (b[l] + b[r]) >> 1
	}
	for i := cas; i < n; i += 2 {
		l, r := neighbours(i, n)
		b[i] += (b[l] + b[r] + 2) >> 2
	}
}

func analyse97(b []float32, cas int) {
	n := len(b)
	if n == 1 {
		if cas == 1 {
			b[0] *= 2
		}
		return
	}
	step := func(start int, f float32) {
		for i := start; i < n; i += 2 {
			l, r := neighbours(i, n)
			b[i] += f * (b[l] + b[r])
		}
	}
	step(1-cas, dwtAlpha)
	step(cas, dwtBeta)
	step(1-cas, dwtGamma)
	step(cas, dwtDelta)
	for i := cas; i < n; i += 2 {
		b[i] /= dwtK
	}
	for i := 1 - cas; i < n; i += 2 {
		b[i] *= dwtK
	}
}

func analyse[T int32 | float32](data []T, stride int, res []jpxRes, lift func([]T, int)) {
	for r := len(res) - 1; r >= 1; r-- {
		cur, low := &res[r], &res[r-1]
		w, h := cur.x1-cur.x0, cur.y1-cur.y0
		lw, lh := low.x1-low.x0, low.y1-low.y0
		if w <= 0 || h <= 0 {
			continue
		}
		casX, casY := cur.x0&1, cur.y0&1
		col := make([]T, h)
		for x := range w {
			for i := range h {
				col[i] = data[i*stride+x]
			}
			lift(col, casY)
			for i := range lh {
				data[i*stride+x] = col[casY+2*i]
			}
			for i := lh; i < h; i++ {
				data[i*stride+x] = col[1-casY+2*(i-lh)]
			}
		}
		row := make([]T, w)
		for y := range h {
			src := data[y*stride:][:w]
			copy(row, src)
			lift(row, casX)
			for i := range lw {
				src[i] = row[casX+2*i]
			}
			for i := lw; i < w; i++ {
				src[i] = row[1-casX+2*(i-lw)]
			}
		}
	}
}

// tier 1 encoder

// rawEncoder writes the bypass bits with their stuffing.
type rawEncoder struct {
	out  []byte
	cur  byte
	free int
}

func newRawEncoder() *rawEncoder { return &rawEncoder{free: 8} }

func (r *rawEncoder) put(bit int) {
	r.free--
	r.cur |= byte(bit) << r.free
	if r.free == 0 {
		r.out = append(r.out, r.cur)
		r.free = 8
		if r.cur == 0xFF {
			r.free = 7
		}
		r.cur = 0
	}
}

func (r *rawEncoder) flush() []byte {
	full := 8
	if len(r.out) > 0 && r.out[len(r.out)-1] == 0xFF {
		full = 7
	}
	if r.free < full {
		r.out = append(r.out, r.cur)
	}
	if n := len(r.out); n > 0 && r.out[n-1] == 0xFF {
		r.out = r.out[:n-1]
	}
	return r.out
}

// t1Enc encodes one code block with the same flag tables as the decoder.
type t1Enc struct {
	t1
	mag []uint32
	neg []bool
	enc *mqEncoder
	raw *rawEncoder
}

func (e *t1Enc) sign(idx, at int, f uint16, raw bool) {
	neg := e.neg[at]
	if raw {
		e.raw.put(boolInt(neg))
	} else {
		sc := t1SC[f>>4&0xFF]
		e.enc.encode(&e.cx[sc&0x1F], boolInt(neg != (sc&0x80 != 0)))
	}
	e.significant(idx, neg)
}

func (e *t1Enc) sigPass(bp, orient int, raw, vsc bool) {
	zc := &t1ZC[orient]
	for k := 0; k < e.h; k += 4 {
		for i := range e.w {
			for j := k; j < min(k+4, e.h); j++ {
				idx := (j+1)*e.stride + i + 1
				f := e.flags[idx]
				if vsc && j&3 == 3 {
					f &^= t1Below
				}
				if f&t1SigOther == 0 || f&(t1Sig|t1Visit) != 0 {
					continue
				}
				at := j*e.w + i
				b := int(e.mag[at]>>bp) & 1
				if raw {
					e.raw.put(b)
				} else {
					e.enc.encode(&e.cx[zc[f&0xFF]], b)
				}
				if b != 0 {
					e.sign(idx, at, f, raw)
				}
				e.flags[idx] |= t1Visit
			}
		}
	}
}

func (e *t1Enc) refPass(bp int, raw, vsc bool) {
	for k := 0; k < e.h; k += 4 {
		for i := range e.w {
			for j := k; j < min(k+4, e.h); j++ {
				idx := (j+1)*e.stride + i + 1
				f := e.flags[idx]
				if f&(t1Sig|t1Visit) != t1Sig {
					continue
				}
				if vsc && j&3 == 3 {
					f &^= t1Below
				}
				b := int(e.mag[j*e.w+i]>>bp) & 1
				if raw {
					e.raw.put(b)
				} else {
					c := ctxMag
					if f&t1Refine != 0 {
						c = ctxMag + 2
					} else if f&t1SigOther != 0 {
						c = ctxMag + 1
					}
					e.enc.encode(&e.cx[c], b)
				}
				e.flags[idx] |= t1Refine
			}
		}
	}
}

func (e *t1Enc) cleanPass(bp, orient int, vsc, segsym bool) {
	zc := &t1ZC[orient]
	for k := 0; k < e.h; k += 4 {
		for i := range e.w {
			agg := false
			run := 0
			if k+3 < e.h {
				base := (k+1)*e.stride + i + 1
				last := e.flags[base+3*e.stride]
				if vsc {
					last &^= t1Below
				}
				all := e.flags[base] | e.flags[base+e.stride] | e.flags[base+2*e.stride] | last
				agg = all&(t1Sig|t1Visit|t1SigOther) == 0
			}
			if agg {
				for run < 4 && e.mag[(k+run)*e.w+i]>>bp&1 == 0 {
					run++
				}
				if run == 4 {
					e.enc.encode(&e.cx[ctxRun], 0)
					continue
				}
				e.enc.encode(&e.cx[ctxRun], 1)
				e.enc.encode(&e.cx[ctxUni], run>>1)
				e.enc.encode(&e.cx[ctxUni], run&1)
			}
			for j := k + run; j < min(k+4, e.h); j++ {
				idx := (j+1)*e.stride + i + 1
				at := j*e.w + i
				f := e.flags[idx]
				if vsc && j&3 == 3 {
					f &^= t1Below
				}
				if agg && j == k+run {
					e.sign(idx, at, f, false)
				} else if f&(t1Sig|t1Visit) == 0 {
					b := int(e.mag[at]>>bp) & 1
					e.enc.encode(&e.cx[zc[f&0xFF]], b)
					if b != 0 {
						e.sign(idx, at, f, false)
					}
				}
				e.flags[idx] &^= t1Visit
			}
		}
	}
	if segsym {
		for _, b := range []int{1, 0, 1, 0} {
			e.enc.encode(&e.cx[ctxUni], b)
		}
	}
}

// encSeg is a finished codeword segment.
type encSeg struct {
	passes int
	data   []byte
}

// encode returns the bit plane count and the segments of a code block.
func (e *t1Enc) encode(w, h, orient, style int, coef []int64) (int, []encSeg) {
	e.prepare(w, h)
	e.resetContexts()
	e.mag = make([]uint32, w*h)
	e.neg = make([]bool, w*h)
	var top uint32
	for i, v := range coef {
		e.neg[i] = v < 0
		if v < 0 {
			v = -v
		}
		e.mag[i] = uint32(v)
		top = max(top, uint32(v))
	}
	numbps := bits.Len32(top)
	if numbps == 0 {
		return 0, nil
	}
	total := 3*numbps - 2
	bypass := style&styleBypass != 0
	vsc := style&styleVSC != 0
	var segs []encSeg
	var prev *jpxSeg
	capacity := segmentCapacity(style, nil)
	inSeg := 0
	bp, ptype := numbps-1, 2
	for pass := range total {
		raw := bypass && pass >= 10 && ptype < 2
		if inSeg == 0 {
			if raw {
				e.raw = newRawEncoder()
			} else {
				e.enc = newMQEncoder()
			}
		}
		switch ptype {
		case 0:
			e.sigPass(bp, orient, raw, vsc)
		case 1:
			e.refPass(bp, raw, vsc)
		default:
			e.cleanPass(bp, orient, vsc, style&styleSegSym != 0)
		}
		if style&styleReset != 0 {
			e.resetContexts()
		}
		inSeg++
		if inSeg == capacity || pass == total-1 {
			var data []byte
			if raw {
				data = e.raw.flush()
			} else {
				data = e.enc.flush()
			}
			segs = append(segs, encSeg{inSeg, data})
			prev = &jpxSeg{maxPasses: capacity}
			capacity = segmentCapacity(style, prev)
			inSeg = 0
		}
		ptype++
		if ptype == 3 {
			ptype = 0
			bp--
		}
	}
	return numbps, segs
}

// packet encoder

type headerWriter struct {
	out  []byte
	cur  byte
	free int
}

func newHeaderWriter() *headerWriter { return &headerWriter{free: 8} }

func (w *headerWriter) put(bit int) {
	w.free--
	w.cur |= byte(bit) << w.free
	if w.free == 0 {
		w.out = append(w.out, w.cur)
		w.free = 8
		if w.cur == 0xFF {
			w.free = 7
		}
		w.cur = 0
	}
}

func (w *headerWriter) bits(v, n int) {
	for i := n - 1; i >= 0; i-- {
		w.put(v >> i & 1)
	}
}

func (w *headerWriter) finish() []byte {
	full := 8
	if n := len(w.out); n > 0 && w.out[n-1] == 0xFF {
		full = 7
	}
	if w.free < full {
		w.out = append(w.out, w.cur)
	}
	if n := len(w.out); n > 0 && w.out[n-1] == 0xFF {
		w.out = append(w.out, 0)
	}
	return w.out
}

// tagEnc encodes a tag tree over the node layout of the decoder.
type tagEnc struct {
	t     *tagTree
	known []bool
}

func newTagEnc(w, h int, leaves []int32) *tagEnc {
	t := newTagTree(w, h)
	copy(t.value, leaves)
	// Parents always come after their children in the node order.
	for i := range t.value {
		if p := t.parent[i]; p >= 0 {
			t.value[p] = min(t.value[p], t.value[i])
		}
	}
	return &tagEnc{t, make([]bool, len(t.value))}
}

func (e *tagEnc) encode(w *headerWriter, leaf, threshold int) {
	t := e.t
	var stack []int32
	node := int32(leaf)
	for t.parent[node] >= 0 {
		stack = append(stack, node)
		node = t.parent[node]
	}
	low := int32(0)
	for {
		if low > t.low[node] {
			t.low[node] = low
		} else {
			low = t.low[node]
		}
		for int(low) < threshold {
			if low >= t.value[node] {
				if !e.known[node] {
					w.put(1)
					e.known[node] = true
				}
				break
			}
			w.put(0)
			low++
		}
		t.low[node] = low
		if len(stack) == 0 {
			return
		}
		node = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
	}
}

func putPassCount(w *headerWriter, n int) {
	switch {
	case n == 1:
		w.put(0)
	case n == 2:
		w.bits(2, 2)
	case n <= 5:
		w.bits(0xC|(n-3), 4)
	case n <= 36:
		w.bits(0x1E0|(n-6), 9)
	default:
		w.bits(0xFF80|(n-37), 16)
	}
}

// encBlock is an encoded code block.
type encBlock struct {
	numbps int
	segs   []encSeg
}

// packetBytes writes the one packet of a precinct.
func packetBytes(res *jpxRes, k int, blocks map[*jpxCblk]*encBlock, o *encOptions, seq int) []byte {
	w := newHeaderWriter()
	w.put(1)
	var body []byte
	for b := range res.bands {
		band := &res.bands[b]
		prec := &band.precs[k]
		if len(prec.cblks) == 0 {
			continue
		}
		incl := make([]int32, len(prec.cblks))
		zero := make([]int32, len(prec.cblks))
		for i := range prec.cblks {
			if blk := blocks[&prec.cblks[i]]; blk != nil && blk.numbps > 0 {
				zero[i] = int32(band.numbps - blk.numbps)
			} else {
				incl[i] = 1
				zero[i] = 1 << 20
			}
		}
		ti := newTagEnc(prec.cw, prec.ch, incl)
		tz := newTagEnc(prec.cw, prec.ch, zero)
		for i := range prec.cblks {
			blk := blocks[&prec.cblks[i]]
			ti.encode(w, i, 1)
			if incl[i] != 0 {
				continue
			}
			tz.encode(w, i, 1<<19)
			passes := 0
			lblockUp := 0
			for _, s := range blk.segs {
				passes += s.passes
				need := bits.Len(uint(len(s.data)))
				lblockUp = max(lblockUp, need-(3+floorLog2(s.passes)))
			}
			putPassCount(w, passes)
			for range lblockUp {
				w.put(1)
			}
			w.put(0)
			for _, s := range blk.segs {
				w.bits(len(s.data), 3+lblockUp+floorLog2(s.passes))
				body = append(body, s.data...)
			}
		}
	}
	var out []byte
	if o.sop {
		out = append(out, 0xFF, 0x91, 0, 4, byte(seq>>8), byte(seq))
	}
	out = append(out, w.finish()...)
	if o.eph {
		out = append(out, 0xFF, 0x92)
	}
	return append(out, body...)
}

// encodeJPX writes a bare codestream, LRCP with one layer.
func encodeJPX(o encOptions) []byte {
	out := []byte{0xFF, 0x4F}
	out = append(out, msegment(mSIZ, o.sizBody())...)
	out = append(out, msegment(mCOD, o.codBody())...)
	out = append(out, msegment(mQCD, o.qcdBody())...)
	d := &jpxDecoder{limit: maxImagePixels}
	if err := d.parseSIZ(o.sizBody()); err != nil {
		panic(err)
	}
	for _, m := range []struct {
		id   int
		body []byte
	}{{mCOD, o.codBody()}, {mQCD, o.qcdBody()}} {
		if err := d.parseParam(m.id, m.body, &d.main); err != nil {
			panic(err)
		}
	}
	if o.roi > 0 {
		for c := range o.comps {
			body := []byte{byte(c), 0, byte(o.roi)}
			out = append(out, msegment(mRGN, body)...)
			if err := d.parseParam(mRGN, body, &d.main); err != nil {
				panic(err)
			}
		}
	}
	for index := range d.tilesX * d.tilesY {
		px, py := index%d.tilesX, index/d.tilesX
		t := &tile{
			d: d, p: &d.main,
			x0: max(px*d.xtsiz, d.xosiz), y0: max(py*d.ytsiz, d.yosiz),
			x1: min((px+1)*d.xtsiz, d.xsiz), y1: min((py+1)*d.ytsiz, d.ysiz),
		}
		if err := t.build(); err != nil {
			panic(err)
		}
		packets := encodeTile(t, &o)
		parts := max(o.parts, 1)
		for part := range parts {
			var data []byte
			for _, p := range packets[part*len(packets)/parts : (part+1)*len(packets)/parts] {
				data = append(data, p...)
			}
			head := slices.Concat(be16(index), be32(12+2+len(data)), []byte{byte(part), byte(parts)})
			out = append(out, msegment(mSOT, head)...)
			out = append(out, 0xFF, 0x93)
			out = append(out, data...)
		}
	}
	return append(out, 0xFF, 0xD9)
}

// encodeTile transforms and codes one tile and returns its packets in LRCP order.
func encodeTile(t *tile, o *encOptions) [][]byte {
	ints := make([][]int32, len(t.comps))
	floats := make([][]float32, len(t.comps))
	shift := int32(0)
	if !o.signed {
		shift = 1 << (o.depth - 1)
	}
	for c := range t.comps {
		tc := &t.comps[c]
		comp := &t.d.comps[c]
		w, h := tc.x1-tc.x0, tc.y1-tc.y0
		ints[c] = make([]int32, w*h)
		for y := range h {
			copy(ints[c][y*w:][:w], o.comps[c].plane[(tc.y0-comp.y0+y)*comp.w+tc.x0-comp.x0:])
		}
		for i := range ints[c] {
			ints[c][i] -= shift
		}
	}
	if o.mct {
		if o.reversible {
			r, g, b := ints[0], ints[1], ints[2]
			for i := range r {
				y := (r[i] + 2*g[i] + b[i]) >> 2
				u := b[i] - g[i]
				v := r[i] - g[i]
				r[i], g[i], b[i] = y, u, v
			}
		} else {
			for c := range 3 {
				floats[c] = toFloats(ints[c])
			}
			r, g, b := floats[0], floats[1], floats[2]
			for i := range r {
				cr, cg, cb := r[i], g[i], b[i]
				r[i] = 0.299*cr + 0.587*cg + 0.114*cb
				g[i] = -0.16875*cr - 0.33126*cg + 0.5*cb
				b[i] = 0.5*cr - 0.41869*cg - 0.08131*cb
			}
		}
	}
	blocks := map[*jpxCblk]*encBlock{}
	enc := &t1Enc{}
	for c := range t.comps {
		tc := &t.comps[c]
		stride := tc.x1 - tc.x0
		if o.reversible {
			analyse(ints[c], stride, tc.res, analyse53)
		} else {
			if floats[c] == nil {
				floats[c] = toFloats(ints[c])
			}
			analyse(floats[c], stride, tc.res, analyse97)
		}
		for r := range tc.res {
			res := &tc.res[r]
			for b := range res.bands {
				band := &res.bands[b]
				for p := range band.precs {
					prec := &band.precs[p]
					for i := range prec.cblks {
						cb := &prec.cblks[i]
						w, h := cb.x1-cb.x0, cb.y1-cb.y0
						coef := make([]int64, w*h)
						for y := range h {
							off := (cb.y0-band.y0+band.yoff+y)*stride + cb.x0 - band.x0 + band.xoff
							for x := range w {
								var v int64
								if o.reversible {
									v = int64(ints[c][off+x])
								} else {
									f := float64(floats[c][off+x]) / float64(band.step)
									v = int64(math.Trunc(f))
								}
								coef[y*w+x] = v << o.roi
							}
						}
						numbps, segs := enc.encode(w, h, band.orient, o.style, coef)
						blocks[cb] = &encBlock{numbps, segs}
					}
				}
			}
		}
	}
	var packets [][]byte
	maxRes := o.levels + 1
	for r := range maxRes {
		for c := range t.comps {
			res := &t.comps[c].res[r]
			for k := range res.next {
				packets = append(packets, packetBytes(res, k, blocks, o, len(packets)))
			}
		}
	}
	return packets
}

func toFloats(v []int32) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

// wrapJP2 puts a codestream into JP2 boxes.
func wrapJP2(cs []byte, jp2h ...[]byte) []byte {
	box := func(typ string, body []byte) []byte {
		return slices.Concat(be32(len(body)+8), []byte(typ), body)
	}
	out := box("jP  ", []byte{0x0D, 0x0A, 0x87, 0x0A})
	out = append(out, box("ftyp", []byte("jp2 \x00\x00\x00\x00jp2 "))...)
	out = append(out, box("jp2h", bytes.Join(jp2h, nil))...)
	return append(out, box("jp2c", cs)...)
}

func boxBytes(typ string, body []byte) []byte {
	return slices.Concat(be32(len(body)+8), []byte(typ), body)
}
