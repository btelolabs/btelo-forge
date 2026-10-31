package pdf

import (
	"errors"
	"fmt"
	"image"
	"slices"
)

// Work on whole pages. The page tree is flattened to one level whenever its order changes: pages take what they
// inherited with them, and what points at a page keeps pointing at the same object, so links, the outline and named
// destinations stay right by themselves. Only a page that goes away needs them fixed.

// RotatePages turns pages clockwise by a multiple of 90 degrees.
func (d *Doc) RotatePages(pages []int, degrees int) error {
	if degrees%90 != 0 {
		return errors.New("pdf: pages turn by multiples of 90 degrees")
	}
	return d.Do(func() error {
		for _, i := range pages {
			ref, dict, err := d.pageObject(i)
			if err != nil {
				return err
			}
			dict["Rotate"] = int64(((d.Rotation(d.Pages()[i])+degrees)%360 + 360) % 360)
			d.SetRef(ref, dict)
		}
		return nil
	})
}

// setPageOrder rewrites the tree so its root lists order, a list of page references.
func (d *Doc) setPageOrder(order []Ref) error {
	root := d.Catalog()
	rootRef, ok := root["Pages"].(Ref)
	if !ok {
		rootRef = d.Add(Dict{"Type": Name("Pages")})
		cat := root.Clone()
		cat["Pages"] = rootRef
		d.SetRef(d.trailer["Root"].(Ref), cat)
	}
	byNum := map[int]Page{}
	for _, p := range d.Pages() {
		byNum[p.Ref.Num] = p
	}
	kids := make(Array, 0, len(order))
	for _, r := range order {
		dict := d.Dict(r)
		if dict == nil {
			return fmt.Errorf("pdf: page object %d is missing", r.Num)
		}
		c := dict.Clone()
		if p, ok := byNum[r.Num]; ok {
			for _, k := range inherited {
				if v, have := p.Dict[k]; have {
					c[k] = v
				}
			}
		}
		c["Parent"] = rootRef
		d.SetRef(r, c)
		kids = append(kids, r)
	}
	node := Dict{"Type": Name("Pages"), "Kids": kids, "Count": int64(len(kids))}
	d.SetRef(rootRef, node)
	return nil
}

func (d *Doc) pageRefs() []Ref {
	pages := d.Pages()
	out := make([]Ref, len(pages))
	for i, p := range pages {
		out[i] = p.Ref
	}
	return out
}

// MovePage puts a page at index to, counted after it was taken out.
func (d *Doc) MovePage(from, to int) error {
	return d.MovePages([]int{from}, to)
}

// MovePages puts pages, in document order, before the page now at index to of the list without them.
func (d *Doc) MovePages(pages []int, to int) error {
	refs := d.pageRefs()
	moving := map[int]bool{}
	for _, i := range pages {
		if i < 0 || i >= len(refs) {
			return fmt.Errorf("pdf: no page %d", i+1)
		}
		moving[i] = true
	}
	var rest, taken []Ref
	for i, r := range refs {
		if moving[i] {
			taken = append(taken, r)
		} else {
			rest = append(rest, r)
		}
	}
	to = min(max(to, 0), len(rest))
	order := slices.Concat(rest[:to], taken, rest[to:])
	return d.Do(func() error { return d.setPageOrder(order) })
}

// DeletePages removes pages; the next save is a clean rewrite so what they held leaves the file.
func (d *Doc) DeletePages(pages []int) error {
	refs := d.pageRefs()
	gone := map[int]bool{}
	var keep []Ref
	for i, r := range refs {
		if slices.Contains(pages, i) {
			gone[r.Num] = true
		} else {
			keep = append(keep, r)
		}
	}
	if len(keep) == 0 {
		return errors.New("pdf: a document keeps at least one page")
	}
	return d.Do(func() error {
		if err := d.dropDestinations(gone); err != nil {
			return err
		}
		d.markStructural()
		return d.setPageOrder(keep)
	})
}

// InsertBlank adds an empty page of the given size before index at.
func (d *Doc) InsertBlank(at int, w, h float64) error {
	return d.Do(func() error {
		ref := d.Add(Dict{"Type": Name("Page"), "MediaBox": Reals(0, 0, w, h), "Resources": Dict{}})
		return d.insertRefs(at, []Ref{ref})
	})
}

