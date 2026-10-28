package files

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Node is a folder that has been analysed: how much its files and the folders under it hold together, as gdu adds
// them up. Only folders are kept, so a drive with millions of files fits in memory; the files of a folder are read
// again when it is shown, which also keeps the listing current.
type Node struct {
	Name     string // the folder's name; the root's is its whole path
	Size     int64  // the bytes of every file under it
	Items    int64  // the files and folders under it
	Mod      time.Time
	Err      bool // some of it could not be read, so it holds at least Size
	Parent   *Node
	Children []*Node

	own      int64 // the bytes of its own files, while scanning
	ownItems int64
}

// Path is where the folder is.
func (n *Node) Path() string {
	if n.Parent == nil {
		return n.Name
	}
	return filepath.Join(n.Parent.Path(), n.Name)
}

// Child is the folder under n with that name, or nil.
func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if samePath(c.Name, name) {
			return c
		}
	}
	return nil
}

// Find is the analysed folder at path, at or under n, or nil when path is outside it.
func (n *Node) Find(path string) *Node {
	root := n.Path()
	if !Within(path, root) {
		return nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil
	}
	at := n
	if rel == "." {
		return at
	}
	for _, part := range splitPath(rel) {
		if at = at.Child(part); at == nil {
			return nil
		}
	}
	return at
}

// Detach takes the folder out of its parent and takes what it held off every folder above it, as after it was
// deleted or moved away.
func (n *Node) Detach() {
	p := n.Parent
	if p == nil {
		return
	}
	for i, c := range p.Children {
		if c == n {
			p.Children = append(p.Children[:i:i], p.Children[i+1:]...)
			break
		}
	}
	p.Grow(-n.Size, -(n.Items + 1))
	n.Parent = nil
}

// Grow adds bytes and items to n and every folder above it, as when a file was added to or removed from n.
func (n *Node) Grow(bytes, items int64) {
	for at := n; at != nil; at = at.Parent {
		at.Size = max(at.Size+bytes, 0)
		at.Items = max(at.Items+items, 0)
	}
}

// Attach puts an analysed folder under n and adds what it holds to n and every folder above it.
func (n *Node) Attach(c *Node) {
	c.Parent = n
	n.Children = append(n.Children, c)
	n.Grow(c.Size, c.Items+1)
}

func splitPath(rel string) []string {
	var out []string
	for rel != "" && rel != "." {
		dir, name := filepath.Split(rel)
		out = append(out, name)
		rel = filepath.Clean(dir)
		if rel == string(filepath.Separator) || dir == "" {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ScanProgress is how far an analysis has come; it is updated while the analysis runs and read from any goroutine.
type ScanProgress struct {
	Files, Dirs, Bytes atomic.Int64
	current            atomic.Pointer[string]
}

// Current is the folder being read.
func (p *ScanProgress) Current() string {
	if s := p.current.Load(); s != nil {
		return *s
	}
	return ""
}

// Scan adds up every folder under root, reading folders on several goroutines at once as gdu does: on a solid-state
// drive the time goes into waiting for the file system, which answers many requests together. Links and junctions
// are not followed. When ctx ends it returns what it had read with ctx's error.
func Scan(ctx context.Context, root string, progress *ScanProgress) (*Node, error) {
	if progress == nil {
		progress = &ScanProgress{}
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	top := &Node{Name: root, Mod: info.ModTime()}
	s := &scanner{ctx: ctx, progress: progress}
	s.cond = sync.NewCond(&s.mu)
	s.push(top)
	workers := 3 * runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(s.work)
	}
	wg.Wait()
	total(top)
	return top, ctx.Err()
}

// scanner shares the folders still to read between the workers. They are taken last in, first out, so the walk
// goes deep first and the list of folders waiting stays short.
type scanner struct {
	ctx      context.Context
	progress *ScanProgress
	mu       sync.Mutex
	cond     *sync.Cond
	stack    []*Node
	pending  int // folders pushed and not finished
}

func (s *scanner) push(n *Node) {
	s.mu.Lock()
	s.stack = append(s.stack, n)
	s.pending++
	s.mu.Unlock()
	s.cond.Signal()
}

func (s *scanner) work() {
	for {
		s.mu.Lock()
		for len(s.stack) == 0 && s.pending > 0 {
			s.cond.Wait()
		}
		if s.pending == 0 {
			s.mu.Unlock()
			s.cond.Broadcast()
			return
		}
		n := s.stack[len(s.stack)-1]
		s.stack = s.stack[:len(s.stack)-1]
		s.mu.Unlock()

		if s.ctx.Err() == nil {
			s.read(n)
		}

		s.mu.Lock()
		s.pending--
		done := s.pending == 0
		s.mu.Unlock()
		if done {
			s.cond.Broadcast()
		}
	}
}

// read lists one folder: its files are added to it, its folders are made and pushed to be read in turn.
func (s *scanner) read(n *Node) {
	path := n.Path()
	s.progress.current.Store(&path)
	des, err := os.ReadDir(path)
	if err != nil {
		n.Err = true
	}
	var subs []*Node
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue
		}
		mode := info.Mode()
		if info.ModTime().After(n.Mod) {
			n.Mod = info.ModTime()
		}
		if realDir(mode) {
			subs = append(subs, &Node{Name: de.Name(), Parent: n, Mod: info.ModTime()})
			continue
		}
		n.ownItems++
		s.progress.Files.Add(1)
		if !isLink(mode) {
			n.own += info.Size()
			s.progress.Bytes.Add(info.Size())
		}
	}
	n.Children = subs
	s.progress.Dirs.Add(1)
	for _, c := range subs {
		s.push(c)
	}
}

// total adds each folder's own files to what its folders hold, from the bottom up.
func total(n *Node) {
	n.Size, n.Items = n.own, n.ownItems
	for _, c := range n.Children {
		total(c)
		n.Size += c.Size
		n.Items += c.Items + 1
		n.Err = n.Err || c.Err
		if c.Mod.After(n.Mod) {
			n.Mod = c.Mod
		}
	}
	n.own, n.ownItems = 0, 0
}
