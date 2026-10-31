package pptx

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Clip is shapes cut or copied: their XML, and the package they came from, frozen, so that a paste into another
// presentation can still bring their pictures, charts and other parts after the first has changed or closed.
type Clip struct {
	from  *Doc
	pkg   *opc.Package
	part  string
	rels  []opc.Rel
	items []clipItem
}

type clipItem struct {
	node  *ox.Node
	box   Box
	ph    *Ph
	baked *ox.Node // the shape as a plain one, for pasting where its placeholder does not fit
}

// Empty reports that the clip holds nothing.
func (c *Clip) Empty() bool { return c == nil || len(c.items) == 0 }

// Copy takes the shapes as they are.
func (d *Doc) Copy(s *Slide, ids []int) *Clip {
	d.flush()
	c := &Clip{from: d, pkg: d.Pkg.Clone(), part: s.Part, rels: s.rels}
	x := d.slideScope(s)
	Walk(d.Shapes(s), func(sh *Shape) {
		for _, id := range ids {
			if sh.ID != id {
				continue
			}
			it := clipItem{node: element(sh).Clone(), box: sh.Box, ph: sh.Ph}
			if sh.Parent != nil {
				// Out of its group, it is placed on the slide itself.
				k := *sh
				k.Node, k.Alt, k.Parent = it.node, nil, nil
				writeBox(&k, sh.Box)
			}
			if sh.Ph != nil {
				it.baked = d.bake(sh, x, s.Layout)
			}
			c.items = append(c.items, it)
		}
	})
	return c
}

