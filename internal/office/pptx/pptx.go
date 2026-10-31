// Package pptx reads and edits PowerPoint presentations. The file's own XML is the model: every part this package
// changes is an ox tree, and only those parts are written back, so what it does not understand stays as it came.
//
// On top of the trees sit a view of what a slide holds (Shape), the inheritance from layouts and masters (style.go),
// a renderer shared by the editor, the thumbnails and the slideshow (render.go) and the edits (edit*.go), each of
// which is one step of a bounded history.
package pptx

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Content types of the parts this package reads or makes.
const (
	ctPresentation = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
	ctSlideshow    = "application/vnd.openxmlformats-officedocument.presentationml.slideshow.main+xml"
	ctTemplate     = "application/vnd.openxmlformats-officedocument.presentationml.template.main+xml"
	ctSlide        = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"
	ctLayout       = "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"
	ctMaster       = "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"
	ctNotes        = "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"
	ctNotesMaster  = "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"
	ctTheme        = "application/vnd.openxmlformats-officedocument.theme+xml"
	ctPresProps    = "application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"
	ctViewProps    = "application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"
	ctTableStyles  = "application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"
	ctCore         = "application/vnd.openxmlformats-package.core-properties+xml"
	ctApp          = "application/vnd.openxmlformats-officedocument.extended-properties+xml"
)

// Relationship types.
const (
	relSlide       = opc.DocRel + "slide"
	relLayout      = opc.DocRel + "slideLayout"
	relMaster      = opc.DocRel + "slideMaster"
	relTheme       = opc.DocRel + "theme"
	relNotes       = opc.DocRel + "notesSlide"
	relNotesMaster = opc.DocRel + "notesMaster"
	relPresProps   = opc.DocRel + "presProps"
	relViewProps   = opc.DocRel + "viewProps"
	relTableStyles = opc.DocRel + "tableStyles"
)

// Namespaces new parts declare.
const (
	nsA = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsR = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsP = "http://schemas.openxmlformats.org/presentationml/2006/main"
)

// Limits on what a file may hold: every file is treated as hostile.
const (
	maxSlides   = 20000
	maxMasters  = 256
	maxLayouts  = 4096
	maxXMLBytes = 512 << 20
	maxDepth    = 2000 // how deep an element may sit in a part
	maxGroups   = 64   // how deep groups may nest
)

// EMU are the file's units: 12700 to the point.
const emuPerPt = 12700

// Master is a slide master.
type Master struct {
	Part    string
	Doc     *ox.Doc
	Rels    []opc.Rel
	Theme   *Theme
	ClrMap  map[string]string
	Layouts []*Layout
	shapes  []*Shape
}

// Layout is a slide layout.
type Layout struct {
	Part   string
	Doc    *ox.Doc
	Rels   []opc.Rel
	Master *Master
	Name   string
	Type   string
	shapes []*Shape
}

// Slide is a slide. Its XML is parsed when it is first needed, so that a long presentation opens at once.
type Slide struct {
	Part   string
	ID     uint32
	RID    string
	Layout *Layout
	Rev    uint64 // changes with every change to the slide

	number   int // on a frozen slide, its place in the presentation
	data     []byte
	doc      *ox.Doc
	rels     []opc.Rel
	dirty    bool
	shapes   []*Shape
	shapesAt uint64 // the Rev shapes were read at, 0 when they never were

	notesPart  string
	notes      *ox.Doc
	notesRels  []opc.Rel
	notesDirty bool
}

// Doc is an open presentation.
type Doc struct {
	charts sync.Map // a chart's part to its *chart.Chart, nil for one that cannot be drawn

	Pkg  *opc.Package
	Part string // the presentation part
	Pres *ox.Doc

	W, H    float64 // the slide's size in points
	Masters []*Master
	Slides  []*Slide

	presDirty   bool
	presRels    []opc.Rel
	defaultText *ox.Node // the presentation's default text style, which nothing here edits
	tableStyles *ox.Doc
	strays      map[string]bool // parts nothing pointed at in the file as it came, which stay
	pics        *picStore
	hist        history
	version     uint64
	nextRev     uint64
}

// ErrBadFile says the package is not a presentation that can be shown.
var ErrBadFile = errors.New("the file is not a usable presentation")

// parsePart reads a part's XML with the depth bounded, as everything that walks the tree recurses.
func parsePart(p *opc.Package, name string) (*ox.Doc, error) {
	part := p.Part(name)
	if part == nil {
		return nil, fmt.Errorf("the part %s is missing", name)
	}
	return parseBytes(name, part.Data)
}

