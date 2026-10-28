package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// ActionKind is what a file operation that can be undone did.
type ActionKind int

const (
	Renamed ActionKind = iota
	Moved
	Copied
	Recycled
)

// Action is a file operation that can be undone.
type Action struct {
	Kind    ActionKind
	Pairs   []Pair    // renamed, moved or copied: each item from where to where
	Trashed []Trashed // recycled: each item and where the bin keeps it
}

// ErrOccupied is a place to put an item back that something else has taken since.
var ErrOccupied = errors.New("something else is there now")

// Undo reverses an action: what was renamed or moved goes back, copies are deleted, recycled items are put back.
// It returns the items put back or deleted, and those that could not be.
func Undo(ctx context.Context, a Action, p *Progress) Result {
	var res Result
	switch a.Kind {
	case Copied:
		var copies []string
		for _, pair := range a.Pairs {
			copies = append(copies, pair.To)
		}
		return Remove(ctx, copies, p)
	case Recycled:
		p.Total.Store(int64(len(a.Trashed)))
		for _, t := range a.Trashed {
			if t.Stored == "" {
				continue
			}
			p.at(t.Original)
			to, err := Restore(t)
			if err != nil {
				res.Failed = append(res.Failed, Failure{t.Original, err})
				continue
			}
			res.Pairs = append(res.Pairs, Pair{t.Stored, to})
			p.Done.Add(1)
		}
		return res
	}
	p.Total.Store(int64(len(a.Pairs)))
	for i := len(a.Pairs) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			break
		}
		pair := a.Pairs[i]
		p.at(pair.To)
		if err := putBack(ctx, pair.To, pair.From); err != nil {
			res.Failed = append(res.Failed, Failure{pair.To, err})
			continue
		}
		res.Pairs = append(res.Pairs, Pair{pair.To, pair.From})
		p.Done.Add(1)
	}
	return res
}

// putBack moves an item from where it went to where it was, copying it there when that is on another drive.
func putBack(ctx context.Context, from, to string) error {
	if _, err := os.Lstat(to); err == nil && !SamePath(from, to) {
		return ErrOccupied
	}
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err == nil { // its folder had gone
		return nil
	}
	if err := copyItem(ctx, from, to, &Progress{}); err != nil {
		os.RemoveAll(to)
		return err
	}
	return Remove(context.WithoutCancel(ctx), []string{from}, &Progress{}).Err()
}
