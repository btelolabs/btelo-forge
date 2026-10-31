package opc

import "maps"

// Clone copies the package for writing out on another goroutine: the parts' bytes are shared, as they are only ever
// replaced, while the table of parts and the content types are its own.
func (p *Package) Clone() *Package {
	c := &Package{
		parts:    make(map[string]*Part, len(p.parts)),
		order:    append([]string(nil), p.order...),
		defaults: maps.Clone(p.defaults),
		override: maps.Clone(p.override),
		ctDoc:    p.ctDoc.Clone(),
	}
	for name, part := range p.parts {
		copied := *part
		c.parts[name] = &copied
	}
	return c
}
