package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// intContext is the context memory of one arithmetic integer decoder, annex A.
type intContext [512]uint8

// decode reads one integer. The second result reports the out-of-band value.
func (cx *intContext) decode(mq *mqDecoder) (int, bool) {
	prev := 1
	bit := func() int {
		d := mq.decode(&cx[prev])
		if prev < 256 {
			prev = prev<<1 | d
		} else {
			prev = (prev<<1|d)&511 | 256
		}
		return d
	}
	read := func(n int) int {
		v := 0
		for range n {
			v = v<<1 | bit()
		}
		return v
	}
	s := bit()
	var v int
	switch {
	case bit() == 0:
		v = read(2)
	case bit() == 0:
		v = read(4) + 4
	case bit() == 0:
		v = read(6) + 20
	case bit() == 0:
		v = read(8) + 84
	case bit() == 0:
		v = read(12) + 340
	default:
		v = read(32) + 4436
	}
	if s == 0 {
		return v, false
	}
	if v > 0 {
		return -v, false
	}
	return 0, true
}

// decodeIAID reads a symbol ID of n bits.
func decodeIAID(mq *mqDecoder, cx []uint8, n int) int {
	prev := 1
	for range n {
		prev = prev<<1 | mq.decode(&cx[prev])
	}
	return prev - 1<<n
}

// textContexts is the arithmetic state of a text region. A symbol dictionary keeps
// one alive across the aggregations it decodes.
type textContexts struct {
	iadt, iafs, iads, iait, iari, iardw, iardh, iardx, iardy intContext
	iaid                                                     []uint8
	gr                                                       []uint8
}

func newTextContexts(codeLen int, gr []uint8) *textContexts {
	if gr == nil {
		gr = make([]uint8, 1<<13)
	}
	return &textContexts{iaid: make([]uint8, 1<<(codeLen+1)), gr: gr}
}

// Reference corners of a text region.
const (
	cornerBottomLeft = iota
	cornerTopLeft
	cornerBottomRight
	cornerTopRight
)

// textParams holds the parameters of table 9 of T.88.
type textParams struct {
	huff, refine, transposed bool
	w, h                     int
	instances                uint32
	logStrips                int
	corner, op               int
	defPixel                 byte
	dsOffset                 int
	rTemplate                int
	rAT                      []atPixel
	syms                     []*bitmap
	codeLen                  int
	symCodes                 *huffTable // nil for fixed length codes in Huffman mode

	fs, ds, dt, rdw, rdh, rdx, rdy, rsize *huffTable
}

// value reads one integer from whichever entropy coder is in use.
func value(mq *mqDecoder, cx *intContext, r *bitReader, t *huffTable) (int, bool, error) {
	if mq != nil {
		v, oob := cx.decode(mq)
		return v, oob, nil
	}
	return t.decode(r)
}

// plain reads an integer for which the out-of-band value is an error.
func plain(mq *mqDecoder, cx *intContext, r *bitReader, t *huffTable) (int, error) {
	v, oob, err := value(mq, cx, r, t)
	if err == nil && oob {
		err = errors.New("unexpected out-of-band value")
	}
	return v, err
}

