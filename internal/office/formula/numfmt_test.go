package formula

import "testing"

func TestRender(t *testing.T) {
	cases := []struct {
		code string
		v    any
		want string
	}{
		{"General", 1234.5, "1234.5"},
		{"General", -1234.5, "-1234.5"},
		{"General", "text", "text"},
		{"General", true, "TRUE"},
		{"", 5, "5"},
		{"0", 1.5, "2"},
		{"0", 2.5, "3"},
		{"0", -2.5, "-3"},
		{"0.00", 1.005, "1.01"},
		{"0.00", 2.675, "2.68"},
		{"0.00", 1.0, "1.00"},
		{"0.00", -0.001, "-0.00"},
		{"#.##", 1.5, "1.5"},
		{"#.##", 0.0, "."},
		{"#.00", 0.5, ".50"},
		{"0.0#", 1.0, "1.0"},
		{"0.0#", 1.25, "1.25"},
		{"0.0?", 1.5, "1.5 "},
		{"???", 5, "  5"},
		{"#", 0, ""},
		{"000", 5, "005"},
		{"000", 12345, "12345"},
		{"#,##0", 1234567, "1,234,567"},
		{"#,##0", 123, "123"},
		{"#,##0", 0, "0"},
		{"#,##0.00", 1234.5, "1,234.50"},
		{"0,000", 5, "0,005"},
		{"#,##0,", 1234567, "1,235"},
		{"#,##0,,\"M\"", 12345678, "12M"},
		{"0.0,", 1500, "1.5"},
		{"000-00-0000", 123456789, "123-45-6789"},
		{"(###) ###-####", 5551234567, "(555) 123-4567"},
		{"0%", 0.256, "26%"},
		{"0.00%", 0.2567, "25.67%"},
		{"0%%", 0.01, "100%%"},
		{"0.00E+00", 12345, "1.23E+04"},
		{"0.00E+00", 0.00012, "1.20E-04"},
		{"0.00E-00", 12345, "1.23E04"},
		{"0.00E+00", 0, "0.00E+00"},
		{"0.0E+0", 9.99, "1.0E+1"},
		{"##0.0E+0", 12345, "12.3E+3"},
		{"##0.0E+0", 1234, "1.2E+3"},
		{"00.0E+00", 1234, "12.3E+02"},
		{"# ?/?", 1.5, "1 1/2"},
		{"# ?/?", 0.5, " 1/2"},
		{"# ?/?", 5, "5    "},
		{"# ?/?", 0, "0    "},
		{"# ??/??", 1.25, "1  1/4 "},
		{"# ?/?", 3.14159, "3 1/7"},
		{"# ??/??", 3.14159, "3 14/99"},
		{"?/?", 1.5, "3/2"},
		{"# ??/16", 1.5, "1  8/16"},
		{"# ?/4", 0.3, " 1/4"},
		{"# ?/4", 2.9, "3    "},
		{"# ?/?", -1.5, "-1 1/2"},
		{"0;-0;\"zero\"", 0, "zero"},
		{"0;-0;\"zero\"", 5, "5"},
		{"0;-0;\"zero\"", -5, "-5"},
		{"0;(0)", -5, "(5)"},
		{"0;0", -5, "5"},
		{"0;(0)", 0, "0"},
		{"0.00;[Red]-0.00", -1, "-1.00"},
		{"0;-0;;@", 0, ""},
		{"0;-0;0;\"t:\"@", "x", "t:x"},
		{"0;-0;0;\"t:\"@", 3, "3"},
		{"@", "x", "x"},
		{"@", 12.5, "12.5"},
		{"@\" kg\"", "5", "5 kg"},
		{"0", "text", "text"},
		{";;;", 5, ""},
		{";;;", "x", ""},
		{"\"$\"#,##0.00", 1234.5, "$1,234.50"},
		{"$#,##0.00", 1234.5, "$1,234.50"},
		{"$#,##0.00", -1234.5, "-$1,234.50"},
		{"\\x0", 5, "x5"},
		{"0\" units\"", 5, "5 units"},
		{"0_)", 5, "5 "},
		{"(0)", 5, "(5)"},
		{"+0;-0", 5, "+5"},
		{"[>100]\"big\";[<0]\"neg\";0", 500, "big"},
		{"[>100]\"big\";[<0]\"neg\";0", -5, "neg"},
		{"[>100]\"big\";[<0]\"neg\";0", 50, "50"},
		{"[<=0]0;0.0", -3, "3"},
		{"[>100]0;0.0", -3, "-3.0"},
		{"[>=100]0;[<100]0.0", 5, "5.0"},
		{"[>100]0", 5, "5"},
		{"[>100]0.0", 500, "500.0"},
		{"[>100]0.0;[>50]0.00", 5, "5"},
		{"[Red]0", 5, "5"},
		{"[Color5]0", 5, "5"},
		{"[$-409]0", 5, "5"},
		{"[$¥-804]#,##0", 1234, "¥1,234"},
		{"[$€-2] 0.00", 1.5, "€ 1.50"},
		{"[DBNum1]0", 5, "5"},
		{"[$-F800]dddd", 43831, "Wednesday"},
		{"0.00元", 3, "3.00元"},

		// Dates.
		{"m/d/yyyy", 43831, "1/1/2020"},
		{"yyyy-mm-dd", 43831, "2020-01-01"},
		{"yy/m/d", 43831, "20/1/1"},
		{"yyyy年m月d日", 43831, "2020年1月1日"},
		{"d-mmm-yy", 43831, "1-Jan-20"},
		{"mmm-yy", 43831, "Jan-20"},
		{"mmmm", 43831, "January"},
		{"mmmmm", 43831, "J"},
		{"dddd", 43831, "Wednesday"},
		{"ddd", 43831, "Wed"},
		{"dd", 43831, "01"},
		{"aaaa", 43831, "星期三"},
		{"aaa", 43831, "三"},
		{"m/d/yyyy", 60, "2/29/1900"},
		{"m/d/yyyy", 0, "1/0/1900"},
		{"m/d/yyyy", 61, "3/1/1900"},
		{"h:mm", 0.5, "12:00"},
		{"h:mm", 0.75, "18:00"},
		{"hh:mm:ss", 0.75 + 1.0/86400, "18:00:01"},
		{"h:mm AM/PM", 0.75, "6:00 PM"},
		{"h:mm AM/PM", 0, "12:00 AM"},
		{"h:mm:ss A/P", 0.25, "6:00:00 A"},
		{"h:mm a/p", 0.75, "6:00 p"},
		{"上午/下午h:mm", 0.75, "下午6:00"},
		{"mm:ss", 0.5 + 90.0/86400, "01:30"},
		{"mm:ss.0", 1.5 / 86400, "00:01.5"},
		{"ss.00", 1.25 / 86400, "01.25"},
		{"h:mm:ss", 0.9999999, "0:00:00"},
		{"yyyy-mm-dd hh:mm", 43831.5, "2020-01-01 12:00"},
		{"m/d/yyyy h:mm", 43831.5, "1/1/2020 12:00"},
		{"[h]:mm", 1.5, "36:00"},
		{"[hh]:mm:ss", 0.04, "00:57:36"},
		{"[m]:ss", 0.5, "720:00"},
		{"[s]", 0.5, "43200"},
		{"[h]:mm:ss", 2 + 3.0/24 + 4.0/1440 + 5.0/86400, "51:04:05"},
		{"yyyy-mm-dd", -1, "########"},
		{"h:mm", -0.5, "########"},
		{"yyyy", 3000000, "########"},
	}
	for _, c := range cases {
		got := ParseFormat(c.code).Render(toValue(c.v), false)
		if got.Text != c.want {
			t.Errorf("%q on %v = %q, want %q", c.code, c.v, got.Text, c.want)
		}
	}
}

