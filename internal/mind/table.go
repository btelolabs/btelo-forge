package mind

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// A table's cells are words or numbers, or formulas as in a spreadsheet: a cell starting with = is worked out from
// numbers, other cells (A1 is the first column's first row), + - * / ^ and brackets, and the functions SUM, AVERAGE,
// MIN, MAX, COUNT, ABS and ROUND over cells or ranges such as A1:B3. A formula that cannot be worked out shows why:
// #REF! for a cell outside the table, #CYCLE! for one that depends on itself, #DIV/0! and #ERROR! otherwise.

var (
	errRef   = errors.New("#REF!")
	errCycle = errors.New("#CYCLE!")
	errDiv   = errors.New("#DIV/0!")
	errSyn   = errors.New("#ERROR!")
)

// evalGrid is every cell as shown, formulas worked out.
func evalGrid(g *Grid) []string {
	e := &evaluator{g: g, val: make([]float64, len(g.Cells)), state: make([]uint8, len(g.Cells)), errs: make([]error, len(g.Cells))}
	out := make([]string, len(g.Cells))
	for i, s := range g.Cells {
		if !strings.HasPrefix(s, "=") {
			out[i] = s
			continue
		}
		v, err := e.cell(i)
		if err != nil {
			out[i] = err.Error()
			continue
		}
		out[i] = formatNumber(v)
	}
	return out
}

// formatNumber writes a result as a spreadsheet shows it: no needless digits, at most twelve significant.
func formatNumber(v float64) string {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return errDiv.Error()
	}
	s := strconv.FormatFloat(v, 'g', 12, 64)
	if strings.ContainsAny(s, "e") {
		return s
	}
	return s
}

// CellValue is the cell of a table at row r, column c as shown, a formula worked out.
func CellValue(g *Grid, r, c int) string {
	if g == nil || r < 0 || c < 0 || r >= g.Rows || c >= g.Cols {
		return ""
	}
	return evalGrid(g)[r*g.Cols+c]
}

type evaluator struct {
	g     *Grid
	val   []float64
	state []uint8 // 0 not worked out, 1 being worked out, 2 done
	errs  []error
}

// cell is the value of cell i: its formula's, or its number; words count as nothing.
func (e *evaluator) cell(i int) (float64, error) {
	switch e.state[i] {
	case 1:
		return 0, errCycle
	case 2:
		return e.val[i], e.errs[i]
	}
	s := strings.TrimSpace(e.g.Cells[i])
	var v float64
	var err error
	if strings.HasPrefix(s, "=") {
		e.state[i] = 1
		p := &parser{src: []rune(s[1:]), e: e}
		v, err = p.expr()
		if err == nil {
			p.space()
			if p.i < len(p.src) {
				err = errSyn
			}
		}
	} else if s != "" {
		v, _ = strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	}
	e.val[i], e.errs[i], e.state[i] = v, err, 2
	return v, err
}

// isNumber reports whether cell i counts for COUNT and AVERAGE: a number or a formula.
func (e *evaluator) isNumber(i int) bool {
	s := strings.TrimSpace(e.g.Cells[i])
	if strings.HasPrefix(s, "=") {
		return true
	}
	_, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return err == nil
}

type parser struct {
	src []rune
	i   int
	e   *evaluator
}

func (p *parser) space() {
	for p.i < len(p.src) && unicode.IsSpace(p.src[p.i]) {
		p.i++
	}
}

func (p *parser) peek() rune {
	p.space()
	if p.i < len(p.src) {
		return p.src[p.i]
	}
	return 0
}

func (p *parser) expr() (float64, error) {
	v, err := p.term()
	for err == nil {
		switch p.peek() {
		case '+':
			p.i++
			var w float64
			w, err = p.term()
			v += w
		case '-':
			p.i++
			var w float64
			w, err = p.term()
			v -= w
		default:
			return v, nil
		}
	}
	return v, err
}

func (p *parser) term() (float64, error) {
	v, err := p.power()
	for err == nil {
		switch p.peek() {
		case '*':
			p.i++
			var w float64
			w, err = p.power()
			v *= w
		case '/':
			p.i++
			var w float64
			if w, err = p.power(); err == nil && w == 0 {
				err = errDiv
			}
			v /= w
		default:
			return v, nil
		}
	}
	return v, err
}

