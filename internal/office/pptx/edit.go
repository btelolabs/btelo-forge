package pptx

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Schema order of the elements edits add children to.
var (
	presOrder = []string{"p:sldMasterIdLst", "p:notesMasterIdLst", "p:handoutMasterIdLst", "p:sldIdLst", "p:sldSz", "p:notesSz", "p:smartTags", "p:embeddedFontLst",
		"p:custShowLst", "p:photoAlbum", "p:custDataLst", "p:kinsoku", "p:defaultTextStyle", "p:modifyVerifier", "p:extLst"}
	spPrOrder = []string{"a:xfrm", "a:custGeom", "a:prstGeom", "a:noFill", "a:solidFill", "a:gradFill", "a:blipFill", "a:pattFill", "a:grpFill", "a:ln", "a:effectLst", "a:effectDag",
		"a:scene3d", "a:sp3d", "a:extLst"}
	lnOrder   = []string{"a:noFill", "a:solidFill", "a:gradFill", "a:pattFill", "a:prstDash", "a:custDash", "a:round", "a:bevel", "a:miter", "a:headEnd", "a:tailEnd", "a:extLst"}
	csldOrder = []string{"p:bg", "p:spTree", "p:custDataLst", "p:controls", "p:extLst"}
	blipOrder = []string{"a:alphaBiLevel", "a:alphaCeiling", "a:alphaFloor", "a:alphaInv", "a:alphaMod", "a:alphaModFix", "a:alphaRepl", "a:biLevel", "a:blur", "a:clrChange", "a:clrRepl",
		"a:duotone", "a:fillOverlay", "a:grayscl", "a:hsl", "a:lum", "a:tint", "a:extLst"}
)

func newElement(xml string) *ox.Node {
	d, err := ox.Parse([]byte(xml))
	if err != nil {
		panic(err) // the markup is this package's own
	}
	return d.Root
}

func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func emu(pt float64) int64 { return int64(math.Round(pt * emuPerPt)) }

// nextShapeID is an id no shape on the slide has.
func nextShapeID(s *Slide) int {
	high := 1
	s.tree().Root.Walk(func(n *ox.Node) bool {
		if n.Name == "p:cNvPr" {
			if v, err := strconv.Atoi(n.Attr("id")); err == nil {
				high = max(high, v)
			}
		}
		return true
	})
	return high + 1
}

// addRel adds a relationship of a slide.
func (d *Doc) addRel(s *Slide, relType, target string, external bool) string {
	id := d.Pkg.AddRel(s.Part, relType, target, external)
	s.rels, _ = d.Pkg.Rels(s.Part)
	return id
}

func tree(s *Slide) *ox.Node { return s.tree().Root.Path("p:cSld", "p:spTree") }

// insertShape adds an element to the top of the slide, before the tree's extension list.
func insertShape(s *Slide, n *ox.Node) {
	t := tree(s)
	if ext := t.Child("p:extLst"); ext != nil {
		t.Insert(t.Index(ext), n)
		return
	}
	t.Append(n)
}

func xfrmXML(b Box, tag string) string {
	var at strings.Builder
	if b.Rot != 0 {
		fmt.Fprintf(&at, ` rot="%d"`, int64(math.Round(normDeg(b.Rot)*60000)))
	}
	if b.FlipH {
		at.WriteString(` flipH="1"`)
	}
	if b.FlipV {
		at.WriteString(` flipV="1"`)
	}
	return fmt.Sprintf(`<%s%s><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></%s>`, tag, at.String(), emu(b.X), emu(b.Y), emu(b.W), emu(b.H), tag)
}

func normDeg(a float64) float64 {
	a = math.Mod(a, 360)
	if a < 0 {
		a += 360
	}
	return a
}

// Basic shape kinds AddShape knows besides the presets.
const (
	ShapeTextBox = "textbox"
)

// AddShape adds a shape with the theme's look for a new shape; "line" and connector presets become connectors, and
// ShapeTextBox an unfilled box that grows to fit its words. It returns the new id.
func (d *Doc) AddShape(s *Slide, preset string, b Box) (int, error) {
	if preset != ShapeTextBox && !HasPreset(preset) {
		return 0, fmt.Errorf("%q is not a shape this knows", preset)
	}
	id := 0
	err := d.Edit("", []*Slide{s}, false, func() error {
		id = nextShapeID(s)
		insertShape(s, newShapeXML(id, preset, b))
		return nil
	})
	return id, err
}

