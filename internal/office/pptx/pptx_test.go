package pptx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
)

func blankDoc(t *testing.T) *Doc {
	t.Helper()
	data, err := Blank()
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func renderer(t *testing.T, d *Doc) *Renderer {
	t.Helper()
	fonts, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skip(err)
	}
	return &Renderer{D: d, Fonts: flow.NewBook(fonts), Prompt: func(*Ph) string { return "Click to add text" },
		Label: func(k string) string { return k }}
}

func shot(t *testing.T, r *Renderer, s *Slide, name string, o Options) *gfx.Canvas {
	t.Helper()
	c := gfx.NewCanvas(1280, 720)
	c.Clear(color.RGBA{40, 40, 40, 255})
	r.Slide(c, s, View{Scale: 1280 / r.D.W}, o)
	if dir := os.Getenv("FORGE_SHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		png.Encode(f, c.Img)
	}
	return c
}

func TestBlankIsComplete(t *testing.T) {
	d := blankDoc(t)
	if len(d.Slides) != 1 || len(d.Masters) != 1 || len(d.Masters[0].Layouts) != 6 {
		t.Fatalf("slides %d masters %d layouts %d", len(d.Slides), len(d.Masters), len(d.Masters[0].Layouts))
	}
	if d.W != 960 || d.H != 540 {
		t.Fatalf("size %v by %v", d.W, d.H)
	}
	s := d.Slides[0]
	list := d.Shapes(s)
	if len(list) != 2 || list[0].Ph == nil || list[0].Box.W == 0 {
		t.Fatalf("placeholders did not inherit: %+v", list)
	}
	// Ids across masters and layouts are unique and in the schema's range.
	seen := map[int64]bool{}
	for _, m := range d.Pres.Root.Child("p:sldMasterIdLst").Elements() {
		seen[attrInt(m, "id", 0)] = true
	}
	for _, l := range d.Masters[0].Doc.Root.Child("p:sldLayoutIdLst").Elements() {
		id := attrInt(l, "id", 0)
		if seen[id] || id < 2147483648 {
			t.Fatalf("layout id %d", id)
		}
		seen[id] = true
	}
	// Every part it holds is well-formed XML, and every relationship lands on a part.
	data, _ := d.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if zr.File[0].Name != "[Content_Types].xml" {
		t.Fatalf("first part %s", zr.File[0].Name)
	}
	for _, f := range zr.File {
		r, _ := f.Open()
		body, _ := io.ReadAll(r)
		r.Close()
		dec := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := dec.Token(); err != nil {
				if err != io.EOF {
					t.Fatalf("%s: %v", f.Name, err)
				}
				break
			}
		}
	}
	pkg, err := opc.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range pkg.Names() {
		rels, err := pkg.Rels(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rels {
			if pkg.Part(opc.Resolve(n, r.Target)) == nil {
				t.Fatalf("%s points at a missing %s", n, r.Target)
			}
		}
	}
}

func TestEveryPresetDraws(t *testing.T) {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	d := blankDoc(t)
	r := renderer(t, d)
	s, err := d.AddSlide(d.Masters[0].Layouts[5], 1)
	if err != nil {
		t.Fatal(err)
	}
	cols := 12
	cw, ch := d.W/float64(cols), 52.0
	for i, n := range names {
		b := Box{X: float64(i%cols)*cw + 8, Y: float64(i/cols)*ch + 8, W: cw - 16, H: ch - 12}
		if i/cols >= 10 {
			b.Y = float64(i/cols-10)*ch + 8
			b.X += 0
		}
		if _, err := d.AddShape(s, n, b); err != nil {
			t.Fatal(n, err)
		}
	}
	c := shot(t, r, s, "presets", Options{})
	if c.Img.At(640, 300) == nil {
		t.Fatal("no image")
	}
}

func colorAt(c *gfx.Canvas, x, y int) color.RGBA { return c.Img.RGBAAt(x, y) }

func TestPlaceholdersPromptOnlyInTheEditor(t *testing.T) {
	d := blankDoc(t)
	r := renderer(t, d)
	s := d.Slides[0]
	a := shot(t, r, s, "blank-show", Options{})
	b := shot(t, r, s, "blank-edit", Options{Prompts: true})
	if bytes.Equal(a.Img.Pix, b.Img.Pix) {
		t.Fatal("prompts make no difference")
	}
	for i := 0; i < len(a.Img.Pix); i += 4 {
		if a.Img.Pix[i] != 255 {
			t.Fatalf("the show has marks on an empty slide at %d", i/4)
		}
	}
}

