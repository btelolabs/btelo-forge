package ox

import (
	"strings"
	"testing"
)

func TestRoundTripKeepsEverything(t *testing.T) {
	src := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:x14="urn:x" mc:Ignorable="x14"><!-- note --><w:body><w:p x14:id="1"><w:r><w:t xml:space="preserve"> a &lt; b </w:t></w:r></w:p><x14:unknown a="1"/><?pi data?></w:body></w:document>`
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); got != src {
		t.Fatalf("changed on the way through:\n%s", got)
	}
}

func TestUnusualPrefixesBecomeTheUsualOnes(t *testing.T) {
	src := `<ns0:document xmlns:ns0="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><ns0:p ns0:rsid="1"/></ns0:document>`
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if d.Root.Name != "w:document" || d.Root.Child("w:p").Attr("w:rsid") != "1" || d.Root.Attr("xmlns:w") == "" {
		t.Fatalf("%s", d.Bytes())
	}
	src = `<x:worksheet xmlns:x="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><x:sheetData/></x:worksheet>`
	if d, err = Parse([]byte(src)); err != nil {
		t.Fatal(err)
	}
	if d.Root.Name != "worksheet" || d.Root.Child("sheetData") == nil || d.Root.Attr("xmlns") == "" {
		t.Fatalf("%s", d.Bytes())
	}
}

func TestDefaultNamespaceGetsThePrefix(t *testing.T) {
	src := `<document xmlns="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><body/></document>`
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if d.Root.Name != "w:document" || d.Root.Child("w:body") == nil || !strings.Contains(string(d.Bytes()), `xmlns:w=`) {
		t.Fatalf("%s", d.Bytes())
	}
}

func TestRejectsWhatOfficeWouldNot(t *testing.T) {
	for _, src := range []string{`<a>`, `<a></b>`, `<a/><b/>`, `<!DOCTYPE a [<!ENTITY x "y">]><a/>`, ``} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%q was read", src)
		}
	}
}

func TestControlCharactersAreDropped(t *testing.T) {
	n := New("t")
	n.SetText("a\x01b\tc")
	d := &Doc{Root: n}
	if got := string(d.Bytes()); !strings.Contains(got, "<t>ab\tc</t>") {
		t.Fatal(got)
	}
}

func TestEnsureFollowsTheSchemaOrder(t *testing.T) {
	order := []string{"w:b", "w:i", "w:color", "w:sz"}
	n := New("w:rPr")
	n.Append(New("w:sz"))
	n.Ensure("w:i", order)
	n.Ensure("w:b", order)
	n.Ensure("w:color", order)
	var got []string
	for _, c := range n.Elements() {
		got = append(got, c.Name)
	}
	if strings.Join(got, " ") != "w:b w:i w:color w:sz" {
		t.Fatal(got)
	}
}
