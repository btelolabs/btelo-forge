package files

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Analyses are kept between runs in a file of their own: a few bytes per folder, written whole and read back as the
// app starts, so that a folder analysed before is shown at once. The file is a header, the number of analyses, and
// each analysis's folders depth first: a folder's name (the root's whole path), its size, items, time changed, time
// analysed, whether some of it could not be read, and how many folders it holds, which follow it.

// analysesMagic starts the file; analysesVersion is the layout above, and a file of another is not read.
var analysesMagic = [4]byte{'B', 'F', 'A', 'N'}

const analysesVersion = 1

// analysesDepth is the deepest a folder is read from the file: deeper is a damaged file, not a real one.
const analysesDepth = 4096

// analysesName is the longest name read from the file, in bytes.
const analysesName = 1 << 15

// ErrAnalysesFormat is returned for a file that is not one of analyses, or of another version.
var ErrAnalysesFormat = errors.New("not a file of analyses")

// EncodeAnalyses writes the analyses whose roots are trees.
func EncodeAnalyses(w io.Writer, trees []*Node) error {
	bw := bufio.NewWriterSize(w, 1<<16)
	bw.Write(analysesMagic[:])
	bw.WriteByte(analysesVersion)
	var num [binary.MaxVarintLen64]byte
	uv := func(v uint64) { bw.Write(num[:binary.PutUvarint(num[:], v)]) }
	iv := func(v int64) { bw.Write(num[:binary.PutVarint(num[:], v)]) }
	stamp := func(t time.Time) {
		if t.IsZero() {
			iv(0)
			return
		}
		iv(t.UnixNano())
	}
	var put func(n *Node)
	put = func(n *Node) {
		uv(uint64(len(n.Name)))
		bw.WriteString(n.Name)
		iv(n.Size)
		iv(n.Items)
		stamp(n.Mod)
		stamp(n.At)
		if n.Err {
			bw.WriteByte(1)
		} else {
			bw.WriteByte(0)
		}
		uv(uint64(len(n.Children)))
		for _, c := range n.Children {
			put(c)
		}
	}
	uv(uint64(len(trees)))
	for _, t := range trees {
		put(t)
	}
	return bw.Flush()
}

// DecodeAnalyses reads analyses written by EncodeAnalyses. A damaged file is an error, never a panic, and gives none
// of its analyses: each root must be a whole path.
func DecodeAnalyses(r io.Reader) ([]*Node, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	var head [5]byte
	if _, err := io.ReadFull(br, head[:]); err != nil {
		return nil, ErrAnalysesFormat
	}
	if [4]byte(head[:4]) != analysesMagic || head[4] != analysesVersion {
		return nil, ErrAnalysesFormat
	}
	var bad error
	fail := func(what string) {
		if bad == nil {
			bad = fmt.Errorf("damaged analyses: %s", what)
		}
	}
	uv := func() uint64 {
		v, err := binary.ReadUvarint(br)
		if err != nil {
			fail("cut short")
		}
		return v
	}
	iv := func() int64 {
		v, err := binary.ReadVarint(br)
		if err != nil {
			fail("cut short")
		}
		return v
	}
	stamp := func() time.Time {
		if v := iv(); v != 0 {
			return time.Unix(0, v)
		}
		return time.Time{}
	}
	var name []byte
	var get func(parent *Node, depth int) *Node
	get = func(parent *Node, depth int) *Node {
		if depth > analysesDepth {
			fail("too deep")
			return nil
		}
		l := uv()
		if bad != nil || l > analysesName {
			fail("a name too long")
			return nil
		}
		if uint64(cap(name)) < l {
			name = make([]byte, l)
		}
		name = name[:l]
		if _, err := io.ReadFull(br, name); err != nil {
			fail("cut short")
			return nil
		}
		n := &Node{Name: string(name), Parent: parent}
		n.Size, n.Items = iv(), iv()
		n.Mod, n.At = stamp(), stamp()
		flag, err := br.ReadByte()
		if err != nil || flag > 1 {
			fail("a flag")
			return nil
		}
		n.Err = flag == 1
		kids := uv()
		if bad != nil || n.Size < 0 || n.Items < 0 {
			fail("a folder's sums")
			return nil
		}
		if kids > 0 {
			n.Children = make([]*Node, 0, min(kids, 1<<12))
			for range kids {
				c := get(n, depth+1)
				if c == nil {
					return nil
				}
				n.Children = append(n.Children, c)
			}
		}
		return n
	}
	count := uv()
	var trees []*Node
	for i := uint64(0); bad == nil && i < count; i++ {
		t := get(nil, 0)
		if t == nil {
			break
		}
		if !filepath.IsAbs(t.Name) {
			fail("a root that is not a whole path")
			break
		}
		trees = append(trees, t)
	}
	if bad != nil {
		return nil, bad
	}
	return trees, nil
}

// LoadAnalyses reads the analyses kept at path; a missing file is none.
func LoadAnalyses(path string) ([]*Node, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeAnalyses(f)
}

// SaveAnalyses writes data, made by EncodeAnalyses, to path, replacing the file whole so a crash never leaves half
// of it.
func SaveAnalyses(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EncodedAnalyses is EncodeAnalyses into memory, for writing elsewhere while the trees go on changing.
func EncodedAnalyses(trees []*Node) []byte {
	var b bytes.Buffer
	EncodeAnalyses(&b, trees) // writing to memory does not fail
	return b.Bytes()
}
