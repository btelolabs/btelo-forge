package formula

import (
	"reflect"
	"testing"
)

func TestText(t *testing.T) {
	cases := []struct {
		src    string
		dr, dc int
		want   string
	}{
		{"A1+$B$2", 1, 1, "B2+$B$2"},
		{"A$1+$A1", 1, 1, "B$1+$A2"},
		{"SUM(A1:B2)", 2, 0, "SUM(A3:B4)"},
		{"SUM( a1 ,  B2 )", 1, 0, "SUM( A2 ,  B3 )"},
		{"\"A1\"&A1", 1, 0, "\"A1\"&A2"},
		{"Sheet1!A1", 0, 1, "Sheet1!B1"},
		{"'My Sheet'!A1", 0, 1, "'My Sheet'!B1"},
		{"Sheet1:Sheet3!A1", 1, 0, "Sheet1:Sheet3!A2"},
		{"A1", -1, 0, "#REF!"},
		{"Sheet1!A1+1", 0, -1, "Sheet1!#REF!+1"},
		{"SUM(A1:A5)", 0, -1, "SUM(#REF!)"},
		{"A:A", 1, 1, "B:B"},
		{"1:1", 1, 1, "2:2"},
		{"$A:$A", 1, 1, "$A:$A"},
		{"_xlfn.STDEV.S(A1)", 1, 0, "_xlfn.STDEV.S(A2)"},
		{"lower(a1)", 1, 0, "lower(A2)"},
		{"Name+A1", 1, 0, "Name+A2"},
		{"XFD1", 0, 1, "#REF!"},
		{"A1048576", 1, 0, "#REF!"},
	}
	for _, c := range cases {
		x, err := Parse(c.src)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.src, err)
		}
		if got := x.Text(c.dr, c.dc); got != c.want {
			t.Errorf("Text(%q,%d,%d) = %q, want %q", c.src, c.dr, c.dc, got, c.want)
		}
	}
}

func TestRefs(t *testing.T) {
	b := newBook("Sheet1", "Data", "Third")
	b.names["BASES"] = "Data!$A$1:$A$3"
	b.names["CHAIN"] = "Bases"
	b.names["SELF"] = "Self"
	cases := []struct {
		src  string
		want []Area
	}{
		{"A1+B2", []Area{{0, 0, 0, 0, 0}, {0, 1, 1, 1, 1}}},
		{"SUM(A:A)", []Area{{0, 0, 0, MaxRows - 1, 0}}},
		{"SUM(2:3)", []Area{{0, 1, 0, 2, MaxCols - 1}}},
		{"Data!B2", []Area{{1, 1, 1, 1, 1}}},
		{"Nope!B2", nil},
		{"SUM(Sheet1:Third!A1)", []Area{{0, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {2, 0, 0, 0, 0}}},
		{"Chain", []Area{{1, 0, 0, 2, 0}}},
		{"Self", nil},
		{"Unknown", nil},
		{"A1:INDEX(B1:B2,1)", []Area{{0, 0, 0, 0, 0}, {0, 0, 1, 1, 1}}},
		{"A1 : B3", []Area{{0, 0, 0, 0, 0}, {0, 2, 1, 2, 1}, {0, 0, 0, 2, 1}}},
	}
	for _, c := range cases {
		x, err := Parse(c.src)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.src, err)
		}
		got := x.Refs(0, 0, 0, 0, 0, b)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Refs(%q) = %v, want %v", c.src, got, c.want)
		}
	}
	x, _ := Parse("A1+$B$1")
	got := x.Refs(0, 5, 5, 2, 3, b)
	if !reflect.DeepEqual(got, []Area{{0, 2, 3, 2, 3}, {0, 0, 1, 0, 1}}) {
		t.Errorf("shifted refs: %v", got)
	}
}