// decodeText is the text region decoding procedure of T.88 6.4.
func (d *jb2) decodeText(p *textParams, mq *mqDecoder, cx *textContexts, r *bitReader) (*bitmap, error) {
	out := newBitmap(p.w, p.h)
	out.fill(p.defPixel)
	strips := 1 << p.logStrips
	t, err := plain(mq, &cx.iadt, r, p.dt)
	if err != nil {
		return nil, err
	}
	stripT := -t * strips
	firstS := 0
	var count uint32
	for count < p.instances {
		if err := d.work.spend(1); err != nil {
			return nil, err
		}
		dt, err := plain(mq, &cx.iadt, r, p.dt)
		if err != nil {
			return nil, err
		}
		stripT += dt * strips
		dfs, err := plain(mq, &cx.iafs, r, p.fs)
		if err != nil {
			return nil, err
		}
		firstS += dfs
		curS := firstS
		for first := true; ; first = false {
			if !first {
				ids, oob, err := value(mq, &cx.iads, r, p.ds)
				if err != nil {
					if count >= p.instances {
						return out, nil // only the closing code is missing
					}
					return nil, err
				}
				if oob {
					break
				}
				curS += ids + p.dsOffset
			}
			if count >= p.instances {
				return out, nil // the end of the data does not matter to the picture
			}
			if err := d.work.spend(16); err != nil {
				return nil, err
			}
			curT := 0
			if strips > 1 {
				if mq != nil {
					curT, _ = cx.iait.decode(mq)
				} else {
					v, err := r.readBits(p.logStrips)
					if err != nil {
						return nil, err
					}
					curT = int(v)
				}
			}
			ti := stripT + curT
			var id int
			switch {
			case mq != nil:
				id = decodeIAID(mq, cx.iaid, p.codeLen)
			case p.symCodes != nil:
				if id, err = plain(nil, nil, r, p.symCodes); err != nil {
					return nil, err
				}
			default:
				v, err := r.readBits(p.codeLen)
				if err != nil {
					return nil, err
				}
				id = int(v)
			}
			if id < 0 || id >= len(p.syms) {
				return nil, fmt.Errorf("symbol %d is not in the dictionary of %d", id, len(p.syms))
			}
			ib := p.syms[id]
			if p.refine {
				ri := 0
				if mq != nil {
					ri, _ = cx.iari.decode(mq)
				} else if ri, err = r.readBit(); err != nil {
					return nil, err
				}
				if ri != 0 {
					if ib, err = d.refineInstance(p, mq, cx, r, ib); err != nil {
						return nil, err
					}
				}
			}
			wi, hi := ib.w, ib.h
			if !p.transposed && (p.corner == cornerTopRight || p.corner == cornerBottomRight) {
				curS += wi - 1
			} else if p.transposed && (p.corner == cornerBottomLeft || p.corner == cornerBottomRight) {
				curS += hi - 1
			}
			x, y := curS, ti
			if p.transposed {
				x, y = ti, curS
			}
			if p.corner == cornerTopRight || p.corner == cornerBottomRight {
				x -= wi - 1
			}
			if p.corner == cornerBottomLeft || p.corner == cornerBottomRight {
				y -= hi - 1
			}
			if err := d.work.spend(out.combine(ib, x, y, p.op)); err != nil {
				return nil, err
			}
			if !p.transposed && (p.corner == cornerTopLeft || p.corner == cornerBottomLeft) {
				curS += wi - 1
			} else if p.transposed && (p.corner == cornerTopLeft || p.corner == cornerTopRight) {
				curS += hi - 1
			}
			count++
		}
	}
	return out, nil
}

// refineInstance decodes a refined symbol instance, 6.4.11.
func (d *jb2) refineInstance(p *textParams, mq *mqDecoder, cx *textContexts, r *bitReader, ib *bitmap) (*bitmap, error) {
	rdw, err := plain(mq, &cx.iardw, r, p.rdw)
	if err != nil {
		return nil, err
	}
	rdh, err := plain(mq, &cx.iardh, r, p.rdh)
	if err != nil {
		return nil, err
	}
	rdx, err := plain(mq, &cx.iardx, r, p.rdx)
	if err != nil {
		return nil, err
	}
	rdy, err := plain(mq, &cx.iardy, r, p.rdy)
	if err != nil {
		return nil, err
	}
	size := 0
	if mq == nil {
		if size, err = plain(nil, nil, r, p.rsize); err != nil {
			return nil, err
		}
		r.align()
	}
	w, h := ib.w+rdw, ib.h+rdh
	if w < 0 || h < 0 || (w > 0 && h > 0 && !fits(w, h, d.limit)) {
		return nil, errors.New("refined symbol has a bad size")
	}
	dec := mq
	if dec == nil {
		dec = new(mqDecoder)
		dec.init(r.rest())
	}
	bm, err := decodeRefinement(dec, cx.gr, w, h, p.rTemplate, ib, rdw>>1+rdx, rdh>>1+rdy, false, p.rAT, &d.work)
	if err != nil {
		return nil, err
	}
	if mq == nil {
		if err := r.skip(size); err != nil {
			return nil, err
		}
	}
	return bm, nil
}

