package formula

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Edit is the insertion or deletion of rows or columns on a sheet.
type Edit struct {
	Sheet string // the sheet edited
	Rows  bool   // rows, else columns
	At    int    // zero-based first row or column
	N     int    // >0 inserts N before At; <0 deletes -N starting at At
}

// moveSpan takes an inclusive span of rows or columns through an edit. ok is false when none of it is left.
func moveSpan(lo, hi int32, at, n, size int) (int32, int32, bool) {
	a, b := int(lo), int(hi)
	if n > 0 {
		if a >= at {
			a += n
		}
		if b >= at {
			b += n
		}
		if a >= size {
			return 0, 0, false
		}
		if b >= size {
			b = size - 1
		}
		return int32(a), int32(b), true
	}
	del := -n
	end := at + del // first index after the deleted run
	switch {
	case a >= end:
		a -= del
	case a >= at:
		a = at
	}
	switch {
	case b >= end:
		b -= del
	case b >= at:
		b = at - 1
	}
	if a > b {
		return 0, 0, false
	}
	return int32(a), int32(b), true
}

func sameSheet(a, b string) bool { return a == b || strings.EqualFold(a, b) }

// Adjust moves the references in a formula through an edit of rows or columns, by the rules Excel uses: references
// after the point move, ranges grow when inserting inside them and shrink when deleting part of them, and a
// reference wholly deleted becomes #REF!. Unqualified references belong to ownSheet. Only references are touched.
// Text that does not parse comes back unchanged.
func Adjust(src, ownSheet string, e Edit) (string, bool) {
	x, err := Parse(src)
	if err != nil || e.N == 0 {
		return src, false
	}
	return rewrite(src, x.refs, func(r *ref) (string, int32, bool) {
		if r.isName || r.bad || r.external || r.sheet2 != "" {
			return "", 0, false
		}
		sheet := ownSheet
		if r.hasSheet {
			sheet = r.sheet1
		}
		if !sameSheet(sheet, e.Sheet) {
			return "", 0, false
		}
		if (e.Rows && r.form == formCols) || (!e.Rows && r.form == formRows) {
			return "", 0, false
		}
		// Work on the normalised span; a reversed reference comes out in order.
		r1, c1, r2, c2, abs := r.r1, r.c1, r.r2, r.c2, r.abs
		if r1 > r2 {
			r1, r2 = r2, r1
			abs = abs&^(absR1|absR2) | (abs&absR1)<<2 | (abs&absR2)>>2
		}
		if c1 > c2 {
			c1, c2 = c2, c1
			abs = abs&^(absC1|absC2) | (abs&absC1)<<2 | (abs&absC2)>>2
		}
		var ok bool
		var n1, n2 int32
		if e.Rows {
			n1, n2, ok = moveSpan(r1, r2, e.At, e.N, MaxRows)
			r1, r2 = n1, n2
		} else {
			n1, n2, ok = moveSpan(c1, c2, e.At, e.N, MaxCols)
			c1, c2 = n1, n2
		}
		if !ok {
			return ErrRef, r.cell, true
		}
		if r1 == r.r1 && r2 == r.r2 && c1 == r.c1 && c2 == r.c2 {
			return "", 0, false
		}
		return r.cellText(r1, c1, r2, c2, abs), r.cell, true
	})
}

// RenameSheet changes the sheet name in references, requoting as needed.
func RenameSheet(src, from, to string) (string, bool) {
	x, err := Parse(src)
	if err != nil {
		return src, false
	}
	return rewrite(src, x.refs, func(r *ref) (string, int32, bool) {
		if !r.hasSheet || r.external {
			return "", 0, false
		}
		a, b := r.sheet1, r.sheet2
		hit := false
		if sameSheet(a, from) {
			a, hit = to, true
		}
		if b != "" && sameSheet(b, from) {
			b, hit = to, true
		}
		if !hit {
			return "", 0, false
		}
		return sheetPrefixText(a, b) + src[r.cell:r.end], r.pos, true
	})
}

// sheetPrefixText prints Sheet1!, 'My Sheet'! or the 3-D form.
func sheetPrefixText(a, b string) string {
	if b == "" {
		return QuoteSheet(a) + "!"
	}
	if !sheetNeedsQuotes(a) && !sheetNeedsQuotes(b) {
		return a + ":" + b + "!"
	}
	return "'" + strings.ReplaceAll(a, "'", "''") + ":" + strings.ReplaceAll(b, "'", "''") + "'!"
}

// DeleteSheet turns the references to a sheet into #REF!.
func DeleteSheet(src, name string) (string, bool) {
	x, err := Parse(src)
	if err != nil {
		return src, false
	}
	return rewrite(src, x.refs, func(r *ref) (string, int32, bool) {
		if !r.hasSheet || r.external {
			return "", 0, false
		}
		if sameSheet(r.sheet1, name) || (r.sheet2 != "" && sameSheet(r.sheet2, name)) {
			return ErrRef, r.pos, true
		}
		return "", 0, false
	})
}

// sheetNeedsQuotes says a sheet name cannot stand bare before the '!'.
func sheetNeedsQuotes(name string) bool {
	if name == "" {
		return true
	}
	first, _ := utf8.DecodeRuneInString(name)
	if first == '.' || unicode.IsDigit(first) {
		return true
	}
	for _, r := range name {
		if r != '_' && r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
			return true
		}
	}
	if _, _, ok := ParseCell(name); ok {
		return true
	}
	return looksLikeR1C1(name)
}

// looksLikeR1C1 matches R, C, RC, R1, C1 and R1C1 in any case.
func looksLikeR1C1(s string) bool {
	u := strings.ToUpper(s)
	i := 0
	digits := func() {
		for i < len(u) && u[i] >= '0' && u[i] <= '9' {
			i++
		}
	}
	if i < len(u) && u[i] == 'R' {
		i++
		digits()
	}
	if i < len(u) && u[i] == 'C' {
		i++
		digits()
	}
	return i == len(u)
}

// QuoteSheet gives the name quoted the Excel way when it needs it, with ' doubled.
func QuoteSheet(name string) string {
	if !sheetNeedsQuotes(name) {
		return name
	}
	return "'" + strings.ReplaceAll(name, "'", "''") + "'"
}

// AdjustRange moves an A1-style area, or a space-separated list of them, through an edit: merges, conditional
// formatting ranges and the like. It gives "" when nothing is left. e.Sheet is not consulted.
func AdjustRange(sqref string, e Edit) string {
	var out []string
	for _, tok := range strings.Fields(sqref) {
		a, ok := ParseArea(tok)
		if !ok {
			out = append(out, tok)
			continue
		}
		if e.N == 0 {
			out = append(out, tok)
			continue
		}
		wholeCol := a.R1 == 0 && a.R2 == MaxRows-1
		wholeRow := a.C1 == 0 && a.C2 == MaxCols-1
		b := a
		if e.Rows && !wholeCol {
			b.R1, b.R2, ok = moveSpan(a.R1, a.R2, e.At, e.N, MaxRows)
		} else if !e.Rows && !wholeRow {
			b.C1, b.C2, ok = moveSpan(a.C1, a.C2, e.At, e.N, MaxCols)
		}
		if !ok {
			continue
		}
		if b == a {
			out = append(out, tok)
		} else {
			out = append(out, AreaName(b))
		}
	}
	return strings.Join(out, " ")
}