func newShapeXML(id int, preset string, b Box) *ox.Node {
	const style = `<p:style><a:lnRef idx="2"><a:schemeClr val="accent1"><a:shade val="15000"/></a:schemeClr></a:lnRef><a:fillRef idx="1"><a:schemeClr val="accent1"/></a:fillRef><a:effectRef idx="0"><a:schemeClr val="accent1"/></a:effectRef><a:fontRef idx="minor"><a:schemeClr val="lt1"/></a:fontRef></p:style>`
	switch {
	case preset == ShapeTextBox:
		return newElement(fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="TextBox %d"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr>`+
			`<p:txBody><a:bodyPr wrap="square" rtlCol="0"><a:spAutoFit/></a:bodyPr><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>`, id, id-1, xfrmXML(b, "a:xfrm")))
	case preset == "line" || strings.Contains(preset, "Connector"):
		const lineStyle = `<p:style><a:lnRef idx="1"><a:schemeClr val="accent1"/></a:lnRef><a:fillRef idx="0"><a:schemeClr val="accent1"/></a:fillRef><a:effectRef idx="0"><a:schemeClr val="accent1"/></a:effectRef><a:fontRef idx="minor"><a:schemeClr val="tx1"/></a:fontRef></p:style>`
		return newElement(fmt.Sprintf(`<p:cxnSp><p:nvCxnSpPr><p:cNvPr id="%d" name="Connector %d"/><p:cNvCxnSpPr/><p:nvPr/></p:nvCxnSpPr><p:spPr>%s<a:prstGeom prst="%s"><a:avLst/></a:prstGeom></p:spPr>%s</p:cxnSp>`,
			id, id-1, xfrmXML(b, "a:xfrm"), escapeAttr(preset), lineStyle))
	}
	return newElement(fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s %d"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>%s<a:prstGeom prst="%s"><a:avLst/></a:prstGeom></p:spPr>%s`+
		`<p:txBody><a:bodyPr rtlCol="0" anchor="ctr"/><a:lstStyle/><a:p><a:pPr algn="ctr"/><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>`,
		id, "Shape", id-1, xfrmXML(b, "a:xfrm"), escapeAttr(preset), style))
}

// imageType is the content type and extension of picture bytes.
func imageType(data []byte) (ct, ext string, ok bool) {
	switch {
	case len(data) > 8 && string(data[1:4]) == "PNG":
		return "image/png", "png", true
	case len(data) > 3 && data[0] == 0xff && data[1] == 0xd8:
		return "image/jpeg", "jpeg", true
	case len(data) > 6 && string(data[:3]) == "GIF":
		return "image/gif", "gif", true
	case len(data) > 2 && string(data[:2]) == "BM":
		return "image/bmp", "bmp", true
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp", "webp", true
	case len(data) > 4 && (string(data[:4]) == "II*\x00" || string(data[:4]) == "MM\x00*"):
		return "image/tiff", "tiff", true
	}
	return "", "", false
}

// AddPicture adds a picture. A box with no size takes the picture's own, fitted to the slide and centred at (X, Y) if
// those are not zero, or on the slide.
func (d *Doc) AddPicture(s *Slide, data []byte, b Box, name string) (int, error) {
	ct, ext, ok := imageType(data)
	if !ok {
		return 0, errors.New("that file is not a picture this can read")
	}
	pw, ph, ok := PictureSize(data)
	if !ok {
		return 0, errors.New("that picture cannot be read")
	}
	if b.W <= 0 || b.H <= 0 {
		w, h := float64(pw)*0.75, float64(ph)*0.75
		k := min(d.W*0.8/w, d.H*0.8/h, 1)
		w, h = w*k, h*k
		cx, cy := d.W/2, d.H/2
		if b.X != 0 || b.Y != 0 {
			cx, cy = b.X, b.Y
		}
		b.X, b.Y, b.W, b.H = cx-w/2, cy-h/2, w, h
	}
	id := 0
	err := d.Edit("", []*Slide{s}, false, func() error {
		id = nextShapeID(s)
		part := d.Pkg.AddImage("ppt/media", ext, ct, data)
		d.pics.add(part, data)
		rid := d.addRel(s, opc.RelImage, opc.RelativeTarget(s.Part, part), false)
		if name == "" {
			name = fmt.Sprintf("Picture %d", id-1)
		}
		insertShape(s, newElement(fmt.Sprintf(`<p:pic><p:nvPicPr><p:cNvPr id="%d" name="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`+
			`<p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`,
			id, escapeAttr(name), rid, xfrmXML(b, "a:xfrm"))))
		return nil
	})
	return id, err
}

