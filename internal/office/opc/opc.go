// Package opc reads and writes Open Packaging Conventions files: the zip containers that .docx, .xlsx and .pptx are.
// A package keeps every part it read, byte for byte, unless a part is replaced; so whatever the formats here do not
// understand goes back out unchanged, with its content type and relationships.
package opc

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// RelNS is the namespace of the package's relationship parts.
const RelNS = "http://schemas.openxmlformats.org/package/2006/relationships"

// DocRel starts the usual relationship types.
const DocRel = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"

// Relationship types the formats share.
const (
	RelOfficeDocument = DocRel + "officeDocument"
	RelImage          = DocRel + "image"
	RelHyperlink      = DocRel + "hyperlink"
	RelCoreProps      = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
	RelAppProps       = DocRel + "extended-properties"
)

// Limits on what is read: a zip can claim, or unpack to, far more than the file's size.
const (
	maxParts    = 200000
	maxPartSize = 2 << 30
	maxTotal    = 8 << 30
)

// Errors a caller tells the user about.
var (
	ErrEncrypted = errors.New("the file is protected with a password")
	ErrNotOffice = errors.New("this is not an Office Open XML file")
)

// Part is one file of the package.
type Part struct {
	Name        string // the name in the package, without a leading slash
	ContentType string
	Data        []byte
	stamp       time.Time
	method      uint16
}

// Rel is one relationship.
type Rel struct {
	ID       string
	Type     string
	Target   string
	External bool
}

// Package is an open package.
type Package struct {
	parts    map[string]*Part
	order    []string // the parts in the order they were in the file; new ones follow
	defaults map[string]string
	override map[string]string
	ctDoc    *ox.Doc
}

// Open reads a package.
func Open(data []byte) (*Package, error) {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
		// A compound file: what Office writes for a password-protected document, or a 97-2003 one.
		return nil, ErrEncrypted
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ErrNotOffice
	}
	if len(zr.File) > maxParts {
		return nil, fmt.Errorf("the file has too many parts (%d)", len(zr.File))
	}
	p := &Package{parts: map[string]*Part{}, defaults: map[string]string{}, override: map[string]string{}}
	var total uint64
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		name := strings.TrimPrefix(f.Name, "/")
		if clean := path.Clean(name); clean != name || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("a part has an unsafe name: %q", f.Name)
		}
		if f.UncompressedSize64 > maxPartSize {
			return nil, fmt.Errorf("%s is too large", name)
		}
		total += f.UncompressedSize64
		if total > maxTotal {
			return nil, errors.New("the file unpacks to too much data")
		}
		r, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		// The size in the header can lie: never read more than the limit, whatever it says.
		body, err := io.ReadAll(io.LimitReader(r, maxPartSize+1))
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if len(body) > maxPartSize {
			return nil, fmt.Errorf("%s is too large", name)
		}
		if _, dup := p.parts[name]; dup {
			return nil, fmt.Errorf("the part %s is there twice", name)
		}
		p.parts[name] = &Part{Name: name, Data: body, stamp: f.Modified, method: f.Method}
		p.order = append(p.order, name)
	}
	ct := p.parts["[Content_Types].xml"]
	if ct == nil {
		return nil, ErrNotOffice
	}
	if p.ctDoc, err = ox.Parse(ct.Data); err != nil {
		return nil, fmt.Errorf("[Content_Types].xml: %w", err)
	}
	for _, e := range p.ctDoc.Root.Elements() {
		switch e.Name {
		case "Default":
			p.defaults[strings.ToLower(e.Attr("Extension"))] = e.Attr("ContentType")
		case "Override":
			p.override[strings.TrimPrefix(e.Attr("PartName"), "/")] = e.Attr("ContentType")
		}
	}
	for _, part := range p.parts {
		part.ContentType = p.typeOf(part.Name)
	}
	return p, nil
}

