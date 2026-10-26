package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const kevinJSON = "\xef\xbb\xbf" + `[{"title":"The Britons","filename":"The Britons.mp3","length":"00:05:07","genre":"22","feel":"Ren Faire, Calming"},
{"title":"","filename":"x.mp3"}]`

const openverseJSON = `{"page_count":3,"results":[
{"id":"a1","title":"Whoosh","creator":"k","url":"https://cdn.example/w.wav","license":"cc0","license_version":"1.0","attribution":"x","duration":3155,"source":"freesound"},
{"id":"b2","title":"Song","creator":"c","url":"https://s.example/?trackid=1&format=mp32","license":"by","license_version":"3.0","attribution":"\"Song\" by c","duration":120000,"genres":["pop"],"source":"jamendo"}]}`

func server(t *testing.T, hits *atomic.Int32) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "pieces.json"):
			w.Write([]byte(kevinJSON))
		case strings.HasPrefix(r.URL.Path, "/audio/"):
			if r.URL.Query().Get("license") != "cc0,by" {
				http.Error(w, "license filter missing", 400)
				return
			}
			w.Write([]byte(openverseJSON))
		case strings.HasPrefix(r.URL.Path, "/mp3-royaltyfree/"):
			w.Header().Set("Content-Length", "5")
			w.Write([]byte("ID3xx"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// sitesAt is the built-in configuration with every site moved to a test server.
func sitesAt(t *testing.T, base string) *Config {
	t.Helper()
	data := strings.NewReplacer(
		"https://incompetech.com/music/royalty-free/", base+"/",
		"https://api.openverse.org/v1/audio/", base+"/audio/",
		"https://ccmixter.org/", base+"/",
		"https://archive.org/", base+"/",
	).Replace(string(DefaultJSON))
	c, err := ParseConfig([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testLibrary(t *testing.T, s *httptest.Server) *Library {
	return New(t.TempDir(), sitesAt(t, s.URL))
}

func TestKevinCatalogParsesAndCreditsEveryTrack(t *testing.T) {
	var hits atomic.Int32
	l := testLibrary(t, server(t, &hits))
	tracks, err := l.Catalog(context.Background(), "kevin")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("%d tracks, want the one with a title", len(tracks))
	}
	tr := tracks[0]
	if tr.Duration != 307 || tr.Genre != "Soundtrack" || tr.Mood != "Ren Faire · Calming" || !strings.Contains(tr.Credit, "Kevin MacLeod") {
		t.Fatalf("%+v", tr)
	}
	if !strings.HasSuffix(tr.URL, "/mp3-royaltyfree/The%20Britons.mp3") {
		t.Fatal(tr.URL)
	}
	if !Matches(tr, "britons 平静") || !Matches(tr, "配乐 calming") || Matches(tr, "rock") {
		t.Fatal("search words")
	}
}

func TestCatalogFallsBackToAStaleCopyOffline(t *testing.T) {
	var hits atomic.Int32
	s := server(t, &hits)
	l := testLibrary(t, s)
	if _, err := l.Catalog(context.Background(), "kevin"); err != nil {
		t.Fatal(err)
	}
	cached, _ := filepath.Glob(filepath.Join(l.dir, "catalog", "kevin-*.json"))
	if len(cached) != 1 {
		t.Fatalf("cached catalogues %v", cached)
	}
	old := mustStat(t, cached[0]).ModTime().AddDate(0, 0, -30)
	os.Chtimes(cached[0], old, old)
	s.Close()
	offline := New(l.dir, l.Config())
	if tracks, err := offline.Catalog(context.Background(), "kevin"); err != nil || len(tracks) != 1 {
		t.Fatalf("offline catalogue %v %v", tracks, err)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestOpenverseSearchFiltersLicensesAndCaches(t *testing.T) {
	var hits atomic.Int32
	l := testLibrary(t, server(t, &hits))
	p, err := l.Search(context.Background(), "freesound", "whoosh", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if p.Pages != 3 || len(p.Tracks) != 2 {
		t.Fatalf("%+v", p)
	}
	if p.Tracks[0].Credit != "" || p.Tracks[0].Ext != "wav" || p.Tracks[0].Duration != 3.155 {
		t.Fatalf("cc0 track %+v", p.Tracks[0])
	}
	if p.Tracks[1].Credit == "" || p.Tracks[1].Ext != "mp3" {
		t.Fatalf("cc-by track %+v", p.Tracks[1])
	}
	before := hits.Load()
	if _, err := l.Search(context.Background(), "freesound", "whoosh", "", 1); err != nil || hits.Load() != before {
		t.Fatal("repeated search went to the network")
	}
}

func TestConcurrentDownloadsFetchOnceAndCache(t *testing.T) {
	var hits atomic.Int32
	l := testLibrary(t, server(t, &hits))
	tracks, err := l.Catalog(context.Background(), "kevin")
	if err != nil || len(tracks) == 0 {
		t.Fatal(tracks, err)
	}
	before := hits.Load()
	var wg sync.WaitGroup
	paths := make([]string, 4)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := l.Download(context.Background(), tracks[0])
			if err != nil {
				t.Error(err)
			}
			paths[i] = p
		}()
	}
	wg.Wait()
	if got := hits.Load() - before; got != 1 {
		t.Fatalf("%d downloads, want 1", got)
	}
	if data, err := os.ReadFile(paths[0]); err != nil || string(data) != "ID3xx" {
		t.Fatalf("%q %v", data, err)
	}
	if p, ok := l.Cached(tracks[0]); !ok || p != paths[0] {
		t.Fatal("not cached")
	}
}

func TestLocalTracksPersist(t *testing.T) {
	dir := t.TempDir()
	song := filepath.Join(dir, "My Song.mp3")
	os.WriteFile(song, []byte("x"), 0o644)
	l := New(filepath.Join(dir, "cache"), DefaultConfig())
	if _, err := l.AddLocal(song, 12); err != nil {
		t.Fatal(err)
	}
	l.AddLocal(song, 12)
	again := New(filepath.Join(dir, "cache"), DefaultConfig())
	got := again.LocalTracks()
	if len(got) != 1 || got[0].Title != "My Song" || got[0].Duration != 12 {
		t.Fatalf("%+v", got)
	}
	if p, ok := again.Cached(got[0]); !ok || p != song {
		t.Fatal("local file not usable")
	}
	if !IsAudio("a.FLAC") || IsAudio("a.mp4") {
		t.Fatal("IsAudio")
	}
}
