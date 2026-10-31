package pdf

// The edit overlay and its history. An edit is a run of Set and Add inside Do; undoing puts back what the overlay
// held for each object before that run.

const maxUndo = 256

type change struct {
	num int
	had bool
	old Object
}

type step struct {
	changes    []change
	seen       map[int]bool
	structural int
}

type history struct {
	undo, redo []*step
	cur        *step
}

// Version changes with each change to the document.
func (d *Doc) Version() uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.version
}

// Do runs one undoable edit; when fn fails, what it did is taken back.
func (d *Doc) Do(fn func() error) error {
	if d.hist.cur != nil {
		return fn() // inside another edit
	}
	s := &step{seen: map[int]bool{}}
	d.hist.cur = s
	err := fn()
	d.hist.cur = nil
	if err != nil {
		d.swap(s)
		return err
	}
	if len(s.changes) == 0 {
		return nil
	}
	d.hist.undo = append(d.hist.undo, s)
	if n := len(d.hist.undo) - maxUndo; n > 0 {
		d.hist.undo = d.hist.undo[n:]
	}
	d.hist.redo = nil
	return nil
}

// swap exchanges a step's objects with the overlay, which turns it into its own reverse.
func (d *Doc) swap(s *step) {
	d.mu.Lock()
	for i := len(s.changes) - 1; i >= 0; i-- {
		c := &s.changes[i]
		cur, had := d.over[c.num]
		if c.had {
			d.over[c.num] = c.old
		} else {
			delete(d.over, c.num)
		}
		c.old, c.had = cur, had
	}
	d.structural -= s.structural
	s.structural = -s.structural
	d.pagesValid = false
	d.version++
	d.mu.Unlock()
}

// CanUndo reports whether there is an edit to take back.
func (d *Doc) CanUndo() bool { return len(d.hist.undo) > 0 }

// CanRedo reports whether there is an edit to do again.
func (d *Doc) CanRedo() bool { return len(d.hist.redo) > 0 }

// Undo takes back the last edit.
func (d *Doc) Undo() bool {
	n := len(d.hist.undo)
	if n == 0 {
		return false
	}
	s := d.hist.undo[n-1]
	d.hist.undo = d.hist.undo[:n-1]
	d.swap(s)
	d.hist.redo = append(d.hist.redo, s)
	return true
}

// Redo does the last undone edit again.
func (d *Doc) Redo() bool {
	n := len(d.hist.redo)
	if n == 0 {
		return false
	}
	s := d.hist.redo[n-1]
	d.hist.redo = d.hist.redo[:n-1]
	d.swap(s)
	d.hist.undo = append(d.hist.undo, s)
	return true
}

// Set replaces an object.
func (d *Doc) Set(num int, o Object) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.hist.cur; s != nil && !s.seen[num] {
		s.seen[num] = true
		cur, had := d.over[num]
		s.changes = append(s.changes, change{num, had, cur})
	}
	d.over[num] = o
	d.pagesValid = false
	d.version++
}

// SetRef replaces the object r refers to.
func (d *Doc) SetRef(r Ref, o Object) { d.Set(r.Num, o) }

// Add stores a new object.
func (d *Doc) Add(o Object) Ref {
	d.mu.Lock()
	n := d.nextNum
	d.nextNum++
	d.mu.Unlock()
	d.Set(n, o)
	return Ref{n, 0}
}

// markStructural notes that this edit only takes effect in a clean rewrite.
func (d *Doc) markStructural() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.structural++
	if d.hist.cur != nil {
		d.hist.cur.structural++
	}
}

// NeedsRewrite reports that the next save rewrites the file, rather than appending to it.
func (d *Doc) NeedsRewrite() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.structural > 0 || d.repaired
}

// Modified reports whether anything differs from the file as read.
func (d *Doc) Modified() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.over) > 0
}
