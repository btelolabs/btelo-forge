package pdf

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/office/pdf/pdftest"
)

func open(t *testing.T, data []byte) *Doc {
	t.Helper()
	d, err := Open(data, "")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestReadSample(t *testing.T) {
	d := open(t, pdftest.Sample(3))
	if d.PageCount() != 3 {
		t.Fatalf("%d pages", d.PageCount())
	}
	o := d.Outline()
	if len(o) != 2 || o[1].Page != 2 || o[0].Title != "First page" {
		t.Fatalf("outline %+v", o)
	}
	links := d.Links(0)
	if len(links) != 2 || links[0].Page != 2 || links[1].URI != "https://example.com/" {
		t.Fatalf("links %+v", links)
	}
	fields := d.Fields()
	if len(fields) != 4 {
		t.Fatalf("fields %+v", fields)
	}
}

func TestHeaderLateAndGarbageBefore(t *testing.T) {
	data := append([]byte("junk bytes before the header\n"), pdftest.Sample(1)...)
	d := open(t, data)
	if d.PageCount() != 1 {
		t.Fatal(d.PageCount())
	}
}

func TestBrokenXrefIsRebuilt(t *testing.T) {
	data := pdftest.Sample(2)
	at := bytes.LastIndex(data, []byte("startxref"))
	broken := append(bytes.Clone(data[:at]), []byte("startxref\n99999999\n%%EOF\n")...)
	d := open(t, broken)
	if !d.Repaired() || d.PageCount() != 2 {
		t.Fatalf("repaired %v pages %d", d.Repaired(), d.PageCount())
	}
	// No cross-references at all.
	cut := bytes.LastIndex(data, []byte("xref\n0 "))
	d = open(t, data[:cut])
	if d.PageCount() != 2 {
		t.Fatal(d.PageCount())
	}
}

func TestPredictorsAndFilters(t *testing.T) {
	// PNG up predictor over two rows of three bytes.
	raw := []byte{2, 1, 2, 3, 2, 1, 1, 1}
	s := NewStream(Dict{"Filter": Name("FlateDecode"), "DecodeParms": Dict{"Predictor": int64(12), "Columns": int64(3)}}, pdftest.Deflate(raw))
	d := Blank(100, 100)
	out, err := d.Decode(s)
	if err != nil || !bytes.Equal(out, []byte{1, 2, 3, 2, 3, 4}) {
		t.Fatalf("%v %v", out, err)
	}
	a85 := NewStream(Dict{"Filter": Name("ASCII85Decode")}, []byte("87cURD]i,\"Ebo80~>"))
	if out, _ := d.Decode(a85); string(out) != "Hello World!" {
		t.Fatalf("%q", out)
	}
	hx := NewStream(Dict{"Filter": Name("ASCIIHexDecode")}, []byte("48 65 6C 6C 6F>"))
	if out, _ := d.Decode(hx); string(out) != "Hello" {
		t.Fatalf("%q", out)
	}
	rl := NewStream(Dict{"Filter": Name("RunLengthDecode")}, []byte{2, 'a', 'b', 'c', 254, 'x', 128})
	if out, _ := d.Decode(rl); string(out) != "abcxxx" {
		t.Fatalf("%q", out)
	}
	// LZW, the example of the PDF specification.
	lzw, _ := hex.DecodeString("800B6050220C0C8501")
	if out, _ := lzwDecode(lzw, 1); !bytes.Equal(out, []byte{45, 45, 45, 45, 45, 65, 45, 45, 45, 66}) {
		t.Fatalf("%v", out)
	}
	// TIFF predictor.
	ti := NewStream(Dict{"Filter": Name("FlateDecode"), "DecodeParms": Dict{"Predictor": int64(2), "Columns": int64(3)}}, pdftest.Deflate([]byte{1, 1, 1}))
	if out, _ := d.Decode(ti); !bytes.Equal(out, []byte{1, 2, 3}) {
		t.Fatalf("%v", out)
	}
}

func TestCyclesDoNotHang(t *testing.T) {
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [2 0 R 3 0 R] /Count 2 >>")
	b.Obj(3, "<< /Type /Pages /Parent 2 0 R /Kids [2 0 R 4 0 R] >>")
	b.Obj(4, "<< /Type /Page /Parent 3 0 R /MediaBox [0 0 100 100] >>")
	b.Obj(5, "5 0 R")
	d := open(t, b.Bytes())
	if d.PageCount() != 1 {
		t.Fatal(d.PageCount())
	}
	if d.Resolve(Ref{5, 0}) != nil {
		t.Fatal("a reference to itself resolved")
	}
}

func TestXrefLoop(t *testing.T) {
	data := pdftest.Sample(1)
	at := bytes.LastIndex(data, []byte("trailer"))
	offset := bytes.LastIndex(data, []byte("xref\n0 "))
	looped := append(bytes.Clone(data[:at]), []byte(fmt.Sprintf("trailer\n<< /Size 60 /Root 1 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n", offset, offset))...)
	d := open(t, looped)
	if d.PageCount() != 1 {
		t.Fatal(d.PageCount())
	}
}

func TestDeepNesting(t *testing.T) {
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /MediaBox [0 0 10 10] /X "+strings.Repeat("[", 200000)+" >>")
	done := make(chan bool)
	go func() {
		Open(b.Bytes(), "")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deep nesting hung")
	}
}

// exercise does what the editor would do to a document, which must never panic.
func exercise(data []byte) {
	d, err := Open(data, "")
	if err != nil {
		return
	}
	n := d.PageCount()
	d.Outline()
	d.Fields()
	for i := 0; i < n && i < 5; i++ {
		d.Links(i)
		d.Annots(i)
		d.ViewSize(d.Pages()[i], 0)
	}
	d.Snapshot().Rewrite(true)
}

func TestFuzzDamage(t *testing.T) {
	base := pdftest.Sample(3)
	rng := rand.New(rand.NewSource(1))
	deadline := time.Now().Add(4 * time.Second)
	// Truncated at every point of the file, roughly.
	for i := 0; i < len(base); i += 97 {
		exercise(base[:i])
	}
	for time.Now().Before(deadline) {
		b := bytes.Clone(base)
		for range 1 + rng.Intn(40) {
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		}
		exercise(b)
	}
}

func TestIncrementalKeepsBytes(t *testing.T) {
	orig := pdftest.Sample(2)
	d := open(t, orig)
	ref, err := d.AddAnnot(0, AnnotSpec{Kind: "Square", Rect: Rect{100, 100, 200, 200}, Color: [3]float64{1, 0, 0}, Width: 2})
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, orig) {
		t.Fatal("the original bytes changed")
	}
	again := open(t, out)
	if again.Repaired() {
		t.Fatal("the update's cross-references did not read")
	}
	if len(again.Annots(0)) != 8 {
		t.Fatalf("%d annotations", len(again.Annots(0)))
	}
	if a := again.Dict(ref); a == nil || a["AP"] == nil {
		t.Fatalf("annotation %v", a)
	}
	// Undone back to nothing, the file is itself again.
	d.Undo()
	out, _ = d.Bytes()
	if !bytes.Equal(out, orig) {
		t.Fatal("undoing everything did not give the original file")
	}
}

// A file written with cross-reference and object streams gets its update as a cross-reference stream.
func TestIncrementalOnXrefStream(t *testing.T) {
	packed, err := open(t, pdftest.Sample(2)).Snapshot().Rewrite(true)
	if err != nil {
		t.Fatal(err)
	}
	d := open(t, packed)
	if d.Repaired() || !d.xrefStream {
		t.Fatal("the packed file did not read as such")
	}
	if len(d.Fields()) != 4 || d.Outline()[1].Page != 1 {
		t.Fatal("objects in object streams did not read")
	}
	if err := d.SetField(&d.Fields()[0], "Packed"); err != nil {
		t.Fatal(err)
	}
	out, _ := d.Bytes()
	if !bytes.HasPrefix(out, packed) {
		t.Fatal("bytes changed")
	}
	a := open(t, out)
	if a.Repaired() || a.Fields()[0].Value != "Packed" {
		t.Fatal(a.Repaired(), a.Fields()[0])
	}
}

func TestDeleteRewrites(t *testing.T) {
	d := open(t, pdftest.Sample(4))
	if err := d.DeletePages([]int{3}); err != nil {
		t.Fatal(err)
	}
	if !d.NeedsRewrite() {
		t.Fatal("a deletion must rewrite")
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("Page 4 of 4")) {
		t.Fatal("the deleted page's text is in the file")
	}
	a := open(t, out)
	if a.PageCount() != 3 || a.Repaired() {
		t.Fatal(a.PageCount(), a.Repaired())
	}
	// The outline's item for the last page lost its target; the named destination and the link are gone.
	if o := a.Outline(); o[1].Page != -1 {
		t.Fatalf("outline %+v", o)
	}
	for _, l := range a.Links(0) {
		if l.URI == "" {
			t.Fatalf("a link to a deleted page stayed: %+v", l)
		}
	}
	// Undo brings it all back.
	d.Undo()
	if d.PageCount() != 4 || d.NeedsRewrite() {
		t.Fatal("undo", d.PageCount(), d.NeedsRewrite())
	}
}

func TestMoveAndRotateKeepLinks(t *testing.T) {
	d := open(t, pdftest.Sample(3))
	if err := d.MovePages([]int{0}, 2); err != nil {
		t.Fatal(err)
	}
	if err := d.RotatePages([]int{2}, 90); err != nil {
		t.Fatal(err)
	}
	out, _ := d.Bytes()
	a := open(t, out)
	if a.Rotation(a.Pages()[2]) != 90 {
		t.Fatal("rotation")
	}
	// Page 1 moved last; its link pointed at what was the last page, now the second.
	links := a.Links(2)
	if links[0].Page != 1 {
		t.Fatalf("link %+v", links[0])
	}
	if o := a.Outline(); o[0].Page != 2 {
		t.Fatalf("outline %+v", o)
	}
}

func TestMergeCollisions(t *testing.T) {
	d := open(t, pdftest.Sample(2))
	other := open(t, pdftest.Sample(2))
	if err := d.Merge(other, 2); err != nil {
		t.Fatal(err)
	}
	out, _ := d.Bytes()
	a := open(t, out)
	if a.PageCount() != 4 {
		t.Fatal(a.PageCount())
	}
	// Both forms' fields are there, under different names.
	seen := map[string]bool{}
	for _, f := range a.Fields() {
		if seen[f.Name] {
			t.Fatalf("field %q twice", f.Name)
		}
		seen[f.Name] = true
	}
	if !seen["name"] || !seen["name (2)"] {
		t.Fatalf("fields %v", seen)
	}
	if links := a.Links(2); len(links) != 2 || links[0].Page != 3 {
		t.Fatalf("a merged link %+v", links)
	}
	if o := a.Outline(); len(o) != 4 || o[3].Page != 3 {
		t.Fatalf("outline %+v", o)
	}
}

func TestExtract(t *testing.T) {
	d := open(t, pdftest.Sample(5))
	out, err := d.Extract([]int{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	a := open(t, out)
	if a.PageCount() != 2 {
		t.Fatal(a.PageCount())
	}
	if !bytes.Contains(deflated(t, a, 0), []byte("Page 2 of 5")) {
		t.Fatal("the wrong page")
	}
}

func deflated(t *testing.T, d *Doc, page int) []byte {
	t.Helper()
	s, _ := d.Resolve(d.Pages()[page].Dict["Contents"]).(*Stream)
	if s == nil {
		t.Fatal("no content")
	}
	b, err := d.Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCompress(t *testing.T) {
	b := pdftest.New()
	body := strings.Repeat("0 0 m 10 10 l S\n", 2000)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>")
	b.Stream(4, "", []byte(body))
	b.Obj(9, "<< /Unused true >>")
	orig := b.Bytes()
	d := open(t, orig)
	out, err := d.Snapshot().Rewrite(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) >= len(orig)/2 {
		t.Fatalf("%d of %d", len(out), len(orig))
	}
	if bytes.Contains(out, []byte("/Unused")) {
		t.Fatal("a dead object stayed")
	}
	a := open(t, out)
	if a.Repaired() || a.PageCount() != 1 {
		t.Fatal(a.Repaired(), a.PageCount())
	}
	if got := deflated(t, a, 0); string(got) != body {
		t.Fatal("the content changed")
	}
}

func TestFormValues(t *testing.T) {
	d := open(t, pdftest.Sample(1))
	field := func(name string) *Field {
		for _, f := range d.Fields() {
			if f.Name == name {
				return &f
			}
		}
		t.Fatalf("no field %s", name)
		return nil
	}
	if err := d.SetField(field("name"), "Grace"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetField(field("agree"), "Yes"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetField(field("colour"), "blue"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetField(field("size"), "l"); err != nil {
		t.Fatal(err)
	}
	out, _ := d.Bytes()
	a := open(t, out)
	d = a
	if v := field("name").Value; v != "Grace" {
		t.Fatal(v)
	}
	if f := field("agree"); f.Value != "Yes" || a.Name(a.Dict(f.Widgets[0].Ref)["AS"]) != "Yes" {
		t.Fatal(f)
	}
	f := field("colour")
	if f.Value != "blue" || a.Name(a.Dict(f.Widgets[0].Ref)["AS"]) != "Off" || a.Name(a.Dict(f.Widgets[1].Ref)["AS"]) != "blue" {
		t.Fatal(f)
	}
	if s, ok := a.Resolve(a.Dict(a.Dict(field("name").Ref)["AP"])["N"]).(*Stream); !ok {
		t.Fatal("no appearance")
	} else if body, _ := a.Decode(s); !bytes.Contains(body, []byte(hex.EncodeToString([]byte("Grace"))[:4])) && !strings.Contains(strings.ToLower(string(body)), "677261") {
		t.Fatalf("appearance %s", body)
	}
}

func TestImageContent(t *testing.T) {
	d := open(t, pdftest.Sample(1))
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := range 16 {
		img.SetNRGBA(i%4, i/4, color.NRGBA{255, 0, 0, uint8(i * 17)})
	}
	if err := d.AddImage(0, img, nil, Rect{10, 10, 110, 110}); err != nil {
		t.Fatal(err)
	}
	out, _ := d.Bytes()
	a := open(t, out)
	res := a.Dict(a.Pages()[0].Dict["Resources"])
	xo := a.Dict(res["XObject"])
	if len(xo) != 1 {
		t.Fatalf("%v", xo)
	}
	for _, v := range xo {
		s, _ := a.Resolve(v).(*Stream)
		if s == nil || s.Dict["SMask"] == nil {
			t.Fatal("no soft mask")
		}
	}
	contents := a.Array(a.Pages()[0].Dict["Contents"])
	if len(contents) != 4 {
		t.Fatalf("contents %v", contents)
	}
}

// encrypted fixtures come from pypdf, see tools/officecheck/pdf_fixtures.py.
func TestEncryptedFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/enc-*.pdf")
	if len(files) == 0 {
		t.Skip("no fixtures")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		password := ""
		if strings.Contains(name, "pw") {
			if _, err := Open(data, ""); err != ErrPassword {
				t.Fatalf("%s: %v", name, err)
			}
			if _, err := Open(data, "wrong"); err != ErrPassword {
				t.Fatalf("%s: %v", name, err)
			}
			password = "secret"
		}
		d, err := Open(data, password)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s, _ := d.Resolve(d.Pages()[0].Dict["Contents"]).(*Stream)
		body, err := d.Decode(s)
		if err != nil || !bytes.Contains(body, []byte("Secret page")) {
			t.Fatalf("%s: %q %v", name, body, err)
		}
		// Edits are written encrypted, and read back.
		if _, err := d.AddAnnot(0, AnnotSpec{Kind: "Text", Rect: Rect{10, 10, 30, 30}, Contents: "note", Color: [3]float64{1, 1, 0}}); err != nil {
			t.Fatal(err)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		a, err := Open(out, password)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := a.Annots(0); len(got) != 1 || got[0].Contents != "note" {
			t.Fatalf("%s: %+v", name, got)
		}
		if bytes.Contains(out[len(data):], []byte("note")) {
			t.Fatalf("%s: a string was written in the clear", name)
		}
		// So does a clean rewrite.
		re, err := d.Snapshot().Rewrite(false)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Open(re, password)
		if err != nil {
			t.Fatalf("%s rewrite: %v", name, err)
		}
		if got := b.Annots(0); len(got) != 1 || got[0].Contents != "note" {
			t.Fatalf("%s rewrite: %+v", name, got)
		}
		s, _ = b.Resolve(b.Pages()[0].Dict["Contents"]).(*Stream)
		if body, err = b.Decode(s); err != nil || !bytes.Contains(body, []byte("Secret page")) {
			t.Fatalf("%s rewrite: %q %v", name, body, err)
		}
	}
}

// A file holding objects nothing here understands keeps them byte for byte through an edit.
func TestFidelity(t *testing.T) {
	base := pdftest.New()
	base.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Vendor 9 0 R >>")
	base.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	base.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /PieceInfo << /App << /Private (x) >> >> >>")
	base.Obj(9, "<< /Mystery [1 2.50 (three) /Four] /Blob <CAFE> >>")
	orig := base.Bytes()
	d := open(t, orig)
	if _, err := d.AddAnnot(0, AnnotSpec{Kind: "Square", Rect: Rect{1, 1, 50, 50}, Color: [3]float64{0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	out, _ := d.Bytes()
	if !bytes.HasPrefix(out, orig) {
		t.Fatal("bytes changed")
	}
	a := open(t, out)
	if a.Dict(Ref{9, 0})["Blob"] != String("\xCA\xFE") {
		t.Fatal("the unknown object is gone")
	}
	// A clean rewrite keeps it too, as it is reachable.
	re, _ := d.Snapshot().Rewrite(false)
	b := open(t, re)
	if b.Dict(b.Catalog()["Vendor"])["Blob"] != String("\xCA\xFE") {
		t.Fatal("the rewrite lost it")
	}
	if b.Dict(b.Dict(b.Pages()[0].Dict["PieceInfo"])["App"])["Private"] != String("x") {
		t.Fatal("the rewrite lost a page's private data")
	}
}

func TestRepairedFileIsRewritten(t *testing.T) {
	data := pdftest.Sample(1)
	cut := bytes.LastIndex(data, []byte("xref\n0 "))
	d := open(t, data[:cut])
	if _, err := d.AddAnnot(0, AnnotSpec{Kind: "Square", Rect: Rect{1, 1, 50, 50}}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	a := open(t, out)
	if a.Repaired() || len(a.Annots(0)) != 8 {
		t.Fatal(a.Repaired(), len(a.Annots(0)))
	}
}

func TestSignedPrefixStays(t *testing.T) {
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm << /SigFlags 3 /Fields [5 0 R] >> >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Annots [5 0 R] >>")
	b.Obj(5, "<< /FT /Sig /T (sig) /V 6 0 R /Type /Annot /Subtype /Widget /Rect [0 0 0 0] >>")
	b.Obj(6, "<< /Type /Sig /Filter /Adobe.PPKLite /ByteRange [0 100 200 300] /Contents <00> >>")
	orig := b.Bytes()
	d := open(t, orig)
	if !d.Signed() {
		t.Fatal("not seen as signed")
	}
	if err := d.CoverRect(0, Rect{0, 0, 10, 10}, [3]float64{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	out, _ := d.Bytes()
	if !bytes.HasPrefix(out, orig) {
		t.Fatal("an edit touched the signed bytes")
	}
}
