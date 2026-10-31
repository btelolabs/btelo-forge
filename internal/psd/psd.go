// Package psd reads and writes Photoshop's file format as far as a picture of layers goes: the pixels of each layer,
// its name, blend mode, opacity, visibility and mask, groups, and the merged picture. It knows nothing of the canvas;
// internal/paint turns a File into a canvas and back. The format is documented by Adobe in its Photoshop File Formats
// Specification.
package psd

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"unicode/utf16"
)

// ErrNotPSD means the file is not a Photoshop file.
var ErrNotPSD = errors.New("not a Photoshop file")

// ErrUnsupported means the file is one this package does not read: its colours are not RGB or grey, or it is the
// large document format, or it has 32 bits a channel.
var ErrUnsupported = errors.New("this Photoshop file uses a kind of colour or size that cannot be opened")

// MaxSide is the longest side of a picture in the format this package reads and writes.
const MaxSide = 30000

// Layer is a layer or, with Children or Group set, a group; Children are in the order of the panel, top first.
type Layer struct {
	Name     string
	Bounds   image.Rectangle
	Pix      []uint8 // four a pixel over Bounds, not premultiplied; empty for a group
	Visible  bool
	Opacity  uint8
	Blend    string // the four letters the format names a blend mode with
	Clipped  bool   // clipped to the layer below
	Mask     *image.Alpha
	Fill     uint8 // what the mask is beyond its rectangle
	MaskOff  bool
	Group    bool
	Open     bool
	Children []*Layer
	Adjust   bool // an adjustment layer, which holds settings this package does not read
	Effects  bool // it has effects, which this package does not read
	divider  bool // the record that ends a group in the file
}

// File is a picture: its size, layers top first, and the merged picture when it holds one.
type File struct {
	W, H   int
	Layers []*Layer
	Merged *image.NRGBA
}

const (
	sectionOther = iota
	sectionOpen
	sectionClosed
	sectionDivider
)

// reader reads big-endian numbers and remembers the first failure.
type reader struct {
	r   io.Reader
	err error
}

func (r *reader) bytes(n int) []byte {
	if r.err != nil || n < 0 {
		if n < 0 {
			r.err = ErrNotPSD
		}
		return nil
	}
	var b bytes.Buffer
	if _, err := io.CopyN(&b, r.r, int64(n)); err != nil {
		r.err = err
		return nil
	}
	return b.Bytes()
}

func (r *reader) u8() uint8 {
	if b := r.bytes(1); len(b) == 1 {
		return b[0]
	}
	return 0
}

func (r *reader) u16() uint16 {
	if b := r.bytes(2); len(b) == 2 {
		return binary.BigEndian.Uint16(b)
	}
	return 0
}

func (r *reader) u32() uint32 {
	if b := r.bytes(4); len(b) == 4 {
		return binary.BigEndian.Uint32(b)
	}
	return 0
}

func (r *reader) i32() int { return int(int32(r.u32())) }

// channelInfo is a channel of a layer record: which, and how many bytes its data is.
type channelInfo struct {
	id   int16
	size uint32
}

// record is a layer's entry in the file, in the file's order, bottom first.
type record struct {
	layer    *Layer
	channels []channelInfo
	maskRect image.Rectangle
	section  uint32
}

