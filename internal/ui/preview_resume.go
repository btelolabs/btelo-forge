package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// A video or sound previewed goes on, when it is previewed again, from where it was left, as a video player
// remembers: the places are kept by file, with the file's size and time so a file changed since starts over.

// playPosition is where a file previewed was left.
type playPosition struct {
	At   float64   `json:"at"`
	Size int64     `json:"size"`
	Mod  time.Time `json:"mod"`
	Seen time.Time `json:"seen"`
}

// maxPlayPositions is how many files' places are kept; the ones seen longest ago go first.
const maxPlayPositions = 500

// resumeEdge is how near the start or the end a place is not worth keeping: the file starts from the beginning.
const resumeEdge = 2.0

func (a *App) positionsPath() string { return filepath.Join(a.stateDir, "positions.json") }

// positionKey is how a file is known among the places kept: its clean path, in any case where names ignore case.
func positionKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		path = strings.ToLower(path)
	}
	return path
}

// playPositions are the places kept, read on first use.
func (a *App) playPositions() map[string]playPosition {
	if a.positions == nil {
		a.positions = map[string]playPosition{}
		data, err := os.ReadFile(a.positionsPath())
		if err == nil {
			err = json.Unmarshal(data, &a.positions)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			a.positions = map[string]playPosition{} // a damaged file only loses the places; it is written anew
		}
	}
	return a.positions
}

// resumeAt is where to start previewing path of length seconds: where it was left, or the start.
func (a *App) resumeAt(path string, length float64) float64 {
	pos, ok := a.playPositions()[positionKey(path)]
	if !ok {
		return 0
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() != pos.Size || !st.ModTime().Equal(pos.Mod) || pos.At >= length-resumeEdge {
		return 0
	}
	return pos.At
}

// rememberPosition keeps where the video or sound previewed is now, for the next time it is previewed.
func (a *App) rememberPosition() {
	p := &a.preview
	if p.path == "" || p.length <= 0 || p.proj == nil && !p.sound {
		return
	}
	at := a.previewPosition()
	all := a.playPositions()
	key := positionKey(p.path)
	old, had := all[key]
	if at < resumeEdge || at > p.length-resumeEdge {
		if !had {
			return
		}
		delete(all, key) // seen to the end, or hardly begun: the next time starts at the beginning
	} else {
		st, err := os.Stat(p.path)
		if err != nil {
			return
		}
		if had && old.At == at {
			return
		}
		all[key] = playPosition{At: at, Size: st.Size(), Mod: st.ModTime(), Seen: time.Now()}
	}
	if len(all) > maxPlayPositions {
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, func(x, y string) int { return all[x].Seen.Compare(all[y].Seen) })
		for _, k := range keys[:len(all)-maxPlayPositions] {
			delete(all, k)
		}
	}
	if err := writeJSONFile(a.positionsPath(), all); err != nil {
		a.toastOnly("files/positions", a.u.tf("Where the videos were left could not be saved: %v", err))
	}
}

// writeJSONFile writes v as JSON to path, whole or not at all.
func writeJSONFile(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".saving"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
