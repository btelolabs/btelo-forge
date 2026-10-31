package font

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

// standard14.txt holds the Adobe Core 14 AFM metrics, the data behind pypdf's
// core_font_metrics.py, with the glyph names pypdf dropped. Each font is two
// lines: name, ascent, descent, cap height, bounding box; then glyph-width pairs.
// Symbol and ZapfDingbats have no ascender in their AFM files, so their bounding
// boxes stand in.
//
//go:embed standard14.txt
var standard14Text string

type std14Metrics struct {
	ascent, descent, capHeight float64
	bbox                       [4]float64
	widths                     map[string]float64
}

var standard14Fonts = sync.OnceValue(func() map[string]*std14Metrics {
	fonts := make(map[string]*std14Metrics, 14)
	lines := strings.Split(standard14Text, "\n")
	for i := 0; i+1 < len(lines); i += 2 {
		head := strings.Fields(lines[i])
		pairs := strings.Fields(lines[i+1])
		if len(head) != 8 {
			continue
		}
		num := make([]float64, 7)
		for j := range num {
			num[j], _ = strconv.ParseFloat(head[j+1], 64)
		}
		m := &std14Metrics{
			ascent: num[0], descent: num[1], capHeight: num[2],
			bbox:   [4]float64{num[3], num[4], num[5], num[6]},
			widths: make(map[string]float64, len(pairs)/2),
		}
		for j := 0; j+1 < len(pairs); j += 2 {
			m.widths[pairs[j]], _ = strconv.ParseFloat(pairs[j+1], 64)
		}
		fonts[head[0]] = m
	}
	return fonts
})

// standard14Families is searched in order, so the longer names come first.
var standard14Families = []struct {
	prefix string
	kind   byte // 'h', 't', 'c', or 0 for a font with no styles.
	name   string
}{
	{"timesnewroman", 't', ""},
	{"times", 't', ""},
	{"couriernew", 'c', ""},
	{"courier", 'c', ""},
	{"arial", 'h', ""},
	{"helvetica", 'h', ""},
	{"symbol", 0, "Symbol"},
	{"zapfdingbats", 0, "ZapfDingbats"},
}

// Words allowed after a family name. "ps" and "mt" come from names like
// TimesNewRomanPS-BoldItalicMT.
var standard14Words = []string{"bold", "italic", "oblique", "roman", "regular", "normal", "psmt", "ps", "mt"}

// Standard14 normalises a font name, its aliases and any subset prefix to the
// canonical name of a standard 14 font.
func Standard14(name string) (canonical string, ok bool) {
	if len(name) > 7 && name[6] == '+' {
		upper := true
		for i := 0; i < 6; i++ {
			upper = upper && name[i] >= 'A' && name[i] <= 'Z'
		}
		if upper {
			name = name[7:]
		}
	}
	s := strings.Map(func(r rune) rune {
		switch r {
		case ' ', ',', '-', '_':
			return -1
		}
		return r
	}, strings.ToLower(name))
	for _, f := range standard14Families {
		rest, found := strings.CutPrefix(s, f.prefix)
		if !found {
			continue
		}
		bold, italic := false, false
	words:
		for rest != "" {
			for _, w := range standard14Words {
				if after, cut := strings.CutPrefix(rest, w); cut {
					bold = bold || w == "bold"
					italic = italic || w == "italic" || w == "oblique"
					rest = after
					continue words
				}
			}
			return "", false
		}
		switch f.kind {
		case 'h':
			return styledName("Helvetica", "", "Oblique", bold, italic), true
		case 'c':
			return styledName("Courier", "", "Oblique", bold, italic), true
		case 't':
			return styledName("Times", "Roman", "Italic", bold, italic), true
		}
		return f.name, true
	}
	return "", false
}

func styledName(family, plain, slanted string, bold, italic bool) string {
	style := plain
	switch {
	case bold && italic:
		style = "Bold" + slanted
	case bold:
		style = "Bold"
	case italic:
		style = slanted
	}
	if style == "" {
		return family
	}
	return family + "-" + style
}

// Standard14Width is the glyph's width in 1/1000 em.
func Standard14Width(canonical, glyphName string) (float64, bool) {
	m := standard14Fonts()[canonical]
	if m == nil {
		return 0, false
	}
	w, ok := m.widths[glyphName]
	return w, ok
}

// Standard14Metrics gives the font's vertical metrics and bounding box.
func Standard14Metrics(canonical string) (ascent, descent, capHeight float64, bbox [4]float64, ok bool) {
	m := standard14Fonts()[canonical]
	if m == nil {
		return 0, 0, 0, bbox, false
	}
	return m.ascent, m.descent, m.capHeight, m.bbox, true
}