// Decode reads a file.
func Decode(in io.Reader) (*File, error) {
	r := &reader{r: bufio.NewReaderSize(in, 1<<16)}
	if sig := r.bytes(4); r.err != nil || string(sig) != "8BPS" {
		return nil, ErrNotPSD
	}
	version := r.u16()
	r.bytes(6)
	channels := int(r.u16())
	h, w := int(r.u32()), int(r.u32())
	depth, mode := int(r.u16()), r.u16()
	switch {
	case r.err != nil:
		return nil, r.err
	case version != 1, mode != 3 && mode != 1, depth != 8 && depth != 16:
		return nil, ErrUnsupported
	case w <= 0 || h <= 0 || w > MaxSide || h > MaxSide || channels < 1 || channels > 56:
		return nil, ErrNotPSD
	}
	r.bytes(int(r.u32())) // the colour mode's data
	r.bytes(int(r.u32())) // image resources
	f := &File{W: w, H: h}
	if left := int(r.u32()); left > 0 {
		section := &reader{r: bytes.NewReader(r.bytes(left))}
		if r.err == nil {
			if err := f.readLayers(section, depth, mode); err != nil {
				return nil, err
			}
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	merged, err := readMerged(r, w, h, channels, depth, mode)
	if err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) || len(f.Layers) == 0 {
			return nil, err
		}
	}
	f.Merged = merged
	if len(f.Layers) == 0 {
		// A flat file: its merged picture is its one layer.
		f.Layers = []*Layer{{Name: "Background", Bounds: image.Rect(0, 0, w, h), Pix: merged.Pix, Visible: true, Opacity: 255, Blend: "norm"}}
	}
	return f, nil
}

// readLayers reads the layer and mask section.
func (f *File) readLayers(r *reader, depth int, mode uint16) error {
	size := int(r.u32())
	if size == 0 {
		// Files of 16 bits keep their layers in a block further on.
		size = -1
		r.bytes(int(r.u32())) // the global mask
		for r.err == nil {
			sig := r.bytes(4)
			if r.err != nil {
				r.err = nil
				return nil
			}
			if s := string(sig); s != "8BIM" && s != "8B64" {
				return ErrNotPSD
			}
			key := string(r.bytes(4))
			n := int(r.u32())
			if key == "Lr16" || key == "Lr32" {
				if key == "Lr32" {
					return ErrUnsupported
				}
				return f.readLayerInfo(&reader{r: bytes.NewReader(r.bytes(n))}, depth, mode)
			}
			r.bytes(n + n%2)
		}
		return r.err
	}
	return f.readLayerInfo(&reader{r: bytes.NewReader(r.bytes(size))}, depth, mode)
}

func (f *File) readLayerInfo(r *reader, depth int, mode uint16) error {
	count := int(int16(r.u16()))
	if count < 0 {
		count = -count
	}
	records := make([]*record, 0, count)
	for range count {
		rec, err := readRecord(r)
		if err != nil {
			return err
		}
		records = append(records, rec)
	}
	for _, rec := range records {
		if err := readChannels(r, rec, depth, mode); err != nil {
			return err
		}
	}
	if r.err != nil {
		return r.err
	}
	// The file goes bottom first; a divider opens a group, which its folder's record closes.
	stack := [][]*Layer{nil}
	for _, rec := range records {
		top := len(stack) - 1
		switch rec.section {
		case sectionDivider:
			stack = append(stack, nil)
		case sectionOpen, sectionClosed:
			if top == 0 {
				return ErrNotPSD
			}
			rec.layer.Group, rec.layer.Open, rec.layer.Children = true, rec.section == sectionOpen, stack[top]
			stack = stack[:top]
			stack[top-1] = append([]*Layer{rec.layer}, stack[top-1]...)
		default:
			stack[top] = append([]*Layer{rec.layer}, stack[top]...)
		}
	}
	for len(stack) > 1 {
		// A divider without its folder: its layers stay, on the level below.
		top := len(stack) - 1
		stack[top-1] = append(stack[top], stack[top-1]...)
		stack = stack[:top]
	}
	f.Layers = stack[0]
	return nil
}

