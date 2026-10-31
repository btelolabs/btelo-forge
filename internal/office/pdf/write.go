package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
	"strconv"
)

// Snapshot is the document's state at one moment, to be written on another goroutine.
type Snapshot struct {
	d    *Doc
	over map[int]Object
	next int
	full bool
}

// Snapshot takes what a save needs; the document may change while it is written.
func (d *Doc) Snapshot() *Snapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	over := make(map[int]Object, len(d.over))
	for k, v := range d.over {
		over[k] = v
	}
	return &Snapshot{d: d, over: over, next: d.nextNum, full: d.structural > 0 || d.repaired}
}

// Save writes the file: the original bytes with the edits appended, or a clean rewrite when edits need one.
func (s *Snapshot) Save() ([]byte, error) {
	if s.full {
		return s.Rewrite(false)
	}
	return s.Incremental()
}

// Bytes writes the document as Save does.
func (d *Doc) Bytes() ([]byte, error) { return d.Snapshot().Save() }

type writer struct {
	buf   bytes.Buffer
	crypt *crypt
	num   int
	gen   int
	plain bool // no encryption for this object
	refs  map[int]int
	d     *Doc
}

func (w *writer) obj(o Object, depth int) {
	if depth > maxDepth*2 {
		w.buf.WriteString("null")
		return
	}
	switch v := o.(type) {
	case nil:
		w.buf.WriteString("null")
	case bool:
		w.buf.WriteString(strconv.FormatBool(v))
	case int64:
		w.buf.WriteString(strconv.FormatInt(v, 10))
	case int:
		w.buf.WriteString(strconv.Itoa(v))
	case float64:
		w.buf.WriteString(fmtNum(v))
	case Name:
		w.name(v)
	case String:
		w.str(v)
	case Ref:
		n, g := v.Num, v.Gen
		if w.refs != nil {
			m, ok := w.refs[v.Num]
			if !ok {
				w.buf.WriteString("null")
				return
			}
			n, g = m, 0
		}
		fmt.Fprintf(&w.buf, "%d %d R", n, g)
	case Array:
		w.buf.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				w.buf.WriteByte(' ')
			}
			w.obj(e, depth+1)
		}
		w.buf.WriteByte(']')
	case Dict:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		w.buf.WriteString("<<")
		for _, k := range keys {
			w.name(Name(k))
			w.buf.WriteByte(' ')
			w.obj(v[Name(k)], depth+1)
		}
		w.buf.WriteString(">>")
	case *Stream:
		w.stream(v)
	default:
		w.buf.WriteString("null")
	}
}

func (w *writer) name(n Name) {
	w.buf.WriteByte('/')
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c <= ' ' || c >= 0x7F || c == '#' || isDelim(c) {
			fmt.Fprintf(&w.buf, "#%02X", c)
		} else {
			w.buf.WriteByte(c)
		}
	}
}

func (w *writer) str(s String) {
	b := []byte(s)
	if w.crypt != nil && !w.plain {
		b = w.crypt.encrypt(w.num, w.gen, b, false)
	}
	const hex = "0123456789ABCDEF"
	w.buf.WriteByte('<')
	for _, c := range b {
		w.buf.WriteByte(hex[c>>4])
		w.buf.WriteByte(hex[c&15])
	}
	w.buf.WriteByte('>')
}

func (w *writer) stream(s *Stream) {
	raw := w.d.Raw(s)
	if w.crypt != nil && !w.plain && w.crypt.encrypted(s.Dict) {
		raw = w.crypt.encrypt(w.num, w.gen, raw, true)
	}
	dict := s.Dict.Clone()
	dict["Length"] = int64(len(raw))
	w.obj(dict, 1)
	w.buf.WriteString("\nstream\n")
	w.buf.Write(raw)
	w.buf.WriteString("\nendstream")
}

func (w *writer) indirect(num, gen int, o Object) int {
	at := w.buf.Len()
	w.num, w.gen = num, gen
	fmt.Fprintf(&w.buf, "%d %d obj\n", num, gen)
	w.obj(o, 0)
	w.buf.WriteString("\nendobj\n")
	return at
}

// Deflate compresses for FlateDecode.
func Deflate(b []byte) []byte {
	var out bytes.Buffer
	z, _ := zlib.NewWriterLevel(&out, zlib.BestCompression)
	z.Write(b)
	z.Close()
	return out.Bytes()
}

