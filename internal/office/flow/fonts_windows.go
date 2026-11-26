//go:build windows

package flow

import (
	"os"
	"path/filepath"
)

func fontDirs() []string {
	dir := os.Getenv("WINDIR")
	if dir == "" {
		dir = `C:\Windows`
	}
	local := os.Getenv("LOCALAPPDATA")
	return []string{filepath.Join(dir, "Fonts"), filepath.Join(local, `Microsoft\Windows\Fonts`)}
}

func platformFamilies(key string) (faceFiles, bool) {
	f, ok := families[key]
	return f, ok
}

func fallbackNames() []string {
	return []string{"msyh.ttc", "segoeui.ttf", "malgun.ttf", "seguisym.ttf", "seguiemj.ttf", "Arial Unicode.ttf"}
}