// AddTable adds a table with the default style. Its cells hold cells[row][col] where given.
func (d *Doc) AddTable(s *Slide, rows, cols int, b Box, cells [][]string) (int, error) {
	if rows < 1 || cols < 1 || rows > maxTableRows || cols > maxTableCols {
		return 0, fmt.Errorf("a table of %d by %d cannot be made", rows, cols)
	}
	if b.W <= 0 {
		b.W = d.W * 0.8
	}
	if b.H <= 0 {
		b.H = float64(rows) * 30
	}
	id := 0
	err := d.Edit("", []*Slide{s}, false, func() error {
		id = nextShapeID(s)
		cw, rh := emu(b.W)/int64(cols), emu(b.H)/int64(rows)
		var x strings.Builder
		fmt.Fprintf(&x, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Table %d"/><p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr>%s`+
			`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblPr firstRow="1" bandRow="1"><a:tableStyleId>%s</a:tableStyleId></a:tblPr><a:tblGrid>`,
			id, id-1, xfrmXML(b, "p:xfrm"), styleMedium2)
		for c := 0; c < cols; c++ {
			fmt.Fprintf(&x, `<a:gridCol w="%d"/>`, cw)
		}
		x.WriteString(`</a:tblGrid>`)
		for r := 0; r < rows; r++ {
			fmt.Fprintf(&x, `<a:tr h="%d">`, rh)
			for c := 0; c < cols; c++ {
				text := ""
				if r < len(cells) && c < len(cells[r]) {
					text = cells[r][c]
				}
				x.WriteString(`<a:tc><a:txBody><a:bodyPr/><a:lstStyle/>`)
				if text == "" {
					x.WriteString(`<a:p><a:endParaRPr lang="en-US"/></a:p>`)
				} else {
					x.WriteString(`<a:p><a:r><a:rPr lang="en-US"/><a:t>` + strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text) + `</a:t></a:r></a:p>`)
				}
				x.WriteString(`</a:txBody><a:tcPr/></a:tc>`)
			}
			x.WriteString(`</a:tr>`)
		}
		x.WriteString(`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`)
		insertShape(s, newElement(x.String()))
		return nil
	})
	return id, err
}

// element is the node in the tree that stands for the shape: its mc:AlternateContent when it has one.
func element(sh *Shape) *ox.Node {
	if sh.Alt != nil {
		return sh.Alt
	}
	return sh.Node
}

// writeBox sets a shape's position in its element's own coordinates.
func writeBox(sh *Shape, b Box) {
	apply := func(n *ox.Node) {
		var xf *ox.Node
		switch n.Name {
		case "p:graphicFrame":
			xf = n.Child("p:xfrm")
			if xf == nil {
				xf = ox.New("p:xfrm")
				n.Insert(1, xf)
			}
		case "p:grpSp":
			xf = n.Child("p:grpSpPr").Ensure("a:xfrm", spPrOrder)
		default:
			pr := n.Child("p:spPr")
			if pr == nil {
				return
			}
			xf = pr.Ensure("a:xfrm", spPrOrder)
		}
		setXfrm(xf, b)
	}
	if sh.Alt != nil {
		for _, branch := range sh.Alt.Elements() {
			for _, k := range branch.Elements() {
				apply(k)
			}
		}
		return
	}
	apply(sh.Node)
}

func setXfrm(xf *ox.Node, b Box) {
	xf.DelAttr("rot")
	if v := int64(math.Round(normDeg(b.Rot) * 60000)); v != 0 {
		xf.SetAttr("rot", strconv.FormatInt(v, 10))
	}
	set := func(name string, on bool) {
		if on {
			xf.SetAttr(name, "1")
		} else {
			xf.DelAttr(name)
		}
	}
	set("flipH", b.FlipH)
	set("flipV", b.FlipV)
	order := []string{"a:off", "a:ext", "a:chOff", "a:chExt"}
	off := xf.Ensure("a:off", order)
	off.SetAttr("x", strconv.FormatInt(emu(b.X), 10))
	off.SetAttr("y", strconv.FormatInt(emu(b.Y), 10))
	ext := xf.Ensure("a:ext", order)
	ext.SetAttr("cx", strconv.FormatInt(emu(max(b.W, 0)), 10))
	ext.SetAttr("cy", strconv.FormatInt(emu(max(b.H, 0)), 10))
}

// unplace is the box a child of the group must have to land at b on the slide.
func unplace(g *Shape, b Box) Box {
	xf := readXfrm(g.Node.Path("p:grpSpPr", "a:xfrm"))
	sx, sy := 1.0, 1.0
	if xf.chW > 0 {
		sx = g.Box.W / xf.chW
	}
	if xf.chH > 0 {
		sy = g.Box.H / xf.chH
	}
	cx, cy := Rotate(-g.Box.Rot).Apply(b.X+b.W/2-g.Box.X-g.Box.W/2, b.Y+b.H/2-g.Box.Y-g.Box.H/2)
	rot := b.Rot - g.Box.Rot
	if g.Box.FlipV {
		cy, rot = -cy, -rot
		b.FlipV = !b.FlipV
	}
	if g.Box.FlipH {
		cx, rot = -cx, -rot
		b.FlipH = !b.FlipH
	}
	b.W, b.H = b.W/sx, b.H/sy
	b.Rot = normDeg(rot)
	b.X, b.Y = (cx+g.Box.W/2)/sx+xf.chX-b.W/2, (cy+g.Box.H/2)/sy+xf.chY-b.H/2
	return b
}