// Incremental appends the edits to the original bytes, which stay as they are.
func (s *Snapshot) Incremental() ([]byte, error) {
	d := s.d
	if len(s.over) == 0 {
		return d.data, nil
	}
	w := &writer{d: d, crypt: d.crypt}
	w.buf.Grow(len(d.data) + 1<<16)
	w.buf.Write(d.data)
	if n := len(d.data); n > 0 && d.data[n-1] != '\n' {
		w.buf.WriteByte('\n')
	}
	nums := make([]int, 0, len(s.over))
	for n := range s.over {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	type entry struct{ num, off, gen int }
	var entries []entry
	for _, n := range nums {
		w.plain = d.isEncryptDict(n)
		gen := d.gen(n)
		at := w.indirect(n, gen, s.over[n])
		entries = append(entries, entry{n, at - d.delta, gen})
	}
	w.plain = false
	size := max(s.next, nums[len(nums)-1]+1)
	trailer := Dict{"Root": d.trailer["Root"], "Prev": int64(d.startxref)}
	for _, k := range []Name{"Info", "ID", "Encrypt"} {
		if v, ok := d.trailer[k]; ok {
			trailer[k] = v
		}
	}
	at := w.buf.Len() - d.delta
	if d.xrefStream {
		entries = append(entries, entry{size, at, 0})
		size++
		var body bytes.Buffer
		index := Array{}
		for i := 0; i < len(entries); {
			j := i
			for j+1 < len(entries) && entries[j+1].num == entries[j].num+1 {
				j++
			}
			index = append(index, int64(entries[i].num), int64(j-i+1))
			for ; i <= j; i++ {
				e := entries[i]
				body.Write([]byte{1, byte(e.off >> 24), byte(e.off >> 16), byte(e.off >> 8), byte(e.off), byte(e.gen >> 8), byte(e.gen)})
			}
		}
		trailer["Type"] = Name("XRef")
		trailer["W"] = Array{int64(1), int64(4), int64(2)}
		trailer["Index"] = index
		trailer["Size"] = int64(size)
		trailer["Filter"] = Name("FlateDecode")
		w.plain = true
		w.indirect(size-1, 0, NewStream(trailer, Deflate(body.Bytes())))
	} else {
		// Object 0 is always listed: some readers take a section that does not start at it for a damaged one.
		w.buf.WriteString("xref\n0 1\n0000000000 65535 f \n")
		for i := 0; i < len(entries); {
			j := i
			for j+1 < len(entries) && entries[j+1].num == entries[j].num+1 {
				j++
			}
			fmt.Fprintf(&w.buf, "%d %d\n", entries[i].num, j-i+1)
			for ; i <= j; i++ {
				fmt.Fprintf(&w.buf, "%010d %05d n \n", entries[i].off, entries[i].gen)
			}
		}
		trailer["Size"] = int64(size)
		w.buf.WriteString("trailer\n")
		w.plain = true
		w.obj(trailer, 0)
		w.buf.WriteByte('\n')
	}
	fmt.Fprintf(&w.buf, "startxref\n%d\n%%%%EOF\n", at)
	return w.buf.Bytes(), nil
}

func (s *Snapshot) get(num int) Object {
	if o, ok := s.over[num]; ok {
		return o
	}
	d := s.d
	d.mu.RLock()
	o, ok := d.cache[num]
	d.mu.RUnlock()
	if ok {
		return o
	}
	o = d.load(num) // from the file: an overlay entry made since this snapshot is not ours
	d.mu.Lock()
	d.cache[num] = o
	d.mu.Unlock()
	return o
}

// reach lists the objects a clean file needs, in the order they are first met.
func (s *Snapshot) reach(root Dict) []int {
	seen := map[int]bool{}
	var order []int
	stack := []Object{root}
	for len(stack) > 0 {
		o := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch v := o.(type) {
		case Ref:
			if !seen[v.Num] {
				seen[v.Num] = true
				order = append(order, v.Num)
				stack = append(stack, s.get(v.Num))
			}
		case Array:
			for i := len(v) - 1; i >= 0; i-- {
				stack = append(stack, v[i])
			}
		case Dict:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, string(k))
			}
			sort.Sort(sort.Reverse(sort.StringSlice(keys)))
			for _, k := range keys {
				stack = append(stack, v[Name(k)])
			}
		case *Stream:
			stack = append(stack, v.Dict)
		}
	}
	return order
}

