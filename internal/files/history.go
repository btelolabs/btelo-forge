package files

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// History is the folders visited, ranked as zoxide ranks them: by how often and how lately, so a few letters jump
// to the folder meant.
type History struct {
	Dirs map[string]*Visit `json:"dirs"`
}

// Visit is how often a folder was visited, and when last.
type Visit struct {
	Rank float64 `json:"rank"`
	Last int64   `json:"last"` // Unix seconds
}

// historyMax is the total of the ranks above which they all age, so old folders fade and the file stays small.
const historyMax = 10000

// Add notes a visit to a folder.
func (h *History) Add(dir string, now time.Time) {
	if h.Dirs == nil {
		h.Dirs = map[string]*Visit{}
	}
	dir = filepath.Clean(dir)
	key := h.key(dir)
	v := h.Dirs[key]
	if v == nil {
		v = &Visit{}
		h.Dirs[key] = v
	}
	v.Rank++
	v.Last = now.Unix()
	var sum float64
	for _, v := range h.Dirs {
		sum += v.Rank
	}
	if sum > historyMax {
		for k, v := range h.Dirs {
			if v.Rank *= 0.9; v.Rank < 1 {
				delete(h.Dirs, k)
			}
		}
	}
}

// key is the folder as already noted, in the case first written, so a folder is one entry however it is spelled.
func (h *History) key(dir string) string {
	for k := range h.Dirs {
		if SamePath(k, dir) {
			return k
		}
	}
	return dir
}

// Forget drops a folder, one that is gone.
func (h *History) Forget(dir string) { delete(h.Dirs, h.key(filepath.Clean(dir))) }

// score weighs a folder's rank by how lately it was visited, as zoxide does.
func (v *Visit) score(now time.Time) float64 {
	age := now.Sub(time.Unix(v.Last, 0))
	switch {
	case age < time.Hour:
		return v.Rank * 4
	case age < 24*time.Hour:
		return v.Rank * 2
	case age < 7*24*time.Hour:
		return v.Rank / 2
	}
	return v.Rank / 4
}

// Match is the folders that match what was typed, best first. The words must appear in the path in their order,
// without regard to case, and the last one in the folder's own name, as zoxide matches; nothing typed lists every
// folder.
func (h *History) Match(query string, now time.Time) []string {
	words := strings.Fields(strings.ToLower(query))
	type scored struct {
		dir   string
		score float64
	}
	var out []scored
	for dir, v := range h.Dirs {
		if matches(dir, words) {
			out = append(out, scored{dir, v.score(now)})
		}
	}
	slices.SortFunc(out, func(a, b scored) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return strings.Compare(a.dir, b.dir)
	})
	dirs := make([]string, len(out))
	for i, s := range out {
		dirs[i] = s.dir
	}
	return dirs
}

func matches(dir string, words []string) bool {
	if len(words) == 0 {
		return true
	}
	lower := strings.ToLower(dir)
	at := 0
	for _, w := range words {
		i := strings.Index(lower[at:], w)
		if i < 0 {
			return false
		}
		at += i + len(w)
	}
	last := words[len(words)-1]
	return strings.Contains(strings.ToLower(filepath.Base(dir)), last)
}

// LoadHistory reads the history kept at path; a missing or damaged file is an empty history.
func LoadHistory(path string) *History {
	h := &History{Dirs: map[string]*Visit{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, h)
	}
	if h.Dirs == nil {
		h.Dirs = map[string]*Visit{}
	}
	return h
}

// Save writes the history to path, replacing the file whole so a crash never leaves half of it.
func (h *History) Save(path string) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
