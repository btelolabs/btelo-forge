//go:build darwin

package font

import (
	"os"
	"path/filepath"
	"testing"
)

func arialFont(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("/System/Library/Fonts/Supplemental/Arial.ttf")
	if err != nil {
		t.Skip("no Arial.ttf")
	}
	return b
}

type namedCollection struct {
	name string
	data []byte
}

func cjkCollections(t *testing.T) []namedCollection {
	t.Helper()
	var out []namedCollection
	for _, path := range []string{
		"/System/Library/Fonts/STHeiti Light.ttc",
		"/System/Library/Fonts/STHeiti Medium.ttc",
	} {
		if b, err := os.ReadFile(path); err == nil {
			out = append(out, namedCollection{name: filepath.Base(path), data: b})
		}
	}
	if len(out) == 0 {
		t.Skip("no CJK .ttc collection found")
	}
	return out
}

func openTypeFontPaths() []string {
	files, _ := filepath.Glob("/System/Library/Fonts/Supplemental/STIXGeneral*.otf")
	return files
}
