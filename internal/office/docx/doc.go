// Package docx reads, lays out and edits WordprocessingML documents. The file's own XML is the model: a document is
// the package with its text parts parsed into ox trees, every edit changes those trees, and a save writes back only
// the parts that changed. What this package does not understand stays in the tree, or in its part, untouched.
//
// Around the trees there is a view, rebuilt as needed and never saved: blocks (paragraphs and tables) with their
// resolved properties (block.go), and pages made from them (layout.go).
package docx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// The namespace of WordprocessingML.
const nsW = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// Content types of the parts this package makes.
const (
	ctMain      = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
	ctStyles    = "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"
	ctNumbering = "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"
	ctSettings  = "application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"
	ctTheme     = "application/vnd.openxmlformats-officedocument.theme+xml"
	ctHeader    = "application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"
	ctFooter    = "application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"
	ctCore      = "application/vnd.openxmlformats-package.core-properties+xml"
	ctApp       = "application/vnd.openxmlformats-officedocument.extended-properties+xml"
)

// Relationship types of the parts this package follows.
const (
	relStyles    = opc.DocRel + "styles"
	relNumbering = opc.DocRel + "numbering"
	relSettings  = opc.DocRel + "settings"
	relTheme     = opc.DocRel + "theme"
	relHeader    = opc.DocRel + "header"
	relFooter    = opc.DocRel + "footer"
)

// Limits on what a file may ask of the layout: hostile nesting must not exhaust the stack.
const (
	maxDepth      = 64      // how deep containers and tables may nest
	maxTableDepth = 12      // how deep tables may nest
	maxPageSide   = 14400.0 // points: Word's own limit is 22 inches, this allows for anything it can write
	maxGridCols   = 256
)

// Part is an XML part being worked on.
type Part struct {
	Name  string
	X     *ox.Doc
	Rels  []opc.Rel
	dirty bool
}

// Doc is an open document.
type Doc struct {
	Pkg    *opc.Package
	Main   *Part
	Styles *Styles
	Num    *Numbering
	Theme  *Theme

	parts map[string]*Part // the parts parsed for editing, by name

	Body     *Story            // the document's text
	stories  map[string]*Story // headers and footers, by part name
	sects    []*Section        // the sections, in order
	Fonts    flow.Fonts        // what text is measured with
	Pics     *Pictures         // the document's pictures, decoded
	charts   charts            // the document's charts, read
	hist     history           //
	lay      paginator         //
	EditPage int               // the page a header or footer being edited is shown on
	Sel      Selection         // the selection, kept here so that edits and history move it
	Pending  *Pending          // formatting chosen with the caret alone, for what is typed next
	rev      uint64            // counts changes, for the shell
	defTab   float32           // the default tab stops, in points
	evenOdd  bool              // headers differ on even pages
	lang     string            // the East Asian language of the theme's fonts
	clip     *clipboard        // what was last copied here, with its formatting
}

// Open reads a document. It returns an error for anything that is not one; it does not panic on a damaged file.
func Open(data []byte, fonts flow.Fonts) (d *Doc, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = nil, fmt.Errorf("the document is damaged: %v", r)
		}
	}()
	pkg, err := opc.Open(data)
	if err != nil {
		return nil, err
	}
	return load(pkg, fonts)
}

func load(pkg *opc.Package, fonts flow.Fonts) (*Doc, error) {
	rels, err := pkg.Rels("")
	if err != nil {
		return nil, err
	}
	rel, ok := opc.RelOfType(rels, opc.RelOfficeDocument)
	if !ok {
		return nil, opc.ErrNotOffice
	}
	d := &Doc{Pkg: pkg, Fonts: fonts, parts: map[string]*Part{}, stories: map[string]*Story{}, defTab: 36}
	d.Main, err = d.part(opc.Resolve("", rel.Target))
	if err != nil {
		return nil, err
	}
	if d.Main.X.Root.Name != "w:document" || d.Main.X.Root.Child("w:body") == nil {
		return nil, errors.New("this file has no Word document in it")
	}
	d.Theme = d.loadTheme()
	d.loadSettings()
	d.Styles = d.loadStyles()
	d.Num = d.loadNumbering()
	d.Pics = newPictures(pkg)
	d.Body = d.newStory(d.Main, d.Main.X.Root.Child("w:body"))
	d.rebuild()
	d.lay.restart()
	d.Sel = Selection{Caret: Pos{B: d.Body.first()}}
	d.Sel.Anchor = d.Sel.Caret
	return d, nil
}

