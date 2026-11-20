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
	Name  string // the folder's name; the root's is its whole path
	Size  int64  // the bytes of every file under it
	Items int64  // the files and folders under it
	Mod   time.Time
	Err   bool // some of it could not be read, so it holds at least Size
	// At is when the folder was analysed, on the folder an analysis started from; the folders in it are as old as
	// the nearest folder above them with a time (Analysed).
	At time.Time
	// Live marks what an analysis running has found so far (ScanProgress.Live): the folders in it hold none of their
	// own yet, as what is under them is only added up.
	Live     bool
	Parent   *Node
	Children []*Node

	own      int64 // the bytes of its own files, while scanning
	ownItems int64
	// byName finds a folder among Children at once, as a list sorted by size asks for every one of thousands; made
	// when first asked for and forgotten when Children or a child's name changes.
	byName map[string]*Node
}

// Analysed is when what n holds was read: the time of the analysis it came from.
func (n *Node) Analysed() time.Time {
	for at := n; at != nil; at = at.Parent {
		if !at.At.IsZero() {
			return at.At
		}
	}
	return time.Time{}
}

// Path is where the folder is.
func (n *Node) Path() string {
	if n.Parent == nil {
		return n.Name
	}
	return filepath.Join(n.Parent.Path(), n.Name)
}

// childIndexed is how many folders a folder holds before Child looks them up by name rather than one by one.
const childIndexed = 16

// Child is the folder under n with that name, or nil.
func (n *Node) Child(name string) *Node {
	if len(n.Children) < childIndexed {
		for _, c := range n.Children {
			if samePath(c.Name, name) {
				return c
			}
		}
		return nil
	}
	if n.byName == nil {
		n.byName = make(map[string]*Node, len(n.Children))
		for _, c := range n.Children {
			n.byName[nameKey(c.Name)] = c
		}
	}
	if c := n.byName[nameKey(name)]; c != nil && samePath(c.Name, name) {
		return c
	}
	return nil
}

// Rename gives the folder a new name: the last part of a path, or the whole path for a root.
func (n *Node) Rename(name string) {
	n.Name = name
	if n.Parent != nil {
		n.Parent.byName = nil
	}
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
			p.byName = nil
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
	n.byName = nil
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
	live               atomic.Pointer[liveRoot]
}

// liveRoot is the folder analysed as it is being read: its own files, read with it, and what each folder in it
// holds so far. It is made once the folder is read, and only its sums change after.
type liveRoot struct {
	own, ownItems int64
	folders       []*liveFolder
}

// liveFolder is a folder in the one analysed, adding up what is read under it.
type liveFolder struct {
	name        string
	size, items atomic.Int64
}

// Live is what the analysis of root has found so far, for a list to show while it runs: the folder and those in it,
// each holding what has been read of it, with no folders further down; nil until the folder itself is read. Each
// call makes new nodes, which nothing else changes.
func (p *ScanProgress) Live(root string) *Node {
	l := p.live.Load()
	if l == nil {
		return nil
	}
	top := &Node{Name: filepath.Clean(root), Size: l.own, Items: l.ownItems, Live: true, Children: make([]*Node, len(l.folders))}
	for i, f := range l.folders {
		c := &Node{Name: f.name, Size: f.size.Load(), Items: f.items.Load(), Parent: top}
		top.Children[i] = c
		top.Size += c.Size
		top.Items += c.Items + 1
	}
	return top
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
	top := &Node{Name: root, Mod: info.ModTime(), At: time.Now()}
	s := &scanner{ctx: ctx, progress: progress}
	s.cond = sync.NewCond(&s.mu)
	s.push(folder{node: top})
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
	stack    []folder
	pending  int // folders pushed and not finished
}

// folder is a folder waiting to be read, with the folder of the one analysed it lies in, which shows its sums as they
// come; the folder analysed has none.
type folder struct {
	node *Node
	live *liveFolder
}

func (s *scanner) push(f folder) {
	s.mu.Lock()
	s.stack = append(s.stack, f)
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
		f := s.stack[len(s.stack)-1]
		s.stack = s.stack[:len(s.stack)-1]
		s.mu.Unlock()

		if s.ctx.Err() == nil {
			s.read(f)
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
func (s *scanner) read(f folder) {
	n := f.node
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
	if n.Parent == nil {
		// The folder analysed: each folder in it adds up what is read under it, for Live.
		l := &liveRoot{own: n.own, ownItems: n.ownItems, folders: make([]*liveFolder, len(subs))}
		for i, c := range subs {
			l.folders[i] = &liveFolder{name: c.Name}
		}
		s.progress.live.Store(l)
		for i, c := range subs {
			s.push(folder{node: c, live: l.folders[i]})
		}
		return
	}
	f.live.size.Add(n.own)
	f.live.items.Add(n.ownItems + int64(len(subs)))
	for _, c := range subs {
		s.push(folder{node: c, live: f.live})
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
