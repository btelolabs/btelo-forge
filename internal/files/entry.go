// Package files is the file manager's work away from the screen: listing folders, adding up how much space each
// takes as gdu does, copying, moving, deleting and restoring with progress, searching names and contents, the
// folders visited, and a git repository's state.
package files

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Entry is one file or folder in a listing.
type Entry struct {
	Name   string
	Dir    bool  // a folder, or a link to one
	Link   bool  // a symbolic link or junction: it is entered, but never counted or deleted through
	Size   int64 // a file's length; a folder's is only known once it has been analysed
	Mod    time.Time
	Hidden bool
}

// List reads a folder. A link is listed as what it points to, a folder or a file, and is marked as a link.
func List(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		info, ierr := de.Info()
		if ierr != nil {
			continue // removed while the folder was read
		}
		out = append(out, entryOf(dir, de.Name(), info))
	}
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return out, nil
}

// entryOf describes a file from what reading its folder told.
func entryOf(dir, name string, info fs.FileInfo) Entry {
	e := Entry{Name: name, Dir: info.IsDir(), Size: info.Size(), Mod: info.ModTime(), Hidden: hidden(name, info)}
	if isLink(info.Mode()) {
		e.Link, e.Size = true, 0
		if target, err := os.Stat(filepath.Join(dir, name)); err == nil {
			e.Dir = target.IsDir()
			if !e.Dir {
				e.Size = target.Size()
			}
		}
	}
	if e.Dir {
		e.Size = 0
	}
	return e
}

// isLink reports whether a mode is a symbolic link, or on Windows a junction or another reparse point that is not
// followed when counting or deleting.
func isLink(m fs.FileMode) bool { return m&(fs.ModeSymlink|fs.ModeIrregular) != 0 }

// realDir reports whether a directory entry is a folder of its own, one to go into when counting, searching or
// deleting.
func realDir(m fs.FileMode) bool { return m.IsDir() && !isLink(m) }

// Within reports whether path is root or inside it, comparing as the file system does (without regard to case on
// Windows and macOS).
func Within(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	if samePath(path, root) {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return len(path) > len(root) && samePath(path[:len(root)], root)
}

// SamePath reports whether two paths name the same place, comparing as the file system does.
func SamePath(a, b string) bool { return samePath(filepath.Clean(a), filepath.Clean(b)) }

// UniqueName is a name for name in dir that nothing there has yet: the name itself, or with " (2)", " (3)" … before
// its extension, as Explorer names copies.
func UniqueName(dir, name string) string {
	if _, err := os.Lstat(filepath.Join(dir, name)); os.IsNotExist(err) {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if ext == name { // a name such as ".gitignore" is all stem
		stem, ext = name, ""
	}
	for i := 2; ; i++ {
		candidate := stem + " (" + strconv.Itoa(i) + ")" + ext
		if _, err := os.Lstat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}
