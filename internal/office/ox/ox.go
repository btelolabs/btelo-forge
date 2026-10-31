// Package ox is an XML tree that keeps what it reads: prefixes, namespace declarations, attribute order, comments,
// processing instructions and unknown elements all come back out as they went in. Office files are full of markup
// no editor knows every part of, so the formats here change the elements they understand in this tree and leave the
// rest alone, which is what keeps a file Word, Excel or PowerPoint wrote intact when it is saved here.
//
// Names are as written, prefix included ("w:p"): OOXML producers use the standard prefixes, and Read rewrites a
// document that binds a known namespace to another prefix (see Canon) so that code can match on the usual names.
package ox

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Attr is one attribute, in the order it was written.
type Attr struct {
	Name  string
	Value string
}

// Kind is what a node is.
type Kind uint8

const (
	Element Kind = iota
	Text
	Comment
	ProcInst // Name is the target, Value the rest
	CData
)

// Node is one node of the tree.
type Node struct {
	Kind     Kind
	Name     string // an element's name with its prefix, or a processing instruction's target
	Attrs    []Attr
	Children []*Node
	Value    string // a text, comment or instruction's content
	Parent   *Node
}

// Doc is a parsed part: what comes before and after the root element, and the root.
type Doc struct {
	Decl   string // the <?xml …?> declaration's content; empty when there was none
	Before []*Node
	Root   *Node
	After  []*Node
}

// New is an element with attributes given as name, value pairs.
func New(name string, attrs ...string) *Node {
	n := &Node{Name: name}
	for i := 0; i+1 < len(attrs); i += 2 {
		n.Attrs = append(n.Attrs, Attr{attrs[i], attrs[i+1]})
	}
	return n
}

// NewText is a text node.
func NewText(s string) *Node { return &Node{Kind: Text, Value: s} }

// maxDepth bounds how deep elements may nest: everything that walks the tree recurses, and a stack cannot be recovered
// from. No real document comes near it.
const maxDepth = 4096

// Parse reads a document. It takes the token stream raw, so that nothing is resolved or normalised on the way
// through, then canonicalises unusual prefixes.
func Parse(data []byte) (*Doc, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "us-ascii") {
			return input, nil
		}
		return nil, fmt.Errorf("the XML is in %s, not UTF-8", charset)
	}
	d := &Doc{}
	var stack []*Node
	add := func(n *Node) {
		if len(stack) > 0 {
			top := stack[len(stack)-1]
			n.Parent = top
			top.Children = append(top.Children, n)
		} else if d.Root == nil {
			d.Before = append(d.Before, n)
		} else {
			d.After = append(d.After, n)
		}
	}
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &Node{Name: rawName(t.Name)}
			for _, at := range t.Attr {
				n.Attrs = append(n.Attrs, Attr{rawName(at.Name), at.Value})
			}
			if len(stack) == 0 {
				if d.Root != nil {
					return nil, fmt.Errorf("a second root element <%s>", n.Name)
				}
				d.Root = n
			} else {
				add(n)
			}
			stack = append(stack, n)
			if len(stack) > maxDepth {
				return nil, fmt.Errorf("the XML nests deeper than %d elements", maxDepth)
			}
		case xml.EndElement:
			name := rawName(t.Name)
			if len(stack) == 0 || stack[len(stack)-1].Name != name {
				return nil, fmt.Errorf("a stray </%s>", name)
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				continue // white space around the root
			}
			add(&Node{Kind: Text, Value: string(t)})
		case xml.Comment:
			add(&Node{Kind: Comment, Value: string(t)})
		case xml.ProcInst:
			if t.Target == "xml" {
				d.Decl = string(t.Inst)
				continue
			}
			add(&Node{Kind: ProcInst, Name: t.Target, Value: string(t.Inst)})
		case xml.Directive:
			return nil, fmt.Errorf("a document type declaration is not allowed in an Office part")
		}
	}
	if len(stack) > 0 {
		return nil, fmt.Errorf("<%s> is not closed", stack[len(stack)-1].Name)
	}
	if d.Root == nil {
		return nil, fmt.Errorf("no root element")
	}
	canon(d.Root)
	return d, nil
}

