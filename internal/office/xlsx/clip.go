package xlsx

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

// Clip is a block of cells lifted for pasting or filling: values, formulas as they stood, and formats.
type Clip struct {
	Rows, Cols int
	Cells      []ClipCell // row-major
	Heights    []float64
	FromRow    int
	FromCol    int
	Sheet      *Sheet
	Cut        bool
}

// ClipCell is one cell of a Clip.
type ClipCell struct {
	Value formula.Value
	F     *Formula
	Text  string // the formula's text
	Style int32
	Has   bool
}

// Copy lifts an area. An area wider or taller than what is used is cut to what is used.
func (s *Sheet) Copy(a formula.Area) *Clip {
	u := s.Used()
	if int(a.R2) == MaxRows-1 {
		a.R2 = max(a.R1, u.R2)
	}
	if int(a.C2) == MaxCols-1 {
		a.C2 = max(a.C1, u.C2)
	}
	cl := &Clip{Rows: int(a.R2-a.R1) + 1, Cols: int(a.C2-a.C1) + 1, FromRow: int(a.R1), FromCol: int(a.C1), Sheet: s}
	cl.Cells = make([]ClipCell, cl.Rows*cl.Cols)
	for i := range cl.Cells {
		r, c := int(a.R1)+i/cl.Cols, int(a.C1)+i%cl.Cols
		cc := &cl.Cells[i]
		cc.Style = s.StyleAt(r, c)
		if cell := s.Cell(r, c); cell != nil {
			cc.Has = true
			cc.Value = s.wb.value(cell)
			if cell.F != nil {
				cc.F = cell.F
				cc.Text = cell.FormulaText()
			}
		}
	}
	return cl
}

