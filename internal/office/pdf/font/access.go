package font

// CFF is the CFF table of an OpenType font, nil for a font with glyf outlines.
func (t *TrueType) CFF() *CFF { return t.cff }