func rawName(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// Bytes writes the document. Text and attribute values are escaped, nothing else changes.
func (d *Doc) Bytes() []byte {
	var b bytes.Buffer
	b.Grow(4096)
	decl := d.Decl
	if decl == "" {
		decl = `version="1.0" encoding="UTF-8" standalone="yes"`
	}
	b.WriteString("<?xml " + decl + "?>\n")
	for _, n := range d.Before {
		write(&b, n)
	}
	write(&b, d.Root)
	for _, n := range d.After {
		write(&b, n)
	}
	return b.Bytes()
}

// Clone copies the document.
func (d *Doc) Clone() *Doc {
	c := &Doc{Decl: d.Decl, Root: d.Root.Clone()}
	for _, n := range d.Before {
		c.Before = append(c.Before, n.Clone())
	}
	for _, n := range d.After {
		c.After = append(c.After, n.Clone())
	}
	return c
}

func write(b *bytes.Buffer, n *Node) {
	switch n.Kind {
	case Text:
		escape(b, n.Value, false)
	case CData:
		b.WriteString("<![CDATA[" + n.Value + "]]>")
	case Comment:
		b.WriteString("<!--" + n.Value + "-->")
	case ProcInst:
		b.WriteString("<?" + n.Name + " " + n.Value + "?>")
	case Element:
		b.WriteByte('<')
		b.WriteString(n.Name)
		for _, a := range n.Attrs {
			b.WriteByte(' ')
			b.WriteString(a.Name)
			b.WriteString(`="`)
			escape(b, a.Value, true)
			b.WriteByte('"')
		}
		if len(n.Children) == 0 {
			b.WriteString("/>")
			return
		}
		b.WriteByte('>')
		for _, c := range n.Children {
			write(b, c)
		}
		b.WriteString("</")
		b.WriteString(n.Name)
		b.WriteByte('>')
	}
}

// escape writes s with what XML needs escaped. Control characters XML 1.0 cannot hold are dropped: Office refuses a
// file containing one, and they can reach here by pasting.
func escape(b *bytes.Buffer, s string, attr bool) {
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"' && attr:
			b.WriteString("&quot;")
		case r == '\n' && attr:
			b.WriteString("&#10;")
		case r == '\t' && attr:
			b.WriteString("&#9;")
		case r == '\r':
			b.WriteString("&#13;")
		case r == '\t', r == '\n':
			b.WriteRune(r)
		case r < 0x20, r == 0xfffe, r == 0xffff:
		default:
			b.WriteRune(r)
		}
	}
}

// Attr is the value of the attribute name, empty when there is none.
func (n *Node) Attr(name string) string {
	v, _ := n.Lookup(name)
	return v
}

// Lookup is the value of the attribute name and whether it is there.
func (n *Node) Lookup(name string) (string, bool) {
	if n == nil {
		return "", false
	}
	for i := range n.Attrs {
		if n.Attrs[i].Name == name {
			return n.Attrs[i].Value, true
		}
	}
	return "", false
}

// SetAttr sets the attribute, in its place when it is there, at the end when it is not.
func (n *Node) SetAttr(name, value string) {
	for i := range n.Attrs {
		if n.Attrs[i].Name == name {
			n.Attrs[i].Value = value
			return
		}
	}
	n.Attrs = append(n.Attrs, Attr{name, value})
}

// DelAttr removes the attribute.
func (n *Node) DelAttr(name string) {
	for i := range n.Attrs {
		if n.Attrs[i].Name == name {
			n.Attrs = append(n.Attrs[:i], n.Attrs[i+1:]...)
			return
		}
	}
}

