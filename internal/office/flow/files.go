package flow

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// File is the path of the family's font file at a style, following the same substitutes as Font; "" when this
// computer has neither the family nor a stand-in. PDF needs the bytes of a font, to draw at any size and to embed.
func (b *Book) File(family string, bold, italic bool) string {
	style := 0
	if bold {
		style = 1
	}
	if italic {
		style += 2
	}
	key := strings.ToLower(strings.TrimSpace(family))
	seen := map[string]bool{}
	for key != "" && !seen[key] {
		seen[key] = true
		files, ok := families[key]
		if runtime.GOOS == "darwin" {
			if m, has := macFamilies[key]; has {
				files, ok = m, true
			}
		}
		if ok {
			for _, dir := range b.dirs {
				path := filepath.Join(dir, files[style])
				if _, err := os.Stat(path); err == nil {
					return path
				}
			}
		}
		key = similar[key]
	}
	return ""
}

// Fallbacks are the files searched for characters a font lacks.
func (b *Book) Fallbacks() []string { return fallbackFiles(b.dirs) }

// Families are the names File answers to on this computer, for a list to choose from.
func (b *Book) Families() []string {
	var out []string
	for key := range families {
		if b.File(key, false, false) != "" {
			out = append(out, key)
		}
	}
	return out
}
