// Package pdf reads and writes PDF files. The file's own bytes stay the model: objects parse on demand from them, an
// edit replaces whole objects in an overlay, and saving appends just those objects, so everything not touched comes
// back byte for byte, signatures included. Objects are never changed in place: an edit makes a copy with Clone, which
// also lets a page render on another goroutine while the window edits.
package pdf

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
)

// Object is one of: nil, bool, int64, float64, String, Name, Array, Dict, *Stream, Ref.
type Object any

// Name is a PDF name without its slash, escapes resolved.
type Name string

// String holds a string's bytes, decrypted.
type String string

// Array is a PDF array.
type Array []Object

// Dict is a PDF dictionary.
type Dict map[Name]Object

// Ref is an indirect reference.
type Ref struct{ Num, Gen int }

// Stream is a stream object. Raw holds the bytes as the file has them, filters applied, decrypted; a stream read
// from a file fetches them on demand through src.
type Stream struct {
	Dict Dict
	raw  []byte
	num  int // the object number and generation its bytes are encrypted under
	gen  int
	doc  *Doc // decrypts raw on demand, nil once raw is plain
}

// NewStream is a stream holding data already encoded as dict's Filter says.
func NewStream(dict Dict, data []byte) *Stream {
	d := dict.Clone()
	d["Length"] = int64(len(data))
	return &Stream{Dict: d, raw: data}
}

// Clone copies the dictionary, one level deep.
func (d Dict) Clone() Dict {
	out := make(Dict, len(d)+2)
	for k, v := range d {
		out[k] = v
	}
	return out
}

// Clone copies the array, one level deep.
func (a Array) Clone() Array { return slices.Clone(a) }

// withDict is the stream with another dictionary and the same bytes.
func (s *Stream) withDict(d Dict) *Stream {
	c := *s
	c.Dict = d
	return &c
}

// Num reads a number, integer or real.
func Num(o Object) (float64, bool) {
	switch v := o.(type) {
	case int64:
		return float64(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// Real makes a number short to write: whole values become integers.
func Real(v float64) Object {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return int64(v)
	}
	return math.Round(v*1e4) / 1e4
}

// Reals makes an array of numbers.
func Reals(v ...float64) Array {
	out := make(Array, len(v))
	for i, x := range v {
		out[i] = Real(x)
	}
	return out
}

// TextString decodes a text string: UTF-16 with a byte order mark, UTF-8 with one, or PDFDocEncoding.
func TextString(s String) string {
	b := []byte(s)
	switch {
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return utf16Text(b[2:], false)
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return utf16Text(b[2:], true)
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	}
	var sb strings.Builder
	for _, c := range b {
		sb.WriteRune(font.PDFDocEncoding[c])
	}
	return sb.String()
}

func utf16Text(b []byte, little bool) string {
	var sb strings.Builder
	for i := 0; i+1 < len(b); i += 2 {
		u := rune(b[i])<<8 | rune(b[i+1])
		if little {
			u = rune(b[i+1])<<8 | rune(b[i])
		}
		if u >= 0xD800 && u < 0xDC00 && i+3 < len(b) {
			v := rune(b[i+2])<<8 | rune(b[i+3])
			if little {
				v = rune(b[i+3])<<8 | rune(b[i+2])
			}
			if v >= 0xDC00 && v < 0xE000 {
				u = 0x10000 + (u-0xD800)<<10 + (v - 0xDC00)
				i += 2
			}
		}
		if u == 0x1B { // a language escape: ESC, the code, ESC
			for i += 2; i+1 < len(b) && !(b[i] == 0 && b[i+1] == 0x1B); i += 2 {
			}
			continue
		}
		sb.WriteRune(u)
	}
	return sb.String()
}

// EncodeText makes a text string: PDFDocEncoding where it fits, else UTF-16BE.
func EncodeText(s string) String {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 || s[i] < 0x20 && s[i] != '\n' && s[i] != '\r' && s[i] != '\t' {
			ascii = false
			break
		}
	}
	if ascii {
		return String(s)
	}
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			hi, lo := 0xD800+r>>10, 0xDC00+r&0x3FF
			out = append(out, byte(hi>>8), byte(hi), byte(lo>>8), byte(lo))
			continue
		}
		out = append(out, byte(r>>8), byte(r))
	}
	return String(out)
}

// Rect is a rectangle in PDF order: lower left, upper right, normalised.
type Rect struct{ X0, Y0, X1, Y1 float64 }

// W is the width.
func (r Rect) W() float64 { return r.X1 - r.X0 }

// H is the height.
func (r Rect) H() float64 { return r.Y1 - r.Y0 }

// Array makes the PDF form.
func (r Rect) Array() Array { return Reals(r.X0, r.Y0, r.X1, r.Y1) }

// Contains reports whether the point is inside.
func (r Rect) Contains(x, y float64) bool { return x >= r.X0 && x <= r.X1 && y >= r.Y0 && y <= r.Y1 }

// Inset moves each edge inwards.
func (r Rect) Inset(d float64) Rect { return Rect{r.X0 + d, r.Y0 + d, r.X1 - d, r.Y1 - d} }

// Union is the box around both.
func (r Rect) Union(o Rect) Rect {
	return Rect{min(r.X0, o.X0), min(r.Y0, o.Y0), max(r.X1, o.X1), max(r.Y1, o.Y1)}
}

// Normal puts the corners in order.
func (r Rect) Normal() Rect {
	return Rect{min(r.X0, r.X1), min(r.Y0, r.Y1), max(r.X0, r.X1), max(r.Y0, r.Y1)}
}

// Matrix is a PDF transformation [a b c d e f].
type Matrix [6]float64

// Identity leaves points alone.
var Identity = Matrix{1, 0, 0, 1, 0, 0}

// Mul is m applied first, then n.
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// Apply maps a point.
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// Invert is the inverse; ok is false for a singular matrix.
func (m Matrix) Invert() (Matrix, bool) {
	det := m[0]*m[3] - m[1]*m[2]
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		return Identity, false
	}
	return Matrix{m[3] / det, -m[1] / det, -m[2] / det, m[0] / det,
		(m[2]*m[5] - m[3]*m[4]) / det, (m[1]*m[4] - m[0]*m[5]) / det}, true
}

// BoxOf is the box around r after m.
func (m Matrix) BoxOf(r Rect) Rect {
	x0, y0 := m.Apply(r.X0, r.Y0)
	out := Rect{x0, y0, x0, y0}
	for _, p := range [][2]float64{{r.X1, r.Y0}, {r.X0, r.Y1}, {r.X1, r.Y1}} {
		x, y := m.Apply(p[0], p[1])
		out = out.Union(Rect{x, y, x, y})
	}
	return out
}

// fmtNum writes a number the way PDF wants it: never with an exponent.
func fmtNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', 5, 64)
	if strings.IndexByte(s, '.') >= 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

// Debug is a short description of an object, for messages.
func Debug(o Object) string { return fmt.Sprintf("%v", o) }
