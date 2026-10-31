package formula

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// put registers a formula at a cell of sheet 0.
func put(t *testing.T, e *Engine, name, src string) {
	t.Helper()
	x, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	e.Set(cellOf(name), x, 0, 0, false)
}

func num(b *memBook, name string) float64 {
	c := cellOf(name)
	return b.cells[c].Num
}

func TestEngineBasics(t *testing.T) {
	b := newBook()
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 1, "A2": 2})
	put(t, e, "B1", "A1+A2")
	put(t, e, "C1", "B1*10")
	put(t, e, "D1", "SUM(B1:C1)")
	if e.Count() != 3 {
		t.Fatalf("Count = %d", e.Count())
	}
	if e.Pending() {
		t.Fatal("Set must not mark anything dirty")
	}
	e.TouchAll()
	if !e.Pending() {
		t.Fatal("TouchAll leaves work")
	}
	if !e.Recalc(0) {
		t.Fatal("Recalc(0) finishes")
	}
	if num(b, "B1") != 3 || num(b, "C1") != 30 || num(b, "D1") != 33 {
		t.Fatalf("first pass: %v %v %v", num(b, "B1"), num(b, "C1"), num(b, "D1"))
	}
	if e.Pending() {
		t.Fatal("nothing left")
	}
	b.writes = 0
	b.set(0, map[string]any{"A2": 5})
	e.Touch(cellOf("A2"))
	e.Recalc(0)
	if num(b, "D1") != 66 {
		t.Fatalf("after a change: %v", num(b, "D1"))
	}
	if b.writes != 3 {
		t.Fatalf("recalculated %d formulas", b.writes)
	}
	// An unrelated cell recalculates nothing.
	b.writes = 0
	b.set(0, map[string]any{"Z9": 5})
	e.Touch(cellOf("Z9"))
	e.Recalc(0)
	if b.writes != 0 {
		t.Fatalf("unrelated touch wrote %d", b.writes)
	}
	// Replacing a formula cuts the old dependency.
	put(t, e, "B1", "A1*100")
	e.Touch(cellOf("B1"))
	e.Recalc(0)
	if num(b, "D1") != 1100 {
		t.Fatalf("after replacing: %v", num(b, "D1"))
	}
	b.writes = 0
	b.set(0, map[string]any{"A2": 7})
	e.Touch(cellOf("A2"))
	e.Recalc(0)
	if b.writes != 0 {
		t.Fatalf("a dropped dependency still fired: %d", b.writes)
	}
	// Removing.
	e.Set(cellOf("D1"), nil, 0, 0, false)
	if e.Count() != 2 {
		t.Fatalf("Count after removal = %d", e.Count())
	}
	e.Reset()
	if e.Count() != 0 || e.Pending() {
		t.Fatal("Reset")
	}
}

func TestEngineRanges(t *testing.T) {
	b := newBook("Sheet1", "Other")
	e := NewEngine(b)
	b.set(0, map[string]any{"A100": 1, "C5000": 2})
	b.set(1, map[string]any{"B2": 3})
	put(t, e, "F1", "SUM(A:A)")
	put(t, e, "F2", "SUM(100:100)")
	put(t, e, "F3", "SUM(A1:C5000)")
	put(t, e, "F4", "SUM(Other!A1:C3)")
	put(t, e, "F5", "SUM(Sheet1:Other!C5000)")
	put(t, e, "F6", "SUM(B1:B3)")
	e.TouchAll()
	e.Recalc(0)
	if num(b, "F1") != 1 || num(b, "F2") != 1 || num(b, "F3") != 3 || num(b, "F4") != 3 || num(b, "F5") != 2 {
		t.Fatalf("ranges: %v %v %v %v %v", num(b, "F1"), num(b, "F2"), num(b, "F3"), num(b, "F4"), num(b, "F5"))
	}
	check := func(c Cell, want int) {
		t.Helper()
		b.writes = 0
		e.Touch(c)
		e.Recalc(0)
		if b.writes != want {
			t.Errorf("touch %v recalculated %d, want %d", c, b.writes, want)
		}
	}
	check(cellOf("A100"), 3)                    // F1 F2 F3
	check(cellOf("A5001"), 1)                   // F1 only
	check(cellOf("C5000"), 2)                   // F3 F5
	check(cellOf("B2"), 2)                      // F3 F6
	check(cellOf("X100"), 1)                    // F2
	check(Cell{Sheet: 1, Row: 1, Col: 1}, 1)    // F4
	check(Cell{Sheet: 1, Row: 4999, Col: 2}, 1) // F5
	check(cellOf("D5000"), 0)
}

