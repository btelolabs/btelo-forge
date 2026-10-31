package scene3d

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// The FBX file format at its lowest: a tree of named nodes, each with a list of typed values. Numbers come back as int64
// and float64, lists as []int64 and []float64, words as string and raw data as []byte, from a binary or a text file alike.

const fbxMagic = "Kaydara FBX Binary  \x00\x1a\x00"

// fbxNode is a node of the file.
type fbxNode struct {
	Name     string
	Props    []any
	Children []*fbxNode
}

// child is the first child of that name, or nil.
func (n *fbxNode) child(name string) *fbxNode {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// floats is the numbers in the node's values.
func (n *fbxNode) floats() []float64 {
	if n == nil {
		return nil
	}
	var out []float64
	for _, p := range n.Props {
		switch v := p.(type) {
		case []float64:
			out = append(out, v...)
		case []int64:
			for _, i := range v {
				out = append(out, float64(i))
			}
		case float64:
			out = append(out, v)
		case int64:
			out = append(out, float64(v))
		}
	}
	return out
}

// ints is the whole numbers in the node's values.
func (n *fbxNode) ints() []int64 {
	if n == nil {
		return nil
	}
	var out []int64
	for _, p := range n.Props {
		switch v := p.(type) {
		case []int64:
			out = append(out, v...)
		case []float64:
			for _, f := range v {
				out = append(out, int64(f))
			}
		case int64:
			out = append(out, v)
		case float64:
			out = append(out, int64(v))
		}
	}
	return out
}

// word is the node's first word.
func (n *fbxNode) word() string {
	if n != nil {
		for _, p := range n.Props {
			if s, ok := p.(string); ok {
				return s
			}
		}
	}
	return ""
}

// parseFBX reads a file, binary or text, as the list of its top nodes.
func parseFBX(data []byte) (*fbxNode, int, error) {
	if bytes.HasPrefix(data, []byte(fbxMagic)) {
		return parseBinaryFBX(data)
	}
	return parseTextFBX(data)
}

type fbxReader struct {
	data    []byte
	wide    bool // offsets are 64 bits, from version 7500
	version int
}

func parseBinaryFBX(data []byte) (*fbxNode, int, error) {
	if len(data) < 27 {
		return nil, 0, fmt.Errorf("the FBX file is cut short")
	}
	version := int(binary.LittleEndian.Uint32(data[23:]))
	r := &fbxReader{data: data, wide: version >= 7500, version: version}
	top := &fbxNode{}
	at := 27
	for at < len(data) {
		n, next, err := r.node(at)
		if err != nil {
			return nil, 0, err
		}
		if n == nil {
			break
		}
		top.Children = append(top.Children, n)
		at = next
	}
	return top, version, nil
}

func (r *fbxReader) node(at int) (*fbxNode, int, error) {
	head := 13
	if r.wide {
		head = 25
	}
	if at+head > len(r.data) {
		return nil, 0, fmt.Errorf("the FBX file is cut short")
	}
	var end, count, length uint64
	var nameLen int
	if r.wide {
		end = binary.LittleEndian.Uint64(r.data[at:])
		count = binary.LittleEndian.Uint64(r.data[at+8:])
		length = binary.LittleEndian.Uint64(r.data[at+16:])
		nameLen = int(r.data[at+24])
	} else {
		end = uint64(binary.LittleEndian.Uint32(r.data[at:]))
		count = uint64(binary.LittleEndian.Uint32(r.data[at+4:]))
		length = uint64(binary.LittleEndian.Uint32(r.data[at+8:]))
		nameLen = int(r.data[at+12])
	}
	if end == 0 {
		return nil, at + head, nil
	}
	if end > uint64(len(r.data)) || at+head+nameLen > len(r.data) {
		return nil, 0, fmt.Errorf("a node of the FBX file runs past its end")
	}
	n := &fbxNode{Name: string(r.data[at+head : at+head+nameLen])}
	p := at + head + nameLen
	props := p
	for range count {
		v, next, err := r.prop(p)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", n.Name, err)
		}
		n.Props = append(n.Props, v)
		p = next
	}
	p = props + int(length)
	for uint64(p) < end {
		c, next, err := r.node(p)
		if err != nil {
			return nil, 0, err
		}
		if c == nil {
			break
		}
		n.Children = append(n.Children, c)
		p = next
	}
	return n, int(end), nil
}

