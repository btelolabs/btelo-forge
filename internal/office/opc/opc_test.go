package opc

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

func sample(t *testing.T) []byte {
	t.Helper()
	p := NewPackage()
	p.Put("word/document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml", []byte("<a/>"))
	p.Put("custom/unknown.bin", "application/x-thing", []byte{1, 2, 3})
	p.AddRel("", RelOfficeDocument, "word/document.xml", false)
	p.AddRel("word/document.xml", RelHyperlink, "https://example.com/", true)
	data, err := p.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPartsComeBackWithTheirTypes(t *testing.T) {
	p, err := Open(sample(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Part("custom/unknown.bin"); got == nil || got.ContentType != "application/x-thing" || !bytes.Equal(got.Data, []byte{1, 2, 3}) {
		t.Fatalf("%+v", got)
	}
	rels, err := p.Rels("")
	if err != nil {
		t.Fatal(err)
	}
	r, ok := RelOfType(rels, RelOfficeDocument)
	if !ok || Resolve("", r.Target) != "word/document.xml" {
		t.Fatalf("%+v", rels)
	}
	rels, _ = p.Rels("word/document.xml")
	if len(rels) != 1 || !rels[0].External {
		t.Fatalf("%+v", rels)
	}
}

func TestContentTypesAndRelsComeFirst(t *testing.T) {
	zr, err := zip.NewReader(bytes.NewReader(sample(t)), int64(len(sample(t))))
	if err != nil {
		t.Fatal(err)
	}
	if zr.File[0].Name != "[Content_Types].xml" || zr.File[1].Name != "_rels/.rels" {
		t.Fatal(zr.File[0].Name, zr.File[1].Name)
	}
}

func TestSavingTwiceIsTheSameBytes(t *testing.T) {
	data := sample(t)
	p, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Open(again)
	if err != nil {
		t.Fatal(err)
	}
	third, _ := p2.Bytes()
	if !bytes.Equal(again, third) {
		t.Fatal("the package changed by being read and written")
	}
}

func TestDeleteTakesItsRelationshipsAndType(t *testing.T) {
	p, err := Open(sample(t))
	if err != nil {
		t.Fatal(err)
	}
	p.Delete("word/document.xml")
	if p.Part("word/_rels/document.xml.rels") != nil {
		t.Fatal("its relationships stayed")
	}
	data, _ := p.Bytes()
	if bytes.Contains(data, []byte("wordprocessingml.document.main")) {
		// compressed data would not match, so look through the reader
		t.Fatal("the override stayed")
	}
	q, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := q.override["word/document.xml"]; ok {
		t.Fatal("the override stayed")
	}
}

func TestImagesWithTheSameBytesShareAPart(t *testing.T) {
	p := NewPackage()
	a := p.AddImage("word/media", "png", "image/png", []byte{9, 9})
	b := p.AddImage("word/media", ".PNG", "image/png", []byte{9, 9})
	c := p.AddImage("word/media", "png", "image/png", []byte{8})
	if a != b || a == c || c != "word/media/image2.png" {
		t.Fatal(a, b, c)
	}
}

func TestRelativeTargets(t *testing.T) {
	for _, c := range [][3]string{
		{"word/document.xml", "word/media/a.png", "media/a.png"},
		{"ppt/slides/slide1.xml", "ppt/media/a.png", "../media/a.png"},
		{"ppt/slides/_rels/x", "ppt/media/a.png", "../../media/a.png"},
		{"a.xml", "word/b.xml", "word/b.xml"},
	} {
		if got := RelativeTarget(c[0], c[1]); got != c[2] {
			t.Errorf("%v: %s", c, got)
		}
		if got := Resolve(c[0], RelativeTarget(c[0], c[1])); got != c[1] {
			t.Errorf("%v resolves to %s", c, got)
		}
	}
}

func TestRefusesWhatIsNotAPackage(t *testing.T) {
	if _, err := Open([]byte("hello")); !errors.Is(err, ErrNotOffice) {
		t.Error(err)
	}
	if _, err := Open([]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1, 0}); !errors.Is(err, ErrEncrypted) {
		t.Error(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../../evil")
	w.Write([]byte("x"))
	zw.Close()
	if _, err := Open(buf.Bytes()); err == nil {
		t.Error("a part leaving the package was read")
	}
}
