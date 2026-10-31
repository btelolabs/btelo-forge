package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// AcroForm fields: values go where the field keeps them, and each widget gets an appearance for that value, so that
// readers which never regenerate appearances show it too.

// FieldKind is a field's kind as the editor sees it.
type FieldKind int

const (
	FieldText FieldKind = iota
	FieldCheck
	FieldRadio
	FieldChoice
	FieldPush
	FieldSignature
)

// Field is a terminal field.
type Field struct {
	Ref      Ref    // the field object holding the value
	Name     string // the full dotted name
	Kind     FieldKind
	Value    string
	Multi    bool
	Password bool
	Combo    bool
	ReadOnly bool
	Options  []FieldOption
	Widgets  []Widget
}

// FieldOption is one choice of a choice field.
type FieldOption struct{ Export, Label string }

// Widget is where a field shows on a page.
type Widget struct {
	Ref   Ref
	Page  int
	Rect  Rect
	State Name // a check box or radio button's on state
}

func (d *Doc) acroForm() Dict { return d.Dict(d.Catalog()["AcroForm"]) }

// Signed reports whether the document carries a digital signature.
func (d *Doc) Signed() bool {
	for _, f := range d.Fields() {
		if f.Kind == FieldSignature && f.Value != "" {
			return true
		}
	}
	return false
}

// Fields lists the terminal fields of the form.
func (d *Doc) Fields() []Field {
	form := d.acroForm()
	if form == nil {
		return nil
	}
	// Which page each widget is on: /P is optional.
	pageOf := map[int]int{}
	for i, p := range d.Pages() {
		for _, a := range d.Array(p.Dict["Annots"]) {
			if r, ok := a.(Ref); ok {
				pageOf[r.Num] = i
			}
		}
	}
	var out []Field
	seen := map[int]bool{}
	for _, f := range d.Array(form["Fields"]) {
		d.walkField(f, "", Dict{}, seen, 0, pageOf, &out)
	}
	return out
}

var fieldInherited = []Name{"FT", "Ff", "V", "DA", "Q", "Opt", "MaxLen"}

func (d *Doc) walkField(o Object, prefix string, inh Dict, seen map[int]bool, depth int, pageOf map[int]int, out *[]Field) {
	r, ok := o.(Ref)
	if !ok || seen[r.Num] || depth > 32 || len(*out) > 100000 {
		return
	}
	seen[r.Num] = true
	dict := d.Dict(o)
	if dict == nil {
		return
	}
	name := prefix
	if t, ok := d.Resolve(dict["T"]).(String); ok {
		if name != "" {
			name += "."
		}
		name += TextString(t)
	}
	next := inh.Clone()
	for _, k := range fieldInherited {
		if v, ok := dict[k]; ok {
			next[k] = v
		}
	}
	kids := d.Array(dict["Kids"])
	// Kids with their own names are fields; without, they are this field's widgets.
	var widgets []Ref
	for _, k := range kids {
		kd := d.Dict(k)
		if kd == nil {
			continue
		}
		if _, named := kd["T"]; named || d.Array(kd["Kids"]) != nil {
			d.walkField(k, name, next, seen, depth+1, pageOf, out)
		} else if kr, ok := k.(Ref); ok {
			widgets = append(widgets, kr)
		}
	}
	if len(kids) > 0 && len(widgets) == 0 {
		return
	}
	if len(kids) == 0 {
		widgets = []Ref{r}
	}
	f := Field{Ref: r, Name: name}
	ff := d.Int(next["Ff"], 0)
	f.ReadOnly = ff&1 != 0
	switch d.Name(next["FT"]) {
	case "Tx":
		f.Kind = FieldText
		f.Multi = ff&(1<<12) != 0
		f.Password = ff&(1<<13) != 0
	case "Btn":
		switch {
		case ff&(1<<16) != 0:
			f.Kind = FieldPush
		case ff&(1<<15) != 0:
			f.Kind = FieldRadio
		default:
			f.Kind = FieldCheck
		}
	case "Ch":
		f.Kind = FieldChoice
		f.Combo = ff&(1<<17) != 0
		for _, e := range d.Array(next["Opt"]) {
			switch v := d.Resolve(e).(type) {
			case String:
				s := TextString(v)
				f.Options = append(f.Options, FieldOption{s, s})
			case Array:
				if len(v) >= 2 {
					a, _ := d.Resolve(v[0]).(String)
					b, _ := d.Resolve(v[1]).(String)
					f.Options = append(f.Options, FieldOption{TextString(a), TextString(b)})
				}
			}
		}
	case "Sig":
		f.Kind = FieldSignature
	default:
		return
	}
	switch v := d.Resolve(next["V"]).(type) {
	case String:
		f.Value = TextString(v)
	case Name:
		f.Value = string(v)
	case Dict:
		if f.Kind == FieldSignature {
			f.Value = "signed"
		}
	case Array:
		if len(v) > 0 {
			if s, ok := d.Resolve(v[0]).(String); ok {
				f.Value = TextString(s)
			}
		}
	}
	for _, w := range widgets {
		wd := d.Dict(w)
		rc, ok := d.RectOf(wd["Rect"])
		if !ok {
			continue
		}
		page, on := pageOf[w.Num]
		if !on {
			continue
		}
		wg := Widget{Ref: w, Page: page, Rect: rc}
		for state := range d.Dict(d.Dict(wd["AP"])["N"]) {
			if state != "Off" {
				wg.State = state
			}
		}
		f.Widgets = append(f.Widgets, wg)
	}
	*out = append(*out, f)
}