// SetBox moves or resizes shapes; key joins the steps of one drag.
func (d *Doc) SetBox(s *Slide, key string, ids []int, boxes []Box) error {
	return d.Edit(key, []*Slide{s}, false, func() error {
		for i, id := range ids {
			sh := d.Find(s, id)
			if sh == nil {
				return fmt.Errorf("the slide has no shape %d", id)
			}
			d.place(s, sh, boxes[i])
		}
		return nil
	})
}

// place writes a shape's box, resizing what a box does not carry: a table's grid, a group's first placing.
func (d *Doc) place(s *Slide, sh *Shape, b Box) {
	if sh.Kind == KindTable && sh.Box.W > 0 && sh.Box.H > 0 && (b.W != sh.Box.W || b.H != sh.Box.H) {
		scaleTable(sh, b.W/sh.Box.W, b.H/sh.Box.H)
	}
	if sh.Kind == KindGroup {
		xf := sh.Node.Child("p:grpSpPr").Ensure("a:xfrm", spPrOrder)
		order := []string{"a:off", "a:ext", "a:chOff", "a:chExt"}
		ch, che := xf.Child("a:chOff"), xf.Child("a:chExt")
		if ch == nil || che == nil || attrFloat(che, "cx", 0) == 0 {
			old := readXfrm(xf)
			c := xf.Ensure("a:chOff", order)
			c.SetAttr("x", strconv.FormatInt(emu(old.box.X), 10))
			c.SetAttr("y", strconv.FormatInt(emu(old.box.Y), 10))
			e := xf.Ensure("a:chExt", order)
			e.SetAttr("cx", strconv.FormatInt(emu(old.box.W), 10))
			e.SetAttr("cy", strconv.FormatInt(emu(old.box.H), 10))
		}
	}
	if sh.Parent != nil {
		b = unplace(sh.Parent, b)
	}
	writeBox(sh, b)
	sh.Box = b
}

func scaleTable(sh *Shape, kx, ky float64) {
	tbl := sh.Node.Path("a:graphic", "a:graphicData", "a:tbl")
	for _, g := range tbl.Child("a:tblGrid").Elements("a:gridCol") {
		g.SetAttr("w", strconv.FormatInt(int64(attrFloat(g, "w", 0)*kx), 10))
	}
	for _, r := range tbl.Elements("a:tr") {
		r.SetAttr("h", strconv.FormatInt(int64(attrFloat(r, "h", 0)*ky), 10))
	}
}

// dropTiming takes the animations of the shapes out of the slide: PowerPoint repairs a file whose timing names a
// shape that is gone. An effect goes with its shape; the whole timing goes when no effect is left.
func dropTiming(s *Slide, ids map[int]bool) {
	timing := s.tree().Root.Child("p:timing")
	if timing == nil {
		return
	}
	isGone := func(n *ox.Node) bool {
		id, err := strconv.Atoi(n.Attr("spid"))
		return err == nil && ids[id]
	}
	for _, tgt := range timing.All("p:spTgt") {
		if !isGone(tgt) || tgt.Parent == nil {
			continue
		}
		// Up to the effect that holds the target.
		n := tgt
		for n != nil && n != timing {
			if n.Name == "p:par" {
				if ctn := n.Child("p:cTn"); ctn != nil && ctn.Attr("presetClass") != "" {
					break
				}
			}
			n = n.Parent
		}
		if n == nil || n == timing {
			continue
		}
		// Going up again while nothing else is left in a parent.
		for {
			list := n.Parent
			n.Remove()
			if list == nil || list.Name != "p:childTnLst" || len(list.Elements()) > 0 {
				break
			}
			ctn := list.Parent
			if ctn == nil || ctn.Parent == nil || ctn.Attr("nodeType") == "mainSeq" || ctn.Attr("nodeType") == "tmRoot" {
				break
			}
			n = ctn.Parent
			if n.Name != "p:par" {
				break
			}
		}
	}
	for _, b := range timing.Path("p:bldLst").Elements() {
		if isGone(b) {
			b.Remove()
		}
	}
	if bl := timing.Child("p:bldLst"); bl != nil && len(bl.Elements()) == 0 {
		bl.Remove()
	}
	if len(timing.All("p:spTgt")) == 0 {
		timing.Remove()
	}
}

// DeleteShapes removes shapes, and their animations.
func (d *Doc) DeleteShapes(s *Slide, ids []int) error {
	return d.Edit("", []*Slide{s}, false, func() error {
		gone := map[int]bool{}
		for _, id := range ids {
			sh := d.Find(s, id)
			if sh == nil {
				continue
			}
			Walk([]*Shape{sh}, func(k *Shape) { gone[k.ID] = true })
			element(sh).Remove()
		}
		dropTiming(s, gone)
		return nil
	})
}

