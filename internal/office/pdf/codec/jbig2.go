package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	jb2MaxSegments = 1 << 16 // segments in one stream
	jb2MaxRefs     = 1 << 16 // referred segments of one segment
	jb2MaxSymbols  = 1 << 21 // symbols visible to one region
)

var errShort = errors.New("jbig2: truncated data")

// bitmap holds one byte per pixel, 0 or 1, which keeps context forming simple and fast.
type bitmap struct {
	w, h int
	pix  []byte
}

func newBitmap(w, h int) *bitmap {
	return &bitmap{w: w, h: h, pix: make([]byte, w*h)}
}

func (b *bitmap) at(x, y int) int {
	if x < 0 || y < 0 || x >= b.w || y >= b.h {
		return 0
	}
	return int(b.pix[y*b.w+x])
}

func (b *bitmap) row(y int) []byte {
	if y < 0 || y >= b.h {
		return nil
	}
	return b.pix[y*b.w : (y+1)*b.w]
}

func (b *bitmap) fill(v byte) {
	if v != 0 {
		for i := range b.pix {
			b.pix[i] = v
		}
	}
}

// Combination operators of T.88.
const (
	opOR = iota
	opAND
	opXOR
	opXNOR
	opREPLACE
)

// combine draws src at x, y and returns how many pixels it touched.
func (b *bitmap) combine(src *bitmap, x, y, op int) int {
	sx, sy, w, h := 0, 0, src.w, src.h
	if x < 0 {
		sx, w, x = -x, w+x, 0
	}
	if y < 0 {
		sy, h, y = -y, h+y, 0
	}
	w = min(w, b.w-x)
	h = min(h, b.h-y)
	if w <= 0 || h <= 0 {
		return 0
	}
	for j := range h {
		d := b.pix[(y+j)*b.w+x:][:w]
		s := src.pix[(sy+j)*src.w+sx:][:w]
		switch op {
		case opOR:
			for i, v := range s {
				d[i] |= v
			}
		case opAND:
			for i, v := range s {
				d[i] &= v
			}
		case opXOR:
			for i, v := range s {
				d[i] ^= v
			}
		case opXNOR:
			for i, v := range s {
				d[i] = d[i] ^ v ^ 1
			}
		default:
			copy(d, s)
		}
	}
	return w * h
}

// JBIG2 decodes an embedded JBIG2 stream, the PDF form with no file header and
// sequential segments, with optional globals. The result is 1 bit per pixel, rows
// padded to bytes, MSB first, 1 = black as in the JBIG2 page bitmap.
func JBIG2(data, globals []byte, maxPixels int) (bits []byte, width, height int, err error) {
	defer func() {
		if r := recover(); r != nil {
			bits, width, height = nil, 0, 0
			err = fmt.Errorf("jbig2: malformed stream: %v", r)
		}
	}()
	d := newJB2(pixelLimit(maxPixels))
	if len(globals) > 0 {
		if err := d.run(globals); err != nil {
			return nil, 0, 0, fmt.Errorf("jbig2 globals: %w", err)
		}
	}
	if err := d.run(data); err != nil {
		return nil, 0, 0, err
	}
	return d.finish()
}

// jb2 is the state of one decode: the page and the results segments refer to.
type jb2 struct {
	limit    int
	work     budget
	held     int // pixels kept for later segments
	segments int

	page       *bitmap
	unknownH   bool
	lastStripe int
	defPixel   byte
	done       bool

	dicts    map[uint32]*symbolDict
	tables   map[uint32]*huffTable
	patterns map[uint32][]*bitmap
	regions  map[uint32]*bitmap
}

func newJB2(limit int) *jb2 {
	return &jb2{
		limit:      limit,
		work:       budget{left: int64(min(8*limit, 1<<29)) + 1<<26},
		lastStripe: -1,
		dicts:      map[uint32]*symbolDict{},
		tables:     map[uint32]*huffTable{},
		patterns:   map[uint32][]*bitmap{},
		regions:    map[uint32]*bitmap{},
	}
}

// segHeader is a parsed segment header.
type segHeader struct {
	num     uint32
	typ     int
	refs    []uint32
	length  uint32
	unknown bool
}

const jb2UnknownLength = 0xFFFFFFFF

var jb2Magic = []byte{0x97, 'J', 'B', '2', 0x0D, 0x0A, 0x1A, 0x0A}