func (r *fbxReader) prop(at int) (any, int, error) {
	d := r.data
	if at >= len(d) {
		return nil, 0, fmt.Errorf("a value runs past the end")
	}
	kind := d[at]
	at++
	need := func(n int) bool { return at+n <= len(d) }
	cut := fmt.Errorf("a value runs past the end")
	switch kind {
	case 'Y':
		if !need(2) {
			return nil, 0, cut
		}
		return int64(int16(binary.LittleEndian.Uint16(d[at:]))), at + 2, nil
	case 'C':
		if !need(1) {
			return nil, 0, cut
		}
		return int64(d[at]), at + 1, nil
	case 'I':
		if !need(4) {
			return nil, 0, cut
		}
		return int64(int32(binary.LittleEndian.Uint32(d[at:]))), at + 4, nil
	case 'L':
		if !need(8) {
			return nil, 0, cut
		}
		return int64(binary.LittleEndian.Uint64(d[at:])), at + 8, nil
	case 'F':
		if !need(4) {
			return nil, 0, cut
		}
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(d[at:]))), at + 4, nil
	case 'D':
		if !need(8) {
			return nil, 0, cut
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(d[at:])), at + 8, nil
	case 'S', 'R':
		if !need(4) {
			return nil, 0, cut
		}
		n := int(binary.LittleEndian.Uint32(d[at:]))
		at += 4
		if n < 0 || !need(n) {
			return nil, 0, cut
		}
		if kind == 'S' {
			return string(d[at : at+n]), at + n, nil
		}
		return d[at : at+n : at+n], at + n, nil
	case 'f', 'd', 'l', 'i', 'b', 'c':
		if !need(12) {
			return nil, 0, cut
		}
		count := int(binary.LittleEndian.Uint32(d[at:]))
		encoding := binary.LittleEndian.Uint32(d[at+4:])
		size := int(binary.LittleEndian.Uint32(d[at+8:]))
		at += 12
		if size < 0 || !need(size) {
			return nil, 0, cut
		}
		raw := d[at : at+size]
		if encoding == 1 {
			z, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, 0, err
			}
			if raw, err = io.ReadAll(z); err != nil {
				return nil, 0, err
			}
		}
		v, err := fbxArray(kind, count, raw)
		return v, at + size, err
	}
	return nil, 0, fmt.Errorf("a value of unknown kind %q", kind)
}

func fbxArray(kind byte, count int, raw []byte) (any, error) {
	width := map[byte]int{'f': 4, 'd': 8, 'l': 8, 'i': 4, 'b': 1, 'c': 1}[kind]
	if count < 0 || len(raw) < count*width {
		return nil, fmt.Errorf("a list is shorter than it says")
	}
	if kind == 'f' || kind == 'd' {
		out := make([]float64, count)
		for i := range out {
			if kind == 'f' {
				out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])))
			} else {
				out[i] = math.Float64frombits(binary.LittleEndian.Uint64(raw[i*8:]))
			}
		}
		return out, nil
	}
	out := make([]int64, count)
	for i := range out {
		switch kind {
		case 'l':
			out[i] = int64(binary.LittleEndian.Uint64(raw[i*8:]))
		case 'i':
			out[i] = int64(int32(binary.LittleEndian.Uint32(raw[i*4:])))
		default:
			out[i] = int64(raw[i])
		}
	}
	return out, nil
}

// The text form: `Name: value, value {` with `; comments`, and lists as `*count { a: 1,2,3 }`.

type fbxText struct {
	s  string
	at int
}

