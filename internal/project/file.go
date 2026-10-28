package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// EditExt is the file name extension of an edit saved as a file of its own, to keep, share or open again.
const EditExt = ".btcut"

// ErrNotEdit means a file is not an edit this version can read.
var ErrNotEdit = errors.New("not a " + EditExt + " edit file")

// WriteFile saves p as an edit file at path, atomically; it is the same layout as a remembered edit.
func WriteFile(path string, p *Project) error { return Save(path, p) }

// ReadFile reads an edit file. Its videos are not checked here: they may have moved since it was saved, which only
// probing them can tell.
func ReadFile(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s saved
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotEdit, err)
	}
	if s.Version != saveVersion && s.Version != 2 || s.Project == nil {
		return nil, fmt.Errorf("%w (version %d)", ErrNotEdit, s.Version)
	}
	p := s.Project
	if s.Version == 2 {
		p.solid()
	}
	if len(p.Sources) == 0 {
		return nil, fmt.Errorf("%w: it names no video", ErrNotEdit)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotEdit, err)
	}
	return p, nil
}
