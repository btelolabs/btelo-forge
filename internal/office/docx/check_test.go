package docx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/office/flow"
)

// TestWriteCheckFiles writes the files tools/officecheck/docx_check.py reads, into FORGE_WORD_DIR: a blank document, a
// document built by editing, and the rich file it finds there after edits.
func TestWriteCheckFiles(t *testing.T) {
	dir := os.Getenv("FORGE_WORD_DIR")
	if dir == "" {
		t.Skip("set FORGE_WORD_DIR")
	}
	data, err := Blank()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blank.docx"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	d := blankDoc(t)
	built(t, d)
	save(t, d, filepath.Join(dir, "built.docx"))
	src, err := os.ReadFile(filepath.Join(dir, "rich.docx"))
	if err != nil {
		t.Fatal(err)
	}
	d, err = Open(src, testFonts(t))
	if err != nil {
		t.Fatal(err)
	}
	d.Finish()
	editRich(t, d)
	save(t, d, filepath.Join(dir, "rich-edited.docx"))
}

func save(t *testing.T, d *Doc, path string) {
	t.Helper()
	data, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// built makes a document using every kind of edit.
func built(t *testing.T, d *Doc) {
	yes := true
	d.Insert("Heading\nBody text with words\n")
	d.Sel = Selection{Anchor: Pos{B: para(d, 0)}, Caret: Pos{B: para(d, 0), Off: 1}}
	d.SetStyle("Heading1")
	b := para(d, 1)
	d.Sel = Selection{Anchor: Pos{B: b, Off: 5}, Caret: Pos{B: b, Off: 9}}
	size, col, hi, family := float32(14), "FF0000", "yellow", "Arial"
	up := int8(1)
	d.FormatRuns(RunChange{Bold: &yes, Italic: &yes, Underline: &yes, Strike: &yes, Size: &size, Color: &col, Highlight: &hi, Family: &family, Vert: &up})
	d.SetAlign(flow.Justify)
	d.SetLineSpacing(1.5)
	d.SetSpacing(6, 12)
	d.SetIndent(18, -9, true)
	d.Sel = Selection{Anchor: Pos{B: para(d, 2)}, Caret: Pos{B: para(d, 2)}}
	d.Insert("one\ntwo\nthree")
	d.Sel = Selection{Anchor: Pos{B: para(d, 2)}, Caret: Pos{B: para(d, 4), Off: 1}}
	d.ToggleList(ListNumber)
	d.SetCaret(Pos{B: para(d, 3)}, false)
	d.Indent(true)
	last := d.Body.flat[len(d.Body.flat)-1]
	d.SetCaret(Pos{B: last, Off: last.para().Len}, false)
	d.Enter()
	d.ToggleList(ListBullet)
	d.Type("bullet")
	d.Enter()
	d.Enter() // ends the list
	d.Type("a link here")
	b = d.Sel.Caret.B
	d.Sel = Selection{Anchor: Pos{B: b, Off: 2}, Caret: Pos{B: b, Off: 6}}
	if err := d.SetLink("https://example.com/x?a=1&b=2", ""); err != nil {
		t.Fatal(err)
	}
	d.SetCaret(Pos{B: b, Off: b.para().Len}, false)
	d.PageBreak()
	if err := d.InsertPicture(samplePNG(t, 200, 100), "pic.png", 0, 0); err != nil {
		t.Fatal(err)
	}
	d.Enter()
	if err := d.InsertTable(3, 4); err != nil {
		t.Fatal(err)
	}
	d.Finish()
	for i := 0; i < 5; i++ {
		d.Type("c")
		d.NextCell(false)
	}
	if err := d.AddRow(true); err != nil {
		t.Fatal(err)
	}
	if err := d.AddColumn(true); err != nil {
		t.Fatal(err)
	}
	d.Finish()
	tb := d.Sel.Caret.B.In.Cell.Row.Table
	if err := d.MergeRange(tb, 1, 2, 1, 3); err != nil {
		t.Fatal(err)
	}
	d.Finish()
	d.MoveDoc(true, false)
	pg := d.Pages()[0]
	st := d.HeaderStory(pg, "header")
	if st == nil {
		t.Fatal("no header")
	}
	d.Sel = Selection{Anchor: Pos{B: st.flat[0]}, Caret: Pos{B: st.flat[0]}}
	d.Type("Header text ")
	d.InsertField("PAGE", "1")
	st = d.HeaderStory(pg, "footer")
	d.Sel = Selection{Anchor: Pos{B: st.flat[0]}, Caret: Pos{B: st.flat[0]}}
	d.Type("Footer")
	d.Sel = Selection{Anchor: Pos{B: d.Body.flat[0]}, Caret: Pos{B: d.Body.flat[0]}}
	d.Finish()
}

// editRich makes a few edits in a file python-docx wrote, for the check that nothing else changed.
func editRich(t *testing.T, d *Doc) {
	find := func(prefix string) *Block {
		for _, b := range d.Body.flat {
			if len(b.Text()) >= len(prefix) && b.Text()[:len(prefix)] == prefix {
				return b
			}
		}
		t.Fatalf("no paragraph %q", prefix)
		return nil
	}
	b := find("Centred")
	d.SetCaret(Pos{B: b, Off: 0}, false)
	d.Type("EDITED ")
	if n := d.ReplaceAll("Step two", "Step 2", true); n != 1 {
		t.Fatalf("replaced %d", n)
	}
	b = find("R3C2")
	d.SetCaret(Pos{B: b, Off: 0}, false)
	if err := d.AddRow(false); err != nil {
		t.Fatal(err)
	}
	d.Finish()
	b = find("First bullet")
	d.SetCaret(Pos{B: b, Off: b.para().Len}, false)
	d.Enter()
	d.Type("Added bullet")
	b = find("Justified")
	yes := true
	d.Sel = Selection{Anchor: Pos{B: b, Off: 0}, Caret: Pos{B: b, Off: 9}}
	d.FormatRuns(RunChange{Bold: &yes})
	d.Finish()
}
