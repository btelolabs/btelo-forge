package formula

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

var argPool = []string{
	"1", "0", "-1", "2.5", "1E+300", "-1E+300", "1E-300", "\"\"", "\"x\"", "\"5\"", "\"*\"", "\"~\"", "\">1\"", "\"<>\"", "\"=\"",
	"TRUE", "FALSE", "#N/A", "#REF!", "#DIV/0!", "A1", "B2", "Z99", "XFD1048576", "A1:A5", "A1:C3", "A:A", "1:2", "A:XFD", "1:1048576",
	"Data!A1:B2", "Nope!A1", "Sheet1:Data!A1", "(A1,B2:B3)", "A1:A3 A2:C2", "{1,2;3,4}", "{1,\"a\",TRUE}", "{1}", "{#N/A,2}",
	"Named", "Missing", "INDEX(A1:C3,0,1)", "OFFSET(A1,1,1,2,2)", "INDIRECT(\"A1:A3\")", "A1:A3*2", "IF(A1:A3>1,A1:A3)", "ROW(A1:A3)",
	"", "2147483648", "1E+15", "-0.5", "1048577", "16385", "60", "2958466",
}

func randomExpr(r *rand.Rand, names []string, depth int) string {
	if depth <= 0 || r.IntN(3) == 0 {
		return argPool[r.IntN(len(argPool))]
	}
	switch r.IntN(5) {
	case 0:
		ops := []string{"+", "-", "*", "/", "^", "&", "=", "<", ">", "<=", ">=", "<>", " ", ":", ","}
		return randomExpr(r, names, depth-1) + ops[r.IntN(len(ops))] + randomExpr(r, names, depth-1)
	case 1:
		return "(" + randomExpr(r, names, depth-1) + ")" + []string{"", "%", "#"}[r.IntN(3)]
	case 2:
		return []string{"-", "+", "@"}[r.IntN(3)] + randomExpr(r, names, depth-1)
	}
	args := make([]string, r.IntN(7))
	for i := range args {
		args[i] = randomExpr(r, names, depth-1)
	}
	return names[r.IntN(len(names))] + "(" + strings.Join(args, ",") + ")"
}

// TestRandomFormulasNeverPanic throws every function at awkward arguments, through every entry point.
func TestRandomFormulasNeverPanic(t *testing.T) {
	names := make([]string, 0, len(funcs))
	for n := range funcs {
		names = append(names, n)
	}
	sort.Strings(names)
	b := newBook("Sheet1", "Data")
	b.set(0, map[string]any{"A1": 1, "A2": "x", "A3": true, "B2": Err(ErrNA), "B3": 2.5, "C1": -3, "C3": 44000})
	b.set(1, map[string]any{"A1": 9, "B2": "y"})
	b.names["NAMED"] = "Sheet1!$A$1:$A$3"
	r := rand.New(rand.NewPCG(1, 2))
	e := NewEngine(b)
	edit := Edit{Sheet: "Sheet1", Rows: true, At: 1, N: -1}
	parsed := 0
	for i := range 60000 {
		src := randomExpr(r, names, 3)
		if strings.Contains(src, "RAND") && r.IntN(2) == 0 {
			continue
		}
		x, err := Parse(src)
		if err != nil {
			continue
		}
		parsed++
		x.Eval(b, 0, 1, 1, 0, 0, false)
		x.Eval(b, 0, 1, 1, 1, 1, true)
		if i%7 == 0 {
			e.Set(Cell{0, int32(5 + i%40), int32(3 + i%20)}, x, 0, 0, i%2 == 0)
		}
		if x.Text(0, 0) != src {
			t.Fatalf("Text(0,0) of %q", src)
		}
		x.Text(3, 2)
		x.Refs(0, 1, 1, 0, 0, b)
		Adjust(src, "Sheet1", edit)
		RenameSheet(src, "Sheet1", "New Name")
		DeleteSheet(src, "Data")
	}
	if parsed < 20000 {
		t.Fatalf("only %d formulas parsed", parsed)
	}
	e.TouchAll()
	if !e.Recalc(0) {
		t.Fatal("unfinished")
	}
}

