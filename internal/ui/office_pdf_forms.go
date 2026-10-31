package ui

import (
	"image"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// Forms on the page, and signing with a picture.

// pdfActiveField is a text field being typed in.
type pdfActiveField struct {
	field  pdf.Field
	widget pdf.Widget
	value  string
}

func (e *pdfEditor) formFields() []pdf.Field {
	if v := e.doc.Version(); e.fields == nil || v != e.fieldsSeen {
		e.fields, e.fieldsSeen = e.doc.Fields(), v
		if e.fields == nil {
			e.fields = []pdf.Field{}
		}
	}
	return e.fields
}

func (e *pdfEditor) fieldAt(page int, x, y float64) (*pdf.Field, *pdf.Widget) {
	fields := e.formFields()
	for i := range fields {
		f := &fields[i]
		if f.ReadOnly || f.Kind == pdf.FieldPush || f.Kind == pdf.FieldSignature {
			continue
		}
		for k := range f.Widgets {
			w := &f.Widgets[k]
			if w.Page == page && w.Rect.Contains(x, y) {
				return f, w
			}
		}
	}
	return nil, nil
}

func (e *pdfEditor) pressField(f *pdf.Field, w *pdf.Widget) {
	a := e.a
	u := &a.u
	switch f.Kind {
	case pdf.FieldText:
		e.active = &pdfActiveField{field: *f, widget: *w, value: f.Value}
		u.editOwner = ""
		u.setFocus("office/pdf/field")
	case pdf.FieldCheck:
		value := string(w.State)
		if f.Value == value {
			value = ""
		}
		e.edited(e.doc.SetField(f, value), w.Page)
		e.rev++
	case pdf.FieldRadio:
		e.edited(e.doc.SetField(f, string(w.State)), w.Page)
		e.rev++
	case pdf.FieldChoice:
		field := *f
		page := w.Page
		var items []menuItem
		for _, o := range f.Options {
			items = append(items, menuItem{label: o.Label, checked: o.Export == f.Value, run: func() {
				e.edited(e.doc.SetField(&field, o.Export), page)
			}})
		}
		a.openMenu("office/pdf/choice", e.screenBox(w.Page, w.Rect), items)
	}
}

// drawFields tints the fields that can be filled in.
func (e *pdfEditor) drawFields(page int) {
	if e.tool != pdfToolSelect {
		return
	}
	u := &e.a.u
	for _, f := range e.formFields() {
		if f.ReadOnly || f.Kind == pdf.FieldPush || f.Kind == pdf.FieldSignature {
			continue
		}
		for _, w := range f.Widgets {
			if w.Page == page {
				box := e.screenBox(page, w.Rect)
				u.c.Fill(box, gfx.Alpha(colAccent, 0.1))
				if u.hover("office/pdf/field-hover", box) {
					u.cursor = CursorHand
				}
			}
		}
	}
}

func (e *pdfEditor) drawActiveField() {
	u := &e.a.u
	af := e.active
	if af.widget.Page >= len(e.pages) {
		e.active = nil
		return
	}
	box := e.screenBox(af.widget.Page, af.widget.Rect)
	box.W, box.H = max(box.W, u.px(120)), max(box.H, u.px(26))
	id := "office/pdf/field"
	e.oversNext = append(e.oversNext, box.Inset(-u.px(2)))
	u.c.Fill(box.Inset(-u.px(2)), colPanel)
	_, done := u.textBox(id, box, &af.value, "", af.field.Multi)
	if u.focus == id {
		for _, k := range u.keys {
			switch {
			case k.Code == keyEscape:
				e.active = nil
				u.focus = "office/pdf"
				return
			case k.Code == keyEnter && !af.field.Multi, k.Code == keyTab:
				e.commitField()
				return
			}
		}
	}
	if done && !af.field.Multi {
		e.commitField()
	}
}

func (e *pdfEditor) commitField() {
	af := e.active
	if af == nil {
		return
	}
	e.active = nil
	if e.a.u.focus == "office/pdf/field" {
		e.a.u.focus = "office/pdf"
	}
	if af.value != af.field.Value {
		e.edited(e.doc.SetField(&af.field, af.value), af.widget.Page)
	}
}

// pdfSignPad is the panel where a signature is drawn or typed.
type pdfSignPad struct {
	strokes [][][2]float32 // in the pad's own pixels
	typed   string
	tab     int
}

func (e *pdfEditor) openSignPad() { e.sign = &pdfSignPad{} }

func (e *pdfEditor) drawSignPad() {
	a := e.a
	u := &a.u
	s := e.sign
	w := min(u.px(460), e.area.W-u.px(20))
	r := gfx.Rect{X: e.area.X + (e.area.W-w)/2, Y: e.area.Y + u.px(30), W: w, H: u.px(290)}
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y + u.px(3), W: r.W, H: r.H}, gfx.Alpha(colBlack, 0.4))
	u.c.Fill(r, colPanel)
	u.c.Outline(r, max(1, u.px(1)), colBorderHi)
	in := r.Inset(u.px(14))
	u.label(u.bold(14), gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(22)}, u.t("Sign"), colText, 0)
	y := in.Y + u.px(26)
	f := u.face(11.5)
	for _, l := range f.Wrap(u.t("This places a picture of your signature on the page. It is not a cryptographic signature and does not prove who signed."), in.W) {
		u.label(f, gfx.Rect{X: in.X, Y: y, W: in.W, H: u.px(17)}, l, colDim, 0)
		y += u.px(17)
	}
	y += u.px(6)
	s.tab = u.chips("office/pdf/sign/tab", gfx.Rect{X: in.X, Y: y, W: u.px(180), H: u.px(26)}, []string{u.t("Draw"), u.t("Type")}, s.tab, true)
	y += u.px(32)
	pad := gfx.Rect{X: in.X, Y: y, W: in.W, H: in.Bottom() - y - u.px(38)}
	if s.tab == 0 {
		u.c.Fill(pad, colWhite)
		id := "office/pdf/sign/pad"
		if u.press(id, pad) {
			s.strokes = append(s.strokes, [][2]float32{{u.mx - pad.X, u.my - pad.Y}})
		}
		if u.dragging(id) {
			n := len(s.strokes) - 1
			s.strokes[n] = append(s.strokes[n], [2]float32{u.mx - pad.X, u.my - pad.Y})
		}
		u.letGo(id)
		u.c.Push(pad)
		ink := gfx.Hex(0x14306e)
		for _, st := range s.strokes {
			for k := 1; k < len(st); k++ {
				u.c.Line(pad.X+st[k-1][0], pad.Y+st[k-1][1], pad.X+st[k][0], pad.Y+st[k][1], u.px(2), ink)
			}
		}
		u.c.Pop()
	} else {
		u.textBox("office/pdf/sign/name", gfx.Rect{X: pad.X, Y: pad.Y, W: pad.W, H: u.px(34)}, &s.typed, u.t("Type your name"), false)
	}
	by := in.Bottom() - u.px(30)
	bw := u.px(84)
	ready := len(s.strokes) > 0 || s.tab == 1 && strings.TrimSpace(s.typed) != ""
	if u.button("office/pdf/sign/ok", gfx.Rect{X: in.Right() - 2*bw - u.px(6), Y: by, W: bw, H: u.px(30)}, "", u.t("Place"), primary, ready) {
		e.placeSignature(pad)
		return
	}
	if u.button("office/pdf/sign/no", gfx.Rect{X: in.Right() - bw, Y: by, W: bw, H: u.px(30)}, "", u.t("Cancel"), plain, true) {
		e.sign = nil
	}
	if s.tab == 0 && u.button("office/pdf/sign/clear", gfx.Rect{X: in.X, Y: by, W: bw, H: u.px(30)}, "", u.t("Clear"), plain, len(s.strokes) > 0) {
		s.strokes = nil
	}
}

