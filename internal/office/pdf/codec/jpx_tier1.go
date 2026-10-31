package codec

import (
	"errors"
	"slices"
	"sync"
)

// Flags of one coefficient: its eight neighbours, their signs, and its own state.
const (
	t1NE = 1 << iota
	t1SE
	t1SW
	t1NW
	t1N
	t1E
	t1S
	t1W
	t1SgnN
	t1SgnE
	t1SgnS
	t1SgnW
	t1Sig
	t1Refine
	t1Visit

	t1SigOther = t1NE | t1SE | t1SW | t1NW | t1N | t1E | t1S | t1W
	// t1Below is what the vertically causal mode hides on a stripe's last row.
	t1Below = t1SE | t1SW | t1S | t1SgnS
)

// Contexts of tier 1.
const (
	ctxZC   = 0
	ctxSC   = 9
	ctxMag  = 14
	ctxRun  = 17
	ctxUni  = 18
	ctxSize = 19
)

var (
	t1ZC [4][256]uint8 // zero coding context by band orientation and neighbours
	t1SC [256]uint8    // sign context in bits 0..4, sign flip in bit 7
)

func init() {
	for orient := range t1ZC {
		for f := range 256 {
			t1ZC[orient][f] = zeroContext(f, orient)
		}
	}
	for f := range 256 {
		t1SC[f] = signContext(f << 4)
	}
}

func zeroContext(f, orient int) uint8 {
	pop := func(a, b int) int {
		n := 0
		if f&a != 0 {
			n++
		}
		if f&b != 0 {
			n++
		}
		return n
	}
	h := pop(t1W, t1E)
	v := pop(t1N, t1S)
	d := pop(t1NW, t1NE) + pop(t1SE, t1SW)
	if orient == 3 {
		hv := h + v
		switch {
		case d == 0:
			return uint8(min(hv, 2))
		case d == 1:
			return uint8(3 + min(hv, 2))
		case d == 2:
			return uint8(6 + min(hv, 1))
		}
		return 8
	}
	if orient == 1 {
		h, v = v, h
	}
	switch h {
	case 0:
		switch v {
		case 0:
			return uint8(min(d, 2))
		case 1:
			return 3
		}
		return 4
	case 1:
		if v == 0 {
			return uint8(5 + min(d, 1))
		}
		return 7
	}
	return 8
}

func signContext(f int) uint8 {
	part := func(sigA, sgnA, sigB, sgnB int) int {
		one := func(sig, sgn int) int {
			switch {
			case f&sig == 0:
				return 0
			case f&sgn != 0:
				return -1
			}
			return 1
		}
		return min(max(one(sigA, sgnA)+one(sigB, sgnB), -1), 1)
	}
	h := part(t1E, t1SgnE, t1W, t1SgnW)
	v := part(t1N, t1SgnN, t1S, t1SgnS)
	flip := uint8(0)
	if h < 0 || (h == 0 && v < 0) {
		h, v = -h, -v
		flip = 0x80
	}
	// With h >= 0: (0,0) 9, (0,1) 10, (1,-1) 11, (1,0) 12, (1,1) 13.
	if h == 0 {
		return uint8(ctxSC+v) | flip
	}
	return uint8(ctxSC+3+v) | flip
}

// rawDecoder reads the bypass bits of T.800 D.6.
type rawDecoder struct {
	data []byte
	pos  int
	c    uint32
	ct   int
}

func (r *rawDecoder) init(data []byte) {
	r.data, r.pos, r.c, r.ct = data, 0, 0, 0
}

func (r *rawDecoder) bit() int {
	if r.ct == 0 {
		next := uint32(0xFF)
		if r.pos < len(r.data) {
			next = uint32(r.data[r.pos])
		}
		if r.c == 0xFF {
			if next > 0x8F {
				r.c = 0xFF
				r.ct = 8
			} else {
				r.c = next
				r.pos++
				r.ct = 7
			}
		} else {
			r.c = next
			r.pos++
			r.ct = 8
		}
	}
	r.ct--
	return int(r.c>>r.ct) & 1
}

// t1 decodes one code block. Workers keep one each.
type t1 struct {
	w, h, stride int
	flags        []uint16
	data         []int32 // magnitudes with sign, with one fractional bit
	buf          []byte
	mq           mqDecoder
	raw          rawDecoder
	cx           [ctxSize]uint8
}

var t1Pool = sync.Pool{New: func() any { return new(t1) }}

func (t *t1) resetContexts() {
	t.cx = [ctxSize]uint8{}
	t.cx[ctxZC] = 4 << 1
	t.cx[ctxRun] = 3 << 1
	t.cx[ctxUni] = 46 << 1
}