func readRecord(r *reader) (*record, error) {
	top, left, bottom, right := r.i32(), r.i32(), r.i32(), r.i32()
	l := &Layer{Bounds: image.Rect(left, top, right, bottom)}
	rec := &record{layer: l}
	n := int(r.u16())
	if n > 56 {
		return nil, ErrNotPSD
	}
	for range n {
		id := int16(r.u16())
		rec.channels = append(rec.channels, channelInfo{id, r.u32()})
	}
	if sig := r.bytes(4); r.err != nil || string(sig) != "8BIM" {
		return nil, ErrNotPSD
	}
	l.Blend = string(r.bytes(4))
	l.Opacity = r.u8()
	l.Clipped = r.u8() != 0
	flags := r.u8()
	l.Visible = flags&2 == 0
	r.u8()
	extra := &reader{r: bytes.NewReader(r.bytes(int(r.u32())))}
	if size := int(extra.u32()); size >= 18 {
		mask := &reader{r: bytes.NewReader(extra.bytes(size))}
		t, ml, b, mr := mask.i32(), mask.i32(), mask.i32(), mask.i32()
		rec.maskRect = image.Rect(ml, t, mr, b)
		l.Fill = mask.u8()
		l.MaskOff = mask.u8()&2 != 0
	} else {
		extra.bytes(size)
	}
	extra.bytes(int(extra.u32())) // the blending ranges
	nameLen := int(extra.u8())
	l.Name = string(extra.bytes(nameLen))
	extra.bytes(3 - nameLen%4)
	for extra.err == nil {
		sig := extra.bytes(4)
		if extra.err != nil {
			break
		}
		if s := string(sig); s != "8BIM" && s != "8B64" {
			break
		}
		key := string(extra.bytes(4))
		data := extra.bytes(int(extra.u32()))
		extra.bytes(len(data) % 2)
		block := &reader{r: bytes.NewReader(data)}
		switch key {
		case "luni":
			chars := make([]uint16, block.u32())
			for i := range chars {
				chars[i] = block.u16()
			}
			if block.err == nil {
				l.Name = string(utf16.Decode(chars))
			}
		case "lsct", "lsdk":
			rec.section = block.u32()
			if len(data) >= 12 {
				block.bytes(4)
				l.Blend = string(block.bytes(4))
			}
		case "levl", "curv", "brit", "hue ", "hue2", "blnc", "blwh", "vibA", "selc", "phfl", "mixr", "clrL", "nvrt", "post",
			"thrs", "grdm", "expA", "SoCo", "GdFl", "PtFl":
			l.Adjust = true
		case "lrFX", "lfx2", "lfxs":
			l.Effects = true
		}
	}
	return rec, r.err
}

// readChannels reads a layer's channels into its pixels and mask.
func readChannels(r *reader, rec *record, depth int, mode uint16) error {
	l := rec.layer
	w, h := l.Bounds.Dx(), l.Bounds.Dy()
	if w < 0 || h < 0 || w > 2*MaxSide || h > 2*MaxSide {
		return ErrNotPSD
	}
	pixels := make([]uint8, w*h*4)
	for i := 3; i < len(pixels); i += 4 {
		pixels[i] = 255
	}
	hasColour := false
	for _, ch := range rec.channels {
		data := r.bytes(int(ch.size))
		if r.err != nil {
			return r.err
		}
		if ch.id == -2 || ch.id == -3 {
			mw, mh := rec.maskRect.Dx(), rec.maskRect.Dy()
			if ch.id == -3 || mw <= 0 || mh <= 0 || l.Mask != nil {
				continue // the user's mask is enough
			}
			pix, err := readPlane(data, mw, mh, depth)
			if err != nil {
				return err
			}
			l.Mask = &image.Alpha{Pix: pix, Stride: mw, Rect: rec.maskRect}
			continue
		}
		if w == 0 || h == 0 || ch.id < -1 || ch.id > 2 {
			continue
		}
		pix, err := readPlane(data, w, h, depth)
		if err != nil {
			return err
		}
		hasColour = true
		if ch.id == -1 {
			for i, v := range pix {
				pixels[4*i+3] = v
			}
			continue
		}
		if mode == 1 {
			for i, v := range pix {
				pixels[4*i], pixels[4*i+1], pixels[4*i+2] = v, v, v
			}
			continue
		}
		for i, v := range pix {
			pixels[4*i+int(ch.id)] = v
		}
	}
	if hasColour {
		l.Pix = pixels
	} else {
		l.Bounds = image.Rectangle{}
	}
	return nil
}

