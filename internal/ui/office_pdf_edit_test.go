package ui

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/pdftest"
)

func fitPage(x *harness, e *pdfEditor) {
	e.fit = pdfFitPage
	settlePDF(x, e)
}

func TestPDFToolsByMouse(t *testing.T) {
	x, e := pdfHarness(t, 3)
	fitPage(x, e)
	annots := func() int { return len(e.doc.Annots(0)) }
	base := annots()
	// Highlight by dragging over words.
	e.tool = pdfToolHighlight
	ax, ay := pointAt(e, 0, 72, 674)
	bx, by := pointAt(e, 0, 170, 674)
	x.drag(ax, ay, bx, by)
	if annots() != base+1 {
		t.Fatalf("highlight: %d", annots())
	}
	q := e.doc.Annots(0)[base]
	if q.Kind != "Highlight" || len(e.doc.Floats(q.Dict["QuadPoints"])) != 8 {
		t.Fatalf("%+v", q)
	}
	settlePDF(x, e)
	// Ink.
	e.tool = pdfToolInk
	ax, ay = pointAt(e, 0, 300, 300)
	bx, by = pointAt(e, 0, 400, 350)
	x.drag(ax, ay, bx, by)
	if annots() != base+2 {
		t.Fatal("ink")
	}
	// Line with an arrow.
	e.tool = pdfToolArrow
	ax, ay = pointAt(e, 0, 300, 200)
	bx, by = pointAt(e, 0, 400, 200)
	x.drag(ax, ay, bx, by)
	if a := e.doc.Annots(0)[base+2]; a.Kind != "Line" || e.doc.Array(a.Dict["LE"]) == nil {
		t.Fatalf("%+v", a)
	}
	// A note takes words.
	e.tool = pdfToolNote
	ax, ay = pointAt(e, 0, 500, 500)
	x.clickAt(ax, ay)
	if e.pending == nil {
		t.Fatal("no box for the note")
	}
	e.pending.text = "Remember"
	e.commitPending()
	if a := e.doc.Annots(0)[base+3]; a.Contents != "Remember" {
		t.Fatalf("%+v", a)
	}
	// A cover is page content, not an annotation.
	e.tool = pdfToolCover
	ax, ay = pointAt(e, 0, 72, 120)
	bx, by = pointAt(e, 0, 200, 80)
	before := e.doc.Array(e.doc.Pages()[0].Dict["Contents"])
	x.drag(ax, ay, bx, by)
	if after := e.doc.Array(e.doc.Pages()[0].Dict["Contents"]); len(after) <= len(before) {
		t.Fatalf("contents %v", after)
	}
	// Recolour a selected annotation, resize it, delete it.
	e.tool = pdfToolRect
	ax, ay = pointAt(e, 0, 100, 300)
	bx, by = pointAt(e, 0, 200, 200)
	x.drag(ax, ay, bx, by)
	e.tool = pdfToolSelect
	settlePDF(x, e)
	cx, cy := pointAt(e, 0, 150, 250)
	x.clickAt(cx, cy)
	if e.sel == nil || e.sel.kind != "Square" {
		t.Fatalf("%+v", e.sel)
	}
	e.recolour([3]float64{0, 1, 0})
	ref := e.sel.ref
	if c := e.doc.Floats(e.doc.Dict(ref)["C"]); c[1] != 1 {
		t.Fatal(c)
	}
	h := e.handles()[2]
	x.drag(h.X+h.W/2, h.Y+h.H/2, h.X+40, h.Y+40)
	r, _ := e.doc.RectOf(e.doc.Dict(ref)["Rect"])
	if r.W() <= 100 {
		t.Fatalf("not resized: %+v", r)
	}
	x.key(keyDelete, false)
	if e.doc.Dict(ref) == nil {
		t.Fatal("the object itself should stay until a rewrite")
	}
	for _, a := range e.doc.Annots(0) {
		if a.Ref == ref {
			t.Fatal("not deleted")
		}
	}
}