func (d *Doc) insertRefs(at int, refs []Ref) error {
	order := d.pageRefs()
	at = min(max(at, 0), len(order))
	return d.setPageOrder(slices.Concat(order[:at], refs, order[at:]))
}

// InsertImagePage adds a page exactly the size of a picture, at 72 points to the inch up to the width of A4.
func (d *Doc) InsertImagePage(at int, img image.Image, jpegData []byte) error {
	return d.Do(func() error {
		ref, w, h, err := d.AddImageObject(img, jpegData)
		if err != nil {
			return err
		}
		pw, ph := float64(w), float64(h)
		if pw > 595 {
			ph, pw = ph*595/pw, 595
		}
		ops := fmt.Sprintf("q %s 0 0 %s 0 0 cm /Im1 Do Q\n", fmtNum(pw), fmtNum(ph))
		content := d.Add(NewStream(Dict{"Filter": Name("FlateDecode")}, Deflate([]byte(ops))))
		page := d.Add(Dict{"Type": Name("Page"), "MediaBox": Reals(0, 0, pw, ph), "Contents": content,
			"Resources": Dict{"XObject": Dict{"Im1": ref}}})
		return d.insertRefs(at, []Ref{page})
	})
}

// dropDestinations removes what points at pages going away: outline items lose their target, named destinations and
// link annotations go.
func (d *Doc) dropDestinations(gone map[int]bool) error {
	dead := func(o Object) bool {
		dest := d.Resolve(o)
		if dict, ok := dest.(Dict); ok {
			dest = d.Resolve(dict["D"])
		}
		if a, ok := dest.(Array); ok && len(a) > 0 {
			if r, ok := a[0].(Ref); ok {
				return gone[r.Num]
			}
		}
		return false
	}
	targetDead := func(item Dict) bool {
		if v, ok := item["Dest"]; ok {
			if p, ok := d.resolveNamed(v); ok {
				v = p
			}
			if dead(v) {
				return true
			}
		}
		if a := d.Dict(item["A"]); a != nil && d.Name(a["S"]) == "GoTo" {
			v := a["D"]
			if p, ok := d.resolveNamed(v); ok {
				v = p
			}
			return dead(v)
		}
		return false
	}
	// The outline.
	seen := map[int]bool{}
	var walk func(o Object, depth int)
	walk = func(o Object, depth int) {
		for steps := 0; steps < 1<<16 && depth < 64; steps++ {
			r, ok := o.(Ref)
			if !ok || seen[r.Num] {
				return
			}
			seen[r.Num] = true
			item := d.Dict(o)
			if item == nil {
				return
			}
			if targetDead(item) {
				c := item.Clone()
				delete(c, "Dest")
				delete(c, "A")
				d.SetRef(r, c)
			}
			walk(item["First"], depth+1)
			o = item["Next"]
		}
	}
	cat := d.Catalog()
	if out := d.Dict(cat["Outlines"]); out != nil {
		walk(out["First"], 0)
	}
	// Named destinations, in both of the places they may live.
	if dests := d.Dict(cat["Dests"]); dests != nil {
		c := dests.Clone()
		changed := false
		for k, v := range c {
			if dead(v) {
				delete(c, k)
				changed = true
			}
		}
		if changed {
			if r, ok := cat["Dests"].(Ref); ok {
				d.SetRef(r, c)
			} else {
				nc := cat.Clone()
				nc["Dests"] = c
				d.SetRef(d.trailer["Root"].(Ref), nc)
			}
		}
	}
	seenTree := map[int]bool{}
	var tree func(o Object, depth int)
	tree = func(o Object, depth int) {
		r, isRef := o.(Ref)
		if isRef {
			if seenTree[r.Num] {
				return
			}
			seenTree[r.Num] = true
		}
		node := d.Dict(o)
		if node == nil || depth > 32 {
			return
		}
		if names := d.Array(node["Names"]); names != nil && isRef {
			var kept Array
			for i := 0; i+1 < len(names); i += 2 {
				if !dead(names[i+1]) {
					kept = append(kept, names[i], names[i+1])
				}
			}
			if len(kept) != len(names) {
				c := node.Clone()
				c["Names"] = kept
				d.SetRef(r, c)
			}
		}
		for _, k := range d.Array(node["Kids"]) {
			tree(k, depth+1)
		}
	}
	tree(d.Dict(cat["Names"])["Dests"], 0)
	// Links on the pages that stay.
	for _, p := range d.Pages() {
		if gone[p.Ref.Num] {
			continue
		}
		annots := d.Array(p.Dict["Annots"])
		var kept Array
		for _, a := range annots {
			ad := d.Dict(a)
			if ad != nil && d.Name(ad["Subtype"]) == "Link" && targetDead(ad) {
				continue
			}
			kept = append(kept, a)
		}
		if len(kept) != len(annots) {
			dict := d.Dict(p.Ref).Clone()
			dict["Annots"] = kept
			d.SetRef(p.Ref, dict)
		}
	}
	return nil
}

