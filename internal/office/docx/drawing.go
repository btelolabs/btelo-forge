package docx

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"strconv"
	"strings"
	"sync"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Pictures, shapes and text boxes: what a w:drawing holds, read as far as it can be shown. The element itself is kept
// and is what is saved.

// Anchor is something floating on the page, tied to a paragraph.
type Anchor struct {
	Key    string // a picture's key, or "?label" for a placeholder
	W, H   float32
	Behind bool
	Box    *Story // a text box's words
	Fill   color.RGBA
	Line   color.RGBA
	Inset  [4]float32 // top, left, bottom, right
	Node   *ox.Node   // the w:drawing
	Run    *ox.Node
	hRel   string
	hAlign string
	hOff   float32
	vRel   string
	vAlign string
	vOff   float32
	stack  *Stack
}

// place works out where the anchor lands on a page, its paragraph's top being at y.
func (a *Anchor) place(pg *Page, s *Section, colX, colW, y float32) gfx.Rect {
	base, span := colX, colW
	switch a.hRel {
	case "page":
		base, span = 0, s.W
	case "margin", "insideMargin", "outsideMargin":
		base, span = s.ML, s.TextW()
	case "leftMargin":
		base, span = 0, s.ML
	case "rightMargin":
		base, span = s.W-s.MR, s.MR
	}
	x := base + a.hOff
	switch a.hAlign {
	case "left", "inside":
		x = base
	case "center":
		x = base + (span-a.W)/2
	case "right", "outside":
		x = base + span - a.W
	}
	base, span = y, 0
	switch a.vRel {
	case "page":
		base, span = 0, s.H
	case "margin":
		base, span = s.MT, s.H-s.MT-s.MB
	case "topMargin":
		base, span = 0, s.MT
	case "bottomMargin":
		base, span = s.H-s.MB, s.MB
	}
	ny := base + a.vOff
	switch a.vAlign {
	case "top":
		ny = base
	case "center":
		ny = base + (span-a.H)/2
	case "bottom":
		ny = base + span - a.H
	}
	return gfx.Rect{X: x, Y: ny, W: a.W, H: a.H}
}

// picRef is the picture under a graphic: its key.
func (pb *paraBuild) picKey(graphic *ox.Node, part *Part) string {
	blip := firstNamed(graphic, "a:blip", 0)
	if blip == nil {
		return ""
	}
	id := blip.Attr("r:embed")
	if id == "" {
		return ""
	}
	rel, ok := opc.RelByID(part.Rels, id)
	if !ok || rel.External {
		return ""
	}
	crop := ""
	if sr := firstNamed(graphic, "a:srcRect", 0); sr != nil {
		crop = strings.Join([]string{sr.Attr("l"), sr.Attr("t"), sr.Attr("r"), sr.Attr("b")}, ",")
	}
	return relResolve(part.Name, rel.Target) + "\x00" + crop
}

func relResolve(from, target string) string {
	return opc.Resolve(from, target)
}

func relTarget(from, to string) string {
	return opc.RelativeTarget(from, to)
}

// firstNamed is the first element called name under n, however deep.
func firstNamed(n *ox.Node, name string, depth int) *ox.Node {
	if n == nil || depth > maxDepth {
		return nil
	}
	for _, c := range n.Children {
		if c.Kind != ox.Element {
			continue
		}
		if c.Name == name {
			return c
		}
		if f := firstNamed(c, name, depth+1); f != nil {
			return f
		}
	}
	return nil
}

// graphicLabel says what a drawing with no picture is.
func graphicLabel(n *ox.Node) string {
	for _, probe := range []struct{ name, label string }{{"c:chart", "chart"}, {"dgm:relIds", "diagram"}, {"wpg:wgp", "shapes"}, {"wps:wsp", "shape"}, {"wpc:wpc", "canvas"}} {
		if firstNamed(n, probe.name, 0) != nil {
			return probe.label
		}
	}
	return "object"
}

// sized reads a picture's size, kept to what a page can hold.
func sized(cx, cy string) (w, h float32) {
	return min(max(emuPt(cx), 1), maxPageSide), min(max(emuPt(cy), 1), maxPageSide)
}

