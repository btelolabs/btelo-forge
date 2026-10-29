package update

import (
	"archive/zip"
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
	"slices"
	"testing"
)

// Releases are compared by their numbers; a build that is not a release is never older than one.
func TestNewer(t *testing.T) {
	for _, c := range []struct {
		release, current string
		want             bool
	}{
		{"v0.3.2", "v0.3.1", true},
		{"v0.3.10", "v0.3.9", true},
		{"v1.0.0", "v0.9.9", true},
		{"0.4.0", "v0.3.1", true},
		{"v0.3.1", "v0.3.1", false},
		{"v0.3.0", "v0.3.1", false},
		{"v0.3.2", "dev", false},
		{"v0.3.2", "dev-1a2b3c4", false},
		{"v0.3", "v0.2.0", false},
		{"latest", "v0.2.0", false},
	} {
		if got := Newer(c.release, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.release, c.current, got)
		}
	}
	if !Same("v0.3.1", "0.3.1") || Same("v0.3.1", "v0.3.2") || Same("dev", "dev") {
		t.Error("Same tells releases apart wrongly")
	}
}

// Each system gets the file the release build publishes for it.
func TestAssetName(t *testing.T) {
	for _, c := range [][3]string{
		{"windows", "amd64", "btelo-forge-windows-amd64.zip"},
		{"windows", "arm64", "btelo-forge-windows-arm64.zip"},
		{"darwin", "arm64", "btelo-forge-macos.zip"},
		{"darwin", "amd64", "btelo-forge-macos.zip"},
		{"windows", "386", ""},
		{"linux", "amd64", ""},
	} {
		if got := AssetName(c[0], c[1]); got != c[2] {
			t.Errorf("AssetName(%s, %s) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// Latest reads GitHub's answer, and turns away an error or a release that is not a version.
func TestLatest(t *testing.T) {
	answer, status := `{"tag_name":"v0.4.0","html_url":"https://example.com/r","assets":[{"name":"a.zip","browser_download_url":"https://example.com/a.zip","size":3,"digest":"sha256:00"}]}`, http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent: GitHub refuses requests without one")
		}
		w.WriteHeader(status)
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	r, err := Latest(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := r.Asset("a.zip"); r.Tag != "v0.4.0" || r.Page != "https://example.com/r" || !ok || a.Size != 3 {
		t.Fatalf("read %+v", r)
	}
	if _, ok := r.Asset("b.zip"); ok {
		t.Error("found a file the release does not have")
	}
	status = http.StatusNotFound
	if _, err := Latest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Error("a 404 gave a release")
	}
	answer, status = `{"tag_name":"nightly"}`, http.StatusOK
	if _, err := Latest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Error("a release that is not a version was taken")
	}
	answer = `{"tag_name":`
	if _, err := Latest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Error("a broken answer was taken")
	}
}

func digest(data []byte) string {
	s := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(s[:])
}

// A download is kept only when it is whole and its checksum is the release's; without a checksum nothing is
// downloaded at all.
func TestDownload(t *testing.T) {
	data := []byte("the new version of the app")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "app.zip")
	var last [2]int64
	good := Asset{Name: "app.zip", URL: srv.URL, Size: int64(len(data)), Digest: digest(data)}
	if err := Download(context.Background(), srv.Client(), good, dest, func(done, total int64) { last = [2]int64{done, total} }); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(data) {
		t.Fatalf("downloaded %q", got)
	}
	if last != [2]int64{int64(len(data)), int64(len(data))} {
		t.Errorf("progress ended at %v", last)
	}
	os.Remove(dest)
	for name, c := range map[string]struct {
		a    Asset
		want error
	}{
		"wrong checksum": {Asset{Name: "app.zip", URL: srv.URL, Size: int64(len(data)), Digest: digest([]byte("else"))}, ErrChecksum},
		"no checksum":    {Asset{Name: "app.zip", URL: srv.URL, Size: int64(len(data))}, ErrNoChecksum},
		"other checksum": {Asset{Name: "app.zip", URL: srv.URL, Digest: "md5:0123"}, ErrNoChecksum},
		"cut short":      {Asset{Name: "app.zip", URL: srv.URL, Size: int64(len(data)) + 5, Digest: digest(data)}, nil},
		"too long":       {Asset{Name: "app.zip", URL: srv.URL, Size: 4, Digest: digest(data)}, nil},
	} {
		err := Download(context.Background(), srv.Client(), c.a, dest, func(int64, int64) {})
		if err == nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) > 0 {
			t.Errorf("%s left %v", name, left)
		}
	}
}

