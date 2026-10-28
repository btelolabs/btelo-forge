package files

import (
	"context"
	"os"
	"path/filepath"
)

// Trashed is an item moved to the Recycle Bin (the Trash), and what is needed to put it back. An item the bin took
// but deleted for good, being larger than the bin holds, has no Stored.
type Trashed struct {
	Original string // where it was
	Stored   string // where the bin keeps it
	Info     string // the bin's note of where it came from, deleted when it is put back
}

// TrashAll moves paths to the Recycle Bin one by one, reporting each. An item the bin cannot take, on a network or
// removable drive, fails rather than being deleted for good.
func TrashAll(ctx context.Context, paths []string, p *Progress) ([]Trashed, Result) {
	var out []Trashed
	var res Result
	p.Total.Store(int64(len(paths)))
	for _, path := range paths {
		if ctx.Err() != nil {
			break
		}
		p.at(path)
		t, err := Trash(path)
		if err != nil {
			res.Failed = append(res.Failed, Failure{path, err})
			continue
		}
		out = append(out, t)
		res.Pairs = append(res.Pairs, Pair{From: path})
		p.Done.Add(1)
	}
	return out, res
}

// Restore puts an item back from the Recycle Bin where it was, under a free name when that one has been taken
// since; it returns where the item is now.
func Restore(t Trashed) (string, error) {
	dir := filepath.Dir(t.Original)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	to := filepath.Join(dir, UniqueName(dir, filepath.Base(t.Original)))
	if err := os.Rename(t.Stored, to); err != nil {
		return "", err
	}
	if t.Info != "" {
		os.Remove(t.Info)
	}
	return to, nil
}
