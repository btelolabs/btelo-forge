//go:build !windows

package files

import (
	"os"
	"path/filepath"
)

// Desktop is the user's desktop folder, or "" when there is none.
func Desktop() string { return homeFolder("Desktop") }

// Pictures is the user's pictures folder, or "" when there is none.
func Pictures() string { return homeFolder("Pictures") }

// homeFolder is the folder of that name in the user's home folder, or "" when there is none.
func homeFolder(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, name)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
}