// makeZip writes an archive of the files given by name.
func makeZip(t *testing.T, path string, files map[string]string, modes map[string]os.FileMode) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		mode := os.FileMode(0o644)
		if m, ok := modes[n]; ok {
			mode = m
		}
		h.SetMode(mode)
		out, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		out.Write([]byte(files[n]))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// An archive unpacks with its folders and the permissions of its programs; one naming a place outside the folder it
// unpacks into is refused.
func TestUnpack(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")
	makeZip(t, archive, map[string]string{
		"Btelo Forge/Btelo Forge.exe": "exe",
		"Btelo Forge/LICENSE":         "licence",
		"Btelo Forge/run":             "#!/bin/sh",
	}, map[string]os.FileMode{"Btelo Forge/run": 0o755})
	out := filepath.Join(dir, "out")
	if err := Unpack(archive, out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "Btelo Forge", "LICENSE")); string(got) != "licence" {
		t.Fatalf("LICENSE holds %q", got)
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(filepath.Join(out, "Btelo Forge", "run")); err != nil || st.Mode().Perm()&0o100 == 0 {
			t.Errorf("the program lost its permission to run: %v %v", st.Mode(), err)
		}
	}
	for _, name := range []string{"../evil.txt", "Btelo Forge/../../evil.txt", "/evil.txt", `..\evil.txt`} {
		bad := filepath.Join(dir, "bad.zip")
		makeZip(t, bad, map[string]string{name: "x"}, nil)
		if err := Unpack(bad, filepath.Join(dir, "bad")); err == nil {
			t.Errorf("%q was unpacked", name)
		}
		if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
			t.Fatalf("%q was written outside the folder", name)
		}
	}
}

// write makes the files given by name under root.
func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// On Windows the program and the files beside it are replaced, the program keeping the name it runs under; on
// macOS the app bundle is, unless macOS runs it from a read-only copy or it is not in a bundle at all.
func TestTargets(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "staged")
	write(t, staged, map[string]string{
		"Btelo Forge/Btelo Forge.exe":                                 "new exe",
		"Btelo Forge/LICENSE":                                         "new licence",
		"Btelo Forge/Btelo Forge.app/Contents/MacOS/btelo-forge":      "new program",
		"Btelo Forge/Btelo Forge.app/Contents/Info.plist":             "plist",
		"__MACOSX/Btelo Forge/._Btelo Forge.app":                      "resource fork",
		"Btelo Forge/Btelo Forge.app/Contents/Resources/AppIcon.icns": "icon",
	})
	install := filepath.Join(dir, "install")
	win, err := TargetFor("windows", filepath.Join(install, "forge.exe"))
	if err != nil || win.Dir != install || win.Launch != filepath.Join(install, "forge.exe") {
		t.Fatalf("windows target %+v, %v", win, err)
	}
	pairs, err := win.Pairs(staged)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		filepath.Join(install, "forge.exe"):       filepath.Join(staged, "Btelo Forge", "Btelo Forge.exe"),
		filepath.Join(install, "LICENSE"):         filepath.Join(staged, "Btelo Forge", "LICENSE"),
		filepath.Join(install, "Btelo Forge.app"): filepath.Join(staged, "Btelo Forge", "Btelo Forge.app"),
	}
	if len(pairs) != len(want) {
		t.Fatalf("pairs %v", pairs)
	}
	for _, p := range pairs {
		if want[p.To] != p.From {
			t.Errorf("%s would be replaced by %s", p.To, p.From)
		}
	}
	if _, err := win.Pairs(filepath.Join(dir, "nothing")); !errors.Is(err, ErrNotThisApp) {
		t.Errorf("an empty download gave %v", err)
	}

	apps := filepath.Join(dir, "Applications")
	exe := filepath.Join(apps, "Btelo Forge.app", "Contents", "MacOS", "btelo-forge")
	mac, err := TargetFor("darwin", exe)
	if err != nil || mac.Dir != apps || mac.Launch != filepath.Join(apps, "Btelo Forge.app") {
		t.Fatalf("macOS target %+v, %v", mac, err)
	}
	pairs, err = mac.Pairs(staged)
	if err != nil || len(pairs) != 1 || pairs[0] != (Pair{From: filepath.Join(staged, "Btelo Forge", "Btelo Forge.app"), To: mac.Launch}) {
		t.Fatalf("macOS pairs %v, %v", pairs, err)
	}
	if _, err := TargetFor("darwin", "/private/var/folders/x/AppTranslocation/1234/d/Btelo Forge.app/Contents/MacOS/btelo-forge"); !errors.Is(err, ErrTranslocated) {
		t.Errorf("a translocated app gave %v", err)
	}
	if _, err := TargetFor("darwin", filepath.Join(dir, "bin", "btelo-forge")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a program outside a bundle gave %v", err)
	}
	if _, err := TargetFor("linux", exe); !errors.Is(err, ErrUnsupported) {
		t.Errorf("linux gave %v", err)
	}
	if err := Writable(dir); err != nil {
		t.Errorf("a temporary folder is not writable: %v", err)
	}
	if err := Writable(filepath.Join(dir, "missing")); !errors.Is(err, ErrReadOnly) {
		t.Errorf("a missing folder gave %v", err)
	}
}

