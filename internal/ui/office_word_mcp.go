package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/docx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
)

// The Word tools: they act on a document open in a tab, the active one or the one whose file is named, as the window
// does, each call one step of its history. Paragraphs are numbered in reading order, those in tables included; word_state
// gives the numbers.

var wordMCPTools = []string{"word_new", "word_open", "word_state", "word_read", "word_insert", "word_replace", "word_format", "word_table", "word_picture", "word_page_setup", "word_render", "word_save", "word_export_pdf"}

const wordMCPInstructions = `Word documents (.docx). word_new or word_open opens one in a tab; later calls act on the active tab, or on the file named by path. word_state lists the paragraphs with their index (reading order, table cells included), style and words, the tables, sections and pages. word_read gives the text. word_insert adds paragraphs, word_replace finds and replaces, word_format changes characters or paragraphs by index, word_table makes and changes tables, word_picture adds a picture, word_page_setup sets the paper, orientation and margins and can start a new section so that later pages differ. word_render returns a page as a PNG: look at it to check your work. Each call is one undo step. A document saves itself; word_save saves now, or to a new path. Everything in the file that is not changed stays as it was.`

var errNoWord = errors.New("no Word document is open; call word_new or word_open first, or name one with path")

func wordEditorOf(od *officeDoc) *wordEditor {
	if od == nil {
		return nil
	}
	e, _ := od.ed.(*wordEditor)
	return e
}

