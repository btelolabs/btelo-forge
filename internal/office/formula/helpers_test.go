package formula

import (
	"sort"
	"strings"
)

// memBook is a small in-memory workbook.
type memBook struct {
	sheets []string
	cells  map[Cell]Value
	names  map[string]string
	d1904  bool
	writes int
	// Filled by spreading an array result, as a real book does.
	spread bool
}

func newBook(sheets ...string) *memBook {
	if len(sheets) == 0 {
		sheets = []string{"Sheet1"}
	}
	return &memBook{sheets: sheets, cells: map[Cell]Value{}, names: map[string]string{}}
}

func (b *memBook) SheetIndex(name string) int {
	for i, s := range b.sheets {
		if strings.EqualFold(s, name) {
			return i
		}
	}
	return -1
}

func (b *memBook) SheetName(i int) string { return b.sheets[i] }

func (b *memBook) Value(sheet, row, col int) Value {
	return b.cells[Cell{int32(sheet), int32(row), int32(col)}]
}

func (b *memBook) Each(sheet, r1, c1, r2, c2 int, fn func(row, col int, v Value) bool) {
	var hits []Cell
	for c := range b.cells {
		if int(c.Sheet) == sheet && int(c.Row) >= r1 && int(c.Row) <= r2 && int(c.Col) >= c1 && int(c.Col) <= c2 {
			hits = append(hits, c)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Row != hits[j].Row {
			return hits[i].Row < hits[j].Row
		}
		return hits[i].Col < hits[j].Col
	})
	for _, c := range hits {
		if !fn(int(c.Row), int(c.Col), b.cells[c]) {
			return
		}
	}
}

func (b *memBook) Bounds(sheet int) (rows, cols int) {
	for c := range b.cells {
		if int(c.Sheet) == sheet {
			rows = max(rows, int(c.Row)+1)
			cols = max(cols, int(c.Col)+1)
		}
	}
	return rows, cols
}

func (b *memBook) Name(name string, sheet int) string {
	if t, ok := b.names[b.sheets[sheet]+"!"+strings.ToUpper(name)]; ok {
		return t
	}
	return b.names[strings.ToUpper(name)]
}

func (b *memBook) Date1904() bool { return b.d1904 }

func (b *memBook) SetResult(c Cell, v Value) {
	b.writes++
	if v.Kind == Array && b.spread {
		g := v.Arr
		for i := range g.Rows {
			for j := range g.Cols {
				b.cells[Cell{c.Sheet, c.Row + int32(i), c.Col + int32(j)}] = g.V[i*g.Cols+j]
			}
		}
		return
	}
	b.cells[c] = v
}

// set fills cells from a compact description: "A1": value.
func (b *memBook) set(sheet int, vals map[string]any) {
	for name, v := range vals {
		r, c, ok := ParseCell(name)
		if !ok {
			panic("bad cell " + name)
		}
		b.cells[Cell{int32(sheet), int32(r), int32(c)}] = toValue(v)
	}
}

func toValue(v any) Value {
	switch x := v.(type) {
	case nil:
		return Value{}
	case int:
		return Num(float64(x))
	case float64:
		return Num(x)
	case string:
		return Str(x)
	case bool:
		return BoolOf(x)
	case Value:
		return x
	}
	panic("bad value")
}

func cellOf(name string) Cell {
	r, c, ok := ParseCell(name)
	if !ok {
		panic("bad cell " + name)
	}
	return Cell{0, int32(r), int32(c)}
}
