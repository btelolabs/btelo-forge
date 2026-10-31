package xlsx

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

func openCSV(t *testing.T, data []byte, tab bool) (*Workbook, *Delimited) {
	t.Helper()
	wb, d, err := OpenDelimited(data, "Sheet1", tab)
	if err != nil {
		t.Fatal(err)
	}
	return wb, d
}

func TestAnUntouchedFileComesBackTheSame(t *testing.T) {
	gbk, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("姓名,金额\r\n张三,1.50\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	utf16le := []byte{0xff, 0xfe, 'a', 0, ',', 0, 'b', 0, '\n', 0, '1', 0, ',', 0, '2', 0, '\n', 0}
	for name, data := range map[string][]byte{
		"plain":      []byte("a,b,c\n1,2,3\n"),
		"no end":     []byte("a,b\n1,2"),
		"crlf":       []byte("a,b\r\n1,2\r\n"),
		"bom":        []byte("\xef\xbb\xbfa,b\n中,文\n"),
		"gbk":        gbk,
		"utf16":      utf16le,
		"semicolons": []byte("a;b;c\n1,5;2,5;3,5\n"),
		"sep line":   []byte("sep=;\na;b\n1;2\n"),
		"quoted":     []byte("a,b\n\"x,y\",\"line\nbreak\"\n\"say \"\"hi\"\"\",z\n"),
		"codes":      []byte("id,phone\n007,13800138000\n12345678901234567890,+86\n"),
		"numbers":    []byte("v\n1.50\n1e3\n0.10\n-0.5\n"),
		"dates":      []byte("d\n2024-01-02\n1/2\n3-4\n"),
		"formula":    []byte("f\n=1+1\n\"=HYPERLINK(\"\"x\"\")\"\n"),
		"uneven":     []byte("a,b,c\n1\n1,2\n"),
		"empties":    []byte("a,b,,\n1,,,\n,,,\n"),
	} {
		t.Run(name, func(t *testing.T) {
			wb, d := openCSV(t, data, false)
			got, err := d.Bytes(wb)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("came back as\n%q, was\n%q", got, data)
			}
		})
	}
}

func TestWordsThatLookLikeNumbersStayWords(t *testing.T) {
	wb, _ := openCSV(t, []byte("007,12345678901234567890,1.5,=1+1,2024-01-02,-3\n"), false)
	s := wb.Sheets[0]
	for c, kind := range []formula.Kind{formula.Text, formula.Text, formula.Number, formula.Text, formula.Text, formula.Number} {
		if got := s.ValueAt(0, c); got.Kind != kind {
			t.Fatalf("column %d is %v", c, got)
		}
	}
}

func TestTheSeparatorIsFound(t *testing.T) {
	for text, want := range map[string]rune{"a,b\n1,2\n": ',', "a\tb\n1\t2\n": '\t', "a;b\n1;2\n": ';', "a|b\n1|2\n": '|', "one\ntwo\n": ',', "\"a;b\",c\n\"1;2\",3\n": ','} {
		if got := separator(text); got != want {
			t.Fatalf("%q: %q", text, got)
		}
	}
	if _, d := openCSV(t, []byte("one\ntwo\n"), true); d.Comma != '\t' {
		t.Fatalf("%q", d.Comma)
	}
}

func TestEditsAreWrittenAsTheFileWasWritten(t *testing.T) {
	wb, d := openCSV(t, []byte("名称;价格\r\n苹果;1.50\r\n"), false)
	s := wb.Sheets[0]
	if err := s.SetInput(1, 0, "梨;红"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetInput(2, 1, "=B2*2"); err != nil {
		t.Fatal(err)
	}
	wb.Calc(0)
	got, err := d.Bytes(wb)
	if err != nil {
		t.Fatal(err)
	}
	want := "名称;价格\r\n\"梨;红\";1.50\r\n;3\r\n"
	if string(got) != want {
		t.Fatalf("%q", got)
	}
}

func TestAnEmptyFileOpens(t *testing.T) {
	wb, d := openCSV(t, nil, false)
	got, err := d.Bytes(wb)
	if err != nil || len(got) != 0 {
		t.Fatalf("%q %v", got, err)
	}
}

func TestDamagedTextDoesNotStopAFile(t *testing.T) {
	data := []byte("a,\xff\xfe\x00b\n\x00,<&>\n")
	wb, d := openCSV(t, append([]byte("x,"), data...), false)
	got, err := d.Bytes(wb)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "<&>") {
		t.Fatalf("%q", got)
	}
}