// wordTarget is the document named by path, opened in a tab when it is not open yet, or the active tab's.
func (a *App) wordTarget(path string) (*wordEditor, error) {
	var e *wordEditor
	if path == "" {
		e = wordEditorOf(a.office)
		if e == nil {
			return nil, errNoWord
		}
	} else {
		if !a.showOffice(path) {
			f := officeFormatOf(path)
			if f == nil || f.id != "word" {
				return nil, fmt.Errorf("%s is not a Word document", filepath.Base(path))
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if _, err := a.openOffice(f, path, data); err != nil {
				return nil, err
			}
		}
		if e = wordEditorOf(a.office); e == nil {
			return nil, errNoWord
		}
	}
	e.d.Finish()
	return e, nil
}

type wordParaView struct {
	Index int    `json:"index"`
	Style string `json:"style,omitempty"`
	Text  string `json:"text"`
	List  string `json:"list,omitempty"`
	Table int    `json:"table,omitempty"` // 1 + the table it is in
	Row   int    `json:"row,omitempty"`
	Col   int    `json:"col,omitempty"`
}

type wordTableView struct {
	Index int `json:"index"`
	First int `json:"first_paragraph"`
	Rows  int `json:"rows"`
	Cols  int `json:"cols"`
}

type wordSectionView struct {
	Width   float32 `json:"width"`
	Height  float32 `json:"height"`
	Columns int     `json:"columns"`
}

type wordStateView struct {
	Path       string            `json:"path"`
	Pages      int               `json:"pages"`
	Words      int               `json:"words"`
	Paragraphs []wordParaView    `json:"paragraphs"`
	Tables     []wordTableView   `json:"tables,omitempty"`
	Sections   []wordSectionView `json:"sections"`
	Styles     []string          `json:"styles"`
}

// wordTables are the top-level and nested tables in reading order.
func wordTables(d *docx.Doc) []*docx.Table {
	var out []*docx.Table
	seen := map[*docx.Table]bool{}
	for _, b := range d.Body.Paragraphs() {
		for c := b.Cell(); c != nil; c = c.Row.Table.Block.Cell() {
			if t := c.Row.Table; !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

func wordState(e *wordEditor) wordStateView {
	d := e.d
	tables := wordTables(d)
	index := map[*docx.Table]int{}
	v := wordStateView{Path: e.od.path, Pages: len(d.Pages()), Words: d.Count().Words}
	for i, t := range tables {
		index[t] = i
		first := t.FirstParagraph()
		v.Tables = append(v.Tables, wordTableView{Index: i, First: first, Rows: len(t.Rows), Cols: len(t.Grid)})
	}
	for i, b := range d.Body.Paragraphs() {
		p := d.ParaOf(b)
		pv := wordParaView{Index: i, Style: p.PP.Style, Text: strings.ReplaceAll(b.Text(), "￼", "[picture]"), List: p.Label}
		if c := b.Cell(); c != nil {
			pv.Table, pv.Row, pv.Col = index[c.Row.Table]+1, c.Row.Index, c.Col
		}
		v.Paragraphs = append(v.Paragraphs, pv)
	}
	for _, s := range d.Sections() {
		v.Sections = append(v.Sections, wordSectionView{Width: s.W, Height: s.H, Columns: s.Cols})
	}
	for _, s := range d.Styles.StyleList() {
		v.Styles = append(v.Styles, s.ID)
	}
	return v
}

// wordPara is paragraph i of the body.
func wordPara(d *docx.Doc, i int) (*docx.Block, error) {
	ps := d.Body.Paragraphs()
	if i < 0 || i >= len(ps) {
		return nil, fmt.Errorf("there is no paragraph %d; the document has %d", i, len(ps))
	}
	return ps[i], nil
}

// wordPick selects paragraphs from..to, or the words of a paragraph that match.
func wordPick(d *docx.Doc, from, to int, text string) error {
	a, err := wordPara(d, from)
	if err != nil {
		return err
	}
	c := a
	if to > from {
		if c, err = wordPara(d, to); err != nil {
			return err
		}
	}
	if text != "" {
		at := strings.Index(a.Text(), text)
		if at < 0 {
			return fmt.Errorf("paragraph %d does not have those words", from)
		}
		n := len([]rune(a.Text()[:at]))
		d.Sel = docx.Selection{Anchor: docx.Pos{B: a, Off: n}, Caret: docx.Pos{B: a, Off: n + len([]rune(text))}}
		return nil
	}
	d.Sel = docx.Selection{Anchor: docx.Pos{B: a}, Caret: docx.Pos{B: c, Off: d.ParaLen(c)}}
	return nil
}

// wordOne runs edits as one step of history.
func wordOne(d *docx.Doc, fn func() error) error {
	d.Begin("")
	defer d.Commit()
	return fn()
}

// renderWord is a page as a PNG. The document's pictures are read first, so the image is complete.
func (e *wordEditor) renderWord(page int, zoom float64) ([]byte, error) {
	d := e.d
	d.Finish()
	pages := d.Pages()
	if page < 0 || page >= len(pages) {
		return nil, fmt.Errorf("there is no page %d; the document has %d", page+1, len(pages))
	}
	if zoom <= 0 {
		zoom = 1.5
	}
	scale := float32(min(max(zoom, 0.25), 4))
	pg := pages[page]
	w, h := int(pg.W*scale+0.5), int(pg.H*scale+0.5)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := gfx.Wrap(img)
	c.Clear(colWhite)
	d.Pics.Sync = true
	d.DrawPage(c, pg, 0, 0, scale)
	d.Pics.Sync = false
	d.Pics.Wait()
	c.Clear(colWhite)
	d.DrawPage(c, pg, 0, 0, scale)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (a *App) addWordTools(s *mcp.Server) {
	format := func() *officeFormat {
		for _, f := range officeFormats {
			if f.id == "word" {
				return f
			}
		}
		return nil
	}
	type pathIn struct {
		Path string `json:"path,omitempty" jsonschema:"the document's file; leave out for the active tab"`
	}
	type openIn struct {
		Path string `json:"path" jsonschema:"the .docx file to open"`
	}
	tool(s, a, "word_new", "Make a new, empty Word document in its own tab, A4, and return its state.", func(in struct{}) (any, error) {
		od, err := a.newOffice(format())
		if err != nil {
			return nil, err
		}
		e := wordEditorOf(od)
		e.d.Finish()
		return wordState(e), nil
	})
	tool(s, a, "word_open", "Open a Word document in a tab and return its state.", func(in openIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		return wordState(e), nil
	})
	tool(s, a, "word_state", "The document's structure: paragraphs with their index, style and words; tables; sections; pages; styles.", func(in pathIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		return wordState(e), nil
	})
	type readIn struct {
		Path string `json:"path,omitempty"`
		From int    `json:"from,omitempty" jsonschema:"the first paragraph"`
		To   int    `json:"to,omitempty" jsonschema:"the last paragraph; leave out for all"`
	}
	tool(s, a, "word_read", "The document's words, a paragraph to a line.", func(in readIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		ps := e.d.Body.Paragraphs()
		to := in.To
		if to <= 0 || to >= len(ps) {
			to = len(ps) - 1
		}
		var b strings.Builder
		for i := max(in.From, 0); i <= to; i++ {
			b.WriteString(strings.ReplaceAll(ps[i].Text(), "￼", "[picture]"))
			b.WriteByte('\n')
		}
		return map[string]any{"text": b.String(), "paragraphs": len(ps)}, nil
	})
	type insertIn struct {
		Path   string `json:"path,omitempty"`
		Text   string `json:"text" jsonschema:"the words; each new line makes a paragraph"`
		After  *int   `json:"after,omitempty" jsonschema:"the paragraph to add after; leave out to add at the end; -1 adds at the start"`
		At     *int   `json:"at,omitempty" jsonschema:"instead of after: a character offset in paragraph 'into' to insert at, staying in that paragraph"`
		Into   int    `json:"into,omitempty" jsonschema:"the paragraph 'at' is in"`
		Style  string `json:"style,omitempty" jsonschema:"a style id for the new paragraphs, such as Heading1"`
		Break_ bool   `json:"page_break,omitempty" jsonschema:"start with a page break"`
	}
	tool(s, a, "word_insert", "Add text: new paragraphs after a paragraph, or words at a place inside one.", func(in insertIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		d := e.d
		err = wordOne(d, func() error {
			if in.At != nil {
				b, err := wordPara(d, in.Into)
				if err != nil {
					return err
				}
				if *in.At < 0 || *in.At > d.ParaLen(b) {
					return fmt.Errorf("paragraph %d has %d characters", in.Into, d.ParaLen(b))
				}
				at := docx.Pos{B: b, Off: *in.At}
				d.Sel = docx.Selection{Anchor: at, Caret: at}
				d.Insert(in.Text)
				return nil
			}
			ps := d.Body.Paragraphs()
			var at docx.Pos
			switch {
			case in.After == nil:
				last := ps[len(ps)-1]
				at = docx.Pos{B: last, Off: d.ParaLen(last)}
			case *in.After < 0:
				at = docx.Pos{B: ps[0]}
			default:
				b, err := wordPara(d, *in.After)
				if err != nil {
					return err
				}
				at = docx.Pos{B: b, Off: d.ParaLen(b)}
			}
			d.Sel = docx.Selection{Anchor: at, Caret: at}
			empty := in.After == nil && d.ParaLen(at.B) == 0
			before := in.After != nil && *in.After < 0
			if !empty && !before {
				d.Enter()
			}
			first := d.Sel.Caret.B
			if in.Break_ {
				d.PageBreak()
			}
			d.Insert(in.Text)
			if before {
				d.Enter()
			}
			if in.Style != "" {
				last := d.Sel.Caret
				d.Sel = docx.Selection{Anchor: docx.Pos{B: d.Current(first)}, Caret: last}
				if before {
					d.Sel.Caret = docx.Pos{B: d.Prev(last.B)}
				}
				d.SetStyle(in.Style)
				d.Sel = docx.Selection{Anchor: last, Caret: last}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		d.Finish()
		return wordState(e), nil
	})
	type replaceIn struct {
		Path      string `json:"path,omitempty"`
		Find      string `json:"find"`
		With      string `json:"with"`
		MatchCase bool   `json:"match_case,omitempty"`
	}
	tool(s, a, "word_replace", "Replace every match of words, in the look of what is replaced. Returns how many.", func(in replaceIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		if in.Find == "" {
			return nil, errors.New("say what to find")
		}
		return map[string]int{"replaced": e.d.ReplaceAll(in.Find, in.With, in.MatchCase)}, nil
	})
	type formatIn struct {
		Path      string   `json:"path,omitempty"`
		From      int      `json:"from" jsonschema:"the first paragraph"`
		To        int      `json:"to,omitempty" jsonschema:"the last paragraph, when several"`
		Text      string   `json:"text,omitempty" jsonschema:"only these words of paragraph 'from'"`
		Bold      *bool    `json:"bold,omitempty"`
		Italic    *bool    `json:"italic,omitempty"`
		Underline *bool    `json:"underline,omitempty"`
		Strike    *bool    `json:"strike,omitempty"`
		Font      *string  `json:"font,omitempty"`
		Size      *float32 `json:"size,omitempty" jsonschema:"points"`
		Color     *string  `json:"color,omitempty" jsonschema:"RRGGBB, or empty for automatic"`
		Highlight *string  `json:"highlight,omitempty" jsonschema:"yellow, green, cyan, magenta, blue, red, darkBlue, darkCyan, darkGreen, darkMagenta, darkRed, darkYellow, darkGray, lightGray, black, or empty"`
		Script    *string  `json:"script,omitempty" jsonschema:"super, sub, or empty"`
		Style     string   `json:"style,omitempty" jsonschema:"a paragraph style id"`
		Align     string   `json:"align,omitempty" jsonschema:"left, center, right or justify"`
		Line      float32  `json:"line_spacing,omitempty" jsonschema:"a multiple, 1.5 for example"`
		Before    *float32 `json:"space_before,omitempty" jsonschema:"points"`
		After     *float32 `json:"space_after,omitempty" jsonschema:"points"`
		Indent    *float32 `json:"indent,omitempty" jsonschema:"the left indent in points"`
		First     *float32 `json:"first_line,omitempty" jsonschema:"the first line's further indent in points; negative hangs"`
		List      string   `json:"list,omitempty" jsonschema:"bullet, number, multilevel or chinese, to toggle a list"`
		Clear     bool     `json:"clear,omitempty" jsonschema:"take all direct formatting away first"`
	}
	tool(s, a, "word_format", "Change characters or paragraphs: the look of words, or a paragraph's style, alignment, spacing, indent and list.", func(in formatIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		d := e.d
		err = wordOne(d, func() error {
			if err := wordPick(d, in.From, in.To, in.Text); err != nil {
				return err
			}
			if in.Clear {
				d.ClearFormatting()
			}
			if in.Style != "" {
				d.SetStyle(in.Style)
			}
			c := docx.RunChange{Bold: in.Bold, Italic: in.Italic, Underline: in.Underline, Strike: in.Strike, Family: in.Font, Size: in.Size, Color: in.Color, Highlight: in.Highlight}
			if in.Color != nil {
				v := strings.TrimPrefix(strings.ToUpper(*in.Color), "#")
				c.Color = &v
			}
			if in.Script != nil {
				v := int8(0)
				switch *in.Script {
				case "super", "superscript":
					v = 1
				case "sub", "subscript":
					v = -1
				}
				c.Vert = &v
			}
			d.FormatRuns(c)
			switch in.Align {
			case "left":
				d.SetAlign(flow.Left)
			case "center", "centre":
				d.SetAlign(flow.Center)
			case "right":
				d.SetAlign(flow.Right)
			case "justify":
				d.SetAlign(flow.Justify)
			case "":
			default:
				return fmt.Errorf("no alignment %q", in.Align)
			}
			if in.Line > 0 {
				d.SetLineSpacing(in.Line)
			}
			if in.Before != nil || in.After != nil {
				b, c := float32(-1), float32(-1)
				if in.Before != nil {
					b = *in.Before
				}
				if in.After != nil {
					c = *in.After
				}
				d.SetSpacing(b, c)
			}
			if in.Indent != nil || in.First != nil {
				left := float32(-1)
				if in.Indent != nil {
					left = *in.Indent
				}
				first := float32(0)
				if in.First != nil {
					first = *in.First
				}
				d.SetIndent(left, first, in.First != nil)
			}
			switch in.List {
			case "bullet":
				d.ToggleList(docx.ListBullet)
			case "number":
				d.ToggleList(docx.ListNumber)
			case "multilevel":
				d.ToggleList(docx.ListMulti)
			case "chinese":
				d.ToggleList(docx.ListChinese)
			case "":
			default:
				return fmt.Errorf("no list %q", in.List)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		d.Finish()
		return wordState(e), nil
	})
	type tableIn struct {
		Path    string     `json:"path,omitempty"`
		Op      string     `json:"op" jsonschema:"new, set, add_row, add_column, delete_row, delete_column, merge or delete"`
		After   *int       `json:"after,omitempty" jsonschema:"new: the paragraph to put it after; leave out for the end"`
		Rows    int        `json:"rows,omitempty" jsonschema:"new: rows"`
		Cols    int        `json:"cols,omitempty" jsonschema:"new: columns"`
		Cells   [][]string `json:"cells,omitempty" jsonschema:"new, set: the words of the cells, row by row"`
		Table   int        `json:"table,omitempty" jsonschema:"the table's index from word_state"`
		Row     int        `json:"row,omitempty" jsonschema:"the row, or the first row of a merge"`
		Col     int        `json:"col,omitempty" jsonschema:"the grid column, or the first of a merge"`
		ToRow   int        `json:"to_row,omitempty" jsonschema:"merge: the last row"`
		ToCol   int        `json:"to_col,omitempty" jsonschema:"merge: the last grid column"`
		Text    string     `json:"text,omitempty" jsonschema:"set: the words of one cell, at row and col"`
		Before  bool       `json:"before,omitempty" jsonschema:"add_row, add_column: above or left of row or col instead of below or right"`
		Heading bool       `json:"heading,omitempty" jsonschema:"new: make the first row repeat on each page"`
	}
	tool(s, a, "word_table", "Make a table or change one: its cells, rows and columns, merged cells.", func(in tableIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		d := e.d
		err = wordOne(d, func() error {
			return wordTableOp(e, in.Op, wordTableArgs(in.After, in.Rows, in.Cols, in.Cells, in.Table, in.Row, in.Col, in.ToRow, in.ToCol, in.Text, in.Before))
		})
		if err != nil {
			return nil, err
		}
		d.Finish()
		return wordState(e), nil
	})
	type pictureIn struct {
		Path   string  `json:"path,omitempty"`
		File   string  `json:"file" jsonschema:"the picture's file: PNG, JPEG, GIF, BMP, WebP or TIFF"`
		After  *int    `json:"after,omitempty" jsonschema:"the paragraph to add it in a new paragraph after; leave out for the end"`
		Width  float32 `json:"width,omitempty" jsonschema:"points; leave out for the natural size, or the text's width when wider"`
		Height float32 `json:"height,omitempty" jsonschema:"points"`
	}
	tool(s, a, "word_picture", "Add a picture in a paragraph of its own.", func(in pictureIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(in.File)
		if err != nil {
			return nil, err
		}
		d := e.d
		err = wordOne(d, func() error {
			if err := wordEndOf(d, in.After); err != nil {
				return err
			}
			return d.InsertPicture(data, filepath.Base(in.File), in.Width, in.Height)
		})
		if err != nil {
			return nil, err
		}
		d.Finish()
		return wordState(e), nil
	})
	type renderIn struct {
		Path string  `json:"path,omitempty"`
		Page int     `json:"page,omitempty" jsonschema:"the page, counting from 1"`
		Zoom float64 `json:"zoom,omitempty" jsonschema:"pixels a point, 1.5 by default"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "word_render", Description: "A page of the document as a PNG, as it will print."},
		func(ctx context.Context, req *mcp.CallToolRequest, in renderIn) (*mcp.CallToolResult, any, error) {
			return imageResult(call(a, func() ([]byte, error) {
				e, err := a.wordTarget(in.Path)
				if err != nil {
					return nil, err
				}
				page := in.Page
				if page == 0 {
					page = 1
				}
				return e.renderWord(page-1, in.Zoom)
			}))
		})
	type saveIn struct {
		Path string `json:"path,omitempty" jsonschema:"the document; leave out for the active tab"`
		To   string `json:"to,omitempty" jsonschema:"a new file to save as; the tab goes on with it"`
	}
	type pageIn struct {
		Path         string    `json:"path,omitempty" jsonschema:"a .docx file; left out: the active tab"`
		Paper        string    `json:"paper,omitempty" jsonschema:"A3, A4, A5, B5, 16K, Letter or Legal"`
		WidthMM      *float64  `json:"width_mm,omitempty" jsonschema:"the page's width, instead of a paper"`
		HeightMM     *float64  `json:"height_mm,omitempty" jsonschema:"the page's height, instead of a paper"`
		Orientation  string    `json:"orientation,omitempty" jsonschema:"portrait or landscape"`
		MarginsMM    []float64 `json:"margins_mm,omitempty" jsonschema:"top, bottom, left and right"`
		ThisSection  bool      `json:"this_section,omitempty" jsonschema:"change only the caret's section; left out: the whole document"`
		SectionBreak bool      `json:"section_break,omitempty" jsonschema:"first start a new section on a new page at the caret"`
	}
	tool(s, a, "word_page_setup", "Set the paper size, orientation and margins, and read them back. Sizes are millimetres.", func(in pageIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		d := e.d
		if in.SectionBreak {
			if !d.CanSectionBreak() {
				return nil, errors.New("a section can only end between the body's paragraphs")
			}
			d.SectionBreak()
		}
		sec := d.SectionAtCaret()
		if sec == nil {
			return nil, errors.New("the document has no section")
		}
		p := docx.PageSetupOf(sec)
		if in.Paper != "" {
			i := slices.IndexFunc(wordPapers, func(w wordPaper) bool { return strings.EqualFold(w.name, in.Paper) })
			if i < 0 {
				return nil, fmt.Errorf("no paper called %q", in.Paper)
			}
			p = wordPapers[i].on(p)
		}
		if in.WidthMM != nil {
			p.W = wordPoints(*in.WidthMM)
		}
		if in.HeightMM != nil {
			p.H = wordPoints(*in.HeightMM)
		}
		if in.Orientation != "" {
			if in.Orientation != "portrait" && in.Orientation != "landscape" {
				return nil, fmt.Errorf("no orientation called %q", in.Orientation)
			}
			if (in.Orientation == "landscape") != p.Landscape() {
				p = p.Turned()
			}
		}
		if len(in.MarginsMM) != 0 {
			if len(in.MarginsMM) != 4 {
				return nil, errors.New("margins_mm takes top, bottom, left and right")
			}
			p.MT, p.MB, p.ML, p.MR = wordPoints(in.MarginsMM[0]), wordPoints(in.MarginsMM[1]), wordPoints(in.MarginsMM[2]), wordPoints(in.MarginsMM[3])
		}
		d.SetPageSetup(p, !in.ThisSection)
		sec = d.SectionAtCaret()
		return map[string]any{"width_mm": wordMM(sec.W), "height_mm": wordMM(sec.H), "margins_mm": []float64{wordMM(sec.MT), wordMM(sec.MB), wordMM(sec.ML), wordMM(sec.MR)}, "sections": len(d.Sections())}, nil
	})

	tool(s, a, "word_save", "Save the document now, or to a new file.", func(in saveIn) (any, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, err
		}
		od := e.od
		if in.To != "" {
			if !strings.EqualFold(filepath.Ext(in.To), ".docx") && officeFormatOf(in.To) != od.format {
				return nil, errors.New("save a Word document as .docx")
			}
			data, err := e.d.Bytes()
			if err != nil {
				return nil, err
			}
			if err := writeFileWhole(in.To, data); err != nil {
				return nil, err
			}
			od.path, od.saved, od.dirty = in.To, e.d.Version(), false
			return map[string]string{"path": in.To}, nil
		}
		od.dirty = true
		if err := a.saveOffice(od, true); err != nil {
			return nil, err
		}
		return map[string]string{"path": od.path}, nil
	})
}

// wordEndOf puts the caret in a new paragraph after one, or at the document's end, in one that is empty if it can.
func wordEndOf(d *docx.Doc, after *int) error {
	ps := d.Body.Paragraphs()
	b := ps[len(ps)-1]
	if after != nil {
		var err error
		if b, err = wordPara(d, *after); err != nil {
			return err
		}
	}
	at := docx.Pos{B: b, Off: d.ParaLen(b)}
	d.Sel = docx.Selection{Anchor: at, Caret: at}
	if after != nil || d.ParaLen(b) > 0 {
		d.Enter()
	}
	return nil
}

// wordTableIn are word_table's arguments.
type wordTableIn struct {
	after                         *int
	rows, cols                    int
	cells                         [][]string
	table, row, col, toRow, toCol int
	text                          string
	before                        bool
}

func wordTableArgs(after *int, rows, cols int, cells [][]string, table, row, col, toRow, toCol int, text string, before bool) wordTableIn {
	return wordTableIn{after, rows, cols, cells, table, row, col, toRow, toCol, text, before}
}

// wordTableOp does one word_table operation.
func wordTableOp(e *wordEditor, op string, in wordTableIn) error {
	d := e.d
	if op == "new" {
		rows, cols := in.rows, in.cols
		if len(in.cells) > 0 {
			rows = len(in.cells)
			for _, r := range in.cells {
				cols = max(cols, len(r))
			}
		}
		if err := wordEndOf(d, in.after); err != nil {
			return err
		}
		if err := d.InsertTable(rows, cols); err != nil {
			return err
		}
		if len(in.cells) == 0 {
			return nil
		}
		t := d.Sel.Caret.B.Cell().Row.Table
		return wordSetCells(d, t, 0, in.cells)
	}
	tables := wordTables(d)
	if in.table < 0 || in.table >= len(tables) {
		return fmt.Errorf("there is no table %d; the document has %d", in.table, len(tables))
	}
	t := tables[in.table]
	if in.row < 0 || in.row >= len(t.Rows) {
		return fmt.Errorf("table %d has %d rows", in.table, len(t.Rows))
	}
	switch op {
	case "set":
		cells := in.cells
		if cells == nil {
			cells = [][]string{{in.text}}
		}
		return wordSetCells(d, t, in.row, cellsFrom(cells, in.col))
	case "merge":
		return d.MergeRange(t, in.row, in.toRow, in.col, in.toCol+1)
	}
	if err := wordSelectCell(d, t, in.row, in.col); err != nil {
		return err
	}
	switch op {
	case "add_row":
		return d.AddRow(in.before)
	case "add_column":
		return d.AddColumn(in.before)
	case "delete_row":
		return d.DeleteRow()
	case "delete_column":
		return d.DeleteColumn()
	case "delete":
		return d.DeleteTable()
	}
	return fmt.Errorf("no operation %q", op)
}

// cellsFrom shifts rows of words right by col grid columns; an empty string leaves a cell alone.
func cellsFrom(cells [][]string, col int) [][]string {
	if col == 0 {
		return cells
	}
	out := make([][]string, len(cells))
	for i, r := range cells {
		out[i] = append(make([]string, col), r...)
	}
	return out
}

func wordSelectCell(d *docx.Doc, t *docx.Table, row, col int) error {
	c := docx.CellAt(t, row, col)
	if c == nil {
		return fmt.Errorf("no cell at row %d, column %d", row, col)
	}
	at := docx.Pos{B: c.C.Blocks[0]}
	d.Sel = docx.Selection{Anchor: at, Caret: at}
	return nil
}

// wordSetCells writes words in cells from a row on. Empty strings are left alone.
func wordSetCells(d *docx.Doc, t *docx.Table, from int, cells [][]string) error {
	node := t.Block
	for r, row := range cells {
		for col, text := range row {
			if text == "" {
				continue
			}
			tb := d.CurrentTable(node)
			if tb == nil || from+r >= len(tb.Rows) {
				return errors.New("the table is smaller than the words given")
			}
			c := docx.CellAt(tb, from+r, col)
			if c == nil {
				continue
			}
			first := c.C.Blocks[0]
			last := c.C.Blocks[len(c.C.Blocks)-1]
			d.Sel = docx.Selection{Anchor: docx.Pos{B: first}, Caret: docx.Pos{B: last, Off: d.ParaLen(last)}}
			d.Insert(text)
		}
	}
	return nil
}