// Group gathers top-level shapes into a group, at the place of the topmost. It returns the group's id. Placeholders
// cannot be in a group, and are left out.
func (d *Doc) Group(s *Slide, ids []int) (int, error) {
	var kids []*Shape
	for _, sh := range d.Shapes(s) {
		for _, id := range ids {
			if sh.ID == id && sh.Ph == nil {
				kids = append(kids, sh)
			}
		}
	}
	if len(kids) < 2 {
		return 0, errors.New("a group needs at least two shapes that are not placeholders")
	}
	id := 0
	err := d.Edit("", []*Slide{s}, false, func() error {
		id = nextShapeID(s)
		x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, k := range kids {
			x, y, w, h := k.Box.Bounds()
			x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x+w), max(y1, y+h)
		}
		g := newElement(fmt.Sprintf(`<p:grpSp><p:nvGrpSpPr><p:cNvPr id="%d" name="Group %d"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="%[3]d" y="%[4]d"/><a:ext cx="%[5]d" cy="%[6]d"/><a:chOff x="%[3]d" y="%[4]d"/><a:chExt cx="%[5]d" cy="%[6]d"/></a:xfrm></p:grpSpPr></p:grpSp>`,
			id, id-1, emu(x0), emu(y0), emu(x1-x0), emu(y1-y0)))
		last := element(kids[len(kids)-1])
		t := tree(s)
		t.Insert(t.Index(last), g)
		for _, k := range kids {
			e := element(k)
			e.Remove()
			g.Append(e)
		}
		return nil
	})
	return id, err
}

// Ungroup puts a group's shapes back among the slide's, where they are.
func (d *Doc) Ungroup(s *Slide, id int) ([]int, error) {
	g := d.Find(s, id)
	if g == nil || g.Kind != KindGroup {
		return nil, errors.New("that is not a group")
	}
	var out []int
	err := d.Edit("", []*Slide{s}, false, func() error {
		holder := element(g).Parent
		at := holder.Index(element(g))
		for _, k := range g.Kids {
			b := k.Box
			e := element(k)
			if k.Kind == KindGroup {
				// Its children keep their own coordinates; the group's frame takes the new place.
				xf := e.Child("p:grpSpPr").Ensure("a:xfrm", spPrOrder)
				setXfrm(xf, b)
			} else {
				k2 := *k
				k2.Parent = nil
				writeBox(&k2, b)
			}
			e.Remove()
			holder.Insert(at, e)
			at++
			out = append(out, k.ID)
		}
		element(g).Remove()
		return nil
	})
	return out, err
}

// Order is a move through the stack.
type Order int

const (
	ToFront Order = iota
	ToBack
	Forward
	Backward
)

// Reorder moves top-level shapes through the slide's stack, keeping their order among themselves.
func (d *Doc) Reorder(s *Slide, ids []int, how Order) error {
	return d.Edit("", []*Slide{s}, false, func() error {
		t := tree(s)
		list := d.Shapes(s)
		chosen := map[*ox.Node]bool{}
		for _, sh := range list {
			for _, id := range ids {
				if sh.ID == id {
					chosen[element(sh)] = true
				}
			}
		}
		var els []*ox.Node
		for _, sh := range list {
			els = append(els, element(sh))
		}
		switch how {
		case ToFront:
			sort.SliceStable(els, func(i, j int) bool { return !chosen[els[i]] && chosen[els[j]] })
		case ToBack:
			sort.SliceStable(els, func(i, j int) bool { return chosen[els[i]] && !chosen[els[j]] })
		case Forward:
			for i := len(els) - 2; i >= 0; i-- {
				if chosen[els[i]] && !chosen[els[i+1]] {
					els[i], els[i+1] = els[i+1], els[i]
				}
			}
		case Backward:
			for i := 1; i < len(els); i++ {
				if chosen[els[i]] && !chosen[els[i-1]] {
					els[i], els[i-1] = els[i-1], els[i]
				}
			}
		}
		// The elements go back in the places shapes held, among whatever else the tree has.
		var places []int
		for i, c := range t.Children {
			for _, sh := range list {
				if element(sh) == c {
					places = append(places, i)
				}
			}
		}
		sort.Ints(places)
		for i, at := range places {
			t.Children[at] = els[i]
		}
		return nil
	})
}

// setPrFill replaces the fill child of a properties element.
func setPrFill(pr *ox.Node, f *ox.Node, order []string) {
	for _, name := range fillNames {
		if c := pr.Child(name); c != nil {
			c.Remove()
		}
	}
	pr.InsertOrdered(f, order)
}

func solidXML(c color.RGBA) *ox.Node {
	f := newElement(`<a:solidFill><a:srgbClr val="` + Hex(c) + `"/></a:solidFill>`)
	if c.A != 255 {
		f.Elements()[0].Append(ox.New("a:alpha", "val", strconv.Itoa(int(float64(c.A)/255*100000+0.5))))
	}
	return f
}