func TestRenderDetails(t *testing.T) {
	r := ParseFormat("[Red]0").Render(Num(5), false)
	if !r.HasColor || r.Color != [3]uint8{255, 0, 0} || !r.Numeric {
		t.Errorf("colour: %+v", r)
	}
	r = ParseFormat("[Color12]0").Render(Num(5), false)
	if !r.HasColor || r.Color != [3]uint8{0x80, 0x80, 0} {
		t.Errorf("Color12: %+v", r)
	}
	r = ParseFormat("0;[Blue]0").Render(Num(-5), false)
	if r.Color != [3]uint8{0, 0, 255} {
		t.Errorf("second section colour: %+v", r)
	}
	r = ParseFormat("0;[Blue]0").Render(Num(5), false)
	if r.HasColor {
		t.Errorf("first section has no colour: %+v", r)
	}
	r = ParseFormat("$* #,##0").Render(Num(1234), false)
	if r.Text != "$1,234" || r.Fill != ' ' || r.FillAt != 1 {
		t.Errorf("fill: %+v", r)
	}
	r = ParseFormat("0*-").Render(Num(7), false)
	if r.Text != "7" || r.Fill != '-' || r.FillAt != 1 {
		t.Errorf("trailing fill: %+v", r)
	}
	r = ParseFormat("_(* #,##0_);_(* \\(#,##0\\);_(* \"-\"_);_(@_)").Render(Num(-1234), false)
	if r.Text != " (1,234)" || r.Fill != ' ' {
		t.Errorf("accounting: %+v", r)
	}
	r = ParseFormat("_(* #,##0_);_(* \\(#,##0\\);_(* \"-\"_);_(@_)").Render(Str("x"), false)
	if r.Text != " x " || r.Numeric {
		t.Errorf("accounting text: %+v", r)
	}
	if ParseFormat("0").Render(Str("x"), false).Numeric {
		t.Error("text is not numeric")
	}
	if !ParseFormat("yyyy").Render(Num(-1), false).Numeric {
		t.Error("a failed date still counts as numeric, to show the hashes")
	}
	if ParseFormat("0").Render(Value{}, false).Text != "" {
		t.Error("blank renders empty")
	}
	if ParseFormat("0").Render(Err(ErrNA), false).Text != ErrNA {
		t.Error("errors show")
	}
	if ParseFormat("m/d/yyyy").Render(Num(0), true).Text != "1/1/1904" {
		t.Error("1904 system")
	}
	if ParseFormat("abcq").Render(Num(1.5), false).Text != "1.5" {
		t.Error("a broken code renders as General")
	}
	if ParseFormat("[Red").Render(Num(1.5), false).Text != "1.5" {
		t.Error("a broken bracket renders as General")
	}
	if ParseFormat("0;0;0;0;0").Render(Num(1.5), false).Text != "1.5" {
		t.Error("five sections render as General")
	}
}