// SetField gives a field a value: the text, the chosen export value, or for a check box or radio button a widget's on
// state ("" or "Off" clears it).
func (d *Doc) SetField(f *Field, value string) error {
	if f.ReadOnly {
		return errors.New("pdf: the field is read only")
	}
	return d.Do(func() error {
		dict := d.Dict(f.Ref)
		if dict == nil {
			return errors.New("pdf: the field is gone")
		}
		c := dict.Clone()
		switch f.Kind {
		case FieldText:
			c["V"] = EncodeText(value)
		case FieldChoice:
			c["V"] = EncodeText(value)
			delete(c, "I")
		case FieldCheck, FieldRadio:
			if value == "" {
				value = "Off"
			}
			c["V"] = Name(value)
		default:
			return errors.New("pdf: the field takes no value")
		}
		d.SetRef(f.Ref, c)
		for _, w := range f.Widgets {
			wd := d.Dict(w.Ref).Clone()
			if f.Kind == FieldCheck || f.Kind == FieldRadio {
				state := Name("Off")
				if w.State == Name(value) {
					state = w.State
				}
				wd["AS"] = state
				d.SetRef(w.Ref, wd)
				continue
			}
			if v, ok := c["V"]; ok && w.Ref == f.Ref {
				wd["V"] = v
			}
			parent := fieldValues(d, wd, c, f)
			s, err := d.widgetFormFor(parent, wd, w.Rect, true)
			if err != nil {
				return err
			}
			if s != nil {
				ap := Dict{}
				if old := d.Dict(wd["AP"]); old != nil {
					ap = old.Clone()
				}
				ap["N"] = d.Add(s)
				wd["AP"] = ap
			}
			d.SetRef(w.Ref, wd)
		}
		return nil
	})
}

// fieldValues merges what a widget inherits, for drawing it.
func fieldValues(d *Doc, wd, field Dict, f *Field) Dict {
	out := Dict{}
	cur := wd
	for depth := 0; cur != nil && depth < 32; depth++ {
		for _, k := range append(fieldInherited, "MK") {
			if _, have := out[k]; !have {
				if v, ok := cur[k]; ok {
					out[k] = v
				}
			}
		}
		cur = d.Dict(cur["Parent"])
	}
	if v, ok := field["V"]; ok {
		out["V"] = v
	}
	return out
}

func (d *Doc) widgetForm(dict Dict, rect Rect, store bool) (*Stream, error) {
	return d.widgetFormFor(fieldValues(d, dict, dict, nil), dict, rect, store)
}

