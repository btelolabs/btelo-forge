package pdf

// Page is a page of the document with what it inherits filled in.
type Page struct {
	Ref   Ref
	Dict  Dict // Resources, MediaBox, CropBox and Rotate are always this page's, inherited or not
	Index int
}

// PageCount is how many pages there are.
func (d *Doc) PageCount() int { return len(d.Pages()) }

// Pages lists the pages in order. Its result must not be changed.
func (d *Doc) Pages() []Page {
	d.mu.RLock()
	if d.pagesValid {
		p := d.pages
		d.mu.RUnlock()
		return p
	}
	d.mu.RUnlock()
	var out []Page
	seen := map[int]bool{}
	root := d.Catalog()
	if root != nil {
		d.walkPages(root["Pages"], Dict{}, seen, 0, &out)
	}
	if len(out) == 0 {
		// A broken tree: every page object there is, in object order.
		for _, n := range d.objectNumbers() {
			if pg, ok := d.Object(n).(Dict); ok && pg["Type"] == Name("Page") {
				out = append(out, d.pageOf(Ref{n, d.gen(n)}, pg, Dict{}, len(out)))
				if len(out) >= maxPages {
					break
				}
			}
		}
	}
	d.mu.Lock()
	d.pages, d.pagesValid = out, true
	d.mu.Unlock()
	return out
}

func (d *Doc) gen(num int) int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if e, ok := d.xref[num]; ok && !e.packed {
		return e.gen
	}
	return 0
}

var inherited = []Name{"Resources", "MediaBox", "CropBox", "Rotate"}

func (d *Doc) walkPages(node Object, inh Dict, seen map[int]bool, depth int, out *[]Page) {
	if depth > 256 || len(*out) >= maxPages {
		return
	}
	var ref Ref
	if r, ok := node.(Ref); ok {
		if seen[r.Num] {
			return
		}
		seen[r.Num] = true
		ref = r
	}
	dict := d.Dict(node)
	if dict == nil {
		return
	}
	kids := d.Array(dict["Kids"])
	if d.Name(dict["Type"]) == "Page" || kids == nil && dict["Contents"] != nil || kids == nil && dict["Type"] == nil {
		*out = append(*out, d.pageOf(ref, dict, inh, len(*out)))
		return
	}
	next := inh.Clone()
	for _, k := range inherited {
		if v, ok := dict[k]; ok {
			next[k] = v
		}
	}
	for _, k := range kids {
		d.walkPages(k, next, seen, depth+1, out)
	}
}

func (d *Doc) pageOf(ref Ref, dict, inh Dict, index int) Page {
	p := Page{Ref: ref, Index: index}
	p.Dict = dict.Clone()
	for _, k := range inherited {
		if _, ok := p.Dict[k]; !ok {
			if v, ok := inh[k]; ok {
				p.Dict[k] = v
			}
		}
	}
	return p
}

// Letter is the size of a page that states none.
var Letter = Rect{0, 0, 612, 792}

// MediaBox is the page's media box.
func (d *Doc) MediaBox(p Page) Rect {
	r, ok := d.RectOf(p.Dict["MediaBox"])
	if !ok || r.W() < 1 || r.H() < 1 || r.W() > 1e6 || r.H() > 1e6 {
		return Letter
	}
	return r
}

// CropBox is the visible part of the page.
func (d *Doc) CropBox(p Page) Rect {
	m := d.MediaBox(p)
	c, ok := d.RectOf(p.Dict["CropBox"])
	if !ok {
		return m
	}
	c = Rect{max(c.X0, m.X0), max(c.Y0, m.Y0), min(c.X1, m.X1), min(c.Y1, m.Y1)}
	if c.W() < 1 || c.H() < 1 {
		return m
	}
	return c
}

// Rotation is the page's own rotation: 0, 90, 180 or 270.
func (d *Doc) Rotation(p Page) int {
	r := d.Int(p.Dict["Rotate"], 0) % 360
	if r < 0 {
		r += 360
	}
	return r / 90 * 90
}

// UserUnit is how many points a unit of the page is.
func (d *Doc) UserUnit(p Page) float64 {
	u := d.Float(p.Dict["UserUnit"], 1)
	if u <= 0 || u > 75000 {
		return 1
	}
	return u
}

// ViewSize is the page's size in points as it is shown, turned by view degrees on top of its own rotation.
func (d *Doc) ViewSize(p Page, view int) (w, h float64) {
	c := d.CropBox(p)
	u := d.UserUnit(p)
	w, h = c.W()*u, c.H()*u
	if (d.Rotation(p)+view)%180 != 0 {
		w, h = h, w
	}
	return w, h
}

// ViewMatrix maps the page's default user space to pixels with the origin at the top left of the page as shown.
func (d *Doc) ViewMatrix(p Page, scale float64, view int) Matrix {
	c := d.CropBox(p)
	s := scale * d.UserUnit(p)
	w, h := c.W()*s, c.H()*s
	base := Matrix{s, 0, 0, -s, -c.X0 * s, c.Y1 * s}
	switch ((d.Rotation(p)+view)%360 + 360) % 360 {
	case 90:
		return base.Mul(Matrix{0, 1, -1, 0, h, 0})
	case 180:
		return base.Mul(Matrix{-1, 0, 0, -1, w, h})
	case 270:
		return base.Mul(Matrix{0, -1, 1, 0, 0, w})
	}
	return base
}