// NewPackage is an empty package.
func NewPackage() *Package {
	d, err := ox.Parse([]byte(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/></Types>`))
	if err != nil {
		panic(err)
	}
	p := &Package{parts: map[string]*Part{}, defaults: map[string]string{}, override: map[string]string{}, ctDoc: d}
	p.defaults["rels"] = "application/vnd.openxmlformats-package.relationships+xml"
	p.defaults["xml"] = "application/xml"
	p.parts["[Content_Types].xml"] = &Part{Name: "[Content_Types].xml", method: zip.Deflate}
	p.order = append(p.order, "[Content_Types].xml")
	return p
}

func (p *Package) typeOf(name string) string {
	if t, ok := p.override[name]; ok {
		return t
	}
	return p.defaults[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
}

// Part is the named part, or nil.
func (p *Package) Part(name string) *Part { return p.parts[strings.TrimPrefix(name, "/")] }

// Names are the parts' names in file order.
func (p *Package) Names() []string { return slices.Clone(p.order) }

// PartsOfType are the names of the parts with the content type, in file order.
func (p *Package) PartsOfType(contentType string) []string {
	var out []string
	for _, n := range p.order {
		if p.parts[n].ContentType == contentType {
			out = append(out, n)
		}
	}
	return out
}

// Put adds or replaces a part. The content type goes in an Override unless the extension's default already says it.
func (p *Package) Put(name, contentType string, data []byte) {
	name = strings.TrimPrefix(name, "/")
	part := p.parts[name]
	if part == nil {
		part = &Part{Name: name, method: zip.Deflate}
		p.parts[name] = part
		p.order = append(p.order, name)
	}
	part.Data = data
	if contentType != "" {
		part.ContentType = contentType
	}
	if part.ContentType != p.typeOf(name) {
		p.override[name] = part.ContentType
		p.syncOverride(name, part.ContentType)
	}
}

// SetData replaces a part's bytes, keeping its type.
func (p *Package) SetData(name string, data []byte) { p.Put(name, "", data) }

// SetDefault declares the content type for an extension, as pictures are.
func (p *Package) SetDefault(ext, contentType string) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if p.defaults[ext] == contentType {
		return
	}
	if _, ok := p.defaults[ext]; ok {
		return // a different type is declared; parts use an Override instead
	}
	p.defaults[ext] = contentType
	// Default entries come first, as Office writes them.
	e := ox.New("Default", "Extension", ext, "ContentType", contentType)
	at := 0
	for i, c := range p.ctDoc.Root.Children {
		if c.Kind == ox.Element && c.Name == "Default" {
			at = i + 1
		}
	}
	p.ctDoc.Root.Insert(at, e)
}

func (p *Package) syncOverride(name, contentType string) {
	for _, e := range p.ctDoc.Root.Elements("Override") {
		if strings.TrimPrefix(e.Attr("PartName"), "/") == name {
			e.SetAttr("ContentType", contentType)
			return
		}
	}
	p.ctDoc.Root.Append(ox.New("Override", "PartName", "/"+name, "ContentType", contentType))
}

// Delete removes a part, its relationships part and its content type.
func (p *Package) Delete(name string) {
	name = strings.TrimPrefix(name, "/")
	if _, ok := p.parts[name]; !ok {
		return
	}
	delete(p.parts, name)
	p.order = slices.DeleteFunc(p.order, func(n string) bool { return n == name })
	if _, ok := p.override[name]; ok {
		delete(p.override, name)
		for _, e := range p.ctDoc.Root.Elements("Override") {
			if strings.TrimPrefix(e.Attr("PartName"), "/") == name {
				e.Remove()
				break
			}
		}
	}
	if rels := relsName(name); p.parts[rels] != nil {
		p.Delete(rels)
	}
}

// Doc parses a part's XML.
func (p *Package) Doc(name string) (*ox.Doc, error) {
	part := p.Part(name)
	if part == nil {
		return nil, fmt.Errorf("the part %s is missing", name)
	}
	d, err := ox.Parse(part.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}

// PutDoc writes parsed XML back to a part.
func (p *Package) PutDoc(name string, d *ox.Doc) { p.SetData(name, d.Bytes()) }

// relsName is the relationships part of a part; "" is the package's own.
func relsName(name string) string {
	if name == "" {
		return "_rels/.rels"
	}
	return path.Join(path.Dir(name), "_rels", path.Base(name)+".rels")
}

// Rels are the relationships of a part, or of the package itself for "".
func (p *Package) Rels(name string) ([]Rel, error) {
	rp := p.parts[relsName(name)]
	if rp == nil {
		return nil, nil
	}
	d, err := ox.Parse(rp.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rp.Name, err)
	}
	var out []Rel
	for _, e := range d.Root.Elements("Relationship") {
		out = append(out, Rel{ID: e.Attr("Id"), Type: e.Attr("Type"), Target: e.Attr("Target"), External: e.Attr("TargetMode") == "External"})
	}
	return out, nil
}

// Resolve is the part a relationship of the part from points to.
func Resolve(from, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(path.Clean(target), "/")
	}
	return path.Join(path.Dir(from), target)
}

// RelOfType is the first relationship of the type.
func RelOfType(rels []Rel, relType string) (Rel, bool) {
	for _, r := range rels {
		if r.Type == relType || sameRelType(r.Type, relType) {
			return r, true
		}
	}
	return Rel{}, false
}

// sameRelType matches a type against its ISO strict form.
func sameRelType(a, b string) bool {
	const strict = "http://purl.oclc.org/ooxml/officeDocument/relationships/"
	return strings.Replace(a, strict, DocRel, 1) == strings.Replace(b, strict, DocRel, 1)
}

// RelByID finds a relationship.
func RelByID(rels []Rel, id string) (Rel, bool) {
	for _, r := range rels {
		if r.ID == id {
			return r, true
		}
	}
	return Rel{}, false
}

// AddRel adds a relationship of the part (or of the package, for "") with a new id, and returns the id. target is
// relative to the part unless external.
func (p *Package) AddRel(name, relType, target string, external bool) string {
	relsPart := relsName(name)
	var d *ox.Doc
	if rp := p.parts[relsPart]; rp != nil {
		parsed, err := ox.Parse(rp.Data)
		if err == nil {
			d = parsed
		}
	}
	if d == nil {
		d = &ox.Doc{Root: ox.New("Relationships", "xmlns", RelNS)}
	}
	used := map[string]bool{}
	for _, e := range d.Root.Elements("Relationship") {
		used[e.Attr("Id")] = true
	}
	id := ""
	for i := len(used) + 1; ; i++ {
		id = "rId" + strconv.Itoa(i)
		if !used[id] {
			break
		}
	}
	e := ox.New("Relationship", "Id", id, "Type", relType, "Target", target)
	if external {
		e.SetAttr("TargetMode", "External")
	}
	d.Root.Append(e)
	p.Put(relsPart, "application/vnd.openxmlformats-package.relationships+xml", d.Bytes())
	return id
}

// DeleteRel removes a relationship by id.
func (p *Package) DeleteRel(name, id string) {
	relsPart := relsName(name)
	rp := p.parts[relsPart]
	if rp == nil {
		return
	}
	d, err := ox.Parse(rp.Data)
	if err != nil {
		return
	}
	for _, e := range d.Root.Elements("Relationship") {
		if e.Attr("Id") == id {
			e.Remove()
			break
		}
	}
	p.SetData(relsPart, d.Bytes())
}

// RelativeTarget is how a relationship of the part from names the part to.
func RelativeTarget(from, to string) string {
	dir := path.Dir(from)
	if dir == "." {
		return to
	}
	fromParts, toParts := strings.Split(dir, "/"), strings.Split(to, "/")
	i := 0
	for i < len(fromParts) && i < len(toParts)-1 && fromParts[i] == toParts[i] {
		i++
	}
	return strings.Repeat("../", len(fromParts)-i) + strings.Join(toParts[i:], "/")
}

// UniqueName is a part name not in use, from a pattern with a %d in it, counting from 1.
func (p *Package) UniqueName(pattern string) string {
	for i := 1; ; i++ {
		name := fmt.Sprintf(pattern, i)
		if p.parts[name] == nil {
			return name
		}
	}
}

// AddImage stores a picture and returns its part name, reusing a part with the same bytes under dir.
func (p *Package) AddImage(dir, ext, contentType string, data []byte) string {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	prefix := strings.TrimSuffix(dir, "/") + "/"
	for _, n := range p.order {
		if strings.HasPrefix(n, prefix) && bytes.Equal(p.parts[n].Data, data) {
			return n
		}
	}
	p.SetDefault(ext, contentType)
	name := p.UniqueName(prefix + "image%d." + ext)
	p.Put(name, contentType, data)
	return name
}

// Bytes writes the package. [Content_Types].xml goes first, as Office wants it; pictures and other parts already
// compressed are stored as they were. The order is otherwise the file's, so diffs between saves stay small, and the
// result is the same bytes for the same content: no time goes in that was not there.
func (p *Package) Bytes() ([]byte, error) {
	p.parts["[Content_Types].xml"].Data = p.ctDoc.Bytes()
	names := slices.Clone(p.order)
	sort.SliceStable(names, func(i, j int) bool { return rank(names[i]) < rank(names[j]) })
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		part := p.parts[n]
		method := part.method
		if method != zip.Store && method != zip.Deflate {
			method = zip.Deflate
		}
		h := &zip.FileHeader{Name: n, Method: method, Modified: part.stamp}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(part.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func rank(name string) int {
	switch {
	case name == "[Content_Types].xml":
		return 0
	case name == "_rels/.rels":
		return 1
	}
	return 2
}
