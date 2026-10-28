package mind

import (
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Nested maps: a node may show another map file whole, and that map may show others. A map is drawn inside the
// chain of maps that show it, outermost first: one already in its chain holds itself, at some depth, and is drawn
// as a warning in its place, as is one deeper than MaxNest; neither is an error, and drawing always ends.

// MaxNest is how many maps deep nested maps are drawn.
const MaxNest = 4

// minNestPx is the smallest width a nested map is drawn at; smaller ones show their icon.
const minNestPx = 24

// NestKey is how map files are compared: the cleaned absolute path, without regard to letter case.
func NestKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return strings.ToLower(filepath.Clean(path))
}

// InChain reports whether path is one of the maps in chain.
func InChain(path string, chain []string) bool {
	k := NestKey(path)
	for _, c := range chain {
		if NestKey(c) == k {
			return true
		}
	}
	return false
}

// Source gives the pictures of the picture, video and smart object nodes of maps drawn away from the window; it may
// take its time, and returns nil for a picture it cannot give.
type Source interface {
	Still(n *Node, px int) *image.RGBA
}

// Thumb is a nested map as drawn.
type Thumb struct {
	Img    *image.RGBA
	State  PictureState
	Aspect float64  // the map's proportions, for the node showing it
	Files  []string // every map file read to draw it, so it is drawn again when one changes
}

// DrawNested draws the map file path, shown inside the maps of chain, at most px wide.
func DrawNested(path string, chain []string, px int, fonts gfx.Fonts, labels Labels, src Source) Thumb {
	if InChain(path, chain) {
		return Thumb{State: PicCycle}
	}
	if len(chain) >= MaxNest {
		return Thumb{State: PicTooDeep}
	}
	t := Thumb{Files: []string{path}}
	f, err := os.Open(path)
	if err != nil {
		t.State = PicMissing
		return t
	}
	m, err := Load(f, filepath.Dir(path), FontMeasurer{fonts})
	f.Close()
	if err != nil {
		t.State = PicMissing
		return t
	}
	m.Layout()
	b := m.Bounds()
	t.Aspect = 4.0 / 3
	if !b.Empty() {
		t.Aspect = math.Min(math.Max(b.W()/b.H(), 0.5), 3)
	}
	px = max(px, minNestPx)
	w, h := px, max(1, int(float64(px)/t.Aspect))
	pics := &nestPictures{chain: append(append([]string{}, chain...), path), fonts: fonts, labels: labels, src: src}
	t.Img = RenderFit(m, w, h, &Options{Fonts: fonts, Pictures: pics, Labels: labels})
	t.Files = append(t.Files, pics.files...)
	t.State = PicReady
	return t
}

// nestPictures gives the pictures inside a nested map as it is drawn.
type nestPictures struct {
	chain  []string
	fonts  gfx.Fonts
	labels Labels
	src    Source
	files  []string
}

func (p *nestPictures) Picture(n *Node, px int) (*image.RGBA, PictureState, bool) {
	if n.Kind == Nested {
		if px < minNestPx {
			return nil, PicLoading, false
		}
		t := DrawNested(n.Ref, p.chain, px, p.fonts, p.labels, p.src)
		p.files = append(p.files, t.Files...)
		return t.Img, t.State, false
	}
	if p.src == nil {
		return nil, PicLoading, false
	}
	if img := p.src.Still(n, px); img != nil {
		return img, PicReady, false
	}
	return nil, PicMissing, false
}

// DrawFile draws a map file whole, in its own proportions, at most box pixels each way, as the asset manager shows
// it; nested maps in it are drawn with it as the outermost of their chain.
func DrawFile(path string, box int, fonts gfx.Fonts, labels Labels, src Source) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := Load(f, filepath.Dir(path), FontMeasurer{fonts})
	if err != nil {
		return nil, err
	}
	m.Layout()
	w, h := box, box*3/4
	if b := m.Bounds(); !b.Empty() {
		aspect := math.Min(math.Max(b.W()/b.H(), 0.25), 4)
		if aspect >= 1 {
			w, h = box, max(1, int(float64(box)/aspect))
		} else {
			w, h = max(1, int(float64(box)*aspect)), box
		}
	}
	pics := &nestPictures{chain: []string{path}, fonts: fonts, labels: labels, src: src}
	return RenderFit(m, w, h, &Options{Fonts: fonts, Pictures: pics, Labels: labels}), nil
}
