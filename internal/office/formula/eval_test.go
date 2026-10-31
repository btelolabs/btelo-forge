package formula

import (
	"math"
	"strings"
	"testing"
)

func sameValue(got Value, want any) bool {
	w := toValue(want)
	if got.Kind != w.Kind {
		return false
	}
	switch got.Kind {
	case Number:
		if got.Num == w.Num {
			return true
		}
		return math.Abs(got.Num-w.Num) <= 1e-9*math.Max(1, math.Abs(w.Num))
	case Text, Error:
		return got.Str == w.Str
	case Bool:
		return got.Num == w.Num
	}
	return true
}

// evalAt parses and evaluates a formula at a cell of a book.
func evalAt(t *testing.T, b *memBook, src, at string, array bool) Value {
	t.Helper()
	x, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	c := cellOf(at)
	return x.Eval(b, int(c.Sheet), int(c.Row), int(c.Col), 0, 0, array)
}

func dataBook() *memBook {
	b := newBook("Sheet1", "Data", "My Sheet")
	b.set(0, map[string]any{
		"A1": 1, "A2": 2, "A3": 3, "A4": 4, "A5": 5,
		"B1": 10, "B2": 20, "B3": 30, "B4": 40, "B5": 50,
		"C1": "apple", "C2": "banana", "C3": "Cherry", "C4": "date", "C5": "apple pie",
		"D1": true, "D2": "x", "D3": 7, "D5": "7",
		"E1": 2.5, "E2": -1.5, "E3": 0, "F1": Err(ErrNA), "F2": 1,
	})
	b.set(1, map[string]any{"A1": 100, "A2": 200, "B1": "north", "B2": "south"})
	b.set(2, map[string]any{"A1": 1000, "B7": 7})
	return b
}

