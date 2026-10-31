package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Limits on what one file may ask for.
const (
	maxObjects    = 8 << 20 // object numbers
	maxObjStm     = 1 << 20 // objects in one object stream
	maxFollow     = 32      // references followed in a row
	maxXrefChain  = 4096    // cross-reference sections
	maxPages      = 200000
	maxStreamSize = 1 << 30 // what one stream may decode to
)

// ErrPassword means the document needs a password; try Open again with it.
var ErrPassword = errors.New("pdf: the document needs a password")

// ErrNotPDF means the file has no sign of being a PDF.
var ErrNotPDF = errors.New("pdf: not a PDF file")

type xrefEntry struct {
	offset int  // of the object in the file, or the object stream's number when packed
	gen    int  // generation, or the index in the object stream when packed
	packed bool // in an object stream
	free   bool
}

// Doc is an open document.
type Doc struct {
	data  []byte // the file as read
	delta int    // what to add to an offset the file states to get a place in data

	mu      sync.RWMutex
	xref    map[int]xrefEntry
	cache   map[int]Object
	objStms map[int]*objStm
	trailer Dict
	crypt   *crypt

	// The edits. Replacing an object puts it in over; a nil entry there means freed.
	over       map[int]Object
	hist       history
	nextNum    int
	version    uint64
	structural int // edits that need a clean rewrite to take effect, as deleting pages does

	rescues    atomic.Int32 // objects looked for by scanning, which costs a pass over the file each
	repaired   bool         // the cross-references were rebuilt by scanning, so appending to them makes no sense
	hybrid     bool         // the section being read is a hybrid file's stream
	xrefStream bool         // the newest cross-reference section is a stream
	startxref  int          // where it is, as the file states it

	fonts      FontLookup
	pages      []Page
	pagesValid bool
}

type objStm struct {
	nums    []int
	offsets []int
	body    []byte
	first   int
}

// Open reads a document from memory; data is kept and must not change. A wrong or missing password gives ErrPassword.
func Open(data []byte, password string) (d *Doc, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = nil, fmt.Errorf("pdf: the file is too damaged to read: %v", r)
		}
	}()
	d = &Doc{data: data, xref: map[int]xrefEntry{}, cache: map[int]Object{}, objStms: map[int]*objStm{}, trailer: Dict{}, over: map[int]Object{}}
	head := bytes.Index(data[:min(len(data), 4096)], []byte("%PDF-"))
	if head < 0 {
		if !bytes.Contains(data[:min(len(data), 1<<20)], []byte(" obj")) {
			return nil, ErrNotPDF
		}
		head = 0
	}
	d.delta = head
	if !d.readXref() {
		d.rebuild()
	}
	err = d.initCrypt(password)
	if errors.Is(err, ErrPassword) {
		return nil, err
	}
	if err != nil || !d.rootOK() {
		if !d.repaired {
			d.rebuild()
			err = d.initCrypt(password)
		}
		if err != nil {
			return nil, err
		}
		if !d.rootOK() && !d.findRoot() {
			return nil, errors.New("pdf: no document catalog found")
		}
	}
	d.nextNum = 1
	for n := range d.xref {
		d.nextNum = max(d.nextNum, n+1)
	}
	if s, ok := Num(d.trailer["Size"]); ok && int(s) > d.nextNum && int(s) < maxObjects {
		d.nextNum = int(s)
	}
	return d, nil
}

func (d *Doc) rootOK() bool {
	root, _ := d.Resolve(d.trailer["Root"]).(Dict)
	if root == nil {
		return false
	}
	pages, _ := d.Resolve(root["Pages"]).(Dict)
	return pages != nil
}

// findRoot looks for a catalog among all the objects.
func (d *Doc) findRoot() bool {
	for _, n := range d.objectNumbers() {
		if dict, ok := d.Resolve(Ref{n, 0}).(Dict); ok && dict["Type"] == Name("Catalog") {
			if _, ok := d.Resolve(dict["Pages"]).(Dict); ok {
				d.trailer["Root"] = Ref{n, d.xref[n].gen}
				return true
			}
		}
	}
	return false
}