// stripFileHeader accepts the stand-alone form too, as long as it is sequential.
func stripFileHeader(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, jb2Magic) {
		return data, nil
	}
	if len(data) < 9 {
		return nil, errShort
	}
	flags := data[8]
	if flags&1 == 0 {
		return nil, errors.New("jbig2: random-access organisation is not supported")
	}
	if flags&2 != 0 {
		return data[9:], nil
	}
	if len(data) < 13 {
		return nil, errShort
	}
	return data[13:], nil
}

func parseSegmentHeader(data []byte) (segHeader, int, error) {
	var s segHeader
	if len(data) < 6 {
		return s, 0, errShort
	}
	s.num = binary.BigEndian.Uint32(data)
	flags := data[4]
	s.typ = int(flags & 63)
	pos := 5
	count := int(data[pos] >> 5)
	switch {
	case count == 7:
		if len(data) < pos+4 {
			return s, 0, errShort
		}
		n := binary.BigEndian.Uint32(data[pos:]) & 0x1FFFFFFF
		if n > jb2MaxRefs {
			return s, 0, fmt.Errorf("jbig2: segment %d refers to %d segments", s.num, n)
		}
		count = int(n)
		pos += 4 + (count+8)/8
	case count > 4:
		return s, 0, fmt.Errorf("jbig2: segment %d has a bad reference count", s.num)
	default:
		pos++
	}
	size := 4
	if s.num <= 256 {
		size = 1
	} else if s.num <= 65536 {
		size = 2
	}
	if len(data) < pos+count*size {
		return s, 0, errShort
	}
	s.refs = make([]uint32, count)
	for i := range s.refs {
		switch size {
		case 1:
			s.refs[i] = uint32(data[pos])
		case 2:
			s.refs[i] = uint32(binary.BigEndian.Uint16(data[pos:]))
		default:
			s.refs[i] = binary.BigEndian.Uint32(data[pos:])
		}
		pos += size
	}
	if flags&0x40 != 0 {
		pos += 4
	} else {
		pos++
	}
	if len(data) < pos+4 {
		return s, 0, errShort
	}
	s.length = binary.BigEndian.Uint32(data[pos:])
	s.unknown = s.length == jb2UnknownLength
	return s, pos + 4, nil
}

// scanUnknownLength finds the end of a generic region of unknown length, by T.88 7.2.7.
func scanUnknownLength(data []byte) (int, error) {
	if len(data) < 18 {
		return 0, errShort
	}
	marker := []byte{0xFF, 0xAC}
	start := 18
	if data[17]&1 != 0 {
		marker = []byte{0, 0}
	} else if data[17]>>1&3 == 0 {
		start += 8
	} else {
		start += 2
	}
	if start > len(data) {
		return 0, errShort
	}
	i := bytes.Index(data[start:], marker)
	if i < 0 || start+i+6 > len(data) {
		return 0, errShort
	}
	return start + i + 6, nil
}

// run decodes one stream of segments into the page.
func (d *jb2) run(data []byte) error {
	data, err := stripFileHeader(data)
	if err != nil {
		return err
	}
	d.done = false
	for pos := 0; pos < len(data) && !d.done; {
		seg, n, err := parseSegmentHeader(data[pos:])
		if err != nil {
			// A page already decoded may be followed by padding.
			if errors.Is(err, errShort) && d.page != nil {
				return nil
			}
			return err
		}
		d.segments++
		if d.segments > jb2MaxSegments {
			return errors.New("jbig2: too many segments")
		}
		if err := d.work.spend(64); err != nil {
			return fmt.Errorf("jbig2: %w", err)
		}
		pos += n
		size := int64(seg.length)
		if seg.unknown {
			if seg.typ != 36 && seg.typ != 38 && seg.typ != 39 {
				return fmt.Errorf("jbig2: segment %d of type %d has no length", seg.num, seg.typ)
			}
			l, err := scanUnknownLength(data[pos:])
			if err != nil {
				return err
			}
			size = int64(l)
		}
		if size > int64(len(data)-pos) {
			return errShort
		}
		if err := d.segment(seg, data[pos:pos+int(size)]); err != nil {
			return err
		}
		pos += int(size)
	}
	return nil
}