func TestEvalTable(t *testing.T) {
	cases := []struct {
		src  string
		want any
	}{
		// Operators and precedence.
		{"1+2*3", 7},
		{"(1+2)*3", 9},
		{"-2^2", 4},
		{"2^3^2", 64},
		{"2^-1", 0.5},
		{"-2^-2", 0.25},
		{"50%", 0.5},
		{"2^50%", math.Sqrt2},
		{"-50%", -0.5},
		{"1+2&3", "33"},
		{"1&2+3", "15"},
		{"1=1", true},
		{"1+1=2", true},
		{"\"a\"&\"b\"=\"AB\"", true},
		{"1<\"a\"", true},
		{"\"z\"<TRUE", true},
		{"FALSE>\"zzz\"", true},
		{"\"a\"=\"A\"", true},
		{"\"a\"<>\"b\"", true},
		{"2>=2", true},
		{"2<=1", false},
		{"\"3\"+1", 4},
		{"\"$1,000\"+1", 1001},
		{"\"50%\"*2", 1},
		{"TRUE+TRUE", 2},
		{"Z99+1", 1},
		{"Z99&\"\"", ""},
		{"Z99=0", true},
		{"Z99=\"\"", true},
		{"\"x\"+1", Err(ErrValue)},
		{"1/0", Err(ErrDiv0)},
		{"0^0", Err(ErrNum)},
		{"0^-1", Err(ErrDiv0)},
		{"(-8)^(1/3)", Err(ErrNum)},
		{"#N/A+1", Err(ErrNA)},
		{"1+#REF!", Err(ErrRef)},
		{"F1=1", Err(ErrNA)},
		{"1E308*10", Err(ErrNum)},
		{"0.1+0.2-0.3", 0},
		{"1.1-1.0-0.1", 8.326672684688674e-17},

		// References.
		{"A1:A3 A2:C2", 2},
		{"SUM(A1:B2 B1:C5)", 30},
		{"A1:A3 C1:C3", Err(ErrNull)},
		{"SUM((A1,A3))", 4},
		{"SUM((A1:A2,B1:B2),A5)", 38},
		{"Data!A1+Data!A2", 300},
		{"'My Sheet'!A1", 1000},
		{"SUM(Sheet1:Data!A1)", 101},
		{"Nope!A1", Err(ErrRef)},
		{"SUM(Sheet1!A1:A3)", 6},
		{"A1:INDEX(A1:A5,3)", Err(ErrValue)},
		{"SUM(A1:INDEX(A1:A5,3))", 6},
		{"SUM(A:A)", 15},
		{"SUM(3:4)", 84},
		{"nosuchname", Err(ErrName)},
		{"Rate*2", 0.5},
		{"Twice", 20},
		{"Loop1", Err(ErrName)},
		{"Data!Local", 9},
		{"Local", Err(ErrName)},
		{"SUM(Bases)", 15},

		// Arrays.
		{"SUM({1,2;3,4})", 10},
		{"SUM({1,2,3}*2)", 12},
		{"{1,2,3}+{10;20}", 11},
		{"SUM({1,2}+{10;20})", 66},
		{"INDEX({1,2;3,4},2,1)", 3},
		{"ROWS({1,2;3,4})", 2},
		{"COLUMNS({1,2;3,4})", 2},
		{"SUM(ABS({-1,-2}))", 3},
		{"SUM({1,\"2\",TRUE})", 1},

		// Math.
		{"SUM(1,\"2\",TRUE)", 4},
		{"SUM(A1:A5,D1:D5)", 22},
		{"SUM(\"x\")", Err(ErrValue)},
		{"SUM(F1:F2)", Err(ErrNA)},
		{"SUM()", 0},
		{"AVERAGE(A1:A5)", 3},
		{"AVERAGE(1,2,)", 1},
		{"AVERAGE(C1:C3)", Err(ErrDiv0)},
		{"AVERAGEA(D1:D3)", 8.0 / 3},
		{"COUNT(A1:E5)", 14},
		{"COUNT(1,\"2\",\"x\",TRUE)", 3},
		{"COUNTA(C1:C5,D4)", 5},
		{"COUNTBLANK(D1:D5)", 1},
		{"MIN(A1:A5)", 1},
		{"MAX(A1:B5)", 50},
		{"MAX(C1:C3)", 0},
		{"MAXA(D1:D3)", 7},
		{"MINA(D1:D3)", 0},
		{"ROUND(2.5,0)", 3},
		{"ROUND(-2.5,0)", -3},
		{"ROUND(1.005,2)", 1.01},
		{"ROUND(2.675,2)", 2.68},
		{"ROUND(1234.5678,-2)", 1200},
		{"ROUND(0.5,0)", 1},
		{"ROUNDUP(1.1,0)", 2},
		{"ROUNDUP(-1.1,0)", -2},
		{"ROUNDDOWN(1.9,0)", 1},
		{"ROUNDDOWN(-1.9,0)", -1},
		{"ROUNDUP(1234.1,-2)", 1300},
		{"INT(-1.5)", -2},
		{"INT(1.5)", 1},
		{"TRUNC(-1.59,1)", -1.5},
		{"TRUNC(1.59)", 1},
		{"MOD(-3,2)", 1},
		{"MOD(3,-2)", -1},
		{"MOD(-3,-2)", -1},
		{"MOD(5.5,2)", 1.5},
		{"MOD(1,0)", Err(ErrDiv0)},
		{"ABS(-3)", 3},
		{"SQRT(16)", 4},
		{"SQRT(-1)", Err(ErrNum)},
		{"POWER(2,10)", 1024},
		{"EXP(0)", 1},
		{"LN(1)", 0},
		{"LN(0)", Err(ErrNum)},
		{"LOG(100)", 2},
		{"LOG(8,2)", 3},
		{"LOG(8,1)", Err(ErrDiv0)},
		{"LOG10(1000)", 3},
		{"PI()", math.Pi},
		{"CEILING(2.1,1)", 3},
		{"CEILING(2.5,0.1)", 2.5},
		{"CEILING(-2.5,2)", -2},
		{"CEILING(-2.5,-2)", -4},
		{"CEILING(2.5,-2)", Err(ErrNum)},
		{"CEILING(0,0)", 0},
		{"FLOOR(2.9,1)", 2},
		{"FLOOR(-2.5,2)", -4},
		{"FLOOR(-2.5,-2)", -2},
		{"FLOOR(2.5,-2)", Err(ErrNum)},
		{"FLOOR(1,0)", Err(ErrDiv0)},
		{"CEILING.MATH(-5.5,2)", -4},
		{"CEILING.MATH(-5.5,2,1)", -6},
		{"CEILING.MATH(6.1)", 7},
		{"FLOOR.MATH(-5.5,2)", -6},
		{"FLOOR.MATH(-5.5,2,1)", -4},
		{"_xlfn.FLOOR.MATH(5.5)", 5},
		{"MROUND(10,3)", 9},
		{"MROUND(1.3,0.2)", 1.4},
		{"MROUND(-10,-3)", -9},
		{"MROUND(5,-2)", Err(ErrNum)},
		{"SIGN(-3)", -1},
		{"SUMPRODUCT(A1:A3,B1:B3)", 140},
		{"SUMPRODUCT(A1:A3*B1:B3)", 140},
		{"SUMPRODUCT((A1:A5>2)*(B1:B5))", 120},
		{"SUMPRODUCT(A1:A3,A1:A2)", Err(ErrValue)},
		{"SUMPRODUCT(C1:C3,A1:A3)", 0},
		{"PRODUCT(A1:A4)", 24},
		{"PRODUCT(C1:C3)", 0},
		{"MEDIAN(1,2,3,4)", 2.5},
		{"MEDIAN(A1:A5)", 3},
		{"MODE(1,2,2,3,3)", 2},
		{"MODE(1,2,3)", Err(ErrNA)},
		{"MODE.SNGL({3,3,2,2})", 3},
		{"STDEV(2,4,4,4,5,5,7,9)", 2.13808993529939},
		{"STDEV.P(2,4,4,4,5,5,7,9)", 2},
		{"STDEVP(2,4,4,4,5,5,7,9)", 2},
		{"VAR(A1:A5)", 2.5},
		{"VAR.S(A1:A5)", 2.5},
		{"VAR.P(A1:A5)", 2},
		{"VARP(A1:A5)", 2},
		{"STDEV(1)", Err(ErrDiv0)},
		{"LARGE(A1:A5,2)", 4},
		{"SMALL(A1:A5,2)", 2},
		{"LARGE(A1:A5,9)", Err(ErrNum)},
		{"SUM(LARGE(A1:A5,{1,2}))", 9},
		{"RANK(3,A1:A5)", 3},
		{"RANK(3,A1:A5,1)", 3},
		{"RANK(5,A1:A5,1)", 5},
		{"RANK(9,A1:A5)", Err(ErrNA)},
		{"RANK.EQ(1,A1:A5)", 5},
		{"QUOTIENT(7,2)", 3},
		{"QUOTIENT(-7,2)", -3},
		{"GCD(24,36)", 12},
		{"LCM(4,6)", 12},
		{"FACT(5)", 120},
		{"FACT(171)", Err(ErrNum)},
		{"DEGREES(PI())", 180},
		{"RADIANS(180)", math.Pi},
		{"SIN(0)", 0},
		{"COS(0)", 1},
		{"ATAN2(1,1)", math.Pi / 4},
		{"ATAN2(0,0)", Err(ErrDiv0)},
		{"ACOS(2)", Err(ErrNum)},
		{"ISEVEN(2.5)", true},
		{"ISODD(3)", true},
		{"SUBTOTAL(9,A1:A5)", 15},
		{"SUBTOTAL(109,A1:A5)", 15},
		{"SUBTOTAL(1,A1:A5)", 3},
		{"SUBTOTAL(2,A1:C5)", 10},
		{"SUBTOTAL(3,C1:C5)", 5},
		{"SUBTOTAL(4,A1:A5)", 5},
		{"SUBTOTAL(5,A1:A5)", 1},
		{"SUBTOTAL(10,A1:A5)", 2.5},
		{"SUBTOTAL(12,A1:A5)", Err(ErrValue)},

		// Criteria.
		{"COUNTIF(A1:A5,\">2\")", 3},
		{"COUNTIF(A1:A5,3)", 1},
		{"COUNTIF(A1:A5,\"<>3\")", 4},
		{"COUNTIF(C1:C5,\"apple\")", 1},
		{"COUNTIF(C1:C5,\"APPLE*\")", 2},
		{"COUNTIF(C1:C5,\"?ate\")", 1},
		{"COUNTIF(C1:C5,\"<>apple\")", 4},
		{"COUNTIF(D1:D5,7)", 2},
		{"COUNTIF(D1:D5,\"=7\")", 2},
		{"COUNTIF(D1:D5,\">5\")", 1},
		{"COUNTIF(D1:D5,TRUE)", 1},
		{"COUNTIF(D1:D5,\"=\")", 1},
		{"COUNTIF(D1:D5,\"\")", 1},
		{"COUNTIF(D1:D5,\"<>\")", 4},
		{"COUNTIF(F1:F2,\"#N/A\")", 1},
		{"COUNTIF(A:A,\"\")", 1048571},
		{"COUNTIF(Z1:Z10,Z99)", 0},
		{"COUNTIF(C1:C5,\">b\")", 3},
		{"SUM(COUNTIF(A1:A5,{1,2,9}))", 2},
		{"SUMIF(A1:A5,\">2\")", 12},
		{"SUMIF(A1:A5,\">2\",B1:B5)", 120},
		{"SUMIF(A1:A5,\">2\",B1)", 120},
		{"SUMIF(C1:C5,\"apple*\",A1:A5)", 6},
		{"SUMIF(D1:D5,\"\",A1:A5)", 4},
		{"SUMIFS(B1:B5,A1:A5,\">1\",A1:A5,\"<5\")", 90},
		{"SUMIFS(B1:B5,A1:A5,\">1\",A1:A4,\"<5\")", Err(ErrValue)},
		{"COUNTIFS(A1:A5,\">1\",C1:C5,\"*a*\")", 3},
		{"COUNTIFS(A1:A5,\">1\",A1:A5,\"<4\")", 2},
		{"AVERAGEIF(A1:A5,\">2\")", 4},
		{"AVERAGEIF(A1:A5,\">9\")", Err(ErrDiv0)},
		{"AVERAGEIF(A1:A5,\">2\",B1:B5)", 40},
		{"AVERAGEIFS(B1:B5,A1:A5,\">1\",A1:A5,\"<4\")", 25},
		{"MINIFS(B1:B5,A1:A5,\">2\")", 30},
		{"MAXIFS(B1:B5,A1:A5,\"<3\")", 20},
		{"_xlfn.MAXIFS(B1:B5,A1:A5,\"<3\")", 20},
		{"MAXIFS(B1:B5,A1:A5,\">99\")", 0},
		{"COUNTIF(C1:C5,\"a~*\")", 0},

		// Logic.
		{"IF(1,2,3)", 2},
		{"IF(0,2,3)", 3},
		{"IF(0,2)", false},
		{"IF(1,2)", 2},
		{"IF(TRUE,,3)", 0},
		{"IF(\"x\",1,2)", Err(ErrValue)},
		{"IF(1,1,1/0)", 1},
		{"IFS(0,1,1,2)", 2},
		{"IFS(0,1)", Err(ErrNA)},
		{"IFERROR(1/0,9)", 9},
		{"IFERROR(1,9)", 1},
		{"IFNA(#N/A,8)", 8},
		{"IFNA(1/0,8)", Err(ErrDiv0)},
		{"AND(TRUE,1,A1:A2)", true},
		{"AND(TRUE,0)", false},
		{"AND(C1:C2)", Err(ErrValue)},
		{"OR(0,0)", false},
		{"OR(0,1)", true},
		{"OR(\"TRUE\")", true},
		{"AND(\"x\")", Err(ErrValue)},
		{"XOR(1,1,1)", true},
		{"XOR(1,1)", false},
		{"NOT(0)", true},
		{"SWITCH(2,1,\"a\",2,\"b\",\"c\")", "b"},
		{"SWITCH(9,1,\"a\",2,\"b\",\"c\")", "c"},
		{"SWITCH(9,1,\"a\")", Err(ErrNA)},
		{"CHOOSE(2,\"a\",\"b\")", "b"},
		{"CHOOSE(3,\"a\",\"b\")", Err(ErrValue)},
		{"SUM(CHOOSE(1,A1:A2,B1:B2))", 3},
		{"TRUE()", true},
		{"FALSE()", false},
		{"NA()", Err(ErrNA)},
		{"N(TRUE)", 1},
		{"N(\"x\")", 0},
		{"T(\"x\")", "x"},
		{"T(1)", ""},
		{"ISBLANK(Z1)", true},
		{"ISBLANK(A1)", false},
		{"ISERROR(1/0)", true},
		{"ISERR(NA())", false},
		{"ISNA(NA())", true},
		{"ISNUMBER(A1)", true},
		{"ISNUMBER(\"1\")", false},
		{"ISTEXT(C1)", true},
		{"ISNONTEXT(A1)", true},
		{"ISLOGICAL(D1)", true},

		// Lookup.
		{"VLOOKUP(3,A1:C5,2,FALSE)", 30},
		{"VLOOKUP(3,A1:C5,3,0)", "Cherry"},
		{"VLOOKUP(3.5,A1:C5,2)", 30},
		{"VLOOKUP(3.5,A1:C5,2,TRUE)", 30},
		{"VLOOKUP(0,A1:C5,2)", Err(ErrNA)},
		{"VLOOKUP(9,A1:C5,2,0)", Err(ErrNA)},
		{"VLOOKUP(3,A1:C5,9,0)", Err(ErrRef)},
		{"VLOOKUP(3,A1:C5,0,0)", Err(ErrValue)},
		{"VLOOKUP(\"app*\",C1:C5,1,0)", "apple"},
		{"VLOOKUP(\"CHERRY\",C1:C5,1,0)", "Cherry"},
		{"VLOOKUP(\"x\",{\"x\",1;\"y\",2},2,0)", 1},
		{"VLOOKUP(3,A:B,2,0)", 30},
		{"HLOOKUP(20,A2:B4,2,0)", 30},
		{"HLOOKUP(99,A2:B4,2,0)", Err(ErrNA)},
		{"HLOOKUP(2,A2:B4,2,0)", 3},
		{"HLOOKUP(2,A2:B4,3)", 4},
		{"MATCH(3,A1:A5,0)", 3},
		{"MATCH(3.5,A1:A5)", 3},
		{"MATCH(3.5,A1:A5,1)", 3},
		{"MATCH(0,A1:A5,1)", Err(ErrNA)},
		{"MATCH(\"b*\",C1:C5,0)", 2},
		{"MATCH(3,{5,4,3,2,1},-1)", 3},
		{"MATCH(3.5,{5,4,3,2,1},-1)", 2},
		{"MATCH(2,A1:B1,0)", Err(ErrNA)},
		{"MATCH(10,A1:B1,0)", 2},
		{"INDEX(A1:C5,2,3)", "banana"},
		{"INDEX(A1:A5,4)", 4},
		{"INDEX(A1:C1,3)", "apple"},
		{"INDEX(A1:C5,9,1)", Err(ErrRef)},
		{"SUM(INDEX(A1:B5,0,2))", 150},
		{"SUM(INDEX(A1:B5,2,0))", 22},
		{"INDEX((A1:A2,B1:B2),2,1,2)", 20},
		{"LOOKUP(3.5,A1:A5,B1:B5)", 30},
		{"LOOKUP(3,{1,2,3,4})", 3},
		{"LOOKUP(3,{1,2;3,4;5,6})", 4},
		{"LOOKUP(0,A1:A5)", Err(ErrNA)},
		{"XLOOKUP(3,A1:A5,B1:B5)", 30},
		{"_xlfn.XLOOKUP(9,A1:A5,B1:B5,\"none\")", "none"},
		{"XLOOKUP(9,A1:A5,B1:B5)", Err(ErrNA)},
		{"XLOOKUP(3.5,A1:A5,B1:B5,,-1)", 30},
		{"XLOOKUP(3.5,A1:A5,B1:B5,,1)", 40},
		{"XLOOKUP(\"app*\",C1:C5,A1:A5,,2)", 1},
		{"XLOOKUP(\"app*\",C1:C5,A1:A5)", Err(ErrNA)},
		{"XLOOKUP(\"app*\",C1:C5,A1:A5,,2,-1)", 5},
		{"XLOOKUP(3,A1:A5,B1:B5,,0,2)", 30},
		{"XLOOKUP(3.5,A1:A5,B1:B5,,1,2)", 40},
		{"SUM(XLOOKUP(2,A1:A5,A1:B5))", 22},
		{"OFFSET(A1,1,1)", 20},
		{"SUM(OFFSET(A1,0,0,3,1))", 6},
		{"SUM(OFFSET(A3,0,0,-3,1))", 6},
		{"OFFSET(A1,-1,0)", Err(ErrRef)},
		{"OFFSET(A1,0,0,0,1)", Err(ErrRef)},
		{"INDIRECT(\"A2\")", 2},
		{"INDIRECT(\"Data!A2\")", 200},
		{"INDIRECT(\"'My Sheet'!A1\")", 1000},
		{"SUM(INDIRECT(\"A1:A3\"))", 6},
		{"INDIRECT(\"R2C1\",FALSE)", 2},
		{"INDIRECT(\"junk!!\")", Err(ErrRef)},
		{"ROW(B7)", 7},
		{"COLUMN(C1)", 3},
		{"ROW(A2:A5)", 2},
		{"ROWS(A1:B5)", 5},
		{"COLUMNS(A1:C5)", 3},
		{"ROWS(A:A)", 1048576},
		{"COLUMNS(1:1)", 16384},

		// Text.
		{"CONCAT(A1:A2,\"x\")", "12x"},
		{"_xlfn.CONCAT(\"a\",1,TRUE)", "a1TRUE"},
		{"CONCATENATE(\"a\",1)", "a1"},
		{"TEXTJOIN(\",\",TRUE,\"a\",\"\",\"b\")", "a,b"},
		{"TEXTJOIN(\",\",FALSE,\"a\",\"\",\"b\")", "a,,b"},
		{"TEXTJOIN(\"-\",TRUE,A1:A3)", "1-2-3"},
		{"LEFT(\"hello\",2)", "he"},
		{"LEFT(\"hello\")", "h"},
		{"RIGHT(\"hello\",2)", "lo"},
		{"RIGHT(\"hi\",9)", "hi"},
		{"LEFT(\"x\",-1)", Err(ErrValue)},
		{"MID(\"hello\",2,3)", "ell"},
		{"MID(\"hello\",9,3)", ""},
		{"MID(\"hello\",0,1)", Err(ErrValue)},
		{"MID(\"你好世界\",2,2)", "好世"},
		{"LEN(\"hello\")", 5},
		{"LEN(\"你好\")", 2},
		{"LEN(12.5)", 4},
		{"LEN(\"😀\")", 2},
		{"LOWER(\"AbC\")", "abc"},
		{"UPPER(\"AbC\")", "ABC"},
		{"PROPER(\"hello wORLD o'neil x1y\")", "Hello World O'Neil X1Y"},
		{"TRIM(\"  a   b  \")", "a b"},
		{"SUBSTITUTE(\"aXbXc\",\"X\",\"-\")", "a-b-c"},
		{"SUBSTITUTE(\"aXbXc\",\"X\",\"-\",2)", "aXb-c"},
		{"SUBSTITUTE(\"aXb\",\"\",\"-\")", "aXb"},
		{"REPLACE(\"abcdef\",2,3,\"Z\")", "aZef"},
		{"FIND(\"b\",\"abcb\")", 2},
		{"FIND(\"b\",\"abcb\",3)", 4},
		{"FIND(\"B\",\"abc\")", Err(ErrValue)},
		{"FIND(\"\",\"abc\",2)", 2},
		{"SEARCH(\"B\",\"abc\")", 2},
		{"SEARCH(\"b?d\",\"abcd\")", 2},
		{"SEARCH(\"*c\",\"abcd\")", 1},
		{"SEARCH(\"z\",\"abc\")", Err(ErrValue)},
		{"VALUE(\"1,234.5\")", 1234.5},
		{"VALUE(\"50%\")", 0.5},
		{"VALUE(\"x\")", Err(ErrValue)},
		{"VALUE(\"(5)\")", -5},
		{"VALUE(\"12:00\")", 0.5},
		{"VALUE(\"1/2/2020\")", 43832},
		{"VALUE(\"2020-01-02\")", 43832},
		{"VALUE(\"1e3\")", 1000},
		{"EXACT(\"a\",\"A\")", false},
		{"EXACT(\"a\",\"a\")", true},
		{"REPT(\"ab\",3)", "ababab"},
		{"REPT(\"a\",40000)", Err(ErrValue)},
		{"CHAR(65)", "A"},
		{"CHAR(128)", "€"},
		{"CODE(\"A\")", 65},
		{"CODE(\"€\")", 128},
		{"UNICHAR(20320)", "你"},
		{"UNICODE(\"你\")", 20320},
		{"CLEAN(\"a\" & CHAR(9) & \"b\")", "ab"},
		{"NUMBERVALUE(\"1.234,5\",\",\",\".\")", 1234.5},
		{"NUMBERVALUE(\"2.5%\")", 0.025},
		{"NUMBERVALUE(\"\")", 0},
		{"FIXED(1234.567)", "1,234.57"},
		{"FIXED(1234.567,1,TRUE)", "1234.6"},
		{"FIXED(1234.567,-2)", "1,200"},
		{"FIXED(-0.5,0)", "-1"},
		{"DOLLAR(1234.567)", "$1,234.57"},
		{"DOLLAR(-1234.5,1)", "($1,234.5)"},
		{"1/3&\"\"", "0.333333333333333"},
		{"1E+20&\"\"", "1E+20"},
		{"100000000000000&\"\"", "100000000000000"},
		{"1000000000000000&\"\"", "1E+15"},
		{"0.0001&\"\"", "0.0001"},
		{"1E-10&\"\"", "1E-10"},
		{"-1.5&\"\"", "-1.5"},

		// TEXT.
		{"TEXT(1234.567,\"#,##0.00\")", "1,234.57"},
		{"TEXT(0.285,\"0.0%\")", "28.5%"},
		{"TEXT(1.5,\"0.00\")", "1.50"},
		{"TEXT(-1.5,\"0.00;(0.00)\")", "(1.50)"},
		{"TEXT(43831,\"yyyy-mm-dd\")", "2020-01-01"},
		{"TEXT(43831,\"mmm d, yyyy\")", "Jan 1, 2020"},
		{"TEXT(43831,\"dddd\")", "Wednesday"},
		{"TEXT(0.5,\"h:mm AM/PM\")", "12:00 PM"},
		{"TEXT(\"123\",\"0.0\")", "123.0"},
		{"TEXT(\"abc\",\"0.0\")", "abc"},
		{"TEXT(\"abc\",\"@@\")", "abcabc"},
		{"TEXT(5,\"\")", ""},
		{"TEXT(5,\"bogus\")", Err(ErrValue)},
		{"TEXT(1/0,\"0\")", Err(ErrDiv0)},
		{"TEXT(TRUE,\"0\")", "TRUE"},
		{"TEXT(1234,\"0,\")", "1"},
		{"TEXT(0.5,\"# ?/?\")", " 1/2"},

		// Dates. 2020-01-01 is 43831; serial 60 is the fictional 1900-02-29.
		{"DATE(2020,1,1)", 43831},
		{"DATE(1900,1,1)", 1},
		{"DATE(1900,2,28)", 59},
		{"DATE(1900,2,29)", 60},
		{"DATE(1900,3,1)", 61},
		{"DATE(1900,1,0)", 0},
		{"DATE(1899,12,31)", SerialFromDate(3799, 12, 31, false)},
		{"DATE(-1,1,1)", Err(ErrNum)},
		{"DATE(100,1,1)", SerialFromDate(2000, 1, 1, false)},
		{"DATE(2020,13,1)", 44197},
		{"DATE(2020,0,1)", 43800},
		{"DATE(2020,3,0)", 43890},
		{"DATE(2020,1,-1)", 43829},
		{"DATE(10000,1,1)", Err(ErrNum)},
		{"DATE(9999,12,31)", 2958465},
		{"TIME(12,0,0)", 0.5},
		{"TIME(25,0,0)", 1.0 / 24},
		{"TIME(0,90,0)", 1.5 / 24},
		{"TIME(-1,0,0)", Err(ErrNum)},
		{"YEAR(43831)", 2020},
		{"MONTH(43831)", 1},
		{"DAY(43831)", 1},
		{"YEAR(60)", 1900},
		{"MONTH(60)", 2},
		{"DAY(60)", 29},
		{"DAY(0)", 0},
		{"MONTH(0)", 1},
		{"YEAR(0)", 1900},
		{"YEAR(-1)", Err(ErrNum)},
		{"HOUR(0.75)", 18},
		{"MINUTE(0.75+1/1440)", 1},
		{"SECOND(0.5+1/86400)", 1},
		{"HOUR(\"13:30\")", 13},
		{"YEAR(\"2020-05-06\")", 2020},
		{"WEEKDAY(1)", 1},
		{"WEEKDAY(7)", 7},
		{"WEEKDAY(0)", 7},
		{"WEEKDAY(43831)", 4},
		{"WEEKDAY(43831,2)", 3},
		{"WEEKDAY(43831,3)", 2},
		{"WEEKDAY(43831,11)", 3},
		{"WEEKDAY(43831,12)", 2},
		{"WEEKDAY(43831,16)", 5},
		{"WEEKDAY(43831,17)", 4},
		{"WEEKDAY(43831,9)", Err(ErrNum)},
		{"EDATE(43861,1)", 43890},
		{"EDATE(43861,-2)", 43799},
		{"EDATE(DATE(2020,3,31),1)", SerialFromDate(2020, 4, 30, false)},
		{"EOMONTH(43831,0)", 43861},
		{"EOMONTH(43831,1)", 43890},
		{"EOMONTH(43831,-1)", 43830},
		{"EOMONTH(DATE(2021,2,10),0)", SerialFromDate(2021, 2, 28, false)},
		{"EOMONTH(32,0)", 60},
		{"EOMONTH(-1,0)", Err(ErrNum)},
		{"DATEDIF(DATE(2020,1,15),DATE(2021,3,10),\"Y\")", 1},
		{"DATEDIF(DATE(2020,1,15),DATE(2021,3,10),\"M\")", 13},
		{"DATEDIF(DATE(2020,1,15),DATE(2021,3,10),\"D\")", 420},
		{"DATEDIF(DATE(2020,1,15),DATE(2021,3,10),\"YM\")", 1},
		{"DATEDIF(DATE(2020,1,15),DATE(2021,3,10),\"MD\")", 23},
		{"DATEDIF(DATE(2020,1,15),DATE(2021,3,10),\"YD\")", 54},
		{"DATEDIF(DATE(2021,1,1),DATE(2020,1,1),\"D\")", Err(ErrNum)},
		{"DATEDIF(1,2,\"Q\")", Err(ErrNum)},
		{"NETWORKDAYS(DATE(2020,1,1),DATE(2020,1,31))", 23},
		{"NETWORKDAYS(DATE(2020,1,31),DATE(2020,1,1))", -23},
		{"NETWORKDAYS(DATE(2020,1,1),DATE(2020,1,31),DATE(2020,1,1))", 22},
		{"NETWORKDAYS(DATE(2020,1,1),DATE(2020,1,31),{43831,43831,43834})", 22},
		{"NETWORKDAYS.INTL(DATE(2020,1,1),DATE(2020,1,7),7)", 5},
		{"NETWORKDAYS.INTL(DATE(2020,1,1),DATE(2020,1,7),11)", 6},
		{"NETWORKDAYS.INTL(DATE(2020,1,6),DATE(2020,1,8),12)", 2},
		{"NETWORKDAYS.INTL(DATE(2020,1,4),DATE(2020,1,5),17)", 1},
		{"NETWORKDAYS.INTL(DATE(2020,1,1),DATE(2020,1,7),\"0000011\")", 5},
		{"NETWORKDAYS.INTL(DATE(2020,1,1),DATE(2020,1,7),\"1111111\")", Err(ErrValue)},
		{"NETWORKDAYS.INTL(1,2,99)", Err(ErrNum)},
		{"WORKDAY(DATE(2020,1,1),10)", SerialFromDate(2020, 1, 15, false)},
		{"WORKDAY(DATE(2020,1,6),-1)", SerialFromDate(2020, 1, 3, false)},
		{"WORKDAY(DATE(2020,1,3),1,DATE(2020,1,6))", SerialFromDate(2020, 1, 7, false)},
		{"WORKDAY.INTL(DATE(2020,1,3),1,11)", SerialFromDate(2020, 1, 4, false)},
		{"DAYS(DATE(2020,3,1),DATE(2020,2,1))", 29},
		{"DATEVALUE(\"1/2/2020\")", 43832},
		{"DATEVALUE(\"2 Jan 2020\")", 43832},
		{"DATEVALUE(\"Jan 2, 2020\")", 43832},
		{"DATEVALUE(\"2020年1月2日\")", 43832},
		{"DATEVALUE(\"1/2/2020 13:00\")", 43832},
		{"DATEVALUE(\"nonsense\")", Err(ErrValue)},
		{"DATEVALUE(\"2/30/2020\")", Err(ErrValue)},
		{"TIMEVALUE(\"6:00 PM\")", 0.75},
		{"TIMEVALUE(\"1/2/2020 12:00\")", 0.5},
		{"WEEKNUM(DATE(2020,1,1))", 1},
		{"WEEKNUM(DATE(2020,1,5))", 2},
		{"WEEKNUM(DATE(2020,1,5),2)", 1},
		{"WEEKNUM(DATE(2020,1,6),2)", 2},
		{"WEEKNUM(DATE(2021,1,1),21)", 53},
		{"ISOWEEKNUM(DATE(2021,1,1))", 53},
		{"ISOWEEKNUM(DATE(2020,12,28))", 53},
		{"ISOWEEKNUM(DATE(2018,12,31))", 1},

		// Finance.
		{"PMT(0.08/12,10,10000)", -1037.03208935915},
		{"PMT(0.08/12,10,10000,0,1)", -1030.16432717797},
		{"PMT(0,10,1000)", -100},
		{"FV(0.06/12,10,-200,-500,1)", 2581.40337406012},
		{"FV(0,10,-100)", 1000},
		{"PV(0.08/12,240,500,,0)", -59777.1458511878},
		{"NPV(0.1,-10000,3000,4200,6800)", 1188.44341233520},
		{"IRR({-70000,12000,15000,18000,21000,26000})", 0.0866309480365},
		{"IRR({-70000,12000,15000})", -0.44350694133474},
		{"IRR({1,2,3})", Err(ErrNum)},
		{"NPER(0.12/12,-100,-1000,10000,1)", 59.6738656742946},
		{"NPER(0,-100,1000)", 10},
		{"RATE(48,-200,8000)", 0.00770147248820},
		{"IPMT(0.1/12,1,36,8000)", -66.6666666666667},
		{"IPMT(0.1,3,3,8000)", -292.447129909366},
		{"PPMT(0.1/12,1,24,2000)", -75.6231860083},
		{"SLN(30000,7500,10)", 2250},
		{"IPMT(0.1,4,3,8000)", Err(ErrNum)},
	}
	b := dataBook()
	b.names["RATE"] = "0.25"
	b.names["TWICE"] = "Sheet1!$A$1*20"
	b.names["LOOP1"] = "Loop2"
	b.names["LOOP2"] = "Loop1"
	b.names["Data!LOCAL"] = "9"
	b.names["BASES"] = "=Sheet1!$A$1:$A$5"
	for _, c := range cases {
		got := evalAt(t, b, c.src, "H10", false)
		if !sameValue(got, c.want) {
			t.Errorf("%s = %#v, want %v", c.src, got, c.want)
		}
	}
}