// TestRandomFormatsNeverPanic renders random format codes over awkward values.
func TestRandomFormatsNeverPanic(t *testing.T) {
	parts := []string{"0", "#", "?", ".", ",", "%", "E+", "E-", "e", "/", "@", "*", " ", "_", "\\", "\"x\"", "\"", ";", "[", "]",
		"[Red]", "[Color9]", "[>1]", "[<=0]", "[h]", "[mm]", "[ss]", "[$¥-804]", "[$-409]", "y", "yy", "yyyy", "m", "mm", "mmm", "mmmm",
		"mmmmm", "d", "dd", "ddd", "dddd", "h", "hh", "s", "ss", ".00", "AM/PM", "A/P", "aaa", "General", "$", "-", "(", ")", ":", "1", "16",
		"年", "上午/下午", "😀"}
	vals := []Value{Num(0), Num(1), Num(-1), Num(0.5), Num(1234.5678), Num(-1e-9), Num(1e300), Num(-1e300), Num(2958465.9999999),
		Num(2958466), Num(60), Num(1e-300), Str(""), Str("x"), BoolOf(true), Err(ErrNA), {}}
	r := rand.New(rand.NewPCG(3, 4))
	for range 100000 {
		var b strings.Builder
		for range r.IntN(8) + 1 {
			b.WriteString(parts[r.IntN(len(parts))])
		}
		f := ParseFormat(b.String())
		f.IsDate()
		f.IsText()
		f.IsGeneral()
		for _, v := range vals {
			f.Render(v, false)
		}
	}
	for _, x := range []float64{1e308, 5e-324, -1.7e308} {
		for w := -1; w < 25; w++ {
			General(x, w)
		}
	}
}

// TestEngineMatchesFullEvaluation edits a random graph and compares each incremental result with a fresh evaluation.
func TestEngineMatchesFullEvaluation(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	const size = 300
	name := func(i int) string { return CellName(i, 0) }
	b := newBook()
	e := NewEngine(b)
	exprs := map[Cell]*Expr{}
	// Each cell reads only cells above it in one column, so the graph has no cycle.
	formula := func(i int) string {
		switch r.IntN(4) {
		case 0:
			return fmt.Sprintf("%s+1", name(r.IntN(i)))
		case 1:
			lo := r.IntN(i)
			return fmt.Sprintf("SUM(%s:%s)", name(lo), name(lo+r.IntN(i-lo)))
		case 2:
			return fmt.Sprintf("%s*2-%s", name(r.IntN(i)), name(r.IntN(i)))
		}
		return fmt.Sprintf("MOD(%s,97)+COUNTIF(%s:%s,\">3\")", name(r.IntN(i)), name(i/2), name(i-1))
	}
	cell := func(i int) Cell {
		ro, co, _ := ParseCell(name(i))
		return Cell{0, int32(ro), int32(co)}
	}
	for i := 1; i < size; i++ {
		if r.IntN(3) == 0 {
			continue
		}
		x, err := Parse(formula(i))
		if err != nil {
			t.Fatal(err)
		}
		c := cell(i)
		exprs[c] = x
		e.Set(c, x, 0, 0, false)
	}
	for i := range size {
		if _, ok := exprs[cell(i)]; !ok {
			b.cells[cell(i)] = Num(float64(r.IntN(10)))
		}
	}
	e.TouchAll()
	e.Recalc(0)
	check := func(step int) {
		t.Helper()
		for i := range size {
			c := cell(i)
			x := exprs[c]
			if x == nil {
				continue
			}
			got := b.cells[c]
			want := x.Eval(b, 0, int(c.Row), int(c.Col), 0, 0, false)
			if got != want {
				t.Fatalf("step %d: %s = %#v, full evaluation gives %#v (%s)", step, name(i), got, want, x.Text(0, 0))
			}
		}
	}
	check(-1)
	for step := range 300 {
		i := 1 + r.IntN(size-1)
		c := cell(i)
		switch r.IntN(3) {
		case 0: // a new constant
			e.Set(c, nil, 0, 0, false)
			delete(exprs, c)
			b.cells[c] = Num(float64(r.IntN(10)))
		case 1: // a new formula
			x, _ := Parse(formula(i))
			e.Set(c, x, 0, 0, false)
			exprs[c] = x
		case 2: // a touched constant, on a budget
			if exprs[c] == nil {
				b.cells[c] = Num(float64(r.IntN(10)))
			}
		}
		e.Touch(c)
		for !e.Recalc(0) {
		}
		check(step)
	}
}
