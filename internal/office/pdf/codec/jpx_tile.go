package codec

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
)

var (
	errPacketEnd = errors.New("jpx: packet data ended")
	errNoPackets = errors.New("jpx: no packet could be decoded")
)

// jpxSeg is a codeword segment of a code block, split over packets.
type jpxSeg struct {
	passes    int
	maxPasses int
	newPasses int
	newLen    int
	chunks    [][]byte
}

type jpxCblk struct {
	x0, y0, x1, y1 int
	numbps         int
	lblock         int
	included       bool
	segs           []jpxSeg
}

type jpxPrecinct struct {
	cw, ch int
	cblks  []jpxCblk
	incl   *tagTree
	zero   *tagTree
}

type jpxBand struct {
	orient         int // 0 LL, 1 HL, 2 LH, 3 HH
	x0, y0, x1, y1 int
	xoff, yoff     int // of the band in the Mallat layout of the tile component
	numbps         int
	step           float32
	precs          []jpxPrecinct
}

type jpxRes struct {
	x0, y0, x1, y1 int
	ppx, ppy       int
	pw, ph         int
	bands          []jpxBand
	next           []int // next layer of each precinct
}

type jpxTileComp struct {
	x0, y0, x1, y1 int
	coding         *jpxCoding
	res            []jpxRes
	idata          []int32
	fdata          []float32
}

// tagTree is the tag tree of T.800 B.10.2.
type tagTree struct {
	parent []int32
	value  []int32
	low    []int32
	stack  []int32
}

func newTagTree(w, h int) *tagTree {
	var sizes [][2]int
	total := 0
	for {
		sizes = append(sizes, [2]int{w, h})
		total += w * h
		if w <= 1 && h <= 1 {
			break
		}
		w, h = (w+1)/2, (h+1)/2
	}
	t := &tagTree{
		parent: make([]int32, total),
		value:  make([]int32, total),
		low:    make([]int32, total),
		stack:  make([]int32, 0, len(sizes)),
	}
	base := 0
	for l, s := range sizes {
		next := base + s[0]*s[1]
		for y := range s[1] {
			for x := range s[0] {
				i := base + y*s[0] + x
				t.value[i] = math.MaxInt32
				if l+1 < len(sizes) {
					t.parent[i] = int32(next + (y/2)*sizes[l+1][0] + x/2)
				} else {
					t.parent[i] = -1
				}
			}
		}
		base = next
	}
	return t
}

// decode reports whether the value of the leaf is below threshold.
func (t *tagTree) decode(r *packetReader, leaf, threshold int) (bool, error) {
	t.stack = t.stack[:0]
	node := int32(leaf)
	for t.parent[node] >= 0 {
		t.stack = append(t.stack, node)
		node = t.parent[node]
	}
	low := int32(0)
	for {
		if low > t.low[node] {
			t.low[node] = low
		} else {
			low = t.low[node]
		}
		for int(low) < threshold && low < t.value[node] {
			b, err := r.bit()
			if err != nil {
				return false, err
			}
			if b != 0 {
				t.value[node] = low
			} else {
				low++
			}
		}
		t.low[node] = low
		if len(t.stack) == 0 {
			return int(t.value[node]) < threshold, nil
		}
		node = t.stack[len(t.stack)-1]
		t.stack = t.stack[:len(t.stack)-1]
	}
}

// packetReader reads packet header bits with the stuffing of T.800 B.10.1.
type packetReader struct {
	data []byte
	pos  int
	last byte
	ct   int
}

func (r *packetReader) bit() (int, error) {
	if r.ct == 0 {
		if r.pos >= len(r.data) {
			return 0, errPacketEnd
		}
		r.ct = 8
		if r.last == 0xFF {
			r.ct = 7
		}
		r.last = r.data[r.pos]
		r.pos++
	}
	r.ct--
	return int(r.last>>r.ct) & 1, nil
}

func (r *packetReader) bits(n int) (int, error) {
	v := 0
	for range n {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | b
	}
	return v, nil
}

// align ends a header: a final 0xFF is followed by a stuffed byte.
func (r *packetReader) align() {
	if r.last == 0xFF {
		r.pos++
	}
	r.last = 0
	r.ct = 0
}

// tile is the working state of one tile being decoded.
type tile struct {
	d       *jpxDecoder
	p       *jpxParams
	x0, y0  int
	x1, y1  int
	comps   []jpxTileComp
	maxRes  int
	reader  packetReader
	pending []pendingSeg
	cblks   int
	precs   int
	loops   int64
}