func (pb *paraBuild) drawing(c, r *ox.Node, rp *RP, st walkState, locked bool) {
	part := pb.part
	switch c.Name {
	case "w:drawing":
		for _, k := range c.Elements() {
			switch k.Name {
			case "wp:inline":
				e := k.Child("wp:extent")
				w, h := sized(e.Attr("cx"), e.Attr("cy"))
				key := pb.picKey(k, part)
				if key == "" {
					key = graphicKey(k, part)
				}
				pb.place(&Seg{N: 1, Kind: SegPicture, R: r, T: c, Locked: locked, Link: st.link},
					flow.Run{Inline: &flow.Inline{W: w, H: h, Key: key}, Style: rpStyle(rp, rp.HAnsi)})
			case "wp:anchor":
				pb.anchor(k, c, r, part)
			}
		}
	default:
		// VML, from old documents: a picture sized in its style, else a mark that something is there.
		w, h := vmlSize(c)
		key := "?" + "object"
		if im := firstNamed(c, "v:imagedata", 0); im != nil {
			if rel, ok := opc.RelByID(part.Rels, im.Attr("r:id")); ok && !rel.External {
				key = relResolve(part.Name, rel.Target) + "\x00"
			}
		}
		pb.place(&Seg{N: 1, Kind: SegPicture, R: r, T: c, Locked: locked, Link: st.link},
			flow.Run{Inline: &flow.Inline{W: w, H: h, Key: key}, Style: rpStyle(rp, rp.HAnsi)})
	}
}

// vmlSize reads width and height from a VML shape's style.
func vmlSize(c *ox.Node) (w, h float32) {
	w, h = 72, 18
	shape := firstNamed(c, "v:shape", 0)
	if shape == nil {
		return w, h
	}
	for _, part := range strings.Split(shape.Attr("style"), ";") {
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "width":
			if p := measure(strings.TrimSpace(v)); p > 0 {
				w = min(p, maxPageSide)
			}
		case "height":
			if p := measure(strings.TrimSpace(v)); p > 0 {
				h = min(p, maxPageSide)
			}
		}
	}
	return w, h
}

func (pb *paraBuild) anchor(k, c, r *ox.Node, part *Part) {
	e := k.Child("wp:extent")
	a := &Anchor{Node: c, Run: r}
	a.W, a.H = sized(e.Attr("cx"), e.Attr("cy"))
	a.Behind = on(k.Attr("behindDoc")) && k.Attr("behindDoc") != ""
	a.Key = pb.picKey(k, part)
	pos := func(n *ox.Node, rel, align *string, off *float32) {
		if n == nil {
			return
		}
		*rel = n.Attr("relativeFrom")
		if al := n.Child("wp:align"); al != nil {
			*align = al.InnerText()
		}
		if o := n.Child("wp:posOffset"); o != nil {
			*off = emuPt(o.InnerText())
		}
	}
	pos(k.Child("wp:positionH"), &a.hRel, &a.hAlign, &a.hOff)
	pos(k.Child("wp:positionV"), &a.vRel, &a.vAlign, &a.vOff)
	if a.Key == "" {
		a.Inset = [4]float32{3.6, 7.2, 3.6, 7.2}
		if body := firstNamed(k, "wps:bodyPr", 0); body != nil {
			read := func(name string, field *float32) {
				if v, ok := body.Lookup(name); ok {
					*field = emuPt(v)
				}
			}
			read("tIns", &a.Inset[0])
			read("lIns", &a.Inset[1])
			read("bIns", &a.Inset[2])
			read("rIns", &a.Inset[3])
		}
		if sp := firstNamed(k, "wps:spPr", 0); sp != nil {
			if v := sp.Path("a:solidFill", "a:srgbClr").Attr("val"); v != "" {
				a.Fill = hexColor(v, color.RGBA{})
			}
			if v := sp.Path("a:ln", "a:solidFill", "a:srgbClr").Attr("val"); v != "" {
				a.Line = hexColor(v, color.RGBA{})
			}
		}
		if tx := firstNamed(k, "w:txbxContent", 0); tx != nil {
			a.Box = pb.d.newStory(part, tx)
			a.Box.ReadOnly = true
			a.Box.sync()
		} else {
			a.Key = graphicKey(k, part)
		}
	}
	pb.p.Anchors = append(pb.p.Anchors, a)
}