func (d *Doc) objectNumbers() []int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]int, 0, len(d.xref))
	for n, e := range d.xref {
		if !e.free {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// Repaired reports that the file's cross-references were damaged and were rebuilt.
func (d *Doc) Repaired() bool { return d.repaired }

// Trailer is the trailer dictionary.
func (d *Doc) Trailer() Dict { return d.trailer }

// readXref follows the chain of cross-reference sections from the end of the file.
func (d *Doc) readXref() bool {
	tail := d.data[max(0, len(d.data)-4096):]
	at := bytes.LastIndex(tail, []byte("startxref"))
	if at < 0 {
		return false
	}
	l := &Lexer{b: tail, Pos: at + len("startxref")}
	offset, ok := l.intToken()
	if !ok {
		return false
	}
	d.startxref = offset
	seen := map[int]bool{}
	queue := []int{offset}
	first := true
	hybrid := -1
	for len(queue) > 0 && len(seen) < maxXrefChain {
		off := queue[0]
		queue = queue[1:]
		if seen[off] {
			continue
		}
		seen[off] = true
		d.hybrid = off == hybrid
		tr, ok := d.readSection(off, first)
		if !ok {
			return false
		}
		if first {
			d.trailer = tr
			first = false
		}
		// A hybrid file's stream comes before the older sections.
		if s, ok := Num(tr["XRefStm"]); ok {
			hybrid = int(s)
			queue = append([]int{hybrid}, queue...)
		}
		if p, ok := Num(tr["Prev"]); ok {
			queue = append(queue, int(p))
		}
	}
	return !first && d.trailer["Root"] != nil
}

func (d *Doc) readSection(off int, newest bool) (Dict, bool) {
	pos := off + d.delta
	if off < 0 || pos < 0 || pos >= len(d.data) {
		return nil, false
	}
	l := &Lexer{b: d.data, Pos: pos}
	if l.accept("xref") {
		if newest {
			d.xrefStream = false
		}
		return d.readTable(l)
	}
	if newest {
		d.xrefStream = true
	}
	return d.readXrefStream(l)
}

func (d *Doc) readTable(l *Lexer) (Dict, bool) {
	for {
		l.skip()
		if l.accept("trailer") {
			o, err := l.object(0)
			tr, ok := o.(Dict)
			return tr, ok && err == nil
		}
		start, ok := l.intToken()
		if !ok {
			return nil, false
		}
		count, ok := l.intToken()
		if !ok || count < 0 || count > maxObjects || start < 0 || start > maxObjects {
			return nil, false
		}
		for i := 0; i < count; i++ {
			off, ok1 := l.intToken()
			gen, ok2 := l.intToken()
			l.skip()
			if !ok1 || !ok2 || l.Pos >= len(l.b) {
				return nil, false
			}
			kind := l.b[l.Pos]
			l.Pos++
			if kind != 'n' && kind != 'f' {
				return nil, false
			}
			n := start + i
			if _, have := d.xref[n]; !have {
				d.xref[n] = xrefEntry{offset: off, gen: gen, free: kind == 'f'}
			}
		}
	}
}

func (d *Doc) readXrefStream(l *Lexer) (Dict, bool) {
	if _, ok := l.intToken(); !ok {
		return nil, false
	}
	if _, ok := l.intToken(); !ok {
		return nil, false
	}
	if !l.accept("obj") {
		return nil, false
	}
	o, err := l.object(0)
	dict, ok := o.(Dict)
	if err != nil || !ok || dict["Type"] != Name("XRef") {
		return nil, false
	}
	s := d.readStreamBody(l, dict, 0, 0, false)
	if s == nil {
		return nil, false
	}
	data, err := d.Decode(s)
	if err != nil && len(data) == 0 {
		return nil, false
	}
	wArr, _ := dict["W"].(Array)
	if len(wArr) < 3 {
		return nil, false
	}
	var w [3]int
	total := 0
	for i := range w {
		v, _ := Num(wArr[i])
		w[i] = int(v)
		if w[i] < 0 || w[i] > 8 {
			return nil, false
		}
		total += w[i]
	}
	if total == 0 {
		return nil, false
	}
	size, _ := Num(dict["Size"])
	index, _ := dict["Index"].(Array)
	if index == nil {
		index = Array{int64(0), int64(size)}
	}
	field := func(b []byte) int {
		v := 0
		for _, c := range b {
			v = v<<8 | int(c)
		}
		return v
	}
	pos := 0
	for i := 0; i+1 < len(index); i += 2 {
		startF, _ := Num(index[i])
		countF, _ := Num(index[i+1])
		start, count := int(startF), int(countF)
		if start < 0 || count < 0 || start > maxObjects || count > maxObjects {
			return nil, false
		}
		for j := 0; j < count; j++ {
			if pos+total > len(data) {
				return dict, true // short: keep what is there
			}
			kind := 1
			if w[0] > 0 {
				kind = field(data[pos : pos+w[0]])
			}
			f2 := field(data[pos+w[0] : pos+w[0]+w[1]])
			f3 := field(data[pos+w[0]+w[1] : pos+total])
			pos += total
			n := start + j
			// A hybrid file's table lists as free the objects only its stream describes.
			if e, have := d.xref[n]; have && !(d.hybrid && e.free) {
				continue
			}
			switch kind {
			case 0:
				d.xref[n] = xrefEntry{free: true}
			case 1:
				d.xref[n] = xrefEntry{offset: f2, gen: f3}
			case 2:
				d.xref[n] = xrefEntry{offset: f2, gen: f3, packed: true}
			}
		}
	}
	return dict, true
}

// rebuild finds every object by scanning the file, the later of two with one number winning.
func (d *Doc) rebuild() {
	d.mu.Lock()
	d.repaired = true
	d.xref = map[int]xrefEntry{}
	d.cache = map[int]Object{}
	d.objStms = map[int]*objStm{}
	d.delta = 0
	d.mu.Unlock()
	b := d.data
	trailer := Dict{}
	var packed []int
	for i := 0; i < len(b); {
		at := bytes.Index(b[i:], []byte("obj"))
		tr := bytes.Index(b[i:], []byte("trailer"))
		if tr >= 0 && (at < 0 || tr < at) {
			l := &Lexer{b: b, Pos: i + tr + len("trailer")}
			if o, err := l.object(0); err == nil {
				if dict, ok := o.(Dict); ok {
					for k, v := range dict {
						trailer[k] = v
					}
				}
			}
			i += tr + len("trailer")
			continue
		}
		if at < 0 {
			break
		}
		at += i
		i = at + 3
		if at >= 3 && b[at-3] == 'e' && b[at-2] == 'n' && b[at-1] == 'd' {
			continue
		}
		num, gen, start, ok := objHeaderBefore(b, at)
		if !ok {
			continue
		}
		d.xref[num] = xrefEntry{offset: start, gen: gen}
		// Look at dictionaries that matter: object streams and cross-reference streams.
		p := at + 3
		for p < len(b) && isSpace(b[p]) {
			p++
		}
		if p+1 < len(b) && b[p] == '<' && b[p+1] == '<' {
			window := b[p:min(len(b), p+512)]
			if bytes.Contains(window, []byte("ObjStm")) {
				packed = append(packed, num)
			}
			if bytes.Contains(window, []byte("XRef")) {
				l := &Lexer{b: b, Pos: p}
				if o, err := l.object(0); err == nil {
					if dict, ok := o.(Dict); ok && dict["Type"] == Name("XRef") {
						for _, k := range []Name{"Root", "Info", "ID", "Encrypt"} {
							if v, ok := dict[k]; ok {
								trailer[k] = v
							}
						}
					}
				}
			}
		}
	}
	d.trailer = trailer
	// With encryption the object streams need the key, which the caller has not set up yet: initCrypt comes back here.
	d.packedAfterScan(packed)
}

func objHeaderBefore(b []byte, at int) (num, gen, start int, ok bool) {
	p := at - 1
	back := func() (int, bool) {
		for p >= 0 && isSpace(b[p]) {
			p--
		}
		end := p
		v, mul := 0, 1
		for p >= 0 && b[p] >= '0' && b[p] <= '9' && end-p < 10 {
			v += int(b[p]-'0') * mul
			mul *= 10
			p--
		}
		return v, end != p
	}
	if p < 0 || !isSpace(b[p]) {
		return 0, 0, 0, false
	}
	gen, ok1 := back()
	if !ok1 || p < 0 || !isSpace(b[p]) {
		return 0, 0, 0, false
	}
	num, ok2 := back()
	if !ok2 || num <= 0 || num > maxObjects {
		return 0, 0, 0, false
	}
	return num, gen, p + 1, true
}

func (d *Doc) packedAfterScan(streams []int) {
	if d.trailer["Encrypt"] != nil && d.crypt == nil {
		return // needs the key: initCrypt calls this again
	}
	for _, n := range streams {
		os := d.objStm(n)
		if os == nil {
			continue
		}
		for i, m := range os.nums {
			if e, have := d.xref[m]; !have || e.packed || e.offset < d.xref[n].offset {
				d.xref[m] = xrefEntry{offset: n, gen: i, packed: true}
			}
		}
	}
	if d.trailer["Root"] == nil {
		d.findRoot()
	}
}

// Resolve follows references to the object itself; a missing object is nil.
func (d *Doc) Resolve(o Object) Object {
	for i := 0; i < maxFollow; i++ {
		r, ok := o.(Ref)
		if !ok {
			return o
		}
		o = d.Object(r.Num)
	}
	return nil
}

// Object is the current object with that number.
func (d *Doc) Object(num int) Object {
	d.mu.RLock()
	if o, ok := d.over[num]; ok {
		d.mu.RUnlock()
		return o
	}
	if o, ok := d.cache[num]; ok {
		d.mu.RUnlock()
		return o
	}
	d.mu.RUnlock()
	o := d.load(num)
	d.mu.Lock()
	d.cache[num] = o
	d.mu.Unlock()
	return o
}

func (d *Doc) load(num int) (out Object) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
		}
	}()
	d.mu.RLock()
	e, ok := d.xref[num]
	d.mu.RUnlock()
	if !ok || e.free {
		return nil
	}
	if e.packed {
		os := d.objStm(e.offset)
		if os == nil {
			return nil
		}
		idx := e.gen
		if idx >= len(os.nums) || os.nums[idx] != num {
			idx = -1
			for i, n := range os.nums {
				if n == num {
					idx = i
				}
			}
			if idx < 0 {
				return nil
			}
		}
		l := &Lexer{b: os.body, Pos: os.first + os.offsets[idx]}
		o, err := l.object(0)
		if err != nil {
			return nil
		}
		if _, isKeyword := o.(Keyword); isKeyword {
			return nil
		}
		return o
	}
	o, ok := d.parseAt(e.offset+d.delta, num)
	if !ok && !d.repaired {
		// Offsets may be from the start of the file, not the header.
		if o, ok = d.parseAt(e.offset, num); !ok {
			d.rescue(num, &o)
		}
	}
	return o
}

