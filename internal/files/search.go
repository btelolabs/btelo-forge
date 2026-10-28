package files

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// skipped are folders searches leave out, as ripgrep does through the ignore files that list them: version control
// and installed packages, which are large and seldom what is looked for.
var skipped = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true}

// Index is every name under a folder, read once and then filtered as each letter is typed, so a search answers at
// once however often it changes. It is filled on one goroutine and filtered on others.
type Index struct {
	Root string
	mu   sync.RWMutex
	rels []string // paths relative to Root, the nearest first
	low  []string // the same in lower case
	dirs []bool
	done atomic.Bool
}

// Done reports whether every name has been read.
func (ix *Index) Done() bool { return ix.done.Load() }

// Len is how many names have been read.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.rels)
}

// NewIndex is an empty index of the names under root, to be filled by Build.
func NewIndex(root string) *Index { return &Index{Root: root} }

// Build reads every name under the index's folder, folder by folder from the nearest, until ctx ends; it may be
// filtered meanwhile. Hidden files are left out unless withHidden is set; links are listed but not followed.
func (ix *Index) Build(ctx context.Context, withHidden bool) {
	defer ix.done.Store(true)
	queue := []string{""}
	for len(queue) > 0 && ctx.Err() == nil {
		rel := queue[0]
		queue = queue[1:]
		des, _ := os.ReadDir(filepath.Join(ix.Root, rel))
		var rels, low []string
		var dirs []bool
		for _, de := range des {
			name := de.Name()
			if !withHidden && hiddenEntry(de) {
				continue
			}
			sub := filepath.Join(rel, name)
			if realDir(de.Type()) && !skipped[name] {
				queue = append(queue, sub)
			}
			rels, low, dirs = append(rels, sub), append(low, strings.ToLower(sub)), append(dirs, de.IsDir())
		}
		ix.mu.Lock()
		ix.rels, ix.low, ix.dirs = append(ix.rels, rels...), append(ix.low, low...), append(ix.dirs, dirs...)
		ix.mu.Unlock()
	}
}

func hiddenEntry(de fs.DirEntry) bool {
	info, err := de.Info()
	if err != nil {
		return strings.HasPrefix(de.Name(), ".")
	}
	return hidden(de.Name(), info)
}

// Hit is a name that matches a search, relative to the folder searched.
type Hit struct {
	Rel string
	Dir bool
}

// Filter is the names that match query, the nearest first, up to limit, and how many match in all. Every word typed
// must appear in the path and the last one in the name itself; case matters only when a capital is typed.
func (ix *Index) Filter(ctx context.Context, query string, limit int) ([]Hit, int) {
	words := strings.Fields(query)
	if len(words) == 0 {
		return nil, 0
	}
	exact := hasUpper(query)
	if !exact {
		for i := range words {
			words[i] = strings.ToLower(words[i])
		}
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []Hit
	n := 0
	for i, rel := range ix.rels {
		if i%4096 == 0 && ctx.Err() != nil {
			break
		}
		s := ix.low[i]
		if exact {
			s = rel
		}
		if !matchWords(s, words) {
			continue
		}
		n++
		if len(out) < limit {
			out = append(out, Hit{Rel: rel, Dir: ix.dirs[i]})
		}
	}
	return out, n
}

func matchWords(s string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	last := words[len(words)-1]
	return strings.Contains(s[strings.LastIndexAny(s, `/\`)+1:], last)
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// Line is a line of a file that holds what was searched for.
type Line struct {
	Rel  string // the file, relative to the folder searched
	No   int    // from 1
	Text string // the line, cut short when it is long
	At   int    // where the match starts in Text, in bytes
	Len  int    // how long it is there
}

// grepMaxSize is the largest file searched; a larger one is a disk image, a database or a video, not text.
const grepMaxSize = 64 << 20

// lineMax is how much of a long line is kept around a match.
const lineMax = 240

// GrepStats are how many files a content search has looked at.
type GrepStats struct{ Files, Matched atomic.Int64 }

// Grep searches the text of every file under root for query, as ripgrep does: files on several goroutines, binary
// files (those with a zero byte near the start) left out, case mattering only when a capital is typed. Each line
// found is passed to found, from any goroutine, until ctx ends.
func Grep(ctx context.Context, root, query string, withHidden bool, stats *GrepStats, found func(Line)) {
	if query == "" {
		return
	}
	exact := hasUpper(query)
	needle := []byte(query)
	if !exact {
		needle = bytes.ToLower(needle)
	}
	paths := make(chan string, 256)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for path := range paths {
				if ctx.Err() != nil {
					continue
				}
				grepFile(root, path, needle, exact, found)
				stats.Files.Add(1)
			}
		})
	}
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil || path == root {
			return nil
		}
		if !withHidden && hiddenEntry(d) || d.IsDir() && skipped[d.Name()] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			paths <- path
		}
		return nil
	})
	close(paths)
	wg.Wait()
}

func grepFile(root, path string, needle []byte, exact bool, found func(Line)) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > grepMaxSize || info.Size() == 0 {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return
	}
	hay := data
	if !exact {
		hay = bytes.ToLower(data)
		if len(hay) != len(data) { // a letter whose lower case is longer: compare line by line
			hay = nil
		}
	}
	if hay != nil && !bytes.Contains(hay, needle) {
		return
	}
	rel, _ := filepath.Rel(root, path)
	no := 0
	for len(data) > 0 {
		no++
		end := bytes.IndexByte(data, '\n')
		line := data
		if end >= 0 {
			line, data = data[:end], data[end+1:]
		} else {
			data = nil
		}
		low := line
		if !exact {
			low = bytes.ToLower(line)
		}
		at := bytes.Index(low, needle)
		if at < 0 {
			continue
		}
		n := len(needle)
		if len(low) != len(line) {
			at, n = 0, 0 // the lower case is longer, so its offsets are not the line's: show the line from the start
		}
		found(cutLine(rel, no, line, at, n))
	}
}

// cutLine keeps the part of a long line around the match, on whole characters, without the line's end.
func cutLine(rel string, no int, line []byte, at, n int) Line {
	line = bytes.TrimRight(line, "\r")
	start := 0
	if len(line) > lineMax {
		start = max(0, at-lineMax/3)
		for start > 0 && !utf8.RuneStart(line[start]) {
			start--
		}
		end := min(len(line), start+lineMax)
		for end < len(line) && !utf8.RuneStart(line[end]) {
			end++
		}
		line = line[start:end]
	}
	text := strings.ToValidUTF8(strings.ReplaceAll(string(line), "\t", "    "), "?")
	// Tabs became four spaces: move the match to where it now is.
	prefix := strings.ReplaceAll(strings.ToValidUTF8(string(line[:min(at-start, len(line))]), "?"), "\t", "    ")
	return Line{Rel: rel, No: no, Text: text, At: min(len(prefix), len(text)), Len: n}
}