// copier brings objects of another document into this one under new numbers.
type copier struct {
	to, from *Doc
	pages    map[int]Ref // the source's pages being copied, to their new objects
	done     map[int]Ref
}

func (c *copier) ref(r Ref) Object {
	if n, ok := c.pages[r.Num]; ok {
		return n
	}
	if n, ok := c.done[r.Num]; ok {
		return n
	}
	o := c.from.Object(r.Num)
	if dict, ok := o.(Dict); ok && c.from.Name(dict["Type"]) == "Page" {
		return nil // a page not coming along: what points at it points nowhere
	}
	n := c.to.Add(nil)
	c.done[r.Num] = n
	c.to.Set(n.Num, c.value(o, 0))
	return n
}

// value copies an object tree. A reference chain does not count as depth, as ref starts over at 0, but it recurses
// only as deep as the graph of objects is, and Add keeps a visited object from being walked twice.
func (c *copier) value(o Object, depth int) Object {
	if depth > maxDepth {
		return nil
	}
	switch v := o.(type) {
	case Ref:
		return c.ref(v)
	case Array:
		out := make(Array, len(v))
		for i, e := range v {
			out[i] = c.value(e, depth+1)
		}
		return out
	case Dict:
		out := make(Dict, len(v))
		for k, e := range v {
			if k == "StructParent" || k == "StructParents" {
				continue
			}
			if x := c.value(e, depth+1); x != nil {
				out[k] = x
			}
		}
		return out
	case *Stream:
		dict, _ := c.value(v.Dict, depth+1).(Dict)
		s := NewStream(dict, c.from.Raw(v))
		return s
	}
	return o
}

// Import copies pages of another document in before index at. Forms keep working, and internal links among the
// copied pages come along.
func (d *Doc) Import(src *Doc, pages []int, at int) error {
	return d.Do(func() error {
		from := src.Pages()
		c := &copier{to: d, from: src, pages: map[int]Ref{}, done: map[int]Ref{}}
		var fresh []Ref
		for _, i := range pages {
			if i < 0 || i >= len(from) {
				return fmt.Errorf("pdf: no page %d in the other document", i+1)
			}
			r := d.Add(nil)
			c.pages[from[i].Ref.Num] = r
			fresh = append(fresh, r)
		}
		for k, i := range pages {
			p := from[i]
			dict := Dict{}
			for key, v := range p.Dict {
				switch key {
				case "Parent", "B", "StructParents", "Thumb", "Metadata", "PieceInfo", "AA":
					continue
				case "Annots":
					dict[key] = c.annots(v)
					continue
				}
				if x := c.value(v, 0); x != nil {
					dict[key] = x
				}
			}
			dict["Type"] = Name("Page")
			d.SetRef(fresh[k], dict)
		}
		d.fixWidgetPages(fresh)
		c.adoptFields(pages)
		c.adoptOutline()
		return d.insertRefs(at, fresh)
	})
}

