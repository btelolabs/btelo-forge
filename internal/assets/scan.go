package assets

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Entry is one file or folder of a folder.
type Entry struct {
	Path string
	Name string
	Kind Kind
	Size int64
	Mod  time.Time
}

// hiddenNames are files the system keeps in folders for itself.
var hiddenNames = []string{"desktop.ini", "thumbs.db", ".ds_store", "$recycle.bin", "system volume information"}

// Scan lists dir: its folders and files, without those the system hides or keeps for itself, nor the app's own
// unfinished writes.
func Scan(dir string) ([]Entry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		name := it.Name()
		lower := strings.ToLower(name)
		if strings.HasPrefix(name, ".") || slices.Contains(hiddenNames, lower) || strings.HasSuffix(lower, ".tmp") || strings.HasSuffix(lower, ".part") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := it.Info()
		if err != nil {
			continue // removed while listing
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if info, err = os.Stat(path); err != nil {
				continue // a broken link
			}
		}
		if hiddenFile(path, info) {
			continue
		}
		e := Entry{Path: path, Name: name, Size: info.Size(), Mod: info.ModTime()}
		if info.IsDir() {
			e.Kind, e.Size = Folder, 0
		} else {
			e.Kind = KindOf(name)
			if strings.EqualFold(filepath.Ext(name), ".ts") && !mpegTS(path) {
				e.Kind = Code // TypeScript, not a broadcast video
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// mpegTS reports whether path starts as an MPEG transport stream does, with its sync byte.
func mpegTS(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [1]byte
	_, err = io.ReadFull(f, b[:])
	return err == nil && b[0] == 0x47
}