// rescue finds an object whose stated offset is wrong by looking for its header.
func (d *Doc) rescue(num int, out *Object) {
	if d.rescues.Add(1) > 64 {
		return
	}
	needle := fmt.Appendf(nil, "%d %d obj", num, 0)
	at := bytes.LastIndex(d.data, needle)
	for at > 0 && !isSpace(d.data[at-1]) {
		at = bytes.LastIndex(d.data[:at], needle)
	}
	if at >= 0 {
		*out, _ = d.parseAt(at, num)
	}
}

func (d *Doc) parseAt(pos, num int) (Object, bool) {
	if pos < 0 || pos >= len(d.data) {
		return nil, false
	}
	l := &Lexer{b: d.data, Pos: pos}
	n, ok := l.intToken()
	if !ok || n != num {
		return nil, false
	}
	gen, ok := l.intToken()
	if !ok || !l.accept("obj") {
		return nil, false
	}
	if d.crypt != nil && !d.isEncryptDict(num) {
		l.decrypt = func(b []byte) []byte { return d.crypt.decrypt(num, gen, b, false) }
	}
	o, err := l.object(0)
	if err != nil {
		return nil, false
	}
	if _, isKeyword := o.(Keyword); isKeyword {
		return nil, true // "n g obj endobj" is an empty object
	}
	// Some writers put numbers and references bare: "5 0 obj 12 0 R endobj".
	if a, isInt := o.(int64); isInt {
		save := l.Pos
		if g, ok := l.intToken(); ok && l.accept("R") {
			return Ref{int(a), g}, true
		}
		l.Pos = save
	}
	dict, ok := o.(Dict)
	if !ok {
		return o, true
	}
	if l.accept("stream") {
		if s := d.readStreamBody(l, dict, num, gen, true); s != nil {
			return s, true
		}
	}
	return dict, true
}

