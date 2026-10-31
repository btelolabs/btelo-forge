package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
)

// New content on pages: text with an embedded subset of a system font, pictures, covering rectangles. All of it goes
// in a new content stream after the page's own, whose graphics state is walled off with a q and a Q.

// FontLookup finds a font file for a family and style, with every character of text if it can. An empty family means
// any readable font.
type FontLookup func(family string, bold, italic bool, text string) (path string)

// SetFonts tells the document how to find fonts for text it writes.
func (d *Doc) SetFonts(l FontLookup) { d.fonts = l }

// Face is a font program on this computer, ready to be embedded in part.
type Face struct {
	tt   *font.TrueType
	name string
}

// LoadFace reads a font file.
func LoadFace(path string) (*Face, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tt, err := font.ParseTrueType(data, 0)
	if err != nil {
		return nil, err
	}
	if tt.IsCFF() {
		return nil, errors.New("pdf: this font has no TrueType outlines to embed")
	}
	name := tt.PostScriptName()
	if name == "" {
		name = "Font"
	}
	return &Face{tt: tt, name: strings.ReplaceAll(name, " ", "")}, nil
}

func (f *Face) gid(r rune) int {
	if id, ok := f.tt.GIDByRune(r); ok {
		return id
	}
	return 0
}

// Measure is the width of a line in ems.
func (f *Face) Measure(s string) float64 {
	w := 0.0
	for _, r := range s {
		w += f.advance(r)
	}
	return w
}

func (f *Face) advance(r rune) float64 {
	a, _ := f.tt.Advance(f.gid(r))
	return a / float64(f.tt.UnitsPerEm())
}

// Ascent is the font's height above the baseline in ems.
func (f *Face) Ascent() float64 { return f.tt.Ascent() / float64(f.tt.UnitsPerEm()) }

// Descent is below it, negative.
func (f *Face) Descent() float64 { return f.tt.Descent() / float64(f.tt.UnitsPerEm()) }

// face finds a font for text in the document's way.
func (d *Doc) face(family string, bold, italic bool, text string) (*Face, error) {
	if d.fonts == nil {
		return nil, errors.New("pdf: no fonts to write text with")
	}
	path := d.fonts(family, bold, italic, text)
	if path == "" {
		return nil, errors.New("pdf: no font found on this computer")
	}
	return LoadFace(path)
}

// Embedded is a subset of a font inside the document.
type Embedded struct {
	Ref  Ref
	face *Face
	gids map[int]int
}

// Encode gives the bytes of a string in the subset, as a hex string for a content stream.
func (e *Embedded) Encode(s string) string {
	var b strings.Builder
	b.WriteByte('<')
	for _, r := range s {
		fmt.Fprintf(&b, "%04X", e.gids[e.face.gid(r)])
	}
	b.WriteByte('>')
	return b.String()
}