func TestEngineUnknownStaysFrozen(t *testing.T) {
	b := newBook()
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 1, "B1": 42})
	put(t, e, "B1", "NOSUCHFN(A1)")
	put(t, e, "C1", "B1+1")
	e.TouchAll()
	e.Recalc(0)
	if num(b, "B1") != 42 || num(b, "C1") != 43 {
		t.Fatalf("frozen: %v %v", num(b, "B1"), num(b, "C1"))
	}
	b.writes = 0
	e.Touch(cellOf("A1"))
	e.Recalc(0)
	if b.writes != 0 {
		t.Fatalf("frozen formula must not depend on anything: %d", b.writes)
	}
	e.Touch(cellOf("B1"))
	e.Recalc(0)
	if b.writes != 1 {
		t.Fatalf("a dependent of a frozen cell still recalculates: %d", b.writes)
	}
	if e.Count() != 2 {
		t.Fatalf("Count = %d", e.Count())
	}
}

func TestEngineCircular(t *testing.T) {
	b := newBook()
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 5, "B1": 9, "C1": 9, "D1": 9, "E1": 9})
	put(t, e, "B1", "C1+1")
	put(t, e, "C1", "B1+1")
	put(t, e, "D1", "B1+A1") // downstream of the cycle
	put(t, e, "E1", "E1+1")  // self
	put(t, e, "F1", "SUM(F1:F3)")
	e.TouchAll()
	e.Recalc(0)
	want := []Cell{cellOf("B1"), cellOf("C1"), cellOf("E1"), cellOf("F1")}
	if got := e.Circular(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Circular = %v", got)
	}
	if num(b, "B1") != 0 || num(b, "C1") != 0 || num(b, "E1") != 0 || num(b, "F1") != 0 {
		t.Fatal("cycle members become 0")
	}
	if num(b, "D1") != 5 {
		t.Fatalf("downstream: %v", num(b, "D1"))
	}
	// Breaking the cycle clears the report.
	put(t, e, "C1", "A1")
	put(t, e, "E1", "1")
	put(t, e, "F1", "2")
	e.TouchAll()
	e.Recalc(0)
	if len(e.Circular()) != 0 {
		t.Fatalf("still circular: %v", e.Circular())
	}
	if num(b, "D1") != 11 {
		t.Fatalf("D1 = %v", num(b, "D1"))
	}
}

func TestEngineLongChain(t *testing.T) {
	const n = 200000
	b := newBook()
	e := NewEngine(b)
	b.cells[Cell{0, 0, 0}] = Num(1)
	for i := 1; i < n; i++ {
		x, err := Parse("A" + fmt.Sprint(i) + "+1")
		if err != nil {
			t.Fatal(err)
		}
		e.Set(Cell{0, int32(i), 0}, x, 0, 0, false)
	}
	e.TouchAll()
	if !e.Recalc(0) {
		t.Fatal("unfinished")
	}
	if got := b.cells[Cell{0, n - 1, 0}].Num; got != n {
		t.Fatalf("end of the chain = %v", got)
	}
	b.cells[Cell{0, 0, 0}] = Num(2)
	e.Touch(Cell{0, 0, 0})
	e.Recalc(0)
	if got := b.cells[Cell{0, n - 1, 0}].Num; got != n+1 {
		t.Fatalf("end of the chain after a touch = %v", got)
	}
	// Close it into a cycle of the whole length: the search must not recurse either.
	x, _ := Parse(fmt.Sprintf("A%d+1", n))
	e.Set(Cell{0, 0, 0}, x, 0, 0, false)
	e.Touch(Cell{0, 0, 0})
	e.Recalc(0)
	if len(e.Circular()) != maxCircular {
		t.Fatalf("Circular has %d cells", len(e.Circular()))
	}
	if got := b.cells[Cell{0, n - 1, 0}].Num; got != 0 {
		t.Fatalf("cycle member = %v", got)
	}
}