// Elements are the child elements, of the given names when any are given.
func (n *Node) Elements(names ...string) []*Node {
	if n == nil {
		return nil
	}
	var out []*Node
	for _, c := range n.Children {
		if c.Kind != Element {
			continue
		}
		if len(names) == 0 {
			out = append(out, c)
			continue
		}
		for _, name := range names {
			if c.Name == name {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Child is the first child element called name, or nil. It takes a nil node, so chains read as paths.
func (n *Node) Child(name string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Kind == Element && c.Name == name {
			return c
		}
	}
	return nil
}

// Path follows child elements by name, nil when one is missing.
func (n *Node) Path(names ...string) *Node {
	for _, name := range names {
		n = n.Child(name)
	}
	return n
}

// Walk calls fn for the element and every element under it, parents first; it goes no deeper where fn is false.
func (n *Node) Walk(fn func(*Node) bool) {
	if n == nil || n.Kind != Element || !fn(n) {
		return
	}
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

// All are the elements called name at any depth under n.
func (n *Node) All(name string) []*Node {
	var out []*Node
	n.Walk(func(e *Node) bool {
		if e != n && e.Name == name {
			out = append(out, e)
		}
		return true
	})
	return out
}

// InnerText is the text directly and indirectly in the element.
func (n *Node) InnerText() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	n.innerText(&b)
	return b.String()
}

func (n *Node) innerText(b *strings.Builder) {
	for _, c := range n.Children {
		switch c.Kind {
		case Text, CData:
			b.WriteString(c.Value)
		case Element:
			c.innerText(b)
		}
	}
}

// SetText makes the element's content the one text.
func (n *Node) SetText(s string) {
	n.Children = n.Children[:0]
	n.Append(NewText(s))
}

// Append adds children at the end.
func (n *Node) Append(cs ...*Node) *Node {
	for _, c := range cs {
		c.Parent = n
		n.Children = append(n.Children, c)
	}
	return n
}

// Index is the place of c among n's children, -1 when it is not one.
func (n *Node) Index(c *Node) int {
	for i, k := range n.Children {
		if k == c {
			return i
		}
	}
	return -1
}

// Insert puts c among the children at i.
func (n *Node) Insert(i int, c *Node) {
	c.Parent = n
	n.Children = append(n.Children, nil)
	copy(n.Children[i+1:], n.Children[i:])
	n.Children[i] = c
}

// Remove takes the node out of its parent.
func (n *Node) Remove() {
	p := n.Parent
	if p == nil {
		return
	}
	if i := p.Index(n); i >= 0 {
		p.Children = append(p.Children[:i], p.Children[i+1:]...)
	}
	n.Parent = nil
}

// Replace puts r where n is.
func (n *Node) Replace(r *Node) {
	p := n.Parent
	if p == nil {
		return
	}
	if i := p.Index(n); i >= 0 {
		p.Children[i] = r
		r.Parent = p
		n.Parent = nil
	}
}

// Clone copies the node and everything under it; the copy has no parent.
func (n *Node) Clone() *Node {
	c := &Node{Kind: n.Kind, Name: n.Name, Value: n.Value}
	if n.Attrs != nil {
		c.Attrs = append([]Attr(nil), n.Attrs...)
	}
	if n.Children != nil {
		c.Children = make([]*Node, len(n.Children))
		for i, k := range n.Children {
			k2 := k.Clone()
			k2.Parent = c
			c.Children[i] = k2
		}
	}
	return c
}

// Ensure is the first child called name, added in schema order when missing: order lists the element's children in
// the order the schema requires, and Office rejects a file whose children are out of order, so the new child goes
// before the first existing child that comes later in that list.
func (n *Node) Ensure(name string, order []string) *Node {
	if c := n.Child(name); c != nil {
		return c
	}
	c := New(name)
	n.InsertOrdered(c, order)
	return c
}

// InsertOrdered adds c among the children where order puts its name; see Ensure.
func (n *Node) InsertOrdered(c *Node, order []string) {
	rank := -1
	for i, name := range order {
		if name == c.Name {
			rank = i
			break
		}
	}
	if rank >= 0 {
		for i, k := range n.Children {
			if k.Kind != Element {
				continue
			}
			for j := rank + 1; j < len(order); j++ {
				if k.Name == order[j] {
					n.Insert(i, c)
					return
				}
			}
		}
	}
	n.Append(c)
}

// knownPrefixes maps each namespace the formats read to the prefix their code uses.
var knownPrefixes = map[string]string{
	"http://schemas.openxmlformats.org/wordprocessingml/2006/main":           "w",
	"http://schemas.openxmlformats.org/officeDocument/2006/relationships":    "r",
	"http://schemas.openxmlformats.org/drawingml/2006/main":                  "a",
	"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing": "wp",
	"http://schemas.openxmlformats.org/drawingml/2006/picture":               "pic",
	"http://schemas.openxmlformats.org/drawingml/2006/chart":                 "c",
	"http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing":    "xdr",
	"http://purl.oclc.org/ooxml/drawingml/chart":                             "c",
	"http://purl.oclc.org/ooxml/drawingml/spreadsheetDrawing":                "xdr",
	"http://schemas.openxmlformats.org/presentationml/2006/main":             "p",
	"http://schemas.openxmlformats.org/markup-compatibility/2006":            "mc",
	"http://purl.oclc.org/ooxml/wordprocessingml/main":                       "w",
	"http://purl.oclc.org/ooxml/officeDocument/relationships":                "r",
	"http://purl.oclc.org/ooxml/drawingml/main":                              "a",
	"http://purl.oclc.org/ooxml/presentationml/main":                         "p",
}

// spreadsheetNS is SpreadsheetML; a workbook's parts normally have it as the default namespace.
const spreadsheetNS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"

// canon renames elements and attributes bound to a known namespace under an unusual prefix to the usual prefix, and
// declares the usual prefix where the old one was declared. Default-namespace markup in a Word or PowerPoint part
// (which some generators write) gets the prefix too. A spreadsheet in a prefixed form (<x:worksheet>) loses it, as
// SpreadsheetML's code reads bare names. The tree it leaves is equivalent XML.
func canon(root *Node) {
	var rename func(n *Node, scope map[string]string)
	rename = func(n *Node, scope map[string]string) {
		changed := false
		next := scope
		for _, a := range n.Attrs {
			if a.Name != "xmlns" && !strings.HasPrefix(a.Name, "xmlns:") {
				continue
			}
			if !changed {
				next = make(map[string]string, len(scope)+2)
				for k, v := range scope {
					next[k] = v
				}
				changed = true
			}
			prefix := strings.TrimPrefix(strings.TrimPrefix(a.Name, "xmlns"), ":")
			next[prefix] = a.Value
		}
		target := func(prefix string) (string, bool) {
			uri, ok := next[prefix]
			if !ok {
				return "", false
			}
			if uri == spreadsheetNS {
				return "", prefix != ""
			}
			want, ok := knownPrefixes[uri]
			if !ok || want == prefix {
				return "", false
			}
			return want, true
		}
		if want, ok := target(prefixOf(n.Name)); ok {
			n.Name = join(want, localOf(n.Name))
		}
		for i := range n.Attrs {
			name := n.Attrs[i].Name
			if name == "xmlns" || strings.HasPrefix(name, "xmlns:") {
				prefix := strings.TrimPrefix(strings.TrimPrefix(name, "xmlns"), ":")
				uri := n.Attrs[i].Value
				if uri == spreadsheetNS && prefix != "" {
					n.Attrs[i].Name = "xmlns"
				} else if want, ok := knownPrefixes[uri]; ok && want != prefix {
					n.Attrs[i].Name = "xmlns:" + want
				}
				continue
			}
			if p := prefixOf(name); p != "" {
				if want, ok := target(p); ok {
					n.Attrs[i].Name = join(want, localOf(name))
				}
			}
		}
		for _, c := range n.Children {
			if c.Kind == Element {
				rename(c, next)
			}
		}
	}
	rename(root, map[string]string{})
}

func prefixOf(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i]
	}
	return ""
}

func localOf(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func join(prefix, local string) string {
	if prefix == "" {
		return local
	}
	return prefix + ":" + local
}
