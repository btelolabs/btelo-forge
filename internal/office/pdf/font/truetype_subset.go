package font

import (
	"encoding/binary"
	"errors"
	"sort"
)

// Subset writes a minimal TrueType holding the given glyphs renumbered 1..n after
// .notdef, with composite components appended after them. newGID maps old ids to
// new ones; glyphs out of range are left out of it. Hinting programs are kept.
func (t *TrueType) Subset(gids []int) (data []byte, newGID map[int]int, err error) {
	defer func() {
		if err != nil {
			data, newGID = nil, nil
		}
	}()
	defer recovered(&err)
	if t.cff != nil {
		return nil, nil, errors.New("font: cannot subset CFF outlines")
	}
	newGID = map[int]int{0: 0}
	order := []int{0}
	add := func(gid int) {
		if _, seen := newGID[gid]; !seen && gid >= 0 && gid < t.numGlyphs {
			newGID[gid] = len(order)
			order = append(order, gid)
		}
	}
	for _, gid := range gids {
		add(gid)
	}
	// The list grows as components turn up, and each glyph joins it once.
	for i := 0; i < len(order); i++ {
		for _, comp := range compositeParts(t.glyphData(order[i])) {
			add(comp.gid)
		}
	}
	var glyf []byte
	loca := make([]byte, 4*(len(order)+1))
	hmtx := make([]byte, 4*len(order))
	for i, gid := range order {
		binary.BigEndian.PutUint32(loca[4*i:], uint32(len(glyf)))
		g := t.glyphData(gid)
		if len(g) >= 10 {
			start := len(glyf)
			glyf = append(glyf, g...)
			for _, comp := range compositeParts(g) {
				if to, ok := newGID[comp.gid]; ok {
					binary.BigEndian.PutUint16(glyf[start+comp.at:], uint16(to))
				}
			}
			for len(glyf)%4 != 0 {
				glyf = append(glyf, 0)
			}
		}
		if adv, ok := t.rawAdvance(gid); ok {
			binary.BigEndian.PutUint16(hmtx[4*i:], uint16(adv))
		}
		lsb, ok := t.leftBearing(gid)
		if !ok {
			lsb = i16(g, 2) // Keeps the outline where the source left it.
		}
		binary.BigEndian.PutUint16(hmtx[4*i+2:], uint16(lsb))
	}
	binary.BigEndian.PutUint32(loca[4*len(order):], uint32(len(glyf)))
	if len(glyf) == 0 {
		glyf = make([]byte, 4)
	}
	tables := map[string][]byte{
		"glyf": glyf,
		"head": t.subsetHead(),
		"hhea": t.subsetHhea(len(order)),
		"hmtx": hmtx,
		"loca": loca,
		"maxp": t.subsetMaxp(len(order)),
	}
	for _, tag := range []string{"cvt ", "fpgm", "prep"} {
		if b := t.tables[tag]; len(b) > 0 {
			tables[tag] = b
		}
	}
	return writeSfnt(tables), newGID, nil
}

func (t *TrueType) rawAdvance(gid int) (int, bool) {
	if t.numHM == 0 {
		return 0, false
	}
	i := min(gid, t.numHM-1)
	if 4*i+2 > len(t.hmtx) {
		return 0, false
	}
	return u16(t.hmtx, 4*i), true
}

// leftBearing reads the hmtx bearing, false when the table stops short of it.
func (t *TrueType) leftBearing(gid int) (int, bool) {
	if t.numHM == 0 {
		return 0, false
	}
	at := 4*gid + 2
	if gid >= t.numHM {
		at = 4*t.numHM + 2*(gid-t.numHM)
	}
	return i16(t.hmtx, at), at+2 <= len(t.hmtx)
}