func TestEngineBudget(t *testing.T) {
	const n = 50000
	b := newBook()
	e := NewEngine(b)
	b.cells[Cell{0, 0, 0}] = Num(1)
	shared, _ := Parse("A1+1")
	for i := 1; i < n; i++ {
		e.Set(Cell{0, int32(i), 0}, shared, i-1, 0, false)
	}
	e.TouchAll()
	steps := 0
	for !e.Recalc(time.Microsecond) {
		steps++
		if !e.Pending() {
			t.Fatal("unfinished work must be pending")
		}
		if steps == 3 {
			// A touch in the middle merges.
			b.cells[Cell{0, 0, 0}] = Num(5)
			e.Touch(Cell{0, 0, 0})
		}
		if steps > 1000000 {
			t.Fatal("no progress")
		}
	}
	if steps == 0 {
		t.Log("the budget was never hit")
	}
	if got := b.cells[Cell{0, n - 1, 0}].Num; got != n+4 {
		t.Fatalf("end after a budgeted pass = %v", got)
	}
}

func TestEngineVolatile(t *testing.T) {
	b := newBook()
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 1})
	put(t, e, "B1", "COUNT(RAND())")
	put(t, e, "C1", "B1+1")
	put(t, e, "D1", "A1+1")
	e.TouchAll()
	e.Recalc(0)
	b.writes = 0
	if !e.Recalc(0) || b.writes != 0 {
		t.Fatalf("no work, no pass: %d writes", b.writes)
	}
	e.Refresh()
	if !e.Pending() {
		t.Fatal("Refresh leaves work")
	}
	e.Recalc(0)
	if b.writes != 2 {
		t.Fatalf("Refresh recalculates volatile and dependents: %d", b.writes)
	}
	b.writes = 0
	e.Touch(cellOf("A1"))
	e.Recalc(0)
	if b.writes != 3 {
		t.Fatalf("any pass takes in volatile formulas: %d", b.writes)
	}
}

func TestEngineDynamicReferences(t *testing.T) {
	b := newBook()
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 2})
	// C1 reaches B1 only through INDIRECT, so nothing orders them statically.
	put(t, e, "C1", `INDIRECT("B1")+1`)
	put(t, e, "B1", "A1*2")
	e.TouchAll()
	e.Recalc(0)
	if num(b, "C1") != 5 {
		t.Fatalf("on-demand read: %v", num(b, "C1"))
	}
	// A dirty cell inside an OFFSET range.
	put(t, e, "D5", "SUM(OFFSET(E1,0,0,3,1))")
	put(t, e, "E1", "A1")
	put(t, e, "E2", "A1+1")
	put(t, e, "E3", "E2+1")
	e.TouchAll()
	e.Recalc(0)
	if num(b, "D5") != 2+3+4 {
		t.Fatalf("on-demand range: %v", num(b, "D5"))
	}
	// A cycle through a dynamic reference.
	put(t, e, "G1", `INDIRECT("G2")+1`)
	put(t, e, "G2", `INDIRECT("G1")+1`)
	e.TouchAll()
	e.Recalc(0)
	c := e.Circular()
	if len(c) == 0 {
		t.Fatal("dynamic cycle not reported")
	}
	if num(b, "G1") != 0 && num(b, "G2") != 0 {
		t.Fatalf("dynamic cycle values: %v %v", num(b, "G1"), num(b, "G2"))
	}
}

func TestEngineArraySpill(t *testing.T) {
	b := newBook()
	b.spread = true
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 1, "A2": 2, "A3": 3})
	x, _ := Parse("A1:A3*2")
	e.Set(cellOf("B1"), x, 0, 0, true)
	put(t, e, "C1", "B3+1")
	e.TouchAll()
	e.Recalc(0)
	if num(b, "B3") != 6 || num(b, "C1") != 7 {
		t.Fatalf("spill: %v %v", num(b, "B3"), num(b, "C1"))
	}
	b.set(0, map[string]any{"A3": 10})
	e.Touch(cellOf("A3"))
	e.Recalc(0)
	if num(b, "C1") != 21 {
		t.Fatalf("dependent of a spilled cell: %v", num(b, "C1"))
	}
}

func TestEngineSubtotalSkipsSubtotals(t *testing.T) {
	b := newBook()
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 1, "A2": 2})
	put(t, e, "A3", "SUBTOTAL(9,A1:A2)")
	put(t, e, "A4", "SUBTOTAL(9,A1:A3)")
	e.TouchAll()
	e.Recalc(0)
	if num(b, "A4") != 3 {
		t.Fatalf("a subtotal counts no other subtotal: %v", num(b, "A4"))
	}
}

