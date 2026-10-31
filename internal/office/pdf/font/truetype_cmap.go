package font

import "sort"

// maxCmapTables bounds the checks made on each subtable when parsing.
const maxCmapTables = 64

// cmapTable is one subtable of a cmap, its bytes running to the end of the table
// because format 4 length fields are often wrong.
type cmapTable struct {
	platform, encoding int
	format             int
	b                  []byte
	count              int  // Segments or groups.
	sorted             bool // Binary search is safe.
}

func parseCmaps(b []byte) []cmapTable {
	if len(b) < 4 {
		return nil
	}
	var out []cmapTable
	n := min(u16(b, 2), maxCmapTables)
	for i := 0; i < n; i++ {
		rec := 4 + 8*i
		if rec+8 > len(b) {
			break
		}
		off := int64(u32(b, rec+4))
		if off+2 > int64(len(b)) {
			continue
		}
		c := cmapTable{platform: u16(b, rec), encoding: u16(b, rec+2), b: b[off:]}
		c.format = u16(c.b, 0)
		switch c.format {
		case 0, 6:
		case 4:
			c.count = u16(c.b, 6) / 2
			c.sorted = true
			for j := 1; j < c.count; j++ {
				if u16(c.b, 14+2*j) < u16(c.b, 12+2*j) {
					c.sorted = false
					break
				}
			}
		case 12:
			c.count = int(min(int64(u32(c.b, 12)), (int64(len(c.b))-16)/12))
			c.sorted = true
			for j := 1; j < c.count; j++ {
				if u32(c.b, 16+12*j+4) < u32(c.b, 16+12*j-8) {
					c.sorted = false
					break
				}
			}
		default:
			continue
		}
		out = append(out, c)
	}
	return out
}

// lookup returns the glyph of code, 0 when unmapped.
func (c *cmapTable) lookup(code uint32) int {
	switch c.format {
	case 0:
		if code < 256 {
			if i := 6 + int(code); i < len(c.b) {
				return int(c.b[i])
			}
		}
	case 6:
		first, count := uint32(u16(c.b, 6)), uint32(u16(c.b, 8))
		if code >= first && code-first < count {
			return u16(c.b, 10+2*int(code-first))
		}
	case 4:
		return c.lookup4(code)
	case 12:
		return c.lookup12(code)
	}
	return 0
}

func (c *cmapTable) lookup4(code uint32) int {
	if code > 0xFFFF {
		return 0
	}
	seg := c.count
	v := int(code)
	end := func(i int) int { return u16(c.b, 14+2*i) }
	start := func(i int) int { return u16(c.b, 16+2*seg+2*i) }
	i := seg
	if c.sorted {
		i = sort.Search(seg, func(i int) bool { return end(i) >= v })
	} else {
		for j := 0; j < seg; j++ {
			if start(j) <= v && v <= end(j) {
				i = j
				break
			}
		}
	}
	if i >= seg || start(i) > v {
		return 0
	}
	delta := u16(c.b, 16+4*seg+2*i)
	rangePos := 16 + 6*seg + 2*i
	ro := u16(c.b, rangePos)
	if ro == 0 {
		return (v + delta) & 0xFFFF
	}
	g := u16(c.b, rangePos+ro+2*(v-start(i)))
	if g == 0 {
		return 0
	}
	return (g + delta) & 0xFFFF
}

func (c *cmapTable) lookup12(code uint32) int {
	start := func(i int) uint32 { return u32(c.b, 16+12*i) }
	end := func(i int) uint32 { return u32(c.b, 20+12*i) }
	i := c.count
	if c.sorted {
		i = sort.Search(c.count, func(i int) bool { return end(i) >= code })
	} else {
		for j := 0; j < c.count; j++ {
			if start(j) <= code && code <= end(j) {
				i = j
				break
			}
		}
	}
	if i >= c.count || start(i) > code {
		return 0
	}
	g := int64(u32(c.b, 24+12*i)) + int64(code-start(i))
	if g > 0xFFFFFF {
		return 0
	}
	return int(g)
}

// GIDByCmap looks code up in that exact subtable; formats 0, 4, 6 and 12 are read.
func (t *TrueType) GIDByCmap(platform, encoding int, code uint32) (int, bool) {
	for i := range t.cmaps {
		c := &t.cmaps[i]
		if c.platform == platform && c.encoding == encoding {
			gid := c.lookup(code)
			return gid, gid != 0 && gid < t.numGlyphs
		}
	}
	return 0, false
}

// HasCmap reports a readable subtable for that pair.
func (t *TrueType) HasCmap(platform, encoding int) bool {
	for i := range t.cmaps {
		if t.cmaps[i].platform == platform && t.cmaps[i].encoding == encoding {
			return true
		}
	}
	return false
}

// Cmaps lists the (platform, encoding) pairs of the readable subtables.
func (t *TrueType) Cmaps() [][2]int {
	out := make([][2]int, len(t.cmaps))
	for i := range t.cmaps {
		out[i] = [2]int{t.cmaps[i].platform, t.cmaps[i].encoding}
	}
	return out
}