func parseBytes(name string, data []byte) (*ox.Doc, error) {
	if len(data) > maxXMLBytes {
		return nil, fmt.Errorf("%s is too large", name)
	}
	if err := checkDepth(data); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	d, err := ox.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}

// checkDepth finds markup nested too deep without building a tree of it.
func checkDepth(data []byte) error {
	depth := 0
	for i := 0; i+1 < len(data); i++ {
		if data[i] != '<' {
			continue
		}
		switch data[i+1] {
		case '/':
			depth--
		case '?', '!':
		default:
			depth++
			if depth > maxDepth {
				return errors.New("the markup is nested too deep")
			}
			for j := i + 1; j < len(data); j++ {
				if data[j] == '>' {
					if data[j-1] == '/' {
						depth--
					}
					break
				}
			}
		}
	}
	return nil
}

// Open reads a presentation.
func Open(data []byte) (*Doc, error) {
	pkg, err := opc.Open(data)
	if err != nil {
		return nil, err
	}
	return load(pkg)
}

func load(pkg *opc.Package) (*Doc, error) {
	rels, err := pkg.Rels("")
	if err != nil {
		return nil, err
	}
	main, ok := opc.RelOfType(rels, opc.RelOfficeDocument)
	if !ok {
		return nil, opc.ErrNotOffice
	}
	d := &Doc{Pkg: pkg, Part: opc.Resolve("", main.Target), pics: newPicStore(pkg)}
	switch pkgType(pkg, d.Part) {
	case ctPresentation, ctSlideshow, ctTemplate, "application/vnd.ms-powerpoint.presentation.macroEnabled.main+xml",
		"application/vnd.ms-powerpoint.slideshow.macroEnabled.main+xml", "application/vnd.ms-powerpoint.template.macroEnabled.main+xml":
	default:
		return nil, opc.ErrNotOffice
	}
	if d.Pres, err = parsePart(pkg, d.Part); err != nil {
		return nil, err
	}
	if d.Pres.Root.Name != "p:presentation" {
		return nil, opc.ErrNotOffice
	}
	if d.presRels, err = pkg.Rels(d.Part); err != nil {
		return nil, err
	}
	sz := d.Pres.Root.Child("p:sldSz")
	cx, cy := attrInt(sz, "cx", 12192000), attrInt(sz, "cy", 6858000)
	if cx <= 0 || cy <= 0 || cx > 51206400*4 || cy > 51206400*4 {
		return nil, fmt.Errorf("%w: the slide size is %d by %d", ErrBadFile, cx, cy)
	}
	d.W, d.H = float64(cx)/emuPerPt, float64(cy)/emuPerPt
	if t := d.Pres.Root.Child("p:defaultTextStyle"); t != nil {
		d.defaultText = t.Clone()
	}
	if err := d.loadMasters(); err != nil {
		return nil, err
	}
	if err := d.loadSlides(); err != nil {
		return nil, err
	}
	if r, ok := opc.RelOfType(d.presRels, relTableStyles); ok {
		// Table styles only change how tables look: a broken part is treated as none.
		if ts, err := parsePart(pkg, opc.Resolve(d.Part, r.Target)); err == nil {
			d.tableStyles = ts
		}
	}
	d.strays = unreachable(pkg)
	return d, nil
}

func pkgType(p *opc.Package, name string) string {
	if part := p.Part(name); part != nil {
		return part.ContentType
	}
	return ""
}