// Embed stores a subset of a face holding the characters of text.
func (d *Doc) Embed(f *Face, text string) (*Embedded, error) {
	seen := map[int]rune{}
	var gids []int
	for _, r := range text {
		id := f.gid(r)
		if _, ok := seen[id]; !ok {
			seen[id] = r
			gids = append(gids, id)
		}
	}
	sort.Ints(gids)
	sub, newGID, err := f.tt.Subset(gids)
	if err != nil {
		return nil, err
	}
	upem := float64(f.tt.UnitsPerEm())
	k := 1000 / upem
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = 'A' + byte((len(sub)>>(i*3)+len(text)*(i+7))%26)
	}
	base := Name(string(tag) + "+" + f.name)
	fileRef := d.Add(NewStream(Dict{"Filter": Name("FlateDecode"), "Length1": int64(len(sub))}, Deflate(sub)))
	bbox := f.tt.BBox()
	desc := d.Add(Dict{"Type": Name("FontDescriptor"), "FontName": base, "Flags": int64(4),
		"FontBBox": Reals(bbox[0]*k, bbox[1]*k, bbox[2]*k, bbox[3]*k), "ItalicAngle": Real(f.tt.ItalicAngle()),
		"Ascent": Real(f.tt.Ascent() * k), "Descent": Real(f.tt.Descent() * k), "CapHeight": Real(f.tt.CapHeight() * k),
		"StemV": int64(80), "FontFile2": fileRef})
	// Widths by new glyph id, which is the CID under Identity-H.
	type entry struct {
		cid int
		w   float64
		r   rune
	}
	var entries []entry
	for old, r := range seen {
		id, ok := newGID[old]
		if !ok {
			continue
		}
		a, _ := f.tt.Advance(old)
		entries = append(entries, entry{id, a * k, r})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].cid < entries[j].cid })
	w := Array{}
	for _, e := range entries {
		w = append(w, int64(e.cid), Array{Real(math.Round(e.w))})
	}
	cid := d.Add(Dict{"Type": Name("Font"), "Subtype": Name("CIDFontType2"), "BaseFont": base,
		"CIDSystemInfo":  Dict{"Registry": String("Adobe"), "Ordering": String("Identity"), "Supplement": int64(0)},
		"FontDescriptor": desc, "CIDToGIDMap": Name("Identity"), "DW": int64(1000), "W": w})
	var cmap bytes.Buffer
	cmap.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for i := 0; i < len(entries); i += 100 {
		end := min(i+100, len(entries))
		fmt.Fprintf(&cmap, "%d beginbfchar\n", end-i)
		for _, e := range entries[i:end] {
			var u strings.Builder
			for _, c := range utf16.Encode([]rune{e.r}) {
				fmt.Fprintf(&u, "%04X", c)
			}
			fmt.Fprintf(&cmap, "<%04X> <%s>\n", e.cid, u.String())
		}
		cmap.WriteString("endbfchar\n")
	}
	cmap.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	toUni := d.Add(NewStream(Dict{"Filter": Name("FlateDecode")}, Deflate(cmap.Bytes())))
	ref := d.Add(Dict{"Type": Name("Font"), "Subtype": Name("Type0"), "BaseFont": base, "Encoding": Name("Identity-H"),
		"DescendantFonts": Array{cid}, "ToUnicode": toUni})
	return &Embedded{Ref: ref, face: f, gids: newGID}, nil
}

// pageObject is the page's own dictionary, not the view with inherited values.
func (d *Doc) pageObject(i int) (Ref, Dict, error) {
	pages := d.Pages()
	if i < 0 || i >= len(pages) {
		return Ref{}, nil, fmt.Errorf("pdf: no page %d", i+1)
	}
	if pages[i].Ref.Num == 0 {
		return Ref{}, nil, errors.New("pdf: the page is not an indirect object")
	}
	dict := d.Dict(pages[i].Ref)
	if dict == nil {
		return Ref{}, nil, fmt.Errorf("pdf: page %d is missing", i+1)
	}
	return pages[i].Ref, dict.Clone(), nil
}

func (d *Doc) isWall(o Object, word string) bool {
	s, ok := d.Resolve(o).(*Stream)
	return ok && strings.TrimSpace(string(d.Raw(s))) == word
}

// AppendContent adds operators after the page's content, with resources under the names given in res: a category, then
// name to object. Names are chosen by the caller through FreeName.
func (d *Doc) AppendContent(index int, ops []byte, res map[Name]Dict) error {
	ref, dict, err := d.pageObject(index)
	if err != nil {
		return err
	}
	page := d.Pages()[index]
	merged := Dict{}
	if r := d.Dict(page.Dict["Resources"]); r != nil {
		merged = r.Clone()
	}
	for cat, entries := range res {
		sub := Dict{}
		if old := d.Dict(merged[cat]); old != nil {
			sub = old.Clone()
		}
		for k, v := range entries {
			sub[k] = v
		}
		merged[cat] = sub
	}
	dict["Resources"] = merged
	var list Array
	switch v := d.Resolve(dict["Contents"]).(type) {
	case Array:
		list = v.Clone()
	case *Stream:
		list = Array{dict["Contents"]}
	}
	if len(list) > 0 && !d.isWall(list[0], "q") {
		q := d.Add(NewStream(Dict{}, []byte("q\n")))
		end := d.Add(NewStream(Dict{}, []byte("Q\n")))
		list = append(append(Array{q}, list...), end)
	}
	list = append(list, d.Add(NewStream(Dict{"Filter": Name("FlateDecode")}, Deflate(ops))))
	dict["Contents"] = list
	d.SetRef(ref, dict)
	return nil
}

