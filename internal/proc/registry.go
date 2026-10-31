package proc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Entry is a helper program this app started, kept in a file while it runs so that a later run can find it when this
// one ended without stopping it: a crash, or a program killed from outside.
type Entry struct {
	Pid     int       `json:"pid"`
	Program string    `json:"program"`
	Label   string    `json:"label"` // what it runs: "model:qwen3-4b", "speech:qwen3-design"
	Started time.Time `json:"started"`
}

// startSlack is how far apart the start recorded and the one the system tells may be for them to be one process.
const startSlack = 15 * time.Second

var reg struct {
	mu      sync.Mutex
	path    string
	entries []Entry // what runs now: left over from before, and started by this run
	left    []Entry // what was found running from before
}

// SetRegistry has the helper programs kept in the file at path from now on. It returns those the file named that are
// still running, which an earlier run left behind; they stay in the file until KillOrphan ends them or they end.
func SetRegistry(path string) []Entry {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.path = path
	reg.entries, reg.left = nil, nil
	if data, err := os.ReadFile(path); err == nil {
		var saved []Entry
		if json.Unmarshal(data, &saved) == nil {
			for _, e := range saved {
				if sameProcess(e) {
					reg.entries = append(reg.entries, e)
				}
			}
		}
	}
	reg.left = slices.Clone(reg.entries)
	saveLocked()
	return slices.Clone(reg.left)
}

// Orphans are the helper programs an earlier run left running, and that still are.
func Orphans() []Entry {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.left = slices.DeleteFunc(reg.left, func(e Entry) bool { return !sameProcess(e) })
	reg.entries = slices.DeleteFunc(reg.entries, func(e Entry) bool { return !sameProcess(e) })
	saveLocked()
	return slices.Clone(reg.left)
}

// KillOrphan ends a program an earlier run left running, with everything it started. It is checked to be that
// program still, so that a process id the system has given to another is never ended.
func KillOrphan(pid int) error {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	i := slices.IndexFunc(reg.left, func(e Entry) bool { return e.Pid == pid })
	if i < 0 {
		return os.ErrProcessDone
	}
	e := reg.left[i]
	reg.left = slices.Delete(reg.left, i, i+1)
	if sameProcess(e) {
		if p, err := os.FindProcess(pid); err == nil {
			KillTree(p)
		}
	}
	reg.entries = slices.DeleteFunc(reg.entries, func(o Entry) bool { return o.Pid == pid })
	saveLocked()
	return nil
}

// register records a program just started.
func register(e Entry) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.path == "" {
		return
	}
	reg.entries = append(reg.entries, e)
	saveLocked()
}

// unregister forgets a program that has ended.
func unregister(pid int) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.path == "" {
		return
	}
	reg.entries = slices.DeleteFunc(reg.entries, func(e Entry) bool { return e.Pid == pid })
	saveLocked()
}

// saveLocked writes the file, whole and then renamed over the old one, so a crash never leaves half of it. A failure
// is not reported: the file only helps a later run, and the program works without it.
func saveLocked() {
	if reg.path == "" {
		return
	}
	data, err := json.MarshalIndent(reg.entries, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(reg.path), 0o755); err != nil {
		return
	}
	tmp := reg.path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		os.Rename(tmp, reg.path)
	}
}

// sameProcess reports whether the process e names is running and is still the program e says: a process id is given
// out again once its process ends, so the program's file and the time it began are compared too.
func sameProcess(e Entry) bool {
	image, began, ok := processInfo(e.Pid)
	if !ok || !sameImage(image, e.Program) {
		return false
	}
	d := began.Sub(e.Started)
	return d > -startSlack && d < startSlack
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// Alive reports whether a process with the id is running.
func Alive(pid int) bool {
	_, _, ok := processInfo(pid)
	return ok
}
