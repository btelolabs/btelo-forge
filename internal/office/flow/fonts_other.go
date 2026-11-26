//go:build !windows && !darwin

package flow

import (
	"os"
	"path/filepath"
)

func fontDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{"/usr/share/fonts", "/usr/local/share/fonts", filepath.Join(home, ".local/share/fonts")}
}

func platformFamilies(key string) (faceFiles, bool) {
	f, ok := families[key]
	return f, ok
}

func fallbackNames() []string {
	return []string{"NotoSansCJK-Regular.ttc", "DejaVuSans.ttf", "Arial Unicode.ttf"}
}