// part is a part parsed for editing, with its relationships.
func (d *Doc) part(name string) (*Part, error) {
	if p := d.parts[name]; p != nil {
		return p, nil
	}
	x, err := d.Pkg.Doc(name)
	if err != nil {
		return nil, err
	}
	rels, err := d.Pkg.Rels(name)
	if err != nil {
		return nil, err
	}
	p := &Part{Name: name, X: x, Rels: rels}
	d.parts[name] = p
	return p, nil
}

// optional is a part the main part relates to with the type, nil when there is none or it cannot be read: a document
// with broken styles still opens, with the defaults.
func (d *Doc) optional(relType string) *Part {
	rel, ok := opc.RelOfType(d.Main.Rels, relType)
	if !ok || rel.External {
		return nil
	}
	p, err := d.part(opc.Resolve(d.Main.Name, rel.Target))
	if err != nil {
		return nil
	}
	return p
}

// addRel relates part p to a target and keeps the part's list of relationships in step.
func (d *Doc) addRel(p *Part, relType, target string, external bool) string {
	id := d.Pkg.AddRel(p.Name, relType, target, external)
	if rels, err := d.Pkg.Rels(p.Name); err == nil {
		p.Rels = rels
	}
	return id
}

// Version counts the changes made to the document.
func (d *Doc) Version() uint64 { return d.rev }

// touched notes that the part changed, so it is written on the next save.
func (d *Doc) touched(p *Part) {
	p.dirty = true
	d.rev++
}

// flush writes the changed parts into the package.
func (d *Doc) flush() {
	for _, p := range d.parts {
		if p.dirty {
			d.Pkg.PutDoc(p.Name, p.X)
			p.dirty = false
		}
	}
}

// Snapshot takes what is to be saved, on the goroutine that edits, and returns the work of packing it, safe to run on
// another.
func (d *Doc) Snapshot() func() ([]byte, error) {
	d.flush()
	return d.Pkg.Clone().Bytes
}

// Bytes is the document as a file.
func (d *Doc) Bytes() ([]byte, error) { return d.Snapshot()() }

// on reads a boolean attribute value of OOXML.
func on(v string) bool {
	switch v {
	case "0", "false", "off", "False", "Off":
		return false
	}
	return true
}

// toggle is the value of an on/off element: present and without a w:val means on.
func toggle(n *ox.Node) bool {
	if v, ok := n.Lookup("w:val"); ok {
		return on(v)
	}
	return true
}

// atoi reads a whole number, 0 when it is not one. Numbers in files may be written as "1.5" by odd producers.
func atoi(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > -1e9 && f < 1e9 {
		return int(f)
	}
	return 0
}

// measure reads a length that is in twips, or with a unit where the file says so ("2.5cm", "12pt"), into points.
func measure(s string) float32 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	for _, u := range []struct {
		suffix string
		points float64
	}{{"pt", 1}, {"cm", 72 / 2.54}, {"mm", 72 / 25.4}, {"in", 72}, {"pc", 12}, {"pi", 12}} {
		if strings.HasSuffix(s, u.suffix) {
			f, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			if err != nil || f > 1e7 || f < -1e7 {
				return 0
			}
			return float32(f * u.points)
		}
	}
	return float32(atoi(s)) / 20
}

// twips writes points as a whole number of twips.
func twips(pt float32) string {
	return strconv.Itoa(int(pt*20 + copysign(0.5, pt)))
}

func copysign(v, sign float32) float32 {
	if sign < 0 {
		return -v
	}
	return v
}

const emuPerPoint = 12700

// emuPt reads a length in EMU into points, bounded.
func emuPt(s string) float32 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f > 1e12 || f < -1e12 {
		return 0
	}
	return float32(f / emuPerPoint)
}
