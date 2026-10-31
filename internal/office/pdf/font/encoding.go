package font

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

// glyphlist.txt comes from the Adobe Glyph List and TeX names in pypdf's
// _codecs/adobe_glyphs.py (BSD licence; the list itself is BSD, by Adobe), plus
// the Zapf Dingbats names. Each line is a code point and its names, canonical
// name first. A leading + marks names of a character sequence, listed under the
// first character; they are not used to go from a rune to a name.
//
//go:embed glyphlist.txt
var glyphListText string

var glyphList = sync.OnceValue(func() *glyphTables {
	t := &glyphTables{
		toRune: make(map[string]rune, 8000),
		toName: make(map[rune]string, 6000),
	}
	for _, line := range strings.Split(glyphListText, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		seq := strings.HasPrefix(fields[0], "+")
		v, err := strconv.ParseUint(strings.TrimPrefix(fields[0], "+"), 16, 32)
		if err != nil {
			continue
		}
		r := rune(v)
		for _, name := range fields[1:] {
			t.toRune[name] = r
		}
		if !seq {
			t.toName[r] = fields[1]
		}
	}
	return t
})

type glyphTables struct {
	toRune map[string]rune
	toName map[rune]string
}

// Ligature names of the kind Unicode-aware fonts use.
var ligatureNames = map[string]rune{
	"f_f": 0xFB00, "f_i": 0xFB01, "f_l": 0xFB02, "f_f_i": 0xFB03, "f_f_l": 0xFB04,
}

// NameToRune maps a glyph name to a character through the Adobe glyph list,
// uniXXXX and uXXXXX forms, with any ".suffix" dropped. A name for a sequence
// gives its first character.
func NameToRune(name string) (rune, bool) {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return 0, false
	}
	if r, ok := glyphList().toRune[name]; ok {
		return r, true
	}
	if r, ok := ligatureNames[name]; ok {
		return r, true
	}
	if len(name) >= 7 && (len(name)-3)%4 == 0 && strings.HasPrefix(name, "uni") {
		return hexRune(name[3:7])
	}
	if len(name) >= 5 && len(name) <= 7 && name[0] == 'u' {
		return hexRune(name[1:])
	}
	return 0, false
}

// hexRune reads upper-case hexadecimal, refusing surrogates.
func hexRune(s string) (rune, bool) {
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return 0, false
		}
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || v > 0x10FFFF || v >= 0xD800 && v <= 0xDFFF {
		return 0, false
	}
	return rune(v), true
}

// RuneToName maps a character to its canonical glyph name, which is a core font's
// name when one exists.
func RuneToName(r rune) (string, bool) {
	s, ok := glyphList().toName[r]
	return s, ok
}
