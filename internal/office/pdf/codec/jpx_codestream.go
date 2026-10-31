package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

const (
	jpxMaxComponents = 32
	jpxMaxDepth      = 30 // sample bits; tier 1 keeps magnitudes in 31 bits

	// Limits on structures a small file could otherwise make very large.
	jpxMaxTileCblks   = 1 << 21
	jpxMaxTotalCblks  = 1 << 23
	jpxMaxTilePrecs   = 1 << 20
	jpxMaxTotalPrecs  = 1 << 22
	jpxMaxPOC         = 1 << 12
	jpxMaxTileParts   = 1 << 18
	jpxMaxPacketLoops = 1 << 28
)

// Codestream markers.
const (
	mSOC = 0xFF4F
	mSIZ = 0xFF51
	mCOD = 0xFF52
	mCOC = 0xFF53
	mTLM = 0xFF55
	mPLM = 0xFF57
	mPLT = 0xFF58
	mQCD = 0xFF5C
	mQCC = 0xFF5D
	mRGN = 0xFF5E
	mPOC = 0xFF5F
	mPPM = 0xFF60
	mPPT = 0xFF61
	mCRG = 0xFF63
	mCOM = 0xFF64
	mSOT = 0xFF90
	mSOP = 0xFF91
	mEPH = 0xFF92
	mSOD = 0xFF93
	mEOC = 0xFFD9
)

// Code block style flags.
const (
	styleBypass  = 1
	styleReset   = 2
	styleTermAll = 4
	styleVSC     = 8
	stylePTerm   = 16
	styleSegSym  = 32
	styleHT      = 64
)

// jpxComponent is an image component and its decoded plane.
type jpxComponent struct {
	depth  int
	signed bool
	dx, dy int
	x0, y0 int
	w, h   int
	plane  []uint16 // unsigned samples of at most 16 bits
}

// jpxCoding is the part of COD and COC that belongs to one component.
type jpxCoding struct {
	levels     int
	xcb, ycb   int
	style      int
	reversible bool
	precincts  []byte // PPx | PPy<<4 per resolution, nil for the maximum
	fromCOC    bool
	set        bool
}

// jpxQuant is a QCD or QCC.
type jpxQuant struct {
	style   int
	guard   int
	steps   []uint16 // exponent<<11 | mantissa
	fromQCC bool
	set     bool
}

// jpxProgression is a progression order over a range, the whole image without POC.
type jpxProgression struct {
	rs, re, cs, ce, layers int
	order                  int
}

// jpxParams is the coding state of the main header or of one tile.
type jpxParams struct {
	order, layers, mct int
	sop, eph           bool
	coding             []jpxCoding
	quant              []jpxQuant
	roi                []int
	poc                []jpxProgression
	ownPOC             bool
}

func (p *jpxParams) clone() *jpxParams {
	c := *p
	c.coding = slices.Clone(p.coding)
	c.quant = slices.Clone(p.quant)
	c.roi = slices.Clone(p.roi)
	for i := range c.coding {
		c.coding[i].fromCOC = false
		c.quant[i].fromQCC = false
	}
	c.ownPOC = false
	return &c
}

// jpxTile gathers a tile's parts until every part has been seen.
type jpxTile struct {
	index  int
	params *jpxParams
	parts  [][]byte
}

// jpxDecoder holds the image level state.
type jpxDecoder struct {
	data  []byte
	limit int

	xsiz, ysiz, xosiz, yosiz     int
	xtsiz, ytsiz, xtosiz, ytosiz int
	tilesX, tilesY               int

	comps []jpxComponent
	main  jpxParams
	tiles []*jpxTile // in order of first appearance
	byID  map[int]*jpxTile

	cblks, precs int
	t1Work       int64
	packets      int
}

func marker(b []byte) int {
	return int(binary.BigEndian.Uint16(b))
}

// segment returns the payload of the marker segment at pos and the position after it.
func (d *jpxDecoder) segment(pos int) ([]byte, int, error) {
	if pos+4 > len(d.data) {
		return nil, 0, errors.New("jpx: truncated header")
	}
	n := marker(d.data[pos+2:])
	if n < 2 || pos+2+n > len(d.data) {
		return nil, 0, errors.New("jpx: truncated header")
	}
	return d.data[pos+4 : pos+2+n], pos + 2 + n, nil
}