func (t *t1) prepare(w, h int) {
	t.w, t.h, t.stride = w, h, w+2
	n := (w + 2) * (h + 2)
	if cap(t.flags) < n {
		t.flags = make([]uint16, n)
		t.data = make([]int32, w*h)
	}
	if cap(t.data) < w*h {
		t.data = make([]int32, w*h)
	}
	t.flags = t.flags[:n]
	t.data = t.data[:w*h]
	clear(t.flags)
	clear(t.data)
}

// significant marks a coefficient and tells its neighbours.
func (t *t1) significant(idx int, negative bool) {
	f := t.flags
	s := t.stride
	f[idx-s-1] |= t1SE
	f[idx-s+1] |= t1SW
	f[idx+s-1] |= t1NE
	f[idx+s+1] |= t1NW
	if negative {
		f[idx-s] |= t1S | t1SgnS
		f[idx-1] |= t1E | t1SgnE
		f[idx+1] |= t1W | t1SgnW
		f[idx+s] |= t1N | t1SgnN
	} else {
		f[idx-s] |= t1S
		f[idx-1] |= t1E
		f[idx+1] |= t1W
		f[idx+s] |= t1N
	}
	f[idx] |= t1Sig
}

func (t *t1) decodeSign(idx int, f uint16) bool {
	sc := t1SC[f>>4&0xFF]
	bit := t.mq.decode(&t.cx[sc&0x1F])
	return (bit != 0) != (sc&0x80 != 0)
}

func (t *t1) sigPass(bp, orient int, raw, vsc bool) {
	value := int32(3) << bp
	zc := &t1ZC[orient]
	for k := 0; k < t.h; k += 4 {
		end := min(k+4, t.h)
		for i := range t.w {
			for j := k; j < end; j++ {
				idx := (j+1)*t.stride + i + 1
				f := t.flags[idx]
				if vsc && j&3 == 3 {
					f &^= t1Below
				}
				if f&t1SigOther == 0 || f&(t1Sig|t1Visit) != 0 {
					continue
				}
				if raw {
					if t.raw.bit() != 0 {
						negative := t.raw.bit() != 0
						t.set(i, j, idx, value, negative)
					}
				} else if t.mq.decode(&t.cx[zc[f&0xFF]]) != 0 {
					t.set(i, j, idx, value, t.decodeSign(idx, f))
				}
				t.flags[idx] |= t1Visit
			}
		}
	}
}

func (t *t1) set(i, j, idx int, value int32, negative bool) {
	if negative {
		value = -value
	}
	t.data[j*t.w+i] = value
	t.significant(idx, negative)
}

func (t *t1) refPass(bp int, raw, vsc bool) {
	half := int32(1) << bp
	for k := 0; k < t.h; k += 4 {
		end := min(k+4, t.h)
		for i := range t.w {
			for j := k; j < end; j++ {
				idx := (j+1)*t.stride + i + 1
				f := t.flags[idx]
				if f&(t1Sig|t1Visit) != t1Sig {
					continue
				}
				if vsc && j&3 == 3 {
					f &^= t1Below
				}
				var bit int
				if raw {
					bit = t.raw.bit()
				} else {
					c := ctxMag
					if f&t1Refine != 0 {
						c = ctxMag + 2
					} else if f&t1SigOther != 0 {
						c = ctxMag + 1
					}
					bit = t.mq.decode(&t.cx[c])
				}
				delta := half
				if bit == 0 {
					delta = -half
				}
				p := &t.data[j*t.w+i]
				if *p < 0 {
					*p -= delta
				} else {
					*p += delta
				}
				t.flags[idx] |= t1Refine
			}
		}
	}
}

func (t *t1) cleanPass(bp, orient int, vsc, segsym bool) {
	value := int32(3) << bp
	zc := &t1ZC[orient]
	for k := 0; k < t.h; k += 4 {
		end := min(k+4, t.h)
		for i := range t.w {
			run := 0
			agg := false
			if k+3 < t.h {
				base := (k+1)*t.stride + i + 1
				last := t.flags[base+3*t.stride]
				if vsc {
					last &^= t1Below
				}
				all := t.flags[base] | t.flags[base+t.stride] | t.flags[base+2*t.stride] | last
				agg = all&(t1Sig|t1Visit|t1SigOther) == 0
			}
			if agg {
				if t.mq.decode(&t.cx[ctxRun]) == 0 {
					continue
				}
				run = t.mq.decode(&t.cx[ctxUni]) << 1
				run |= t.mq.decode(&t.cx[ctxUni])
			}
			for j := k + run; j < end; j++ {
				idx := (j+1)*t.stride + i + 1
				f := t.flags[idx]
				if vsc && j&3 == 3 {
					f &^= t1Below
				}
				if agg && j == k+run {
					t.set(i, j, idx, value, t.decodeSign(idx, f))
				} else if f&(t1Sig|t1Visit) == 0 {
					if t.mq.decode(&t.cx[zc[f&0xFF]]) != 0 {
						t.set(i, j, idx, value, t.decodeSign(idx, f))
					}
				}
				t.flags[idx] &^= t1Visit
			}
		}
	}
	if segsym {
		for range 4 {
			t.mq.decode(&t.cx[ctxUni])
		}
	}
}

