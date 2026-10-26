package gfx

import _ "embed"

// Fonts are the typefaces the app draws with.
type Fonts struct {
	Regular *Font
	Bold    *Font
	Mono    *Font // fixed-width, for code; the regular font when the system has none
	Icons   *Font
}

//go:embed fonts/MaterialIcons-Regular.ttf
var materialIcons []byte

// LoadSystemFonts opens the system's CJK-capable fonts for text, with fallbacks for other scripts, a fixed-width
// font for code and the embedded Material Icons for symbols.
func LoadSystemFonts() (Fonts, error) {
	var f Fonts
	var err error
	regular, bold, mono, fallbacks := systemFonts()
	if f.Regular, err = LoadFont(regular...); err != nil {
		return f, err
	}
	if f.Bold, err = LoadFont(bold...); err != nil {
		return f, err
	}
	if f.Icons, err = ParseFont(materialIcons); err != nil {
		return f, err
	}
	f.Regular.WithFallbacks(fallbacks...)
	f.Bold.WithFallbacks(fallbacks...)
	if f.Mono, err = LoadFont(mono...); err != nil {
		f.Mono = f.Regular
	} else {
		f.Mono.WithFallbacks(append(regular[:1:1], fallbacks...)...)
	}
	return f, nil
}
