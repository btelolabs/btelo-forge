package pdf

import (
	"bytes"
	"compress/flate"
	"encoding/ascii85"
	"errors"
	"fmt"
	"io"
)

// Filter is an image filter left undone by Decode, with its parameters.
type Filter struct {
	Name  Name
	Parms Dict
}

// Raw is the stream's encoded bytes, decrypted.
func (d *Doc) Raw(s *Stream) []byte {
	if s.doc != nil && s.doc.crypt != nil {
		return s.doc.crypt.decrypt(s.num, s.gen, s.raw, true)
	}
	return s.raw
}

func (d *Doc) filters(s *Stream) []Filter {
	var names []Object
	f := d.Resolve(firstOf(s.Dict, "Filter", "F"))
	switch v := f.(type) {
	case Name:
		names = []Object{v}
	case Array:
		names = v
	}
	var parms []Object
	switch v := d.Resolve(firstOf(s.Dict, "DecodeParms", "DP")).(type) {
	case Dict:
		parms = []Object{v}
	case Array:
		parms = v
	}
	out := make([]Filter, 0, len(names))
	for i, n := range names {
		fl := Filter{Name: abbreviated(d.Name(n))}
		if i < len(parms) {
			fl.Parms = d.Dict(parms[i])
		}
		out = append(out, fl)
	}
	return out
}

func firstOf(dict Dict, keys ...Name) Object {
	for _, k := range keys {
		if v, ok := dict[k]; ok {
			return v
		}
	}
	return nil
}

func abbreviated(n Name) Name {
	switch n {
	case "Fl":
		return "FlateDecode"
	case "LZW":
		return "LZWDecode"
	case "A85":
		return "ASCII85Decode"
	case "AHx":
		return "ASCIIHexDecode"
	case "RL":
		return "RunLengthDecode"
	case "DCT":
		return "DCTDecode"
	case "CCF":
		return "CCITTFaxDecode"
	}
	return n
}

// Decode undoes every filter; it fails on an image filter. What was decoded before an error is returned with it.
func (d *Doc) Decode(s *Stream) ([]byte, error) {
	out, rest, err := d.DecodeImage(s)
	if err != nil {
		return out, err
	}
	if rest != nil {
		return nil, fmt.Errorf("pdf: stream ends in image filter %s", rest.Name)
	}
	return out, nil
}

// DecodeImage undoes the filters up to an image codec, which is returned undone for the image code to handle.
func (d *Doc) DecodeImage(s *Stream) ([]byte, *Filter, error) {
	data := d.Raw(s)
	for _, f := range d.filters(s) {
		var err error
		switch f.Name {
		case "FlateDecode":
			data, err = inflate(data)
		case "LZWDecode":
			data, err = lzwDecode(data, d.Int(f.Parms["EarlyChange"], 1))
		case "ASCII85Decode":
			data, err = a85Decode(data)
		case "ASCIIHexDecode":
			data = hexDecode(data)
		case "RunLengthDecode":
			data, err = runLength(data)
		case "Crypt":
			continue // decrypted already, or Identity
		case "DCTDecode", "JPXDecode", "CCITTFaxDecode", "JBIG2Decode":
			fl := f
			return data, &fl, nil
		default:
			return nil, nil, fmt.Errorf("pdf: unknown filter %s", f.Name)
		}
		if err != nil && len(data) == 0 {
			return nil, nil, err
		}
		if f.Name == "FlateDecode" || f.Name == "LZWDecode" {
			data = d.unpredict(data, f.Parms)
		}
		if err != nil {
			return data, nil, nil // partial data: keep what came out, as readers do
		}
	}
	return data, nil, nil
}

// inflate tolerates a damaged tail and a missing or wrong zlib header.
func inflate(b []byte) ([]byte, error) {
	try := func(src []byte) ([]byte, error) {
		r := flate.NewReader(bytes.NewReader(src))
		defer r.Close()
		var out bytes.Buffer
		_, err := io.Copy(&out, io.LimitReader(r, maxStreamSize))
		return out.Bytes(), err
	}
	for len(b) > 0 && isSpace(b[0]) {
		b = b[1:] // a stray line end before the data
	}
	if len(b) >= 2 && b[0]&0x0F == 8 && (int(b[0])<<8|int(b[1]))%31 == 0 {
		out, err := try(b[2:])
		if err == nil || len(out) > 0 {
			return out, err
		}
	}
	return try(b)
}

func lzwDecode(b []byte, early int) ([]byte, error) {
	type entry struct {
		prev int32
		ch   byte
		len  int32
	}
	table := make([]entry, 4096)
	for i := range 256 {
		table[i] = entry{-1, byte(i), 1}
	}
	var out []byte
	next, width := 258, 9
	prev := -1
	var acc uint32
	bits := 0
	scratch := make([]byte, 0, 4096)
	emit := func(code int) byte {
		n := int(table[code].len)
		if cap(scratch) < n {
			scratch = make([]byte, n)
		}
		s := scratch[:n]
		for i := n - 1; i >= 0 && code >= 0; i-- {
			s[i] = table[code].ch
			code = int(table[code].prev)
		}
		out = append(out, s...)
		return s[0]
	}
	for _, c := range b {
		acc = acc<<8 | uint32(c)
		bits += 8
		for bits >= width {
			code := int(acc>>(bits-width)) & (1<<width - 1)
			bits -= width
			switch {
			case code == 256:
				next, width, prev = 258, 9, -1
				continue
			case code == 257:
				return out, nil
			case prev < 0:
				if code >= 256 {
					return out, errors.New("pdf: bad LZW data")
				}
				out = append(out, byte(code))
			case code < next:
				first := emit(code)
				if next < 4096 {
					table[next] = entry{int32(prev), first, table[prev].len + 1}
					next++
				}
			case code == next && next < 4096:
				e := int(prev)
				for table[e].prev >= 0 {
					e = int(table[e].prev)
				}
				table[next] = entry{int32(prev), table[e].ch, table[prev].len + 1}
				next++
				emit(code)
			default:
				return out, errors.New("pdf: bad LZW data")
			}
			if len(out) > maxStreamSize {
				return out, errors.New("pdf: stream too large")
			}
			prev = code
			if next+early >= 1<<width && width < 12 {
				width++
			}
		}
	}
	return out, nil
}