// widgetFormFor draws a text or choice widget.
func (d *Doc) widgetFormFor(vals, wd Dict, rect Rect, store bool) (*Stream, error) {
	ft := d.Name(vals["FT"])
	if ft != "Tx" && ft != "Ch" {
		return nil, nil
	}
	value := ""
	switch v := d.Resolve(vals["V"]).(type) {
	case String:
		value = TextString(v)
	case Array:
		if len(v) > 0 {
			if s, ok := d.Resolve(v[0]).(String); ok {
				value = TextString(s)
			}
		}
	}
	if ft == "Ch" {
		// Show the option's label.
		for _, e := range d.Array(vals["Opt"]) {
			if a := d.Array(e); len(a) >= 2 {
				x, _ := d.Resolve(a[0]).(String)
				l, _ := d.Resolve(a[1]).(String)
				if TextString(x) == value {
					value = TextString(l)
					break
				}
			}
		}
	}
	ff := d.Int(vals["Ff"], 0)
	if ff&(1<<13) != 0 { // password
		value = strings.Repeat("*", len([]rune(value)))
	}
	form := d.acroForm()
	da, _ := d.Resolve(vals["DA"]).(String)
	if da == "" {
		da, _ = d.Resolve(form["DA"]).(String)
	}
	name, size, ops := parseDA(string(da))
	if ops == "" {
		ops = "0 g"
	}
	var fontObj Object
	if name != "" {
		fontObj = d.Dict(d.Dict(form["DR"])["Font"])[name]
		if fontObj == nil {
			fontObj = d.Dict(d.Dict(wd["DR"])["Font"])[name]
		}
	}
	if name == "" {
		name = "Helv"
	}
	run, err := d.textRunFor(name, fontObj, value, store)
	if err != nil || run == nil {
		return nil, err
	}
	w, h := rect.W(), rect.H()
	bbox := Rect{0, 0, w, h}
	var b bytes.Buffer
	mk := d.Dict(wd["MK"])
	if bg := d.Floats(mk["BG"]); len(bg) == 3 {
		fmt.Fprintf(&b, "%s rg 0 0 %s %s re f\n", rgbOps([3]float64{bg[0], bg[1], bg[2]}), fmtNum(w), fmtNum(h))
	}
	if bc := d.Floats(mk["BC"]); len(bc) == 3 {
		fmt.Fprintf(&b, "%s RG 1 w 0.5 0.5 %s %s re S\n", rgbOps([3]float64{bc[0], bc[1], bc[2]}), fmtNum(w-1), fmtNum(h-1))
	}
	multi := ft == "Tx" && ff&(1<<12) != 0
	pad := 2.0
	if size <= 0 {
		// Automatic: as large as fits.
		size = 12
		if !multi {
			size = min(max((h-2*pad)*0.8, 4), 24)
			if wd := run.width(value); wd > 0 {
				size = min(size, (w-2*pad)/wd)
			}
		}
	}
	fmt.Fprintf(&b, "/Tx BMC q 1 1 %s %s re W n BT /%s %s Tf %s\n", fmtNum(w-2), fmtNum(h-2), run.name, fmtNum(size), ops)
	lines := []string{value}
	y := (h - size*(run.asc+run.desc)) / 2
	if multi {
		lines = wrapText(value, (w-2*pad)/size, run.width)
		y = h - pad - size
	}
	q := d.Int(vals["Q"], 0)
	lastX, lastY := 0.0, 0.0
	for _, l := range lines {
		x := pad
		switch q {
		case 1:
			x = (w - run.width(l)*size) / 2
		case 2:
			x = w - pad - run.width(l)*size
		}
		fmt.Fprintf(&b, "%s %s Td %s Tj\n", fmtNum(x-lastX), fmtNum(y-lastY), run.enc(l))
		lastX, lastY = x, y
		y -= size * 1.2
	}
	b.WriteString("ET Q EMC\n")
	s := formStream(bbox, run.res, b.Bytes())
	return s, nil
}

func formStream(bbox Rect, res Dict, ops []byte) *Stream {
	s := form(bbox, res, ops, false)
	delete(s.Dict, "ForgeDrawn")
	return s
}