// symbolDict is the result of a symbol dictionary segment.
type symbolDict struct {
	syms   []*bitmap
	gb, gr []uint8 // retained contexts, nil when not retained
}

// refTables returns the code tables a segment refers to, in order.
func (d *jb2) refTables(seg segHeader) []*huffTable {
	var out []*huffTable
	for _, n := range seg.refs {
		if t, ok := d.tables[n]; ok {
			out = append(out, t)
		}
	}
	return out
}

// refSymbols concatenates the exported symbols of the dictionaries a segment refers to.
func (d *jb2) refSymbols(seg segHeader) ([]*bitmap, *symbolDict, error) {
	var syms []*bitmap
	var last *symbolDict
	for _, n := range seg.refs {
		dict, ok := d.dicts[n]
		if !ok {
			if _, table := d.tables[n]; table {
				continue
			}
			return nil, nil, fmt.Errorf("refers to missing segment %d", n)
		}
		if len(syms)+len(dict.syms) > jb2MaxSymbols {
			return nil, nil, errors.New("too many symbols")
		}
		if err := d.work.spend(len(dict.syms)); err != nil {
			return nil, nil, err
		}
		syms = append(syms, dict.syms...)
		last = dict
	}
	return syms, last, nil
}

// tablePick walks through user tables as selection fields ask for them.
type tablePick struct {
	user []*huffTable
}

// next returns standard table n, or the next user table when n is 0.
func (p *tablePick) next(n int) (*huffTable, error) {
	if n != 0 {
		return standardTable(n), nil
	}
	if len(p.user) == 0 {
		return nil, errors.New("missing user code table")
	}
	t := p.user[0]
	p.user = p.user[1:]
	return t, nil
}

// choose maps a selection field to a table: fields lists the table of each value, 0 meaning user.
func (p *tablePick) choose(sel int, fields ...int) (*huffTable, error) {
	if sel >= len(fields) || fields[sel] < 0 {
		return nil, errors.New("bad Huffman table selection")
	}
	return p.next(fields[sel])
}

func symCodeLength(n int) int {
	if n <= 1 {
		return 0
	}
	return bits.Len(uint(n - 1))
}