func (p *parser) power() (float64, error) {
	v, err := p.unary()
	if err == nil && p.peek() == '^' {
		p.i++
		var w float64
		w, err = p.power()
		v = math.Pow(v, w)
	}
	return v, err
}

func (p *parser) unary() (float64, error) {
	switch p.peek() {
	case '-':
		p.i++
		v, err := p.unary()
		return -v, err
	case '+':
		p.i++
		return p.unary()
	}
	return p.atom()
}

func (p *parser) atom() (float64, error) {
	c := p.peek()
	switch {
	case c == '(':
		p.i++
		v, err := p.expr()
		if err == nil && p.peek() != ')' {
			err = errSyn
		}
		p.i++
		return v, err
	case c >= '0' && c <= '9' || c == '.':
		start := p.i
		for p.i < len(p.src) && (p.src[p.i] >= '0' && p.src[p.i] <= '9' || p.src[p.i] == '.') {
			p.i++
		}
		v, err := strconv.ParseFloat(string(p.src[start:p.i]), 64)
		if err != nil {
			return 0, errSyn
		}
		return v, nil
	case unicode.IsLetter(c):
		start := p.i
		for p.i < len(p.src) && (unicode.IsLetter(p.src[p.i]) || unicode.IsDigit(p.src[p.i])) {
			p.i++
		}
		word := strings.ToUpper(string(p.src[start:p.i]))
		if p.peek() == '(' {
			p.i++
			return p.function(word)
		}
		r, c, ok := cellRef(word)
		if !ok {
			return 0, errSyn
		}
		return p.ref(r, c)
	}
	return 0, errSyn
}

// ref is the value of the cell at row r, column c.
func (p *parser) ref(r, c int) (float64, error) {
	g := p.e.g
	if r >= g.Rows || c >= g.Cols {
		return 0, errRef
	}
	return p.e.cell(r*g.Cols + c)
}

// cellRef reads a cell's name, such as B12: its row and column from 0.
func cellRef(s string) (row, col int, ok bool) {
	i := 0
	for i < len(s) && s[i] >= 'A' && s[i] <= 'Z' {
		col = col*26 + int(s[i]-'A'+1)
		i++
	}
	if i == 0 || i == len(s) || i > 3 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil || n < 1 {
		return 0, 0, false
	}
	return n - 1, col - 1, true
}

// CellName names the cell at row r, column c, as A1 names the first.
func CellName(r, c int) string {
	name := ""
	for c++; c > 0; c = (c - 1) / 26 {
		name = string(rune('A'+(c-1)%26)) + name
	}
	return name + strconv.Itoa(r+1)
}

// function works out a function whose opening bracket was read: its arguments are expressions or ranges.
func (p *parser) function(name string) (float64, error) {
	var vals []float64
	counted := 0
	for p.peek() != ')' {
		if p.i >= len(p.src) {
			return 0, errSyn
		}
		start := p.i
		if from, to, ok := p.rangeArg(); ok {
			g := p.e.g
			for r := min(from[0], to[0]); r <= max(from[0], to[0]); r++ {
				for c := min(from[1], to[1]); c <= max(from[1], to[1]); c++ {
					if r >= g.Rows || c >= g.Cols {
						return 0, errRef
					}
					i := r*g.Cols + c
					v, err := p.e.cell(i)
					if err != nil {
						return 0, err
					}
					if p.e.isNumber(i) {
						vals = append(vals, v)
						counted++
					}
				}
			}
		} else {
			p.i = start
			v, err := p.expr()
			if err != nil {
				return 0, err
			}
			vals = append(vals, v)
			counted++
		}
		if p.peek() == ',' || p.peek() == ';' {
			p.i++
		}
	}
	p.i++
	switch name {
	case "SUM":
		s := 0.0
		for _, v := range vals {
			s += v
		}
		return s, nil
	case "AVERAGE", "AVG", "MEAN":
		if counted == 0 {
			return 0, errDiv
		}
		s := 0.0
		for _, v := range vals {
			s += v
		}
		return s / float64(counted), nil
	case "MIN", "MAX":
		if len(vals) == 0 {
			return 0, nil
		}
		v := vals[0]
		for _, w := range vals[1:] {
			if name == "MIN" {
				v = math.Min(v, w)
			} else {
				v = math.Max(v, w)
			}
		}
		return v, nil
	case "COUNT":
		return float64(counted), nil
	case "ABS":
		if len(vals) != 1 {
			return 0, errSyn
		}
		return math.Abs(vals[0]), nil
	case "ROUND":
		if len(vals) == 0 || len(vals) > 2 {
			return 0, errSyn
		}
		d := 0.0
		if len(vals) == 2 {
			d = vals[1]
		}
		f := math.Pow(10, d)
		return math.Round(vals[0]*f) / f, nil
	}
	return 0, errSyn
}

