package project

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
)

const historyLimit = 200

// History is the undo and redo stacks of snapshots.
type History struct {
	past, future []*Project
}

// Commit records before as the state an undo returns to; it clears the redo stack.
func (h *History) Commit(before *Project) {
	h.past = append(h.past, before.Clone())
	if len(h.past) > historyLimit {
		h.past = h.past[len(h.past)-historyLimit:]
	}
	h.future = h.future[:0]
}

// Undo returns the previous state, or nil when there is none.
func (h *History) Undo(current *Project) *Project {
	if len(h.past) == 0 {
		return nil
	}
	prev := h.past[len(h.past)-1]
	h.past = h.past[:len(h.past)-1]
	h.future = append(h.future, current.Clone())
	return prev
}

// Redo returns the state undone last, or nil.
func (h *History) Redo(current *Project) *Project {
	if len(h.future) == 0 {
		return nil
	}
	next := h.future[len(h.future)-1]
	h.future = h.future[:len(h.future)-1]
	h.past = append(h.past, current.Clone())
	return next
}

func (h *History) CanUndo() bool { return len(h.past) > 0 }
func (h *History) CanRedo() bool { return len(h.future) > 0 }

// saveVersion is bumped whenever the saved layout changes. Version 2 had no opacity and is read as fully opaque;
// older versions are discarded, since they only hold an unfinished edit that the source video can always restart.
const saveVersion = 3

type saved struct {
	Version int      `json:"version"`
	Project *Project `json:"project"`
}

// SavePath is where the edit of source is remembered inside dir.
func SavePath(dir, source string) string {
	sum := sha1.Sum([]byte(filepath.Clean(source)))
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".json")
}

// Save writes p atomically.
func Save(path string, p *Project) error {
	data, err := json.Marshal(saved{Version: saveVersion, Project: p})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads a remembered edit whose first source is src, and checks it still fits; it returns an error when there is
// nothing usable.
func Load(path string, src Source) (*Project, error) {
	p, err := ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !p.Fits(src) {
		return nil, ErrSourceChanged
	}
	p.Sources[0] = src
	return p, nil
}

// ErrSourceChanged means the video an edit starts from is no longer the one it was made from.
var ErrSourceChanged = errors.New("the source video has changed")

// Fits reports whether src, as probed now, is still the video p's first source was.
func (p *Project) Fits(src Source) bool {
	first := p.Sources[0]
	return first.Path == src.Path && math.Abs(first.Duration-src.Duration) <= 0.05 && first.Width == src.Width && first.Height == src.Height
}

// solid makes every clip and overlay fully opaque, as edits were before opacity existed.
func (p *Project) solid() {
	for k := range p.Video {
		for i := range p.Video[k].Clips {
			p.Video[k].Clips[i].Opacity = 1
		}
	}
	for k := range p.Overlays {
		for i := range p.Overlays[k].Items {
			p.Overlays[k].Items[i].Opacity = 1
		}
	}
}

func (p *Project) validate() error {
	if len(p.Video) == 0 || len(p.Video[0].Clips) == 0 || len(p.Overlays) == 0 || len(p.Music) == 0 {
		return errors.New("missing tracks")
	}
	if !p.Export.Valid() {
		return errors.New("unknown export settings")
	}
	if p.Canvas.Width <= 0 || p.Canvas.Height <= 0 || p.Canvas.FPS <= 0 {
		return errors.New("invalid canvas")
	}
	ids := map[int]bool{}
	id := func(n int) error {
		if n <= 0 || n >= p.NextID || ids[n] {
			return errors.New("invalid ids")
		}
		ids[n] = true
		return nil
	}
	for k, t := range p.Video {
		for i, c := range t.Clips {
			if c.Source < 0 || c.Source >= len(p.Sources) || c.Speed < Speeds[0] || c.Speed > Speeds[len(Speeds)-1] ||
				c.In < 0 || c.Out > p.Sources[c.Source].Duration+0.05 || c.Out-c.In <= 0 || c.Opacity < 0 || c.Opacity > 1 {
				return errors.New("a clip is out of range")
			}
			if k > 0 && i > 0 && c.Start < t.Clips[i-1].End()-1e-6 {
				return errors.New("overlapping clips")
			}
			if err := id(c.ID); err != nil {
				return err
			}
		}
	}
	for _, t := range p.Music {
		for _, m := range t.Clips {
			if m.Length <= 0 || m.Offset < 0 || m.Start < 0 {
				return errors.New("invalid music clip")
			}
			if err := id(m.ID); err != nil {
				return err
			}
		}
	}
	for _, t := range p.Overlays {
		for _, o := range t.Items {
			if o.End <= o.Start || o.Kind == ImageOverlay && (o.Image == "" || o.Aspect <= 0) || o.Opacity < 0 || o.Opacity > 1 {
				return errors.New("invalid overlay")
			}
			if err := id(o.ID); err != nil {
				return err
			}
		}
	}
	p.layout()
	return nil
}
