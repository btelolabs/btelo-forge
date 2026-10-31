package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/mind"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

func TestParsePages(t *testing.T) {
	if got, ok := parsePages("", 3); !ok || len(got) != 3 || got[2] != 2 {
		t.Fatalf("all: %v %v", got, ok)
	}
	got, ok := parsePages("1-2, 4，9", 5)
	if !ok || len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 3 {
		t.Fatalf("%v %v", got, ok)
	}
	for _, bad := range []string{"x", "0", "3-1", "9"} {
		if _, ok := parsePages(bad, 5); ok {
			t.Errorf("%q was taken", bad)
		}
	}
}

func TestHeaderParts(t *testing.T) {
	got := headerParts(`&L&"Arial,Bold"&12Left&CMiddle &P&RRight`)
	if got != [3]string{"Left", "Middle &P", "Right"} {
		t.Fatalf("%q", got)
	}
}

func opsOf(p *printing.Page) (pictures, shapes, words int) {
	for _, op := range p.Ops {
		switch op.(type) {
		case *printing.Picture:
			pictures++
		case *printing.Fill, *printing.Stroke, *printing.Shade:
			shapes++
		case *printing.Text:
			words++
		}
	}
	return
}

// keysOpenTheDialog presses Ctrl+P in a tab.
func keysOpenTheDialog(t *testing.T, x *harness) {
	t.Helper()
	x.key('P', true)
	x.frame(Input{})
	if x.a.print == nil {
		t.Fatal("Ctrl+P opened nothing")
	}
}

func TestWordPrintsAsVectors(t *testing.T) {
	x := newHarness(t)
	e := wordOpen(t, x)
	x.typeIn("Printed words 打印的字")
	keysOpenTheDialog(t, x)
	src, ok := x.a.print.src.(*wordPrint)
	if !ok {
		t.Fatalf("source %T", x.a.print.src)
	}
	if n := src.count(); n != 1 {
		t.Fatalf("%d pages", n)
	}
	pg, err := src.page(0)
	if err != nil {
		t.Fatal(err)
	}
	pics, _, words := opsOf(pg)
	if pics != 0 || words == 0 {
		t.Fatalf("%d pictures, %d runs of words", pics, words)
	}
	pgs := e.d.Pages()
	if pg.W != float64(pgs[0].W) || pg.H != float64(pgs[0].H) {
		t.Fatalf("%v × %v", pg.W, pg.H)
	}
	var b bytes.Buffer
	if err := printing.WritePDF(context.Background(), &b, &printing.Doc{Pages: []*printing.Page{pg}}, nil); err != nil {
		t.Fatal(err)
	}
	doc, err := pdf.Open(b.Bytes(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Pages()) != 1 {
		t.Fatalf("%d pages read back", len(doc.Pages()))
	}
}

// A job builds its pages, goes off on its own, shows a card, and ends in a file; the window stays alive throughout.
func TestDialogRunsAJobAndCardEnds(t *testing.T) {
	x := newHarness(t)
	wordOpen(t, x)
	x.typeIn("Words")
	keysOpenTheDialog(t, x)
	p := x.a.print
	dir := t.TempDir()
	p.run = &printRun{list: []int{0}, done: func(d *printing.Doc) { x.a.savePDF(d, filepath.Join(dir, "out.pdf")) }}
	x.frame(Input{})
	if x.a.print != nil {
		t.Fatal("the dialog stayed")
	}
	if len(x.a.tasks()) != 1 {
		t.Fatal("no card")
	}
	x.pump(10*time.Second, func() bool { return len(x.a.printJobs) == 0 })
	data, err := os.ReadFile(filepath.Join(dir, "out.pdf"))
	if err != nil || !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Fatalf("the file: %v", err)
	}
}

func TestPrintGoesToTheViewerWithoutAPrinter(t *testing.T) {
	x := newHarness(t)
	wordOpen(t, x)
	x.typeIn("Words")
	keysOpenTheDialog(t, x)
	p := x.a.print
	x.a.printToPrinter(p, []int{0})
	host := x.a.host.(*fakeHost)
	x.pump(10*time.Second, func() bool { return len(host.opened) > 0 })
	if len(host.opened) == 0 || !strings.HasSuffix(host.opened[0], ".pdf") {
		t.Fatalf("opened %v", host.opened)
	}
	os.Remove(host.opened[0])
}

