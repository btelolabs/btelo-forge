package media

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The test binary stands in for ffmpeg: started with fakeVersion set, it prints that version and exits.
const fakeVersion = "BVC_FAKE_FFMPEG_VERSION"

func TestMain(m *testing.M) {
	if v := os.Getenv(fakeVersion); v != "" {
		fmt.Printf("ffmpeg version %s Copyright (c) the FFmpeg developers\n", v)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNewEnoughReadsReleaseAndDevelopmentVersions(t *testing.T) {
	min := [2]int{6, 0}
	for v, want := range map[string]bool{
		"9.0.2": true, "n7.1": true, "6.0": true, "6.1.1-full_build-www.gyan.dev": true, "5.1.2": false, "4.4.2-0ubuntu0.22.04.1": false,
		"N-127654-g7045cc9b80": true, "2024-09-01-git-abc": true, "git-2023-10-01-abc": true, "unknown": false,
	} {
		if got := newEnough(v, min); got != want {
			t.Errorf("newEnough(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestToolsConfigErrorsArePrecise(t *testing.T) {
	if len(DefaultToolsConfig().For("windows", "amd64")) == 0 || len(DefaultToolsConfig().For("darwin", "arm64")) == 0 {
		t.Fatal("the built-in configuration has no download for Windows or Apple silicon")
	}
	base := string(DefaultToolsJSON)
	for data, want := range map[string]string{
		strings.Replace(base, `"minimum": "6.0"`, `"minimum": "six"`, 1):    "minimum",
		strings.Replace(base, `"os": "windows"`, `"os": "plan9"`, 1):        "os must be",
		strings.Replace(base, `win64-gpl-9.0.zip"`, `win64-gpl-9.0.7z"`, 1): ".zip",
		strings.Replace(base, `"version": 1`, `"version": 1, "x": 1`, 1):    "unknown field",
	} {
		if _, err := ParseToolsConfig([]byte(data)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v, want %q", err, want)
		}
	}
}

// fakeArchive zips copies of the test binary as ffmpeg and ffprobe, inside a folder as the real builds do.
func fakeArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, n := range names {
		f, _ := w.Create("ffmpeg-9.0-build/bin/" + programName(n))
		f.Write(body)
	}
	readme, _ := w.Create("ffmpeg-9.0-build/LICENSE.txt")
	readme.Write([]byte("GPL"))
	w.Close()
	return buf.Bytes()
}

func TestInstallDownloadsUnpacksAndChecksTheVersion(t *testing.T) {
	both := fakeArchive(t, "ffmpeg", "ffprobe")
	onlyProbe := fakeArchive(t, "ffprobe")
	sum := sha256.Sum256(both)
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/broken.zip":
			http.Error(w, "gone", http.StatusNotFound)
		case "/both.zip":
			w.Write(both)
		case "/probe.zip":
			w.Write(onlyProbe)
		}
	}))
	defer s.Close()
	config := func(downloads string) *ToolsConfig {
		c, err := ParseToolsConfig([]byte(`{"version": 1, "minimum": "6.0", "downloads": [` + downloads + `]}`))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	here := fmt.Sprintf(`"os": %q, "arch": %q`, runtime.GOOS, runtime.GOARCH)
	c := config(fmt.Sprintf(`{"name": "broken", %s, "archives": [{"url": "%s/broken.zip"}]},
		{"name": "good", %s, "archives": [{"url": "%s/both.zip", "sha256": %q}]}`, here, s.URL, here, s.URL, hex.EncodeToString(sum[:])))
	root := filepath.Clean(t.TempDir())
	t.Setenv(fakeVersion, "9.0.2")
	if _, err := Locate(context.Background(), c, root); err == nil && os.Getenv("PATH") == "" {
		t.Fatal("found ffmpeg before installing it")
	}
	var progress []float64
	tools, err := Install(context.Background(), c, root, http.DefaultClient, func(f float64) { progress = append(progress, f) })
	if err != nil {
		t.Fatal(err)
	}
	if tools.Version != "9.0.2" || !strings.HasPrefix(tools.FFmpeg, root) || len(progress) == 0 || progress[len(progress)-1] != 1 {
		t.Fatalf("%+v progress %v", tools, progress)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(root, "*.part")); len(leftovers) != 0 {
		t.Fatalf("left %v", leftovers)
	}
	again, err := Locate(context.Background(), c, root)
	if err != nil || again.FFmpeg == "" {
		t.Fatalf("the installed ffmpeg is not found again: %v", err)
	}

	t.Setenv(fakeVersion, "4.4.2")
	old := config(fmt.Sprintf(`{"name": "old", %s, "archives": [{"url": "%s/both.zip"}]}`, here, s.URL))
	if _, err := Install(context.Background(), old, t.TempDir(), http.DefaultClient, func(float64) {}); !errors.Is(err, ErrTooOld) {
		t.Fatalf("an old download was accepted: %v", err)
	}
	t.Setenv(fakeVersion, "9.0.2")
	half := config(fmt.Sprintf(`{"name": "half", %s, "archives": [{"url": "%s/probe.zip"}]}`, here, s.URL))
	if _, err := Install(context.Background(), half, t.TempDir(), http.DefaultClient, func(float64) {}); err == nil || !strings.Contains(err.Error(), "no ffmpeg") {
		t.Fatalf("an archive without ffmpeg was accepted: %v", err)
	}
	bad := config(fmt.Sprintf(`{"name": "bad", %s, "archives": [{"url": "%s/both.zip", "sha256": %q}]}`, here, s.URL, strings.Repeat("0", 64)))
	if _, err := Install(context.Background(), bad, t.TempDir(), http.DefaultClient, func(float64) {}); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("a wrong checksum was accepted: %v", err)
	}
}