func TestImplicitIntersectionAndArrays(t *testing.T) {
	b := dataBook()
	if got := evalAt(t, b, "A1:A5", "H3", false); !sameValue(got, 3) {
		t.Errorf("implicit intersection on row 3: %#v", got)
	}
	if got := evalAt(t, b, "A1:A5", "H9", false); !sameValue(got, Err(ErrValue)) {
		t.Errorf("intersection outside: %#v", got)
	}
	if got := evalAt(t, b, "A1:C1", "B9", false); !sameValue(got, 10) {
		t.Errorf("row intersection: %#v", got)
	}
	if got := evalAt(t, b, "{5,6;7,8}", "H9", false); !sameValue(got, 5) {
		t.Errorf("array top-left: %#v", got)
	}
	if got := evalAt(t, b, "A1:A3*2", "H2", false); !sameValue(got, 4) {
		t.Errorf("operator intersects when not an array formula: %#v", got)
	}
	got := evalAt(t, b, "A1:A3*2", "H2", true)
	if got.Kind != Array || got.Arr.Rows != 3 || got.Arr.V[2].Num != 6 {
		t.Fatalf("array formula: %#v", got)
	}
	got = evalAt(t, b, "IF(A1:A3>1,A1:A3,\"no\")", "H9", true)
	if got.Kind != Array || got.Arr.V[0].Str != "no" || got.Arr.V[2].Num != 3 {
		t.Fatalf("array IF: %#v", got)
	}
	got = evalAt(t, b, "SUM(IF(A1:A5>2,A1:A5))", "H9", true)
	if !sameValue(got, 12) {
		t.Fatalf("SUM(IF()) as array: %#v", got)
	}
	got = evalAt(t, b, "SUM(A1:A3*B1:B3)", "H9", true)
	if !sameValue(got, 140) {
		t.Fatalf("array sum of products: %#v", got)
	}
	got = evalAt(t, b, "ROW(A2:A4)", "H9", true)
	if got.Kind != Array || got.Arr.Rows != 3 || got.Arr.V[0].Num != 2 {
		t.Fatalf("ROW as array: %#v", got)
	}
	got = evalAt(t, b, "{1,2}+{1;2;3}", "H9", true)
	if got.Kind != Array || got.Arr.Rows != 3 || got.Arr.Cols != 2 || got.Arr.V[5].Num != 5 {
		t.Fatalf("broadcast: %#v", got)
	}
	got = evalAt(t, b, "{1,2,3}+{1,2}", "H9", true)
	if got.Kind != Array || got.Arr.V[2].Str != ErrNA {
		t.Fatalf("broadcast past the end: %#v", got)
	}
	got = evalAt(t, b, "A1", "H9", true)
	if !sameValue(got, 1) {
		t.Fatalf("single cell as array: %#v", got)
	}
	got = evalAt(t, b, "Z1", "H9", false)
	if !sameValue(got, 0) {
		t.Fatalf("a blank result is 0: %#v", got)
	}
	got = evalAt(t, b, "VLOOKUP(A1:A2,A1:B5,2,0)", "H9", true)
	if got.Kind != Array || got.Arr.V[1].Num != 20 {
		t.Fatalf("array lookup key: %#v", got)
	}
	got = evalAt(t, b, "SUMPRODUCT(SUMIF(A1:A5,A1:A2,B1:B5))", "H9", false)
	if !sameValue(got, 30) {
		t.Fatalf("SUMIF array criteria: %#v", got)
	}
}