// TSV is the block as tab-separated text of what the cells show, as other programs read a paste.
func (s *Sheet) TSV(cl *Clip) string {
	var b strings.Builder
	for i := range cl.Cells {
		if i > 0 {
			if i%cl.Cols == 0 {
				b.WriteString("\r\n")
			} else {
				b.WriteByte('\t')
			}
		}
		c := &cl.Cells[i]
		if !c.Has {
			continue
		}
		text := s.wb.Styles.Get(c.Style).Format.Render(c.Value, s.wb.date).Text
		if strings.ContainsAny(text, "\t\n\"") {
			text = `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
		}
		b.WriteString(text)
	}
	return b.String()
}

// Paste lays a block at (r, c), repeated across dst when dst is a whole multiple of it. Relative references move as far
// as the cell moved, except for a cut. formats false keeps the target's formats.
func (s *Sheet) Paste(cl *Clip, dst formula.Area, formats bool) error {
	rows, cols := cl.Rows, cl.Cols
	if h := int(dst.R2-dst.R1) + 1; h%cl.Rows == 0 {
		rows = h
	}
	if w := int(dst.C2-dst.C1) + 1; w%cl.Cols == 0 {
		cols = w
	}
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			r, c := int(dst.R1)+i, int(dst.C1)+j
			if r >= MaxRows || c >= MaxCols {
				continue
			}
			src := &cl.Cells[(i%cl.Rows)*cl.Cols+j%cl.Cols]
			s.pasteCell(src, r, c, r-(cl.FromRow+i%cl.Rows), c-(cl.FromCol+j%cl.Cols), cl.Cut, formats)
		}
	}
	return nil
}

func (s *Sheet) pasteCell(src *ClipCell, r, c, dr, dc int, cut, formats bool) {
	switch {
	case src.F != nil && src.F.Expr != nil:
		f := &Formula{Expr: src.F.Expr, DR: src.F.DR + int32(dr), DC: src.F.DC + int32(dc), Shared: -1}
		if cut {
			f.DR, f.DC = src.F.DR, src.F.DC
		}
		f.Text = f.Expr.Text(int(f.DR), int(f.DC))
		// A formula that moved re-parses so its Expr holds its own text.
		if ex, err := formula.Parse(f.Text); err == nil {
			f.Expr, f.DR, f.DC = ex, 0, 0
		}
		s.put(r, c, formula.Value{}, f)
	case src.F != nil:
		s.put(r, c, src.Value, &Formula{Text: src.Text, Shared: -1})
	default:
		s.put(r, c, src.Value, nil)
	}
	if formats {
		s.styleCell(r, c, func(int32) int32 { return src.Style })
	}
}

// Fill extends the block in src over dst, which contains it and reaches past it along one direction. Numbers and dates
// continue the step between the first and last of two or more; a single number repeats; text ending in a number counts
// on; names of days and months go round; formulas move as if copied.
func (s *Sheet) Fill(src, dst formula.Area) {
	down := dst.R2 > src.R2 || dst.R1 < src.R1
	if down {
		for c := int(src.C1); c <= int(src.C2); c++ {
			line := make([]int, int(src.R2-src.R1)+1)
			for i := range line {
				line[i] = int(src.R1) + i
			}
			s.fillLine(line, c, true, int(dst.R1), int(dst.R2))
		}
		return
	}
	for r := int(src.R1); r <= int(src.R2); r++ {
		line := make([]int, int(src.C2-src.C1)+1)
		for i := range line {
			line[i] = int(src.C1) + i
		}
		s.fillLine(line, r, false, int(dst.C1), int(dst.C2))
	}
}

var reTrailingNum = regexp.MustCompile(`^(.*?)(\d+)$`)

var seriesLists = [][]string{
	{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
	{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"},
	{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
	{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	{"星期一", "星期二", "星期三", "星期四", "星期五", "星期六", "星期日"},
	{"周一", "周二", "周三", "周四", "周五", "周六", "周日"},
	{"一月", "二月", "三月", "四月", "五月", "六月", "七月", "八月", "九月", "十月", "十一月", "十二月"},
	{"第一季度", "第二季度", "第三季度", "第四季度"},
	{"Q1", "Q2", "Q3", "Q4"},
}

func listOf(text string) ([]string, int) {
	for _, l := range seriesLists {
		for i, v := range l {
			if strings.EqualFold(v, text) {
				return l, i
			}
		}
	}
	return nil, 0
}

// fillLine fills one row or column from the source cells at positions line (rows, or columns when down is false), the
// cross position fixed, over from..to.
func (s *Sheet) fillLine(line []int, cross int, down bool, from, to int) {
	cell := func(p int) (r, c int) {
		if down {
			return p, cross
		}
		return cross, p
	}
	type seed struct {
		v    formula.Value
		f    *Formula
		text string
		st   int32
	}
	seeds := make([]seed, len(line))
	allNum := true
	for i, p := range line {
		r, c := cell(p)
		sd := seed{st: s.StyleAt(r, c), v: s.ValueAt(r, c)}
		if ce := s.Cell(r, c); ce != nil && ce.F != nil {
			sd.f, sd.text = ce.F, ce.FormulaText()
		}
		if sd.v.Kind != formula.Number || sd.f != nil {
			allNum = false
		}
		seeds[i] = sd
	}
	first, last := line[0], line[len(line)-1]
	stepNum := 0.0
	if allNum && len(line) > 1 {
		// Excel's linear trend: the least squares line.
		var sx, sy, sxx, sxy float64
		n := float64(len(line))
		for i := range seeds {
			x, y := float64(i), seeds[i].v.Num
			sx += x
			sy += y
			sxx += x * x
			sxy += x * y
		}
		stepNum = (n*sxy - sx*sy) / (n*sxx - sx*sx)
	}
	put := func(p int) {
		// The seed this place repeats: position in the pattern, counting back too for a drag up or left.
		off := p - first
		n := len(line)
		i := ((off % n) + n) % n
		sd := seeds[i]
		r, c := cell(p)
		cycles := off
		if off >= 0 {
			cycles = off / n
		} else {
			cycles = (off - n + 1) / n
		}
		switch {
		case sd.f != nil && sd.f.Expr != nil:
			dr, dc := int32(0), int32(0)
			if down {
				dr = int32(p - line[i])
			} else {
				dc = int32(p - line[i])
			}
			text := sd.f.Expr.Text(int(sd.f.DR+dr), int(sd.f.DC+dc))
			if ex, err := formula.Parse(text); err == nil {
				s.put(r, c, formula.Value{}, &Formula{Expr: ex, Text: text, Shared: -1})
			}
		case allNum && len(line) > 1:
			s.put(r, c, formula.Num(seeds[0].v.Num+float64(off)*stepNum), nil)
		case sd.v.Kind == formula.Text:
			s.put(r, c, formula.Str(nextText(sd.v.Str, cycles, n)), nil)
		case sd.v.Kind == formula.Number && s.wb.Styles.Get(sd.st).Format.IsDate():
			s.put(r, c, formula.Num(sd.v.Num+float64(off)), nil)
		default:
			s.put(r, c, sd.v, nil)
		}
		s.styleCell(r, c, func(int32) int32 { return sd.st })
	}
	for p := last + 1; p <= to; p++ {
		put(p)
	}
	for p := first - 1; p >= from; p-- {
		put(p)
	}
}

// nextText is a text seed moved on by cycles rounds of a pattern of n cells.
func nextText(text string, cycles, n int) string {
	if l, i := listOf(text); l != nil {
		return l[((i+cycles*n)%len(l)+len(l))%len(l)]
	}
	if m := reTrailingNum.FindStringSubmatch(text); m != nil {
		if v, err := strconv.Atoi(m[2]); err == nil {
			v += cycles * n
			if v < 0 {
				v = -v
			}
			digits := strconv.Itoa(v)
			if len(digits) < len(m[2]) {
				digits = strings.Repeat("0", len(m[2])-len(digits)) + digits
			}
			return m[1] + digits
		}
	}
	return text
}