// readPlane decodes one channel's data, which starts with how it is compressed.
func readPlane(data []byte, w, h, depth int) ([]uint8, error) {
	if len(data) < 2 {
		return make([]uint8, w*h), nil
	}
	bytesEach := depth / 8
	rowBytes := w * bytesEach
	body := data[2:]
	switch binary.BigEndian.Uint16(data) {
	case 0:
		if len(body) < rowBytes*h {
			return nil, ErrNotPSD
		}
	case 1:
		if len(body) < h*2 {
			return nil, ErrNotPSD
		}
		rows := body[h*2:]
		out := make([]byte, 0, rowBytes*h)
		for y := range h {
			n := int(binary.BigEndian.Uint16(body[y*2:]))
			if n > len(rows) {
				return nil, ErrNotPSD
			}
			var err error
			if out, err = unpackBits(out, rows[:n], rowBytes); err != nil {
				return nil, err
			}
			rows = rows[n:]
		}
		body = out
	default:
		return nil, ErrUnsupported // ZIP compression, which only files of 16 and 32 bits use
	}
	if bytesEach == 1 {
		return body[:w*h], nil
	}
	out := make([]uint8, w*h)
	for i := range out {
		out[i] = body[i*2]
	}
	return out, nil
}

// unpackBits adds a row of size bytes decoded from PackBits to out.
func unpackBits(out, in []byte, size int) ([]byte, error) {
	end := len(out) + size
	for len(in) > 0 && len(out) < end {
		n := int(int8(in[0]))
		in = in[1:]
		switch {
		case n >= 0:
			if n+1 > len(in) {
				return nil, ErrNotPSD
			}
			out = append(out, in[:n+1]...)
			in = in[n+1:]
		case n > -128:
			if len(in) == 0 {
				return nil, ErrNotPSD
			}
			for range 1 - n {
				out = append(out, in[0])
			}
			in = in[1:]
		}
	}
	if len(out) != end {
		return nil, ErrNotPSD
	}
	return out, nil
}

// readMerged reads the merged picture at the end of the file.
func readMerged(r *reader, w, h, channels, depth int, mode uint16) (*image.NRGBA, error) {
	compression := r.u16()
	if r.err != nil {
		return nil, r.err
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	bytesEach := depth / 8
	rowBytes := w * bytesEach
	var counts []byte
	if compression == 1 {
		counts = r.bytes(2 * h * channels)
	} else if compression != 0 {
		return nil, ErrUnsupported
	}
	if r.err != nil {
		return nil, r.err
	}
	colours := 3
	if mode == 1 {
		colours = 1
	}
	plane := make([]byte, 0, rowBytes*h)
	for c := range min(channels, colours+1) {
		plane = plane[:0]
		for y := range h {
			if compression == 0 {
				plane = append(plane, r.bytes(rowBytes)...)
			} else {
				row := r.bytes(int(binary.BigEndian.Uint16(counts[(c*h+y)*2:])))
				if r.err != nil {
					return nil, r.err
				}
				var err error
				if plane, err = unpackBits(plane, row, rowBytes); err != nil {
					return nil, err
				}
			}
		}
		if r.err != nil {
			return nil, r.err
		}
		for i := range w * h {
			v := plane[i*bytesEach]
			switch {
			case c == colours:
				img.Pix[4*i+3] = v
			case colours == 1:
				img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2] = v, v, v
			default:
				img.Pix[4*i+c] = v
			}
		}
	}
	return img, nil
}

// writer writes big-endian numbers and remembers the first failure.
type writer struct {
	w   *bufio.Writer
	err error
}

func (w *writer) bytes(b []byte) {
	if w.err == nil {
		_, w.err = w.w.Write(b)
	}
}

