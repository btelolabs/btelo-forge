package pptx

import (
	"errors"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// The history keeps, for each step, what the slides it touched held before it: their XML and relationships, and the
// presentation's list of slides when the step changed that. Undoing swaps the two states. It holds whole slides,
// which are small next to their pictures, and pictures never leave the package while the file is open.

// Bounds on the history: steps, and nodes kept all told.
const (
	maxSteps = 300
	maxNodes = 8 << 20
)

type slideState struct {
	s         *Slide
	doc       *ox.Doc
	rels      []byte
	relList   []opc.Rel
	layout    *Layout
	notesPart string
	notes     *ox.Doc
}

type step struct {
	key    string
	slides []slideState
	order  []*Slide
	pres   *ox.Doc
	rels   []byte
	nodes  int
}

type history struct {
	undo, redo []*step
	nodes      int
	open       bool // the last step may still take in more of the same key
	tainted    bool // parts may have lost their last reference
}

func countNodes(n *ox.Node) int {
	c := 1
	for _, k := range n.Children {
		c += countNodes(k)
	}
	return c
}

func (d *Doc) relsData(name string) []byte {
	if p := d.Pkg.Part(relsPart(name)); p != nil {
		return p.Data
	}
	return nil
}

func (d *Doc) saveSlide(st *step, s *Slide) {
	k := slideState{s: s, doc: s.tree().Clone(), rels: d.relsData(s.Part), relList: s.rels, layout: s.Layout, notesPart: s.notesPart}
	if n := d.notesTree(s); n != nil {
		k.notes = n.Clone()
	}
	st.nodes += countNodes(k.doc.Root)
	st.slides = append(st.slides, k)
}

func (d *Doc) capture(key string, slides []*Slide, pres bool) *step {
	st := &step{key: key}
	for _, s := range slides {
		d.saveSlide(st, s)
	}
	if pres {
		st.order = append([]*Slide(nil), d.Slides...)
		st.pres = d.Pres.Clone()
		st.rels = d.relsData(d.Part)
		st.nodes += countNodes(st.pres.Root)
	}
	return st
}

// Edit runs fn as one step of the history, having kept the slides it says it touches; pres says it changes the
// presentation's own part or the list of slides. A step with the same non-empty key as the last goes on being that
// step, which is how typing or a drag becomes one undo; EndBurst closes it.
func (d *Doc) Edit(key string, slides []*Slide, pres bool, fn func() error) error {
	h := &d.hist
	join := key != "" && h.open && len(h.undo) > 0 && h.undo[len(h.undo)-1].key == key
	var st *step
	if !join {
		st = d.capture(key, slides, pres)
	} else {
		last := h.undo[len(h.undo)-1]
		for _, s := range slides {
			known := false
			for i := range last.slides {
				known = known || last.slides[i].s == s
			}
			if !known {
				d.saveSlide(last, s)
			}
		}
	}
	if err := fn(); err != nil {
		if st != nil {
			d.restore(st)
		}
		return err
	}
	if st != nil {
		h.undo = append(h.undo, st)
		h.nodes += st.nodes
		h.redo = nil
		h.open = key != ""
		for len(h.undo) > 1 && (len(h.undo) > maxSteps || h.nodes > maxNodes) {
			h.nodes -= h.undo[0].nodes
			h.undo = h.undo[1:]
		}
	}
	for _, s := range slides {
		d.touch(s)
	}
	if pres {
		d.presDirty = true
		h.tainted = true
	}
	d.version++
	return nil
}

// touch marks a slide changed.
func (d *Doc) touch(s *Slide) {
	d.nextRev++
	s.Rev = d.nextRev
	s.dirty = true
}

// EndBurst says that the next edit is a step of its own, whatever its key.
func (d *Doc) EndBurst() { d.hist.open = false }

// CanUndo and CanRedo say whether there is a step to go to.
func (d *Doc) CanUndo() bool { return len(d.hist.undo) > 0 }
func (d *Doc) CanRedo() bool { return len(d.hist.redo) > 0 }

// ErrNoStep says the history has nothing that way.
var ErrNoStep = errors.New("nothing to undo")

// Undo goes back a step.
func (d *Doc) Undo() error { return d.swap(&d.hist.undo, &d.hist.redo) }

// Redo goes forward a step.
func (d *Doc) Redo() error { return d.swap(&d.hist.redo, &d.hist.undo) }

func (d *Doc) swap(from, to *[]*step) error {
	if len(*from) == 0 {
		return ErrNoStep
	}
	st := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	slides := make([]*Slide, len(st.slides))
	for i := range st.slides {
		slides[i] = st.slides[i].s
	}
	back := d.capture(st.key, slides, st.pres != nil)
	d.restore(st)
	*to = append(*to, back)
	d.hist.open = false
	d.hist.tainted = true
	d.version++
	return nil
}

func (d *Doc) restore(st *step) {
	for i := range st.slides {
		k := &st.slides[i]
		s := k.s
		s.doc, s.rels, s.Layout, s.notesPart, s.notes = k.doc, k.relList, k.layout, k.notesPart, k.notes
		s.notesDirty = s.notes != nil
		d.setRelsData(s.Part, k.rels)
		d.touch(s)
	}
	if st.pres != nil {
		d.Pres = st.pres
		d.Slides = st.order
		d.setRelsData(d.Part, st.rels)
		d.presRels, _ = d.Pkg.Rels(d.Part)
		d.presDirty = true
	}
}

func (d *Doc) setRelsData(name string, data []byte) {
	if data == nil {
		if d.Pkg.Part(relsPart(name)) != nil {
			d.Pkg.Delete(relsPart(name))
		}
		return
	}
	d.Pkg.Put(relsPart(name), "application/vnd.openxmlformats-package.relationships+xml", data)
}
