package codec

import (
	"encoding/binary"
	"errors"
	"sync"
)

// bitReader reads MSB first and fails at the end of its data.
type bitReader struct {
	data []byte
	pos  int  // next byte
	bit  uint // bits used of data[pos]
}

func (r *bitReader) readBit() (int, error) {
	if r.pos >= len(r.data) {
		return 0, errShort
	}
	v := int(r.data[r.pos]>>(7-r.bit)) & 1
	r.bit++
	if r.bit == 8 {
		r.bit = 0
		r.pos++
	}
	return v, nil
}

func (r *bitReader) readBits(n int) (uint32, error) {
	var v uint32
	for range n {
		b, err := r.readBit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | uint32(b)
	}
	return v, nil
}

// align moves to the next byte boundary.
func (r *bitReader) align() {
	if r.bit != 0 {
		r.bit = 0
		r.pos++
	}
}

// rest returns the data from the current, aligned position.
func (r *bitReader) rest() []byte {
	if r.pos >= len(r.data) {
		return nil
	}
	return r.data[r.pos:]
}

func (r *bitReader) skip(n int) error {
	if n < 0 || n > len(r.data)-r.pos {
		return errShort
	}
	r.pos += n
	return nil
}

// Kinds of table line.
const (
	huffNormal = iota
	huffLower
	huffUpper
	huffOOB
)

// huffLine is a line of a code table, annex B.
type huffLine struct {
	low        int
	plen, rlen int
	kind       int
}

const huffMaxLen = 32

// huffTable is a canonical prefix code.
type huffTable struct {
	first  [huffMaxLen + 1]uint64
	count  [huffMaxLen + 1]uint32
	offset [huffMaxLen + 1]uint32
	lines  []huffLine
	maxLen int
}

// newHuffTable assigns the codes by the procedure of B.3.
func newHuffTable(lines []huffLine) (*huffTable, error) {
	t := &huffTable{}
	used := 0
	for _, l := range lines {
		if l.plen > huffMaxLen {
			return nil, errors.New("Huffman prefix is too long")
		}
		if l.plen > 0 {
			t.count[l.plen]++
			used++
			t.maxLen = max(t.maxLen, l.plen)
		}
	}
	// Refuse an over-subscribed code. An incomplete one is harmless.
	var kraft uint64
	for l := 1; l <= t.maxLen; l++ {
		kraft += uint64(t.count[l]) << (huffMaxLen - l)
	}
	if kraft > 1<<huffMaxLen {
		return nil, errors.New("Huffman table is over-subscribed")
	}
	var next uint32
	var code uint64
	for l := 1; l <= t.maxLen; l++ {
		code = (code + uint64(t.count[l-1])) * 2
		t.first[l] = code
		t.offset[l] = next
		next += t.count[l]
	}
	t.lines = make([]huffLine, used)
	fill := t.offset
	for _, l := range lines {
		if l.plen > 0 {
			t.lines[fill[l.plen]] = l
			fill[l.plen]++
		}
	}
	return t, nil
}

// decode reads one value. The second result reports the out-of-band code.
func (t *huffTable) decode(r *bitReader) (int, bool, error) {
	var code uint64
	for l := 1; l <= t.maxLen; l++ {
		b, err := r.readBit()
		if err != nil {
			return 0, false, err
		}
		code = code<<1 | uint64(b)
		if t.count[l] == 0 || code < t.first[l] || code-t.first[l] >= uint64(t.count[l]) {
			continue
		}
		line := &t.lines[t.offset[l]+uint32(code-t.first[l])]
		switch line.kind {
		case huffOOB:
			return 0, true, nil
		case huffLower:
			v, err := r.readBits(32)
			return line.low - int(v), false, err
		case huffUpper:
			v, err := r.readBits(32)
			return line.low + int(v), false, err
		}
		v, err := r.readBits(line.rlen)
		return line.low + int(v), false, err
	}
	return 0, false, errors.New("bad Huffman code")
}