func TestEngineLookupIndex(t *testing.T) {
	const n = 100000
	b := newBook("Sheet1", "Table")
	e := NewEngine(b)
	for i := range n {
		b.cells[Cell{1, int32(i), 0}] = Num(float64(i))
		b.cells[Cell{1, int32(i), 1}] = Str(fmt.Sprint("v", i))
	}
	fast := &fastBook{memBook: b, rows: n}
	e = NewEngine(fast)
	x, _ := Parse("VLOOKUP(A1,Table!$A$1:$B$100000,2,FALSE)")
	for i := range n {
		b.cells[Cell{0, int32(i), 0}] = Num(float64(n - 1 - i))
		e.Set(Cell{0, int32(i), 1}, x, i, 0, false)
	}
	e.TouchAll()
	start := time.Now()
	e.Recalc(0)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("100k lookups took %v", d)
	}
	if got := b.cells[Cell{0, 0, 1}].Str; got != fmt.Sprint("v", n-1) {
		t.Fatalf("first lookup: %q", got)
	}
	// The index must not outlive a change to its column.
	b.cells[Cell{1, 7, 0}] = Num(-5)
	e.Touch(Cell{1, 7, 0})
	b.cells[Cell{0, 0, 0}] = Num(-5)
	e.Touch(Cell{0, 0, 0})
	e.Recalc(0)
	if got := b.cells[Cell{0, 0, 1}].Str; got != "v7" {
		t.Fatalf("after a change: %q", got)
	}
}

func TestEngineIndexFollowsResults(t *testing.T) {
	b := newBook()
	e := NewEngine(b)
	for i := range 100 {
		b.cells[Cell{0, int32(i), 0}] = Num(float64(i))
	}
	shared, _ := Parse("A1*2")
	for i := range 100 {
		e.Set(Cell{0, int32(i), 1}, shared, i, 0, false)
	}
	put(t, e, "D1", "MATCH(20,B1:B100,0)")
	put(t, e, "D2", "MATCH(22,B1:B100,0)")
	e.TouchAll()
	e.Recalc(0)
	if num(b, "D1") != 11 || num(b, "D2") != 12 {
		t.Fatalf("matches on formula results: %v %v", num(b, "D1"), num(b, "D2"))
	}
	b.cells[Cell{0, 5, 0}] = Num(10) // B6 is now 20, ahead of B11
	e.Touch(Cell{0, 5, 0})
	e.Recalc(0)
	if num(b, "D1") != 6 {
		t.Fatalf("after a recalculated column: %v", num(b, "D1"))
	}
}

func TestEngineChurn(t *testing.T) {
	b := newBook()
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 1})
	x, _ := Parse("A1+1")
	// Enough replacements to trigger compaction of the graph.
	c := cellOf("B1")
	for range 200000 {
		e.Set(c, x, 0, 0, false)
	}
	if e.Count() != 1 {
		t.Fatalf("Count = %d", e.Count())
	}
	if len(e.fs) > 100000 {
		t.Fatalf("records are not reclaimed: %d", len(e.fs))
	}
	e.Touch(cellOf("A1"))
	e.Recalc(0)
	if num(b, "B1") != 2 {
		t.Fatalf("after churn: %v", num(b, "B1"))
	}
}

func TestEngineNamedRange(t *testing.T) {
	b := newBook()
	b.names["INPUT"] = "Sheet1!$A$1"
	e := NewEngine(b)
	b.set(0, map[string]any{"A1": 4})
	put(t, e, "B1", "Input*2")
	e.TouchAll()
	e.Recalc(0)
	if num(b, "B1") != 8 {
		t.Fatalf("through a name: %v", num(b, "B1"))
	}
	b.set(0, map[string]any{"A1": 5})
	e.Touch(cellOf("A1"))
	e.Recalc(0)
	if num(b, "B1") != 10 {
		t.Fatalf("a name carries the dependency: %v", num(b, "B1"))
	}
}

// fastBook gives the large test a column read without scanning every cell of the map.
type fastBook struct {
	*memBook
	rows int
}

func (f *fastBook) Each(sheet, r1, c1, r2, c2 int, fn func(row, col int, v Value) bool) {
	r2 = min(r2, f.rows-1)
	for r := r1; r <= r2; r++ {
		for c := c1; c <= c2; c++ {
			if v, ok := f.cells[Cell{int32(sheet), int32(r), int32(c)}]; ok && !fn(r, c, v) {
				return
			}
		}
	}
}

func (f *fastBook) Bounds(int) (int, int) { return f.rows, 2 }