func (d *Doc) loadMasters() error {
	ids := d.Pres.Root.Path("p:sldMasterIdLst").Elements("p:sldMasterId")
	if len(ids) == 0 {
		return fmt.Errorf("%w: it has no slide master", ErrBadFile)
	}
	if len(ids) > maxMasters {
		return fmt.Errorf("%w: too many slide masters", ErrBadFile)
	}
	layouts := 0
	for _, id := range ids {
		r, ok := opc.RelByID(d.presRels, id.Attr("r:id"))
		if !ok {
			return fmt.Errorf("%w: a slide master is not in the package", ErrBadFile)
		}
		m := &Master{Part: opc.Resolve(d.Part, r.Target)}
		var err error
		if m.Doc, err = parsePart(d.Pkg, m.Part); err != nil {
			return err
		}
		if m.Rels, err = d.Pkg.Rels(m.Part); err != nil {
			return err
		}
		m.ClrMap = clrMapOf(m.Doc.Root.Child("p:clrMap"))
		th, ok := opc.RelOfType(m.Rels, relTheme)
		if !ok {
			return fmt.Errorf("%w: %s has no theme", ErrBadFile, m.Part)
		}
		if m.Theme, err = loadTheme(d.Pkg, opc.Resolve(m.Part, th.Target)); err != nil {
			return err
		}
		for _, lid := range m.Doc.Root.Path("p:sldLayoutIdLst").Elements("p:sldLayoutId") {
			lr, ok := opc.RelByID(m.Rels, lid.Attr("r:id"))
			if !ok {
				continue
			}
			if layouts++; layouts > maxLayouts {
				return fmt.Errorf("%w: too many layouts", ErrBadFile)
			}
			l := &Layout{Part: opc.Resolve(m.Part, lr.Target), Master: m}
			if l.Doc, err = parsePart(d.Pkg, l.Part); err != nil {
				return err
			}
			if l.Rels, err = d.Pkg.Rels(l.Part); err != nil {
				return err
			}
			l.Name = l.Doc.Root.Path("p:cSld").Attr("name")
			l.Type = l.Doc.Root.Attr("type")
			m.Layouts = append(m.Layouts, l)
		}
		d.Masters = append(d.Masters, m)
		m.shapes = treeShapes(m.Doc.Root.Path("p:cSld", "p:spTree"), nil)
		for _, l := range m.Layouts {
			l.shapes = treeShapes(l.Doc.Root.Path("p:cSld", "p:spTree"), m)
		}
	}
	return nil
}

func (d *Doc) loadSlides() error {
	ids := d.Pres.Root.Path("p:sldIdLst").Elements("p:sldId")
	if len(ids) > maxSlides {
		return fmt.Errorf("%w: too many slides", ErrBadFile)
	}
	layouts := map[string]*Layout{}
	for _, m := range d.Masters {
		for _, l := range m.Layouts {
			layouts[l.Part] = l
		}
	}
	for _, id := range ids {
		r, ok := opc.RelByID(d.presRels, id.Attr("r:id"))
		if !ok {
			return fmt.Errorf("%w: a slide is not in the package", ErrBadFile)
		}
		s := &Slide{Part: opc.Resolve(d.Part, r.Target), RID: r.ID}
		n, _ := strconv.ParseUint(id.Attr("id"), 10, 32)
		s.ID = uint32(n)
		part := d.Pkg.Part(s.Part)
		if part == nil {
			return fmt.Errorf("%w: %s is missing", ErrBadFile, s.Part)
		}
		s.data = part.Data
		var err error
		if s.rels, err = d.Pkg.Rels(s.Part); err != nil {
			return err
		}
		if lr, ok := opc.RelOfType(s.rels, relLayout); ok {
			s.Layout = layouts[opc.Resolve(s.Part, lr.Target)]
		}
		if s.Layout == nil {
			// A slide with no layout of its own cannot inherit properly, but it can still show.
			if s.Layout = d.anyLayout(); s.Layout == nil {
				return fmt.Errorf("%w: it has no layouts", ErrBadFile)
			}
		}
		if nr, ok := opc.RelOfType(s.rels, relNotes); ok {
			s.notesPart = opc.Resolve(s.Part, nr.Target)
		}
		d.nextRev++
		s.Rev = d.nextRev
		d.Slides = append(d.Slides, s)
	}
	return nil
}

func (d *Doc) anyLayout() *Layout {
	for _, m := range d.Masters {
		if len(m.Layouts) > 0 {
			return m.Layouts[0]
		}
	}
	return nil
}

// tree is the slide's XML, parsed when first needed. A slide that cannot be parsed shows empty, and its bytes stay
// in the package, as nothing can then change it.
func (s *Slide) tree() *ox.Doc {
	if s.doc == nil {
		doc, err := parseBytes(s.Part, s.data)
		if err != nil || doc.Root.Name != "p:sld" || doc.Root.Path("p:cSld", "p:spTree") == nil {
			doc, _ = ox.Parse([]byte(`<p:sld xmlns:p="` + nsP + `"><p:cSld><p:spTree/></p:cSld></p:sld>`))
		}
		s.doc = doc
	}
	return s.doc
}

// Hidden reports that the slideshow skips the slide.
func (s *Slide) Hidden() bool { return s.tree().Root.Attr("show") == "0" }