func TestRelativeOffsets(t *testing.T) {
	b := dataBook()
	x, err := Parse("A1+$A$1")
	if err != nil {
		t.Fatal(err)
	}
	// Written at C1, evaluated two rows down.
	if got := x.Eval(b, 0, 2, 2, 2, 0, false); !sameValue(got, 4) {
		t.Errorf("shifted: %#v", got)
	}
	x, _ = Parse("A1")
	if got := x.Eval(b, 0, 0, 1, -1, 0, false); !sameValue(got, Err(ErrRef)) {
		t.Errorf("shifted off the sheet: %#v", got)
	}
}

func TestDate1904(t *testing.T) {
	b := newBook()
	b.d1904 = true
	cases := []struct {
		src  string
		want any
	}{
		{"DATE(1904,1,1)", 0},
		{"DATE(2020,1,1)", 43831 - 1462},
		{"YEAR(0)", 1904},
		{"WEEKDAY(0)", 6}, // 1 January 1904 was a Friday
		{"TEXT(0,\"yyyy-mm-dd\")", "1904-01-01"},
	}
	for _, c := range cases {
		if got := evalAt(t, b, c.src, "A1", false); !sameValue(got, c.want) {
			t.Errorf("%s = %#v, want %v", c.src, got, c.want)
		}
	}
}