// pendingSeg is a segment whose data follows the packet header being read.
type pendingSeg struct {
	cb  *jpxCblk
	idx int
}

// decodeTiles runs every tile, keeping whatever a damaged tile still gives.
func (d *jpxDecoder) decodeTiles() error {
	for _, t := range d.tiles {
		if err := d.decodeTile(t); err != nil {
			return err
		}
		t.parts = nil
	}
	if d.packets == 0 {
		return errNoPackets
	}
	return nil
}

func (d *jpxDecoder) decodeTile(src *jpxTile) error {
	p := src.params
	px := src.index % d.tilesX
	py := src.index / d.tilesX
	t := &tile{
		d: d, p: p,
		x0: max(d.xtosiz+px*d.xtsiz, d.xosiz),
		y0: max(d.ytosiz+py*d.ytsiz, d.yosiz),
		x1: min(d.xtosiz+(px+1)*d.xtsiz, d.xsiz),
		y1: min(d.ytosiz+(py+1)*d.ytsiz, d.ysiz),
	}
	if t.x1 <= t.x0 || t.y1 <= t.y0 || len(src.parts) == 0 {
		return nil
	}
	if err := t.build(); err != nil {
		return err
	}
	data := src.parts[0]
	if len(src.parts) > 1 {
		data = slices.Concat(src.parts...)
	}
	t.reader.data = data
	before := d.packets
	if err := t.readPackets(); err != nil {
		return err
	}
	if d.packets == before {
		return nil // nothing to transform, and a large tile is costly
	}
	if err := t.tier1(); err != nil {
		return err
	}
	t.inverseDWT()
	if err := t.inverseMCT(); err != nil {
		return err
	}
	t.store()
	return nil
}

// build lays out resolutions, bands, precincts and code blocks, B.5 to B.7.
func (t *tile) build() error {
	d := t.d
	t.comps = make([]jpxTileComp, len(d.comps))
	for c := range d.comps {
		comp := &d.comps[c]
		tc := &t.comps[c]
		cod := &t.p.coding[c]
		tc.coding = cod
		tc.x0 = ceilDiv(t.x0, comp.dx)
		tc.y0 = ceilDiv(t.y0, comp.dy)
		tc.x1 = ceilDiv(t.x1, comp.dx)
		tc.y1 = ceilDiv(t.y1, comp.dy)
		t.maxRes = max(t.maxRes, cod.levels+1)
		tc.res = make([]jpxRes, cod.levels+1)
		var prev *jpxRes
		for r := range tc.res {
			res := &tc.res[r]
			nd := cod.levels - r
			res.x0 = ceilShift(tc.x0, nd)
			res.y0 = ceilShift(tc.y0, nd)
			res.x1 = ceilShift(tc.x1, nd)
			res.y1 = ceilShift(tc.y1, nd)
			res.ppx, res.ppy = 15, 15
			if cod.precincts != nil {
				res.ppx = int(cod.precincts[r] & 15)
				res.ppy = int(cod.precincts[r] >> 4)
			}
			if r > 0 && (res.ppx == 0 || res.ppy == 0) {
				return errors.New("jpx: bad precinct size")
			}
			if res.x1 > res.x0 {
				res.pw = ceilShift(res.x1, res.ppx) - res.x0>>res.ppx
			}
			if res.y1 > res.y0 {
				res.ph = ceilShift(res.y1, res.ppy) - res.y0>>res.ppy
			}
			count := res.pw * res.ph
			t.precs += count
			d.precs += count
			if t.precs > jpxMaxTilePrecs || d.precs > jpxMaxTotalPrecs {
				return errors.New("jpx: too many precincts")
			}
			res.next = make([]int, count)
			if err := t.buildBands(c, r, res, prev); err != nil {
				return err
			}
			prev = res
		}
	}
	return nil
}