// Freeze is a copy of the slide that another goroutine may render while this one is edited.
func (d *Doc) Freeze(s *Slide) *Slide {
	c := &Slide{Part: s.Part, ID: s.ID, Layout: s.Layout, Rev: s.Rev, rels: s.rels, data: s.data, number: d.Number(s)}
	if s.doc != nil {
		c.doc = s.doc.Clone()
	}
	return c
}

// Number is the slide's place, from one, or zero when it is not in the presentation.
func (d *Doc) Number(s *Slide) int {
	for i, k := range d.Slides {
		if k.Part == s.Part {
			return i + 1
		}
	}
	return 0
}

// Version changes with every change.
func (d *Doc) Version() uint64 { return d.version }

func attrInt(n *ox.Node, name string, fallback int64) int64 {
	if v, ok := n.Lookup(name); ok {
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return i
		}
	}
	return fallback
}

func attrFloat(n *ox.Node, name string, fallback float64) float64 {
	if v, ok := n.Lookup(name); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return fallback
}

// attrBool reads an xsd:boolean.
func attrBool(n *ox.Node, name string, fallback bool) bool {
	switch n.Attr(name) {
	case "1", "true", "on":
		return true
	case "0", "false", "off":
		return false
	}
	return fallback
}

func clampF(v, lo, hi float64) float64 { return min(max(v, lo), hi) }

// relsPart is the name of a part's relationships part.
func relsPart(name string) string {
	return path.Join(path.Dir(name), "_rels", path.Base(name)+".rels")
}

// unreachable are the parts no relationship leads to, from the package's own relationships on.
func unreachable(pkg *opc.Package) map[string]bool {
	seen := map[string]bool{}
	var visit func(name string, depth int)
	visit = func(name string, depth int) {
		rels, err := pkg.Rels(name)
		if err != nil || depth > 64 {
			return
		}
		for _, r := range rels {
			if r.External {
				continue
			}
			if t := opc.Resolve(name, r.Target); !seen[t] {
				seen[t] = true
				visit(t, depth+1)
			}
		}
	}
	visit("", 0)
	out := map[string]bool{}
	for _, n := range pkg.Names() {
		if !seen[n] && n != "[Content_Types].xml" && !strings.HasSuffix(n, ".rels") {
			out[n] = true
		}
	}
	return out
}

// Saver takes what is to be saved, here, and returns the work of writing it out, which may run on another goroutine.
func (d *Doc) Saver() func() ([]byte, error) {
	d.flush()
	pkg := d.Pkg.Clone()
	sweep := d.prune(pkg)
	strays := d.strays
	return func() ([]byte, error) {
		if sweep {
			for name := range unreachable(pkg) {
				if !strays[name] {
					pkg.Delete(name)
				}
			}
		}
		return pkg.Bytes()
	}
}

// Bytes is the file as it stands.
func (d *Doc) Bytes() ([]byte, error) { return d.Saver()() }

// flush moves changed trees into the package.
func (d *Doc) flush() {
	if d.presDirty {
		d.Pkg.PutDoc(d.Part, d.Pres)
		d.presDirty = false
	}
	for _, s := range d.Slides {
		if s.dirty {
			d.Pkg.Put(s.Part, ctSlide, s.tree().Bytes())
			s.data = d.Pkg.Part(s.Part).Data
			s.dirty = false
		}
		if s.notesDirty && s.notes != nil {
			d.Pkg.Put(s.notesPart, ctNotes, s.notes.Bytes())
			s.notesDirty = false
		}
	}
}

// prune takes out of pkg slides that were deleted or undone, and a slide's relationships to pictures no longer on it;
// the live package keeps them all, for undo. It reports that parts may be left that nothing reaches.
func (d *Doc) prune(pkg *opc.Package) bool {
	live := map[string]bool{}
	for _, s := range d.Slides {
		live[s.Part] = true
	}
	changed := d.hist.tainted
	for _, name := range pkg.PartsOfType(ctSlide) {
		if !live[name] {
			pkg.Delete(name)
			changed = true
		}
	}
	for _, s := range d.Slides {
		if s.doc == nil {
			continue
		}
		used := map[string]bool{}
		s.doc.Root.Walk(func(n *ox.Node) bool {
			for _, at := range n.Attrs {
				if strings.HasPrefix(at.Name, "r:") {
					used[at.Value] = true
				}
			}
			return true
		})
		for _, r := range s.rels {
			if r.Type == opc.RelImage && !used[r.ID] {
				pkg.DeleteRel(s.Part, r.ID)
				changed = true
			}
		}
	}
	return changed
}