func TestPDFAddAndReplaceText(t *testing.T) {
	x, e := pdfHarness(t, 1)
	fitPage(x, e)
	e.tool = pdfToolText
	ax, ay := pointAt(e, 0, 72, 400)
	x.clickAt(ax, ay)
	if e.pending == nil {
		t.Fatal("no box")
	}
	e.pending.text = "新的文字 new words"
	e.commitPending()
	settlePDF(x, e)
	full := e.textOf(0).Full()
	if !strings.Contains(full, "新的文字 new words") {
		t.Fatalf("%q", full)
	}
	e.tool = pdfToolEditText
	ax, ay = pointAt(e, 0, 100, 674)
	x.clickAt(ax, ay)
	p := e.pending
	if p == nil || !strings.HasPrefix(p.text, "The quick brown fox") || p.style.Size < 11 || p.style.Size > 13 {
		t.Fatalf("%+v", p)
	}
	if pdfPendingHint(p.kind) == "" {
		t.Fatal("replacing must say what it really does")
	}
	p.text = "A replaced line"
	e.commitPending()
	settlePDF(x, e)
	full = e.textOf(0).Full()
	if !strings.Contains(full, "A replaced line") {
		t.Fatalf("%q", full)
	}
	// The old words are covered, and honestly still there.
	if !strings.Contains(full, "quick brown fox") {
		t.Fatalf("%q", full)
	}
	img := e.bitmaps[pdfBitmapKey{e.pages[0].Ref.Num, false}].img
	m := e.doc.ViewMatrix(e.pages[0], e.pxPerPoint(), 0)
	px, py := m.Apply(300, 674) // where the old line ran on and the new does not
	if c := img.RGBAAt(int(px), int(py)); c.R < 250 || c.G < 250 {
		t.Fatalf("not covered: %v", c)
	}
}

func TestPDFThumbnailReorder(t *testing.T) {
	x, e := pdfHarness(t, 4)
	first := e.pages[0].Ref
	x.mx, x.my = e.sideW/2, 0
	side := gfx.Rect{X: 0, Y: e.area.Y, W: e.sideW, H: e.area.H}
	cell := e.a.u.px(150)
	x.drag(side.X+side.W/2, side.Y+e.a.u.px(40), side.X+side.W/2, side.Y+3.2*cell)
	settlePDF(x, e)
	if e.pages[0].Ref == first {
		t.Fatal("the page did not move")
	}
	e.undo()
	settlePDF(x, e)
	if e.pages[0].Ref != first {
		t.Fatal("undo did not bring it back")
	}
}

func TestPDFPageMenu(t *testing.T) {
	x, e := pdfHarness(t, 3)
	e.picked = map[int]bool{1: true}
	menu := e.pagesMenu(e.chosenPages())
	run := func(label string) {
		t.Helper()
		for _, m := range menu {
			if m.label == label {
				m.run()
				settlePDF(x, e)
				menu = e.pagesMenu(e.chosenPages())
				return
			}
		}
		t.Fatalf("no %s", label)
	}
	run("Turn right")
	if e.doc.Rotation(e.pages[1]) != 90 {
		t.Fatal("not turned")
	}
	e.picked = map[int]bool{1: true}
	menu = e.pagesMenu(e.chosenPages())
	run("Delete")
	if len(e.pages) != 2 {
		t.Fatal(len(e.pages))
	}
	run("Insert a blank page")
	if len(e.pages) != 3 {
		t.Fatal(len(e.pages))
	}
	other := filepath.Join(x.dir, "other.pdf")
	os.WriteFile(other, pdftest.Sample(2), 0o644)
	x.h.chosen = []string{other}
	run("Insert a PDF…")
	x.fromSystem()
	if len(e.pages) != 5 {
		t.Fatal(len(e.pages))
	}
	pic := filepath.Join(x.dir, "pic.png")
	f, _ := os.Create(pic)
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	png.Encode(f, img)
	f.Close()
	x.h.chosen = []string{pic}
	run("Insert pictures as pages…")
	x.fromSystem()
	if len(e.pages) != 6 {
		t.Fatal(len(e.pages))
	}
	// Dropped on the window, it comes to be placed.
	if !e.dropped([]string{pic}) || e.pending == nil || e.pending.kind != pdfPendImage {
		t.Fatal("a dropped picture is not being placed")
	}
	e.commitPending()
	settlePDF(x, e)
}