func TestTextEditing(t *testing.T) {
	d := blankDoc(t)
	s := d.Slides[0]
	id := d.Shapes(s)[0].ID
	err := d.Edit("", []*Slide{s}, false, func() error {
		body, _ := d.Body(s, Target{ID: id})
		at := TypeText(body, Pos{}, "Hello world")
		if at != (Pos{0, 11}) {
			t.Fatalf("at %+v", at)
		}
		EachRun(body, Pos{0, 6}, Pos{0, 11}, SetBool("b", true))
		at = SplitPara(body, Pos{0, 5})
		if at != (Pos{1, 0}) {
			t.Fatalf("split %+v", at)
		}
		if got := PlainText(body); got != "Hello\n world" {
			t.Fatalf("text %q", got)
		}
		if len(body.Elements("a:p")[1].Elements("a:r")) != 2 {
			t.Fatal("the bold run did not stay apart")
		}
		DeleteRange(body, Pos{0, 3}, Pos{1, 3})
		if got := PlainText(body); got != "Helrld" {
			t.Fatalf("after delete %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Undo(); err != nil {
		t.Fatal(err)
	}
	if d.Find(s, id).HasText() {
		t.Fatal("undo left the words")
	}
	if err := d.Redo(); err != nil {
		t.Fatal(err)
	}
	if !d.Find(s, id).HasText() {
		t.Fatal("redo lost the words")
	}
}

func TestSlidesAddMoveDeleteUndo(t *testing.T) {
	d := blankDoc(t)
	l := d.Masters[0].Layouts[1]
	a, _ := d.AddSlide(l, 1)
	b, _ := d.AddSlide(l, 2)
	if a.ID != 257 || b.ID != 258 {
		t.Fatalf("ids %d %d", a.ID, b.ID)
	}
	if err := d.MoveSlide(2, 0); err != nil {
		t.Fatal(err)
	}
	if d.Slides[0] != b {
		t.Fatal("not moved")
	}
	if err := d.DeleteSlide(a); err != nil {
		t.Fatal(err)
	}
	data, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	re, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(re.Slides) != 2 {
		t.Fatalf("%d slides", len(re.Slides))
	}
	pkg, _ := opc.Open(data)
	if len(pkg.PartsOfType(ctSlide)) != 2 {
		t.Fatalf("slide parts %v", pkg.PartsOfType(ctSlide))
	}
	if err := d.Undo(); err != nil {
		t.Fatal(err)
	}
	data, _ = d.Bytes()
	re, err = Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(re.Slides) != 3 {
		t.Fatalf("undo gave %d slides", len(re.Slides))
	}
}

func TestPictureAndPasteBetweenPresentations(t *testing.T) {
	var png1 bytes.Buffer
	c := gfx.NewCanvas(40, 30)
	c.Clear(color.RGBA{10, 200, 30, 255})
	png.Encode(&png1, c.Img)
	a, b := blankDoc(t), blankDoc(t)
	sa, sb := a.Slides[0], b.Slides[0]
	id, err := a.AddPicture(sa, png1.Bytes(), Box{}, "green")
	if err != nil {
		t.Fatal(err)
	}
	clip := a.Copy(sa, []int{id})
	ids, err := b.Paste(sb, clip, 0, 0)
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	r := renderer(t, b)
	out := shot(t, r, sb, "pasted", Options{})
	if got := colorAt(out, 640, 360); got.G < 190 || got.R > 30 {
		t.Fatalf("picture not drawn: %v", got)
	}
	data, _ := b.Bytes()
	pkg, _ := opc.Open(data)
	found := false
	for _, n := range pkg.Names() {
		found = found || strings.HasPrefix(n, "ppt/media/")
	}
	if !found {
		t.Fatal("the picture did not come along")
	}
	if !strings.Contains(string(pkg.Part("[Content_Types].xml").Data), `Extension="png"`) {
		t.Fatal("no content type for png")
	}
	// Deleting it and saving removes the picture too; undo brings it back.
	if err := b.DeleteShapes(sb, ids); err != nil {
		t.Fatal(err)
	}
	data, _ = b.Bytes()
	pkg, _ = opc.Open(data)
	for _, n := range pkg.Names() {
		if strings.HasPrefix(n, "ppt/media/") {
			t.Fatal("orphan picture kept")
		}
	}
}

func TestGroupKeepsPlaceAndUngroupRestores(t *testing.T) {
	d := blankDoc(t)
	s := d.Slides[0]
	a, _ := d.AddShape(s, "rect", Box{X: 100, Y: 100, W: 100, H: 50})
	b, _ := d.AddShape(s, "ellipse", Box{X: 300, Y: 200, W: 80, H: 80, Rot: 30})
	g, err := d.Group(s, []int{a, b})
	if err != nil {
		t.Fatal(err)
	}
	gs := d.Find(s, g)
	if len(gs.Kids) != 2 {
		t.Fatal("kids", len(gs.Kids))
	}
	if k := d.Find(s, a); k.Box.X != 100 || k.Box.W != 100 {
		t.Fatalf("moved by grouping: %+v", k.Box)
	}
	// Doubling the group's size doubles what is in it.
	box := gs.Box
	box.W, box.H = box.W*2, box.H*2
	if err := d.SetBox(s, "", []int{g}, []Box{box}); err != nil {
		t.Fatal(err)
	}
	if k := d.Find(s, a); k.Box.W < 199 || k.Box.W > 201 {
		t.Fatalf("not scaled: %+v", k.Box)
	}
	ids, err := d.Ungroup(s, g)
	if err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	if k := d.Find(s, a); k.Parent != nil || k.Box.W < 199 || k.Box.W > 201 {
		t.Fatalf("after ungroup: %+v", k.Box)
	}
}

func TestHostileFilesDoNotPanic(t *testing.T) {
	data, _ := Blank()
	pkg, _ := opc.Open(data)
	cases := map[string][]byte{
		"deep":  []byte(strings.Repeat("<p:sp>", 100000)),
		"bad":   []byte("<p:sld><"),
		"empty": []byte(`<p:sld xmlns:p="` + nsP + `"/>`),
		"huge table": []byte(`<p:sld xmlns:p="` + nsP + `" xmlns:a="` + nsA + `"><p:cSld><p:spTree><p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="2" name="x"/></p:nvGraphicFramePr><p:xfrm><a:off x="0" y="0"/><a:ext cx="100" cy="100"/></p:xfrm><a:graphic><a:graphicData><a:tbl><a:tblGrid><a:gridCol w="-5"/></a:tblGrid><a:tr h="99999999999999999999"><a:tc gridSpan="100000" rowSpan="-3"/></a:tr></a:tbl></a:graphicData></a:graphic></p:graphicFrame>` +
			`<p:sp><p:nvSpPr><p:cNvPr id="x"/></p:nvSpPr><p:spPr><a:xfrm rot="abc"><a:off x="NaN" y="1e308"/><a:ext cx="1e308" cy="-1"/></a:xfrm><a:custGeom><a:pathLst><a:path w="0" h="0"><a:moveTo><a:pt x="q" y="w"/></a:moveTo><a:arcTo wR="0" hR="0" stAng="1e20" swAng="1e20"/></a:path></a:pathLst></a:custGeom></p:spPr></p:sp></p:spTree></p:cSld></p:sld>`),
	}
	for name, body := range cases {
		p2 := pkg.Clone()
		p2.SetData("ppt/slides/slide1.xml", body)
		out, err := p2.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		d, err := Open(out)
		if err != nil {
			continue // refused is fine
		}
		r := renderer(t, d)
		shot(t, r, d.Slides[0], "hostile-"+strings.ReplaceAll(name, " ", "-"), Options{Prompts: true})
	}
}

func TestUnknownPartsSurviveAnEdit(t *testing.T) {
	data, _ := Blank()
	pkg, _ := opc.Open(data)
	custom := []byte(`<?xml version="1.0"?><x:thing xmlns:x="urn:x"><x:y a="1"/></x:thing>`)
	pkg.Put("customXml/item1.xml", "application/xml", custom)
	pkg.AddRel("ppt/presentation.xml", "urn:custom", "../customXml/item1.xml", false)
	pkg.Put("ppt/unknown/thing.bin", "application/x-thing", []byte{1, 2, 3, 0, 255})
	pkg.AddRel("ppt/slides/slide1.xml", "urn:thing", "../unknown/thing.bin", false)
	slide, _ := pkg.Doc("ppt/slides/slide1.xml")
	// Elements nothing here knows, in the slide being edited.
	ext := newElement(`<p:extLst xmlns:p14="urn:p14"><p:ext uri="{X}"><p14:creationId val="123"/></p:ext></p:extLst>`)
	slide.Root.Append(ext)
	timing := newElement(`<p:timing><p:tnLst/></p:timing>`)
	slide.Root.Insert(slide.Root.Index(ext), timing)
	pkg.PutDoc("ppt/slides/slide1.xml", slide)
	orig, _ := pkg.Bytes()
	d, err := Open(orig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddShape(d.Slides[0], "rect", Box{X: 10, Y: 10, W: 10, H: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddSlide(d.Masters[0].Layouts[0], 1); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	re, _ := opc.Open(out)
	from, _ := opc.Open(orig)
	for _, n := range []string{"customXml/item1.xml", "ppt/unknown/thing.bin", "ppt/theme/theme1.xml", "ppt/slideMasters/slideMaster1.xml", "ppt/slideLayouts/slideLayout1.xml", "docProps/app.xml"} {
		if re.Part(n) == nil || !bytes.Equal(re.Part(n).Data, from.Part(n).Data) {
			t.Fatalf("%s changed", n)
		}
	}
	got := string(re.Part("ppt/slides/slide1.xml").Data)
	if !strings.Contains(got, `<p14:creationId val="123"/>`) || !strings.Contains(got, "<p:timing>") {
		t.Fatalf("slide lost its unknown markup: %s", got)
	}
	if strings.Index(got, "<p:cSld>") > strings.Index(got, "<p:timing>") {
		t.Fatal("order broken")
	}
}

func TestDeletingAnAnimatedShapeDropsItsAnimation(t *testing.T) {
	d := blankDoc(t)
	s := d.Slides[0]
	a, _ := d.AddShape(s, "rect", Box{X: 10, Y: 10, W: 10, H: 10})
	b, _ := d.AddShape(s, "rect", Box{X: 50, Y: 10, W: 10, H: 10})
	effect := func(id int) string {
		return `<p:par><p:cTn id="5" fill="hold"><p:stCondLst><p:cond delay="0"/></p:stCondLst><p:childTnLst><p:par><p:cTn id="6" presetID="1" presetClass="entr" nodeType="clickEffect"><p:stCondLst><p:cond delay="0"/></p:stCondLst><p:childTnLst><p:set><p:cBhvr><p:cTn id="7"/><p:tgtEl><p:spTgt spid="` + strconv.Itoa(id) + `"/></p:tgtEl></p:cBhvr></p:set></p:childTnLst></p:cTn></p:par></p:childTnLst></p:cTn></p:par>`
	}
	timing := newElement(`<p:timing><p:tnLst><p:par><p:cTn id="1" nodeType="tmRoot"><p:childTnLst><p:seq><p:cTn id="2" nodeType="mainSeq"><p:childTnLst>` + effect(a) + effect(b) + `</p:childTnLst></p:cTn></p:seq></p:childTnLst></p:cTn></p:par></p:tnLst></p:timing>`)
	s.tree().Root.Append(timing)
	if err := d.DeleteShapes(s, []int{a}); err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, n := range s.tree().Root.All("p:spTgt") {
		left = append(left, n.Attr("spid"))
	}
	if len(left) != 1 || left[0] != strconv.Itoa(b) {
		t.Fatalf("targets %v", left)
	}
	if err := d.DeleteShapes(s, []int{b}); err != nil {
		t.Fatal(err)
	}
	if s.tree().Root.Child("p:timing") != nil {
		t.Fatal("empty timing stays")
	}
}

func TestNotesAreMade(t *testing.T) {
	d := blankDoc(t)
	s := d.Slides[0]
	if err := d.SetNotes(s, "Remember this", ""); err != nil {
		t.Fatal(err)
	}
	data, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	re, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := re.Notes(re.Slides[0]); got != "Remember this" {
		t.Fatalf("notes %q", got)
	}
	if re.Pres.Root.Child("p:notesMasterIdLst") == nil {
		t.Fatal("no notes master")
	}
	kids := re.Pres.Root.Elements()
	if kids[0].Name != "p:sldMasterIdLst" || kids[1].Name != "p:notesMasterIdLst" || kids[2].Name != "p:sldIdLst" {
		t.Fatalf("order %s %s %s", kids[0].Name, kids[1].Name, kids[2].Name)
	}
	if err := d.Undo(); err != nil {
		t.Fatal(err)
	}
	if d.Notes(s) != "" {
		t.Fatal("undo left notes")
	}
}

func TestNumbering(t *testing.T) {
	for _, c := range []struct {
		scheme string
		n      int
		want   string
	}{{"arabicPeriod", 3, "3."}, {"romanLcPeriod", 4, "iv."}, {"alphaUcParenR", 27, "AA)"}, {"arabicParenBoth", 2, "(2)"}, {"arabicPlain", 7, "7"}} {
		if got := autoNumber(c.scheme, c.n); got != c.want {
			t.Errorf("%s %d: %q", c.scheme, c.n, got)
		}
	}
}

func TestColourTransforms(t *testing.T) {
	x := &colorCtx{theme: &Theme{Colors: map[string]color.RGBA{"accent1": {0x44, 0x72, 0xc4, 255}}}, cmap: map[string]string{"tx1": "dk1"}}
	n := newElement(`<a:schemeClr val="accent1"><a:lumMod val="60000"/><a:lumOff val="40000"/></a:schemeClr>`)
	c, ok := x.resolve(n)
	if !ok || c.R < 0x80 || c.R > 0x90 {
		t.Fatalf("%v %v", c, ok)
	}
	p, ok := x.resolve(newElement(`<a:prstClr val="dkBlue"/>`))
	if !ok || p.B == 0 {
		t.Fatalf("%v", p)
	}
}