// annots copies a page's annotations, turning named destinations into explicit ones, as the names belong to the other
// document.
func (c *copier) annots(o Object) Object {
	src := c.from
	out := Array{}
	for _, a := range src.Array(o) {
		dict := src.Dict(a)
		if dict == nil {
			continue
		}
		if src.Name(dict["Subtype"]) == "Link" {
			dict = dict.Clone()
			if v, ok := dict["Dest"]; ok {
				if p, ok := src.resolveNamed(v); ok {
					dict["Dest"] = p
				}
			}
			if act := src.Dict(dict["A"]); act != nil && src.Name(act["S"]) == "GoTo" {
				if p, ok := src.resolveNamed(act["D"]); ok {
					ac := act.Clone()
					ac["D"] = p
					dict["A"] = ac
				}
			}
			delete(dict, "P")
			if x := c.value(dict, 0); x != nil {
				out = append(out, c.to.Add(x))
			}
			continue
		}
		if dict["P"] != nil {
			dict = dict.Clone()
			delete(dict, "P")
		}
		if r, ok := a.(Ref); ok {
			if _, have := c.done[r.Num]; !have {
				n := c.to.Add(nil)
				c.done[r.Num] = n
				c.to.Set(n.Num, c.value(dict, 0))
				out = append(out, n)
				continue
			}
			out = append(out, c.done[r.Num])
			continue
		}
		if x := c.value(dict, 0); x != nil {
			out = append(out, c.to.Add(x))
		}
	}
	return out
}

func (d *Doc) fixWidgetPages(pages []Ref) {
	for _, r := range pages {
		for _, a := range d.Array(d.Dict(r)["Annots"]) {
			ar, ok := a.(Ref)
			if !ok {
				continue
			}
			if dict := d.Dict(ar); dict != nil {
				c := dict.Clone()
				c["P"] = r
				d.SetRef(ar, c)
			}
		}
	}
}

// adoptFields adds the copied pages' form fields to this document's form, renaming any whose name is taken.
func (c *copier) adoptFields(pages []int) {
	d, src := c.to, c.from
	srcForm := src.acroForm()
	if srcForm == nil {
		return
	}
	cat := d.Catalog()
	form := Dict{}
	if f := d.acroForm(); f != nil {
		form = f.Clone()
	}
	fields := Array{}
	if f := d.Array(form["Fields"]); f != nil {
		fields = f.Clone()
	}
	names := map[string]bool{}
	for _, f := range d.Fields() {
		names[f.Name] = true
	}
	added := false
	for _, f := range src.Array(srcForm["Fields"]) {
		r, ok := f.(Ref)
		if !ok {
			continue
		}
		n, ok := c.done[r.Num]
		if !ok {
			continue // none of its widgets came along
		}
		dict := d.Dict(n)
		if dict == nil {
			continue
		}
		if t, ok := d.Resolve(dict["T"]).(String); ok {
			name := TextString(t)
			for i := 2; names[name]; i++ {
				name = fmt.Sprintf("%s (%d)", TextString(t), i)
			}
			names[name] = true
			cp := dict.Clone()
			cp["T"] = EncodeText(name)
			d.SetRef(n, cp)
		}
		fields = append(fields, n)
		added = true
	}
	if !added {
		return
	}
	form["Fields"] = fields
	// Fields draw with the fonts of the form's resources.
	if dr := src.Dict(srcForm["DR"]); dr != nil {
		merged := Dict{}
		if old := d.Dict(form["DR"]); old != nil {
			merged = old.Clone()
		}
		fonts := Dict{}
		if old := d.Dict(merged["Font"]); old != nil {
			fonts = old.Clone()
		}
		for k, v := range src.Dict(dr["Font"]) {
			if _, have := fonts[k]; !have {
				if x := c.value(v, 0); x != nil {
					fonts[k] = x
				}
			}
		}
		merged["Font"] = fonts
		form["DR"] = merged
	}
	if da, ok := srcForm["DA"]; ok {
		if _, have := form["DA"]; !have {
			form["DA"] = c.value(da, 0)
		}
	}
	if r, ok := cat["AcroForm"].(Ref); ok {
		d.SetRef(r, form)
		return
	}
	nc := cat.Clone()
	nc["AcroForm"] = d.Add(form)
	d.SetRef(d.trailer["Root"].(Ref), nc)
}

