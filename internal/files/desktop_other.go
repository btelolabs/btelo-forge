//go:build !windows

package files

import (
	"os"
	"path/filepath"
)

// Desktop is the user's desktop folder, or "" when there is none.
func Desktop() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, "Desktop")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
}