// SetFill gives shapes a plain colour, or no fill with an alpha of zero.
func (d *Doc) SetFill(s *Slide, ids []int, c color.RGBA) error {
	return d.eachShape(s, ids, func(sh *Shape) {
		pr := spPr(sh)
		if pr == nil || sh.Kind == KindPicture {
			return
		}
		if c.A == 0 {
			setPrFill(pr, ox.New("a:noFill"), spPrOrder)
		} else {
			setPrFill(pr, solidXML(c), spPrOrder)
		}
	})
}

func (d *Doc) eachShape(s *Slide, ids []int, fn func(*Shape)) error {
	return d.Edit("", []*Slide{s}, false, func() error {
		for _, id := range ids {
			sh := d.Find(s, id)
			if sh == nil {
				return fmt.Errorf("the slide has no shape %d", id)
			}
			if sh.Kind == KindGroup {
				Walk(sh.Kids, func(k *Shape) {
					if k.Kind != KindGroup {
						fn(k)
					}
				})
				continue
			}
			fn(sh)
		}
		return nil
	})
}

// SetLine sets shapes' outlines: a colour (alpha zero for none) when setColor is true, and a width when width is not
// negative.
func (d *Doc) SetLine(s *Slide, ids []int, c color.RGBA, setColor bool, width float64) error {
	return d.eachShape(s, ids, func(sh *Shape) {
		pr := spPr(sh)
		if pr == nil {
			return
		}
		ln := pr.Ensure("a:ln", spPrOrder)
		if setColor {
			if c.A == 0 {
				setPrFill(ln, ox.New("a:noFill"), lnOrder)
			} else {
				setPrFill(ln, solidXML(c), lnOrder)
			}
		}
		if width >= 0 {
			ln.SetAttr("w", strconv.FormatInt(emu(width), 10))
		}
	})
}

// Look is how a shape is drawn, for showing in the toolbar.
func (d *Doc) Look(s *Slide, sh *Shape) (fill color.RGBA, line color.RGBA, width float64) {
	r := d.look(sh, d.slideScope(s), s.Layout)
	if r.fill != nil && !r.fill.None {
		fill = r.fill.Color
	}
	if r.line.Fill != nil && !r.line.Fill.None {
		line = r.line.Fill.Color
	}
	return fill, line, r.line.Width
}

// SetOpacity sets how see-through shapes are, from 0 to 1, in their fill and line, or a picture's alpha.
func (d *Doc) SetOpacity(s *Slide, ids []int, opacity float64) error {
	opacity = clampF(opacity, 0, 1)
	alpha := strconv.Itoa(int(opacity*100000 + 0.5))
	x := d.slideScope(s)
	return d.eachShape(s, ids, func(sh *Shape) {
		pr := spPr(sh)
		if sh.Kind == KindPicture {
			blip := sh.Node.Path("p:blipFill", "a:blip")
			if blip == nil {
				return
			}
			if c := blip.Child("a:alphaModFix"); c != nil {
				c.Remove()
			}
			if opacity < 1 {
				blip.InsertOrdered(ox.New("a:alphaModFix", "amt", alpha), blipOrder)
			}
			return
		}
		if pr == nil {
			return
		}
		look := d.look(sh, x, s.Layout)
		setAlpha := func(c *ox.Node) {
			if c == nil {
				return
			}
			for _, k := range c.Elements("a:alpha") {
				k.Remove()
			}
			if opacity < 1 {
				c.Append(ox.New("a:alpha", "val", alpha))
			}
		}
		if f := pr.Child("a:solidFill"); f != nil {
			setAlpha(first(f))
		} else if look.fill != nil && !look.fill.None && look.fill.Pic == "" && len(look.fill.Stops) < 2 && pr.Child("a:gradFill") == nil && pr.Child("a:blipFill") == nil && pr.Child("a:noFill") == nil {
			// The colour came from the style: it is written out so that it can carry the alpha.
			f := solidXML(opaque(look.fill.Color))
			setPrFill(pr, f, spPrOrder)
			setAlpha(first(f))
		}
		if gr := pr.Child("a:gradFill"); gr != nil {
			for _, g := range gr.Child("a:gsLst").Elements("a:gs") {
				setAlpha(first(g))
			}
		}
		if f := pr.Path("a:ln", "a:solidFill"); f != nil {
			setAlpha(first(f))
		}
	})
}

func opaque(c color.RGBA) color.RGBA { c.A = 255; return c }

// SetBackground gives the slide a plain background.
func (d *Doc) SetBackground(s *Slide, c color.RGBA) error {
	return d.Edit("", []*Slide{s}, false, func() error {
		cs := s.tree().Root.Child("p:cSld")
		if bg := cs.Child("p:bg"); bg != nil {
			bg.Remove()
		}
		bg := newElement(`<p:bg><p:bgPr><a:effectLst/></p:bgPr></p:bg>`)
		bg.Child("p:bgPr").Insert(0, solidXML(c))
		cs.InsertOrdered(bg, csldOrder)
		return nil
	})
}