func TestFormatKinds(t *testing.T) {
	for _, c := range []struct {
		code            string
		date, text, gen bool
	}{
		{"General", false, false, true},
		{"", false, false, true},
		{"general", false, false, true},
		{"[Red]General", false, false, false},
		{"0.00", false, false, false},
		{"m/d/yyyy", true, false, false},
		{"h:mm", true, false, false},
		{"[h]:mm", true, false, false},
		{"\"day\"0", false, false, false},
		{"@", false, true, false},
		{"0;@", false, false, false},
		{"yyyy\"年\"", true, false, false},
		{"[$-409]m/d/yy", true, false, false},
		{"\\d0", false, false, false},
	} {
		f := ParseFormat(c.code)
		if f.IsDate() != c.date || f.IsText() != c.text || f.IsGeneral() != c.gen {
			t.Errorf("%q: date %v text %v general %v", c.code, f.IsDate(), f.IsText(), f.IsGeneral())
		}
	}
}

func TestBuiltinFormat(t *testing.T) {
	want := map[int]string{0: "General", 1: "0", 3: "#,##0", 9: "0%", 14: "m/d/yyyy", 37: "#,##0_);(#,##0)", 49: "@", 23: "", 50: ""}
	for id, code := range want {
		if got := BuiltinFormat(id); got != code {
			t.Errorf("BuiltinFormat(%d) = %q", id, got)
		}
	}
	for id := range 50 {
		if code := BuiltinFormat(id); code != "" && ParseFormat(code).bad {
			t.Errorf("builtin %d does not parse: %q", id, code)
		}
	}
	if ParseFormat(BuiltinFormat(5)).Render(Num(-1234), false).Text != "($1,234)" {
		t.Error("format 5")
	}
	if ParseFormat(BuiltinFormat(44)).Render(Num(1234.5), false).Text != " $1,234.50 " {
		t.Errorf("format 44: %q", ParseFormat(BuiltinFormat(44)).Render(Num(1234.5), false).Text)
	}
}