// segment dispatches one segment.
func (d *jb2) segment(seg segHeader, data []byte) error {
	var err error
	switch seg.typ {
	case 0:
		err = d.symbolDictSegment(seg, data)
	case 4, 6, 7:
		err = d.textSegment(seg, data)
	case 16:
		err = d.patternSegment(seg, data)
	case 20, 22, 23:
		err = d.halftoneSegment(seg, data)
	case 36, 38, 39:
		err = d.genericSegment(seg, data)
	case 40, 42, 43:
		err = d.refinementSegment(seg, data)
	case 48:
		if d.page != nil {
			d.done = true // the stream's next page; a PDF image is one page
			return nil
		}
		err = d.pageSegment(data)
	case 49, 51:
		d.done = d.page != nil
	case 50:
		err = d.endOfStripe(data)
	case 52:
		// Profiles only describe the stream.
	case 53:
		err = d.tableSegment(seg, data)
	case 62:
		if len(data) >= 4 && binary.BigEndian.Uint32(data)&0x40000000 != 0 {
			err = errors.New("necessary extension segment is not supported")
		}
	default:
		err = fmt.Errorf("unknown type %d", seg.typ)
	}
	if err != nil {
		if errors.Is(err, errShort) {
			return err
		}
		return fmt.Errorf("jbig2: segment %d: %w", seg.num, err)
	}
	return nil
}

func (d *jb2) pageSegment(data []byte) error {
	if len(data) < 19 {
		return errShort
	}
	w := binary.BigEndian.Uint32(data)
	h := binary.BigEndian.Uint32(data[4:])
	flags := data[16]
	d.defPixel = flags >> 2 & 1
	if w == 0 || int64(w) > int64(d.limit) {
		return fmt.Errorf("page of %d by %d is too large", w, h)
	}
	if h == jb2UnknownLength {
		d.unknownH = true
		d.page = newBitmap(int(w), 0)
		return nil
	}
	if !fits(int(w), int(h), d.limit) {
		return fmt.Errorf("page of %d by %d is too large or empty", w, h)
	}
	d.page = newBitmap(int(w), int(h))
	d.page.fill(d.defPixel)
	return nil
}

// grow extends a page of unknown height to h rows.
func (d *jb2) grow(h int) error {
	if !d.unknownH || h <= d.page.h {
		return nil
	}
	if !fits(d.page.w, h, d.limit) {
		return fmt.Errorf("page of %d by %d is too large", d.page.w, h)
	}
	old := len(d.page.pix)
	size := d.page.w * h
	if size <= cap(d.page.pix) {
		d.page.pix = d.page.pix[:size]
	} else {
		pix := make([]byte, size, min(size*2, d.limit))
		copy(pix, d.page.pix)
		d.page.pix = pix
	}
	tail := d.page.pix[old:]
	for i := range tail {
		tail[i] = d.defPixel
	}
	d.page.h = h
	return nil
}

func (d *jb2) endOfStripe(data []byte) error {
	if len(data) < 4 {
		return errShort
	}
	if d.page == nil {
		return errors.New("end of stripe before page information")
	}
	y := binary.BigEndian.Uint32(data)
	if int64(y) >= int64(d.limit) {
		return fmt.Errorf("stripe end %d is beyond the limit", y)
	}
	d.lastStripe = int(y)
	return d.grow(int(y) + 1)
}

// finish packs the page into the output form.
func (d *jb2) finish() ([]byte, int, int, error) {
	if d.page == nil {
		return nil, 0, 0, errors.New("jbig2: no page in the stream")
	}
	p := d.page
	h := p.h
	if d.unknownH && d.lastStripe >= 0 {
		h = min(h, d.lastStripe+1)
	}
	if !fits(p.w, h, d.limit) {
		return nil, 0, 0, errors.New("jbig2: the page has no rows")
	}
	stride := (p.w + 7) / 8
	out := make([]byte, stride*h)
	for y := range h {
		row := p.row(y)
		o := out[y*stride:][:stride]
		for x, v := range row {
			o[x>>3] |= v << (7 - x&7)
		}
	}
	return out, p.w, h, nil
}

// regionInfo is the field every region segment starts with.
type regionInfo struct {
	w, h, x, y int
	op         int
}

const regionInfoSize = 17

func (d *jb2) parseRegion(data []byte) (regionInfo, error) {
	var r regionInfo
	if len(data) < regionInfoSize {
		return r, errShort
	}
	w := binary.BigEndian.Uint32(data)
	h := binary.BigEndian.Uint32(data[4:])
	x := binary.BigEndian.Uint32(data[8:])
	y := binary.BigEndian.Uint32(data[12:])
	flags := data[16]
	if flags&8 != 0 {
		return r, errors.New("colour extension is not supported")
	}
	if flags&7 > opREPLACE {
		return r, errors.New("bad combination operator")
	}
	if int64(w) > int64(d.limit) || int64(h) > int64(d.limit) || (w > 0 && h > 0 && !fits(int(w), int(h), d.limit)) {
		return r, fmt.Errorf("region of %d by %d is too large", w, h)
	}
	if int64(x) > 1<<31 || int64(y) > 1<<31 {
		return r, errors.New("region offset is out of range")
	}
	return regionInfo{int(w), int(h), int(x), int(y), int(flags & 7)}, nil
}