func (w *writer) u16(v uint16) { w.bytes(binary.BigEndian.AppendUint16(nil, v)) }
func (w *writer) u32(v uint32) { w.bytes(binary.BigEndian.AppendUint32(nil, v)) }
func (w *writer) rect(r image.Rectangle) {
	for _, v := range []int{r.Min.Y, r.Min.X, r.Max.Y, r.Max.X} {
		w.u32(uint32(int32(v)))
	}
}

// packRows compresses rows of width w, a value every step bytes from offset, with PackBits, with the row sizes before them, as one channel of a layer holds
// them: the compression, then the sizes, then the rows.
func packRows(pix []uint8, stride, step, offset, w, h int) []byte {
	var sizes, rows []byte
	for y := range h {
		before := len(rows)
		line := make([]byte, w)
		for x := range w {
			line[x] = pix[y*stride+x*step+offset]
		}
		rows = packBits(rows, line)
		sizes = binary.BigEndian.AppendUint16(sizes, uint16(len(rows)-before))
	}
	return append(append([]byte{0, 1}, sizes...), rows...)
}

// packBits adds a row compressed with PackBits to out.
func packBits(out, row []byte) []byte {
	for i := 0; i < len(row); {
		run := 1
		for i+run < len(row) && row[i+run] == row[i] && run < 128 {
			run++
		}
		if run >= 2 {
			out = append(out, byte(1-run), row[i])
			i += run
			continue
		}
		start := i
		for i < len(row) && i-start < 128 && (i+1 >= len(row) || row[i+1] != row[i]) {
			i++
		}
		if i == start {
			i++
		}
		out = append(out, byte(i-start-1))
		out = append(out, row[start:i]...)
	}
	return out
}

// flatten lists layers as the file does, bottom first, with a divider and a folder record around each group.
func flatten(layers []*Layer, out []*Layer) []*Layer {
	for i := len(layers) - 1; i >= 0; i-- {
		l := layers[i]
		if l.Group {
			out = append(out, &Layer{Name: "</Layer group>", Blend: "norm", Opacity: 255, divider: true})
			out = flatten(l.Children, out)
		}
		out = append(out, l)
	}
	return out
}

// Encode writes the picture as a file of 8-bit RGB with transparency. f.Merged must be the size of the picture.
func Encode(out io.Writer, f *File) error {
	if f.W <= 0 || f.H <= 0 || f.W > MaxSide || f.H > MaxSide {
		return fmt.Errorf("a Photoshop file's sides can be up to %d pixels", MaxSide)
	}
	w := &writer{w: bufio.NewWriterSize(out, 1<<16)}
	w.bytes([]byte("8BPS"))
	w.u16(1)
	w.bytes(make([]byte, 6))
	w.u16(4)
	w.u32(uint32(f.H))
	w.u32(uint32(f.W))
	w.u16(8)
	w.u16(3)
	w.u32(0) // no colour mode data
	w.u32(0) // no image resources
	section := layerSection(f)
	w.u32(uint32(len(section)))
	w.bytes(section)
	// The merged picture: one compression for all, then all the rows' sizes, then all the rows.
	w.u16(1)
	var sizes, rows []byte
	stride := f.Merged.Stride
	for c := range 4 {
		line := make([]byte, f.W)
		for y := range f.H {
			for x := range f.W {
				line[x] = f.Merged.Pix[y*stride+x*4+c]
			}
			before := len(rows)
			rows = packBits(rows, line)
			sizes = binary.BigEndian.AppendUint16(sizes, uint16(len(rows)-before))
		}
	}
	w.bytes(sizes)
	w.bytes(rows)
	if w.err != nil {
		return w.err
	}
	return w.w.Flush()
}

