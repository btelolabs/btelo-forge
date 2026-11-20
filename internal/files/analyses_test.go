package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sameTree reports where two analyses differ, or "".
func sameTree(a, b *Node) string {
	if a.Name != b.Name || a.Size != b.Size || a.Items != b.Items || a.Err != b.Err || !a.Mod.Equal(b.Mod) || !a.At.Equal(b.At) || len(a.Children) != len(b.Children) {
		return fmt.Sprintf("%s: %+v against %+v", a.Path(), *a, *b)
	}
	for i := range a.Children {
		if b.Children[i].Parent != b {
			return b.Children[i].Path() + " has another parent"
		}
		if d := sameTree(a.Children[i], b.Children[i]); d != "" {
			return d
		}
	}
	return ""
}

func TestAnalysesReadBackAsTheyWereWritten(t *testing.T) {
	root := tree(t, map[string]string{
		"a.txt": "12345", "big/x.bin": strings.Repeat("x", 1000), "big/deep/y.bin": strings.Repeat("y", 500),
		"small/z": "zz", "empty/": "", "名字/文件": "abc",
	})
	n, err := Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	n.Child("small").Err = true
	other := &Node{Name: filepath.Join(t.TempDir(), "other"), Size: 7, Items: 1}
	path := filepath.Join(t.TempDir(), "kept", "analyses.bin")
	if err := SaveAnalyses(path, EncodedAnalyses([]*Node{n, other})); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAnalyses(path)
	if err != nil || len(got) != 2 {
		t.Fatalf("read back %d analyses: %v", len(got), err)
	}
	for i, want := range []*Node{n, other} {
		if d := sameTree(want, got[i]); d != "" {
			t.Errorf("analysis %d differs at %s", i, d)
		}
	}
	if deep := got[0].Find(filepath.Join(root, "big", "deep")); deep == nil || !deep.Analysed().Equal(n.At) || n.At.IsZero() {
		t.Errorf("a folder read back was analysed at %v, its analysis at %v", deep.Analysed(), n.At)
	}
	if none, err := LoadAnalyses(filepath.Join(t.TempDir(), "missing.bin")); none != nil || err != nil {
		t.Errorf("a missing file gave %v, %v", none, err)
	}
}

// A damaged file, cut anywhere or with any byte changed, is refused or read as some well-formed analyses: it never
// panics, and never gives a folder a parent other than its own.
func TestDamagedAnalysesAreRefusedNotTrusted(t *testing.T) {
	root := &Node{Name: filepath.Join(t.TempDir(), "r"), Size: 300, Items: 5, Mod: time.Unix(1_700_000_000, 0), At: time.Now()}
	for i := range 3 {
		c := &Node{Name: fmt.Sprintf("c%d", i), Size: 100, Items: 1}
		root.Attach(c)
		c.Attach(&Node{Name: "leaf"})
	}
	data := EncodedAnalyses([]*Node{root})
	if _, err := DecodeAnalyses(bytes.NewReader([]byte("JSON{}"))); !errors.Is(err, ErrAnalysesFormat) {
		t.Errorf("another file gave %v", err)
	}
	other := bytes.Clone(data)
	other[4] = analysesVersion + 1
	if _, err := DecodeAnalyses(bytes.NewReader(other)); !errors.Is(err, ErrAnalysesFormat) {
		t.Errorf("another version gave %v", err)
	}
	for cut := 0; cut < len(data); cut++ {
		if got, err := DecodeAnalyses(bytes.NewReader(data[:cut])); err == nil && len(got) == 1 {
			t.Fatalf("cut at %d of %d, the file still read whole", cut, len(data))
		}
	}
	rng := rand.New(rand.NewPCG(3, 9))
	for range 3000 {
		bad := bytes.Clone(data)
		bad[5+rng.IntN(len(bad)-5)] ^= byte(1 + rng.IntN(255))
		got, err := DecodeAnalyses(bytes.NewReader(bad))
		if err != nil {
			continue
		}
		for _, t2 := range got {
			var walk func(n *Node)
			walk = func(n *Node) {
				for _, c := range n.Children {
					if c.Parent != n {
						t.Fatalf("a folder read from a damaged file has another parent")
					}
					walk(c)
				}
			}
			walk(t2)
		}
	}
	var deep bytes.Buffer
	deep.Write(analysesMagic[:])
	deep.WriteByte(analysesVersion)
	deep.WriteByte(1)
	for range analysesDepth + 10 {
		deep.Write([]byte{1, 'x', 0, 0, 0, 0, 0, 1})
	}
	if _, err := DecodeAnalyses(&deep); err == nil {
		t.Error("a file nested beyond any real folder was read")
	}
}

// Live shows the folder analysed and those in it adding up as they are read; once the analysis is done it is what
// the analysis found of them.
func TestLiveIsWhatTheFoldersHoldSoFar(t *testing.T) {
	root := tree(t, map[string]string{
		"a.txt": "12345", "big/x.bin": strings.Repeat("x", 1000), "big/deep/y.bin": strings.Repeat("y", 500),
		"small/z": "zz", "empty/": "",
	})
	var p ScanProgress
	if p.Live(root) != nil {
		t.Fatal("an analysis not started shows something")
	}
	n, err := Scan(context.Background(), root, &p)
	if err != nil {
		t.Fatal(err)
	}
	live := p.Live(root)
	if live == nil || !live.Live || n.Live || live.Size != n.Size || live.Items != n.Items || len(live.Children) != len(n.Children) {
		t.Fatalf("live %+v, analysed %+v", live, n)
	}
	for _, c := range n.Children {
		l := live.Child(c.Name)
		if l == nil || l.Size != c.Size || l.Items != c.Items || l.Parent != live || len(l.Children) != 0 {
			t.Errorf("live %s is %+v, analysed %+v", c.Name, l, c)
		}
	}
	if p.Live(root) == live {
		t.Error("Live gave the same nodes twice")
	}
}

// The analyses of a large drive are written and read in a moment: they are made on the drawing goroutine.
func TestAnalysesOfManyFoldersAreQuick(t *testing.T) {
	root := &Node{Name: filepath.Join(t.TempDir(), "drive"), At: time.Now()}
	var grow func(n *Node, depth int)
	count := 0
	grow = func(n *Node, depth int) {
		for i := range 9 {
			if depth == 0 || count >= 400_000 {
				return
			}
			c := &Node{Name: fmt.Sprintf("folder %d of level %d", i, depth), Size: int64(i) << 20, Items: 3, Mod: time.Now(), Parent: n}
			n.Children = append(n.Children, c)
			count++
			grow(c, depth-1)
		}
	}
	grow(root, 7)
	start := time.Now()
	data := EncodedAnalyses([]*Node{root})
	enc := time.Since(start)
	start = time.Now()
	got, err := DecodeAnalyses(bytes.NewReader(data))
	dec := time.Since(start)
	if err != nil || len(got) != 1 {
		t.Fatal(err)
	}
	t.Logf("%d folders: %d KB, written in %v, read in %v", count, len(data)>>10, enc, dec)
	if enc > 250*time.Millisecond {
		t.Errorf("writing %d folders took %v", count, enc)
	}
}
