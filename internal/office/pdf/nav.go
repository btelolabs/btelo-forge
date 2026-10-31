package pdf

import "strings"

// Links, the outline and destinations.

// OutlineItem is a bookmark.
type OutlineItem struct {
	Title string
	Page  int     // -1 when it leads nowhere
	Ref   Ref     // the target page's object
	Top   float64 // where on the page, in user space; 0 for the top of the page
	HasY  bool
	Kids  []OutlineItem
}

// Link is a link annotation's target.
type Link struct {
	Rect Rect
	Page int // -1 for an address
	Top  float64
	HasY bool
	URI  string
}

// resolveNamed turns a named destination into its target.
func (d *Doc) resolveNamed(o Object) (Object, bool) {
	var name string
	switch v := d.Resolve(o).(type) {
	case Name:
		name = string(v)
	case String:
		name = string(v)
	default:
		return nil, false
	}
	cat := d.Catalog()
	if dests := d.Dict(cat["Dests"]); dests != nil {
		if v, ok := dests[Name(name)]; ok {
			return v, true
		}
	}
	return d.nameTree(d.Dict(cat["Names"])["Dests"], name, 0, map[int]bool{})
}

func (d *Doc) nameTree(o Object, key string, depth int, seen map[int]bool) (Object, bool) {
	if r, ok := o.(Ref); ok {
		if seen[r.Num] {
			return nil, false
		}
		seen[r.Num] = true
	}
	node := d.Dict(o)
	if node == nil || depth > 32 {
		return nil, false
	}
	names := d.Array(node["Names"])
	for i := 0; i+1 < len(names); i += 2 {
		if s, ok := d.Resolve(names[i]).(String); ok && string(s) == key {
			return names[i+1], true
		}
	}
	for _, k := range d.Array(node["Kids"]) {
		if lim := d.Array(d.Dict(k)["Limits"]); len(lim) == 2 {
			lo, _ := d.Resolve(lim[0]).(String)
			hi, _ := d.Resolve(lim[1]).(String)
			if key < string(lo) || key > string(hi) {
				continue
			}
		}
		if v, ok := d.nameTree(k, key, depth+1, seen); ok {
			return v, true
		}
	}
	return nil, false
}

// target reads a destination.
func (d *Doc) target(o Object, pageIndex map[int]int) (page int, ref Ref, top float64, hasY bool) {
	page = -1
	if v, ok := d.resolveNamed(o); ok {
		o = v
	}
	dest := d.Resolve(o)
	if dict, ok := dest.(Dict); ok {
		dest = d.Resolve(dict["D"])
	}
	a, ok := dest.(Array)
	if !ok || len(a) == 0 {
		return
	}
	switch v := a[0].(type) {
	case Ref:
		ref = v
		if i, ok := pageIndex[v.Num]; ok {
			page = i
		}
	case int64: // a page number, in a remote file's form
		page = int(v)
	}
	if len(a) >= 2 {
		switch d.Name(a[1]) {
		case "XYZ":
			if len(a) >= 4 {
				top, hasY = Num(d.Resolve(a[3]))
			}
		case "FitH", "FitBH":
			if len(a) >= 3 {
				top, hasY = Num(d.Resolve(a[2]))
			}
		case "FitR":
			if len(a) >= 6 {
				top, hasY = Num(d.Resolve(a[5]))
			}
		}
	}
	return
}

func (d *Doc) pageIndex() map[int]int {
	out := map[int]int{}
	for i, p := range d.Pages() {
		out[p.Ref.Num] = i
	}
	return out
}

// Outline reads the bookmarks.
func (d *Doc) Outline() []OutlineItem {
	root := d.Dict(d.Catalog()["Outlines"])
	if root == nil {
		return nil
	}
	seen := map[int]bool{}
	total := 0
	index := d.pageIndex()
	var walk func(o Object, depth int) []OutlineItem
	walk = func(o Object, depth int) []OutlineItem {
		var out []OutlineItem
		for total < 1<<17 && depth < 32 {
			r, ok := o.(Ref)
			if !ok || seen[r.Num] {
				break
			}
			seen[r.Num] = true
			total++
			item := d.Dict(o)
			if item == nil {
				break
			}
			it := OutlineItem{Page: -1}
			if s, ok := d.Resolve(item["Title"]).(String); ok {
				it.Title = strings.TrimSpace(TextString(s))
			}
			if v, ok := item["Dest"]; ok {
				it.Page, it.Ref, it.Top, it.HasY = d.target(v, index)
			} else if a := d.Dict(item["A"]); a != nil && d.Name(a["S"]) == "GoTo" {
				it.Page, it.Ref, it.Top, it.HasY = d.target(a["D"], index)
			}
			it.Kids = walk(item["First"], depth+1)
			out = append(out, it)
			o = item["Next"]
		}
		return out
	}
	return walk(root["First"], 0)
}

// Links reads the links of a page.
func (d *Doc) Links(index int) []Link {
	pages := d.Pages()
	if index < 0 || index >= len(pages) {
		return nil
	}
	var out []Link
	var idx map[int]int
	for _, a := range d.Array(pages[index].Dict["Annots"]) {
		dict := d.Dict(a)
		if dict == nil || d.Name(dict["Subtype"]) != "Link" {
			continue
		}
		rect, ok := d.RectOf(dict["Rect"])
		if !ok {
			continue
		}
		if idx == nil {
			idx = d.pageIndex()
		}
		l := Link{Rect: rect, Page: -1}
		if act := d.Dict(dict["A"]); act != nil {
			switch d.Name(act["S"]) {
			case "URI":
				if s, ok := d.Resolve(act["URI"]).(String); ok {
					l.URI = string(s)
				}
			case "GoTo":
				l.Page, _, l.Top, l.HasY = d.target(act["D"], idx)
			}
		} else if v, ok := dict["Dest"]; ok {
			l.Page, _, l.Top, l.HasY = d.target(v, idx)
		}
		if l.URI != "" || l.Page >= 0 {
			out = append(out, l)
		}
	}
	return out
}