// hold accounts for pixels kept for later segments: symbols, patterns, intermediate regions.
func (d *jb2) hold(pixels int) error {
	d.held += pixels
	if d.held > 2*d.limit {
		return errors.New("segments hold too much memory")
	}
	return nil
}

// emit puts a finished region on the page, or keeps it when it is intermediate.
func (d *jb2) emit(seg segHeader, r regionInfo, bm *bitmap, intermediate bool) error {
	if intermediate {
		if err := d.hold(bm.w * bm.h); err != nil {
			return err
		}
		d.regions[seg.num] = bm
		return nil
	}
	if d.page == nil {
		return errors.New("region before page information")
	}
	if err := d.grow(r.y + r.h); err != nil {
		return err
	}
	return d.work.spend(d.page.combine(bm, r.x, r.y, r.op))
}

// atPixel is an adaptive template pixel.
type atPixel struct {
	x, y int
}

func parseAT(data []byte, n int) ([]atPixel, []byte, error) {
	if len(data) < 2*n {
		return nil, nil, errShort
	}
	at := make([]atPixel, n)
	for i := range at {
		at[i] = atPixel{int(int8(data[2*i])), int(int8(data[2*i+1]))}
	}
	return at, data[2*n:], nil
}

func (d *jb2) genericSegment(seg segHeader, data []byte) error {
	r, err := d.parseRegion(data)
	if err != nil {
		return err
	}
	if len(data) < regionInfoSize+1 {
		return errShort
	}
	flags := data[regionInfoSize]
	body := data[regionInfoSize+1:]
	mmr := flags&1 != 0
	template := int(flags >> 1 & 3)
	tpgd := flags&8 != 0
	if flags&16 != 0 {
		return errors.New("extended templates are not supported")
	}
	var at []atPixel
	if !mmr {
		n := 1
		if template == 0 {
			n = 4
		}
		if at, body, err = parseAT(body, n); err != nil {
			return err
		}
	}
	if seg.unknown {
		if len(body) < 4 {
			return errShort
		}
		rows := binary.BigEndian.Uint32(body[len(body)-4:])
		body = body[:len(body)-4]
		if int64(rows) < int64(r.h) {
			r.h = int(rows)
		}
	}
	var bm *bitmap
	if mmr {
		bm, _, err = decodeMMR(body, r.w, r.h, &d.work)
	} else {
		var mq mqDecoder
		mq.init(body)
		bm, err = decodeGeneric(&mq, make([]uint8, genericContexts(template)), r.w, r.h, template, at, tpgd, nil, &d.work)
	}
	if err != nil {
		return err
	}
	return d.emit(seg, r, bm, seg.typ == 36)
}

func (d *jb2) refinementSegment(seg segHeader, data []byte) error {
	r, err := d.parseRegion(data)
	if err != nil {
		return err
	}
	if len(data) < regionInfoSize+1 {
		return errShort
	}
	flags := data[regionInfoSize]
	body := data[regionInfoSize+1:]
	template := int(flags & 1)
	tpgr := flags&2 != 0
	at := []atPixel{{-1, -1}, {-1, -1}}
	if template == 0 {
		if at, body, err = parseAT(body, 2); err != nil {
			return err
		}
	}
	var ref *bitmap
	for _, n := range seg.refs {
		if bm, ok := d.regions[n]; ok {
			ref = bm
			delete(d.regions, n)
			break
		}
	}
	if ref == nil {
		// With no intermediate region the page itself is refined.
		if d.page == nil {
			return errors.New("refinement before page information")
		}
		ref = newBitmap(r.w, r.h)
		ref.combine(d.page, -r.x, -r.y, opREPLACE)
		if err := d.work.spend(r.w * r.h); err != nil {
			return err
		}
	}
	var mq mqDecoder
	mq.init(body)
	bm, err := decodeRefinement(&mq, make([]uint8, 1<<13), r.w, r.h, template, ref, 0, 0, tpgr, at, &d.work)
	if err != nil {
		return err
	}
	return d.emit(seg, r, bm, seg.typ == 40)
}
