package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
)

func testFonts(t testing.TB) flow.Fonts {
	t.Helper()
	fonts, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skip(err)
	}
	return flow.NewBook(fonts)
}

func blankDoc(t testing.TB) *Doc {
	t.Helper()
	data, err := Blank()
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(data, testFonts(t))
	if err != nil {
		t.Fatal(err)
	}
	d.Layout(0)
	return d
}

func bodyXML(d *Doc) string { return string(d.Main.X.Bytes()) }

func TestBlankLaysOutOnePage(t *testing.T) {
	d := blankDoc(t)
	if n := len(d.Pages()); n != 1 {
		t.Fatalf("pages = %d", n)
	}
	pg := d.Pages()[0]
	if pg.W < 590 || pg.W > 600 || pg.H < 840 || pg.H > 845 {
		t.Fatalf("not A4: %v x %v", pg.W, pg.H)
	}
	if len(pg.Pieces) != 1 {
		t.Fatalf("pieces = %d", len(pg.Pieces))
	}
}

func TestTypingAndUndoRoundTrip(t *testing.T) {
	d := blankDoc(t)
	before := bodyXML(d)
	d.Type("Hello ")
	d.Type("世界")
	d.Enter()
	d.Type("Second")
	d.Layout(0)
	after := bodyXML(d)
	if !strings.Contains(after, "Hello 世界") || !strings.Contains(after, "Second") {
		t.Fatalf("typed text missing: %s", after)
	}
	for d.Undo() {
	}
	d.Layout(0)
	if got := bodyXML(d); got != before {
		t.Fatalf("undo left\n%s\nwanted\n%s", got, before)
	}
	for d.Redo() {
	}
	d.Layout(0)
	if got := bodyXML(d); got != after {
		t.Fatalf("redo left\n%s\nwanted\n%s", got, after)
	}
	if len(d.Body.flat) != 2 || d.Sel.Caret.B.Text() != "Second" || d.Sel.Caret.Off != 6 {
		t.Fatalf("caret after redo: %q %d", d.Sel.Caret.B.Text(), d.Sel.Caret.Off)
	}
}

func para(d *Doc, i int) *Block { return d.Body.flat[i] }

func TestPagination(t *testing.T) {
	d := blankDoc(t)
	for i := 0; i < 200; i++ {
		d.Insert(fmt.Sprintf("Paragraph number %d with some more words so that a line has a fair length to it and wraps over.\n", i))
	}
	d.Finish()
	n := len(d.Pages())
	if n < 3 {
		t.Fatalf("pages = %d", n)
	}
	// Every line is on one page, in order, inside the margins.
	seen := map[*Block]int{}
	for _, pg := range d.Pages() {
		for i := range pg.Pieces {
			p := &pg.Pieces[i]
			seen[p.B] += p.To - p.From
			bottom := p.Y + lineSum(p.L, p.From, p.To)
			if bottom > pg.Bottom+0.1 {
				t.Fatalf("page %d: piece ends at %v past %v", pg.Index, bottom, pg.Bottom)
			}
		}
	}
	for _, b := range d.Body.flat {
		if want := len(b.para().laid.Lines); seen[b] != want {
			t.Fatalf("block %d: %d lines placed of %d", b.Flat, seen[b], want)
		}
	}
	// An edit on page one keeps later pages' objects: only what changed is poured again.
	last := d.Pages()[n-1]
	d.SetCaret(Pos{B: para(d, 0), Off: 0}, false)
	d.Type("X")
	d.Layout(0)
	if d.Pages()[n-1] != last {
		t.Fatalf("the last page was made again for a change on page one")
	}
	if got := len(d.Pages()); got != n {
		t.Fatalf("pages %d, was %d", got, n)
	}
	// A page-long insertion moves things on.
	d.Insert(strings.Repeat("Line of text that takes some room.\n", 80))
	d.Finish()
	if len(d.Pages()) <= n {
		t.Fatalf("pages did not grow: %d", len(d.Pages()))
	}
	for i := 0; i < 2; i++ {
		d.Undo()
	}
	d.Finish()
	if got := len(d.Pages()); got != n {
		t.Fatalf("after undo pages %d, want %d", got, n)
	}
}

func TestPageBreakAndKeepNext(t *testing.T) {
	d := blankDoc(t)
	d.Type("one")
	d.PageBreak()
	d.Finish()
	if n := len(d.Pages()); n != 2 {
		t.Fatalf("pages = %d", n)
	}
	if _, ok := d.Reach(d.Sel.Caret); !ok {
		t.Fatal("caret not found")
	}
	d.Undo()
	d.Finish()
	if n := len(d.Pages()); n != 1 {
		t.Fatalf("pages = %d", n)
	}
}

