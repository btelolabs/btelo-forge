package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPageNeedsTheKeyAndServesExports(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "旅行 vlog.cut.mp4")
	os.WriteFile(video, []byte("0123456789"), 0o644)
	files := func() []File { return []File{{Path: video, At: time.Now()}, {Path: filepath.Join(dir, "gone.mp4")}} }
	text := func() Text { return Text{Title: "Exports", Empty: "none", Download: "Download", Refresh: "Refresh"} }
	s := httptest.NewServer(Handler("secret", files, text))
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