func TestPDFSaveCopyAndCompress(t *testing.T) {
	x, e := pdfHarness(t, 2)
	e.openSaveAs(nil, true)
	x.frame(Input{})
	e.finishSaveAs()
	dst := filepath.Join(filepath.Dir(e.od.path), "sample compressed.pdf")
	x.pump(5*time.Second, func() bool { _, err := os.Stat(dst); return err == nil })
	time.Sleep(50 * time.Millisecond)
	data, _ := os.ReadFile(dst)
	d, err := pdf.Open(data, "")
	if err != nil || d.PageCount() != 2 {
		t.Fatal(err)
	}
	e.openSaveAs([]int{1}, false)
	x.frame(Input{})
	e.finishSaveAs()
	dst = filepath.Join(filepath.Dir(e.od.path), "sample pages.pdf")
	x.pump(5*time.Second, func() bool { _, err := os.Stat(dst); return err == nil })
	time.Sleep(50 * time.Millisecond)
	data, _ = os.ReadFile(dst)
	if d, err = pdf.Open(data, ""); err != nil || d.PageCount() != 1 {
		t.Fatal(err)
	}
}

func TestPDFPassword(t *testing.T) {
	data, err := os.ReadFile("../office/pdf/testdata/enc-aes-256-pw.pdf")
	if err != nil {
		t.Skip(err)
	}
	x := newHarness(t)
	path := filepath.Join(x.dir, "locked.pdf")
	os.WriteFile(path, data, 0o644)
	x.a.openOfficeFile(path)
	x.pump(10*time.Second, func() bool { return x.a.office != nil })
	e := x.a.office.ed.(*pdfEditor)
	if !e.locked {
		t.Fatal("not locked")
	}
	x.frame(Input{})
	x.shot("en-pdf-locked")
	e.password = "nope"
	e.unlock()
	if !e.pwWrong || !e.locked {
		t.Fatal("a wrong password was taken")
	}
	x.frame(Input{})
	e.password = "secret"
	e.unlock()
	if e.locked || e.doc == nil {
		t.Fatal("still locked")
	}
	settlePDF(x, e)
	if e.textOf(0) == nil {
		t.Fatal("no text")
	}
}

func TestPDFSignature(t *testing.T) {
	x, e := pdfHarness(t, 1)
	fitPage(x, e)
	e.openSignPad()
	x.frame(Input{})
	pad := gfx.Rect{X: 0, Y: 0, W: 400, H: 100}
	e.sign.strokes = [][][2]float32{{{10, 10}, {60, 40}, {120, 12}}}
	e.placeSignature(pad)
	if e.pending == nil || e.pending.kind != pdfPendImage {
		t.Fatal("no picture to place")
	}
	x.frame(Input{})
	before := len(e.doc.Array(e.doc.Pages()[0].Dict["Contents"]))
	e.commitPending()
	if n := len(e.doc.Array(e.doc.Pages()[0].Dict["Contents"])); n <= before {
		t.Fatal("not placed")
	}
}

func TestPDFMissingFontStillDraws(t *testing.T) {
	// A CJK font that is not embedded, with a predefined CMap, draws through a system font.
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 100] /Contents 4 0 R /Resources << /Font << /F 5 0 R >> >> >>")
	b.Stream(4, "", []byte("BT /F 24 Tf 10 40 Td <4F60597D> Tj ET"))
	b.Obj(5, "<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UTF16-H /DescendantFonts [6 0 R] >>")
	b.Obj(6, "<< /Type /Font /Subtype /CIDFontType0 /BaseFont /STSong-Light /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 2 >> /DW 1000 >>")
	x := newHarness(t)
	path := filepath.Join(x.dir, "cjk.pdf")
	os.WriteFile(path, b.Bytes(), 0o644)
	x.a.openOfficeFile(path)
	x.pump(10*time.Second, func() bool { return x.a.office != nil })
	e := x.a.office.ed.(*pdfEditor)
	settlePDF(x, e)
	if got := e.textOf(0).Full(); !strings.Contains(got, "你好") {
		t.Fatalf("%q", got)
	}
	bm := e.bitmaps[pdfBitmapKey{e.pages[0].Ref.Num, false}]
	dark := 0
	for i := 0; i+3 < len(bm.img.Pix); i += 4 {
		if bm.img.Pix[i] < 100 {
			dark++
		}
	}
	if dark < 50 {
		t.Fatalf("%d dark pixels", dark)
	}
}

// callPDF runs a tool through the MCP server, as a client does.
func callPDF(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		text := ""
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		t.Fatalf("%s: %s", name, text)
	}
	return res
}

func mcpText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%+v", res.Content[0])
	}
	return tc.Text
}