// readStreamBody takes a stream's bytes after the word "stream". A wrong Length is put right by looking for
// endstream.
func (d *Doc) readStreamBody(l *Lexer, dict Dict, num, gen int, afterKeyword bool) *Stream {
	if !afterKeyword && !l.accept("stream") {
		return nil
	}
	b := l.b
	p := l.Pos
	if p < len(b) && b[p] == '\r' {
		p++
	}
	if p < len(b) && b[p] == '\n' {
		p++
	}
	length := -1
	if v, ok := Num(d.Resolve(dict["Length"])); ok && v >= 0 {
		length = int(v)
	}
	if length >= 0 && p+length <= len(b) {
		q := &Lexer{b: b, Pos: p + length}
		q.skip()
		if !bytes.HasPrefix(b[min(q.Pos, len(b)):], []byte("endstream")) && !bytes.HasPrefix(b[min(q.Pos, len(b)):], []byte("endobj")) {
			length = -1
		}
	} else {
		length = -1
	}
	if length < 0 {
		end := bytes.Index(b[p:], []byte("endstream"))
		if end < 0 {
			end = bytes.Index(b[p:], []byte("endobj"))
		}
		if end < 0 {
			end = len(b) - p
		}
		length = end
		if length > 0 && b[p+length-1] == '\n' {
			length--
		}
		if length > 0 && b[p+length-1] == '\r' {
			length--
		}
	}
	l.Pos = p + length
	s := &Stream{Dict: dict, raw: b[p : p+length], num: num, gen: gen}
	if d.crypt != nil && d.crypt.encrypted(dict) {
		s.doc = d
	}
	return s
}