func TestTableOps(t *testing.T) {
	d := blankDoc(t)
	d.Type("before")
	if err := d.InsertTable(2, 3); err != nil {
		t.Fatal(err)
	}
	d.Finish()
	if !d.CaretInTable() {
		t.Fatal("caret not in the table")
	}
	d.Type("A1")
	d.NextCell(false)
	d.Type("B1")
	d.Finish()
	if err := d.AddRow(false); err != nil {
		t.Fatal(err)
	}
	if err := d.AddColumn(false); err != nil {
		t.Fatal(err)
	}
	d.Finish()
	x := bodyXML(d)
	if strings.Count(x, "<w:tr>") != 3 || strings.Count(x, "<w:gridCol") != 4 {
		t.Fatalf("rows or columns: %s", x)
	}
	tb := d.Sel.Caret.B.In.Cell.Row.Table
	if len(tb.Rows[0].Cells) != 4 {
		t.Fatalf("cells = %d", len(tb.Rows[0].Cells))
	}
	// Merge the first two cells of row 0.
	d.Sel = Selection{Anchor: Pos{B: tb.Rows[0].Cells[0].C.Blocks[0]}, Caret: Pos{B: tb.Rows[0].Cells[1].C.Blocks[0]}}
	if err := d.MergeCells(); err != nil {
		t.Fatal(err)
	}
	d.Finish()
	x = bodyXML(d)
	if !strings.Contains(x, `<w:gridSpan w:val="2"/>`) || !strings.Contains(x, "A1") || !strings.Contains(x, "B1") {
		t.Fatalf("merge: %s", x)
	}
	for d.Undo() {
	}
	d.Finish()
	if strings.Contains(bodyXML(d), "w:tbl") {
		t.Fatalf("table left after undo: %s", bodyXML(d))
	}
}

func TestDeleteAcrossParagraphs(t *testing.T) {
	d := blankDoc(t)
	d.Insert("aaaa\nbbbb\ncccc")
	d.Layout(0)
	d.Sel = Selection{Anchor: Pos{B: para(d, 0), Off: 2}, Caret: Pos{B: para(d, 2), Off: 2}}
	d.DeleteSelection()
	d.Layout(0)
	if len(d.Body.flat) != 1 || para(d, 0).Text() != "aacc" {
		t.Fatalf("got %d paragraphs, %q", len(d.Body.flat), para(d, 0).Text())
	}
	d.Undo()
	d.Layout(0)
	if len(d.Body.flat) != 3 || para(d, 2).Text() != "cccc" {
		t.Fatalf("undo: %d", len(d.Body.flat))
	}
}

func TestFormatSplitsRuns(t *testing.T) {
	d := blankDoc(t)
	d.Type("hello world")
	b := para(d, 0)
	d.Sel = Selection{Anchor: Pos{B: b, Off: 6}, Caret: Pos{B: b, Off: 11}}
	yes := true
	d.FormatRuns(RunChange{Bold: &yes})
	x := bodyXML(d)
	if !strings.Contains(x, `<w:rPr><w:b/><w:bCs/></w:rPr><w:t>world</w:t>`) || !strings.Contains(x, `<w:t xml:space="preserve">hello </w:t>`) {
		t.Fatalf("format: %s", x)
	}
	if !d.CaretRun().Bold {
		t.Fatal("caret state not bold")
	}
	// Pending format applies to what is typed next.
	d.SetCaret(Pos{B: b, Off: 5}, false)
	sz := float32(20)
	d.FormatRuns(RunChange{Size: &sz})
	if d.CaretRun().Size != 20 {
		t.Fatal("pending size not shown")
	}
	d.Type("Z")
	x = bodyXML(d)
	if !strings.Contains(x, `<w:sz w:val="40"/><w:szCs w:val="40"/></w:rPr><w:t>Z</w:t>`) {
		t.Fatalf("pending: %s", x)
	}
}