// BackgroundColor is the slide's plain background, when it has one of its own.
func (d *Doc) BackgroundColor(s *Slide) (color.RGBA, bool) {
	f := d.slideScope(s).readFill(fillChild(s.tree().Root.Path("p:cSld", "p:bg", "p:bgPr")))
	if f == nil || f.None {
		return color.RGBA{}, false
	}
	return f.Color, true
}

// sldIDList is the presentation's list of slides.
func (d *Doc) sldIDList() *ox.Node { return d.Pres.Root.Ensure("p:sldIdLst", presOrder) }

func (d *Doc) nextSlideID() uint32 {
	high := uint32(255)
	for _, s := range d.Slides {
		high = max(high, s.ID)
	}
	for _, e := range d.sldIDList().Elements("p:sldId") {
		if v, err := strconv.ParseUint(e.Attr("id"), 10, 32); err == nil {
			high = max(high, uint32(v))
		}
	}
	return high + 1
}

// register adds a slide part to the presentation at a place.
func (d *Doc) register(s *Slide, at int) {
	id := d.nextSlideID()
	rid := d.Pkg.AddRel(d.Part, relSlide, opc.RelativeTarget(d.Part, s.Part), false)
	d.presRels, _ = d.Pkg.Rels(d.Part)
	s.ID, s.RID = id, rid
	list := d.sldIDList()
	e := ox.New("p:sldId", "id", strconv.FormatUint(uint64(id), 10), "r:id", rid)
	els := list.Elements("p:sldId")
	if at >= len(els) {
		list.Append(e)
	} else {
		list.Insert(list.Index(els[at]), e)
	}
	d.Slides = append(d.Slides, nil)
	copy(d.Slides[at+1:], d.Slides[at:])
	d.Slides[at] = s
	d.sectionAdd(s, at)
}

// sectionLists are the p14 section lists of slide ids.
func (d *Doc) sectionList() *ox.Node {
	var out *ox.Node
	d.Pres.Root.Child("p:extLst").Walk(func(n *ox.Node) bool {
		if strings.HasSuffix(n.Name, ":sectionLst") {
			out = n
		}
		return out == nil
	})
	return out
}

// sectionAdd puts a new slide into the section of the slide before it.
func (d *Doc) sectionAdd(s *Slide, at int) {
	sl := d.sectionList()
	if sl == nil {
		return
	}
	prefix := strings.TrimSuffix(sl.Name, "sectionLst")
	var list *ox.Node
	var after *ox.Node
	if at > 0 {
		prev := strconv.FormatUint(uint64(d.Slides[at-1].ID), 10)
		sl.Walk(func(n *ox.Node) bool {
			if n.Name == prefix+"sldId" && n.Attr("id") == prev {
				list, after = n.Parent, n
			}
			return true
		})
	}
	if list == nil {
		if secs := sl.Elements(prefix + "section"); len(secs) > 0 {
			list = secs[0].Child(prefix + "sldIdLst")
		}
	}
	if list == nil {
		return
	}
	e := ox.New(prefix+"sldId", "id", strconv.FormatUint(uint64(s.ID), 10))
	if after != nil {
		list.Insert(list.Index(after)+1, e)
	} else {
		list.Insert(0, e)
	}
}

func (d *Doc) sectionDrop(id uint32) {
	sl := d.sectionList()
	if sl == nil {
		return
	}
	for _, n := range sl.All(strings.TrimSuffix(sl.Name, "sectionLst") + "sldId") {
		if n.Attr("id") == strconv.FormatUint(uint64(id), 10) {
			n.Remove()
		}
	}
}

// AddSlide adds a slide on a layout at a place, with the layout's placeholders empty.
func (d *Doc) AddSlide(l *Layout, at int) (*Slide, error) {
	at = clampI(at, 0, len(d.Slides))
	if len(d.Slides) >= maxSlides {
		return nil, errors.New("the presentation has too many slides")
	}
	var s *Slide
	err := d.Edit("", nil, true, func() error {
		part := d.Pkg.UniqueName("ppt/slides/slide%d.xml")
		var shapes strings.Builder
		id := 2
		for _, k := range l.shapes {
			if k.Ph == nil || family(k.Ph.Type) == "dt" || family(k.Ph.Type) == "ftr" || family(k.Ph.Type) == "sldNum" {
				continue
			}
			at := ""
			if k.Ph.Type != "" {
				at += ` type="` + escapeAttr(k.Ph.Type) + `"`
			}
			if k.Ph.Idx != 0 {
				at += ` idx="` + strconv.Itoa(k.Ph.Idx) + `"`
			}
			if v := k.Node.Path("p:nvSpPr", "p:nvPr", "p:ph").Attr("sz"); v != "" {
				at += ` sz="` + escapeAttr(v) + `"`
			}
			shapes.WriteString(emptyPlaceholder(id, fmt.Sprintf("%s %d", phName(k.Ph.Type), id-1), strings.TrimSpace(at)))
			id++
		}
		data := []byte(slideXML(shapes.String()))
		d.Pkg.Put(part, ctSlide, data)
		d.Pkg.AddRel(part, relLayout, opc.RelativeTarget(part, l.Part), false)
		s = &Slide{Part: part, Layout: l, data: data}
		s.rels, _ = d.Pkg.Rels(part)
		d.register(s, at)
		d.touch(s)
		return nil
	})
	return s, err
}

