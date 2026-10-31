package render

import (
	"os"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
)

// BookFonts finds fonts through the document fonts of this computer.
type BookFonts struct {
	Book *flow.Book

	mu    sync.Mutex
	files map[string]*font.TrueType
	backs []*font.TrueType
	found map[rune]runeFont
	ready bool
}

type runeFont struct {
	p   font.Program
	gid int
}

func (b *BookFonts) load(path string) *font.TrueType {
	if t, ok := b.files[path]; ok {
		return t
	}
	if b.files == nil {
		b.files = map[string]*font.TrueType{}
	}
	var t *font.TrueType
	if data, err := os.ReadFile(path); err == nil {
		t, _ = font.ParseTrueType(data, 0)
	}
	b.files[path] = t
	return t
}

// Substitute is the family's font, when the computer has it or a close one.
func (b *BookFonts) Substitute(family string, bold, italic bool) font.Program {
	if b.Book == nil {
		return nil
	}
	path := b.Book.File(family, bold, italic)
	if path == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.load(path); t != nil {
		return t
	}
	return nil
}

// ForRune searches the fallback fonts.
func (b *BookFonts) ForRune(r rune) (font.Program, int) {
	if b.Book == nil {
		return nil, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.ready {
		b.ready = true
		b.found = map[rune]runeFont{}
		for _, path := range b.Book.Fallbacks() {
			if t := b.load(path); t != nil {
				b.backs = append(b.backs, t)
			}
		}
	}
	if f, ok := b.found[r]; ok {
		return f.p, f.gid
	}
	var out runeFont
	for _, t := range b.backs {
		if id, ok := t.GIDByRune(r); ok && id != 0 {
			out = runeFont{t, id}
			break
		}
	}
	b.found[r] = out
	return out.p, out.gid
}