func TestToday(t *testing.T) {
	b := newBook()
	v := evalAt(t, b, "NOW()-TODAY()", "A1", false)
	if v.Kind != Number || v.Num < 0 || v.Num >= 1 {
		t.Errorf("NOW-TODAY = %#v", v)
	}
	r := evalAt(t, b, "RANDBETWEEN(3,3)+RAND()*0", "A1", false)
	if !sameValue(r, 3) {
		t.Errorf("RANDBETWEEN: %#v", r)
	}
	if v := evalAt(t, b, "RANDBETWEEN(3,1)", "A1", false); !sameValue(v, Err(ErrNum)) {
		t.Errorf("RANDBETWEEN reversed: %#v", v)
	}
}

func TestHostileFormulas(t *testing.T) {
	deep := strings.Repeat("(", 600) + "1" + strings.Repeat(")", 600)
	if _, err := Parse(deep); err == nil {
		t.Error("600 parentheses should be refused")
	}
	if _, err := Parse(strings.Repeat("-", 2000) + "1"); err == nil {
		t.Error("2000 minus signs should be refused")
	}
	if _, err := Parse(strings.Repeat("SUM(", 2000) + "1" + strings.Repeat(")", 2000)); err == nil {
		t.Error("2000 nested calls should be refused")
	}
	ok := strings.Repeat("(", 100) + "1" + strings.Repeat(")", 100)
	if x, err := Parse(ok); err != nil {
		t.Error(err)
	} else if got := x.Eval(newBook(), 0, 0, 0, 0, 0, false); !sameValue(got, 1) {
		t.Errorf("nested: %#v", got)
	}
	// A very long flat sum neither recurses nor overflows.
	long := "1" + strings.Repeat("+1", 200000)
	x, err := Parse(long)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.Eval(newBook(), 0, 0, 0, 0, 0, false); !sameValue(got, 200001) {
		t.Errorf("long sum: %#v", got)
	}
	b := newBook()
	b.set(0, map[string]any{"A1": 1})
	huge, _ := Parse("A1:XFD1048576+1")
	if got := huge.Eval(b, 0, 0, 0, 0, 0, true); got.Kind != Number {
		// Clipped to the used part, which is one cell.
		t.Errorf("clipped: %#v", got)
	}
	big := newBook()
	big.set(0, map[string]any{"XFD1048576": 1})
	if got := huge.Eval(big, 0, 0, 0, 0, 0, true); !sameValue(got, Err(ErrNum)) {
		t.Errorf("too large to materialise: %#v", got)
	}
	rep, _ := Parse(`REPT(REPT("a",30000),30000)`)
	if got := rep.Eval(b, 0, 0, 0, 0, 0, false); !sameValue(got, Err(ErrValue)) {
		t.Errorf("text limit: %#v", got)
	}
	amp, _ := Parse(`REPT("a",30000)&REPT("a",30000)`)
	if got := amp.Eval(b, 0, 0, 0, 0, 0, false); !sameValue(got, Err(ErrValue)) {
		t.Errorf("text limit on &: %#v", got)
	}
	// Junk must come back as a parse error or a value, never a panic.
	junk := []string{"", " ", "(", ")", "{", "}", "{1,", "{1;2,3}", "1+", "+", "\"", "'", "'a", "'a'!", "A1:", ":A1", "#", "#X",
		"[", "[1", "SUM(", "SUM(1", "SUM(1,", "1 2", "1..2", "a!", "!a", "$", "$$A1", "A$", "@", "@@", "%", "A1%%%",
		"T[", "T[x]", "T[[#All],[x]]", "[@x]", "[1]Sheet1!A1", "'[Book.xlsx]S'!A1", "_xlfn.FOO(", "_xlfn.FOO()",
		"A1#", "LAMBDA(x,x)(1)", "_xlpm.x", "{1}{2}", "1:1:1", "A:A:A", "A1 B1 C1", "Sheet1:Sheet2!", "#REF!A1", "x!#REF!",
		"INDEX()", "INDEX(1)", "INDEX(1,2,3,4,5)", "OFFSET()", "VLOOKUP(1,2,3)", "MATCH(1,2)", "ROW(1)", "COLUMN(1)",
		"LARGE(1,1)", "IF()", "CHOOSE(1)", "SWITCH(1,2)", "TEXT()", "SUMIF(1,2)", "COUNTIFS(A1,1,A2)", "SUMIFS(1,2,3)"}
	for _, s := range junk {
		x, err := Parse(s)
		if err != nil {
			continue
		}
		x.Eval(b, 0, 0, 0, 0, 0, false)
		x.Eval(b, 0, 0, 0, 0, 0, true)
		x.Text(1, 1)
		x.Refs(0, 0, 0, 1, 1, b)
	}
}

