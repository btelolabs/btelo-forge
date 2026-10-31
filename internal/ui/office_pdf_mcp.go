package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/render"
)

// The PDF tools act on a document open in a tab, as the window does; each call is one step of its history.

var pdfMCPTools = []string{"pdf_open", "pdf_state", "pdf_text", "pdf_search", "pdf_render", "pdf_pages", "pdf_annotate", "pdf_add_text", "pdf_add_image", "pdf_fill_form", "pdf_save"}

const pdfMCPInstructions = `PDF documents: pdf_open opens a file in a tab; every other tool takes path, or acts on the active PDF tab. Pages are numbered from 1. Positions are in PDF user space: points, origin at the bottom left of the page, as pdf_state and pdf_text report them. Read with pdf_text and pdf_search, look with pdf_render (a PNG of a page), change with pdf_pages (rotate, delete, move, insert_blank, merge), pdf_annotate (real PDF annotations), pdf_add_text and pdf_add_image (new page content), pdf_fill_form, and write with pdf_save. Saving appends to the file, so what is untouched stays byte for byte, unless pages were deleted, when it rewrites the file. Replaced or covered content stays in the file; nothing here redacts. A placed signature picture is not a cryptographic signature.`

var errNoPDF = errors.New("no PDF is open; call pdf_open first, or name one with path")

// pdfTarget is the document named by path, opened in a tab when it is not open yet, or the active tab's.
func (a *App) pdfTarget(path string) (*pdfEditor, error) {
	if path != "" {
		if !a.showOffice(path) {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			f := officeFormatOf(path)
			if f == nil || f.id != "pdf" {
				return nil, fmt.Errorf("%s is not a PDF", path)
			}
			if _, err := a.openOffice(f, path, data); err != nil {
				return nil, err
			}
		}
	}
	if a.office != nil {
		if e, ok := a.office.ed.(*pdfEditor); ok {
			if e.locked {
				return nil, errors.New("the document is locked; give its password in the window, or with password")
			}
			return e, nil
		}
	}
	return nil, errNoPDF
}

type pdfPageView struct {
	Page       int       `json:"page"`
	Width      float64   `json:"width"`
	Height     float64   `json:"height"`
	Box        []float64 `json:"box"`
	Rotation   int       `json:"rotation,omitempty"`
	Annots     int       `json:"annotations,omitempty"`
	FormFields int       `json:"form_fields,omitempty"`
}

type pdfFieldView struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Value   string   `json:"value,omitempty"`
	Options []string `json:"options,omitempty"`
	States  []string `json:"states,omitempty"`
	Page    int      `json:"page,omitempty"`
}

type pdfStateView struct {
	Path        string         `json:"path"`
	Pages       []pdfPageView  `json:"pages"`
	Saved       bool           `json:"saved"`
	Signed      bool           `json:"signed,omitempty"`
	Encrypted   bool           `json:"encrypted,omitempty"`
	Repaired    bool           `json:"repaired,omitempty"`
	WillRewrite bool           `json:"save_rewrites_file,omitempty"`
	Fields      []pdfFieldView `json:"fields,omitempty"`
	Outline     int            `json:"bookmarks,omitempty"`
}

func (e *pdfEditor) state() pdfStateView {
	d := e.doc
	v := pdfStateView{Path: e.od.path, Saved: e.od.saved == d.Version(), Signed: d.Signed(), Encrypted: d.Encrypted(), Repaired: d.Repaired(),
		WillRewrite: d.NeedsRewrite(), Outline: len(d.Outline())}
	fields := d.Fields()
	onPage := map[int]int{}
	for _, f := range fields {
		fv := pdfFieldView{Name: f.Name, Kind: pdfFieldKind(f.Kind), Value: f.Value}
		for _, o := range f.Options {
			fv.Options = append(fv.Options, o.Export)
		}
		for _, w := range f.Widgets {
			onPage[w.Page]++
			fv.Page = w.Page + 1
			if w.State != "" {
				fv.States = append(fv.States, string(w.State))
			}
		}
		v.Fields = append(v.Fields, fv)
	}
	for i, p := range e.pages {
		w, h := d.ViewSize(p, 0)
		c := d.CropBox(p)
		v.Pages = append(v.Pages, pdfPageView{Page: i + 1, Width: w, Height: h, Box: []float64{c.X0, c.Y0, c.X1, c.Y1}, Rotation: d.Rotation(p),
			Annots: len(d.Annots(i)), FormFields: onPage[i]})
	}
	return v
}