func (d *jpxDecoder) parseMain() error {
	if len(d.data) < 4 || marker(d.data) != mSOC || marker(d.data[2:]) != mSIZ {
		return errors.New("jpx: the codestream does not start with SOC and SIZ")
	}
	body, pos, err := d.segment(2)
	if err != nil {
		return err
	}
	if err := d.parseSIZ(body); err != nil {
		return err
	}
	for {
		if pos+2 > len(d.data) {
			return errors.New("jpx: no tile in the codestream")
		}
		m := marker(d.data[pos:])
		if m == mSOT {
			break
		}
		if m < 0xFF00 {
			return fmt.Errorf("jpx: expected a marker at offset %d", pos)
		}
		body, next, err := d.segment(pos)
		if err != nil {
			return err
		}
		if err := d.parseParam(m, body, &d.main); err != nil {
			return err
		}
		pos = next
	}
	if d.main.layers == 0 {
		return errors.New("jpx: no COD marker")
	}
	for c := range d.comps {
		if !d.main.coding[c].set || !d.main.quant[c].set {
			return errors.New("jpx: a component has no coding or quantization parameters")
		}
	}
	return d.parseTileParts(pos)
}

func (d *jpxDecoder) parseSIZ(b []byte) error {
	if len(b) < 36 {
		return errors.New("jpx: short SIZ marker")
	}
	rsiz := binary.BigEndian.Uint16(b)
	if rsiz&0x4000 != 0 {
		return errors.New("jpx: High Throughput JPEG 2000 is not supported")
	}
	u := func(i int) int { return int(binary.BigEndian.Uint32(b[2+4*i:])) }
	d.xsiz, d.ysiz, d.xosiz, d.yosiz = u(0), u(1), u(2), u(3)
	d.xtsiz, d.ytsiz, d.xtosiz, d.ytosiz = u(4), u(5), u(6), u(7)
	n := int(binary.BigEndian.Uint16(b[34:]))
	w, h := d.xsiz-d.xosiz, d.ysiz-d.yosiz
	if !fits(w, h, d.limit) {
		return fmt.Errorf("jpx: image of %d by %d is too large or empty", w, h)
	}
	if n == 0 || n > jpxMaxComponents {
		return fmt.Errorf("jpx: %d components are not supported", n)
	}
	if len(b) < 36+3*n {
		return errors.New("jpx: short SIZ marker")
	}
	if d.xtsiz == 0 || d.ytsiz == 0 || d.xtosiz > d.xosiz || d.ytosiz > d.yosiz ||
		d.xtosiz+d.xtsiz <= d.xosiz || d.ytosiz+d.ytsiz <= d.yosiz {
		return errors.New("jpx: bad tile grid")
	}
	d.tilesX = (d.xsiz - d.xtosiz + d.xtsiz - 1) / d.xtsiz
	d.tilesY = (d.ysiz - d.ytosiz + d.ytsiz - 1) / d.ytsiz
	if d.tilesX*d.tilesY > 65535 {
		return fmt.Errorf("jpx: %d by %d tiles is too many", d.tilesX, d.tilesY)
	}
	d.comps = make([]jpxComponent, n)
	samples := 0
	for i := range d.comps {
		c := &d.comps[i]
		e := b[36+3*i:]
		c.signed = e[0]&0x80 != 0
		c.depth = int(e[0]&0x7F) + 1
		c.dx, c.dy = int(e[1]), int(e[2])
		if c.dx == 0 || c.dy == 0 {
			return errors.New("jpx: bad component subsampling")
		}
		if c.depth > jpxMaxDepth {
			return fmt.Errorf("jpx: %d bit samples are not supported", c.depth)
		}
		c.x0 = ceilDiv(d.xosiz, c.dx)
		c.y0 = ceilDiv(d.yosiz, c.dy)
		c.w = ceilDiv(d.xsiz, c.dx) - c.x0
		c.h = ceilDiv(d.ysiz, c.dy) - c.y0
		if c.w <= 0 || c.h <= 0 {
			return errors.New("jpx: a component has no samples")
		}
		samples += c.w * c.h
	}
	if samples > 5*d.limit {
		return errors.New("jpx: too many samples")
	}
	for i := range d.comps {
		d.comps[i].plane = make([]uint16, d.comps[i].w*d.comps[i].h)
	}
	d.t1Work = 64*int64(samples) + 1<<26
	d.main.coding = make([]jpxCoding, n)
	d.main.quant = make([]jpxQuant, n)
	d.main.roi = make([]int, n)
	d.byID = map[int]*jpxTile{}
	return nil
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

func ceilShift(a, n int) int {
	return (a + 1<<n - 1) >> n
}

// parseParam applies a parameter marker to the main header or to a tile.
func (d *jpxDecoder) parseParam(m int, b []byte, p *jpxParams) error {
	switch m {
	case mCOD:
		return d.parseCOD(b, p)
	case mCOC:
		c, rest, err := d.componentIndex(b)
		if err != nil {
			return err
		}
		if len(rest) < 1 {
			return errors.New("jpx: short COC marker")
		}
		cs, err := parseCoding(rest[1:], rest[0]&1 != 0)
		if err != nil {
			return err
		}
		cs.fromCOC = true
		p.coding[c] = cs
	case mQCD:
		q, err := parseQuant(b)
		if err != nil {
			return err
		}
		for i := range p.quant {
			if !p.quant[i].fromQCC {
				p.quant[i] = q
			}
		}
	case mQCC:
		c, rest, err := d.componentIndex(b)
		if err != nil {
			return err
		}
		q, err := parseQuant(rest)
		if err != nil {
			return err
		}
		q.fromQCC = true
		p.quant[c] = q
	case mRGN:
		c, rest, err := d.componentIndex(b)
		if err != nil {
			return err
		}
		if len(rest) < 2 || rest[0] != 0 {
			return errors.New("jpx: unsupported RGN marker")
		}
		p.roi[c] = int(rest[1])
	case mPOC:
		return d.parsePOC(b, p)
	case mPPM, mPPT:
		return errors.New("jpx: packed packet headers are not supported")
	}
	return nil
}

func (d *jpxDecoder) componentIndex(b []byte) (int, []byte, error) {
	size := 1
	if len(d.comps) > 256 {
		size = 2
	}
	if len(b) < size {
		return 0, nil, errors.New("jpx: short marker")
	}
	c := int(b[0])
	if size == 2 {
		c = marker(b)
	}
	if c >= len(d.comps) {
		return 0, nil, errors.New("jpx: marker names a missing component")
	}
	return c, b[size:], nil
}

func (d *jpxDecoder) parseCOD(b []byte, p *jpxParams) error {
	if len(b) < 5 {
		return errors.New("jpx: short COD marker")
	}
	scod := b[0]
	p.sop = scod&2 != 0
	p.eph = scod&4 != 0
	p.order = int(b[1])
	p.layers = marker(b[2:])
	p.mct = int(b[4])
	if p.order > 4 {
		return fmt.Errorf("jpx: unknown progression order %d", p.order)
	}
	if p.layers == 0 {
		return errors.New("jpx: no quality layers")
	}
	if p.mct > 1 {
		return errors.New("jpx: unknown multiple component transform")
	}
	cs, err := parseCoding(b[5:], scod&1 != 0)
	if err != nil {
		return err
	}
	for i := range p.coding {
		if !p.coding[i].fromCOC {
			p.coding[i] = cs
		}
	}
	return nil
}

func parseCoding(b []byte, hasPrecincts bool) (jpxCoding, error) {
	var c jpxCoding
	if len(b) < 5 {
		return c, errors.New("jpx: short coding style")
	}
	c.levels = int(b[0])
	c.xcb = int(b[1]&15) + 2
	c.ycb = int(b[2]&15) + 2
	c.style = int(b[3])
	if c.levels > 32 {
		return c, errors.New("jpx: too many decomposition levels")
	}
	if c.xcb > 10 || c.ycb > 10 || c.xcb+c.ycb > 12 {
		return c, errors.New("jpx: bad code block size")
	}
	if c.style&styleHT != 0 {
		return c, errors.New("jpx: High Throughput code blocks are not supported")
	}
	if c.style > 63 {
		return c, errors.New("jpx: unknown code block style")
	}
	switch b[4] {
	case 0:
	case 1:
		c.reversible = true
	default:
		return c, errors.New("jpx: unknown wavelet transform")
	}
	if hasPrecincts {
		if len(b) < 5+c.levels+1 {
			return c, errors.New("jpx: short precinct list")
		}
		c.precincts = slices.Clone(b[5 : 5+c.levels+1])
	}
	c.set = true
	return c, nil
}

func parseQuant(b []byte) (jpxQuant, error) {
	var q jpxQuant
	if len(b) < 1 {
		return q, errors.New("jpx: short quantization marker")
	}
	q.style = int(b[0] & 31)
	q.guard = int(b[0] >> 5)
	b = b[1:]
	switch q.style {
	case 0:
		for _, v := range b {
			q.steps = append(q.steps, uint16(v>>3)<<11)
		}
	case 1, 2:
		for ; len(b) >= 2; b = b[2:] {
			q.steps = append(q.steps, binary.BigEndian.Uint16(b))
		}
	default:
		return q, errors.New("jpx: unknown quantization style")
	}
	if len(q.steps) == 0 {
		return q, errors.New("jpx: quantization marker has no steps")
	}
	q.set = true
	return q, nil
}

func (d *jpxDecoder) parsePOC(b []byte, p *jpxParams) error {
	size := 1
	if len(d.comps) > 256 {
		size = 2
	}
	entry := 5 + 2*size
	if !p.ownPOC {
		p.poc = nil
		p.ownPOC = true
	}
	for ; len(b) >= entry; b = b[entry:] {
		var e jpxProgression
		e.rs = int(b[0])
		e.layers = marker(b[1+size:])
		e.re = int(b[3+size])
		if size == 1 {
			e.cs, e.ce = int(b[1]), int(b[4+size])
			if e.ce == 0 {
				e.ce = 256
			}
		} else {
			e.cs, e.ce = marker(b[1:]), marker(b[4+size:])
		}
		e.order = int(b[entry-1])
		if e.order > 4 {
			return errors.New("jpx: unknown progression order in POC")
		}
		if len(p.poc) >= jpxMaxPOC {
			return errors.New("jpx: too many progression changes")
		}
		p.poc = append(p.poc, e)
	}
	return nil
}

// endOfTileParts ends the walk at damage: tile parts already read still make a picture.
func (d *jpxDecoder) endOfTileParts(err error) error {
	for _, t := range d.tiles {
		if len(t.parts) > 0 {
			return nil
		}
	}
	return err
}

// parseTileParts walks the tile parts, keeping each tile's parameters and data.
func (d *jpxDecoder) parseTileParts(pos int) error {
	count := 0
	for pos+12 <= len(d.data) {
		if marker(d.data[pos:]) != mSOT {
			if len(d.tiles) > 0 {
				return nil // EOC, or damage after complete tile parts
			}
			return errors.New("jpx: expected a tile part")
		}
		count++
		if count > jpxMaxTileParts {
			return errors.New("jpx: too many tile parts")
		}
		index := marker(d.data[pos+4:])
		size := int(binary.BigEndian.Uint32(d.data[pos+6:]))
		if index >= d.tilesX*d.tilesY {
			return fmt.Errorf("jpx: tile %d does not exist", index)
		}
		end := pos + size
		if size == 0 || end > len(d.data) {
			end = len(d.data)
		}
		if end < pos+14 {
			return d.endOfTileParts(errors.New("jpx: bad tile part length"))
		}
		t := d.byID[index]
		if t == nil {
			t = &jpxTile{index: index, params: d.main.clone()}
			d.byID[index] = t
			d.tiles = append(d.tiles, t)
		}
		p := pos + 12
		for {
			if p+2 > end {
				return d.endOfTileParts(errors.New("jpx: tile part has no SOD marker"))
			}
			m := marker(d.data[p:])
			if m == mSOD {
				p += 2
				break
			}
			if m < 0xFF00 {
				return d.endOfTileParts(fmt.Errorf("jpx: expected a marker at offset %d", p))
			}
			body, next, err := d.segment(p)
			if err != nil {
				return d.endOfTileParts(err)
			}
			if err := d.parseParam(m, body, t.params); err != nil {
				return err
			}
			p = next
		}
		body := d.data[p:end]
		if size == 0 && len(body) >= 2 && marker(body[len(body)-2:]) == mEOC {
			body = body[:len(body)-2]
		}
		t.parts = append(t.parts, body)
		pos = end
	}
	if len(d.tiles) == 0 {
		return errors.New("jpx: no tile in the codestream")
	}
	return nil
}
