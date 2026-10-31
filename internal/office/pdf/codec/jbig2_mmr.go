package codec

import (
	"errors"
	"sync"
)

// faxCode is one run length code of T.4.
type faxCode struct {
	run  int
	bits string
}

var faxWhite = []faxCode{
	{0, "00110101"}, {1, "000111"}, {2, "0111"}, {3, "1000"}, {4, "1011"}, {5, "1100"}, {6, "1110"}, {7, "1111"},
	{8, "10011"}, {9, "10100"}, {10, "00111"}, {11, "01000"}, {12, "001000"}, {13, "000011"}, {14, "110100"},
	{15, "110101"}, {16, "101010"}, {17, "101011"}, {18, "0100111"}, {19, "0001100"}, {20, "0001000"},
	{21, "0010111"}, {22, "0000011"}, {23, "0000100"}, {24, "0101000"}, {25, "0101011"}, {26, "0010011"},
	{27, "0100100"}, {28, "0011000"}, {29, "00000010"}, {30, "00000011"}, {31, "00011010"}, {32, "00011011"},
	{33, "00010010"}, {34, "00010011"}, {35, "00010100"}, {36, "00010101"}, {37, "00010110"}, {38, "00010111"},
	{39, "00101000"}, {40, "00101001"}, {41, "00101010"}, {42, "00101011"}, {43, "00101100"}, {44, "00101101"},
	{45, "00000100"}, {46, "00000101"}, {47, "00001010"}, {48, "00001011"}, {49, "01010010"}, {50, "01010011"},
	{51, "01010100"}, {52, "01010101"}, {53, "00100100"}, {54, "00100101"}, {55, "01011000"}, {56, "01011001"},
	{57, "01011010"}, {58, "01011011"}, {59, "01001010"}, {60, "01001011"}, {61, "00110010"}, {62, "00110011"},
	{63, "00110100"},
	{64, "11011"}, {128, "10010"}, {192, "010111"}, {256, "0110111"}, {320, "00110110"}, {384, "00110111"},
	{448, "01100100"}, {512, "01100101"}, {576, "01101000"}, {640, "01100111"}, {704, "011001100"},
	{768, "011001101"}, {832, "011010010"}, {896, "011010011"}, {960, "011010100"}, {1024, "011010101"},
	{1088, "011010110"}, {1152, "011010111"}, {1216, "011011000"}, {1280, "011011001"}, {1344, "011011010"},
	{1408, "011011011"}, {1472, "010011000"}, {1536, "010011001"}, {1600, "010011010"}, {1664, "011000"},
	{1728, "010011011"},
}

var faxBlack = []faxCode{
	{0, "0000110111"}, {1, "010"}, {2, "11"}, {3, "10"}, {4, "011"}, {5, "0011"}, {6, "0010"}, {7, "00011"},
	{8, "000101"}, {9, "000100"}, {10, "0000100"}, {11, "0000101"}, {12, "0000111"}, {13, "00000100"},
	{14, "00000111"}, {15, "000011000"}, {16, "0000010111"}, {17, "0000011000"}, {18, "0000001000"},
	{19, "00001100111"}, {20, "00001101000"}, {21, "00001101100"}, {22, "00000110111"}, {23, "00000101000"},
	{24, "00000010111"}, {25, "00000011000"}, {26, "000011001010"}, {27, "000011001011"}, {28, "000011001100"},
	{29, "000011001101"}, {30, "000001101000"}, {31, "000001101001"}, {32, "000001101010"}, {33, "000001101011"},
	{34, "000011010010"}, {35, "000011010011"}, {36, "000011010100"}, {37, "000011010101"}, {38, "000011010110"},
	{39, "000011010111"}, {40, "000001101100"}, {41, "000001101101"}, {42, "000011011010"}, {43, "000011011011"},
	{44, "000001010100"}, {45, "000001010101"}, {46, "000001010110"}, {47, "000001010111"}, {48, "000001100100"},
	{49, "000001100101"}, {50, "000001010010"}, {51, "000001010011"}, {52, "000000100100"}, {53, "000000110111"},
	{54, "000000111000"}, {55, "000000100111"}, {56, "000000101000"}, {57, "000001011000"}, {58, "000001011001"},
	{59, "000000101011"}, {60, "000000101100"}, {61, "000001011010"}, {62, "000001100110"}, {63, "000001100111"},
	{64, "0000001111"}, {128, "000011001000"}, {192, "000011001001"}, {256, "000001011011"}, {320, "000000110011"},
	{384, "000000110100"}, {448, "000000110101"}, {512, "0000001101100"}, {576, "0000001101101"},
	{640, "0000001001010"}, {704, "0000001001011"}, {768, "0000001001100"}, {832, "0000001001101"},
	{896, "0000001110010"}, {960, "0000001110011"}, {1024, "0000001110100"}, {1088, "0000001110101"},
	{1152, "0000001110110"}, {1216, "0000001110111"}, {1280, "0000001010010"}, {1344, "0000001010011"},
	{1408, "0000001010100"}, {1472, "0000001010101"}, {1536, "0000001011010"}, {1600, "0000001011011"},
	{1664, "0000001100100"}, {1728, "0000001100101"},
}

var faxShared = []faxCode{
	{1792, "00000001000"}, {1856, "00000001100"}, {1920, "00000001101"}, {1984, "000000010010"},
	{2048, "000000010011"}, {2112, "000000010100"}, {2176, "000000010101"}, {2240, "000000010110"},
	{2304, "000000010111"}, {2368, "000000011100"}, {2432, "000000011101"}, {2496, "000000011110"},
	{2560, "000000011111"},
}

