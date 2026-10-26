package web

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestPageNeedsTheKeyAndServesExports(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "旅行 vlog.cut.mp4")
	os.WriteFile(video, []byte("0123456789"), 0o644)
	files := func() []File { return []File{{Path: video, At: time.Now()}, {Path: filepath.Join(dir, "gone.mp4")}} }
	s := httptest.NewServer(Handler("secret", Site{Files: files, Text: testText, UploadDir: func() (string, error) { return dir, nil }, Received: func([]string) {}}))
	defer s.Close()

	get := func(path string, header ...string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", s.URL+path, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(body)
	}
	if resp, _ := get("/"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("page without the key: %d", resp.StatusCode)
	}
	if resp, _ := get("/?k=wrong"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("page with a wrong key: %d", resp.StatusCode)
	}
	resp, body := get("/?k=secret")
	if resp.StatusCode != 200 || !strings.Contains(body, `hx-get="/list"`) || !strings.Contains(body, "/htmx.min.js") {
		t.Fatalf("page %d %s", resp.StatusCode, body)
	}
	_, body = get("/list?k=secret")
	if !strings.Contains(body, "旅行 vlog.cut.mp4") || strings.Contains(body, "gone.mp4") || !strings.Contains(body, "Download") {
		t.Fatalf("list %s", body)
	}
	resp, body = get("/file/0/x.mp4?k=secret", "Range", "bytes=2-4")
	if resp.StatusCode != http.StatusPartialContent || body != "234" {
		t.Fatalf("range %d %q", resp.StatusCode, body)
	}
	if resp, _ := get("/file/7/x.mp4?k=secret"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing file %d", resp.StatusCode)
	}
	if resp, js := get("/htmx.min.js"); resp.StatusCode != 200 || !strings.HasPrefix(js, "var htmx") {
		t.Fatal("htmx not served")
	}
}

func testText() Text {
	return Text{Title: "Exports", Empty: "none", Download: "Download", Refresh: "Refresh", Exports: "Exported", Upload: "Upload",
		UploadHint: "to the computer", Send: "Send", Uploaded: "Sent %d: %s", Refused: "Skipped %d: %s", Failed: "Failed: %v"}
}

func upload(t *testing.T, url string, files map[string]string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		part, _ := w.CreateFormFile("files", name)
		part.Write([]byte(files[name]))
	}
	w.Close()
	req, _ := http.NewRequest("POST", url, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func TestPhonesUploadMediaIntoTheFolder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("old"), 0o644)
	var received []string
	site := Site{Files: func() []File { return nil }, Text: testText, UploadDir: func() (string, error) { return dir, nil },
		Received: func(paths []string) { received = append(received, paths...) }}
	s := httptest.NewServer(Handler("secret", site))
	defer s.Close()
	if code, _ := upload(t, s.URL+"/upload", map[string]string{"clip.mp4": "x"}); code != http.StatusForbidden {
		t.Fatalf("upload without the key: %d", code)
	}
	code, body := upload(t, s.URL+"/upload?k=secret", map[string]string{"clip.mp4": "new", "../../evil.mov": "e", "notes.exe": "no", "照片.JPG": "jpg"})
	if code != 200 || !strings.Contains(body, "Sent 3") || !strings.Contains(body, "Skipped 1: notes.exe") {
		t.Fatalf("%d %s", code, body)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "clip.mp4")); string(data) != "old" {
		t.Fatal("an upload replaced an existing file")
	}
	for name, want := range map[string]string{"clip (2).mp4": "new", "evil.mov": "e", "照片.JPG": "jpg"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != want {
			t.Fatalf("%s: %q %v", name, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.mov")); err == nil {
		t.Fatal("a name with .. escaped the folder")
	}
	if len(received) != 3 {
		t.Fatalf("received %v", received)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".upload-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
	failing := site
	failing.UploadDir = func() (string, error) { return "", errors.New("no folder") }
	f := httptest.NewServer(Handler("secret", failing))
	defer f.Close()
	if _, body := upload(t, f.URL+"/upload?k=secret", map[string]string{"a.mp4": "a"}); !strings.Contains(body, "Failed: no folder") {
		t.Fatal(body)
	}
}

func TestCleanNameKeepsOnlyASafeBaseName(t *testing.T) {
	for in, want := range map[string]string{`C:\Users\me\a.mp4`: "a.mp4", "../x.mov": "x.mov", "..": "upload", "a:b?.mp4": "a_b_.mp4", ".hidden.mp4": "hidden.mp4"} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}