// adoptOutline hangs the other document's outline, for the copied pages, under this document's.
func (c *copier) adoptOutline() {
	d, src := c.to, c.from
	items := src.Outline()
	if len(items) == 0 {
		return
	}
	var keep func(list []OutlineItem) []OutlineItem
	keep = func(list []OutlineItem) []OutlineItem {
		var out []OutlineItem
		for _, it := range list {
			it.Kids = keep(it.Kids)
			if it.Ref.Num != 0 {
				if _, ok := c.pages[it.Ref.Num]; ok {
					out = append(out, it)
					continue
				}
			}
			if len(it.Kids) > 0 {
				it.Page = -1
				out = append(out, it)
			}
		}
		return out
	}
	items = keep(items)
	if len(items) == 0 {
		return
	}
	cat := d.Catalog()
	rootRef, ok := cat["Outlines"].(Ref)
	root := d.Dict(cat["Outlines"])
	if !ok || root == nil {
		root = Dict{"Type": Name("Outlines")}
		rootRef = d.Add(root)
		nc := cat.Clone()
		nc["Outlines"] = rootRef
		d.SetRef(d.trailer["Root"].(Ref), nc)
	}
	var build func(list []OutlineItem, parent Ref) (first, last Ref, count int)
	build = func(list []OutlineItem, parent Ref) (first, last Ref, count int) {
		refs := make([]Ref, len(list))
		for i := range list {
			refs[i] = d.Add(nil)
		}
		for i, it := range list {
			dict := Dict{"Title": EncodeText(it.Title), "Parent": parent}
			if it.Ref.Num != 0 {
				if n, ok := c.pages[it.Ref.Num]; ok {
					dict["Dest"] = Array{n, Name("XYZ"), nil, Real(it.Top), nil}
				}
			}
			if i > 0 {
				dict["Prev"] = refs[i-1]
			}
			if i+1 < len(list) {
				dict["Next"] = refs[i+1]
			}
			if len(it.Kids) > 0 {
				f, l, n := build(it.Kids, refs[i])
				dict["First"], dict["Last"], dict["Count"] = f, l, int64(n)
				count += n
			}
			d.SetRef(refs[i], dict)
			count++
		}
		return refs[0], refs[len(refs)-1], count
	}
	first, last, n := build(items, rootRef)
	r := d.Dict(rootRef).Clone()
	if lastOld, ok := r["Last"].(Ref); ok {
		prev := d.Dict(lastOld).Clone()
		prev["Next"] = first
		d.SetRef(lastOld, prev)
		f := d.Dict(first).Clone()
		f["Prev"] = lastOld
		d.SetRef(first, f)
	} else {
		r["First"] = first
	}
	r["Last"] = last
	r["Count"] = int64(d.Int(r["Count"], 0) + n)
	d.SetRef(rootRef, r)
}

// Blank makes a new document with one empty page.
func Blank(w, h float64) *Doc {
	d := &Doc{xref: map[int]xrefEntry{}, cache: map[int]Object{}, objStms: map[int]*objStm{}, trailer: Dict{}, over: map[int]Object{}, nextNum: 1, repaired: true}
	pages := d.Add(Dict{"Type": Name("Pages"), "Kids": Array{}, "Count": int64(0)})
	first := d.Add(Dict{"Type": Name("Page"), "Parent": pages, "MediaBox": Reals(0, 0, w, h), "Resources": Dict{}})
	d.Set(pages.Num, Dict{"Type": Name("Pages"), "Kids": Array{first}, "Count": int64(1)})
	d.trailer["Root"] = d.Add(Dict{"Type": Name("Catalog"), "Pages": pages})
	return d
}

// Extract makes a new document of the given pages.
func (d *Doc) Extract(pages []int) ([]byte, error) {
	if len(pages) == 0 {
		return nil, errors.New("pdf: no pages chosen")
	}
	out := Blank(612, 792)
	if err := out.Import(d, pages, 1); err != nil {
		return nil, err
	}
	if err := out.setPageOrder(out.pageRefs()[1:]); err != nil {
		return nil, err
	}
	return out.Snapshot().Rewrite(false)
}

// Merge brings in every page of src before index at.
func (d *Doc) Merge(src *Doc, at int) error {
	idx := make([]int, src.PageCount())
	for i := range idx {
		idx[i] = i
	}
	return d.Import(src, idx, at)
}
