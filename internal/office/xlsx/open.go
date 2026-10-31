package xlsx

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/btelolabs/btelo-forge/internal/office/dml"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Part types.
const (
	ctWorkbook  = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
	ctMacro     = "application/vnd.ms-excel.sheet.macroEnabled.main+xml"
	ctWorksheet = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"
	ctStrings   = "application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"
	ctStyles    = "application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"
	ctTheme     = "application/vnd.openxmlformats-officedocument.theme+xml"

	relWorksheet = opc.DocRel + "worksheet"
	relStrings   = opc.DocRel + "sharedStrings"
	relStyles    = opc.DocRel + "styles"
	relTheme     = opc.DocRel + "theme"
	relCalcChain = opc.DocRel + "calcChain"
	relTable     = opc.DocRel + "table"
	relDrawing   = opc.DocRel + "drawing"
	relComments  = opc.DocRel + "comments"
	relVML       = opc.DocRel + "vmlDrawing"
	relChart     = opc.DocRel + "chart"
	nsR          = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
)

// Schema orders.
var (
	worksheetOrder = []string{"sheetPr", "dimension", "sheetViews", "sheetFormatPr", "cols", "sheetData", "sheetCalcPr", "sheetProtection",
		"protectedRanges", "scenarios", "autoFilter", "sortState", "dataConsolidate", "customSheetViews", "mergeCells", "phoneticPr",
		"conditionalFormatting", "dataValidations", "hyperlinks", "printOptions", "pageMargins", "pageSetup", "headerFooter", "rowBreaks",
		"colBreaks", "customProperties", "cellWatches", "ignoredErrors", "smartTags", "drawing", "legacyDrawing", "legacyDrawingHF",
		"drawingHF", "picture", "oleObjects", "controls", "webPublishItems", "tableParts", "extLst"}
	workbookOrder = []string{"fileVersion", "fileSharing", "workbookPr", "workbookProtection", "bookViews", "sheets", "functionGroups",
		"externalReferences", "definedNames", "calcPr", "oleSize", "customWorkbookViews", "pivotCaches", "smartTagPr", "smartTagTypes",
		"webPublishing", "fileRecoveryPr", "webPublishObjects", "extLst"}
	sheetPrOrder = []string{"tabColor", "outlinePr", "pageSetUpPr"}
)

// eagerFormulas is how many formulas Open works out itself when the file asks for it.
const eagerFormulas = 20000

const rowsMark = "<sheetData><!--forge:rows--></sheetData>"

// Open reads a workbook. Formulas are made ready to recalculate; a value the file does not hold is worked out here.
func Open(data []byte) (*Workbook, error) {
	pkg, err := opc.Open(data)
	if err != nil {
		return nil, err
	}
	return load(pkg)
}

