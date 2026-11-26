//go:build windows

package font

import (
	"os"
	"path/filepath"
	"testing"
)

func systemFontFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(`C:\Windows\Fonts`, name))
	if err != nil {
		t.Skip("no ", name)
	}
	return b
}

func arialFont(t *testing.T) []byte { return systemFontFile(t, "arial.ttf") }

type namedCollection struct {
	name string
	data []byte
}

func cjkCollections(t *testing.T) []namedCollection {
	t.Helper()
	var out []namedCollection
	for _, name := range []string{"simsun.ttc", "msyh.ttc"} {
		out = append(out, namedCollection{name: name, data: systemFontFile(t, name)})
	}
	return out
}

func openTypeFontPaths() []string {
	files, _ := filepath.Glob(`C:\Windows\Fonts\*.otf`)
	return files
}