// decode runs all passes of a code block, leaving the result in t.data.
func (t *t1) decode(cb *jpxCblk, orient, style int) {
	t.prepare(cb.x1-cb.x0, cb.y1-cb.y0)
	t.resetContexts()
	bypass := style&styleBypass != 0
	vsc := style&styleVSC != 0
	bp := cb.numbps - 1
	ptype := 2
	pass := 0
	for s := range cb.segs {
		seg := &cb.segs[s]
		data := joinChunks(seg.chunks, &t.buf)
		started := false
		wasRaw := false
		for range seg.passes {
			if bp < 0 {
				return
			}
			raw := bypass && pass >= 10 && ptype < 2
			if !started || raw != wasRaw {
				if raw {
					t.raw.init(data)
				} else {
					t.mq.init(data)
				}
				started, wasRaw = true, raw
			}
			switch ptype {
			case 0:
				t.sigPass(bp, orient, raw, vsc)
			case 1:
				t.refPass(bp, raw, vsc)
			default:
				t.cleanPass(bp, orient, vsc, style&styleSegSym != 0)
			}
			if style&styleReset != 0 {
				t.resetContexts()
			}
			pass++
			ptype++
			if ptype == 3 {
				ptype = 0
				bp--
			}
		}
	}
}

func joinChunks(chunks [][]byte, buf *[]byte) []byte {
	if len(chunks) == 1 {
		return chunks[0]
	}
	*buf = (*buf)[:0]
	for _, c := range chunks {
		*buf = append(*buf, c...)
	}
	return *buf
}

// t1Task is a code block with where its coefficients go.
type t1Task struct {
	cb   *jpxCblk
	band *jpxBand
	tc   *jpxTileComp
	roi  int
}

// tier1 decodes every code block of the tile into its coefficient arrays.
func (t *tile) tier1() error {
	var tasks []t1Task
	var work int64
	for c := range t.comps {
		tc := &t.comps[c]
		w, h := tc.x1-tc.x0, tc.y1-tc.y0
		if tc.coding.reversible {
			tc.idata = make([]int32, w*h)
		} else {
			tc.fdata = make([]float32, w*h)
		}
		for r := range tc.res {
			res := &tc.res[r]
			for b := range res.bands {
				band := &res.bands[b]
				for p := range band.precs {
					prec := &band.precs[p]
					for i := range prec.cblks {
						cb := &prec.cblks[i]
						passes := 0
						for s := range cb.segs {
							passes += cb.segs[s].passes
						}
						if passes == 0 || cb.numbps == 0 {
							continue
						}
						passes = min(passes, 3*cb.numbps-2)
						work += int64((cb.x1-cb.x0)*(cb.y1-cb.y0)) * int64(passes)
						tasks = append(tasks, t1Task{cb, band, tc, t.p.roi[c]})
					}
				}
			}
		}
	}
	t.d.t1Work -= work
	if t.d.t1Work < 0 {
		return errors.New("jpx: " + errWork.Error())
	}
	// Longest first spreads the load better over the workers.
	slices.SortFunc(tasks, func(a, b t1Task) int {
		return (b.cb.x1-b.cb.x0)*(b.cb.y1-b.cb.y0) - (a.cb.x1-a.cb.x0)*(a.cb.y1-a.cb.y0)
	})
	parallelFor(len(tasks), 4, func(lo, hi int) {
		w := t1Pool.Get().(*t1)
		defer t1Pool.Put(w)
		for _, task := range tasks[lo:hi] {
			task.run(w)
		}
	})
	return nil
}

func (task *t1Task) run(w *t1) {
	cb, band, tc := task.cb, task.band, task.tc
	w.decode(cb, band.orient, tc.coding.style)
	if task.roi > 0 {
		// Coefficients at or above 2^s belong to the region; the fractional bit counts too.
		thresh := int64(1) << (task.roi + 1)
		for i, v := range w.data {
			mag := int64(v)
			if mag < 0 {
				mag = -mag
			}
			if mag >= thresh {
				mag >>= task.roi
				if v < 0 {
					mag = -mag
				}
				w.data[i] = int32(mag)
			}
		}
	}
	stride := tc.x1 - tc.x0
	for j := range w.h {
		src := w.data[j*w.w:][:w.w]
		off := (cb.y0-band.y0+band.yoff+j)*stride + cb.x0 - band.x0 + band.xoff
		if tc.idata != nil {
			dst := tc.idata[off:][:w.w]
			for i, v := range src {
				dst[i] = v / 2
			}
		} else {
			dst := tc.fdata[off:][:w.w]
			step := band.step * 0.5
			for i, v := range src {
				dst[i] = float32(v) * step
			}
		}
	}
}