// Replacing puts every new file in place and keeps the old ones aside until Cleanup; a failure part way puts every
// old one back.
func TestReplace(t *testing.T) {
	dir := t.TempDir()
	install, staged := filepath.Join(dir, "install"), filepath.Join(dir, "staged")
	write(t, install, map[string]string{"app.exe": "old exe", "README.md": "old readme", "user.txt": "the user's"})
	write(t, staged, map[string]string{"app.exe": "new exe", "README.md": "new readme", "NOTICE": "new notice", "bundle/Contents/x": "new x"})
	pairs := []Pair{
		{filepath.Join(staged, "app.exe"), filepath.Join(install, "app.exe")},
		{filepath.Join(staged, "README.md"), filepath.Join(install, "README.md")},
		{filepath.Join(staged, "NOTICE"), filepath.Join(install, "NOTICE")},
		{filepath.Join(staged, "bundle"), filepath.Join(install, "bundle")},
	}

	// The third move in fails: the old exe and readme come back, nothing new stays.
	calls := 0
	rename = func(from, to string) error {
		calls++
		if calls == 5 {
			return fmt.Errorf("refused")
		}
		return os.Rename(from, to)
	}
	defer func() { rename = os.Rename }()
	if err := Replace(pairs); err == nil {
		t.Fatal("a failed move was not reported")
	}
	if read(t, filepath.Join(install, "app.exe")) != "old exe" || read(t, filepath.Join(install, "README.md")) != "old readme" {
		t.Fatal("the old files were not put back")
	}
	if left, _ := os.ReadDir(install); len(left) != 3 {
		var names []string
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Fatalf("after the failure the folder holds %v", names)
	}

	rename = os.Rename
	if err := Replace(pairs); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"app.exe": "new exe", "README.md": "new readme", "NOTICE": "new notice", "bundle/Contents/x": "new x", "user.txt": "the user's", "app.exe" + oldSuffix: "old exe"} {
		if got := read(t, filepath.Join(install, filepath.FromSlash(name))); got != want {
			t.Errorf("%s holds %q, want %q", name, got, want)
		}
	}
	if !Cleanup(install) {
		t.Fatal("Cleanup left something")
	}
	if left, _ := os.ReadDir(install); len(left) != 5 {
		t.Errorf("after Cleanup the folder holds %d entries", len(left))
	}

	// A copy that cannot be made changes nothing.
	missing := append(pairs[:1:1], Pair{filepath.Join(staged, "gone"), filepath.Join(install, "gone")})
	if err := Replace(missing); err == nil {
		t.Fatal("a missing new file was not reported")
	}
	if read(t, filepath.Join(install, "app.exe")) != "new exe" {
		t.Error("a failed copy changed the program")
	}
	if left, _ := filepath.Glob(filepath.Join(install, "*"+newSuffix)); len(left) > 0 {
		t.Errorf("a failed copy left %v", left)
	}
}