// FreeName is a resource name not in use on a page.
func (d *Doc) FreeName(index int, cat Name, prefix string) Name {
	res := d.Dict(d.Pages()[index].Dict["Resources"])
	used := d.Dict(res[cat])
	for i := 1; ; i++ {
		n := Name(fmt.Sprintf("%s%d", prefix, i))
		if _, ok := used[n]; !ok {
			return n
		}
	}
}

// uprightMatrix turns content so that it reads upright on a page with a /Rotate, about (x, y).
func (d *Doc) uprightMatrix(index int, x, y float64) Matrix {
	switch d.Rotation(d.Pages()[index]) {
	case 90:
		return Matrix{0, 1, -1, 0, x, y}
	case 180:
		return Matrix{-1, 0, 0, -1, x, y}
	case 270:
		return Matrix{0, -1, 1, 0, x, y}
	}
	return Matrix{1, 0, 0, 1, x, y}
}

// TextStyle is how new text looks.
type TextStyle struct {
	Family       string
	Bold, Italic bool
	Size         float64
	Color        [3]float64
}

// AddText places text on a page, its first baseline starting at (x, y) in user space, the lines spaced 1.2 sizes
// apart. width, when above zero, wraps long lines.
func (d *Doc) AddText(index int, text string, x, y, width float64, st TextStyle) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("pdf: no text to add")
	}
	return d.Do(func() error {
		f, err := d.face(st.Family, st.Bold, st.Italic, text)
		if err != nil {
			return err
		}
		lines := wrapText(text, width/max(st.Size, 0.1), f.Measure)
		em, err := d.Embed(f, strings.Join(lines, ""))
		if err != nil {
			return err
		}
		name := d.FreeName(index, "Font", "FF")
		var b bytes.Buffer
		m := d.uprightMatrix(index, x, y)
		fmt.Fprintf(&b, "q %s %s %s %s %s %s cm BT /%s %s Tf %s rg %s TL\n", fmtNum(m[0]), fmtNum(m[1]), fmtNum(m[2]), fmtNum(m[3]), fmtNum(m[4]), fmtNum(m[5]),
			name, fmtNum(st.Size), rgbOps(st.Color), fmtNum(st.Size*1.2))
		for _, l := range lines {
			fmt.Fprintf(&b, "%s Tj T*\n", em.Encode(l))
		}
		b.WriteString("ET Q\n")
		return d.AppendContent(index, b.Bytes(), map[Name]Dict{"Font": {name: em.Ref}})
	})
}

func rgbOps(c [3]float64) string {
	return fmtNum(c[0]) + " " + fmtNum(c[1]) + " " + fmtNum(c[2])
}

// wrapText breaks text into lines of at most width ems, at spaces or between CJK characters; width 0 breaks only at
// line ends.
func wrapText(text string, width float64, measure func(string) float64) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if width <= 0 || measure(para) <= width {
			out = append(out, para)
			continue
		}
		rs := []rune(para)
		for len(rs) > 0 {
			end, lastBreak := 0, -1
			for end < len(rs) && measure(string(rs[:end+1])) <= width {
				if rs[end] == ' ' || rs[end] >= 0x2E80 {
					lastBreak = end + 1
				}
				end++
			}
			switch {
			case end == len(rs):
			case end == 0:
				end = 1
			case lastBreak > 0:
				end = lastBreak
			}
			out = append(out, strings.TrimRight(string(rs[:end]), " "))
			rs = rs[end:]
		}
	}
	return out
}