func TestPDFMCP(t *testing.T) {
	x := newHarness(t)
	path := filepath.Join(x.dir, "tools.pdf")
	os.WriteFile(path, pdftest.Sample(3), 0o644)
	other := filepath.Join(x.dir, "other.pdf")
	os.WriteFile(other, pdftest.Sample(1), 0o644)
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	x.a.addPDFTools(srv)
	cl, sv := mcp.NewInMemoryTransports()
	go srv.Run(context.Background(), sv)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(context.Background(), cl, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	// The tools run on the window's goroutine: keep frames coming while the client waits.
	done := make(chan struct{})
	go func() {
		defer close(done)
		pdfMCPScript(t, session, path, other)
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-done:
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("gave up")
		}
		x.frame(Input{})
		time.Sleep(2 * time.Millisecond)
	}
}

func pdfMCPScript(t *testing.T, s *mcp.ClientSession, path, other string) {
	t.Helper()
	state := mcpText(t, callPDF(t, s, "pdf_open", map[string]any{"path": path}))
	var st pdfStateView
	if err := json.Unmarshal([]byte(state), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Pages) != 3 || len(st.Fields) != 4 {
		t.Fatalf("%s", state)
	}
	if got := mcpText(t, callPDF(t, s, "pdf_text", map[string]any{"first": 2, "last": 2})); !strings.Contains(got, "Page 2 of 3") {
		t.Fatal(got)
	}
	if got := mcpText(t, callPDF(t, s, "pdf_search", map[string]any{"query": "lazy dog"})); strings.Count(got, `"page"`) != 3 {
		t.Fatal(got)
	}
	img := callPDF(t, s, "pdf_render", map[string]any{"page": 1})
	if _, ok := img.Content[0].(*mcp.ImageContent); !ok {
		t.Fatalf("%+v", img.Content[0])
	}
	callPDF(t, s, "pdf_pages", map[string]any{"op": "rotate", "pages": []int{2}, "degrees": 90})
	callPDF(t, s, "pdf_pages", map[string]any{"op": "insert_blank", "to": 2})
	callPDF(t, s, "pdf_pages", map[string]any{"op": "merge", "other": other})
	callPDF(t, s, "pdf_pages", map[string]any{"op": "move", "pages": []int{5}, "to": 1})
	callPDF(t, s, "pdf_pages", map[string]any{"op": "delete", "pages": []int{1}})
	callPDF(t, s, "pdf_annotate", map[string]any{"page": 1, "kind": "highlight", "search": "lazy dog", "color": "#ffee00"})
	callPDF(t, s, "pdf_annotate", map[string]any{"page": 1, "kind": "rectangle", "rect": []float64{100, 100, 200, 200}})
	callPDF(t, s, "pdf_annotate", map[string]any{"page": 1, "kind": "text", "rect": []float64{100, 220, 300, 260}, "contents": "你好 box", "size": 14})
	callPDF(t, s, "pdf_add_text", map[string]any{"page": 1, "text": "写在页面上", "x": 72, "y": 300, "size": 16})
	pic := filepath.Join(filepath.Dir(path), "p.png")
	f, _ := os.Create(pic)
	m := image.NewRGBA(image.Rect(0, 0, 4, 4))
	m.Set(0, 0, color.Black)
	png.Encode(f, m)
	f.Close()
	callPDF(t, s, "pdf_add_image", map[string]any{"page": 1, "image": pic, "rect": []float64{300, 300, 400, 400}})
	callPDF(t, s, "pdf_fill_form", map[string]any{"values": map[string]string{"name": "Lin", "agree": "Yes", "size": "l", "colour": "red"}})
	callPDF(t, s, "pdf_save", nil)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := pdf.Open(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.PageCount() != 4 {
		t.Fatal(d.PageCount())
	}
	var name string
	for _, f := range d.Fields() {
		if f.Name == "name" {
			name = f.Value
		}
	}
	if name != "Lin" {
		t.Fatalf("name %q", name)
	}
	if got := mcpText(t, callPDF(t, s, "pdf_text", map[string]any{"first": 1, "last": 1})); !strings.Contains(got, "写在页面上") {
		t.Fatal(got)
	}
	copyTo := filepath.Join(filepath.Dir(path), "copy.pdf")
	callPDF(t, s, "pdf_save", map[string]any{"to": copyTo, "compress": true})
	if data, err = os.ReadFile(copyTo); err != nil {
		t.Fatal(err)
	}
	if d, err = pdf.Open(data, ""); err != nil || d.PageCount() != 4 {
		t.Fatal(err)
	}
}