func TestAdjust(t *testing.T) {
	ins := func(at, n int) Edit { return Edit{Sheet: "Sheet1", Rows: true, At: at, N: n} }
	col := func(at, n int) Edit { return Edit{Sheet: "Sheet1", At: at, N: n} }
	cases := []struct {
		src  string
		own  string
		e    Edit
		want string
	}{
		{"A1", "Sheet1", ins(0, 1), "A2"},
		{"A5+1", "Sheet1", ins(2, 2), "A7+1"},
		{"A1", "Sheet1", ins(1, 2), "A1"},
		{"$A$5", "Sheet1", ins(2, 1), "$A$6"},
		{"A5", "Sheet2", ins(2, 1), "A5"},
		{"Sheet1!A5", "Sheet2", ins(2, 1), "Sheet1!A6"},
		{"sheet1!A5", "Sheet2", ins(2, 1), "sheet1!A6"},
		{"'Sheet1'!A5", "Sheet2", ins(2, 1), "'Sheet1'!A6"},
		{"Data!A5", "Sheet1", ins(2, 1), "Data!A5"},
		{"SUM(A1:A5)", "Sheet1", ins(2, 3), "SUM(A1:A8)"}, // grows inside
		{"SUM(A1:A5)", "Sheet1", ins(0, 1), "SUM(A2:A6)"}, // before: moves
		{"SUM(A1:A5)", "Sheet1", ins(5, 1), "SUM(A1:A5)"}, // right after: no growth
		{"SUM(A2:A5)", "Sheet1", ins(1, 1), "SUM(A3:A6)"}, // at the first row: moves
		{"SUM(A2:A5)", "Sheet1", ins(4, 1), "SUM(A2:A6)"}, // at the last row: grows
		{"A5", "Sheet1", ins(4, -1), "#REF!"},
		{"A5+B7", "Sheet1", ins(4, -1), "#REF!+B6"},
		{"Sheet1!A5", "Data", ins(4, -1), "Sheet1!#REF!"},
		{"A5", "Sheet1", ins(1, -2), "A3"},
		{"A3", "Sheet1", ins(4, -1), "A3"},
		{"SUM(A1:A5)", "Sheet1", ins(1, -2), "SUM(A1:A3)"},
		{"SUM(A1:A5)", "Sheet1", ins(0, -2), "SUM(A1:A3)"},
		{"SUM(A1:A5)", "Sheet1", ins(3, -2), "SUM(A1:A3)"},
		{"SUM(A1:A5)", "Sheet1", ins(3, -9), "SUM(A1:A3)"},
		{"SUM(A2:A5)", "Sheet1", ins(0, -9), "SUM(#REF!)"},
		{"SUM(A2:A5)", "Sheet1", ins(1, -4), "SUM(#REF!)"},
		{"SUM(A:A)", "Sheet1", ins(1, 1), "SUM(A:A)"},
		{"SUM(A:A)", "Sheet1", col(0, 1), "SUM(B:B)"},
		{"SUM(B:D)", "Sheet1", col(2, 1), "SUM(B:E)"},
		{"SUM(B:D)", "Sheet1", col(1, -3), "SUM(#REF!)"},
		{"SUM(2:3)", "Sheet1", col(0, 1), "SUM(2:3)"},
		{"SUM(2:3)", "Sheet1", ins(0, 1), "SUM(3:4)"},
		{"A1+$C1", "Sheet1", col(1, 1), "A1+$D1"},
		{"C1:A1", "Sheet1", col(1, 1), "A1:D1"},
		{"\"A5\"&A5&Name", "Sheet1", ins(0, 1), "\"A5\"&A6&Name"},
		{"  A5  +  1", "Sheet1", ins(0, 1), "  A6  +  1"},
		{"lower(a5)", "Sheet1", ins(0, 1), "lower(A6)"},
		{"not a formula (", "Sheet1", ins(0, 1), "not a formula ("},
		{"Sheet1:Sheet3!A5", "Sheet1", ins(0, 1), "Sheet1:Sheet3!A5"},
		{"A1048576", "Sheet1", ins(0, 1), "#REF!"},
		{"SUM(A1:A1048576)", "Sheet1", ins(0, 1), "SUM(A2:A1048576)"},
	}
	for _, c := range cases {
		got, changed := Adjust(c.src, c.own, c.e)
		if got != c.want || changed != (c.want != c.src) {
			t.Errorf("Adjust(%q,%v) = %q,%v, want %q", c.src, c.e, got, changed, c.want)
		}
	}
}