// placeSignature turns the pad into a picture or text to be placed.
func (e *pdfEditor) placeSignature(pad gfx.Rect) {
	s := e.sign
	e.sign = nil
	page := e.currentPage()
	box := e.doc.CropBox(e.pages[page])
	if s.tab == 1 {
		st := pdf.TextStyle{Family: "georgia", Italic: true, Size: 22, Color: [3]float64{0.08, 0.19, 0.43}}
		cx, cy := (box.X0+box.X1)/2, (box.Y0+box.Y1)/2
		e.pending = &pdfPending{kind: pdfPendSignText, page: page, text: s.typed, style: st, rect: pdf.Rect{X0: cx - 110, Y0: cy - 18, X1: cx + 110, Y1: cy + 22}}
		e.focusPending()
		return
	}
	e.placePicture(signatureImage(s.strokes, pad, e.a.u.px(2)), nil)
}

// signatureImage draws strokes at twice their size on a clear ground, cropped to what was drawn.
func signatureImage(strokes [][][2]float32, pad gfx.Rect, width float32) image.Image {
	const k = 2
	minX, minY, maxX, maxY := pad.W, pad.H, float32(0), float32(0)
	for _, st := range strokes {
		for _, p := range st {
			minX, maxX = min(minX, p[0]), max(maxX, p[0])
			minY, maxY = min(minY, p[1]), max(maxY, p[1])
		}
	}
	m := width * 2
	w, h := int((maxX-minX+2*m)*k)+1, int((maxY-minY+2*m)*k)+1
	c := gfx.NewCanvas(max(w, 2), max(h, 2))
	ink := gfx.Hex(0x14306e)
	for _, st := range strokes {
		for i := 1; i < len(st); i++ {
			a, b := st[i-1], st[i]
			c.Line((a[0]-minX+m)*k, (a[1]-minY+m)*k, (b[0]-minX+m)*k, (b[1]-minY+m)*k, width*k, ink)
		}
	}
	return c.Img
}