// The standard tables of annex B, lines in the order that gives the printed codes.
var huffStandard = [15][]huffLine{
	{{0, 1, 4, 0}, {16, 2, 8, 0}, {272, 3, 16, 0}, {65808, 3, 32, huffUpper}},
	{{0, 1, 0, 0}, {1, 2, 0, 0}, {2, 3, 0, 0}, {3, 4, 3, 0}, {11, 5, 6, 0}, {75, 6, 32, huffUpper}, {0, 6, 0, huffOOB}},
	{{-256, 8, 8, 0}, {0, 1, 0, 0}, {1, 2, 0, 0}, {2, 3, 0, 0}, {3, 4, 3, 0}, {11, 5, 6, 0},
		{-257, 8, 32, huffLower}, {75, 7, 32, huffUpper}, {0, 6, 0, huffOOB}},
	{{1, 1, 0, 0}, {2, 2, 0, 0}, {3, 3, 0, 0}, {4, 4, 3, 0}, {12, 5, 6, 0}, {76, 5, 32, huffUpper}},
	{{-255, 7, 8, 0}, {1, 1, 0, 0}, {2, 2, 0, 0}, {3, 3, 0, 0}, {4, 4, 3, 0}, {12, 5, 6, 0},
		{-256, 7, 32, huffLower}, {76, 6, 32, huffUpper}},
	{{-2048, 5, 10, 0}, {-1024, 4, 9, 0}, {-512, 4, 8, 0}, {-256, 4, 7, 0}, {-128, 5, 6, 0}, {-64, 5, 5, 0},
		{-32, 4, 5, 0}, {0, 2, 7, 0}, {128, 3, 7, 0}, {256, 3, 8, 0}, {512, 4, 9, 0}, {1024, 4, 10, 0},
		{-2049, 6, 32, huffLower}, {2048, 6, 32, huffUpper}},
	{{-1024, 4, 9, 0}, {-512, 3, 8, 0}, {-256, 4, 7, 0}, {-128, 5, 6, 0}, {-64, 5, 5, 0}, {-32, 4, 5, 0},
		{0, 4, 5, 0}, {32, 5, 5, 0}, {64, 5, 6, 0}, {128, 4, 7, 0}, {256, 3, 8, 0}, {512, 3, 9, 0}, {1024, 3, 10, 0},
		{-1025, 5, 32, huffLower}, {2048, 5, 32, huffUpper}},
	{{-15, 8, 3, 0}, {-7, 9, 1, 0}, {-5, 8, 1, 0}, {-3, 9, 0, 0}, {-2, 7, 0, 0}, {-1, 4, 0, 0}, {0, 2, 1, 0},
		{2, 5, 0, 0}, {3, 6, 0, 0}, {4, 3, 4, 0}, {20, 6, 1, 0}, {22, 4, 4, 0}, {38, 4, 5, 0}, {70, 5, 6, 0},
		{134, 5, 7, 0}, {262, 6, 7, 0}, {390, 7, 8, 0}, {646, 6, 10, 0},
		{-16, 9, 32, huffLower}, {1670, 9, 32, huffUpper}, {0, 2, 0, huffOOB}},
	{{-31, 8, 4, 0}, {-15, 9, 2, 0}, {-11, 8, 2, 0}, {-7, 9, 1, 0}, {-5, 7, 1, 0}, {-3, 4, 1, 0}, {-1, 3, 1, 0},
		{1, 3, 1, 0}, {3, 5, 1, 0}, {5, 6, 1, 0}, {7, 3, 5, 0}, {39, 6, 2, 0}, {43, 4, 5, 0}, {75, 4, 6, 0},
		{139, 5, 7, 0}, {267, 5, 8, 0}, {523, 6, 8, 0}, {779, 7, 9, 0}, {1291, 6, 11, 0},
		{-32, 9, 32, huffLower}, {3339, 9, 32, huffUpper}, {0, 2, 0, huffOOB}},
	{{-21, 7, 4, 0}, {-5, 8, 0, 0}, {-4, 7, 0, 0}, {-3, 5, 0, 0}, {-2, 2, 2, 0}, {2, 5, 0, 0}, {3, 6, 0, 0},
		{4, 7, 0, 0}, {5, 8, 0, 0}, {6, 2, 6, 0}, {70, 5, 5, 0}, {102, 6, 5, 0}, {134, 6, 6, 0}, {198, 6, 7, 0},
		{326, 6, 8, 0}, {582, 6, 9, 0}, {1094, 6, 10, 0}, {2118, 7, 11, 0},
		{-22, 8, 32, huffLower}, {4166, 8, 32, huffUpper}, {0, 2, 0, huffOOB}},
	{{1, 1, 0, 0}, {2, 2, 1, 0}, {4, 4, 0, 0}, {5, 4, 1, 0}, {7, 5, 1, 0}, {9, 5, 2, 0}, {13, 6, 2, 0},
		{17, 7, 2, 0}, {21, 7, 3, 0}, {29, 7, 4, 0}, {45, 7, 5, 0}, {77, 7, 6, 0}, {141, 7, 32, huffUpper}},
	{{1, 1, 0, 0}, {2, 2, 0, 0}, {3, 3, 1, 0}, {5, 5, 0, 0}, {6, 5, 1, 0}, {8, 6, 1, 0}, {10, 7, 0, 0},
		{11, 7, 1, 0}, {13, 7, 2, 0}, {17, 7, 3, 0}, {25, 7, 4, 0}, {41, 8, 5, 0}, {73, 8, 32, huffUpper}},
	{{1, 1, 0, 0}, {2, 3, 0, 0}, {3, 4, 0, 0}, {4, 5, 0, 0}, {5, 4, 1, 0}, {7, 3, 3, 0}, {15, 6, 1, 0},
		{17, 6, 2, 0}, {21, 6, 3, 0}, {29, 6, 4, 0}, {45, 6, 5, 0}, {77, 7, 6, 0}, {141, 7, 32, huffUpper}},
	{{-2, 3, 0, 0}, {-1, 3, 0, 0}, {0, 1, 0, 0}, {1, 3, 0, 0}, {2, 3, 0, 0}},
	{{-24, 7, 4, 0}, {-8, 6, 2, 0}, {-4, 5, 1, 0}, {-2, 4, 0, 0}, {-1, 3, 0, 0}, {0, 1, 0, 0}, {1, 3, 0, 0},
		{2, 4, 0, 0}, {3, 5, 1, 0}, {5, 6, 2, 0}, {9, 7, 4, 0},
		{-25, 7, 32, huffLower}, {25, 7, 32, huffUpper}},
}

