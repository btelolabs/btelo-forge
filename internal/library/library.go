// Package library lists free music and sound effects, downloads them into a local cache and remembers imported files.
//
// YouTube's own Audio Library has no public interface (it needs a signed-in YouTube Studio session), so the online
// sources are sites that allow direct download. They are described in JSON (see Config), so more sites can be added
// without changing the program. Files downloaded from YouTube Studio can be imported.
package library

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Provider names where a track comes from: the id of its source, or Local for imported files.
type Provider string

// Local marks imported files.
const Local Provider = "local"

// Track is one playable sound.
type Track struct {
	Provider Provider
	ID       string
	Title    string
	Artist   string
	Genre    string
	Mood     string
	Duration float64
	URL      string // remote address, or the file path for local tracks
	Referer  string // page the host wants named when the file is fetched
	Ext      string
	License  string
	Credit   string
	Keywords string // extra search words, such as the Chinese names of the genre and mood
	Page     string
}

// Key identifies the track across providers.
func (t Track) Key() string { return string(t.Provider) + ":" + t.ID }

const userAgent = "btelo-video-cut/1.0 (+https://github.com/btelolabs/btelo-video-cut)"

// Library is safe for concurrent use.
type Library struct {
	dir    string
	client *http.Client

	mu         sync.Mutex
	config     *Config
	generation int // counts configuration changes, so a catalog fetched for an older one is not kept
	catalogs   map[string][]Track
	searches   map[string]Page
	downloads  map[string]*download
	local      []Track
}

type download struct {
	done        chan struct{}
	path        string
	err         error
	read, total int64
}

// New keeps its cache under dir and searches the sources of config.
func New(dir string, config *Config) *Library {
	l := &Library{dir: dir, client: &http.Client{}, config: config, catalogs: map[string][]Track{}, searches: map[string]Page{},
		downloads: map[string]*download{}}
	l.loadLocal()
	return l
}

// Config is the configuration in use.
func (l *Library) Config() *Config {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.config
}

// SetConfig switches to another configuration and forgets results of the old one.
func (l *Library) SetConfig(c *Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.config = c
	l.generation++
	clear(l.catalogs)
	clear(l.searches)
}

func (l *Library) get(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	for name, v := range headers {
		if v != "" {
			req.Header.Set(name, v)
		}
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

// fetchCached returns the body at url, reusing a copy in the cache younger than maxAge; a stale copy is used when
// the network fails.
func (l *Library) fetchCached(ctx context.Context, url string, headers map[string]string, name string, maxAge time.Duration) ([]byte, error) {
	path := filepath.Join(l.dir, "catalog", name)
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < maxAge {
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
	}
	data, err := l.fetch(ctx, url, headers)
	if err != nil {
		if stale, staleErr := os.ReadFile(path); staleErr == nil {
			return stale, nil
		}
		return nil, err
	}
	if err := writeAtomic(path, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (l *Library) fetch(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := l.get(ctx, url, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// file is where a remote track is cached.
func (l *Library) file(t Track) string {
	sum := sha1.Sum([]byte(t.Key()))
	ext := t.Ext
	if ext == "" {
		ext = "mp3"
	}
	return filepath.Join(l.dir, "sounds", string(t.Provider), hex.EncodeToString(sum[:10])+"."+ext)
}

// Cached returns the local file of t when it is already available.
func (l *Library) Cached(t Track) (string, bool) {
	if t.Provider == Local {
		_, err := os.Stat(t.URL)
		return t.URL, err == nil
	}
	path := l.file(t)
	_, err := os.Stat(path)
	return path, err == nil
}

// Progress reports a running download of t: bytes read and total (0 when unknown); ok is false when none runs.
func (l *Library) Progress(t Track) (read, total int64, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d, ok := l.downloads[t.Key()]
	if !ok {
		return 0, 0, false
	}
	return d.read, d.total, true
}

// Download fetches t into the cache once, however many callers ask at the same time, and returns its path.
func (l *Library) Download(ctx context.Context, t Track) (string, error) {
	if path, ok := l.Cached(t); ok {
		return path, nil
	}
	if t.Provider == Local {
		return "", fmt.Errorf("local file is missing: %s", t.URL)
	}
	l.mu.Lock()
	d, running := l.downloads[t.Key()]
	if !running {
		d = &download{done: make(chan struct{})}
		l.downloads[t.Key()] = d
		go l.transfer(t, d)
	}
	l.mu.Unlock()
	select {
	case <-d.done:
		return d.path, d.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// transfer runs detached from any caller so a cancelled preview does not waste the bytes already fetched.
func (l *Library) transfer(t Track, d *download) {
	defer func() {
		l.mu.Lock()
		delete(l.downloads, t.Key())
		l.mu.Unlock()
		close(d.done)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	resp, err := l.get(ctx, t.URL, map[string]string{"Referer": t.Referer})
	if err != nil {
		d.err = fmt.Errorf("download failed: %w", err)
		return
	}
	defer resp.Body.Close()
	l.mu.Lock()
	d.total = max(resp.ContentLength, 0)
	l.mu.Unlock()
	path := l.file(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		d.err = err
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "part-*")
	if err != nil {
		d.err = err
		return
	}
	defer os.Remove(tmp.Name())
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				tmp.Close()
				d.err = werr
				return
			}
			l.mu.Lock()
			d.read += int64(n)
			l.mu.Unlock()
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			tmp.Close()
			d.err = fmt.Errorf("download interrupted: %w", rerr)
			return
		}
	}
	if err := tmp.Close(); err != nil {
		d.err = err
		return
	}
	if d.total > 0 && d.read != d.total {
		d.err = fmt.Errorf("download incomplete: %d of %d bytes", d.read, d.total)
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		d.err = err
		return
	}
	d.path = path
}

func (l *Library) cachedPage(key string) (Page, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.searches[key]
	return p, ok
}

func (l *Library) keepPage(key string, p Page) {
	l.mu.Lock()
	l.searches[key] = p
	l.mu.Unlock()
}

// Matches reports whether every word of query appears in the track's text fields.
func Matches(t Track, query string) bool {
	hay := strings.ToLower(t.Title + " " + t.Artist + " " + t.Genre + " " + t.Mood + " " + t.Keywords)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}