func TestMindPrintsAsVectors(t *testing.T) {
	x := newHarness(t)
	x.a.newMindNow()
	md := x.a.board
	if md == nil {
		t.Fatal("no map")
	}
	m := md.m
	root := m.Roots()[0].ID
	m.Update(root, func(n *mind.Node) { n.Text = "思维导图 Map" })
	x.frame(Input{})
	keysOpenTheDialog(t, x)
	src := x.a.print.src.(*mindPrint)
	pg, err := src.page(0)
	if err != nil {
		t.Fatal(err)
	}
	if pics, shapes, words := opsOf(pg); pics != 0 || shapes == 0 || words == 0 {
		t.Fatalf("%d pictures, %d shapes, %d runs of words", pics, shapes, words)
	}
	src.tiled = true
	if n := src.count(); n < 1 {
		t.Fatalf("%d pages", n)
	}
}

func canvasWithEverything(t *testing.T, x *harness) *artDoc {
	t.Helper()
	x.a.newCanvasNow()
	x.frame(Input{})
	ad := x.a.art
	if ad == nil {
		t.Fatal("no canvas")
	}
	return ad
}

// artRecorder notes what a canvas hands over.
type artRecorder struct {
	pictures, vectors, words, rects int
}

func (r *artRecorder) Picture(*image.RGBA)               { r.pictures++ }
func (r *artRecorder) Vector(*paint.Vector, float64)     { r.vectors++ }
func (r *artRecorder) Words(paint.Text, float64)         { r.words++ }
func (r *artRecorder) Rect(image.Rectangle, color.NRGBA) { r.rects++ }
func (r *artRecorder) PushRect(image.Rectangle)          {}
func (r *artRecorder) PushShape(*paint.Vector)           {}
func (r *artRecorder) Pop()                              {}