// symbolDictSegment decodes a symbol dictionary, 7.4.2 and 6.5.
func (d *jb2) symbolDictSegment(seg segHeader, data []byte) error {
	if len(data) < 2 {
		return errShort
	}
	flags := int(binary.BigEndian.Uint16(data))
	body := data[2:]
	huff := flags&1 != 0
	refagg := flags&2 != 0
	template := flags >> 10 & 3
	rTemplate := flags >> 12 & 1
	var err error
	var at []atPixel
	if !huff {
		n := 1
		if template == 0 {
			n = 4
		}
		if at, body, err = parseAT(body, n); err != nil {
			return err
		}
	}
	rAT := []atPixel{{-1, -1}, {-1, -1}}
	if refagg && rTemplate == 0 {
		if rAT, body, err = parseAT(body, 2); err != nil {
			return err
		}
	}
	if len(body) < 8 {
		return errShort
	}
	numEx := binary.BigEndian.Uint32(body)
	numNew := binary.BigEndian.Uint32(body[4:])
	body = body[8:]

	in, lastDict, err := d.refSymbols(seg)
	if err != nil {
		return err
	}
	if int64(numNew)+int64(len(in)) > jb2MaxSymbols || numEx > jb2MaxSymbols {
		return fmt.Errorf("dictionary of %d symbols is too large", numNew)
	}

	var tDH, tDW, tBM, tAG *huffTable
	if huff {
		pick := tablePick{d.refTables(seg)}
		if tDH, err = pick.choose(flags>>2&3, 4, 5, -1, 0); err != nil {
			return err
		}
		if tDW, err = pick.choose(flags>>4&3, 2, 3, -1, 0); err != nil {
			return err
		}
		if tBM, err = pick.choose(flags>>6&1, 1, 0); err != nil {
			return err
		}
		if tAG, err = pick.choose(flags>>7&1, 1, 0); err != nil {
			return err
		}
	}

	// Context memory, possibly carried over from the last dictionary referred to.
	gb := make([]uint8, genericContexts(template))
	gr := make([]uint8, 1<<13)
	if flags&0x100 != 0 {
		if lastDict == nil || lastDict.gb == nil {
			return errors.New("retained contexts are missing")
		}
		if len(lastDict.gb) != len(gb) {
			return errors.New("retained contexts do not match the template")
		}
		copy(gb, lastDict.gb)
		copy(gr, lastDict.gr)
	}

	codeLen := symCodeLength(len(in) + int(numNew))
	if huff {
		codeLen = max(codeLen, 1)
	}
	var mq *mqDecoder
	r := &bitReader{data: body}
	if !huff {
		mq = new(mqDecoder)
		mq.init(body)
	}
	var iadh, iadw, iaai, iaex intContext
	cx := newTextContexts(codeLen, gr)

	syms := make([]*bitmap, len(in), len(in)+min(int(numNew), 1<<12))
	copy(syms, in)
	var widths []int
	if huff && !refagg {
		widths = make([]int, 0, min(int(numNew), 1<<12))
	}
	addPixels := func(w, h int) error {
		if w < 0 || h < 0 || (w > 0 && h > 0 && !fits(w, h, d.limit)) {
			return fmt.Errorf("symbol of %d by %d has a bad size", w, h)
		}
		return d.hold(w * h)
	}

	height := 0
	decoded := 0
	for decoded < int(numNew) {
		if err := d.work.spend(1); err != nil {
			return err
		}
		dh, err := plain(mq, &iadh, r, tDH)
		if err != nil {
			return err
		}
		height += dh
		if height < 0 || height > d.limit {
			return errors.New("bad height class")
		}
		width, total := 0, 0
		widths = widths[:0]
		for {
			if err := d.work.spend(1); err != nil {
				return err
			}
			dw, oob, err := value(mq, &iadw, r, tDW)
			if err != nil {
				return err
			}
			if oob {
				break
			}
			if decoded >= int(numNew) {
				return errors.New("more symbols than declared")
			}
			width += dw
			total += width
			if err := addPixels(width, height); err != nil {
				return err
			}
			switch {
			case !huff && !refagg:
				bm, err := decodeGeneric(mq, gb, width, height, template, at, false, nil, &d.work)
				if err != nil {
					return err
				}
				syms = append(syms, bm)
			case refagg:
				bm, err := d.aggregate(mq, r, cx, tAG, &iaai, syms, codeLen, width, height, rTemplate, rAT)
				if err != nil {
					return err
				}
				syms = append(syms, bm)
			default:
				widths = append(widths, width)
			}
			decoded++
		}
		if huff && !refagg {
			if total > 0 && height > 0 && !fits(total, height, d.limit) {
				return errors.New("height class is too large")
			}
			size, err := plain(nil, nil, r, tBM)
			if err != nil {
				return err
			}
			r.align()
			var sheet *bitmap
			if size == 0 {
				if sheet, err = rawBitmap(r, total, height); err != nil {
					return err
				}
			} else {
				if size < 0 || size > len(r.rest()) {
					return errShort
				}
				if sheet, _, err = decodeMMR(r.rest()[:size], total, height, &d.work); err != nil {
					return err
				}
				r.pos += size
			}
			x := 0
			for _, w := range widths {
				bm := newBitmap(w, height)
				bm.combine(sheet, -x, 0, opREPLACE)
				syms = append(syms, bm)
				x += w
			}
		}
	}

	// Export flags, 6.5.10.
	total := len(syms)
	exported := make([]*bitmap, 0, min(int(numEx), total))
	for i, flag := 0, false; i < total; flag = !flag {
		if err := d.work.spend(1); err != nil {
			return err
		}
		run, err := plain(mq, &iaex, r, standardTable(1))
		if err != nil {
			return err
		}
		if run < 0 || run > total-i {
			return errors.New("bad export run")
		}
		if flag {
			exported = append(exported, syms[i:i+run]...)
		}
		i += run
	}
	dict := &symbolDict{syms: exported}
	if flags&0x200 != 0 {
		dict.gb, dict.gr = gb, gr
	}
	d.dicts[seg.num] = dict
	return nil
}

