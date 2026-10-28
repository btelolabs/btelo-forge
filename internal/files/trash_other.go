//go:build !windows

package files

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// ErrNoBin is an item on another drive than the Trash.
var ErrNoBin = errors.New("this drive has no Trash")

// trashDir is where the Trash keeps items: ~/.Trash on macOS, the freedesktop.org trash elsewhere, with its notes
// of where each came from.
func trashDir() (files, info string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".Trash"), "", nil
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "Trash", "files"), filepath.Join(data, "Trash", "info"), nil
}

func device(path string) (uint64, bool) {
	var st syscall.Stat_t
	if syscall.Lstat(path, &st) != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}

// CanTrash reports whether path is on the same drive as the Trash, so moving it there is a rename.
func CanTrash(path string) bool {
	files, _, err := trashDir()
	if err != nil || os.MkdirAll(files, 0o700) != nil {
		return false
	}
	a, ok1 := device(path)
	b, ok2 := device(files)
	return ok1 && ok2 && a == b
}

// Trash moves one item to the Trash.
func Trash(path string) (Trashed, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Trashed{}, err
	}
	if !CanTrash(path) {
		return Trashed{}, ErrNoBin
	}
	files, infoDir, _ := trashDir()
	name := UniqueName(files, filepath.Base(path))
	t := Trashed{Original: path, Stored: filepath.Join(files, name)}
	if infoDir != "" {
		if err := os.MkdirAll(infoDir, 0o700); err != nil {
			return Trashed{}, err
		}
		t.Info = filepath.Join(infoDir, name+".trashinfo")
		note := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", (&url.URL{Path: path}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
		if err := os.WriteFile(t.Info, []byte(note), 0o600); err != nil {
			return Trashed{}, err
		}
	}
	if err := os.Rename(path, t.Stored); err != nil {
		if t.Info != "" {
			os.Remove(t.Info)
		}
		return Trashed{}, err
	}
	return t, nil
}