func TestLists(t *testing.T) {
	d := blankDoc(t)
	d.Insert("one\ntwo\nthree")
	d.Layout(0)
	d.Sel = Selection{Anchor: Pos{B: para(d, 0)}, Caret: Pos{B: para(d, 2), Off: 1}}
	d.ToggleList(ListNumber)
	d.Finish()
	for i, want := range []string{"1.", "2.", "3."} {
		if got := para(d, i).para().Label; got != want {
			t.Fatalf("para %d label %q", i, got)
		}
	}
	d.SetCaret(Pos{B: para(d, 1)}, false)
	d.Indent(true)
	d.Finish()
	if got := para(d, 1).para().Label; got != "a." {
		t.Fatalf("level 2 label %q", got)
	}
	if got := para(d, 2).para().Label; got != "2." {
		t.Fatalf("after sub item %q", got)
	}
	for d.Undo() {
	}
	d.Finish()
	if got := para(d, 0).para().Label; got != "" {
		t.Fatalf("undo left label %q", got)
	}
}

func TestFindReplace(t *testing.T) {
	d := blankDoc(t)
	d.Insert("Cat cat CAT\nthe cat sat")
	d.Layout(0)
	if n := len(d.FindAll("cat", false)); n != 4 {
		t.Fatalf("found %d", n)
	}
	if n := len(d.FindAll("cat", true)); n != 2 {
		t.Fatalf("found %d", n)
	}
	if n := d.ReplaceAll("cat", "dog", true); n != 2 {
		t.Fatalf("replaced %d", n)
	}
	if got := para(d, 1).Text(); got != "the dog sat" {
		t.Fatalf("text %q", got)
	}
	d.Undo()
	if got := para(d, 1).Text(); got != "the cat sat" {
		t.Fatalf("undo text %q", got)
	}
}

func TestPartsSurvive(t *testing.T) {
	data := fidelityFile(t)
	d, err := Open(data, testFonts(t))
	if err != nil {
		t.Fatal(err)
	}
	d.Layout(0)
	d.SetCaret(Pos{B: para(d, 0), Off: 3}, false)
	d.Type("XYZ")
	d.Layout(0)
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	in := unzip(t, data)
	got := unzip(t, out)
	for name, body := range in {
		if name == "word/document.xml" || name == "[Content_Types].xml" {
			continue
		}
		if !bytes.Equal(got[name], body) {
			t.Errorf("%s changed", name)
		}
	}
	x := string(got["word/document.xml"])
	for _, want := range []string{`<w:bookmarkStart w:id="1" w:name="mark"/>`, `<foo:unknown xmlns:foo="urn:foo" foo:a="1"><foo:b>kept</foo:b></foo:unknown>`,
		`<w:commentRangeStart w:id="0"/>`, `<w:del w:id="5" w:author="x"><w:r><w:delText>gone</w:delText></w:r></w:del>`,
		`<m:oMath xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"><m:r><m:t>x+1</m:t></m:r></m:oMath>`, `w:rsidR="00AB"`} {
		if !strings.Contains(x, want) {
			t.Errorf("lost %s in %s", want, x)
		}
	}
	if !strings.Contains(x, "HelXYZlo") {
		t.Errorf("edit missing: %s", x)
	}
}

func unzip(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		b.ReadFrom(r)
		r.Close()
		out[f.Name] = b.Bytes()
	}
	return out
}

func fidelityFile(t *testing.T) []byte {
	t.Helper()
	blank, err := Blank()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(blank), int64(len(blank)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, _ := zw.Create(f.Name)
		r, _ := f.Open()
		var b bytes.Buffer
		b.ReadFrom(r)
		r.Close()
		body := b.Bytes()
		if f.Name == "word/document.xml" {
			body = bytes.Replace(body, []byte(`<w:p/>`), []byte(`<w:p w:rsidR="00AB"><w:bookmarkStart w:id="1" w:name="mark"/><w:r><w:t>Hello</w:t></w:r><w:bookmarkEnd w:id="1"/><w:commentRangeStart w:id="0"/><w:r><w:t xml:space="preserve"> there</w:t></w:r><w:commentRangeEnd w:id="0"/><w:del w:id="5" w:author="x"><w:r><w:delText>gone</w:delText></w:r></w:del><foo:unknown xmlns:foo="urn:foo" foo:a="1"><foo:b>kept</foo:b></foo:unknown><m:oMath xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"><m:r><m:t>x+1</m:t></m:r></m:oMath></w:p>`), 1)
		}
		w.Write(body)
	}
	cw, _ := zw.Create("customXml/item1.xml")
	cw.Write([]byte(`<data>opaque</data>`))
	ew, _ := zw.Create("word/embeddings/thing.bin")
	ew.Write([]byte{1, 2, 3, 4})
	zw.Close()
	return buf.Bytes()
}
