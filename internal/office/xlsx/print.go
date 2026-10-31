package xlsx

import (
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

// What a sheet says about how it prints: its print area and titles, which are defined names of the workbook, and its
// page setup, margins, options, breaks, header and footer, which are elements of the worksheet.

// PageSetup is how a sheet prints.
type PageSetup struct {
	Landscape    bool
	Oriented     bool       // the file says which way the paper is turned
	PaperW       float64    // points, 0 when the file does not say
	PaperH       float64    //
	Scale        int        // per cent, 0 when not said
	FitToPage    bool       // scale to FitWidth by FitHeight pages rather than by Scale
	FitWidth     int        // pages across; 0 is as many as it takes
	FitHeight    int        // pages down; 0 is as many as it takes
	Margins      [4]float64 // left, top, right, bottom, in points
	HeaderAt     float64    // from the paper's top
	FooterAt     float64    // from the paper's bottom
	Grid         bool
	Headings     bool
	CenterH      bool
	CenterV      bool
	OverThenDown bool
	RowBreaks    []int // a page starts at each of these rows
	ColBreaks    []int // and at each of these columns
	Header       string
	Footer       string
	Areas        []formula.Area // the print area, empty for all that is used
	TitleRows    [2]int         // first and last row repeated on each page, -1 for none
	TitleCols    [2]int
}

// paperPoints are the paper sizes of ST_PaperSize this knows, by number.
var paperPoints = map[int][2]float64{
	1: {612, 792}, 3: {792, 1224}, 5: {612, 1008}, 7: {522, 756}, 8: {842, 1191}, 9: {595, 842}, 11: {420, 595},
	12: {729, 1032}, 13: {516, 729}, 66: {1191, 1684}, 70: {298, 420},
}

func attrFloat(v string, or float64) float64 {
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return or
}

func attrOn(v string) bool { return v == "1" || v == "true" }

// PrintSetup reads how the sheet prints.
func (s *Sheet) PrintSetup() PageSetup {
	const inch = 72
	p := PageSetup{TitleRows: [2]int{-1, -1}, TitleCols: [2]int{-1, -1}, Margins: [4]float64{0.7 * inch, 0.75 * inch, 0.7 * inch, 0.75 * inch},
		HeaderAt: 0.3 * inch, FooterAt: 0.3 * inch}
	root := s.doc.Root
	if m := root.Child("pageMargins"); m != nil {
		p.Margins = [4]float64{attrFloat(m.Attr("left"), 0.7) * inch, attrFloat(m.Attr("top"), 0.75) * inch,
			attrFloat(m.Attr("right"), 0.7) * inch, attrFloat(m.Attr("bottom"), 0.75) * inch}
		p.HeaderAt = attrFloat(m.Attr("header"), 0.3) * inch
		p.FooterAt = attrFloat(m.Attr("footer"), 0.3) * inch
	}
	if o := root.Child("printOptions"); o != nil {
		p.Grid = attrOn(o.Attr("gridLines"))
		p.Headings = attrOn(o.Attr("headings"))
		p.CenterH = attrOn(o.Attr("horizontalCentered"))
		p.CenterV = attrOn(o.Attr("verticalCentered"))
	}
	if u := root.Path("sheetPr", "pageSetUpPr"); u != nil {
		p.FitToPage = attrOn(u.Attr("fitToPage"))
	}
	if ps := root.Child("pageSetup"); ps != nil {
		switch ps.Attr("orientation") {
		case "landscape":
			p.Landscape, p.Oriented = true, true
		case "portrait":
			p.Oriented = true
		}
		if size, ok := paperPoints[int(attrFloat(ps.Attr("paperSize"), 0))]; ok {
			p.PaperW, p.PaperH = size[0], size[1]
		}
		p.Scale = int(attrFloat(ps.Attr("scale"), 0))
		p.FitWidth = int(attrFloat(ps.Attr("fitToWidth"), 1))
		p.FitHeight = int(attrFloat(ps.Attr("fitToHeight"), 1))
		p.OverThenDown = ps.Attr("pageOrder") == "overThenDown"
	} else {
		p.FitWidth, p.FitHeight = 1, 1
	}
	breaks := func(name string) []int {
		var out []int
		for _, b := range root.Child(name).Elements("brk") {
			// The id is the row or column after which the page ends, counted from one.
			if v := int(attrFloat(b.Attr("id"), 0)); v > 0 {
				out = append(out, v)
			}
		}
		return out
	}
	p.RowBreaks, p.ColBreaks = breaks("rowBreaks"), breaks("colBreaks")
	if hf := root.Child("headerFooter"); hf != nil {
		if n := hf.Child("oddHeader"); n != nil {
			p.Header = n.InnerText()
		}
		if n := hf.Child("oddFooter"); n != nil {
			p.Footer = n.InnerText()
		}
	}
	index := -1
	for i, k := range s.wb.Sheets {
		if k == s {
			index = i
		}
	}
	for _, e := range s.wb.doc.Root.Child("definedNames").Elements("definedName") {
		if e.Attr("localSheetId") != strconv.Itoa(index) {
			continue
		}
		switch e.Attr("name") {
		case "_xlnm.Print_Area":
			for _, part := range strings.Split(e.InnerText(), ",") {
				if a, ok := parsePrintArea(part); ok {
					p.Areas = append(p.Areas, a)
				}
			}
		case "_xlnm.Print_Titles":
			for _, part := range strings.Split(e.InnerText(), ",") {
				if a, ok := parsePrintArea(part); ok {
					if a.C1 == 0 && a.C2 == MaxCols-1 {
						p.TitleRows = [2]int{int(a.R1), int(a.R2)}
					} else if a.R1 == 0 && a.R2 == MaxRows-1 {
						p.TitleCols = [2]int{int(a.C1), int(a.C2)}
					}
				}
			}
		}
	}
	return p
}

// parsePrintArea reads Sheet1!$A$1:$C$9, or a whole-row or whole-column range such as $1:$2 or $A:$B.
func parsePrintArea(s string) (formula.Area, bool) {
	s = strings.TrimSpace(s)
	if at := strings.LastIndexByte(s, '!'); at >= 0 {
		s = s[at+1:]
	}
	s = strings.ReplaceAll(s, "$", "")
	first, last, two := strings.Cut(s, ":")
	if !two {
		last = first
	}
	c1, r1, ok1 := printCell(first)
	c2, r2, ok2 := printCell(last)
	if !ok1 || !ok2 {
		return formula.Area{}, false
	}
	if c1 < 0 && c2 < 0 {
		c1, c2 = 0, MaxCols-1
	}
	if r1 < 0 && r2 < 0 {
		r1, r2 = 0, MaxRows-1
	}
	if c1 < 0 || c2 < 0 || r1 < 0 || r2 < 0 || c1 > c2 || r1 > r2 || c2 >= MaxCols || r2 >= MaxRows {
		return formula.Area{}, false
	}
	return formula.Area{R1: int32(r1), C1: int32(c1), R2: int32(r2), C2: int32(c2)}, true
}

// printCell is the column and row of A1, either of which may be missing (-1).
func printCell(s string) (col, row int, ok bool) {
	col, row = -1, -1
	i := 0
	v := 0
	for i < len(s) {
		c := s[i] &^ 0x20
		if c < 'A' || c > 'Z' {
			break
		}
		v = v*26 + int(c-'A') + 1
		if v > MaxCols {
			return 0, 0, false
		}
		i++
	}
	if i > 0 {
		col = v - 1
	}
	if i < len(s) {
		n, err := strconv.Atoi(s[i:])
		if err != nil || n < 1 {
			return 0, 0, false
		}
		row = n - 1
	}
	return col, row, i > 0 || i < len(s)
}