func (t *tile) buildBands(c, r int, res, prev *jpxRes) error {
	tc := &t.comps[c]
	cod := tc.coding
	q := &t.p.quant[c]
	orients := []int{1, 2, 3}
	if r == 0 {
		orients = []int{0}
	}
	res.bands = make([]jpxBand, len(orients))
	for i, orient := range orients {
		b := &res.bands[i]
		b.orient = orient
		nb := cod.levels - r + 1
		if r == 0 {
			nb = cod.levels
		}
		xob, yob := orient&1, orient>>1
		if nb > 0 {
			half := 1 << (nb - 1)
			b.x0 = ceilShift(tc.x0-half*xob, nb)
			b.y0 = ceilShift(tc.y0-half*yob, nb)
			b.x1 = ceilShift(tc.x1-half*xob, nb)
			b.y1 = ceilShift(tc.y1-half*yob, nb)
		} else {
			b.x0, b.y0, b.x1, b.y1 = tc.x0, tc.y0, tc.x1, tc.y1
		}
		if prev != nil {
			if xob != 0 {
				b.xoff = prev.x1 - prev.x0
			}
			if yob != 0 {
				b.yoff = prev.y1 - prev.y0
			}
		}
		exp, mant, err := q.step(r, i)
		if err != nil {
			return err
		}
		b.numbps = max(exp+q.guard-1+t.p.roi[c], 0)
		gain := [4]int{0, 1, 1, 2}[orient]
		b.step = float32(math.Ldexp(1+float64(mant)/2048, t.d.comps[c].depth+gain-exp))

		ppx, ppy := res.ppx, res.ppy
		if r > 0 {
			ppx--
			ppy--
		}
		xcb := min(cod.xcb, ppx)
		ycb := min(cod.ycb, ppy)
		b.precs = make([]jpxPrecinct, res.pw*res.ph)
		baseX := res.x0 >> res.ppx
		baseY := res.y0 >> res.ppy
		for py := range res.ph {
			for px := range res.pw {
				x0 := max((baseX+px)<<ppx, b.x0)
				y0 := max((baseY+py)<<ppy, b.y0)
				x1 := min((baseX+px+1)<<ppx, b.x1)
				y1 := min((baseY+py+1)<<ppy, b.y1)
				if x1 <= x0 || y1 <= y0 {
					continue
				}
				if err := t.buildPrecinct(&b.precs[py*res.pw+px], x0, y0, x1, y1, xcb, ycb); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (t *tile) buildPrecinct(p *jpxPrecinct, x0, y0, x1, y1, xcb, ycb int) error {
	gx0, gy0 := x0>>xcb, y0>>ycb
	p.cw = ceilShift(x1, xcb) - gx0
	p.ch = ceilShift(y1, ycb) - gy0
	n := p.cw * p.ch
	t.cblks += n
	t.d.cblks += n
	if t.cblks > jpxMaxTileCblks || t.d.cblks > jpxMaxTotalCblks {
		return errors.New("jpx: too many code blocks")
	}
	p.cblks = make([]jpxCblk, n)
	for j := range p.ch {
		for i := range p.cw {
			cb := &p.cblks[j*p.cw+i]
			cb.x0 = max((gx0+i)<<xcb, x0)
			cb.y0 = max((gy0+j)<<ycb, y0)
			cb.x1 = min((gx0+i+1)<<xcb, x1)
			cb.y1 = min((gy0+j+1)<<ycb, y1)
		}
	}
	p.incl = newTagTree(p.cw, p.ch)
	p.zero = newTagTree(p.cw, p.ch)
	return nil
}

// step returns the exponent and mantissa of band b of resolution r.
func (q *jpxQuant) step(r, b int) (int, int, error) {
	idx := 0
	if r > 0 {
		idx = 3*(r-1) + 1 + b
	}
	if q.style == 1 {
		exp := int(q.steps[0] >> 11)
		if r > 0 {
			exp = max(exp-(r-1), 0)
		}
		return exp, int(q.steps[0] & 0x7FF), nil
	}
	if idx >= len(q.steps) {
		return 0, 0, errors.New("jpx: quantization marker has too few steps")
	}
	return int(q.steps[idx] >> 11), int(q.steps[idx] & 0x7FF), nil
}

// readPackets walks the progression. Damage or the end of the data only stops the
// tile: what was read so far is kept, as far as a picture can come of it.
func (t *tile) readPackets() error {
	seq := t.p.poc
	if len(seq) == 0 {
		seq = []jpxProgression{{re: t.maxRes, ce: len(t.comps), layers: t.p.layers, order: t.p.order}}
	}
	for i := range seq {
		e := seq[i]
		e.re = min(e.re, t.maxRes)
		e.ce = min(e.ce, len(t.comps))
		e.layers = min(e.layers, t.p.layers)
		var err error
		switch e.order {
		case 0:
			err = t.orderLRCP(e)
		case 1:
			err = t.orderRLCP(e)
		default:
			err = t.orderPosition(e)
		}
		if err != nil {
			if errors.Is(err, errPacketEnd) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (t *tile) orderLRCP(e jpxProgression) error {
	for l := range e.layers {
		for r := e.rs; r < e.re; r++ {
			if err := t.resolutionLayer(e, r, l); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *tile) orderRLCP(e jpxProgression) error {
	for r := e.rs; r < e.re; r++ {
		for l := range e.layers {
			if err := t.resolutionLayer(e, r, l); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *tile) resolutionLayer(e jpxProgression, r, l int) error {
	for c := e.cs; c < e.ce; c++ {
		tc := &t.comps[c]
		if r >= len(tc.res) {
			continue
		}
		for k := range tc.res[r].next {
			if err := t.packet(c, r, k, l); err != nil {
				return err
			}
		}
		if err := t.loop(1); err != nil {
			return err
		}
	}
	return nil
}

// loop counts progression steps that read nothing, which a hostile header could multiply.
func (t *tile) loop(n int) error {
	t.loops += int64(n)
	if t.loops > jpxMaxPacketLoops {
		return errPacketEnd
	}
	return nil
}

// precinctPos is a precinct at the place on the reference grid where it starts.
type precinctPos struct {
	x, y, c, r, k int
}

// orderPosition handles RPCL, PCRL and CPRL. Instead of scanning every grid position,
// as the standard words it, it sorts the precincts by the position that triggers them.
func (t *tile) orderPosition(e jpxProgression) error {
	var list []precinctPos
	for c := e.cs; c < e.ce; c++ {
		tc := &t.comps[c]
		comp := &t.d.comps[c]
		for r := e.rs; r < e.re && r < len(tc.res); r++ {
			res := &tc.res[r]
			nd := tc.coding.levels - r
			for py := range res.ph {
				y := t.y0
				if py > 0 || res.y0<<nd&(1<<(res.ppy+nd)-1) == 0 {
					y = comp.dy * ((res.y0>>res.ppy + py) << (res.ppy + nd))
				}
				if y >= t.y1 {
					continue
				}
				for px := range res.pw {
					x := t.x0
					if px > 0 || res.x0<<nd&(1<<(res.ppx+nd)-1) == 0 {
						x = comp.dx * ((res.x0>>res.ppx + px) << (res.ppx + nd))
					}
					if x >= t.x1 {
						continue
					}
					list = append(list, precinctPos{x, y, c, r, py*res.pw + px})
				}
			}
		}
	}
	var key func(a precinctPos) [5]int
	switch e.order {
	case 2: // RPCL
		key = func(a precinctPos) [5]int { return [5]int{a.r, a.y, a.x, a.c, a.k} }
	case 3: // PCRL
		key = func(a precinctPos) [5]int { return [5]int{a.y, a.x, a.c, a.r, a.k} }
	default: // CPRL
		key = func(a precinctPos) [5]int { return [5]int{a.c, a.y, a.x, a.r, a.k} }
	}
	slices.SortFunc(list, func(a, b precinctPos) int {
		ka, kb := key(a), key(b)
		return slices.CompareFunc(ka[:], kb[:], cmp.Compare[int])
	})
	for _, a := range list {
		for l := range e.layers {
			if err := t.packet(a.c, a.r, a.k, l); err != nil {
				return err
			}
		}
	}
	return nil
}

// packet reads the packet of one precinct and layer, B.9 and B.10.
func (t *tile) packet(c, r, k, l int) error {
	res := &t.comps[c].res[r]
	if l < res.next[k] {
		return t.loop(1) // already sent, under a progression change
	}
	res.next[k] = l + 1
	rd := &t.reader
	if rd.pos >= len(rd.data) {
		return errPacketEnd
	}
	if t.p.sop && rd.pos+6 <= len(rd.data) && marker(rd.data[rd.pos:]) == mSOP {
		rd.pos += 6
	}
	t.pending = t.pending[:0]
	if err := t.packetHeader(c, res, k, l); err != nil {
		return err
	}
	rd.align()
	if t.p.eph && rd.pos+2 <= len(rd.data) && marker(rd.data[rd.pos:]) == mEPH {
		rd.pos += 2
	}
	for _, p := range t.pending {
		seg := &p.cb.segs[p.idx]
		n := min(seg.newLen, len(rd.data)-rd.pos)
		seg.chunks = append(seg.chunks, rd.data[rd.pos:rd.pos+n])
		seg.passes += seg.newPasses
		seg.newPasses = 0
		rd.pos += n
		if n < seg.newLen {
			return errPacketEnd
		}
	}
	t.d.packets++
	return nil
}

func (t *tile) packetHeader(c int, res *jpxRes, k, l int) error {
	rd := &t.reader
	nonEmpty, err := rd.bit()
	if err != nil {
		return err
	}
	if nonEmpty == 0 {
		return nil
	}
	style := t.comps[c].coding.style
	for b := range res.bands {
		band := &res.bands[b]
		prec := &band.precs[k]
		for i := range prec.cblks {
			cb := &prec.cblks[i]
			var included bool
			if !cb.included {
				if included, err = prec.incl.decode(rd, i, l+1); err != nil {
					return err
				}
			} else {
				bit, err := rd.bit()
				if err != nil {
					return err
				}
				included = bit != 0
			}
			if !included {
				continue
			}
			if !cb.included {
				cb.included = true
				zero := 1
				for {
					ok, err := prec.zero.decode(rd, i, zero)
					if err != nil {
						return err
					}
					if ok {
						break
					}
					zero++
					if zero > 80 {
						return errPacketEnd
					}
				}
				cb.numbps = band.numbps + 1 - zero
				if cb.numbps < 0 {
					return errPacketEnd
				}
				if cb.numbps > jpxMaxDepth {
					return fmt.Errorf("jpx: %d bit planes are not supported", cb.numbps)
				}
				cb.lblock = 3
			}
			passes, err := readPassCount(rd)
			if err != nil {
				return err
			}
			for {
				bit, err := rd.bit()
				if err != nil {
					return err
				}
				if bit == 0 {
					break
				}
				cb.lblock++
				if cb.lblock > 64 {
					return errPacketEnd
				}
			}
			if err := t.readLengths(cb, style, passes); err != nil {
				return err
			}
		}
	}
	return nil
}

func readPassCount(rd *packetReader) (int, error) {
	bit, err := rd.bit()
	if err != nil || bit == 0 {
		return 1, err
	}
	if bit, err = rd.bit(); err != nil || bit == 0 {
		return 2, err
	}
	n, err := rd.bits(2)
	if err != nil {
		return 0, err
	}
	if n < 3 {
		return 3 + n, nil
	}
	if n, err = rd.bits(5); err != nil {
		return 0, err
	}
	if n < 31 {
		return 6 + n, nil
	}
	n, err = rd.bits(7)
	return 37 + n, err
}

// segmentCapacity is how many passes the codeword segment after prev can hold.
func segmentCapacity(style int, prev *jpxSeg) int {
	switch {
	case style&styleTermAll != 0:
		return 1
	case style&styleBypass != 0:
		if prev == nil {
			return 10
		}
		if prev.maxPasses == 1 || prev.maxPasses == 10 {
			return 2
		}
		return 1
	}
	return 1 << 30
}

// readLengths reads the length of each segment the new passes touch, B.10.7.
func (t *tile) readLengths(cb *jpxCblk, style, passes int) error {
	rd := &t.reader
	if len(cb.segs) == 0 {
		cb.segs = append(cb.segs, jpxSeg{maxPasses: segmentCapacity(style, nil)})
	}
	for passes > 0 {
		seg := &cb.segs[len(cb.segs)-1]
		if seg.passes+seg.newPasses >= seg.maxPasses {
			cb.segs = append(cb.segs, jpxSeg{maxPasses: segmentCapacity(style, seg)})
			seg = &cb.segs[len(cb.segs)-1]
		}
		n := min(seg.maxPasses-seg.passes-seg.newPasses, passes)
		bits := cb.lblock + floorLog2(n)
		if bits > 32 {
			return errPacketEnd
		}
		length, err := rd.bits(bits)
		if err != nil {
			return err
		}
		seg.newPasses = n
		seg.newLen = length
		passes -= n
		t.pending = append(t.pending, pendingSeg{cb, len(cb.segs) - 1})
	}
	return nil
}

func floorLog2(n int) int {
	r := 0
	for n > 1 {
		n >>= 1
		r++
	}
	return r
}