// CoverRect paints a rectangle over what is on the page. What it covers stays in the file.
func (d *Doc) CoverRect(index int, r Rect, c [3]float64) error {
	return d.Do(func() error {
		ops := fmt.Sprintf("q %s rg %s %s %s %s re f Q\n", rgbOps(c), fmtNum(r.X0), fmtNum(r.Y0), fmtNum(r.W()), fmtNum(r.H()))
		return d.AppendContent(index, []byte(ops), nil)
	})
}

// AddImageObject stores a picture as an image XObject. JPEG files go in as they are; img is used otherwise.
func (d *Doc) AddImageObject(img image.Image, jpegData []byte) (Ref, int, int, error) {
	if len(jpegData) > 0 {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpegData))
		if err != nil {
			return Ref{}, 0, 0, err
		}
		cs := Name("DeviceRGB")
		extra := Dict{}
		switch cfg.ColorModel {
		case color.GrayModel:
			cs = "DeviceGray"
		case color.CMYKModel:
			cs = "DeviceCMYK"
			extra["Decode"] = Reals(1, 0, 1, 0, 1, 0, 1, 0)
		}
		dict := Dict{"Type": Name("XObject"), "Subtype": Name("Image"), "Width": int64(cfg.Width), "Height": int64(cfg.Height),
			"ColorSpace": cs, "BitsPerComponent": int64(8), "Filter": Name("DCTDecode")}
		for k, v := range extra {
			dict[k] = v
		}
		return d.Add(NewStream(dict, jpegData)), cfg.Width, cfg.Height, nil
	}
	if img == nil {
		return Ref{}, 0, 0, errors.New("pdf: no picture")
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return Ref{}, 0, 0, errors.New("pdf: an empty picture")
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(nrgba, nrgba.Rect, img, b.Min, draw.Src)
	rgb := make([]byte, 3*w*h)
	alpha := make([]byte, w*h)
	opaque := true
	for i := range w * h {
		copy(rgb[i*3:], nrgba.Pix[i*4:i*4+3])
		alpha[i] = nrgba.Pix[i*4+3]
		opaque = opaque && alpha[i] == 255
	}
	dict := Dict{"Type": Name("XObject"), "Subtype": Name("Image"), "Width": int64(w), "Height": int64(h),
		"ColorSpace": Name("DeviceRGB"), "BitsPerComponent": int64(8), "Filter": Name("FlateDecode")}
	if !opaque {
		dict["SMask"] = d.Add(NewStream(Dict{"Type": Name("XObject"), "Subtype": Name("Image"), "Width": int64(w), "Height": int64(h),
			"ColorSpace": Name("DeviceGray"), "BitsPerComponent": int64(8), "Filter": Name("FlateDecode")}, Deflate(alpha)))
	}
	return d.Add(NewStream(dict, Deflate(rgb))), w, h, nil
}

// AddImage places a picture to fill r, which is in user space, on a page.
func (d *Doc) AddImage(index int, img image.Image, jpegData []byte, r Rect) error {
	return d.Do(func() error {
		ref, _, _, err := d.AddImageObject(img, jpegData)
		if err != nil {
			return err
		}
		name := d.FreeName(index, "XObject", "FIm")
		m := d.pictureMatrix(index, r)
		ops := fmt.Sprintf("q %s %s %s %s %s %s cm /%s Do Q\n", fmtNum(m[0]), fmtNum(m[1]), fmtNum(m[2]), fmtNum(m[3]), fmtNum(m[4]), fmtNum(m[5]), name)
		return d.AppendContent(index, []byte(ops), map[Name]Dict{"XObject": {name: ref}})
	})
}

// pictureMatrix maps the unit square to r so that a picture shows upright on a rotated page.
func (d *Doc) pictureMatrix(index int, r Rect) Matrix {
	switch d.Rotation(d.Pages()[index]) {
	case 90:
		return Matrix{0, r.H(), -r.W(), 0, r.X1, r.Y0}
	case 180:
		return Matrix{-r.W(), 0, 0, -r.H(), r.X1, r.Y1}
	case 270:
		return Matrix{0, -r.H(), r.W(), 0, r.X0, r.Y1}
	}
	return Matrix{r.W(), 0, 0, r.H(), r.X0, r.Y0}
}
