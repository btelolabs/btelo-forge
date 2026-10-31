package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// patternSegment decodes a pattern dictionary, 7.4.4 and 6.7.
func (d *jb2) patternSegment(seg segHeader, data []byte) error {
	if len(data) < 7 {
		return errShort
	}
	flags := data[0]
	mmr := flags&1 != 0
	template := int(flags >> 1 & 3)
	pw, ph := int(data[1]), int(data[2])
	grayMax := binary.BigEndian.Uint32(data[3:])
	body := data[7:]
	if pw == 0 || ph == 0 {
		return errors.New("empty pattern")
	}
	if int64(grayMax) >= int64(d.limit) || !fits(int(grayMax+1), pw*ph, d.limit) {
		return errors.New("pattern dictionary is too large")
	}
	n := int(grayMax) + 1
	if err := d.hold(n * pw * ph); err != nil {
		return err
	}
	var sheet *bitmap
	var err error
	if mmr {
		sheet, _, err = decodeMMR(body, n*pw, ph, &d.work)
	} else {
		at := append([]atPixel{{-pw, 0}}, genericShapes[0].defaultAT[1:]...)
		if template != 0 {
			at = at[:1]
		}
		var mq mqDecoder
		mq.init(body)
		sheet, err = decodeGeneric(&mq, make([]uint8, genericContexts(template)), n*pw, ph, template, at, false, nil, &d.work)
	}
	if err != nil {
		return err
	}
	out := make([]*bitmap, n)
	for i := range out {
		bm := newBitmap(pw, ph)
		bm.combine(sheet, -i*pw, 0, opREPLACE)
		out[i] = bm
	}
	d.patterns[seg.num] = out
	return nil
}

// halftoneSegment decodes a halftone region, 7.4.5 and 6.6.
func (d *jb2) halftoneSegment(seg segHeader, data []byte) error {
	ri, err := d.parseRegion(data)
	if err != nil {
		return err
	}
	if len(data) < regionInfoSize+21 {
		return errShort
	}
	head := data[regionInfoSize:]
	flags := head[0]
	mmr := flags&1 != 0
	template := int(flags >> 1 & 3)
	enableSkip := flags&8 != 0
	op := int(flags >> 4 & 7)
	if op > opREPLACE {
		return errors.New("bad halftone combination operator")
	}
	gw := binary.BigEndian.Uint32(head[1:])
	gh := binary.BigEndian.Uint32(head[5:])
	gx := int64(int32(binary.BigEndian.Uint32(head[9:])))
	gy := int64(int32(binary.BigEndian.Uint32(head[13:])))
	rx := int64(binary.BigEndian.Uint16(head[17:]))
	ry := int64(binary.BigEndian.Uint16(head[19:]))
	body := head[21:]

	var pats []*bitmap
	for _, n := range seg.refs {
		if p, ok := d.patterns[n]; ok {
			pats = p
			break
		}
	}
	if len(pats) == 0 {
		return errors.New("halftone region has no pattern dictionary")
	}
	out := newBitmap(ri.w, ri.h)
	out.fill(flags >> 7 & 1)
	if gw == 0 || gh == 0 {
		return d.emit(seg, ri, out, seg.typ == 20)
	}
	grid := max(4*ri.w*ri.h, 1<<16)
	if int64(gw) > int64(d.limit) || int64(gh) > int64(d.limit) || !fits(int(gw), int(gh), min(grid, d.limit)) {
		return fmt.Errorf("halftone grid of %d by %d is too large", gw, gh)
	}
	w, h := int(gw), int(gh)
	bpp := bits.Len(uint(len(pats) - 1))
	pw, ph := pats[0].w, pats[0].h

	var skip *bitmap
	if enableSkip {
		skip = newBitmap(w, h)
		for m := range h {
			for n := range w {
				x := (gx + int64(m)*ry + int64(n)*rx) >> 8
				y := (gy + int64(m)*rx - int64(n)*ry) >> 8
				if x+int64(pw) <= 0 || x >= int64(ri.w) || y+int64(ph) <= 0 || y >= int64(ri.h) {
					skip.pix[m*w+n] = 1
				}
			}
		}
	}

	// Gray-scale image, annex C: bit planes from the top, Gray coded.
	values := make([]uint32, w*h)
	var mq *mqDecoder
	var cx []uint8
	var at []atPixel
	if !mmr {
		mq = new(mqDecoder)
		mq.init(body)
		cx = make([]uint8, genericContexts(template))
		a1 := 3
		if template > 1 {
			a1 = 2
		}
		at = append([]atPixel{{a1, -1}}, genericShapes[0].defaultAT[1:]...)
		if template != 0 {
			at = at[:1]
		}
	}
	var prev *bitmap
	for range bpp {
		var plane *bitmap
		if mmr {
			var used int
			if plane, used, err = decodeMMR(body, w, h, &d.work); err != nil {
				return err
			}
			body = body[used:]
		} else if plane, err = decodeGeneric(mq, cx, w, h, template, at, false, skip, &d.work); err != nil {
			return err
		}
		for i := range plane.pix {
			if prev != nil {
				plane.pix[i] ^= prev.pix[i]
			}
			values[i] = values[i]<<1 | uint32(plane.pix[i])
		}
		prev = plane
	}

	for m := range h {
		if err := d.work.spend(w); err != nil {
			return err
		}
		for n := range w {
			v := values[m*w+n]
			if int(v) >= len(pats) {
				v = uint32(len(pats) - 1)
			}
			x := (gx + int64(m)*ry + int64(n)*rx) >> 8
			y := (gy + int64(m)*rx - int64(n)*ry) >> 8
			if x+int64(pw) <= 0 || x >= int64(ri.w) || y+int64(ph) <= 0 || y >= int64(ri.h) {
				continue
			}
			if err := d.work.spend(out.combine(pats[v], int(x), int(y), op)); err != nil {
				return err
			}
		}
	}
	return d.emit(seg, ri, out, seg.typ == 20)
}
