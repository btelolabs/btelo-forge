package library

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// AudioExtensions are the file types accepted as music.
var AudioExtensions = []string{".mp3", ".wav", ".m4a", ".aac", ".flac", ".ogg", ".opus", ".wma"}

// IsAudio reports whether path looks like a sound file.
func IsAudio(path string) bool {
	return slices.Contains(AudioExtensions, strings.ToLower(filepath.Ext(path)))
}

func hashName(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:10])
}

func (l *Library) localFile() string { return filepath.Join(l.dir, "local.json") }

func (l *Library) loadLocal() {
	data, err := os.ReadFile(l.localFile())
	if err != nil {
		return
	}
	var tracks []Track
	if json.Unmarshal(data, &tracks) == nil {
		l.local = tracks
	}
}

// LocalTracks lists imported files, newest first.
func (l *Library) LocalTracks() []Track {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.local)
}

// AddLocal remembers a file (for example one downloaded from YouTube Studio's Audio Library) and returns its track.
func (l *Library) AddLocal(path string, duration float64) (Track, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Track{}, err
	}
	t := Track{
		Provider: Local,
		ID:       hashName(strings.ToLower(abs)),
		Title:    strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)),
		Artist:   filepath.Base(filepath.Dir(abs)),
		Duration: duration,
		URL:      abs,
		Ext:      strings.TrimPrefix(strings.ToLower(filepath.Ext(abs)), "."),
		License:  "Local file",
	}
	l.mu.Lock()
	l.local = slices.DeleteFunc(l.local, func(x Track) bool { return x.ID == t.ID })
	l.local = slices.Insert(l.local, 0, t)
	data, err := json.MarshalIndent(l.local, "", "  ")
	l.mu.Unlock()
	if err != nil {
		return Track{}, err
	}
	return t, writeAtomic(l.localFile(), data)
}

// RemoveLocal forgets an imported file; the file itself is left alone.
func (l *Library) RemoveLocal(t Track) error {
	l.mu.Lock()
	l.local = slices.DeleteFunc(l.local, func(x Track) bool { return x.ID == t.ID })
	data, err := json.MarshalIndent(l.local, "", "  ")
	l.mu.Unlock()
	if err != nil {
		return err
	}
	return writeAtomic(l.localFile(), data)
}