var standardTables = sync.OnceValue(func() (out [15]*huffTable) {
	for i, lines := range huffStandard {
		t, err := newHuffTable(lines)
		if err != nil {
			panic(err) // the tables above are constants
		}
		out[i] = t
	}
	return out
})

// standardTable returns table B.n.
func standardTable(n int) *huffTable {
	return standardTables()[n-1]
}

// tableSegment decodes a code table segment, B.2.
func (d *jb2) tableSegment(seg segHeader, data []byte) error {
	if len(data) < 9 {
		return errShort
	}
	flags := data[0]
	oob := flags&1 != 0
	ps := int(flags>>1&7) + 1
	rs := int(flags>>4&7) + 1
	low := int(int32(binary.BigEndian.Uint32(data[1:])))
	high := int(int32(binary.BigEndian.Uint32(data[5:])))
	if low >= high {
		return errors.New("empty code table")
	}
	r := bitReader{data: data[9:]}
	var lines []huffLine
	for cur := low; cur < high; {
		if err := d.work.spend(1); err != nil {
			return err
		}
		p, err := r.readBits(ps)
		if err != nil {
			return err
		}
		n, err := r.readBits(rs)
		if err != nil {
			return err
		}
		if n >= 32 {
			return errors.New("code table range is too long")
		}
		lines = append(lines, huffLine{cur, int(p), int(n), huffNormal})
		cur += 1 << n
	}
	p, err := r.readBits(ps)
	if err != nil {
		return err
	}
	lines = append(lines, huffLine{low - 1, int(p), 32, huffLower})
	if p, err = r.readBits(ps); err != nil {
		return err
	}
	lines = append(lines, huffLine{high, int(p), 32, huffUpper})
	if oob {
		if p, err = r.readBits(ps); err != nil {
			return err
		}
		lines = append(lines, huffLine{0, int(p), 0, huffOOB})
	}
	t, err := newHuffTable(lines)
	if err != nil {
		return err
	}
	d.tables[seg.num] = t
	return nil
}