func parseTextFBX(data []byte) (*fbxNode, int, error) {
	if !bytes.Contains(data[:min(len(data), 4096)], []byte("FBXHeaderExtension")) && !bytes.Contains(data[:min(len(data), 4096)], []byte("FBX ")) {
		return nil, 0, fmt.Errorf("this is not an FBX file")
	}
	t := &fbxText{s: string(data)}
	top := &fbxNode{}
	if err := t.block(top, true); err != nil {
		return nil, 0, err
	}
	version := 7000
	if v := top.child("FBXHeaderExtension").child("FBXVersion"); v != nil && len(v.Props) > 0 {
		if i, ok := v.Props[0].(int64); ok {
			version = int(i)
		}
	}
	return top, version, nil
}

// skip passes spaces and comments.
func (t *fbxText) skip() {
	for t.at < len(t.s) {
		switch c := t.s[t.at]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			t.at++
		case c == ';':
			if end := strings.IndexByte(t.s[t.at:], '\n'); end >= 0 {
				t.at += end
			} else {
				t.at = len(t.s)
			}
		default:
			return
		}
	}
}

func (t *fbxText) block(parent *fbxNode, top bool) error {
	for {
		t.skip()
		if t.at >= len(t.s) {
			if top {
				return nil
			}
			return fmt.Errorf("the FBX file is cut short")
		}
		if t.s[t.at] == '}' {
			t.at++
			return nil
		}
		colon := strings.IndexByte(t.s[t.at:], ':')
		if colon < 0 {
			return fmt.Errorf("the FBX file has a broken line")
		}
		n := &fbxNode{Name: strings.TrimSpace(t.s[t.at : t.at+colon])}
		t.at += colon + 1
		if err := t.values(n); err != nil {
			return err
		}
		parent.Children = append(parent.Children, n)
	}
}

func (t *fbxText) values(n *fbxNode) error {
	for {
		for t.at < len(t.s) && (t.s[t.at] == ' ' || t.s[t.at] == '\t') {
			t.at++
		}
		if t.at >= len(t.s) {
			return nil
		}
		switch c := t.s[t.at]; {
		case c == '{':
			t.at++
			return t.block(n, false)
		case c == '\r' || c == '\n' || c == ';' || c == '}':
			return nil
		case c == ',':
			t.at++
			t.skip()
		case c == '"':
			end := strings.IndexByte(t.s[t.at+1:], '"')
			if end < 0 {
				return fmt.Errorf("the FBX file has a word with no end")
			}
			n.Props = append(n.Props, t.s[t.at+1:t.at+1+end])
			t.at += end + 2
		case c == '*':
			if err := t.list(n); err != nil {
				return err
			}
		default:
			start := t.at
			for t.at < len(t.s) && !strings.ContainsRune(", \t\r\n{};", rune(t.s[t.at])) {
				t.at++
			}
			word := t.s[start:t.at]
			if i, err := strconv.ParseInt(word, 10, 64); err == nil {
				n.Props = append(n.Props, i)
			} else if f, err := strconv.ParseFloat(word, 64); err == nil {
				n.Props = append(n.Props, f)
			} else {
				n.Props = append(n.Props, word)
			}
		}
	}
}

// list reads `*count { a: 1,2,3 }` into one value.
func (t *fbxText) list(n *fbxNode) error {
	open := strings.IndexByte(t.s[t.at:], '{')
	if open < 0 {
		return fmt.Errorf("the FBX file has a broken list")
	}
	shut := strings.IndexByte(t.s[t.at+open:], '}')
	if shut < 0 {
		return fmt.Errorf("the FBX file has a broken list")
	}
	body := t.s[t.at+open+1 : t.at+open+shut]
	t.at += open + shut + 1
	if c := strings.IndexByte(body, ':'); c >= 0 {
		body = body[c+1:]
	}
	var out []float64
	whole := true
	for _, w := range strings.FieldsFunc(body, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' || r == '\n' }) {
		f, err := strconv.ParseFloat(w, 64)
		if err != nil {
			return fmt.Errorf("the FBX file has a broken number %q", w)
		}
		whole = whole && !strings.ContainsAny(w, ".eE")
		out = append(out, f)
	}
	if whole {
		ints := make([]int64, len(out))
		for i, f := range out {
			ints[i] = int64(f)
		}
		n.Props = append(n.Props, ints)
		return nil
	}
	n.Props = append(n.Props, out)
	return nil
}