// Rewrite writes a clean file holding only what is reachable, with new object numbers. compress also packs objects
// into object streams and deflates the streams that are not compressed yet. Stream bytes stay as they are otherwise.
func (s *Snapshot) Rewrite(compress bool) ([]byte, error) {
	d := s.d
	roots := Dict{"Root": d.trailer["Root"]}
	if v, ok := d.trailer["Info"]; ok {
		roots["Info"] = v
	}
	enc, encrypted := d.trailer["Encrypt"]
	if encrypted {
		roots["Encrypt"] = enc
	}
	order := s.reach(roots)
	refs := make(map[int]int, len(order))
	for i, n := range order {
		refs[n] = i + 1
	}
	w := &writer{d: d, crypt: d.crypt, refs: refs}
	version := "1.7"
	if h := bytes.Index(d.data[:min(len(d.data), 4096)], []byte("%PDF-")); h >= 0 && h+8 <= len(d.data) && !compress {
		if v := string(d.data[h+5 : h+8]); v >= "1.4" && v <= "2.0" && v[1] == '.' {
			version = v
		}
	}
	w.buf.WriteString("%PDF-" + version + "\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(order)+1)
	var packed [][]int // the objects of each object stream
	var batch []int
	for _, n := range order {
		o := s.get(n)
		id := refs[n]
		if st, ok := o.(*Stream); ok {
			if compress {
				o = s.tighten(st)
			}
			w.plain = false
			offsets[id] = w.indirect(id, 0, o)
			continue
		}
		isEnc := false
		if r, ok := enc.(Ref); ok && r.Num == n {
			isEnc = true
		}
		if compress && !isEnc {
			batch = append(batch, n)
			if len(batch) == 200 {
				packed = append(packed, batch)
				batch = nil
			}
			continue
		}
		w.plain = isEnc
		offsets[id] = w.indirect(id, 0, o)
		w.plain = false
	}
	if len(batch) > 0 {
		packed = append(packed, batch)
	}
	total := len(order)
	type slot struct{ stm, index int }
	slots := map[int]slot{}
	for _, list := range packed {
		total++
		stmID := total
		var head, body bytes.Buffer
		sub := &writer{d: d, refs: refs}
		for i, n := range list {
			fmt.Fprintf(&head, "%d %d ", refs[n], body.Len())
			sub.buf.Reset()
			sub.obj(s.get(n), 0)
			body.Write(sub.buf.Bytes())
			body.WriteByte('\n')
			slots[refs[n]] = slot{stmID, i}
		}
		first := head.Len()
		head.Write(body.Bytes())
		stm := NewStream(Dict{"Type": Name("ObjStm"), "N": int64(len(list)), "First": int64(first), "Filter": Name("FlateDecode")}, Deflate(head.Bytes()))
		offsets = append(offsets, 0)
		offsets[stmID] = w.indirect(stmID, 0, stm)
	}
	trailer := Dict{"Root": d.trailer["Root"], "Size": int64(total + 1)}
	for _, k := range []Name{"Info", "ID", "Encrypt"} {
		if v, ok := d.trailer[k]; ok {
			trailer[k] = v
		}
	}
	if _, ok := trailer["ID"]; !ok {
		id := String(fmt.Sprintf("%016x", len(d.data)+total))
		trailer["ID"] = Array{id, id}
	}
	at := w.buf.Len()
	if compress {
		xrefID := total + 1
		var body bytes.Buffer
		body.Write([]byte{0, 0, 0, 0, 0, 0xFF, 0xFF})
		for id := 1; id <= xrefID; id++ {
			off := at
			if id <= total {
				off = offsets[id]
			}
			if sl, ok := slots[id]; ok {
				body.Write([]byte{2, byte(sl.stm >> 24), byte(sl.stm >> 16), byte(sl.stm >> 8), byte(sl.stm), byte(sl.index >> 8), byte(sl.index)})
				continue
			}
			body.Write([]byte{1, byte(off >> 24), byte(off >> 16), byte(off >> 8), byte(off), 0, 0})
		}
		trailer["Type"] = Name("XRef")
		trailer["W"] = Array{int64(1), int64(4), int64(2)}
		trailer["Size"] = int64(xrefID + 1)
		trailer["Filter"] = Name("FlateDecode")
		w.plain = true
		w.indirect(xrefID, 0, NewStream(trailer, Deflate(body.Bytes())))
		fmt.Fprintf(&w.buf, "startxref\n%d\n%%%%EOF\n", at)
		return w.buf.Bytes(), nil
	}
	fmt.Fprintf(&w.buf, "xref\n0 %d\n0000000000 65535 f \n", total+1)
	for id := 1; id <= total; id++ {
		fmt.Fprintf(&w.buf, "%010d 00000 n \n", offsets[id])
	}
	w.buf.WriteString("trailer\n")
	w.plain = true
	w.obj(trailer, 0)
	fmt.Fprintf(&w.buf, "\nstartxref\n%d\n%%%%EOF\n", at)
	return w.buf.Bytes(), nil
}

// tighten deflates a stream that holds no compressed data yet. Other filters are left: an image codec's data would
// only grow.
func (s *Snapshot) tighten(st *Stream) *Stream {
	d := s.d
	f := d.filters(st)
	worth := len(f) == 0
	if len(f) == 1 && f[0].Parms == nil {
		switch f[0].Name {
		case "ASCII85Decode", "ASCIIHexDecode", "LZWDecode", "RunLengthDecode":
			worth = true
		}
	}
	if !worth || st.Dict["Type"] == Name("Metadata") {
		return st
	}
	plain, err := d.Decode(st)
	if err != nil {
		return st
	}
	packed := Deflate(plain)
	if len(packed) >= len(d.Raw(st)) {
		return st
	}
	dict := st.Dict.Clone()
	dict["Filter"] = Name("FlateDecode")
	delete(dict, "DecodeParms")
	return NewStream(dict, packed)
}
