package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// JPXImage is a decoded JPEG 2000 image.
type JPXImage struct {
	Width, Height int
	Components    int    // after palette expansion
	BitDepth      int    // of the source samples; Pix is always 8 bit
	Pix           []byte // interleaved 8 bit samples, Width*Height*Components
	ColorSpace    string // "sRGB", "sGray", "sYCC", "CMYK" or "" when the boxes say nothing
	HasAlpha      bool   // the last component is opacity per cdef
}

// JPX decodes a JPEG 2000 image: JP2 boxes or a bare codestream.
func JPX(data []byte, maxPixels int) (img *JPXImage, err error) {
	defer func() {
		if r := recover(); r != nil {
			img = nil
			err = fmt.Errorf("jpx: malformed stream: %v", r)
		}
	}()
	info, err := parseJP2(data)
	if err != nil {
		return nil, err
	}
	d := &jpxDecoder{data: info.codestream, limit: pixelLimit(maxPixels)}
	if err := d.parseMain(); err != nil {
		return nil, err
	}
	if err := d.decodeTiles(); err != nil {
		return nil, err
	}
	return d.assemble(info)
}

// jp2Info is what the boxes around a codestream say.
type jp2Info struct {
	colorSpace string
	palette    *jp2Palette
	cmap       []jp2Map
	cdef       []jp2Def
	codestream []byte
}

// jp2Palette holds the palette columns already scaled to 8 bits.
type jp2Palette struct {
	entries int
	depth   int
	columns [][]byte
}

type jp2Map struct {
	comp    int
	palette bool
	column  int
}

type jp2Def struct {
	channel, typ, assoc int
}

func walkBoxes(data []byte, fn func(typ string, body []byte) error) error {
	for len(data) >= 8 {
		size := uint64(binary.BigEndian.Uint32(data))
		typ := string(data[4:8])
		head := uint64(8)
		switch size {
		case 0:
			size = uint64(len(data))
		case 1:
			if len(data) < 16 {
				return nil
			}
			size = binary.BigEndian.Uint64(data[8:])
			head = 16
		}
		if size < head {
			return errors.New("jpx: bad box length")
		}
		// A truncated last box still gives what it has.
		size = min(size, uint64(len(data)))
		if err := fn(typ, data[head:size]); err != nil {
			return err
		}
		data = data[size:]
	}
	return nil
}