// layerSection is the layer and mask section's contents.
func layerSection(f *File) []byte {
	var info, data bytes.Buffer
	layers := flatten(f.Layers, nil)
	num := &writer{w: bufio.NewWriter(&info)}
	num.u16(uint16(int16(-len(layers)))) // negative: the first alpha is the merged picture's transparency
	for _, l := range layers {
		var channels [][]byte
		var ids []int16
		w, h := l.Bounds.Dx(), l.Bounds.Dy()
		if len(l.Pix) > 0 {
			for c, id := range []int16{-1, 0, 1, 2} {
				offset := []int{3, 0, 1, 2}[c]
				ids = append(ids, id)
				channels = append(channels, packRows(l.Pix, w*4, 4, offset, w, h))
			}
		}
		if l.Mask != nil {
			ids = append(ids, -2)
			channels = append(channels, packRows(l.Mask.Pix, l.Mask.Stride, 1, 0, l.Mask.Rect.Dx(), l.Mask.Rect.Dy()))
		}
		bounds := l.Bounds
		if len(l.Pix) == 0 {
			bounds = image.Rectangle{}
		}
		num.rect(bounds)
		num.u16(uint16(len(ids)))
		for i, id := range ids {
			num.u16(uint16(id))
			num.u32(uint32(len(channels[i])))
		}
		num.bytes([]byte("8BIM"))
		num.bytes([]byte(l.Blend))
		num.bytes([]byte{l.Opacity, 0})
		flags := byte(8) // bit 3 says bit 4 means something; every modern writer sets it
		if !l.Visible && !l.divider {
			flags |= 2
		}
		num.bytes([]byte{flags, 0})
		extra := extraData(l)
		num.u32(uint32(len(extra)))
		num.bytes(extra)
		for _, c := range channels {
			data.Write(c)
		}
	}
	num.w.Flush()
	info.Write(data.Bytes())
	if info.Len()%2 == 1 {
		info.WriteByte(0)
	}
	var out bytes.Buffer
	sec := &writer{w: bufio.NewWriter(&out)}
	sec.u32(uint32(info.Len()))
	sec.bytes(info.Bytes())
	sec.u32(0) // no global mask
	sec.w.Flush()
	return out.Bytes()
}

// extraData is a layer record's mask, name and tagged blocks.
func extraData(l *Layer) []byte {
	var out bytes.Buffer
	w := &writer{w: bufio.NewWriter(&out)}
	if l.Mask != nil {
		w.u32(20)
		w.rect(l.Mask.Rect)
		flags := byte(0)
		if l.MaskOff {
			flags = 2
		}
		w.bytes([]byte{l.Fill, flags, 0, 0})
	} else {
		w.u32(0)
	}
	w.u32(0) // no blending ranges
	// The name in the old, limited way, with its padding, then in full.
	ascii := make([]byte, 0, len(l.Name))
	for _, c := range l.Name {
		if c >= 0x80 {
			c = 0x3f // a question mark: the full name is in the block after
		}
		ascii = append(ascii, byte(c))
	}
	if len(ascii) > 255 {
		ascii = ascii[:255]
	}
	w.bytes([]byte{byte(len(ascii))})
	w.bytes(ascii)
	w.bytes(make([]byte, 3-len(ascii)%4))
	chars := utf16.Encode([]rune(l.Name))
	block := func(key string, data []byte) {
		w.bytes([]byte("8BIM"))
		w.bytes([]byte(key))
		w.u32(uint32(len(data)))
		w.bytes(data)
		if len(data)%2 == 1 {
			w.bytes([]byte{0})
		}
	}
	name := binary.BigEndian.AppendUint32(nil, uint32(len(chars)))
	for _, c := range chars {
		name = binary.BigEndian.AppendUint16(name, c)
	}
	block("luni", name)
	switch {
	case l.divider:
		block("lsct", binary.BigEndian.AppendUint32(nil, sectionDivider))
	case l.Group:
		kind := uint32(sectionClosed)
		if l.Open {
			kind = sectionOpen
		}
		s := binary.BigEndian.AppendUint32(nil, kind)
		s = append(s, "8BIM"...)
		s = append(s, l.Blend...)
		block("lsct", s)
	}
	w.w.Flush()
	return out.Bytes()
}