func pdfFieldKind(k pdf.FieldKind) string {
	return [...]string{"text", "checkbox", "radio", "choice", "button", "signature"}[k]
}

// pageRange reads pages from first to last, 1-based; zero means the ends.
func (e *pdfEditor) pageRange(first, last int) (int, int, error) {
	if first <= 0 {
		first = 1
	}
	if last <= 0 {
		last = len(e.pages)
	}
	if first > len(e.pages) || last > len(e.pages) || first > last {
		return 0, 0, fmt.Errorf("the document has %d pages", len(e.pages))
	}
	return first - 1, last - 1, nil
}

func (e *pdfEditor) pageArg(page int) (int, error) {
	if page < 1 || page > len(e.pages) {
		return 0, fmt.Errorf("page %d: the document has %d pages", page, len(e.pages))
	}
	return page - 1, nil
}

func pdfColor(s string, def [3]float64) ([3]float64, error) {
	if s == "" {
		return def, nil
	}
	c, err := parseColor(s)
	if err != nil {
		return def, err
	}
	return [3]float64{float64(c[0]) / 255, float64(c[1]) / 255, float64(c[2]) / 255}, nil
}

func (a *App) addPDFTools(s *mcp.Server) {
	type openIn struct {
		Path     string `json:"path" jsonschema:"the file to open"`
		Password string `json:"password,omitempty" jsonschema:"for a file that needs one"`
	}
	tool(s, a, "pdf_open", "Open a PDF in a tab and report its pages, form fields and state.", func(in openIn) (any, error) {
		if !a.showOffice(in.Path) {
			data, err := os.ReadFile(in.Path)
			if err != nil {
				return nil, err
			}
			f := officeFormatOf(in.Path)
			if f == nil || f.id != "pdf" {
				return nil, fmt.Errorf("%s is not a PDF", in.Path)
			}
			if _, err := a.openOffice(f, in.Path, data); err != nil {
				return nil, err
			}
		}
		e, ok := a.office.ed.(*pdfEditor)
		if !ok {
			return nil, errNoPDF
		}
		if e.locked {
			if in.Password == "" {
				return nil, errors.New("the document needs a password")
			}
			e.password = in.Password
			e.unlock()
			if e.locked {
				return nil, errors.New("wrong password")
			}
		}
		return e.state(), nil
	})
	type pathIn struct {
		Path string `json:"path,omitempty" jsonschema:"the file, else the active PDF tab"`
	}
	tool(s, a, "pdf_state", "The open PDF's pages with sizes, its form fields and saved state.", func(in pathIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		return e.state(), nil
	})
	type textIn struct {
		Path  string `json:"path,omitempty"`
		First int    `json:"first,omitempty" jsonschema:"the first page, from 1"`
		Last  int    `json:"last,omitempty" jsonschema:"the last page"`
		Lines bool   `json:"lines,omitempty" jsonschema:"also give each line's box"`
	}
	type lineView struct {
		Text string    `json:"text"`
		Box  []float64 `json:"box"`
	}
	type pageText struct {
		Page  int        `json:"page"`
		Text  string     `json:"text"`
		Lines []lineView `json:"lines,omitempty"`
	}
	tool(s, a, "pdf_text", "The text of a range of pages, optionally with the position of each line.", func(in textIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		first, last, err := e.pageRange(in.First, in.Last)
		if err != nil {
			return nil, err
		}
		var out []pageText
		for i := first; i <= last; i++ {
			cctx, cancel := context.WithTimeout(a.life, 30*time.Second)
			t, err := e.cache.Text(cctx, e.pages[i])
			cancel()
			if t == nil {
				return nil, err
			}
			pt := pageText{Page: i + 1, Text: t.Full()}
			if in.Lines {
				for k := range t.Lines {
					b := t.Lines[k].Box
					pt.Lines = append(pt.Lines, lineView{t.LineText(k), []float64{b.X0, b.Y0, b.X1, b.Y1}})
				}
			}
			out = append(out, pt)
		}
		return out, nil
	})
	type searchIn struct {
		Path  string `json:"path,omitempty"`
		Query string `json:"query"`
	}
	type hitView struct {
		Page    int         `json:"page"`
		Text    string      `json:"text"`
		Quads   [][]float64 `json:"quads"`
		Context string      `json:"context,omitempty"`
	}
	tool(s, a, "pdf_search", "Find text in the whole document, ignoring case. It gives each hit's page, boxes and surrounding words.", func(in searchIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		var out []hitView
		for i, p := range e.pages {
			cctx, cancel := context.WithTimeout(a.life, 30*time.Second)
			t, _ := e.cache.Text(cctx, p)
			cancel()
			if t == nil {
				continue
			}
			for _, m := range t.Search(in.Query) {
				h := hitView{Page: i + 1, Text: t.String(m.From, m.To), Context: strings.TrimSpace(t.String(max(m.From-30, 0), min(m.To+30, len(t.Chars))))}
				for _, q := range t.Quads(m.From, m.To) {
					h.Quads = append(h.Quads, q[:])
				}
				out = append(out, h)
				if len(out) >= 1000 {
					return out, nil
				}
			}
		}
		return out, nil
	})
	type renderIn struct {
		Path  string  `json:"path,omitempty"`
		Page  int     `json:"page" jsonschema:"from 1"`
		Scale float64 `json:"scale,omitempty" jsonschema:"pixels a point, 1.5 when left out"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "pdf_render", Description: "A PNG of a page as it looks now, annotations included."}, func(ctx context.Context, req *mcp.CallToolRequest, in renderIn) (*mcp.CallToolResult, any, error) {
		return imageResult(call(a, func() ([]byte, error) {
			e, err := a.pdfTarget(in.Path)
			if err != nil {
				return nil, err
			}
			i, err := e.pageArg(in.Page)
			if err != nil {
				return nil, err
			}
			return e.pagePNG(i, in.Scale)
		}))
	})
	type pagesIn struct {
		Path    string  `json:"path,omitempty"`
		Op      string  `json:"op" jsonschema:"rotate, delete, move, insert_blank or merge"`
		Pages   []int   `json:"pages,omitempty" jsonschema:"the pages acted on, from 1"`
		Degrees int     `json:"degrees,omitempty" jsonschema:"for rotate: a multiple of 90, clockwise"`
		To      int     `json:"to,omitempty" jsonschema:"for move, insert_blank and merge: the place the new pages take, from 1; for move, counted without the pages moved"`
		Width   float64 `json:"width,omitempty" jsonschema:"for insert_blank, in points"`
		Height  float64 `json:"height,omitempty"`
		Other   string  `json:"other,omitempty" jsonschema:"for merge: the PDF to bring in"`
	}
	tool(s, a, "pdf_pages", "Rotate, delete, move, insert blank pages or merge another PDF. Links, bookmarks and form fields stay right. One call is one undo step.", func(in pagesIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		var idx []int
		for _, p := range in.Pages {
			i, err := e.pageArg(p)
			if err != nil {
				return nil, err
			}
			idx = append(idx, i)
		}
		d := e.doc
		switch in.Op {
		case "rotate":
			err = d.RotatePages(idx, in.Degrees)
		case "delete":
			err = d.DeletePages(idx)
		case "move":
			err = d.MovePages(idx, in.To-1)
		case "insert_blank":
			w, h := in.Width, in.Height
			if w <= 0 || h <= 0 {
				w, h = d.ViewSize(e.pages[0], 0)
			}
			at := in.To - 1
			if in.To <= 0 {
				at = len(e.pages)
			}
			err = d.InsertBlank(at, w, h)
		case "merge":
			var data []byte
			if data, err = os.ReadFile(in.Other); err == nil {
				var src *pdf.Doc
				if src, err = pdf.Open(data, ""); err == nil {
					at := in.To - 1
					if in.To <= 0 {
						at = len(e.pages)
					}
					err = d.Merge(src, at)
				}
			}
		default:
			return nil, fmt.Errorf("unknown op %q", in.Op)
		}
		if err != nil {
			return nil, err
		}
		e.rev++
		e.sel = nil
		e.clearSelection()
		e.sync()
		return e.state(), nil
	})
	type annotIn struct {
		Path     string      `json:"path,omitempty"`
		Page     int         `json:"page"`
		Kind     string      `json:"kind" jsonschema:"highlight, underline, strikeout, ink, rectangle, ellipse, line, arrow, note or text"`
		Search   string      `json:"search,omitempty" jsonschema:"for highlight, underline and strikeout: the words to mark, found on the page"`
		Quads    [][]float64 `json:"quads,omitempty" jsonschema:"or the boxes to mark, eight numbers each, as pdf_search gives them"`
		Rect     []float64   `json:"rect,omitempty" jsonschema:"x0, y0, x1, y1 for rectangle, ellipse, note and text"`
		Points   [][]float64 `json:"points,omitempty" jsonschema:"for ink: strokes, each x y x y ...; for line and arrow, one stroke of x0 y0 x1 y1"`
		Color    string      `json:"color,omitempty" jsonschema:"as #RRGGBB"`
		Width    float64     `json:"width,omitempty"`
		Contents string      `json:"contents,omitempty" jsonschema:"the words of a note or text box"`
		Size     float64     `json:"size,omitempty" jsonschema:"letter size of a text box"`
	}
	tool(s, a, "pdf_annotate", "Add a real PDF annotation, with its appearance, so other readers show it.", func(in annotIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		i, err := e.pageArg(in.Page)
		if err != nil {
			return nil, err
		}
		col, err := pdfColor(in.Color, e.color)
		if err != nil {
			return nil, err
		}
		spec := pdf.AnnotSpec{Color: col, Width: in.Width, Contents: in.Contents, Size: in.Size}
		if len(in.Rect) == 4 {
			spec.Rect = pdf.Rect{X0: in.Rect[0], Y0: in.Rect[1], X1: in.Rect[2], Y1: in.Rect[3]}.Normal()
		}
		switch in.Kind {
		case "highlight", "underline", "strikeout":
			spec.Kind = map[string]pdf.Name{"highlight": "Highlight", "underline": "Underline", "strikeout": "StrikeOut"}[in.Kind]
			for _, q := range in.Quads {
				if len(q) == 8 {
					spec.Quads = append(spec.Quads, [8]float64(q))
				}
			}
			if in.Search != "" {
				cctx, cancel := context.WithTimeout(a.life, 30*time.Second)
				t, _ := e.cache.Text(cctx, e.pages[i])
				cancel()
				if t != nil {
					for _, m := range t.Search(in.Search) {
						spec.Quads = append(spec.Quads, t.Quads(m.From, m.To)...)
					}
				}
			}
			if len(spec.Quads) == 0 {
				return nil, errors.New("nothing to mark: the words were not found on the page, and no quads were given")
			}
		case "ink":
			spec.Kind, spec.Ink = "Ink", in.Points
			if len(spec.Ink) == 0 {
				return nil, errors.New("ink needs points")
			}
		case "rectangle":
			spec.Kind = "Square"
		case "ellipse":
			spec.Kind = "Circle"
		case "line", "arrow":
			if len(in.Points) != 1 || len(in.Points[0]) != 4 {
				return nil, errors.New("a line needs one stroke of four numbers")
			}
			spec.Kind, spec.Arrow = "Line", in.Kind == "arrow"
			spec.Line = [4]float64(in.Points[0])
		case "note":
			spec.Kind = "Text"
		case "text":
			spec.Kind = "FreeText"
		default:
			return nil, fmt.Errorf("unknown kind %q", in.Kind)
		}
		if _, err := e.doc.AddAnnot(i, spec); err != nil {
			return nil, err
		}
		e.touched(i)
		e.sync()
		e.touched(i)
		return e.state().Pages[i], nil
	})
	type addTextIn struct {
		Path   string  `json:"path,omitempty"`
		Page   int     `json:"page"`
		Text   string  `json:"text"`
		X      float64 `json:"x" jsonschema:"where the first baseline starts"`
		Y      float64 `json:"y"`
		Width  float64 `json:"width,omitempty" jsonschema:"wraps long lines at this width"`
		Family string  `json:"family,omitempty" jsonschema:"a font family on this computer; Chinese text works with any, through a fallback"`
		Size   float64 `json:"size,omitempty"`
		Bold   bool    `json:"bold,omitempty"`
		Italic bool    `json:"italic,omitempty"`
		Color  string  `json:"color,omitempty"`
	}
	tool(s, a, "pdf_add_text", "Write text on a page as new content, with an embedded subset of the font.", func(in addTextIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		i, err := e.pageArg(in.Page)
		if err != nil {
			return nil, err
		}
		col, err := pdfColor(in.Color, [3]float64{})
		if err != nil {
			return nil, err
		}
		st := pdf.TextStyle{Family: in.Family, Bold: in.Bold, Italic: in.Italic, Size: in.Size, Color: col}
		if st.Size <= 0 {
			st.Size = 12
		}
		if st.Family == "" {
			st.Family = "arial"
		}
		if err := e.doc.AddText(i, in.Text, in.X, in.Y, in.Width, st); err != nil {
			return nil, err
		}
		e.touched(i)
		e.sync()
		e.touched(i)
		return e.state().Pages[i], nil
	})
	type addImageIn struct {
		Path  string    `json:"path,omitempty"`
		Page  int       `json:"page"`
		Image string    `json:"image" jsonschema:"the picture file"`
		Rect  []float64 `json:"rect" jsonschema:"x0, y0, x1, y1 it fills"`
	}
	tool(s, a, "pdf_add_image", "Place a picture on a page as new content. A JPEG goes in as it is.", func(in addImageIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		i, err := e.pageArg(in.Page)
		if err != nil {
			return nil, err
		}
		if len(in.Rect) != 4 {
			return nil, errors.New("rect needs four numbers")
		}
		img, data, err := pdfLoadPicture(in.Image)
		if err != nil {
			return nil, err
		}
		r := pdf.Rect{X0: in.Rect[0], Y0: in.Rect[1], X1: in.Rect[2], Y1: in.Rect[3]}.Normal()
		if err := e.doc.AddImage(i, img, data, r); err != nil {
			return nil, err
		}
		e.touched(i)
		e.sync()
		e.touched(i)
		return e.state().Pages[i], nil
	})
	type fillIn struct {
		Path   string            `json:"path,omitempty"`
		Values map[string]string `json:"values" jsonschema:"field name to value: text, an option's export value, or for a check box or radio button one of the states pdf_state lists, empty to clear"`
	}
	tool(s, a, "pdf_fill_form", "Fill in form fields, with new appearances. All the values are one undo step.", func(in fillIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		err = e.doc.Do(func() error {
			for name, v := range in.Values {
				fields := e.doc.Fields()
				found := false
				for k := range fields {
					if fields[k].Name == name {
						found = true
						if err := e.doc.SetField(&fields[k], v); err != nil {
							return fmt.Errorf("%s: %w", name, err)
						}
					}
				}
				if !found {
					return fmt.Errorf("no field %q", name)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		e.rev++
		e.sync()
		return e.state().Fields, nil
	})
	type saveIn struct {
		Path     string `json:"path,omitempty"`
		To       string `json:"to,omitempty" jsonschema:"save a copy to this file instead"`
		Compress bool   `json:"compress,omitempty" jsonschema:"a clean rewrite with unused objects dropped and streams compressed; needs to"`
	}
	tool(s, a, "pdf_save", "Write the document, or with to a copy of it. Saving appends to the file where it can.", func(in saveIn) (any, error) {
		e, err := a.pdfTarget(in.Path)
		if err != nil {
			return nil, err
		}
		e.commitPending()
		e.commitField()
		if in.To == "" {
			if in.Compress {
				return nil, errors.New("compress writes a copy: give to")
			}
			e.od.dirty = e.od.ed.version() != e.od.saved
			if err := a.saveOffice(e.od, true); err != nil {
				return nil, err
			}
			return map[string]any{"saved": e.od.path}, nil
		}
		s := e.doc.Snapshot()
		var data []byte
		if in.Compress {
			data, err = s.Rewrite(true)
		} else {
			data, err = s.Save()
		}
		if err != nil {
			return nil, err
		}
		if err := writeFileWhole(in.To, data); err != nil {
			return nil, err
		}
		return map[string]any{"saved": in.To, "bytes": len(data)}, nil
	})
}

// pagePNG draws a page for a tool.
func (e *pdfEditor) pagePNG(i int, scale float64) ([]byte, error) {
	if scale <= 0 {
		scale = 1.5
	}
	cctx, cancel := context.WithTimeout(e.a.life, 30*time.Second)
	defer cancel()
	res := e.cache.Page(cctx, e.pages[i], render.Options{Scale: scale})
	var b bytes.Buffer
	if err := png.Encode(&b, res.Img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
