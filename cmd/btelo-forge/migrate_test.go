package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// saveEdit writes an edit naming paths, as the app saves one, and reads them back after a rewrite.
func saveEdit(t *testing.T, path string, paths map[string]string) func() map[string]string {
	data, _ := json.Marshal(paths)
	write(t, path, string(data))
	return func() map[string]string {
		var got map[string]string
		if err := json.Unmarshal([]byte(read(t, path)), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
}

func TestDataMovesToTheNewName(t *testing.T) {
	cache, config := t.TempDir(), t.TempDir()
	oldCache, oldState := filepath.Join(cache, oldDataName), filepath.Join(config, oldDataName)
	write(t, filepath.Join(oldCache, "library", "song.mp3"), "music")
	write(t, filepath.Join(oldState, "settings.json"), `{"language":"zh"}`)
	other := filepath.Join(config, oldDataName+"-backup", "keep.png")
	edit := saveEdit(t, filepath.Join(oldState, "edits", "e.json"), map[string]string{
		"image": filepath.Join(oldState, "pasted", "a.png"),
		"music": filepath.Join(oldCache, "library", "song.mp3"),
		"other": other, // in a folder whose name only begins like the old one
	})

	cacheDir, moves := dataDir(cache)
	stateDir, stateMoves := dataDir(config)
	for k, v := range stateMoves {
		moves[k] = v
	}
	if filepath.Base(cacheDir) != dataName || filepath.Base(stateDir) != dataName || len(moves) != 2 {
		t.Fatalf("moved to %s %s: %v", cacheDir, stateDir, moves)
	}
	if _, err := os.Stat(oldState); !os.IsNotExist(err) {
		t.Error("the old folder is still there")
	}
	if got := read(t, filepath.Join(stateDir, "settings.json")); got != `{"language":"zh"}` {
		t.Errorf("the settings came over as %q", got)
	}
	if err := rewritePaths(filepath.Join(stateDir, "edits"), moves); err != nil {
		t.Fatal(err)
	}
	edit = func() map[string]string {
		var got map[string]string
		json.Unmarshal([]byte(read(t, filepath.Join(stateDir, "edits", "e.json"))), &got)
		return got
	}
	got := edit()
	if got["image"] != filepath.Join(stateDir, "pasted", "a.png") || got["music"] != filepath.Join(cacheDir, "library", "song.mp3") {
		t.Errorf("rewritten paths %v", got)
	}
	if got["other"] != other {
		t.Errorf("a path outside the folder changed to %s", got["other"])
	}
	// The next run finds nothing more to move.
	if dir, moves := dataDir(config); len(moves) != 0 || dir != stateDir {
		t.Errorf("second run: %s, moved %v", dir, moves)
	}
	fresh := t.TempDir()
	if dir, moves := dataDir(fresh); len(moves) != 0 || dir != filepath.Join(fresh, dataName) {
		t.Errorf("fresh: %s, moved %v", dir, moves)
	}
}

// TestDataMergesWhenBothFoldersExist: a new folder already there (from a test run, or an earlier copy) gets what
// it lacks from the old one, and only what moved is renamed in the edits.
func TestDataMergesWhenBothFoldersExist(t *testing.T) {
	cache, config := t.TempDir(), t.TempDir()
	old, now := filepath.Join(cache, oldDataName), filepath.Join(cache, dataName)
	write(t, filepath.Join(now, "library", "catalog", "kevin.json"), "new catalog")
	write(t, filepath.Join(old, "library", "catalog", "kevin.json"), "old catalog")
	write(t, filepath.Join(old, "library", "catalog", "archive.json"), "archive")
	write(t, filepath.Join(old, "library", "sounds", "kevin", "song.mp3"), "music")
	write(t, filepath.Join(old, "library", "catalog", "newer.json"), "old but newer")
	write(t, filepath.Join(now, "library", "catalog", "newer.json"), "new but older")
	past := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(now, "library", "catalog", "newer.json"), past, past)
	os.Chtimes(filepath.Join(old, "library", "catalog", "kevin.json"), past, past)
	write(t, filepath.Join(old, "whisper", "model.bin"), "model")
	write(t, filepath.Join(old, "ffmpeg", "ffmpeg.exe"), "tool")
	edit := saveEdit(t, filepath.Join(config, dataName, "edits", "e.json"), map[string]string{
		"music": filepath.Join(old, "library", "sounds", "kevin", "song.mp3"),
		"kept":  filepath.Join(old, "library", "catalog", "kevin.json"),
	})

	dir, moves := dataDir(cache)
	if dir != now {
		t.Fatalf("the data is in %s", dir)
	}
	for _, f := range []string{"whisper/model.bin", "ffmpeg/ffmpeg.exe", "library/sounds/kevin/song.mp3", "library/catalog/archive.json"} {
		if _, err := os.Stat(filepath.Join(now, f)); err != nil {
			t.Errorf("%s did not move: %v", f, err)
		}
	}
	if got := read(t, filepath.Join(now, "library", "catalog", "kevin.json")); got != "new catalog" {
		t.Errorf("the new folder's copy was replaced by %q", got)
	}
	if got := read(t, filepath.Join(now, "library", "catalog", "newer.json")); got != "old but newer" {
		t.Errorf("the newer copy from the old folder did not win: %q", got)
	}
	// The older copy of a file in both stays in the old folder, so the old folder stays with it.
	if got := read(t, filepath.Join(old, "library", "catalog", "kevin.json")); got != "old catalog" {
		t.Errorf("the old copy is %q", got)
	}
	if err := rewritePaths(filepath.Join(config, dataName, "edits"), moves); err != nil {
		t.Fatal(err)
	}
	got := edit()
	if got["music"] != filepath.Join(now, "library", "sounds", "kevin", "song.mp3") {
		t.Errorf("the moved song is named %s", got["music"])
	}
	if got["kept"] != filepath.Join(old, "library", "catalog", "kevin.json") {
		t.Errorf("a file that did not move was renamed to %s", got["kept"])
	}
	// Once only empty folders are left, the old folder goes.
	os.Remove(filepath.Join(old, "library", "catalog", "kevin.json"))
	if _, moves := dataDir(cache); len(moves) != 0 {
		t.Errorf("moved again: %v", moves)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the emptied old folder is still there")
	}
}