func TestRenameAndDeleteSheet(t *testing.T) {
	ren := []struct{ src, from, to, want string }{
		{"Sheet1!A1+1", "Sheet1", "Data", "Data!A1+1"},
		{"Sheet1!A1", "Sheet1", "My Data", "'My Data'!A1"},
		{"'My Data'!A1", "My Data", "Plain", "Plain!A1"},
		{"sheet1!A1:B2", "Sheet1", "It's", "'It''s'!A1:B2"},
		{"Sheet1:Sheet3!A1", "Sheet3", "End", "Sheet1:End!A1"},
		{"Sheet1:Sheet3!A1", "Sheet1", "My Start", "'My Start:Sheet3'!A1"},
		{"Sheet2!A1", "Sheet1", "X", "Sheet2!A1"},
		{"A1", "Sheet1", "X", "A1"},
		{"\"Sheet1!A1\"", "Sheet1", "X", "\"Sheet1!A1\""},
		{"Sheet1!Name", "Sheet1", "X", "X!Name"},
		{"Sheet1!#REF!", "Sheet1", "X", "X!#REF!"},
		{"bad (", "Sheet1", "X", "bad ("},
	}
	for _, c := range ren {
		got, changed := RenameSheet(c.src, c.from, c.to)
		if got != c.want || changed != (c.want != c.src) {
			t.Errorf("RenameSheet(%q) = %q,%v, want %q", c.src, got, changed, c.want)
		}
	}
	del := []struct{ src, name, want string }{
		{"Data!A1+A2", "Data", "#REF!+A2"},
		{"SUM('My Data'!A1:B2)", "my data", "SUM(#REF!)"},
		{"Sheet1:Data!A1", "Data", "#REF!"},
		{"Other!A1", "Data", "Other!A1"},
	}
	for _, c := range del {
		got, changed := DeleteSheet(c.src, c.name)
		if got != c.want || changed != (c.want != c.src) {
			t.Errorf("DeleteSheet(%q) = %q,%v, want %q", c.src, got, changed, c.want)
		}
	}
	quote := map[string]string{
		"Sheet1": "Sheet1", "My Sheet": "'My Sheet'", "2020": "'2020'", "A1": "'A1'", "It's": "'It''s'",
		"销售": "销售", "a-b": "'a-b'", "R1C1": "'R1C1'", "RC": "'RC'", "": "''", "a.b": "a.b", "Data_1": "Data_1", "XFD1": "'XFD1'",
	}
	for in, want := range quote {
		if got := QuoteSheet(in); got != want {
			t.Errorf("QuoteSheet(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAdjustRange(t *testing.T) {
	rows := func(at, n int) Edit { return Edit{Rows: true, At: at, N: n} }
	cols := func(at, n int) Edit { return Edit{At: at, N: n} }
	cases := []struct {
		in   string
		e    Edit
		want string
	}{
		{"A1:B2", rows(0, 1), "A2:B3"},
		{"A1:B2", rows(1, 1), "A1:B3"},
		{"A1:B2", rows(2, 1), "A1:B2"},
		{"A1:B2", rows(0, -2), ""},
		{"A1:B2 D4", rows(0, -2), "D2"},
		{"A1:B5 D4:E4", rows(3, -1), "A1:B4"},
		{"A1", cols(0, 2), "C1"},
		{"A:B", rows(1, 1), "A:B"},
		{"A:B", cols(0, 1), "B:C"},
		{"1:2", cols(0, 1), "1:2"},
		{"1:2", rows(0, 1), "2:3"},
		{"", rows(0, 1), ""},
		{"$A$1", rows(9, 1), "$A$1"},
	}
	for _, c := range cases {
		if got := AdjustRange(c.in, c.e); got != c.want {
			t.Errorf("AdjustRange(%q,%v) = %q, want %q", c.in, c.e, got, c.want)
		}
	}
}

func TestAddressing(t *testing.T) {
	for c, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 701: "ZZ", 702: "AAA", 16383: "XFD"} {
		if got := ColName(c); got != want {
			t.Errorf("ColName(%d) = %q", c, got)
		}
		if got, ok := ParseCol(want); !ok || got != c {
			t.Errorf("ParseCol(%q) = %d,%v", want, got, ok)
		}
	}
	if got, ok := ParseCol("aa"); !ok || got != 26 {
		t.Errorf("ParseCol(aa) = %d", got)
	}
	for _, bad := range []string{"", "XFE", "AAAA", "A1"} {
		if _, ok := ParseCol(bad); ok {
			t.Errorf("ParseCol(%q) should fail", bad)
		}
	}
	if CellName(0, 0) != "A1" || CellName(9, 27) != "AB10" {
		t.Error("CellName")
	}
	if r, c, ok := ParseCell("$AB$10"); !ok || r != 9 || c != 27 {
		t.Errorf("ParseCell: %d %d %v", r, c, ok)
	}
	for _, bad := range []string{"", "A", "1", "A0", "A1048577", "XFE1", "A1:B2", "$A$", "A1x"} {
		if _, _, ok := ParseCell(bad); ok {
			t.Errorf("ParseCell(%q) should fail", bad)
		}
	}
	areas := map[string]Area{
		"A1":       {0, 0, 0, 0, 0},
		"A1:C3":    {0, 0, 0, 2, 2},
		"C3:A1":    {0, 0, 0, 2, 2},
		"$A$1:$B2": {0, 0, 0, 1, 1},
		"A:B":      {0, 0, 0, MaxRows - 1, 1},
		"$A:$A":    {0, 0, 0, MaxRows - 1, 0},
		"1:3":      {0, 0, 0, 2, MaxCols - 1},
	}
	for s, want := range areas {
		if got, ok := ParseArea(s); !ok || got != want {
			t.Errorf("ParseArea(%q) = %v,%v", s, got, ok)
		}
	}
	for _, bad := range []string{"", "A", "A1:", ":A1", "A1:B", "1:A", "A1:B2:C3"} {
		if _, ok := ParseArea(bad); ok {
			t.Errorf("ParseArea(%q) should fail", bad)
		}
	}
	names := map[Area]string{
		{R1: 0, C1: 0, R2: 0, C2: 0}:           "A1",
		{R1: 0, C1: 0, R2: 2, C2: 2}:           "A1:C3",
		{R1: 0, C1: 1, R2: MaxRows - 1, C2: 2}: "B:C",
		{R1: 1, C1: 0, R2: 2, C2: MaxCols - 1}: "2:3",
		{Sheet: 5, R1: 1, C1: 1, R2: 1, C2: 1}: "B2",
	}
	for a, want := range names {
		if got := AreaName(a); got != want {
			t.Errorf("AreaName(%v) = %q, want %q", a, got, want)
		}
	}
}