func load(pkg *opc.Package) (wb *Workbook, err error) {
	defer func() {
		if r := recover(); r != nil {
			wb, err = nil, fmt.Errorf("the workbook could not be read: %v", r)
		}
	}()
	rels, err := pkg.Rels("")
	if err != nil {
		return nil, err
	}
	rel, ok := opc.RelOfType(rels, opc.RelOfficeDocument)
	if !ok {
		return nil, opc.ErrNotOffice
	}
	part := opc.Resolve("", rel.Target)
	doc, err := pkg.Doc(part)
	if err != nil {
		return nil, err
	}
	if doc.Root.Name != "workbook" {
		return nil, opc.ErrNotOffice
	}
	wb = &Workbook{pkg: pkg, part: part, doc: doc, gen: 1, names: map[string]string{}}
	if v, ok := doc.Root.Child("workbookPr").Lookup("date1904"); ok {
		wb.date = v == "1" || v == "true"
	}
	wbRels, err := pkg.Rels(part)
	if err != nil {
		return nil, err
	}
	var theme []byte
	if r, ok := opc.RelOfType(wbRels, relTheme); ok {
		if p := pkg.Part(opc.Resolve(part, r.Target)); p != nil {
			theme = p.Data
		}
	}
	stylesPart := ""
	if r, ok := opc.RelOfType(wbRels, relStyles); ok {
		stylesPart = opc.Resolve(part, r.Target)
	}
	wb.scheme = dml.ParseScheme(theme)
	if wb.Styles, err = loadStyles(pkg, stylesPart, part, theme); err != nil {
		return nil, err
	}
	wb.strs = &strTable{}
	if r, ok := opc.RelOfType(wbRels, relStrings); ok {
		if p := pkg.Part(opc.Resolve(part, r.Target)); p != nil {
			wb.strs.part = p.Name
			if err := wb.strs.load(p.Data); err != nil {
				return nil, fmt.Errorf("%s: %w", p.Name, err)
			}
		}
	}
	sheets := doc.Root.Child("sheets").Elements("sheet")
	if len(sheets) == 0 {
		return nil, errors.New("the workbook has no sheets")
	}
	if len(sheets) > 4096 {
		return nil, errors.New("the workbook has too many sheets")
	}
	limit := int64(maxDenseRows)
	var mu sync.Mutex
	var first error
	workers := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for _, e := range sheets {
		s := &Sheet{wb: wb, Name: e.Attr("name"), id: e.Attr("sheetId"), rid: e.Attr("r:id"), hidden: e.Attr("state") != "" && e.Attr("state") != "visible"}
		wb.Sheets = append(wb.Sheets, s)
		r, ok := opc.RelByID(wbRels, s.rid)
		if !ok || r.External {
			// A chart sheet or macro sheet has another relationship type; see below. A missing one is broken.
			return nil, fmt.Errorf("the sheet %q has no part", s.Name)
		}
		s.part = opc.Resolve(part, r.Target)
		if r.Type != relWorksheet && !strings.HasSuffix(r.Type, "/worksheet") {
			// A chart sheet stays in the file, not shown; nothing here can edit it.
			s.doc = &ox.Doc{Root: ox.New("worksheet")}
			s.hidden, s.chart = true, true
			s.sync()
			continue
		}
		p := pkg.Part(s.part)
		if p == nil {
			return nil, fmt.Errorf("the part %s is missing", s.part)
		}
		wg.Add(1)
		workers <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-workers }()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					first = fmt.Errorf("%s: %v", s.part, r)
					mu.Unlock()
				}
			}()
			mu.Lock()
			room := limit
			mu.Unlock()
			before := room
			err := s.load(p.Data, &room)
			mu.Lock()
			limit -= before - room
			if err != nil && first == nil {
				first = fmt.Errorf("%s: %w", s.part, err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	wb.syncNames()
	wb.calc = formula.NewEngine(wb)
	wb.fillEngine(true)
	if calc := doc.Root.Child("calcPr"); calc.Attr("fullCalcOnLoad") == "1" || calc.Attr("fullCalcOnLoad") == "true" {
		wb.calc.TouchAll()
	}
	// Few formulas are worked out here; many are left to Calc, a little at a time, so the file opens at once.
	if wb.calc.Count() <= eagerFormulas {
		wb.calc.Recalc(0)
	}
	return wb, nil
}

// load reads a worksheet part: the sheetData by hand, the rest as a tree.
func (s *Sheet) load(data []byte, limit *int64) error {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	open, tagEnd, closeAt, closeEnd, prefix := findSheetData(data)
	if open < 0 {
		// A sheet without sheetData is not valid but harmless.
		doc, err := ox.Parse(data)
		if err != nil {
			return err
		}
		s.doc = doc
		if doc.Root.Name != "worksheet" {
			return errors.New("not a worksheet")
		}
		doc.Root.InsertOrdered(markNode(), worksheetOrder)
		s.touched = true
		s.sync()
		return nil
	}
	body := data[tagEnd:closeAt]
	if prefix != "" {
		// The tree drops a spreadsheet prefix; the rows have to read the same.
		body = bytes.ReplaceAll(body, []byte("<"+prefix+":"), []byte("<"))
		body = bytes.ReplaceAll(body, []byte("</"+prefix+":"), []byte("</"))
	}
	s.src = body
	rest := make([]byte, 0, open+len(rowsMark)+len(data)-closeEnd)
	rest = append(append(append(rest, data[:open]...), rowsMark...), data[closeEnd:]...)
	if prefix != "" {
		rest = bytes.Replace(rest, []byte(rowsMark), []byte("<"+prefix+":sheetData><!--forge:rows--></"+prefix+":sheetData>"), 1)
	}
	doc, err := ox.Parse(rest)
	if err != nil {
		return err
	}
	if doc.Root.Name != "worksheet" {
		return errors.New("not a worksheet")
	}
	s.doc = doc
	if len(body) > 1<<32-1 {
		return errors.New("the worksheet is too large")
	}
	if err := s.scanRows(limit); err != nil {
		return err
	}
	s.sync()
	return nil
}

func markNode() *ox.Node {
	n := ox.New("sheetData")
	n.Append(&ox.Node{Kind: ox.Comment, Value: "forge:rows"})
	return n
}

// findSheetData finds the sheetData element at the top of the worksheet: where its start tag begins and ends, where
// its end tag begins and ends (for <sheetData/>, the same place). open is -1 when there is none.
func findSheetData(data []byte) (open, tagEnd, closeAt, closeEnd int, prefix string) {
	z := &scanner{b: data}
	depth := 0
	var attrs [32]attr
	for z.next() {
		if ok, err := z.skipMarkup(); err != nil {
			return -1, 0, 0, 0, ""
		} else if ok {
			continue
		}
		if z.b[z.i+1] == '/' {
			depth--
			e := bytes.IndexByte(z.b[z.i:], '>')
			if e < 0 {
				return -1, 0, 0, 0, ""
			}
			z.i += e + 1
			continue
		}
		start := z.i
		name, _, closed, err := z.tag(attrs[:0])
		if err != nil {
			return -1, 0, 0, 0, ""
		}
		local := name
		p := ""
		if k := bytes.IndexByte(name, ':'); k >= 0 {
			p, local = string(name[:k]), name[k+1:]
		}
		if depth == 1 && string(local) == "sheetData" {
			if closed {
				return start, z.i, z.i, z.i, p
			}
			end := "</sheetData>"
			if p != "" {
				end = "</" + p + ":sheetData>"
			}
			// The last, as nothing inside can contain it; searching from the end is quick on a huge sheet.
			e := bytes.LastIndex(data, []byte(end))
			if e < z.i {
				return -1, 0, 0, 0, ""
			}
			return start, z.i, e, e + len(end), p
		}
		if !closed {
			depth++
		}
	}
	return -1, 0, 0, 0, ""
}

// sync makes the ready-to-use values from the tree.
func (s *Sheet) sync() {
	root := s.doc.Root
	s.defH, s.defW = 15, 0
	if f := root.Child("sheetFormatPr"); f != nil {
		if v, err := strconv.ParseFloat(f.Attr("defaultRowHeight"), 64); err == nil && v > 0 && v <= 8192 {
			s.defH = v
		}
		if v, err := strconv.ParseFloat(f.Attr("defaultColWidth"), 64); err == nil && v > 0 && v <= 255 {
			s.defW = v
		}
		if s.defW == 0 {
			if v, err := strconv.ParseFloat(f.Attr("baseColWidth"), 64); err == nil && v >= 0 && v <= 255 {
				s.defW = defaultWidth(v)
			}
		}
	}
	if s.defW == 0 {
		s.defW = defaultWidth(8)
	}
	s.cols = s.cols[:0]
	for _, e := range root.Child("cols").Elements("col") {
		lo, _ := strconv.Atoi(e.Attr("min"))
		hi, _ := strconv.Atoi(e.Attr("max"))
		if lo < 1 || hi < lo || lo > MaxCols {
			continue
		}
		c := Col{Min: lo - 1, Max: min(hi, MaxCols) - 1, Width: -1}
		if v, err := strconv.ParseFloat(e.Attr("width"), 64); err == nil && v >= 0 && v <= 255 {
			c.Width = v
		}
		c.Hidden = boolAttr(e, "hidden")
		c.Custom = boolAttr(e, "customWidth")
		if n, err := strconv.Atoi(e.Attr("style")); err == nil {
			c.Style = int32(n)
		}
		s.cols = append(s.cols, c)
	}
	s.merges = s.merges[:0]
	for _, e := range root.Child("mergeCells").Elements("mergeCell") {
		if a, ok := formula.ParseArea(e.Attr("ref")); ok {
			s.merges = append(s.merges, a)
		}
	}
	s.indexMerges()
	s.freezeR, s.freezeC = 0, 0
	if p := root.Path("sheetViews", "sheetView", "pane"); p != nil && (p.Attr("state") == "frozen" || p.Attr("state") == "frozenSplit") {
		s.freezeC, _ = strconv.Atoi(p.Attr("xSplit"))
		s.freezeR, _ = strconv.Atoi(p.Attr("ySplit"))
		s.freezeC, s.freezeR = min(max(s.freezeC, 0), MaxCols-1), min(max(s.freezeR, 0), MaxRows-1)
	}
	s.tab = ""
	if t := root.Path("sheetPr", "tabColor"); t != nil {
		s.tab = s.wb.Styles.resolve(colorOf(t))
	}
}

// defaultWidth is the width of a column nothing is said about, from the sheet's base width in digits: the digits, five
// pixels of padding and gridline, rounded up to a multiple of eight pixels.
func defaultWidth(base float64) float64 {
	return math.Ceil((base*7+5)/8) * 8 / 7
}

func (s *Sheet) indexMerges() {
	s.mergeIdx = make(map[int32][]int32, len(s.merges))
	for i, m := range s.merges {
		for b := m.R1 >> 6; b <= m.R2>>6; b++ {
			s.mergeIdx[b] = append(s.mergeIdx[b], int32(i))
		}
	}
}

func boolAttr(e *ox.Node, name string) bool {
	v := e.Attr(name)
	return v == "1" || v == "true"
}

// syncNames reads the defined names.
func (wb *Workbook) syncNames() {
	clear(wb.names)
	for _, e := range wb.doc.Root.Child("definedNames").Elements("definedName") {
		key := lower(e.Attr("name"))
		if v, ok := e.Lookup("localSheetId"); ok {
			if n, err := strconv.Atoi(v); err == nil {
				key = localName(n, key)
			}
		}
		wb.names[key] = e.InnerText()
	}
}

func localName(sheet int, lowerName string) string { return strconv.Itoa(sheet) + "!" + lowerName }

func lower(s string) string { return strings.ToLower(s) }

func equalFold(a, b string) bool { return strings.EqualFold(a, b) }

// fillEngine registers every formula. missing also marks those that have no stored value.
func (wb *Workbook) fillEngine(missing bool) {
	wb.calc.Reset()
	for si, s := range wb.Sheets {
		for r, row := range s.rows {
			if row == nil {
				continue
			}
			for i := range row.Cells {
				c := &row.Cells[i]
				if c.F == nil || c.F.Expr == nil {
					continue
				}
				at := formula.Cell{Sheet: int32(si), Row: int32(r), Col: c.Col}
				wb.calc.Set(at, c.F.Expr, int(c.F.DR), int(c.F.DC), c.F.Array != "")
				if missing && c.Kind == Empty && c.F.Expr.Known() {
					wb.calc.Touch(at)
				}
			}
		}
	}
}

// strTable is the shared strings. Those in the file are never rewritten: new ones are added at the end.
type strTable struct {
	part  string
	data  []byte
	items []strItem
	base  int // how many are in the file
	index map[string]int32
}

type strItem struct {
	text    string
	off, n  uint32 // the <si> element in data
	rich    bool
	checked bool
}

// Text is a shared string, "" for a number out of range.
func (t *strTable) text(i int) string {
	if i < 0 || i >= len(t.items) {
		return ""
	}
	return t.items[i].text
}

func (t *strTable) load(data []byte) error {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	t.data = data
	if len(data) > 1<<32-1 {
		return errors.New("too large")
	}
	z := &scanner{b: data}
	var attrs [8]attr
	for z.next() {
		if ok, err := z.skipMarkup(); err != nil {
			return err
		} else if ok {
			continue
		}
		if z.b[z.i+1] == '/' {
			e := bytes.IndexByte(z.b[z.i:], '>')
			if e < 0 {
				return errBadXML
			}
			z.i += e + 1
			continue
		}
		start := z.i
		name, _, closed, err := z.tag(attrs[:0])
		if err != nil {
			return err
		}
		if k := bytes.IndexByte(name, ':'); k >= 0 {
			name = name[k+1:]
		}
		if string(name) == "sst" {
			continue
		}
		if string(name) != "si" {
			if !closed {
				if err := z.skipTo(name); err != nil {
					return err
				}
			}
			continue
		}
		it := strItem{off: uint32(start)}
		if !closed {
			text, err := z.inlineText()
			if err != nil {
				return err
			}
			it.text = decodeXString(text)
		}
		it.n = uint32(z.i - start)
		it.rich = bytes.Contains(data[start:z.i], []byte("<r>")) || bytes.Contains(data[start:z.i], []byte("<r "))
		t.items = append(t.items, it)
	}
	t.base = len(t.items)
	return nil
}

// add is the index of the text, added at the end when it is new.
func (t *strTable) add(text string) int32 {
	if t.index == nil {
		t.index = make(map[string]int32, len(t.items))
		for i := range t.items {
			if !t.items[i].rich {
				t.index[t.items[i].text] = int32(i)
			}
		}
	}
	if i, ok := t.index[text]; ok {
		return i
	}
	i := int32(len(t.items))
	t.items = append(t.items, strItem{text: text})
	t.index[text] = i
	return i
}

// Run is a stretch of a rich string with its own format.
type Run struct {
	Text    string
	Bold    bool
	Italic  bool
	Size    float64
	Font    string
	Color   string // RRGGBB, "" for the cell's
	Under   bool
	Strike  bool
	HasProp bool
}

// Runs are a shared string's formatted stretches, nil for a plain one.
func (wb *Workbook) Runs(c *Cell) []Run {
	if c.Kind != SharedS {
		return nil
	}
	t := wb.strs
	i := int(c.Num)
	if i < 0 || i >= len(t.items) || !t.items[i].rich {
		return nil
	}
	it := &t.items[i]
	doc, err := ox.Parse(t.data[it.off : it.off+it.n])
	if err != nil {
		return nil
	}
	var out []Run
	for _, e := range doc.Root.Elements("r") {
		r := Run{Text: decodeXString(e.Child("t").InnerText())}
		if p := e.Child("rPr"); p != nil {
			r.HasProp = true
			r.Bold, r.Italic = present(p, "b"), present(p, "i")
			r.Under, r.Strike = p.Child("u") != nil, present(p, "strike")
			if sz, err := strconv.ParseFloat(p.Child("sz").Attr("val"), 64); err == nil {
				r.Size = sz
			}
			r.Font = p.Child("rFont").Attr("val")
			if col := p.Child("color"); col != nil {
				r.Color = wb.Styles.resolve(colorOf(col))
			}
		}
		out = append(out, r)
	}
	return out
}

// present reads a boolean child element, on unless val says not.
func present(p *ox.Node, name string) bool {
	e := p.Child(name)
	if e == nil {
		return false
	}
	v, ok := e.Lookup("val")
	return !ok || v == "1" || v == "true"
}

// validSheetName says what is wrong with a sheet's name, or nil.
func validSheetName(wb *Workbook, name string, self *Sheet) error {
	if name == "" || len([]rune(name)) > 31 {
		return errors.New("a sheet's name has 1 to 31 characters")
	}
	if strings.ContainsAny(name, `\/?*[]:`) || name[0] == '\'' || name[len(name)-1] == '\'' {
		return errors.New(`a sheet's name cannot contain \ / ? * [ ] : or start or end with '`)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("a sheet's name cannot contain control characters")
		}
	}
	if o := wb.Sheet(name); o != nil && o != self {
		return errors.New("a sheet of that name exists")
	}
	return nil
}