func (d *Doc) objStm(num int) *objStm {
	d.mu.RLock()
	os, have := d.objStms[num]
	d.mu.RUnlock()
	if have {
		return os
	}
	os = d.parseObjStm(num)
	d.mu.Lock()
	d.objStms[num] = os
	d.mu.Unlock()
	return os
}

func (d *Doc) parseObjStm(num int) *objStm {
	d.mu.RLock()
	e, ok := d.xref[num]
	d.mu.RUnlock()
	if !ok || e.packed || e.free {
		return nil // an object stream is never itself in an object stream
	}
	s, _ := d.Object(num).(*Stream)
	if s == nil {
		return nil
	}
	body, err := d.Decode(s)
	if err != nil && len(body) == 0 {
		return nil
	}
	nf, _ := Num(d.Resolve(s.Dict["N"]))
	first, _ := Num(d.Resolve(s.Dict["First"]))
	n := int(nf)
	if n < 0 || n > maxObjStm || first < 0 || int(first) > len(body) {
		return nil
	}
	os := &objStm{body: body, first: int(first)}
	l := &Lexer{b: body[:int(first)]}
	for i := 0; i < n; i++ {
		a, ok1 := l.intToken()
		b, ok2 := l.intToken()
		if !ok1 || !ok2 {
			break
		}
		os.nums = append(os.nums, a)
		os.offsets = append(os.offsets, b)
	}
	return os
}

// Dict resolves to a dictionary, a stream giving its dictionary; nil when it is neither.
func (d *Doc) Dict(o Object) Dict {
	switch v := d.Resolve(o).(type) {
	case Dict:
		return v
	case *Stream:
		return v.Dict
	}
	return nil
}

// Array resolves to an array.
func (d *Doc) Array(o Object) Array {
	a, _ := d.Resolve(o).(Array)
	return a
}

// Name resolves to a name.
func (d *Doc) Name(o Object) Name {
	n, _ := d.Resolve(o).(Name)
	return n
}

// Float resolves to a number, def when it is none.
func (d *Doc) Float(o Object, def float64) float64 {
	if v, ok := Num(d.Resolve(o)); ok {
		return v
	}
	return def
}

// Int resolves to an integer, def when it is no number.
func (d *Doc) Int(o Object, def int) int {
	if v, ok := Num(d.Resolve(o)); ok {
		return int(v)
	}
	return def
}

// Bool resolves to a boolean.
func (d *Doc) Bool(o Object, def bool) bool {
	if v, ok := d.Resolve(o).(bool); ok {
		return v
	}
	return def
}

// Floats resolves an array of numbers.
func (d *Doc) Floats(o Object) []float64 {
	a := d.Array(o)
	if a == nil {
		return nil
	}
	out := make([]float64, 0, len(a))
	for _, v := range a {
		out = append(out, d.Float(v, 0))
	}
	return out
}

// RectOf reads a rectangle.
func (d *Doc) RectOf(o Object) (Rect, bool) {
	v := d.Floats(o)
	if len(v) < 4 {
		return Rect{}, false
	}
	return Rect{v[0], v[1], v[2], v[3]}.Normal(), true
}

// MatrixOf reads a matrix, the identity when absent.
func (d *Doc) MatrixOf(o Object) Matrix {
	v := d.Floats(o)
	if len(v) < 6 {
		return Identity
	}
	return Matrix{v[0], v[1], v[2], v[3], v[4], v[5]}
}

// Catalog is the document's root dictionary.
func (d *Doc) Catalog() Dict { return d.Dict(d.trailer["Root"]) }
