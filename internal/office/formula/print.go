package formula

import (
	"strconv"
	"strings"
)

// shift gives the corners of the reference with its relative parts moved by dr, dc, in the order written.
// ok is false when a corner leaves the sheet; with wrap it comes round the other side instead, as names do.
func (r *ref) shift(dr, dc int, wrap bool) (r1, c1, r2, c2 int32, ok bool) {
	r1, c1, r2, c2 = r.r1, r.c1, r.r2, r.c2
	move := func(v *int32, d, size int, absolute bool) bool {
		if absolute || d == 0 {
			return true
		}
		n := int(*v) + d
		if wrap {
			n %= size
			if n < 0 {
				n += size
			}
		}
		if n < 0 || n >= size {
			return false
		}
		*v = int32(n)
		return true
	}
	if r.form != formCols {
		if !move(&r1, dr, MaxRows, r.abs&absR1 != 0) || !move(&r2, dr, MaxRows, r.abs&absR2 != 0) {
			return 0, 0, 0, 0, false
		}
	}
	if r.form != formRows {
		if !move(&c1, dc, MaxCols, r.abs&absC1 != 0) || !move(&c2, dc, MaxCols, r.abs&absC2 != 0) {
			return 0, 0, 0, 0, false
		}
	}
	return r1, c1, r2, c2, true
}

// cellText prints the cell part of a reference from its corners.
func (r *ref) cellText(r1, c1, r2, c2 int32, abs uint8) string {
	var b strings.Builder
	cell := func(row, col int32, absRow, absCol bool) {
		if absCol {
			b.WriteByte('$')
		}
		b.WriteString(ColName(int(col)))
		if absRow {
			b.WriteByte('$')
		}
		b.WriteString(strconv.Itoa(int(row) + 1))
	}
	switch r.form {
	case formCell:
		cell(r1, c1, abs&absR1 != 0, abs&absC1 != 0)
	case formRange:
		cell(r1, c1, abs&absR1 != 0, abs&absC1 != 0)
		b.WriteByte(':')
		cell(r2, c2, abs&absR2 != 0, abs&absC2 != 0)
	case formCols:
		if abs&absC1 != 0 {
			b.WriteByte('$')
		}
		b.WriteString(ColName(int(c1)))
		b.WriteByte(':')
		if abs&absC2 != 0 {
			b.WriteByte('$')
		}
		b.WriteString(ColName(int(c2)))
	case formRows:
		if abs&absR1 != 0 {
			b.WriteByte('$')
		}
		b.WriteString(strconv.Itoa(int(r1) + 1))
		b.WriteByte(':')
		if abs&absR2 != 0 {
			b.WriteByte('$')
		}
		b.WriteString(strconv.Itoa(int(r2) + 1))
	}
	return b.String()
}

// rewrite replaces the spans fn chooses; the rest of the source stays exactly as written.
// fn gives the new text of the span from `from` to the reference's end, and whether to replace it.
func rewrite(src string, refs []ref, fn func(r *ref) (text string, from int32, ok bool)) (string, bool) {
	var b strings.Builder
	last := 0
	changed := false
	for i := range refs {
		r := &refs[i]
		text, from, ok := fn(r)
		if !ok {
			continue
		}
		if src[from:r.end] == text {
			continue
		}
		b.WriteString(src[last:from])
		b.WriteString(text)
		last = int(r.end)
		changed = true
	}
	if !changed {
		return src, false
	}
	b.WriteString(src[last:])
	return b.String(), true
}

// Text gives the formula text, without '=', with the relative parts of references shifted by dr, dc.
// A reference pushed off the sheet becomes #REF!. Text(0, 0) is the source exactly.
func (x *Expr) Text(dr, dc int) string {
	if dr == 0 && dc == 0 {
		return x.src
	}
	out, _ := rewrite(x.src, x.refs, func(r *ref) (string, int32, bool) {
		if r.isName || r.bad || r.external {
			return "", 0, false
		}
		r1, c1, r2, c2, ok := r.shift(dr, dc, false)
		if !ok {
			return ErrRef, r.cell, true
		}
		if r1 == r.r1 && c1 == r.c1 && r2 == r.r2 && c2 == r.c2 {
			return "", 0, false
		}
		return r.cellText(r1, c1, r2, c2, r.abs), r.cell, true
	})
	return out
}