func (t *TrueType) subsetHead() []byte {
	head := make([]byte, 54)
	if src := t.tables["head"]; len(src) >= 54 {
		copy(head, src)
	} else {
		binary.BigEndian.PutUint32(head, 0x00010000)
		binary.BigEndian.PutUint32(head[12:], 0x5F0F3CF5)
		binary.BigEndian.PutUint16(head[18:], uint16(t.upem))
		for i, v := range t.bbox {
			binary.BigEndian.PutUint16(head[36+2*i:], uint16(int16(v)))
		}
	}
	binary.BigEndian.PutUint32(head[8:], 0) // Set by writeSfnt.
	binary.BigEndian.PutUint16(head[50:], 1)
	return head
}

func (t *TrueType) subsetHhea(n int) []byte {
	hhea := make([]byte, 36)
	if src := t.tables["hhea"]; len(src) >= 36 {
		copy(hhea, src)
	} else {
		binary.BigEndian.PutUint32(hhea, 0x00010000)
		binary.BigEndian.PutUint16(hhea[4:], uint16(int16(t.ascent)))
		binary.BigEndian.PutUint16(hhea[6:], uint16(int16(t.descent)))
	}
	binary.BigEndian.PutUint16(hhea[34:], uint16(n))
	return hhea
}

func (t *TrueType) subsetMaxp(n int) []byte {
	maxp := make([]byte, 32)
	if src := t.tables["maxp"]; len(src) >= 32 {
		copy(maxp, src)
	} else {
		binary.BigEndian.PutUint32(maxp, 0x00010000)
		for i := 6; i < 32; i += 2 {
			binary.BigEndian.PutUint16(maxp[i:], 0x4000)
		}
	}
	binary.BigEndian.PutUint16(maxp[4:], uint16(n))
	return maxp
}

type compositePart struct {
	gid int
	at  int // Offset of the glyph id field.
}

// compositeParts walks a composite record list. Bad records end the walk.
func compositeParts(g []byte) []compositePart {
	if len(g) < 10 || i16(g, 0) >= 0 {
		return nil
	}
	var parts []compositePart
	p := 10
	for len(parts) < maxComponents && p+4 <= len(g) {
		flags := u16(g, p)
		parts = append(parts, compositePart{gid: u16(g, p+2), at: p + 2})
		p += 4
		if flags&compWords != 0 {
			p += 4
		} else {
			p += 2
		}
		switch {
		case flags&compScale != 0:
			p += 2
		case flags&compXYScale != 0:
			p += 4
		case flags&comp2x2 != 0:
			p += 8
		}
		if flags&compMore == 0 {
			break
		}
	}
	return parts
}

func sfntChecksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i+4 <= len(b); i += 4 {
		sum += binary.BigEndian.Uint32(b[i:])
	}
	if rest := len(b) % 4; rest != 0 {
		var last [4]byte
		copy(last[:], b[len(b)-rest:])
		sum += binary.BigEndian.Uint32(last[:])
	}
	return sum
}

// writeSfnt lays the tables out in tag order and sets the head checksum.
func writeSfnt(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	n := len(tags)
	pow, log := 1, 0
	for pow*2 <= n {
		pow *= 2
		log++
	}
	size := 12 + 16*n
	for _, tag := range tags {
		size += (len(tables[tag]) + 3) &^ 3
	}
	out := make([]byte, 12+16*n, size)
	binary.BigEndian.PutUint32(out, 0x00010000)
	binary.BigEndian.PutUint16(out[4:], uint16(n))
	binary.BigEndian.PutUint16(out[6:], uint16(16*pow))
	binary.BigEndian.PutUint16(out[8:], uint16(log))
	binary.BigEndian.PutUint16(out[10:], uint16(16*(n-pow)))
	headAt := 0
	for i, tag := range tags {
		b := tables[tag]
		rec := out[12+16*i:]
		copy(rec, tag)
		binary.BigEndian.PutUint32(rec[4:], sfntChecksum(b))
		binary.BigEndian.PutUint32(rec[8:], uint32(len(out)))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(b)))
		if tag == "head" {
			headAt = len(out)
		}
		out = append(out, b...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
	}
	binary.BigEndian.PutUint32(out[headAt+8:], 0xB1B0AFBA-sfntChecksum(out))
	return out
}
