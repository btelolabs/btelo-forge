package codec

import "errors"

// genericContexts is the size of the context memory of a generic template.
func genericContexts(template int) int {
	switch template {
	case 0:
		return 1 << 16
	case 1:
		return 1 << 13
	}
	return 1 << 10
}

// genericShape describes one template as runs of consecutive pixels on the three rows.
// Bits are laid out as in T.88 figures 3 to 6, which is what fixes the TPGD contexts.
type genericShape struct {
	cur           int   // pixels left of x on this row
	up1, up1Ahead int   // pixels on row y-1, and how many of them lie right of x
	up1Shift      uint  // bit of the row y-1 run
	up2, up2Ahead int   // the same for row y-2
	up2Shift      uint  // bit of the row y-2 run
	atShift       []int // bit of each adaptive pixel
	tpgd          int   // context of the typical prediction flag
	defaultAT     []atPixel
}

var genericShapes = [4]genericShape{
	{cur: 4, up1: 5, up1Ahead: 2, up1Shift: 5, up2: 3, up2Ahead: 1, up2Shift: 12,
		atShift: []int{4, 10, 11, 15}, tpgd: 0x9B25,
		defaultAT: []atPixel{{3, -1}, {-3, -1}, {2, -2}, {-2, -2}}},
	{cur: 3, up1: 5, up1Ahead: 2, up1Shift: 4, up2: 4, up2Ahead: 2, up2Shift: 9,
		atShift: []int{3}, tpgd: 0x0795, defaultAT: []atPixel{{3, -1}}},
	{cur: 2, up1: 4, up1Ahead: 1, up1Shift: 3, up2: 3, up2Ahead: 1, up2Shift: 7,
		atShift: []int{2}, tpgd: 0x00E5, defaultAT: []atPixel{{2, -1}}},
	{cur: 4, up1: 5, up1Ahead: 1, up1Shift: 5,
		atShift: []int{4}, tpgd: 0x0195, defaultAT: []atPixel{{2, -1}}},
}

// decodeGeneric is the generic region decoding procedure of T.88 6.2 for arithmetic data.
// cx is the template's context memory, skip an optional mask of pixels left at 0.
func decodeGeneric(mq *mqDecoder, cx []uint8, w, h, template int, at []atPixel, tpgd bool, skip *bitmap, work *budget) (*bitmap, error) {
	s := &genericShapes[template]
	if len(at) != len(s.atShift) {
		return nil, errors.New("wrong number of adaptive pixels")
	}
	if len(cx) < genericContexts(template) {
		return nil, errors.New("context memory is too small")
	}
	bm := newBitmap(w, h)
	if w == 0 || h == 0 {
		return bm, nil
	}
	curMask := uint32(1)<<s.cur - 1
	up1Mask := uint32(1)<<s.up1 - 1
	up2Mask := uint32(1)<<s.up2 - 1
	atRows := make([][]byte, len(at))
	ltp := 0
	for y := range h {
		if err := work.spend(w); err != nil {
			return nil, err
		}
		cur := bm.row(y)
		if tpgd {
			ltp ^= mq.decode(&cx[s.tpgd])
			if ltp != 0 {
				if y > 0 {
					copy(cur, bm.row(y-1))
				}
				continue
			}
		}
		up1 := bm.row(y - 1)
		up2 := bm.row(y - 2)
		for i, p := range at {
			atRows[i] = bm.row(y + p.y)
		}
		var r0, r1, r2 uint32
		for i := 0; i <= s.up1Ahead; i++ {
			r1 = r1<<1 | pixelAt(up1, i, w)
		}
		for i := 0; i <= s.up2Ahead; i++ {
			r2 = r2<<1 | pixelAt(up2, i, w)
		}
		r2 &= up2Mask
		var rowSkip []byte
		if skip != nil {
			rowSkip = skip.row(y)
		}
		for x := range w {
			var bit int
			if rowSkip == nil || rowSkip[x] == 0 {
				c := r0 | r1<<s.up1Shift | r2<<s.up2Shift
				for i, p := range at {
					ax := x + p.x
					if ax >= 0 && ax < w && atRows[i] != nil {
						c |= uint32(atRows[i][ax]) << s.atShift[i]
					}
				}
				bit = mq.decode(&cx[c])
				cur[x] = byte(bit)
			}
			r0 = (r0<<1 | uint32(bit)) & curMask
			r1 = (r1<<1 | pixelAt(up1, x+s.up1Ahead+1, w)) & up1Mask
			r2 = (r2<<1 | pixelAt(up2, x+s.up2Ahead+1, w)) & up2Mask
		}
	}
	return bm, nil
}

func pixelAt(row []byte, x, w int) uint32 {
	if row == nil || x >= w {
		return 0
	}
	return uint32(row[x])
}

// decodeRefinement is the refinement region decoding procedure of T.88 6.3.
func decodeRefinement(mq *mqDecoder, cx []uint8, w, h, template int, ref *bitmap, dx, dy int, tpgr bool, at []atPixel, work *budget) (*bitmap, error) {
	if len(cx) < 1<<13 {
		return nil, errors.New("context memory is too small")
	}
	bm := newBitmap(w, h)
	sltp := 0x100 // only the central reference pixel set
	if template == 1 {
		sltp = 0x80
	}
	ltp := 0
	for y := range h {
		if err := work.spend(w * 2); err != nil {
			return nil, err
		}
		if tpgr {
			ltp ^= mq.decode(&cx[sltp])
		}
		ry := y - dy
		for x := range w {
			rx := x - dx
			if ltp != 0 {
				// Typical prediction: a uniform 3 by 3 reference fixes the pixel.
				v := ref.at(rx, ry)
				same := true
				for j := -1; j <= 1 && same; j++ {
					for i := -1; i <= 1; i++ {
						if ref.at(rx+i, ry+j) != v {
							same = false
							break
						}
					}
				}
				if same {
					bm.pix[y*w+x] = byte(v)
					continue
				}
			}
			var c int
			if template == 0 {
				c = bm.at(x-1, y) |
					bm.at(x+1, y-1)<<1 |
					bm.at(x, y-1)<<2 |
					bm.at(x+at[0].x, y+at[0].y)<<3 |
					ref.at(rx+1, ry+1)<<4 |
					ref.at(rx, ry+1)<<5 |
					ref.at(rx-1, ry+1)<<6 |
					ref.at(rx+1, ry)<<7 |
					ref.at(rx, ry)<<8 |
					ref.at(rx-1, ry)<<9 |
					ref.at(rx+1, ry-1)<<10 |
					ref.at(rx, ry-1)<<11 |
					ref.at(rx+at[1].x, ry+at[1].y)<<12
			} else {
				c = bm.at(x-1, y) |
					bm.at(x+1, y-1)<<1 |
					bm.at(x, y-1)<<2 |
					bm.at(x-1, y-1)<<3 |
					ref.at(rx+1, ry+1)<<4 |
					ref.at(rx, ry+1)<<5 |
					ref.at(rx+1, ry)<<6 |
					ref.at(rx, ry)<<7 |
					ref.at(rx-1, ry)<<8 |
					ref.at(rx, ry-1)<<9
			}
			bm.pix[y*w+x] = byte(mq.decode(&cx[c]))
		}
	}
	return bm, nil
}
