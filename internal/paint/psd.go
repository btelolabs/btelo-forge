package paint

import (
	"errors"
	"image"
	"image/color"

	"github.com/btelolabs/btelo-forge/internal/psd"
)

// A canvas and Photoshop's file. The file holds layers of pixels with their blend modes, opacity and masks, and
// groups; what a canvas holds beyond that goes to pixels, and what has no such form on the way is counted in PSDLoss, so
// the person can be told.

// PSDLoss says what did not come through between a canvas and a Photoshop file.
type PSDLoss struct {
	Adjustments int // adjustment layers left out
	Effects     int // layers whose effects were left out
	Clipped     int // layers no longer clipped to the one below
}

// Any reports whether something did not come through.
func (l PSDLoss) Any() bool { return l.Adjustments+l.Effects+l.Clipped > 0 }

// psdBlends name the blend modes as the format does, by Blend.
var psdBlends = [blendCount]string{
	BlendNormal: "norm", BlendDarken: "dark", BlendMultiply: "mul ", BlendColorBurn: "idiv", BlendLinearBurn: "lbrn",
	BlendDarkerColor: "dkCl", BlendLighten: "lite", BlendScreen: "scrn", BlendColorDodge: "div ", BlendLinearDodge: "lddg",
	BlendLighterColor: "lgCl", BlendOverlay: "over", BlendSoftLight: "sLit", BlendHardLight: "hLit", BlendVividLight: "vLit",
	BlendLinearLight: "lLit", BlendPinLight: "pLit", BlendHardMix: "hMix", BlendDifference: "diff", BlendExclusion: "smud",
	BlendSubtract: "fsub", BlendDivide: "fdiv", BlendHue: "hue ", BlendSaturation: "sat ", BlendColor: "colr",
	BlendLuminosity: "lum ", BlendPassThrough: "pass",
}

func blendOfPSD(key string) Blend {
	for b, k := range psdBlends {
		if k == key {
			return Blend(b)
		}
	}
	return BlendNormal
}

// FromPSD makes a canvas of a Photoshop file, framed to its size.
func FromPSD(f *psd.File) (*Doc, PSDLoss, error) {
	d, err := NewFramed(f.W, f.H, "Background", color.NRGBA{})
	if err != nil {
		return nil, PSDLoss{}, err
	}
	var loss PSDLoss
	d.Root.Children = d.nodesOfPSD(f.Layers, &loss)
	d.Active = 0
	if first := firstLayer(d.Root.Children); first != nil {
		d.Active = first.ID
	}
	return d, loss, nil
}

// firstLayer is the topmost node that is not a group, or a group when they all are.
func firstLayer(nodes []*Node) *Node {
	for _, n := range nodes {
		if !n.IsGroup() {
			return n
		}
		if l := firstLayer(n.Children); l != nil {
			return l
		}
	}
	if len(nodes) > 0 {
		return nodes[0]
	}
	return nil
}

func (d *Doc) nodesOfPSD(layers []*psd.Layer, loss *PSDLoss) []*Node {
	out := make([]*Node, 0, len(layers))
	for _, l := range layers {
		if l.Adjust {
			loss.Adjustments++
			continue
		}
		if l.Effects {
			loss.Effects++
		}
		if l.Clipped {
			loss.Clipped++
		}
		n := &Node{ID: d.newID(), Name: l.Name, Visible: l.Visible, Opacity: float64(l.Opacity) / 255, Blend: blendOfPSD(l.Blend)}
		if l.Group {
			n.Open = l.Open
			n.Children = d.nodesOfPSD(l.Children, loss)
		} else {
			n.Pixels = NewRaster()
			if len(l.Pix) > 0 {
				n.Pixels.Write(premultiplied(l.Pix, l.Bounds))
			}
		}
		if l.Mask != nil {
			n.Mask = NewMask(l.Fill)
			n.Mask.Write(l.Mask)
			n.MaskOff = l.MaskOff
		}
		out = append(out, n)
	}
	return out
}

func premultiplied(pix []uint8, r image.Rectangle) *image.RGBA {
	img := image.NewRGBA(r)
	for i := 0; i+3 < len(pix); i += 4 {
		a := uint32(pix[i+3])
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(mul8(uint32(pix[i]), a)), uint8(mul8(uint32(pix[i+1]), a)), uint8(mul8(uint32(pix[i+2]), a)), pix[i+3]
	}
	return img
}

func straight(img *image.RGBA) []uint8 {
	out := make([]uint8, len(img.Pix))
	for i := 0; i+3 < len(out); i += 4 {
		a := uint32(img.Pix[i+3])
		out[i+3] = img.Pix[i+3]
		if a != 0 {
			for c := range 3 {
				out[i+c] = uint8(min((uint32(img.Pix[i+c])*255+a/2)/a, 255))
			}
		}
	}
	return out
}

// ToPSD is the canvas as a Photoshop file: its frame, or everything on it when it has none. Vector and text layers
// go to pixels.
func (d *Doc) ToPSD() (*psd.File, PSDLoss, error) {
	area := d.Frame
	if area.Empty() {
		area = d.Extent()
	}
	if area.Empty() {
		return nil, PSDLoss{}, errors.New("the canvas is empty")
	}
	if area.Dx() > psd.MaxSide || area.Dy() > psd.MaxSide {
		return nil, PSDLoss{}, ErrTooBig
	}
	merged := d.Composite(area)
	f := &psd.File{W: area.Dx(), H: area.Dy(), Merged: &image.NRGBA{Pix: straight(merged), Stride: area.Dx() * 4, Rect: image.Rect(0, 0, area.Dx(), area.Dy())}}
	var loss PSDLoss
	f.Layers = layersOfDoc(d.Root.Children, area, &loss)
	return f, loss, nil
}

func layersOfDoc(nodes []*Node, area image.Rectangle, loss *PSDLoss) []*psd.Layer {
	out := make([]*psd.Layer, 0, len(nodes))
	for _, n := range nodes {
		if n.Adjust != nil {
			loss.Adjustments++
			continue
		}
		if n.Style != nil || n.Filters != nil {
			loss.Effects++
		}
		l := &psd.Layer{Name: n.Name, Visible: n.Visible, Opacity: uint8(n.Opacity*255 + 0.5), Blend: psdBlends[n.Blend]}
		switch {
		case n.IsGroup():
			l.Group, l.Open = true, n.Open
			l.Children = layersOfDoc(n.Children, area, loss)
		default:
			raster := rasterOf(n)
			if b := raster.Bounds().Intersect(area); !b.Empty() {
				pixels := raster.Read(b)
				l.Bounds, l.Pix = b.Sub(area.Min), straight(pixels)
			}
		}
		if n.Mask != nil {
			b := n.Mask.TileBounds().Intersect(area)
			if !b.Empty() {
				m := n.Mask.Read(b)
				l.Mask = &image.Alpha{Pix: m.Pix, Stride: m.Stride, Rect: b.Sub(area.Min)}
			} else {
				l.Mask = &image.Alpha{Pix: []uint8{n.Mask.Fill()}, Stride: 1, Rect: image.Rect(0, 0, 1, 1)}
			}
			l.Fill, l.MaskOff = n.Mask.Fill(), n.MaskOff
		}
		out = append(out, l)
	}
	return out
}