// aggregate decodes one symbol made by refining others, 6.5.8.2.
func (d *jb2) aggregate(mq *mqDecoder, r *bitReader, cx *textContexts, tAG *huffTable, iaai *intContext, syms []*bitmap, codeLen, w, h, rTemplate int, rAT []atPixel) (*bitmap, error) {
	n, err := plain(mq, iaai, r, tAG)
	if err != nil {
		return nil, err
	}
	if n < 1 {
		return nil, errors.New("bad aggregate instance count")
	}
	if n > 1 {
		p := &textParams{
			huff: mq == nil, refine: true, w: w, h: h, instances: uint32(n),
			corner: cornerTopLeft, op: opOR, rTemplate: rTemplate, rAT: rAT,
			syms: syms, codeLen: codeLen,
		}
		if mq == nil {
			p.fs, p.ds, p.dt = standardTable(6), standardTable(8), standardTable(11)
			b15 := standardTable(15)
			p.rdw, p.rdh, p.rdx, p.rdy = b15, b15, b15, b15
			p.rsize = standardTable(1)
		}
		return d.decodeText(p, mq, cx, r)
	}
	var id, rdx, rdy, size int
	if mq != nil {
		id = decodeIAID(mq, cx.iaid, codeLen)
		rdx, _ = cx.iardx.decode(mq)
		rdy, _ = cx.iardy.decode(mq)
	} else {
		v, err := r.readBits(codeLen)
		if err != nil {
			return nil, err
		}
		id = int(v)
		b15 := standardTable(15)
		if rdx, err = plain(nil, nil, r, b15); err != nil {
			return nil, err
		}
		if rdy, err = plain(nil, nil, r, b15); err != nil {
			return nil, err
		}
		if size, err = plain(nil, nil, r, standardTable(1)); err != nil {
			return nil, err
		}
		r.align()
	}
	if id >= len(syms) {
		return nil, fmt.Errorf("symbol %d is not in the dictionary of %d", id, len(syms))
	}
	dec := mq
	if dec == nil {
		dec = new(mqDecoder)
		dec.init(r.rest())
	}
	bm, err := decodeRefinement(dec, cx.gr, w, h, rTemplate, syms[id], rdx, rdy, false, rAT, &d.work)
	if err != nil {
		return nil, err
	}
	if mq == nil {
		if err := r.skip(size); err != nil {
			return nil, err
		}
	}
	return bm, nil
}

// rawBitmap reads an uncompressed collective bitmap, rows padded to bytes.
func rawBitmap(r *bitReader, w, h int) (*bitmap, error) {
	stride := (w + 7) / 8
	data := r.rest()
	if stride*h > len(data) {
		return nil, errShort
	}
	bm := newBitmap(w, h)
	for y := range h {
		row := data[y*stride:]
		for x := range w {
			bm.pix[y*w+x] = row[x>>3] >> (7 - x&7) & 1
		}
	}
	r.pos += stride * h
	return bm, nil
}