// rangeArg reads a range such as A1:B3 if one comes next.
func (p *parser) rangeArg() (from, to [2]int, ok bool) {
	p.space()
	j := p.i
	read := func() (int, int, bool) {
		start := j
		for j < len(p.src) && (unicode.IsLetter(p.src[j]) || unicode.IsDigit(p.src[j])) {
			j++
		}
		return cellRef(strings.ToUpper(string(p.src[start:j])))
	}
	r0, c0, ok0 := read()
	if !ok0 || j >= len(p.src) || p.src[j] != ':' {
		return from, to, false
	}
	j++
	r1, c1, ok1 := read()
	if !ok1 {
		return from, to, false
	}
	p.i = j
	return [2]int{r0, c0}, [2]int{r1, c1}, true
}

// SetCell sets the words or formula of a table's cell, growing the table when the cell lies beyond it.
func (m *Map) SetCell(id ID, row, col int, text string) error {
	if row < 0 || col < 0 {
		return errRef
	}
	n := m.Node(id)
	if n == nil {
		return ErrNoNode
	}
	if n.Kind != Table {
		return ErrNotATable
	}
	return m.Update(id, func(n *Node) {
		g := n.Cells
		if g == nil {
			g = &Grid{}
			n.Cells = g
		}
		g.resize(max(g.Rows, row+1), max(g.Cols, col+1))
		g.Cells[row*g.Cols+col] = text
	})
}

// resize makes the grid rows × cols, keeping the cells that stay.
func (g *Grid) resize(rows, cols int) {
	if rows == g.Rows && cols == g.Cols && len(g.Cells) == rows*cols {
		return
	}
	cells := make([]string, rows*cols)
	for r := range min(rows, g.Rows) {
		for c := range min(cols, g.Cols) {
			cells[r*cols+c] = g.Cells[r*g.Cols+c]
		}
	}
	w := make([]float64, cols)
	copy(w, g.ColW)
	g.Rows, g.Cols, g.Cells, g.ColW = rows, cols, cells, w
}

// InsertRow puts an empty row before row at (at == Rows adds one at the end); InsertCol does so for a column.
func (g *Grid) InsertRow(at int) {
	at = min(max(at, 0), g.Rows)
	cells := make([]string, 0, (g.Rows+1)*g.Cols)
	cells = append(cells, g.Cells[:at*g.Cols]...)
	cells = append(cells, make([]string, g.Cols)...)
	cells = append(cells, g.Cells[at*g.Cols:]...)
	g.Cells, g.Rows = cells, g.Rows+1
}

func (g *Grid) InsertCol(at int) {
	at = min(max(at, 0), g.Cols)
	cells := make([]string, 0, g.Rows*(g.Cols+1))
	for r := range g.Rows {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		cells = append(cells, row[:at]...)
		cells = append(cells, "")
		cells = append(cells, row[at:]...)
	}
	w := make([]float64, 0, g.Cols+1)
	w = append(w, g.ColW[:min(at, len(g.ColW))]...)
	w = append(w, 0)
	if at < len(g.ColW) {
		w = append(w, g.ColW[at:]...)
	}
	g.Cells, g.Cols, g.ColW = cells, g.Cols+1, w
}

// DeleteRow removes row at, and DeleteCol column at; the last row or column stays.
func (g *Grid) DeleteRow(at int) {
	if g.Rows <= 1 || at < 0 || at >= g.Rows {
		return
	}
	g.Cells = append(g.Cells[:at*g.Cols:at*g.Cols], g.Cells[(at+1)*g.Cols:]...)
	g.Rows--
}

func (g *Grid) DeleteCol(at int) {
	if g.Cols <= 1 || at < 0 || at >= g.Cols {
		return
	}
	cells := make([]string, 0, g.Rows*(g.Cols-1))
	for r := range g.Rows {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		cells = append(cells, row[:at]...)
		cells = append(cells, row[at+1:]...)
	}
	if at < len(g.ColW) {
		g.ColW = append(g.ColW[:at:at], g.ColW[at+1:]...)
	}
	g.Cells, g.Cols = cells, g.Cols-1
}