const faxPeek = 13 // the longest run length code

// faxEntry is a lookup result: the run and the code length, with zero length for no code.
type faxEntry struct {
	run  int16
	bits uint8
}

var (
	faxOnce   sync.Once
	faxTables [2][]faxEntry
)

func faxTable(black int) []faxEntry {
	faxOnce.Do(func() {
		for colour, codes := range [][]faxCode{faxWhite, faxBlack} {
			t := make([]faxEntry, 1<<faxPeek)
			for _, list := range [][]faxCode{codes, faxShared} {
				for _, c := range list {
					v := 0
					for _, b := range c.bits {
						v = v<<1 | int(b-'0')
					}
					shift := faxPeek - len(c.bits)
					for i := range 1 << shift {
						t[v<<shift|i] = faxEntry{int16(c.run), uint8(len(c.bits))}
					}
				}
			}
			faxTables[colour] = t
		}
	})
	return faxTables[black]
}

// mmrReader reads bits MSB first. Past the end it gives zeros, which match no code.
type mmrReader struct {
	data []byte
	bit  int
}

// peek returns the next n bits, at most 24, without using them.
func (r *mmrReader) peek(n int) int {
	first := r.bit >> 3
	var acc uint32
	for i := range 4 {
		acc <<= 8
		if first+i < len(r.data) {
			acc |= uint32(r.data[first+i])
		}
	}
	return int(acc << (r.bit & 7) >> (32 - n))
}

func (r *mmrReader) atEnd() bool {
	return r.bit >= len(r.data)*8
}

// run reads a full run: make-up codes followed by a terminating code.
func (r *mmrReader) run(black int) (int, error) {
	t := faxTable(black)
	total := 0
	for {
		e := t[r.peek(faxPeek)]
		if e.bits == 0 {
			return 0, errors.New("bad run length code in MMR data")
		}
		r.bit += int(e.bits)
		total += int(e.run)
		if e.run < 64 {
			return total, nil
		}
		if total > 1<<28 {
			return 0, errors.New("run too long in MMR data")
		}
	}
}

// decodeMMR decodes a T.6 bitmap and returns the bytes used, an EOFB included.
func decodeMMR(data []byte, w, h int, work *budget) (*bitmap, int, error) {
	bm := newBitmap(w, h)
	if w == 0 || h == 0 {
		return bm, 0, nil
	}
	r := mmrReader{data: data}
	ref := make([]int, 0, 64)
	cur := make([]int, 0, 64)
	at := func(list []int, i int) int {
		if i < len(list) {
			return list[i]
		}
		return w
	}
	eofb := false
rows:
	for y := range h {
		if err := work.spend(w + 1); err != nil {
			return nil, 0, err
		}
		cur = cur[:0]
		row := bm.row(y)
		a0, colour, bi := -1, 0, 0
		for a0 < w {
			if r.peek(24) == 0x001001 {
				r.bit += 24
				eofb = true
				break rows
			}
			if r.atEnd() {
				return nil, 0, errors.New("truncated MMR data")
			}
			if err := work.spend(1); err != nil {
				return nil, 0, err
			}
			// b1 is the first change right of a0 towards the opposite colour.
			for bi > 0 && at(ref, bi-1) > a0 {
				bi--
			}
			for at(ref, bi) <= a0 && at(ref, bi) < w {
				bi++
			}
			if bi&1 != colour {
				bi++
			}
			b1, b2 := at(ref, bi), at(ref, bi+1)
			var delta int
			switch {
			case r.peek(1) == 1:
				r.bit++
			case r.peek(3) == 3:
				r.bit += 3
				delta = 1
			case r.peek(3) == 2:
				r.bit += 3
				delta = -1
			case r.peek(3) == 1: // horizontal
				r.bit += 3
				n1, err := r.run(colour)
				if err != nil {
					return nil, 0, err
				}
				n2, err := r.run(1 - colour)
				if err != nil {
					return nil, 0, err
				}
				start := max(a0, 0)
				mid := start + n1
				end := mid + n2
				if end > w {
					return nil, 0, errors.New("MMR run passes the end of the row")
				}
				if colour == 1 {
					fillRun(row, start, mid)
				} else {
					fillRun(row, mid, end)
				}
				cur = append(cur, mid, end)
				a0 = end
				continue
			case r.peek(4) == 1: // pass
				r.bit += 4
				if colour == 1 {
					fillRun(row, max(a0, 0), b2)
				}
				a0 = b2
				continue
			case r.peek(6) == 3:
				r.bit += 6
				delta = 2
			case r.peek(6) == 2:
				r.bit += 6
				delta = -2
			case r.peek(7) == 3:
				r.bit += 7
				delta = 3
			case r.peek(7) == 2:
				r.bit += 7
				delta = -3
			default:
				return nil, 0, errors.New("bad mode code in MMR data")
			}
			a1 := b1 + delta
			if a1 < 0 || a1 > w || a1 < a0 || (a1 == a0 && a0 >= 0 && a1 < w) {
				return nil, 0, errors.New("MMR change moves backwards")
			}
			if colour == 1 {
				fillRun(row, max(a0, 0), a1)
			}
			cur = append(cur, a1)
			a0 = a1
			colour = 1 - colour
		}
		ref, cur = cur, ref
	}
	if !eofb && r.peek(24) == 0x001001 {
		r.bit += 24
	}
	return bm, min((r.bit+7)/8, len(data)), nil
}

func fillRun(row []byte, from, to int) {
	for i := from; i < to; i++ {
		row[i] = 1
	}
}