func a85Decode(b []byte) ([]byte, error) {
	src := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		if isSpace(c) {
			continue
		}
		if c == '~' {
			break
		}
		if c == '<' && i+1 < len(b) && b[i+1] == '~' {
			i++
			continue
		}
		src = append(src, c)
	}
	dst := make([]byte, 0, len(src)*4/5+8)
	r := ascii85.NewDecoder(bytes.NewReader(src))
	buf := bytes.NewBuffer(dst)
	_, err := io.Copy(buf, r)
	return buf.Bytes(), err
}

func hexDecode(b []byte) []byte {
	out := make([]byte, 0, len(b)/2)
	var hi byte
	half := false
	for _, c := range b {
		if c == '>' {
			break
		}
		v, ok := hexVal(c)
		if !ok {
			continue
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return out
}

func runLength(b []byte) ([]byte, error) {
	var out []byte
	for i := 0; i < len(b); {
		n := int(b[i])
		i++
		switch {
		case n == 128:
			return out, nil
		case n < 128:
			end := min(i+n+1, len(b))
			out = append(out, b[i:end]...)
			i = end
		default:
			if i >= len(b) {
				return out, nil
			}
			out = append(out, bytes.Repeat(b[i:i+1], 257-n)...)
			i++
		}
		if len(out) > maxStreamSize {
			return out, errors.New("pdf: stream too large")
		}
	}
	return out, nil
}

func (d *Doc) unpredict(data []byte, parms Dict) []byte {
	pred := d.Int(parms["Predictor"], 1)
	if pred <= 1 {
		return data
	}
	colors := max(d.Int(parms["Colors"], 1), 1)
	bpc := max(d.Int(parms["BitsPerComponent"], 8), 1)
	columns := max(d.Int(parms["Columns"], 1), 1)
	if colors > 1024 || bpc > 32 || columns > 1<<28 {
		return data
	}
	bytesPer := max(colors*bpc/8, 1)
	row := (colors*bpc*columns + 7) / 8
	if pred == 2 {
		return tiffPredict(data, colors, bpc, columns, row)
	}
	// PNG: one tag byte a row, of any kind.
	out := make([]byte, 0, len(data))
	prev := make([]byte, row)
	cur := make([]byte, row)
	for p := 0; p < len(data); p += row + 1 {
		tag := data[p]
		n := copy(cur, data[min(p+1, len(data)):])
		clear(cur[n:])
		switch tag {
		case 1:
			for i := bytesPer; i < row; i++ {
				cur[i] += cur[i-bytesPer]
			}
		case 2:
			for i := range row {
				cur[i] += prev[i]
			}
		case 3:
			for i := range row {
				var a int
				if i >= bytesPer {
					a = int(cur[i-bytesPer])
				}
				cur[i] += byte((a + int(prev[i])) / 2)
			}
		case 4:
			for i := range row {
				var a, c int
				if i >= bytesPer {
					a, c = int(cur[i-bytesPer]), int(prev[i-bytesPer])
				}
				b := int(prev[i])
				pa, pb, pc := abs(b-c), abs(a-c), abs(a+b-2*c)
				switch {
				case pa <= pb && pa <= pc:
					cur[i] += byte(a)
				case pb <= pc:
					cur[i] += byte(b)
				default:
					cur[i] += byte(c)
				}
			}
		}
		if n > 0 {
			out = append(out, cur[:n]...)
		}
		prev, cur = cur, prev
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func tiffPredict(data []byte, colors, bpc, columns, row int) []byte {
	out := bytes.Clone(data)
	switch bpc {
	case 8:
		for p := 0; p < len(out); p += row {
			end := min(p+row, len(out))
			for i := p + colors; i < end; i++ {
				out[i] += out[i-colors]
			}
		}
	case 16:
		for p := 0; p < len(out); p += row {
			end := min(p+row, len(out))
			for i := p + 2*colors; i+1 < end; i += 2 {
				v := (int(out[i])<<8 | int(out[i+1])) + (int(out[i-2*colors])<<8 | int(out[i-2*colors+1]))
				out[i], out[i+1] = byte(v>>8), byte(v)
			}
		}
	default:
		for p := 0; p < len(out); p += row {
			end := min(p+row, len(out))
			last := make([]int, colors)
			mask := 1<<bpc - 1
			bit := 0
			for c := 0; c < columns*colors; c++ {
				i := p + bit/8
				if i >= end {
					break
				}
				shift := 8 - bpc - bit%8
				v := (int(out[i]) >> shift) & mask
				v = (v + last[c%colors]) & mask
				last[c%colors] = v
				out[i] = out[i]&^byte(mask<<shift) | byte(v<<shift)
				bit += bpc
			}
		}
	}
	return out
}