func TestParseKnown(t *testing.T) {
	cases := []struct {
		src      string
		known    bool
		volatile bool
	}{
		{"SUM(A1:A3)", true, false},
		{"_xlfn.STDEV.S(A1:A3)", true, false},
		{"_xlfn._xlws.FILTER(A1:A3,B1:B3)", false, false},
		{"FOO(1)", false, false},
		{"[1]Sheet1!A1+1", false, false},
		{"'[Book.xlsx]Sheet 1'!A1", false, false},
		{"Table1[Col]", false, false},
		{"Table1[[#This Row],[Col]]+1", false, false},
		{"[@Col]", false, false},
		{"_xlfn.LET(_xlpm.x,1,_xlpm.x+1)", false, false},
		{"A1#", false, false},
		{"_xlfn.LAMBDA(_xlpm.x,_xlpm.x)(1)", false, false},
		{"NOW()", true, true},
		{"1+TODAY()", true, true},
		{"RAND()", true, true},
		{"RANDBETWEEN(1,2)", true, true},
		{"OFFSET(A1,1,1)", true, true},
		{"IF(1,INDIRECT(\"A1\"))", true, true},
		{"INDEX(A1:A3,1)", true, false},
		{"LOG10(100)", true, false},
		{"A1:B2", true, false},
		{"a1:b2", true, false},
		{"true", true, false},
		{"#REF!+1", true, false},
		{"Sheet1!#REF!", true, false},
		{"#REF!A1", true, false},
		{"Sheet1!Name", true, false},
		{"@A1:A3", true, false},
	}
	for _, c := range cases {
		x, err := Parse(c.src)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.src, err)
			continue
		}
		if x.Known() != c.known || x.Volatile() != c.volatile {
			t.Errorf("%q: Known %v Volatile %v", c.src, x.Known(), x.Volatile())
		}
		if x.Text(0, 0) != c.src {
			t.Errorf("Text(0,0) of %q = %q", c.src, x.Text(0, 0))
		}
	}
	for _, bad := range []string{"", "1+", "(1", "1)", "SUM(1", "\"abc", "{1,2;3}", "1 +* 2", "A1::B2", "'x'", "#FOO!"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}
