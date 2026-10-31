package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// withBody is a file whose document.xml has body as its body.
func withBody(t testing.TB, body string, extra map[string]string) []byte {
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
		data := b.Bytes()
		if f.Name == "word/document.xml" {
			data = []byte(xmlHead + `<w:document ` + docNS + ` xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"><w:body>` + body + `</w:body></w:document>`)
		}
		w.Write(data)
	}
	for name, s := range extra {
		w, _ := zw.Create(name)
		w.Write([]byte(s))
	}
	zw.Close()
	return buf.Bytes()
}

// survive opens, lays out, draws and edits a file: nothing may panic, and it must end.
func survive(t *testing.T, data []byte) {
	t.Helper()
	d, err := Open(data, testFonts(t))
	if err != nil {
		return // refused is fine
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Finish()
		c := gfx.NewCanvas(300, 400)
		for i, pg := range d.Pages() {
			if i > 3 {
				break
			}
			d.DrawPage(c, pg, 0, 0, 0.5)
		}
		d.Type("x")
		d.Enter()
		d.Backspace(false)
		d.SelectAll()
		d.Copy()
		d.DeleteSelection()
		d.Undo()
		d.Undo()
		d.Finish()
		if _, err := d.Bytes(); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the document never finished")
	}
}

func TestHostileBodies(t *testing.T) {
	deep := strings.Repeat("<w:tbl><w:tr><w:tc>", 200) + "<w:p/>" + strings.Repeat("</w:tc></w:tr></w:tbl>", 200)
	sdt := strings.Repeat("<w:sdt><w:sdtContent>", 300) + "<w:p/>" + strings.Repeat("</w:sdtContent></w:sdt>", 300)
	links := "<w:p>" + strings.Repeat("<w:hyperlink>", 300) + "<w:r><w:t>x</w:t></w:r>" + strings.Repeat("</w:hyperlink>", 300) + "</w:p>"
	cases := map[string]string{
		"empty":        "",
		"no paragraph": "<w:sectPr/>",
		"deep tables":  deep,
		"deep sdt":     sdt,
		"deep links":   links,
		"zero page":    `<w:p/><w:sectPr><w:pgSz w:w="0" w:h="0"/></w:sectPr>`,
		"tiny page":    `<w:p><w:r><w:t>words on a very small page indeed</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="10" w:h="10"/><w:pgMar w:top="99999" w:bottom="99999" w:left="99999" w:right="99999" w:header="99999" w:footer="99999"/></w:sectPr>`,
		"huge indent":  `<w:p><w:pPr><w:ind w:left="99999999" w:right="99999999" w:firstLine="99999999"/></w:pPr><w:r><w:t>words</w:t></w:r></w:p>`,
		"huge span":    `<w:tbl><w:tblGrid><w:gridCol w:w="100"/></w:tblGrid><w:tr><w:tc><w:tcPr><w:gridSpan w:val="2000000"/></w:tcPr><w:p/></w:tc></w:tr></w:tbl><w:p/>`,
		"no cells":     `<w:tbl><w:tr/><w:tr/></w:tbl><w:p/>`,
		"cell no para": `<w:tbl><w:tr><w:tc/></w:tr></w:tbl><w:p/>`,
		"lone vmerge":  `<w:tbl><w:tblGrid><w:gridCol/><w:gridCol/></w:tblGrid><w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc><w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p/></w:tc></w:tr></w:tbl><w:p/>`,
		"exact tall":   `<w:tbl><w:tr><w:trPr><w:trHeight w:val="999999" w:hRule="exact"/></w:trPr><w:tc><w:p><w:r><w:t>x</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:p/>`,
		"tall row":     `<w:tbl><w:tr><w:trPr><w:cantSplit/><w:trHeight w:val="999999"/></w:trPr><w:tc><w:p/></w:tc></w:tr></w:tbl><w:p/>`,
		"bad numbers":  `<w:p><w:pPr><w:numPr><w:ilvl w:val="99"/><w:numId w:val="-5"/></w:numPr><w:spacing w:line="x" w:before="y"/></w:pPr><w:r><w:rPr><w:sz w:val="-4"/><w:color w:val="zz"/></w:rPr><w:t>x</w:t></w:r></w:p>`,
		"huge picture": `<w:p><w:r><w:drawing><wp:inline><wp:extent cx="99999999999999999" cy="-5"/></wp:inline></w:drawing></w:r></w:p>`,
		"bad drawing":  `<w:p><w:r><w:drawing><wp:anchor><wp:positionH/></wp:anchor></w:drawing><w:pict/></w:r></w:p>`,
		"stray field":  `<w:p><w:r><w:fldChar w:fldCharType="end"/></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:t>x</w:t></w:r></w:p>`,
		"breaks":       `<w:p><w:pPr><w:pageBreakBefore/></w:pPr><w:r><w:br w:type="page"/><w:br w:type="page"/><w:br w:type="column"/></w:r></w:p><w:p><w:pPr><w:keepNext/><w:pageBreakBefore/></w:pPr></w:p>`,
		"keep chain":   strings.Repeat(`<w:p><w:pPr><w:keepNext/><w:keepLines/></w:pPr><w:r><w:t>keep with next</w:t></w:r></w:p>`, 300),
		"text box":     `<w:p><w:r><mc:AlternateContent><mc:Choice><w:drawing><wp:anchor><wp:extent cx="1" cy="1"/><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent/></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></mc:Choice></mc:AlternateContent></w:r></w:p>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) { survive(t, withBody(t, body, nil)) })
	}
}

func TestDeepXMLIsRefused(t *testing.T) {
	body := strings.Repeat("<w:x>", 2000000) + strings.Repeat("</w:x>", 2000000)
	if _, err := Open(withBody(t, "<w:p/>"+body, nil), testFonts(t)); err == nil {
		t.Fatal("two million levels of nesting were accepted")
	}
}

func TestBrokenPackages(t *testing.T) {
	good, err := Blank()
	if err != nil {
		t.Fatal(err)
	}
	fonts := testFonts(t)
	for _, data := range [][]byte{nil, []byte("not a zip"), good[:len(good)/2], good[:40]} {
		if _, err := Open(data, fonts); err == nil {
			t.Fatal("a broken file opened")
		}
	}
	// Bytes flipped anywhere: an error or a working document, never a panic.
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		data := bytes.Clone(good)
		for k := 0; k < 8; k++ {
			data[r.Intn(len(data))] ^= byte(1 + r.Intn(255))
		}
		survive(t, data)
	}
}

// styleCycle is styles based on each other in a ring; numbering and a theme with holes in them.
func TestDamagedStyles(t *testing.T) {
	data := withBody(t, `<w:p><w:pPr><w:pStyle w:val="A"/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:rPr><w:rStyle w:val="A"/></w:rPr><w:t>x</w:t></w:r></w:p>`, nil)
	d, err := Open(data, testFonts(t))
	if err != nil {
		t.Fatal(err)
	}
	st := d.Styles
	st.ByID["A"] = &Style{ID: "A", BasedOn: "B", Type: "paragraph", Node: d.Main.X.Root}
	st.ByID["B"] = &Style{ID: "B", BasedOn: "A", Type: "paragraph", Node: d.Main.X.Root}
	if n := len(st.chain("A")); n > 32 {
		t.Fatalf("chain %d", n)
	}
	d.Finish()
}

func samplePNG(t testing.TB, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPictureInsert(t *testing.T) {
	d := blankDoc(t)
	if err := d.InsertPicture(samplePNG(t, 4000, 2000), "wide.png", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertPicture([]byte("not a picture"), "x", 0, 0); err == nil {
		t.Fatal("rubbish accepted")
	}
	d.Finish()
	seg := d.Body.flat[0].P.Segs[0]
	if seg.Kind != SegPicture {
		t.Fatalf("seg %+v", seg)
	}
	l := d.Body.flat[0].para().laid
	f := l.Lines[0].Frags[0]
	if f.Inline == nil || f.Inline.W > d.sects[0].TextW()+0.1 || f.Inline.H < 100 {
		t.Fatalf("inline %+v", f.Inline)
	}
	d.Pics.Sync = true
	if d.Pics.Get(f.Inline.Key) == nil {
		t.Fatal("the picture does not decode")
	}
	// Removing and undoing keep the file's part.
	d.Backspace(false)
	d.Undo()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	parts := unzip(t, out)
	if parts["word/media/image1.png"] == nil {
		t.Fatal("no image part")
	}
	if !strings.Contains(string(parts["word/_rels/document.xml.rels"]), "media/image1.png") {
		t.Fatal("no relationship")
	}
}

// A long document opens and lays out at a usable pace, and an edit on it is quick.
func TestLongDocumentPace(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, `<w:p><w:r><w:t xml:space="preserve">Paragraph %d of the long document, 这是一段中文，with enough words to wrap across the page at least the once, or perhaps a little more than that.</w:t></w:r></w:p>`, i)
	}
	start := time.Now()
	d, err := Open(withBody(t, b.String(), nil), testFonts(t))
	if err != nil {
		t.Fatal(err)
	}
	opened := time.Since(start)
	start = time.Now()
	d.Finish()
	laid := time.Since(start)
	t.Logf("open %v, layout %v, %d pages", opened, laid, len(d.Pages()))
	worst := time.Duration(0)
	d.SetCaret(Pos{B: d.Body.flat[10000]}, false)
	for i := 0; i < 50; i++ {
		start = time.Now()
		d.Type("x")
		d.Layout(0)
		worst = max(worst, time.Since(start))
	}
	start = time.Now()
	d.Enter()
	d.Layout(0)
	enter := time.Since(start)
	t.Logf("typing worst %v, enter %v", worst, enter)
	if worst > 30*time.Millisecond {
		t.Fatalf("a keystroke took %v", worst)
	}
}
