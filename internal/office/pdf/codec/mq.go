package codec

// mqEntry is one row of the probability table shared by T.88 and T.800.
type mqEntry struct {
	qe         uint32
	nmps, nlps uint8
	sw         uint8
}

var mqTable = [47]mqEntry{
	{0x5601, 1, 1, 1}, {0x3401, 2, 6, 0}, {0x1801, 3, 9, 0}, {0x0AC1, 4, 12, 0},
	{0x0521, 5, 29, 0}, {0x0221, 38, 33, 0}, {0x5601, 7, 6, 1}, {0x5401, 8, 14, 0},
	{0x4801, 9, 14, 0}, {0x3801, 10, 14, 0}, {0x3001, 11, 17, 0}, {0x2401, 12, 18, 0},
	{0x1C01, 13, 20, 0}, {0x1601, 29, 21, 0}, {0x5601, 15, 14, 1}, {0x5401, 16, 14, 0},
	{0x5101, 17, 15, 0}, {0x4801, 18, 16, 0}, {0x3801, 19, 17, 0}, {0x3401, 20, 18, 0},
	{0x3001, 21, 19, 0}, {0x2801, 22, 19, 0}, {0x2401, 23, 20, 0}, {0x2201, 24, 21, 0},
	{0x1C01, 25, 22, 0}, {0x1801, 26, 23, 0}, {0x1601, 27, 24, 0}, {0x1401, 28, 25, 0},
	{0x1201, 29, 26, 0}, {0x1101, 30, 27, 0}, {0x0AC1, 31, 28, 0}, {0x09C1, 32, 29, 0},
	{0x08A1, 33, 30, 0}, {0x0521, 34, 31, 0}, {0x0441, 35, 32, 0}, {0x02A1, 36, 33, 0},
	{0x0221, 37, 34, 0}, {0x0141, 38, 35, 0}, {0x0111, 39, 36, 0}, {0x0085, 40, 37, 0},
	{0x0049, 41, 38, 0}, {0x0025, 42, 39, 0}, {0x0015, 43, 40, 0}, {0x0009, 44, 41, 0},
	{0x0005, 45, 42, 0}, {0x0001, 45, 43, 0}, {0x5601, 46, 46, 0},
}

// mqStep is mqTable indexed by a context byte, state<<1 | mps, with the next bytes ready made.
type mqStep struct {
	qe       uint32
	mps, lps uint8
}

var mqSteps = func() (t [94]mqStep) {
	for i, e := range mqTable {
		for m := range 2 {
			lps := m
			if e.sw != 0 {
				lps = 1 - m
			}
			t[i<<1|m] = mqStep{e.qe, e.nmps<<1 | uint8(m), e.nlps<<1 | uint8(lps)}
		}
	}
	return t
}()

// mqDecoder is the arithmetic decoder of T.88 annex E and T.800 annex C.
// Past the end of its data it reads the 0xFF bytes both standards prescribe, so it never fails.
type mqDecoder struct {
	data []byte
	pos  int
	a, c uint32
	ct   int
}

func (d *mqDecoder) byteAt(i int) uint32 {
	if i < len(d.data) {
		return uint32(d.data[i])
	}
	return 0xFF
}

// init starts decoding data from its first byte.
func (d *mqDecoder) init(data []byte) {
	d.data = data
	d.pos = 0
	d.c = d.byteAt(0) << 16
	d.ct = 0
	d.byteIn()
	d.c <<= 7
	d.ct -= 7
	d.a = 0x8000
}

func (d *mqDecoder) byteIn() {
	if d.byteAt(d.pos) == 0xFF {
		next := d.byteAt(d.pos + 1)
		if next > 0x8F {
			d.c += 0xFF00
			d.ct = 8
		} else {
			d.pos++
			d.c += next << 9
			d.ct = 7
		}
		return
	}
	d.pos++
	d.c += d.byteAt(d.pos) << 8
	d.ct = 8
}

// decode returns the next decision in the context *cx, a byte of state<<1 | mps.
func (d *mqDecoder) decode(cx *uint8) int {
	st := *cx
	mps := int(st & 1)
	e := &mqSteps[st]
	d.a -= e.qe
	var bit int
	if d.c>>16 < e.qe {
		if d.a < e.qe {
			bit = mps
			*cx = e.mps
		} else {
			bit = 1 - mps
			*cx = e.lps
		}
		d.a = e.qe
	} else {
		d.c -= e.qe << 16
		if d.a&0x8000 != 0 {
			return mps
		}
		if d.a < e.qe {
			bit = 1 - mps
			*cx = e.lps
		} else {
			bit = mps
			*cx = e.mps
		}
	}
	for {
		if d.ct == 0 {
			d.byteIn()
		}
		d.a <<= 1
		d.c <<= 1
		d.ct--
		if d.a&0x8000 != 0 {
			return bit
		}
	}
}
