package ui

import (
	"maps"
	"math"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// sweep is the right button marking items, as the dual-pane file managers of Windows mark them: pressing an item
// flips its mark, and moving the pointer on with the button held gives every item from the pressed one to the one
// under the pointer that same mark, so a sweep marks a run, or unmarks it when it began on a marked item. Items the
// pointer comes back from get their marks back. Holding the button still on the item opens its menu instead.
type sweep struct {
	pane *filesPane
	from string          // the item pressed, by name, as the rows can change under it when the folder is read again
	to   int             // the row the sweep reaches
	mark bool            // the mark the swept items get
	was  map[string]bool // the marks before the press
	x, y float32         // where the button was pressed
	// moved is set once the pointer left the press, after which resting no longer opens the menu.
	moved  bool
	began  time.Time
	ticked time.Time // the frame before, for scrolling past the list's edge at a pace the frame rate does not change
}

// sweepHold is how long the right button rests on an item before its menu opens.
const sweepHold = 500 * time.Millisecond

// rowOf is the row showing the entry with that name, or -1.
func (p *filesPane) rowOf(name string) int {
	for i, r := range p.rows {
		if p.entries[r].Name == name {
			return i
		}
	}
	return -1
}

// startSweep begins a sweep on row of list i, where the right button was just pressed.
func (a *App) startSweep(fd *filesDoc, i int, p *filesPane, row int) {
	u := &a.u
	at := u.rightAt
	u.rightAt = nil
	if fd.pick != nil {
		return // its marks and menu are the file manager's
	}
	a.focusPane(fd, i)
	name := p.entries[p.rows[row]].Name
	now := time.Now()
	fd.sweep = &sweep{pane: p, from: name, to: row, mark: !p.marked[name], was: maps.Clone(p.marked),
		x: at.X, y: at.Y, began: now, ticked: now}
	p.cursor = row
	fd.sweep.apply()
}

// sweeping is the sweep running on p, or nil; it ends the sweep once the button is let go or its item is gone.
func (fd *filesDoc) sweeping(u *ctx, p *filesPane) *sweep {
	s := fd.sweep
	if s == nil || s.pane != p {
		return nil
	}
	if !u.rightDown || p.rowOf(s.from) < 0 {
		fd.sweep = nil
		return nil
	}
	return s
}

// edgeScroll is how far, in items, a list scrolls this frame while the pointer is beyond its edge by beyond items:
// eight items a second, and three more for each item beyond; it is negative above the list.
func (s *sweep) edgeScroll(beyond float32) float32 {
	now := time.Now()
	dt := float32(min(now.Sub(s.ticked).Seconds(), 0.1))
	s.ticked = now
	if beyond == 0 {
		return 0
	}
	speed := 8 + 3*float32(math.Abs(float64(beyond)))
	if beyond < 0 {
		speed = -speed
	}
	return dt * speed
}

// sweepTo carries the sweep to row, or keeps where it reached for -1, and opens the item's menu once the button
// has rested on it.
func (a *App) sweepTo(fd *filesDoc, p *filesPane, s *sweep, row int) {
	u := &a.u
	if dx, dy := u.mx-s.x, u.my-s.y; dx*dx+dy*dy > u.px(4)*u.px(4) {
		s.moved = true
	}
	if row >= 0 && row != s.to {
		s.to, s.moved = row, true
		p.cursor = row
		s.apply()
	}
	if !s.moved && time.Since(s.began) >= sweepHold {
		p.marked = s.was // the press asked for the menu, not a mark
		p.cursor = p.rowOf(s.from)
		fd.sweep = nil
		a.fileMenu(fd, gfx.Rect{X: s.x, Y: s.y, W: 1, H: 1})
	}
}

// apply marks the items from the pressed one to the one reached, and gives the others their marks from before.
func (s *sweep) apply() {
	p := s.pane
	from := p.rowOf(s.from)
	s.to = min(max(s.to, 0), len(p.rows)-1)
	p.marked = maps.Clone(s.was)
	for j := min(from, s.to); j <= max(from, s.to); j++ {
		if name := p.entries[p.rows[j]].Name; s.mark {
			p.marked[name] = true
		} else {
			delete(p.marked, name)
		}
	}
}