func TestCanvasKeepsVectorsAboveWhatBlends(t *testing.T) {
	d := paint.New("base")
	d.Frame = image.Rect(0, 0, 200, 200)
	v := d.AddVector(paint.Vector{Kind: paint.VectorRect, Size: paint.Pt{X: 100, Y: 50}, M: paint.Identity, Fill: paint.VectorPaint{Kind: paint.PaintSolid, Color: color.NRGBA{255, 0, 0, 255}}}, "shape")
	r := &artRecorder{}
	d.Print(d.Frame, 0, r)
	if r.vectors != 1 {
		t.Fatalf("%+v", r)
	}
	// With the layer below it set to multiply, the layers from there down are one picture, and the vector above stays.
	base := d.Node(d.Root.Children[len(d.Root.Children)-1].ID)
	base.Blend = paint.BlendMultiply
	r = &artRecorder{}
	d.Print(d.Frame, 0, r)
	if r.vectors != 1 || r.pictures > 1 {
		t.Fatalf("%+v", r)
	}
	// Now the vector itself blends: it is pixels.
	v.Blend = paint.BlendMultiply
	r = &artRecorder{}
	d.Print(d.Frame, 0, r)
	if r.vectors != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestPrintKeysAreListed(t *testing.T) {
	x := newHarness(t)
	ad := canvasWithEverything(t, x)
	found := false
	for _, c := range x.a.artCommands(ad, x.a.screen) {
		if c.id == "draw.print" {
			found = true
			if !c.key.Ctrl || c.key.Code != 'P' {
				t.Fatalf("key %+v", c.key)
			}
		}
		if c.id == "draw.export-slices" && (!c.key.Shift || !c.key.Ctrl) {
			t.Fatalf("Export slices keeps %+v", c.key)
		}
	}
	if !found {
		t.Fatal("no print command")
	}
}

// TestPrintProof makes a PDF and the dialog's picture of each format, for looking at and for tools/officecheck. It runs
// when FORGE_PRINT_OUT names a folder, on the samples of tools/officecheck/print_samples.py in FORGE_PRINT_SAMPLES.
func TestPrintProof(t *testing.T) {
	out := os.Getenv("FORGE_PRINT_OUT")
	samples := os.Getenv("FORGE_PRINT_SAMPLES")
	if out == "" || samples == "" {
		t.Skip("set FORGE_PRINT_OUT and FORGE_PRINT_SAMPLES")
	}
	shots := os.Getenv("FORGE_SHOTS")
	for _, lang := range []string{"en", "zh"} {
		x := newHarness(t)
		if lang == "zh" {
			x.a.u.lang = Chinese
		}
		export := func(name string, src printSource) {
			t.Helper()
			d, err := allPages(src)
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			data, err := encodePDF(context.Background(), d, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, name+".pdf"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		shot := func(name string) {
			t.Helper()
			x.pump(20*time.Second, func() bool {
				x.frame(Input{})
				p := x.a.print
				return p != nil && !p.loading && p.preview != nil
			})
			x.frame(Input{})
			if shots != "" {
				x.shot("print-" + name + "-" + lang)
			}
			x.a.print = nil
			x.frame(Input{})
		}
		open := func(path string) *officeDoc {
			t.Helper()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			od, err := x.a.openOffice(officeFormatOf(path), path, data)
			if err != nil {
				t.Fatal(err)
			}
			for range 200 {
				x.frame(Input{})
				if s, ok := od.ed.(*sheetEd); !ok || s.wb != nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			x.frame(Input{})
			return od
		}
		// Word.
		od := open(filepath.Join(samples, "word.docx"))
		export("word", x.a.officePrintSource(od))
		x.a.printOffice(od)
		shot("word")
		// Excel.
		od = open(filepath.Join(samples, "sheet.xlsx"))
		src := x.a.officePrintSource(od).(*sheetPrint)
		src.what = sheetWorkbook
		export("sheet", src)
		x.a.openPrint(src)
		shot("sheet")
		// PowerPoint.
		od = open(filepath.Join(samples, "slides.pptx"))
		sl := x.a.officePrintSource(od).(*slidesPrint)
		export("slides", sl)
		export("slides-notes", &slidesPrint{e: sl.e, layout: slidesNotes})
		export("slides-six", &slidesPrint{e: sl.e, layout: slidesSix, hidden: true})
		x.a.openPrint(&slidesPrint{e: sl.e, layout: slidesFour, hidden: true})
		shot("slides")
		printer := func(name string, src printSource) {
			t.Helper()
			if !slices.Contains(printing.Printers(), "Microsoft Print to PDF") {
				return
			}
			tg, err := printing.PrinterTarget("Microsoft Print to PDF")
			if err != nil {
				t.Fatal(err)
			}
			tg.Output = filepath.Join(out, "gdi-"+name+".pdf")
			os.Remove(tg.Output)
			d, err := allPages(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := tg.Print(context.Background(), d, nil); err != nil {
				t.Fatal(err)
			}
		}
		printer("slides", x.a.officePrintSource(od))
		printer("word", x.a.officePrintSource(open(filepath.Join(samples, "word.docx"))))
		printer("sheet", src)
		// PDF: this very kind of file, read back and printed.
		od = open(filepath.Join(out, "slides.pdf"))
		x.a.printOffice(od)
		pp, ok := x.a.print.src.(*pdfPrint)
		if !ok {
			t.Fatalf("source %T", x.a.print.src)
		}
		shot("pdf")
		export("pdf-again", pp)
		// Mind map.
		x.a.newMindNow()
		md := x.a.board
		m := md.m
		root := m.Roots()[0].ID
		m.Update(root, func(n *mind.Node) { n.Text = "打印 Printing" })
		child, err := m.Add(mind.Node{Text: "Word 文档"}, root, 0)
		if err != nil {
			t.Fatal(err)
		}
		m.Add(mind.Node{Text: "Excel 表格"}, root, 1)
		m.Add(mind.Node{Text: "Vector 矢量"}, child, 0)
		x.frame(Input{})
		export("mind", newMindPrint(x.a, md))
		printer("mind", newMindPrint(x.a, md))
		x.a.printMind(md)
		shot("mind")
		// Canvas.
		x.a.newCanvasNow()
		x.frame(Input{})
		ad := x.a.art
		d := ad.d
		grad := paint.VectorPaint{Kind: paint.PaintLinear, From: paint.Pt{}, To: paint.Pt{X: 300, Y: 0}, Stops: []paint.Stop{{At: 0, Color: color.NRGBA{255, 40, 40, 255}}, {At: 1, Color: color.NRGBA{40, 40, 255, 255}}}}
		d.AddVector(paint.Vector{Kind: paint.VectorRect, Size: paint.Pt{X: 300, Y: 200}, Radius: 24, M: paint.Translate(40, 40), Fill: grad,
			Stroke: paint.VectorStroke{Paint: paint.VectorPaint{Kind: paint.PaintSolid, Color: color.NRGBA{A: 255}}, Width: 6}}, "rect")
		d.AddVector(paint.Vector{Kind: paint.VectorEllipse, Size: paint.Pt{X: 200, Y: 200}, M: paint.Translate(260, 160), Fill: paint.VectorPaint{Kind: paint.PaintSolid, Color: color.NRGBA{40, 180, 90, 160}}}, "disc")
		ad.changedAll()
		x.frame(Input{})
		cs := newCanvasPrint(x.a, ad)
		export("canvas", cs)
		printer("canvas", cs)
		x.a.openPrint(cs)
		shot("canvas")
	}
}