// fbxWriter writes the binary form of version 7400.
const fbxVersion = 7400

func (n *fbxNode) writeTo(b *bytes.Buffer) {
	at := b.Len()
	b.Write(make([]byte, 12))
	b.WriteByte(byte(len(n.Name)))
	b.WriteString(n.Name)
	props := b.Len()
	for _, p := range n.Props {
		writeFBXValue(b, p)
	}
	length := b.Len() - props
	for _, c := range n.Children {
		c.writeTo(b)
	}
	if len(n.Children) > 0 || len(n.Props) == 0 {
		b.Write(make([]byte, 13))
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint32(out[at:], uint32(b.Len()))
	binary.LittleEndian.PutUint32(out[at+4:], uint32(len(n.Props)))
	binary.LittleEndian.PutUint32(out[at+8:], uint32(length))
}

func writeFBXValue(b *bytes.Buffer, p any) {
	le := binary.LittleEndian
	switch v := p.(type) {
	case bool:
		b.WriteByte('C')
		if v {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
	case int32:
		b.WriteByte('I')
		binary.Write(b, le, v)
	case int64:
		b.WriteByte('L')
		binary.Write(b, le, v)
	case float64:
		b.WriteByte('D')
		binary.Write(b, le, v)
	case string:
		b.WriteByte('S')
		binary.Write(b, le, uint32(len(v)))
		b.WriteString(v)
	case []byte:
		b.WriteByte('R')
		binary.Write(b, le, uint32(len(v)))
		b.Write(v)
	case []float64:
		writeFBXList(b, 'd', len(v), v)
	case []float32:
		writeFBXList(b, 'f', len(v), v)
	case []int32:
		writeFBXList(b, 'i', len(v), v)
	case []int64:
		writeFBXList(b, 'l', len(v), v)
	default:
		panic(fmt.Sprintf("an FBX value of type %T", p))
	}
}

// writeFBXList writes a list, packed when there is enough of it for that to pay.
func writeFBXList(b *bytes.Buffer, kind byte, count int, values any) {
	var raw bytes.Buffer
	binary.Write(&raw, binary.LittleEndian, values)
	data, encoding := raw.Bytes(), uint32(0)
	if len(data) > 128 {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		w.Write(data)
		w.Close()
		data, encoding = z.Bytes(), 1
	}
	b.WriteByte(kind)
	binary.Write(b, binary.LittleEndian, []uint32{uint32(count), encoding, uint32(len(data))})
	b.Write(data)
}

var (
	fbxFooterID  = []byte{0xfa, 0xbc, 0xab, 0x09, 0xd0, 0xc8, 0xd4, 0x66, 0xb1, 0x76, 0xfb, 0x83, 0x1c, 0xf7, 0x26, 0x7e}
	fbxFooterEnd = []byte{0xf8, 0x5a, 0x8c, 0x6a, 0xde, 0xf5, 0xd9, 0x7e, 0xec, 0xe9, 0x0c, 0xe3, 0x75, 0x8f, 0x29, 0x0b}
)

// encodeFBX writes the top nodes as a whole file.
func encodeFBX(top []*fbxNode) []byte {
	var b bytes.Buffer
	b.WriteString(fbxMagic)
	binary.Write(&b, binary.LittleEndian, uint32(fbxVersion))
	for _, n := range top {
		n.writeTo(&b)
	}
	b.Write(make([]byte, 13))
	b.Write(fbxFooterID)
	b.Write(make([]byte, 4))
	b.Write(make([]byte, (16-(b.Len()%16))%16))
	binary.Write(&b, binary.LittleEndian, uint32(fbxVersion))
	b.Write(make([]byte, 120))
	b.Write(fbxFooterEnd)
	return b.Bytes()
}
