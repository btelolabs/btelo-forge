package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
)

// The presentation tools act on a deck open in a tab: the one a path names, opened if need be, or the active one.
// Slides are numbered from one, shapes by their ids on a slide, and positions are in points from the slide's top left.

var slidesMCPTools = []string{"slides_new", "slides_open", "slides_state", "slides_add_slide", "slides_delete_slide", "slides_move_slide",
	"slides_add_shape", "slides_set_text", "slides_update_shape", "slides_add_picture", "slides_add_table", "slides_notes", "slides_render", "slides_save", "slides_export_pdf"}

const slidesMCPInstructions = `Presentations (.pptx). Open or make one with slides_new or slides_open; every other tool acts on the file named by path, or the active tab. Each call is one undo step, and changes are saved by themselves a moment later; slides_save saves at once.

Slides are numbered from 1. slides_state lists each slide's layout and shapes, with their ids, boxes and words; positions are in points, from the slide's top left (a 16:9 slide is 960 by 540). Empty placeholders are shapes too: fill them with slides_set_text rather than adding text boxes over them. Check your work with slides_render, which returns a PNG of a slide as the slideshow shows it.`

var errNoSlides = errors.New("no presentation is open; call slides_new or slides_open first, or name one with path")

// slidesTarget is the deck a path names, opened in a tab when it is not open yet, or the active tab's.
func (a *App) slidesTarget(path string) (*slidesEditor, error) {
	if path != "" {
		for i, d := range a.docs {
			if d.office != nil && samePath(d.office.path, path) {
				a.switchTab(i)
				if e, ok := d.office.ed.(*slidesEditor); ok {
					return e, nil
				}
				return nil, fmt.Errorf("%s is not a presentation", path)
			}
		}
		f := officeFormatOf(path)
		if f == nil || f.id != "slides" {
			return nil, fmt.Errorf("%s is not a presentation", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		od, err := a.openOffice(f, path, data)
		if err != nil {
			return nil, err
		}
		return od.ed.(*slidesEditor), nil
	}
	if a.office != nil {
		if e, ok := a.office.ed.(*slidesEditor); ok {
			return e, nil
		}
	}
	return nil, errNoSlides
}

// slidesAt is a deck and one of its slides, by number; 0 means the one shown.
func (a *App) slidesAt(path string, n int) (*slidesEditor, *pptx.Slide, error) {
	e, err := a.slidesTarget(path)
	if err != nil {
		return nil, nil, err
	}
	if e.act != nil {
		return nil, nil, errors.New("the deck is being dragged on with the mouse; try again in a moment")
	}
	e.endText()
	if n == 0 {
		n = e.cur + 1
	}
	if n < 1 || n > len(e.d.Slides) {
		return nil, nil, fmt.Errorf("there is no slide %d; the deck has %d", n, len(e.d.Slides))
	}
	return e, e.d.Slides[n-1], nil
}

type slideShapeView struct {
	ID          int              `json:"id"`
	Kind        string           `json:"kind"`
	Name        string           `json:"name,omitempty"`
	Placeholder string           `json:"placeholder,omitempty"`
	Box         []float64        `json:"box"` // x, y, width, height in points
	Rotation    float64          `json:"rotation,omitempty"`
	Text        string           `json:"text,omitempty"`
	Prompt      bool             `json:"empty_placeholder,omitempty"`
	Table       [][]string       `json:"table,omitempty"`
	Children    []slideShapeView `json:"children,omitempty"`
}

type slideView struct {
	Number int              `json:"number"`
	Layout string           `json:"layout"`
	Hidden bool             `json:"hidden,omitempty"`
	Notes  string           `json:"notes,omitempty"`
	Shapes []slideShapeView `json:"shapes"`
}

type slidesState struct {
	Path    string      `json:"path"`
	Width   float64     `json:"width"`
	Height  float64     `json:"height"`
	Layouts []string    `json:"layouts"`
	Current int         `json:"current"`
	Slides  []slideView `json:"slides"`
}

func (e *slidesEditor) shapeView(s *pptx.Slide, k *pptx.Shape) slideShapeView {
	kinds := map[pptx.Kind]string{pptx.KindShape: "shape", pptx.KindPicture: "picture", pptx.KindGroup: "group", pptx.KindConnector: "connector", pptx.KindTable: "table", pptx.KindGraphic: "graphic"}
	v := slideShapeView{ID: k.ID, Kind: kinds[k.Kind], Name: k.Name, Box: []float64{k.Box.X, k.Box.Y, k.Box.W, k.Box.H}, Rotation: k.Box.Rot}
	if k.Ph != nil {
		v.Placeholder = k.Ph.Type
		if v.Placeholder == "" {
			v.Placeholder = "content"
		}
		v.Prompt = k.EmptyPlaceholder()
	}
	if b, _ := e.d.Body(s, pptx.Target{ID: k.ID}); b != nil {
		v.Text = strings.ReplaceAll(pptx.PlainText(b), "\v", "\n")
	}
	if k.Kind == pptx.KindTable {
		for _, row := range k.Node.Path("a:graphic", "a:graphicData", "a:tbl").Elements("a:tr") {
			var cells []string
			for _, tc := range row.Elements("a:tc") {
				cells = append(cells, pptx.PlainText(tc.Child("a:txBody")))
			}
			v.Table = append(v.Table, cells)
		}
	}
	for _, c := range k.Kids {
		v.Children = append(v.Children, e.shapeView(s, c))
	}
	return v
}

func (e *slidesEditor) layoutNames() []string {
	var out []string
	for _, m := range e.d.Masters {
		for _, l := range m.Layouts {
			out = append(out, l.Name)
		}
	}
	return out
}

func (e *slidesEditor) state() slidesState {
	st := slidesState{Path: e.od.path, Width: e.d.W, Height: e.d.H, Layouts: e.layoutNames(), Current: e.cur + 1}
	for i, s := range e.d.Slides {
		v := slideView{Number: i + 1, Layout: s.Layout.Name, Hidden: s.Hidden(), Notes: e.d.Notes(s), Shapes: []slideShapeView{}}
		for _, k := range e.d.Shapes(s) {
			v.Shapes = append(v.Shapes, e.shapeView(s, k))
		}
		st.Slides = append(st.Slides, v)
	}
	return st
}

func (e *slidesEditor) layoutNamed(name string) (*pptx.Layout, error) {
	for _, m := range e.d.Masters {
		for _, l := range m.Layouts {
			if strings.EqualFold(l.Name, name) || strings.EqualFold(l.Type, name) {
				return l, nil
			}
		}
	}
	if name == "" {
		for _, m := range e.d.Masters {
			for _, l := range m.Layouts {
				if l.Type == "obj" {
					return l, nil
				}
			}
		}
		return e.d.Masters[0].Layouts[0], nil
	}
	return nil, fmt.Errorf("there is no layout %q; the deck has: %s", name, strings.Join(e.layoutNames(), ", "))
}

func parseSlideColor(s string) (color.RGBA, error) {
	if s == "none" {
		return color.RGBA{}, nil
	}
	c, err := parseColor(s)
	return color.RGBA{c[0], c[1], c[2], 255}, err
}

func (e *slidesEditor) boxOf(b []float64, fallback pptx.Box) (pptx.Box, error) {
	if len(b) == 0 {
		return fallback, nil
	}
	if len(b) != 4 {
		return pptx.Box{}, errors.New("a box is x, y, width and height")
	}
	return pptx.Box{X: b[0], Y: b[1], W: b[2], H: b[3]}, nil
}

// slidesRenderPNG draws a slide as the slideshow shows it, at most max pixels wide.
func (e *slidesEditor) slidesRenderPNG(s *pptx.Slide, width int) ([]byte, error) {
	if width <= 0 {
		width = 1280
	}
	width = min(width, 4096)
	h := int(float64(width) * e.d.H / e.d.W)
	img := image.NewRGBA(image.Rect(0, 0, width, h))
	c := gfx.Wrap(img)
	c.Clear(colWhite)
	e.r.Slide(c, s, pptx.View{Scale: float64(width) / e.d.W}, pptx.Options{})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (a *App) addSlidesTools(s *mcp.Server) {
	type pathIn struct {
		Path string `json:"path,omitempty" jsonschema:"the .pptx file; the active tab when left out"`
	}
	tool(s, a, "slides_new", "Make a new presentation in its own tab, with a title slide, and return its state.", func(in none) (any, error) {
		f := officeFormatOf("x.pptx")
		od, err := a.newOffice(f)
		if err != nil {
			return nil, err
		}
		return od.ed.(*slidesEditor).state(), nil
	})
	type openIn struct {
		Path string `json:"path" jsonschema:"absolute path of the .pptx file"`
	}
	tool(s, a, "slides_open", "Open a presentation in a tab, or show its tab, and return its state.", func(in openIn) (any, error) {
		e, err := a.slidesTarget(in.Path)
		if err != nil {
			return nil, err
		}
		return e.state(), nil
	})
	tool(s, a, "slides_state", "The deck: its size, layouts, and each slide's layout, notes and shapes with their ids, boxes and words.", func(in pathIn) (any, error) {
		e, err := a.slidesTarget(in.Path)
		if err != nil {
			return nil, err
		}
		e.endText()
		return e.state(), nil
	})
	type addSlideIn struct {
		Path   string `json:"path,omitempty"`
		Layout string `json:"layout,omitempty" jsonschema:"the layout's name, as slides_state lists; Title and Content when left out"`
		At     int    `json:"at,omitempty" jsonschema:"the number the new slide gets; the end when left out"`
	}
	tool(s, a, "slides_add_slide", "Add a slide on a layout, its placeholders empty, and return it.", func(in addSlideIn) (any, error) {
		e, err := a.slidesTarget(in.Path)
		if err != nil {
			return nil, err
		}
		e.endText()
		l, err := e.layoutNamed(in.Layout)
		if err != nil {
			return nil, err
		}
		at := len(e.d.Slides)
		if in.At > 0 {
			at = in.At - 1
		}
		sl, err := e.d.AddSlide(l, at)
		if err != nil {
			return nil, err
		}
		e.goTo(e.d.Number(sl) - 1)
		return e.state().Slides[e.cur], nil
	})
	type slideIn struct {
		Path  string `json:"path,omitempty"`
		Slide int    `json:"slide" jsonschema:"the slide's number, from 1"`
	}
	tool(s, a, "slides_delete_slide", "Delete a slide.", func(in slideIn) (any, error) {
		e, sl, err := a.slidesAt(in.Path, in.Slide)
		if err != nil {
			return nil, err
		}
		e.deleteSlide(sl)
		return "deleted", nil
	})
	type moveIn struct {
		Path string `json:"path,omitempty"`
		From int    `json:"from" jsonschema:"the slide's number now"`
		To   int    `json:"to" jsonschema:"the number it gets"`
	}
	tool(s, a, "slides_move_slide", "Move a slide to another place.", func(in moveIn) (any, error) {
		e, err := a.slidesTarget(in.Path)
		if err != nil {
			return nil, err
		}
		e.endText()
		if err := e.d.MoveSlide(in.From-1, in.To-1); err != nil {
			return nil, err
		}
		e.goTo(in.To - 1)
		return "moved", nil
	})
	type addShapeIn struct {
		Path   string    `json:"path,omitempty"`
		Slide  int       `json:"slide,omitempty" jsonschema:"the slide's number; the one shown when left out"`
		Shape  string    `json:"shape" jsonschema:"textbox, or a preset name: rect, roundRect, ellipse, triangle, rightArrow, chevron, line and so on"`
		Box    []float64 `json:"box" jsonschema:"x, y, width and height in points"`
		Text   string    `json:"text,omitempty"`
		Fill   string    `json:"fill,omitempty" jsonschema:"#rrggbb, or none"`
		Line   string    `json:"line,omitempty" jsonschema:"#rrggbb, or none"`
		Width  float64   `json:"line_width,omitempty" jsonschema:"in points"`
		Size   float64   `json:"size,omitempty" jsonschema:"the words' size in points"`
		Colour string    `json:"color,omitempty" jsonschema:"the words' colour, #rrggbb"`
	}
	tool(s, a, "slides_add_shape", "Add a text box or a shape, with its words and look, and return its id.", func(in addShapeIn) (any, error) {
		e, sl, err := a.slidesAt(in.Path, in.Slide)
		if err != nil {
			return nil, err
		}
		b, err := e.boxOf(in.Box, pptx.Box{X: 100, Y: 100, W: 200, H: 100})
		if err != nil {
			return nil, err
		}
		id, err := e.d.AddShape(sl, in.Shape, b)
		if err != nil {
			return nil, err
		}
		if err := e.applyShape(sl, id, shapeChange{text: in.Text, setText: in.Text != "", fill: in.Fill, line: in.Line, width: in.Width, size: in.Size, colour: in.Colour, opacity: -1}); err != nil {
			return nil, err
		}
		return map[string]int{"id": id}, nil
	})
	type textIn struct {
		Path  string `json:"path,omitempty"`
		Slide int    `json:"slide,omitempty" jsonschema:"the slide's number; the one shown when left out"`
		ID    int    `json:"id" jsonschema:"the shape's id, from slides_state"`
		Text  string `json:"text" jsonschema:"the words; a newline starts a paragraph"`
		Row   int    `json:"row,omitempty" jsonschema:"in a table, the cell's row, from 1"`
		Col   int    `json:"col,omitempty" jsonschema:"in a table, the cell's column, from 1"`
	}
	tool(s, a, "slides_set_text", "Replace the words of a shape, a placeholder or a table's cell, keeping the look of its first words.", func(in textIn) (any, error) {
		e, sl, err := a.slidesAt(in.Path, in.Slide)
		if err != nil {
			return nil, err
		}
		e.goTo(e.d.Number(sl) - 1)
		t := pptx.Target{ID: in.ID}
		if in.Row > 0 || in.Col > 0 {
			t = pptx.Target{ID: in.ID, Cell: true, Row: in.Row - 1, Col: in.Col - 1}
		}
		if k := e.d.Find(sl, in.ID); k == nil {
			return nil, fmt.Errorf("slide %d has no shape %d", e.cur+1, in.ID)
		} else if !t.Cell && k.Kind == pptx.KindShape {
			e.ensureBody(k)
		}
		if err := e.setWords(t, in.Text); err != nil {
			return nil, err
		}
		return "set", nil
	})
	type updateIn struct {
		Path     string    `json:"path,omitempty"`
		Slide    int       `json:"slide,omitempty" jsonschema:"the slide's number; the one shown when left out"`
		ID       int       `json:"id"`
		Box      []float64 `json:"box,omitempty" jsonschema:"x, y, width and height in points"`
		Rotation *float64  `json:"rotation,omitempty" jsonschema:"degrees clockwise"`
		Fill     string    `json:"fill,omitempty" jsonschema:"#rrggbb, or none"`
		Line     string    `json:"line,omitempty" jsonschema:"#rrggbb, or none"`
		Width    float64   `json:"line_width,omitempty" jsonschema:"in points"`
		Opacity  *float64  `json:"opacity,omitempty" jsonschema:"from 0 to 1"`
		Size     float64   `json:"size,omitempty" jsonschema:"the words' size in points"`
		Colour   string    `json:"color,omitempty" jsonschema:"the words' colour, #rrggbb"`
		Bold     *bool     `json:"bold,omitempty"`
		Align    string    `json:"align,omitempty" jsonschema:"left, center or right"`
		Bullets  string    `json:"bullets,omitempty" jsonschema:"none, bullet or number"`
		Order    string    `json:"order,omitempty" jsonschema:"front, back, forward or backward"`
		Delete   bool      `json:"delete,omitempty" jsonschema:"remove the shape"`
	}
	tool(s, a, "slides_update_shape", "Change a shape: where it is, how it looks, its words' look, its place in the stack, or remove it.", func(in updateIn) (any, error) {
		e, sl, err := a.slidesAt(in.Path, in.Slide)
		if err != nil {
			return nil, err
		}
		k := e.d.Find(sl, in.ID)
		if k == nil {
			return nil, fmt.Errorf("the slide has no shape %d", in.ID)
		}
		if in.Delete {
			return "deleted", e.d.DeleteShapes(sl, []int{in.ID})
		}
		box := k.Box
		if len(in.Box) > 0 {
			b, err := e.boxOf(in.Box, box)
			if err != nil {
				return nil, err
			}
			box.X, box.Y, box.W, box.H = b.X, b.Y, b.W, b.H
		}
		if in.Rotation != nil {
			box.Rot = *in.Rotation
		}
		if box != k.Box {
			if err := e.d.SetBox(sl, "", []int{in.ID}, []pptx.Box{box}); err != nil {
				return nil, err
			}
		}
		ch := shapeChange{fill: in.Fill, line: in.Line, width: in.Width, size: in.Size, colour: in.Colour, bold: in.Bold, opacity: -1}
		ch.align, ch.bullets = in.Align, in.Bullets
		if in.Opacity != nil {
			ch.opacity = *in.Opacity
		}
		if err := e.applyShape(sl, in.ID, ch); err != nil {
			return nil, err
		}
		switch in.Order {
		case "front":
			err = e.d.Reorder(sl, []int{in.ID}, pptx.ToFront)
		case "back":
			err = e.d.Reorder(sl, []int{in.ID}, pptx.ToBack)
		case "forward":
			err = e.d.Reorder(sl, []int{in.ID}, pptx.Forward)
		case "backward":
			err = e.d.Reorder(sl, []int{in.ID}, pptx.Backward)
		case "":
		default:
			err = fmt.Errorf("order %q is not front, back, forward or backward", in.Order)
		}
		return "updated", err
	})
	type pictureIn struct {
		Path  string    `json:"path,omitempty"`
		Slide int       `json:"slide,omitempty" jsonschema:"the slide's number; the one shown when left out"`
		File  string    `json:"file" jsonschema:"absolute path of the picture"`
		Box   []float64 `json:"box,omitempty" jsonschema:"x, y, width and height in points; its own size, centred and fitted to the slide, when left out"`
	}
	tool(s, a, "slides_add_picture", "Add a picture from a file and return its id.", func(in pictureIn) (any, error) {
		e, sl, err := a.slidesAt(in.Path, in.Slide)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(in.File)
		if err != nil {
			return nil, err
		}
		b, err := e.boxOf(in.Box, pptx.Box{})
		if err != nil {
			return nil, err
		}
		id, err := e.d.AddPicture(sl, data, b, "")
		if err != nil {
			return nil, err
		}
		return map[string]int{"id": id}, nil
	})
	type tableIn struct {
		Path  string     `json:"path,omitempty"`
		Slide int        `json:"slide,omitempty" jsonschema:"the slide's number; the one shown when left out"`
		Rows  int        `json:"rows,omitempty"`
		Cols  int        `json:"cols,omitempty"`
		Cells [][]string `json:"cells,omitempty" jsonschema:"the cells' words by row; rows and cols come from it when left out"`
		Box   []float64  `json:"box,omitempty" jsonschema:"x, y, width and height in points"`
	}
	tool(s, a, "slides_add_table", "Add a table in the deck's default table style and return its id.", func(in tableIn) (any, error) {
		e, sl, err := a.slidesAt(in.Path, in.Slide)
		if err != nil {
			return nil, err
		}
		if in.Rows == 0 {
			in.Rows = len(in.Cells)
		}
		if in.Cols == 0 {
			for _, r := range in.Cells {
				in.Cols = max(in.Cols, len(r))
			}
		}
		b, err := e.boxOf(in.Box, pptx.Box{X: e.d.W * 0.1, Y: e.d.H * 0.25, W: e.d.W * 0.8})
		if err != nil {
			return nil, err
		}
		id, err := e.d.AddTable(sl, in.Rows, in.Cols, b, in.Cells)
		if err != nil {
			return nil, err
		}
		return map[string]int{"id": id}, nil
	})
	type notesIn struct {
		Path  string  `json:"path,omitempty"`
		Slide int     `json:"slide,omitempty" jsonschema:"the slide's number; the one shown when left out"`
		Text  *string `json:"text,omitempty" jsonschema:"the new notes; left out, the tool only reads them"`
	}
	tool(s, a, "slides_notes", "Read a slide's speaker notes, or replace them.", func(in notesIn) (any, error) {
		e, sl, err := a.slidesAt(in.Path, in.Slide)
		if err != nil {
			return nil, err
		}
		if in.Text != nil {
			if err := e.d.SetNotes(sl, *in.Text, ""); err != nil {
				return nil, err
			}
			e.notesFor = ""
		}
		return map[string]string{"notes": e.d.Notes(sl)}, nil
	})
	type renderIn struct {
		Path  string `json:"path,omitempty"`
		Slide int    `json:"slide,omitempty" jsonschema:"the slide's number; the one shown when left out"`
		Width int    `json:"width,omitempty" jsonschema:"in pixels; 1280 when left out"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "slides_render", Description: "A PNG of a slide as the slideshow shows it, to check it."},
		func(ctx contextT, req *mcp.CallToolRequest, in renderIn) (*mcp.CallToolResult, any, error) {
			return imageResult(call(a, func() ([]byte, error) {
				e, sl, err := a.slidesAt(in.Path, in.Slide)
				if err != nil {
					return nil, err
				}
				return e.slidesRenderPNG(sl, in.Width)
			}))
		})
	tool(s, a, "slides_save", "Save the deck now.", func(in pathIn) (any, error) {
		e, err := a.slidesTarget(in.Path)
		if err != nil {
			return nil, err
		}
		e.endText()
		od := e.od
		if e.version() == od.saved {
			return map[string]string{"path": od.path}, nil
		}
		od.dirty = true
		deadline := time.Now().Add(time.Minute)
		for od.saving && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
			a.drainJobs()
		}
		if err := a.saveOffice(od, true); err != nil {
			return nil, err
		}
		return map[string]string{"path": od.path}, nil
	})
}

// shapeChange is the look a tool sets on a shape; empty fields leave things as they are, and an opacity below zero too.
type shapeChange struct {
	text    string
	setText bool
	fill    string
	line    string
	width   float64
	size    float64
	colour  string
	bold    *bool
	align   string
	bullets string
	opacity float64
}

// applyShape applies a change to a shape.
func (e *slidesEditor) applyShape(s *pptx.Slide, id int, c shapeChange) error {
	ids := []int{id}
	if c.fill != "" {
		col, err := parseSlideColor(c.fill)
		if err != nil {
			return err
		}
		if err := e.d.SetFill(s, ids, col); err != nil {
			return err
		}
	}
	if c.line != "" || c.width > 0 {
		var col color.RGBA
		var err error
		if c.line != "" {
			if col, err = parseSlideColor(c.line); err != nil {
				return err
			}
		}
		width := -1.0
		if c.width > 0 {
			width = c.width
		}
		if err := e.d.SetLine(s, ids, col, c.line != "", width); err != nil {
			return err
		}
	}
	if c.opacity >= 0 {
		if err := e.d.SetOpacity(s, ids, c.opacity); err != nil {
			return err
		}
	}
	return e.applyWords(s, id, c)
}

// applyWords applies the part of a change that is about the shape's words.
func (e *slidesEditor) applyWords(s *pptx.Slide, id int, c shapeChange) error {
	if c.setText {
		if err := e.goToAnd(s, func() error { return e.setWords(pptx.Target{ID: id}, c.text) }); err != nil {
			return err
		}
	}
	if c.size == 0 && c.colour == "" && c.bold == nil && c.align == "" && c.bullets == "" {
		return nil
	}
	var ink color.RGBA
	if c.colour != "" {
		col, err := parseSlideColor(c.colour)
		if err != nil {
			return err
		}
		ink = col
	}
	align := ""
	switch c.align {
	case "left":
		align = "l"
	case "center", "centre":
		align = "ctr"
	case "right":
		align = "r"
	case "":
	default:
		return fmt.Errorf("align %q is not left, center or right", c.align)
	}
	if c.bullets != "" && c.bullets != "none" && c.bullets != "bullet" && c.bullets != "number" {
		return fmt.Errorf("bullets %q is not none, bullet or number", c.bullets)
	}
	return e.d.Edit("", []*pptx.Slide{s}, false, func() error {
		body, k := e.d.Body(s, pptx.Target{ID: id})
		if body == nil {
			return errSlidesNoWords
		}
		a, b := pptx.Whole(body)
		if c.size > 0 {
			pptx.EachRun(body, a, b, pptx.SetSize(c.size))
		}
		if c.colour != "" {
			pptx.EachRun(body, a, b, pptx.SetColor(ink))
		}
		if c.bold != nil {
			pptx.EachRun(body, a, b, pptx.SetBool("b", *c.bold))
		}
		if align != "" {
			pptx.EachPara(body, 0, 1<<30, pptx.SetAlign(align))
		}
		if c.bullets != "" {
			pptx.EachPara(body, 0, 1<<30, pptx.SetBullet(c.bullets))
		}
		e.r.FitText(s, k)
		return nil
	})
}

// goToAnd does something that acts on the slide shown, with s shown.
func (e *slidesEditor) goToAnd(s *pptx.Slide, fn func() error) error {
	e.goTo(e.d.Number(s) - 1)
	return fn()
}