// Pictures decodes the document's pictures once, away from the window, and keeps them at a size pages can use.
type Pictures struct {
	pkg     *opc.Package
	mu      sync.Mutex
	cache   map[string]*picEntry
	Ready   func()
	Sync    bool // decode at once, for rendering without a window
	slots   chan struct{}
	pending sync.WaitGroup
}

type picEntry struct {
	img     image.Image
	loading bool
}

// Largest size a picture is kept at, and the largest it may be in the file.
const (
	maxPicSide   = 2400
	maxPicPixels = 120_000_000
)

func newPictures(pkg *opc.Package) *Pictures {
	return &Pictures{pkg: pkg, cache: map[string]*picEntry{}, slots: make(chan struct{}, 2)}
}

// Get is the picture for a key, or nil while it is being decoded or when it cannot be.
func (p *Pictures) Get(key string) image.Image {
	if strings.HasPrefix(key, "?") {
		return nil
	}
	p.mu.Lock()
	e := p.cache[key]
	if e != nil {
		img := e.img
		p.mu.Unlock()
		return img
	}
	e = &picEntry{loading: true}
	p.cache[key] = e
	p.mu.Unlock()
	name, crop, _ := strings.Cut(key, "\x00")
	part := p.pkg.Part(name)
	if part == nil {
		e.loading = false
		return nil
	}
	data := part.Data
	if p.Sync {
		e.img, e.loading = decodePicture(data, crop), false
		return e.img
	}
	p.pending.Add(1)
	go func() {
		defer p.pending.Done()
		p.slots <- struct{}{}
		img := decodePicture(data, crop)
		<-p.slots
		p.mu.Lock()
		e.img, e.loading = img, false
		ready := p.Ready
		p.mu.Unlock()
		if ready != nil {
			ready()
		}
	}()
	return nil
}

// Wait returns when the pictures being decoded are done.
func (p *Pictures) Wait() { p.pending.Wait() }

// decodePicture reads a picture, cropped, and scales down what is larger than a page needs. It returns nil for what
// it cannot read: a format with no decoder here, or a hostile file.
func decodePicture(data []byte, crop string) (out image.Image) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPicPixels {
		return nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b := src.Bounds()
	if crop != "" && crop != ",,," {
		parts := strings.Split(crop, ",")
		if len(parts) == 4 {
			f := func(i int) float64 {
				v, _ := strconv.ParseFloat(parts[i], 64)
				return min(max(v/100000, 0), 1)
			}
			w, h := float64(b.Dx()), float64(b.Dy())
			r := image.Rect(b.Min.X+int(f(0)*w), b.Min.Y+int(f(1)*h), b.Max.X-int(f(2)*w), b.Max.Y-int(f(3)*h))
			if !r.Empty() {
				b = r
			}
		}
	}
	w, h := b.Dx(), b.Dy()
	if m := max(w, h); m > maxPicSide {
		w, h = max(w*maxPicSide/m, 1), max(h*maxPicSide/m, 1)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Rect, src, b.Min, draw.Src)
	} else {
		xdraw.ApproxBiLinear.Scale(dst, dst.Rect, src, b, draw.Src, nil)
	}
	return dst
}

// encodedPicture gives bytes a Word file can hold: PNG and JPEG as they are, others converted to PNG.
func encodedPicture(data []byte) (out []byte, ext, contentType string, w, h int, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", "", 0, 0, err
	}
	switch format {
	case "png":
		return data, "png", "image/png", cfg.Width, cfg.Height, nil
	case "jpeg":
		return data, "jpg", "image/jpeg", cfg.Width, cfg.Height, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", "", 0, 0, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", "", 0, 0, err
	}
	return buf.Bytes(), "png", "image/png", cfg.Width, cfg.Height, nil
}