// bake is a placeholder as a shape in its own right: where it is and how its words are set no longer come from a
// layout.
func (d *Doc) bake(sh *Shape, x *scope, l *Layout) *ox.Node {
	n := sh.Node.Clone()
	if ph := n.Path("p:nvSpPr", "p:nvPr", "p:ph"); ph != nil {
		ph.Remove()
	}
	pr := n.Child("p:spPr")
	if pr == nil {
		return n
	}
	if pr.Child("a:xfrm") == nil {
		setXfrm(pr.Ensure("a:xfrm", spPrOrder), sh.Box)
	}
	if pr.Child("a:prstGeom") == nil && pr.Child("a:custGeom") == nil {
		pr.InsertOrdered(newElement(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`), spPrOrder)
	}
	tx := n.Child("p:txBody")
	if tx == nil {
		return n
	}
	lists := d.textChain(sh, x, l)
	for _, p := range tx.Elements("a:p") {
		level := min(max(int(attrInt(p.Child("a:pPr"), "lvl", 0)), 0), 8)
		st := styleOf(lists, level)
		if st.Align != "" && p.Child("a:pPr").Attr("algn") == "" {
			ensurePPr(p).SetAttr("algn", st.Align)
		}
		for _, r := range p.Elements("a:r", "a:fld", "a:br") {
			rp := ensureRPr(r)
			if _, ok := rp.Lookup("sz"); !ok {
				rp.SetAttr("sz", strconv.Itoa(int(st.Run.Size*100+0.5)))
			}
			if st.Run.Bold {
				if _, ok := rp.Lookup("b"); !ok {
					rp.SetAttr("b", "1")
				}
			}
			if st.Run.HasColor && fillChild(rp) == nil {
				rp.InsertOrdered(solidXML(st.Run.Color), rPrOrder)
			}
		}
	}
	return n
}

// Paste adds the clip's shapes to the slide, moved by (dx, dy), and returns their new ids.
func (d *Doc) Paste(s *Slide, c *Clip, dx, dy float64) ([]int, error) {
	if c.Empty() {
		return nil, errors.New("there is nothing to paste")
	}
	var out []int
	err := d.Edit("", []*Slide{s}, false, func() error {
		// What the slide's own placeholders already fill.
		taken := map[string]bool{}
		for _, sh := range d.Shapes(s) {
			if sh.Ph != nil {
				taken[sh.Ph.Type+"/"+strconv.Itoa(sh.Ph.Idx)] = true
			}
		}
		rels := map[string]string{}
		next := nextShapeID(s)
		for _, it := range c.items {
			n := it.node
			useBaked := it.ph != nil && (taken[it.ph.Type+"/"+strconv.Itoa(it.ph.Idx)] || findPh(s.Layout.shapes, it.ph, false) == nil)
			if useBaked && it.baked != nil {
				n = it.baked
			}
			n = n.Clone()
			if !useBaked && it.ph != nil {
				taken[it.ph.Type+"/"+strconv.Itoa(it.ph.Idx)] = true
			}
			ids := map[string]string{}
			n.Walk(func(k *ox.Node) bool {
				if k.Name == "p:cNvPr" {
					ids[k.Attr("id")] = strconv.Itoa(next)
					k.SetAttr("id", strconv.Itoa(next))
					next++
				}
				return true
			})
			// Connectors inside the pasted shapes stay joined to their copies, and others let go.
			n.Walk(func(k *ox.Node) bool {
				if k.Name == "a:stCxn" || k.Name == "a:endCxn" {
					if v, ok := ids[k.Attr("id")]; ok {
						k.SetAttr("id", v)
					} else {
						k.Remove()
					}
				}
				return true
			})
			if err := d.bringRels(s, c, n, rels); err != nil {
				return err
			}
			moveNode(n, dx, dy)
			insertShape(s, n)
			top := n
			if n.Name == "mc:AlternateContent" {
				if b := altBranch(n); b != nil {
					top = b
				}
			}
			for _, k := range top.Elements() {
				if len(k.Elements()) > 0 && strings.HasPrefix(k.Name, "p:nv") {
					if id, err := strconv.Atoi(k.Elements()[0].Attr("id")); err == nil {
						out = append(out, id)
					}
				}
			}
		}
		return nil
	})
	return out, err
}

// moveNode shifts a top-level element's position.
func moveNode(n *ox.Node, dx, dy float64) {
	var move func(e *ox.Node)
	move = func(e *ox.Node) {
		if e.Name == "mc:AlternateContent" {
			for _, b := range e.Elements() {
				for _, k := range b.Elements() {
					move(k)
				}
			}
			return
		}
		var xf *ox.Node
		switch e.Name {
		case "p:graphicFrame":
			xf = e.Child("p:xfrm")
		case "p:grpSp":
			xf = e.Path("p:grpSpPr", "a:xfrm")
		default:
			xf = e.Path("p:spPr", "a:xfrm")
		}
		if off := xf.Child("a:off"); off != nil {
			off.SetAttr("x", strconv.FormatInt(int64(attrFloat(off, "x", 0))+emu(dx), 10))
			off.SetAttr("y", strconv.FormatInt(int64(attrFloat(off, "y", 0))+emu(dy), 10))
		}
	}
	move(n)
}

// bringRels gives the slide what the pasted element's r: attributes point at, and rewrites them.
func (d *Doc) bringRels(s *Slide, c *Clip, n *ox.Node, done map[string]string) error {
	var err error
	n.Walk(func(k *ox.Node) bool {
		for i := range k.Attrs {
			at := &k.Attrs[i]
			if !strings.HasPrefix(at.Name, "r:") || at.Value == "" {
				continue
			}
			if v, ok := done[at.Value]; ok {
				at.Value = v
				continue
			}
			r, ok := opc.RelByID(c.rels, at.Value)
			if !ok {
				continue
			}
			nid := ""
			if r.External {
				nid = d.addRel(s, r.Type, r.Target, true)
			} else {
				src := opc.Resolve(c.part, r.Target)
				target := src
				if c.from != d || r.Type != opc.RelImage {
					target, err = d.copyPart(c.pkg, src, map[string]string{}, 0)
					if err != nil {
						return false
					}
				}
				nid = d.addRel(s, r.Type, opc.RelativeTarget(s.Part, target), false)
			}
			done[at.Value] = nid
			at.Value = nid
		}
		return err == nil
	})
	return err
}

// copyPart brings a part and what it depends on into this package, and returns its name there.
func (d *Doc) copyPart(from *opc.Package, name string, seen map[string]string, depth int) (string, error) {
	if v, ok := seen[name]; ok {
		return v, nil
	}
	if depth > 16 {
		return "", errors.New("the parts of what is pasted go too deep")
	}
	part := from.Part(name)
	if part == nil {
		return "", fmt.Errorf("the part %s is missing", name)
	}
	if strings.HasPrefix(part.ContentType, "image/") {
		out := d.Pkg.AddImage(path.Dir(name), path.Ext(name), part.ContentType, part.Data)
		d.pics.add(out, part.Data)
		seen[name] = out
		return out, nil
	}
	out := d.Pkg.UniqueName(numberPattern(name))
	seen[name] = out
	d.Pkg.Put(out, part.ContentType, part.Data)
	d.pics.add(out, part.Data)
	rels, err := from.Rels(name)
	if err != nil {
		return "", err
	}
	for _, r := range rels {
		target := r.Target
		if !r.External {
			switch r.Type {
			case relSlide, relLayout, relMaster, relNotes, relNotesMaster:
				continue // a part of the presentation, not of what was copied
			}
			t, err := d.copyPart(from, opc.Resolve(name, r.Target), seen, depth+1)
			if err != nil {
				return "", err
			}
			target = opc.RelativeTarget(out, t)
		}
		d.putRel(out, r, target)
	}
	if data := d.relsData(out); data != nil {
		d.pics.add(relsPart(out), data)
	}
	return out, nil
}

// putRel writes a relationship keeping its id, as the part's own XML names it.
func (d *Doc) putRel(name string, r opc.Rel, target string) {
	var doc *ox.Doc
	if data := d.relsData(name); data != nil {
		doc, _ = ox.Parse(data)
	}
	if doc == nil {
		doc = &ox.Doc{Root: ox.New("Relationships", "xmlns", opc.RelNS)}
	}
	e := ox.New("Relationship", "Id", r.ID, "Type", r.Type, "Target", target)
	if r.External {
		e.SetAttr("TargetMode", "External")
	}
	doc.Root.Append(e)
	d.setRelsData(name, doc.Bytes())
}

// DuplicateSlide adds a copy of the slide after it, with its own notes and its own charts and other parts.
func (d *Doc) DuplicateSlide(s *Slide) (*Slide, error) {
	var out *Slide
	at := d.Number(s)
	err := d.Edit("", nil, true, func() error {
		d.flush()
		part := d.Pkg.UniqueName("ppt/slides/slide%d.xml")
		data := s.tree().Clone().Bytes()
		d.Pkg.Put(part, ctSlide, data)
		out = &Slide{Part: part, Layout: s.Layout, data: data}
		seen := map[string]string{}
		for _, r := range s.rels {
			target := r.Target
			if !r.External {
				src := opc.Resolve(s.Part, r.Target)
				switch r.Type {
				case relLayout, opc.RelImage, relSlide:
				case relNotes:
					t, err := d.copyNotes(src, part)
					if err != nil {
						return err
					}
					target = opc.RelativeTarget(part, t)
					out.notesPart = t
				default:
					t, err := d.copyPart(d.Pkg, src, seen, 0)
					if err != nil {
						return err
					}
					target = opc.RelativeTarget(part, t)
				}
			}
			d.putRel(part, r, target)
		}
		out.rels, _ = d.Pkg.Rels(part)
		d.register(out, at)
		d.touch(out)
		return nil
	})
	return out, err
}

func (d *Doc) copyNotes(src, slidePart string) (string, error) {
	part := d.Pkg.Part(src)
	if part == nil {
		return "", fmt.Errorf("the part %s is missing", src)
	}
	out := d.Pkg.UniqueName("ppt/notesSlides/notesSlide%d.xml")
	d.Pkg.Put(out, ctNotes, part.Data)
	rels, err := d.Pkg.Rels(src)
	if err != nil {
		return "", err
	}
	for _, r := range rels {
		target := r.Target
		if r.Type == relSlide {
			target = opc.RelativeTarget(out, slidePart)
		}
		d.putRel(out, r, target)
	}
	return out, nil
}