func phName(t string) string {
	switch t {
	case "title", "ctrTitle":
		return "Title"
	case "subTitle":
		return "Subtitle"
	case "pic":
		return "Picture Placeholder"
	case "chart":
		return "Chart Placeholder"
	case "tbl":
		return "Table Placeholder"
	case "body":
		return "Text Placeholder"
	}
	return "Content Placeholder"
}

// DeleteSlide removes a slide. Other slides' links to it go, or PowerPoint would repair the file.
func (d *Doc) DeleteSlide(s *Slide) error {
	var others []*Slide
	for _, o := range d.Slides {
		if o == s {
			continue
		}
		for _, r := range o.rels {
			if r.Type == relSlide && opc.Resolve(o.Part, r.Target) == s.Part {
				others = append(others, o)
				break
			}
		}
	}
	return d.Edit("", append(others, s), true, func() error {
		list := d.sldIDList()
		for _, e := range list.Elements("p:sldId") {
			if e.Attr("r:id") == s.RID {
				e.Remove()
			}
		}
		for _, show := range d.Pres.Root.Path("p:custShowLst").Elements("p:custShow") {
			for _, e := range show.Child("p:sldLst").Elements("p:sld") {
				if e.Attr("r:id") == s.RID {
					e.Remove()
				}
			}
		}
		d.sectionDrop(s.ID)
		d.Pkg.DeleteRel(d.Part, s.RID)
		d.presRels, _ = d.Pkg.Rels(d.Part)
		for i, k := range d.Slides {
			if k == s {
				d.Slides = append(d.Slides[:i:i], d.Slides[i+1:]...)
				break
			}
		}
		for _, o := range others {
			var gone []string
			for _, r := range o.rels {
				if r.Type == relSlide && opc.Resolve(o.Part, r.Target) == s.Part {
					gone = append(gone, r.ID)
				}
			}
			o.tree().Root.Walk(func(n *ox.Node) bool {
				for _, id := range gone {
					if n.Attr("r:id") == id && (n.Name == "a:hlinkClick" || n.Name == "a:hlinkHover" || n.Name == "a:hlinkMouseOver") {
						n.Remove()
					}
				}
				return true
			})
			for _, id := range gone {
				d.Pkg.DeleteRel(o.Part, id)
			}
			o.rels, _ = d.Pkg.Rels(o.Part)
		}
		return nil
	})
}

// MoveSlide moves the slide at from to the place to.
func (d *Doc) MoveSlide(from, to int) error {
	if from < 0 || from >= len(d.Slides) {
		return fmt.Errorf("there is no slide %d", from+1)
	}
	to = clampI(to, 0, len(d.Slides)-1)
	if from == to {
		return nil
	}
	return d.Edit("", nil, true, func() error {
		s := d.Slides[from]
		list := d.sldIDList()
		els := list.Elements("p:sldId")
		e := els[from]
		e.Remove()
		els = list.Elements("p:sldId")
		if to >= len(els) {
			list.Append(e)
		} else {
			list.Insert(list.Index(els[to]), e)
		}
		d.Slides = append(d.Slides[:from:from], d.Slides[from+1:]...)
		d.Slides = append(d.Slides, nil)
		copy(d.Slides[to+1:], d.Slides[to:])
		d.Slides[to] = s
		d.sectionDrop(s.ID)
		d.sectionAdd(s, to)
		return nil
	})
}

// SetHidden hides a slide from the slideshow, or shows it.
func (d *Doc) SetHidden(s *Slide, hide bool) error {
	return d.Edit("", []*Slide{s}, false, func() error {
		if hide {
			s.tree().Root.SetAttr("show", "0")
		} else {
			s.tree().Root.DelAttr("show")
		}
		return nil
	})
}

// numberPattern turns a part's name into a pattern for a new one: ppt/charts/chart3.xml gives ppt/charts/chart%d.xml.
func numberPattern(name string) string {
	dir, file := path.Split(name)
	ext := path.Ext(file)
	base := strings.TrimRight(strings.TrimSuffix(file, ext), "0123456789")
	return dir + strings.ReplaceAll(base, "%", "") + "%d" + ext
}