func parseJP2(data []byte) (*jp2Info, error) {
	info := &jp2Info{}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0x4F {
		info.codestream = data
		return info, nil
	}
	if len(data) < 12 || string(data[4:8]) != "jP  " {
		return nil, errors.New("jpx: neither JP2 boxes nor a codestream")
	}
	err := walkBoxes(data, func(typ string, body []byte) error {
		switch typ {
		case "jp2h":
			return walkBoxes(body, info.headerBox)
		case "jp2c":
			if info.codestream == nil {
				info.codestream = body
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if info.codestream == nil {
		return nil, errors.New("jpx: no codestream box")
	}
	return info, nil
}

// headerBox reads one box inside jp2h.
func (info *jp2Info) headerBox(typ string, b []byte) error {
	switch typ {
	case "colr":
		// The first method this reader understands wins, as T.800 I.5.3.3 asks.
		if info.colorSpace == "" && len(b) >= 7 && b[0] == 1 {
			switch binary.BigEndian.Uint32(b[3:]) {
			case 16:
				info.colorSpace = "sRGB"
			case 17:
				info.colorSpace = "sGray"
			case 18:
				info.colorSpace = "sYCC"
			case 12:
				info.colorSpace = "CMYK"
			}
		}
	case "pclr":
		return info.parsePalette(b)
	case "cmap":
		for ; len(b) >= 4; b = b[4:] {
			info.cmap = append(info.cmap, jp2Map{int(binary.BigEndian.Uint16(b)), b[2] == 1, int(b[3])})
		}
	case "cdef":
		if len(b) < 2 {
			return errors.New("jpx: short cdef box")
		}
		n := int(binary.BigEndian.Uint16(b))
		b = b[2:]
		if len(b) < n*6 {
			return errors.New("jpx: short cdef box")
		}
		for i := range n {
			e := b[i*6:]
			info.cdef = append(info.cdef, jp2Def{
				int(binary.BigEndian.Uint16(e)),
				int(binary.BigEndian.Uint16(e[2:])),
				int(binary.BigEndian.Uint16(e[4:])),
			})
		}
	}
	return nil
}

func (info *jp2Info) parsePalette(b []byte) error {
	if len(b) < 3 {
		return errors.New("jpx: short pclr box")
	}
	entries := int(binary.BigEndian.Uint16(b))
	cols := int(b[2])
	b = b[3:]
	if entries == 0 || cols == 0 || len(b) < cols {
		return errors.New("jpx: bad pclr box")
	}
	depths := b[:cols]
	b = b[cols:]
	p := &jp2Palette{entries: entries, columns: make([][]byte, cols)}
	for c := range cols {
		p.columns[c] = make([]byte, entries)
		p.depth = max(p.depth, int(depths[c]&0x7F)+1)
	}
	for i := range entries {
		for c := range cols {
			depth := int(depths[c]&0x7F) + 1
			n := (depth + 7) / 8
			if depth > 38 || len(b) < n {
				return errors.New("jpx: bad pclr box")
			}
			var v uint64
			for _, x := range b[:n] {
				v = v<<8 | uint64(x)
			}
			b = b[n:]
			p.columns[c][i] = scaleTo8(v, depth)
		}
	}
	info.palette = p
	return nil
}

// scaleTo8 maps a sample of the given depth onto 0..255.
func scaleTo8(v uint64, depth int) byte {
	if depth == 8 {
		return byte(v)
	}
	top := uint64(1)<<depth - 1
	v = min(v, top)
	return byte((v*255 + top/2) / top)
}

// assemble turns the component planes into the interleaved 8 bit image.
func (d *jpxDecoder) assemble(info *jp2Info) (*JPXImage, error) {
	w, h := d.xsiz-d.xosiz, d.ysiz-d.yosiz
	channels, err := d.channels(info)
	if err != nil {
		return nil, err
	}
	order, alpha := outputOrder(info, len(channels))
	n := len(order)
	if !fits(w, h*n, 5*d.limit) {
		return nil, errors.New("jpx: the expanded image is too large")
	}
	// Each component gets a sample index per column, and a table to 8 bits.
	xmap := make([][]int32, len(d.comps))
	luts := make([][]byte, len(d.comps))
	for c := range d.comps {
		comp := &d.comps[c]
		xmap[c] = make([]int32, w)
		for x := range w {
			xmap[c][x] = int32(min(max((x+d.xosiz)/comp.dx-comp.x0, 0), comp.w-1))
		}
		depth := min(comp.depth, 16)
		luts[c] = make([]byte, 1<<depth)
		for v := range luts[c] {
			luts[c][v] = scaleTo8(uint64(v), depth)
		}
	}
	pix := make([]byte, w*h*n)
	parallelFor(h, 16, func(lo, hi int) {
		rows := make([][]uint16, len(d.comps))
		for y := lo; y < hi; y++ {
			for c := range d.comps {
				comp := &d.comps[c]
				row := min(max((y+d.yosiz)/comp.dy-comp.y0, 0), comp.h-1)
				rows[c] = comp.plane[row*comp.w:][:comp.w]
			}
			out := pix[y*w*n:][:w*n]
			for i, ch := range order {
				m := channels[ch]
				col := xmap[m.comp]
				src := rows[m.comp]
				if m.palette {
					table := info.palette.columns[m.column]
					for x := range w {
						out[x*n+i] = table[min(int(src[col[x]]), len(table)-1)]
					}
				} else {
					lut := luts[m.comp]
					for x := range w {
						out[x*n+i] = lut[src[col[x]]]
					}
				}
			}
		}
	})
	depth := d.comps[0].depth
	if info.palette != nil {
		depth = info.palette.depth
	}
	return &JPXImage{
		Width: w, Height: h, Components: n, BitDepth: depth,
		Pix: pix, ColorSpace: info.colorSpace, HasAlpha: alpha,
	}, nil
}

// channels lists the image channels after the optional palette.
func (d *jpxDecoder) channels(info *jp2Info) ([]jp2Map, error) {
	if info.palette == nil {
		out := make([]jp2Map, len(d.comps))
		for i := range out {
			out[i].comp = i
		}
		return out, nil
	}
	if len(info.cmap) == 0 {
		return nil, errors.New("jpx: palette without a component mapping")
	}
	if len(info.cmap) > jpxMaxComponents {
		return nil, errors.New("jpx: too many channels")
	}
	for _, m := range info.cmap {
		if m.comp >= len(d.comps) || (m.palette && m.column >= len(info.palette.columns)) {
			return nil, errors.New("jpx: bad component mapping")
		}
	}
	return info.cmap, nil
}

// outputOrder applies cdef: colours by association, then one opacity channel.
// Channels of unspecified meaning are dropped, a viewer cannot use them.
func outputOrder(info *jp2Info, channels int) ([]int, bool) {
	identity := make([]int, channels)
	for i := range identity {
		identity[i] = i
	}
	if len(info.cdef) == 0 {
		return identity, false
	}
	colours := make([]int, channels+1)
	for i := range colours {
		colours[i] = -1
	}
	alpha := -1
	count := 0
	for _, e := range info.cdef {
		if e.channel >= channels {
			return identity, false
		}
		switch e.typ {
		case 0:
			if e.assoc < 1 || e.assoc > channels || colours[e.assoc] >= 0 {
				return identity, false
			}
			colours[e.assoc] = e.channel
			count++
		case 1, 2:
			if alpha < 0 {
				alpha = e.channel
			}
		}
	}
	if count == 0 {
		return identity, false
	}
	out := make([]int, 0, channels)
	for a := 1; a <= count; a++ {
		if colours[a] < 0 {
			return identity, false
		}
		out = append(out, colours[a])
	}
	if alpha >= 0 {
		out = append(out, alpha)
	}
	return out, alpha >= 0
}