// textSegment decodes a text region segment, 7.4.3.
func (d *jb2) textSegment(seg segHeader, data []byte) error {
	ri, err := d.parseRegion(data)
	if err != nil {
		return err
	}
	if len(data) < regionInfoSize+2 {
		return errShort
	}
	flags := int(binary.BigEndian.Uint16(data[regionInfoSize:]))
	body := data[regionInfoSize+2:]
	dso := flags >> 10 & 31
	if dso > 15 {
		dso -= 32
	}
	p := &textParams{
		huff:       flags&1 != 0,
		refine:     flags&2 != 0,
		logStrips:  flags >> 2 & 3,
		corner:     flags >> 4 & 3,
		transposed: flags&64 != 0,
		op:         flags >> 7 & 3,
		defPixel:   byte(flags >> 9 & 1),
		dsOffset:   dso,
		rTemplate:  flags >> 15 & 1,
		rAT:        []atPixel{{-1, -1}, {-1, -1}},
		w:          ri.w,
		h:          ri.h,
	}
	hflags := 0
	if p.huff {
		if len(body) < 2 {
			return errShort
		}
		hflags = int(binary.BigEndian.Uint16(body))
		body = body[2:]
	}
	if p.refine && p.rTemplate == 0 {
		if p.rAT, body, err = parseAT(body, 2); err != nil {
			return err
		}
	}
	if len(body) < 4 {
		return errShort
	}
	p.instances = binary.BigEndian.Uint32(body)
	body = body[4:]
	if p.syms, _, err = d.refSymbols(seg); err != nil {
		return err
	}
	p.codeLen = symCodeLength(len(p.syms))
	var mq *mqDecoder
	r := &bitReader{data: body}
	if p.huff {
		pick := tablePick{d.refTables(seg)}
		if p.fs, err = pick.choose(hflags&3, 6, 7, -1, 0); err != nil {
			return err
		}
		if p.ds, err = pick.choose(hflags>>2&3, 8, 9, 10, 0); err != nil {
			return err
		}
		if p.dt, err = pick.choose(hflags>>4&3, 11, 12, 13, 0); err != nil {
			return err
		}
		if p.rdw, err = pick.choose(hflags>>6&3, 14, 15, -1, 0); err != nil {
			return err
		}
		if p.rdh, err = pick.choose(hflags>>8&3, 14, 15, -1, 0); err != nil {
			return err
		}
		if p.rdx, err = pick.choose(hflags>>10&3, 14, 15, -1, 0); err != nil {
			return err
		}
		if p.rdy, err = pick.choose(hflags>>12&3, 14, 15, -1, 0); err != nil {
			return err
		}
		if p.rsize, err = pick.choose(hflags>>14&1, 1, 0); err != nil {
			return err
		}
		if p.symCodes, err = d.symbolIDCodes(r, len(p.syms)); err != nil {
			return err
		}
	} else {
		mq = new(mqDecoder)
		mq.init(body)
	}
	bm, err := d.decodeText(p, mq, newTextContexts(p.codeLen, nil), r)
	if err != nil {
		return err
	}
	return d.emit(seg, ri, bm, seg.typ == 4)
}

// symbolIDCodes reads the symbol ID code lengths, 7.4.3.1.7.
func (d *jb2) symbolIDCodes(r *bitReader, n int) (*huffTable, error) {
	lines := make([]huffLine, 35)
	for i := range lines {
		v, err := r.readBits(4)
		if err != nil {
			return nil, err
		}
		lines[i] = huffLine{low: i, plen: int(v)}
	}
	runs, err := newHuffTable(lines)
	if err != nil {
		return nil, err
	}
	syms := make([]huffLine, 0, min(n, 1<<12))
	for len(syms) < n {
		if err := d.work.spend(1); err != nil {
			return nil, err
		}
		code, err := plain(nil, nil, r, runs)
		if err != nil {
			return nil, err
		}
		repeat, length := 1, code
		var extra uint32
		switch code {
		case 32:
			if len(syms) == 0 {
				return nil, errors.New("symbol ID code repeats nothing")
			}
			extra, err = r.readBits(2)
			repeat, length = int(extra)+3, syms[len(syms)-1].plen
		case 33:
			extra, err = r.readBits(3)
			repeat, length = int(extra)+3, 0
		case 34:
			extra, err = r.readBits(7)
			repeat, length = int(extra)+11, 0
		}
		if err != nil {
			return nil, err
		}
		if len(syms)+repeat > n {
			return nil, errors.New("symbol ID code runs past the last symbol")
		}
		for range repeat {
			syms = append(syms, huffLine{low: len(syms), plen: length})
		}
	}
	r.align()
	return newHuffTable(syms)
}