func TestGeneral(t *testing.T) {
	cases := []struct {
		n    float64
		w    int
		want string
	}{
		{0, 0, "0"},
		{1, 0, "1"},
		{-1, 0, "-1"},
		{1.5, 0, "1.5"},
		{1234567890, 0, "1234567890"},
		{12345678901, 0, "12345678901"},
		{123456789012, 0, "1.23457E+11"},
		{1e15, 0, "1E+15"},
		{1e11, 0, "1E+11"},
		{1e10, 0, "10000000000"},
		{0.000012, 0, "0.000012"},
		{1.2e-7, 0, "1.2E-07"},
		{1e-7, 0, "1E-07"},
		{1.23456789e-6, 0, "1.23457E-06"},
		{0.1, 0, "0.1"},
		{0.123456789012, 0, "0.123456789"},
		{1234.56789012345, 0, "1234.56789"},
		{123456.789012, 0, "123456.789"},
		{0.1 + 0.2, 0, "0.3"},
		{1.0 / 3, 0, "0.333333333"},
		{-1.0 / 3, 0, "-0.333333333"},
		{1e100, 0, "1E+100"},
		{1.5e-100, 0, "1.5E-100"},
		{1234.5678, 6, "1234.6"},
		{1234.5678, 4, "1235"},
		{1234567, 5, "1E+06"},
		{0.0012345, 6, "0.0012"},
		{-1234.5678, 6, "-1235"},
		{123456789012, 8, "1.23E+11"},
		{1e15, 2, "1E+15"},
	}
	for _, c := range cases {
		if got := General(c.n, c.w); got != c.want {
			t.Errorf("General(%v,%d) = %q, want %q", c.n, c.w, got, c.want)
		}
	}
}

func TestSerials(t *testing.T) {
	cases := []struct {
		y, m, d int
		want    float64
	}{
		{1900, 1, 1, 1},
		{1900, 2, 28, 59},
		{1900, 2, 29, 60},
		{1900, 3, 1, 61},
		{1904, 1, 1, 1462},
		{2000, 1, 1, 36526},
		{2020, 1, 1, 43831},
		{9999, 12, 31, 2958465},
		{2020, 14, 1, 44228},
		{2020, 1, 32, 43862},
	}
	for _, c := range cases {
		if got := SerialFromDate(c.y, c.m, c.d, false); got != c.want {
			t.Errorf("SerialFromDate(%d,%d,%d) = %v, want %v", c.y, c.m, c.d, got, c.want)
		}
	}
	if SerialFromDate(1904, 1, 1, true) != 0 || SerialFromDate(2020, 1, 1, true) != 43831-1462 {
		t.Error("1904 serials")
	}
	y, m, d, hh, mm, ss, ms := DateFromSerial(43831.75+30.5/86400, false)
	if y != 2020 || m != 1 || d != 1 || hh != 18 || mm != 0 || ss != 30 || ms != 500 {
		t.Errorf("DateFromSerial: %d-%d-%d %d:%d:%d.%v", y, m, d, hh, mm, ss, ms)
	}
	if y, m, d, _, _, _, _ = DateFromSerial(60, false); y != 1900 || m != 2 || d != 29 {
		t.Errorf("serial 60: %d-%d-%d", y, m, d)
	}
	if y, m, d, _, _, _, _ = DateFromSerial(59, false); y != 1900 || m != 2 || d != 28 {
		t.Errorf("serial 59: %d-%d-%d", y, m, d)
	}
	if y, m, d, _, _, _, _ = DateFromSerial(61, false); y != 1900 || m != 3 || d != 1 {
		t.Errorf("serial 61: %d-%d-%d", y, m, d)
	}
	if y, m, d, _, _, _, _ = DateFromSerial(1, false); y != 1900 || m != 1 || d != 1 {
		t.Errorf("serial 1: %d-%d-%d", y, m, d)
	}
	if y, m, d, _, _, _, _ = DateFromSerial(0, true); y != 1904 || m != 1 || d != 1 {
		t.Errorf("serial 0, 1904: %d-%d-%d", y, m, d)
	}
	// A time just short of midnight rounds into the next day.
	if _, _, d, hh, _, _, _ = DateFromSerial(43831.99999999999, false); d != 2 || hh != 0 {
		t.Errorf("rounding at midnight: %d %d", d, hh)
	}
	// Every day round-trips.
	for s := 1.0; s < 2958465; s += 97 {
		y, m, d, _, _, _, _ := DateFromSerial(s, false)
		if got := SerialFromDate(y, m, d, false); got != s {
			t.Fatalf("round trip of %v: %d-%d-%d gives %v", s, y, m, d, got)
		}
	}
}
