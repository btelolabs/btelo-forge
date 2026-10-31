package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKindsAreListedWithTheirFolders(t *testing.T) {
	want := map[Kind]string{KindLLM: "", KindImage: "ai-image", KindVoice: "ai-voice", KindTranscribe: "", KindModel3D: "ai-3dmodel", KindVideo: "ai-video", KindMatting: ""}
	if len(Kinds) != len(want) {
		t.Fatalf("%d kinds", len(Kinds))
	}
	for _, k := range Kinds {
		if !k.Valid() || k.Folder() != want[k] || k.Makes() != (want[k] != "") {
			t.Errorf("%s: valid %v, folder %q", k, k.Valid(), k.Folder())
		}
	}
	if Kind("music").Valid() || Kind("").Or() != KindLLM || KindImage.Or() != KindImage {
		t.Fatal("kind checks")
	}
}

func TestChoicesCarryTheirKind(t *testing.T) {
	c := DefaultConfig()
	for id, want := range map[string]Kind{"qwen3-8b": KindLLM, "grok/grok-4.7": KindLLM, "grok/grok-imagine-image-2.0": KindImage, "gemini/gemini-3-pro-image": KindImage} {
		ch, ok := c.Choice(id)
		if !ok || ch.Kind != want {
			t.Errorf("%s: %q, %v", id, ch.Kind, ok)
		}
	}
	images := 0
	for _, ch := range c.Choices() {
		if ch.Kind == "" {
			t.Errorf("%s has no kind", ch.ID)
		}
		if ch.Kind == KindImage {
			images++
		}
	}
	if images < 2 {
		t.Fatalf("%d image models", images)
	}
}

func TestKindMistakesAreNamed(t *testing.T) {
	good := string(DefaultJSON)
	for name, c := range map[string]struct{ from, to, want string }{
		"online": {`"kind": "image", "edits": true, "name": "Grok Imagine Image 2.0"`, `"kind": "music", "name": "Grok Imagine Image 2.0"`, "kind must be"},
		"3d":     {`"kind": "image", "edits": true, "name": "Grok Imagine Image 2.0"`, `"kind": "model3d", "name": "Grok Imagine Image 2.0"`, "needs a task"},
		"vision": {`"kind": "image", "edits": true, "name": "Grok Imagine Image 2.0"`, `"kind": "image", "vision": true, "name": "Grok Imagine Image 2.0"`, "vision is for language"},
		"local":  {`"id": "qwen3-4b",`, `"id": "qwen3-4b", "kind": "image",`, "kind must be"},
	} {
		if !strings.Contains(good, c.from) {
			t.Fatalf("%s: the test text is not in the built-in file", name)
		}
		_, err := ParseConfig([]byte(strings.Replace(good, c.from, c.to, 1)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestSaveAssetKeepsEachKindInItsFolderAndNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 10, 30, 9, 8, 7, 0, time.UTC)
	first, err := SaveAsset(root, KindImage, "A red fox, in the snow!", ".png", []byte("one"), at)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SaveAsset(root, KindImage, "A red fox, in the snow!", ".png", []byte("two"), at)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("the second picture replaced the first")
	}
	if want := filepath.Join(root, "ai-image", "20261030-090807-a-red-fox-in-the-snow.png"); first != want {
		t.Errorf("first = %s, want %s", first, want)
	}
	if got, _ := os.ReadFile(first); string(got) != "one" {
		t.Errorf("first holds %q", got)
	}
	for kind, folder := range map[Kind]string{KindVideo: "ai-video", KindVoice: "ai-voice", KindModel3D: "ai-3dmodel"} {
		p, err := SaveAsset(root, kind, "中文 提示", ".bin", []byte("x"), at)
		if err != nil || filepath.Dir(p) != filepath.Join(root, folder) || !strings.Contains(filepath.Base(p), "中文-提示") {
			t.Errorf("%s: %s, %v", kind, p, err)
		}
	}
	if _, err := SaveAsset(root, KindLLM, "x", ".txt", nil, at); err == nil {
		t.Error("a language model's text was saved as an asset")
	}
	if Slug("   !!! ") != "untitled" || len(Slug(strings.Repeat("word ", 40))) > 40 {
		t.Error("slug")
	}
}

func fakePNG(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestGenerateAsksForAPictureAndReadsItInline(t *testing.T) {
	pic := fakePNG(t)
	var mu sync.Mutex
	var path, auth string
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&sent)
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pic)}}})
	}))
	defer srv.Close()
	got, err := Endpoint{URL: srv.URL + "/v1/", Key: "k", Model: "m"}.Generate(context.Background(), "a fox", ImageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/images/generations" || auth != "Bearer k" || sent["model"] != "m" || sent["prompt"] != "a fox" || sent["response_format"] != "b64_json" {
		t.Fatalf("asked %s %s %v", path, auth, sent)
	}
	if got.MIME != "image/png" || got.Ext() != ".png" || !bytes.Equal(got.Data, pic) {
		t.Fatalf("got %s, %d bytes", got.MIME, len(got.Data))
	}
}

func TestGenerateFetchesAPictureOfferedByAddress(t *testing.T) {
	pic := fakePNG(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/made.png" {
			w.Write(pic)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"url": srv.URL + "/made.png"}}})
	}))
	defer srv.Close()
	got, err := Endpoint{URL: srv.URL, Model: "m"}.Generate(context.Background(), "a fox", ImageOptions{})
	if err != nil || !bytes.Equal(got.Data, pic) {
		t.Fatalf("%v, %d bytes", err, len(got.Data))
	}
}

func TestGenerateRefusesWhatIsNotAPictureOrNotAnswered(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"key":      {401, `{"error":{"message":"bad key"}}`, "API key"},
		"empty":    {200, `{"data":[]}`, "no picture"},
		"text":     {200, `{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte("hello, not a picture")) + `"}]}`, "not a picture"},
		"base64":   {200, `{"data":[{"b64_json":"%%%"}]}`, "decoded"},
		"garbage":  {200, `nope`, "could not be read"},
		"file url": {200, `{"data":[{"url":"file:///etc/passwd"}]}`, "not http"},
	}
	for name, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}))
		_, err := Endpoint{URL: srv.URL, Model: "m"}.Generate(context.Background(), "x", ImageOptions{})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestGenerateStopsWhenCancelled(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := (Endpoint{URL: srv.URL, Model: "m"}).Generate(ctx, "x", ImageOptions{}); err != context.DeadlineExceeded {
		t.Fatalf("err = %v", err)
	}
}

func writeImage(t *testing.T, name string, w, h int, encode func(f *os.File, img image.Image) error) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := encode(f, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadImageSendsSmallPicturesAsTheyAreAndScalesLargeOnesDown(t *testing.T) {
	small := writeImage(t, "s.png", 200, 100, func(f *os.File, img image.Image) error { return png.Encode(f, img) })
	got, err := LoadImage(small)
	want, _ := os.ReadFile(small)
	if err != nil || got.MIME != "image/png" || !bytes.Equal(got.Data, want) {
		t.Fatalf("small: %v %s", err, got.MIME)
	}
	large := writeImage(t, "l.png", 3200, 1600, func(f *os.File, img image.Image) error { return png.Encode(f, img) })
	got, err = LoadImage(large)
	if err != nil || got.MIME != "image/jpeg" {
		t.Fatalf("large: %v %s", err, got.MIME)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got.Data))
	if err != nil || cfg.Width != MaxSendSide || cfg.Height != MaxSendSide/2 {
		t.Fatalf("large is %dx%d, %v", cfg.Width, cfg.Height, err)
	}
	if _, err := LoadImage(filepath.Join(t.TempDir(), "gone.png")); err == nil {
		t.Fatal("a missing file was read")
	}
	text := filepath.Join(t.TempDir(), "t.png")
	os.WriteFile(text, []byte("not a picture"), 0o644)
	if _, err := LoadImage(text); err == nil || !strings.Contains(err.Error(), "not a picture") {
		t.Fatalf("err = %v", err)
	}
}

func TestWireMessagesSkipEmptiesAndSendPictures(t *testing.T) {
	msgs := wireMessages([]Message{
		{Role: User, Text: "hi"},
		{Role: Assistant, Media: []Media{{Kind: KindImage, Path: "x.png"}}},
		{Role: User, Text: "what is this", Images: []Image{{MIME: "image/png", Data: []byte{1}}}},
	})
	if len(msgs) != 2 {
		t.Fatalf("%d messages: %v", len(msgs), msgs)
	}
	parts, ok := msgs[1]["content"].([]map[string]any)
	if !ok || len(parts) != 2 || parts[0]["type"] != "image_url" || parts[1]["text"] != "what is this" {
		t.Fatalf("content %v", msgs[1]["content"])
	}
}

func TestTheBuiltInImageModelsRunOnThisComputer(t *testing.T) {
	c := DefaultConfig()
	for id, parts := range map[string]int{"sd15": 0, "z-image-turbo": 2} {
		ch, ok := c.Choice(id)
		if !ok || ch.Kind != KindImage || ch.Local == nil || ch.Local.API != APISD || len(ch.Local.Parts) != parts {
			t.Errorf("%s: %+v %v", id, ch, ok)
			continue
		}
		if ch.Local.ImageSize == "" || ch.Local.Steps == 0 {
			t.Errorf("%s has no size or steps of its own", id)
		}
		if _, ok := c.Engine(*ch.Local, ""); !ok {
			t.Errorf("no engine runs %s here", id)
		}
	}
}

func TestImageModelMistakesAreNamed(t *testing.T) {
	good := string(DefaultJSON)
	for name, c := range map[string]struct{ from, to, want string }{
		"api kind": {`"id": "sd15",
      "api": "sd-server",
      "kind": "image",`, `"id": "sd15",
      "api": "sd-server",
      "kind": "llm",`, `kind must be one of ["image" "video"]`},
		"no model": {`"arguments": ["-m", "{model}"],
      "imageSize"`, `"arguments": ["--other", "x"],
      "imageSize"`, "arguments must use {model}"},
		"size":        {`"imageSize": "512x512"`, `"imageSize": "big"`, "imageSize must be like"},
		"steps":       {`"steps": 20`, `"steps": 500`, "steps must be"},
		"unused part": {`"--vae", "{vae}", `, ``, "{vae} is not in the model's arguments"},
		"unknown":     {`"--vae", "{vae}"`, `"--vae", "{vea}"`, "unknown placeholder {vea}"},
		"part role":   {`{"role": "llm", "url"`, `{"role": "vae", "url"`, "role"},
		"part url":    {`"role": "vae", "url": "https://huggingface.co/Comfy-Org`, `"role": "vae", "url": "ftp://huggingface.co/Comfy-Org`, "parts: vae"},
		"engine": {`"id": "sd-cpu",
      "api": "sd-server"`, `"id": "sd-cpu",
      "api": "other"`, "api must be"},
		"llm steps": {`"id": "qwen3-4b",`, `"id": "qwen3-4b", "steps": 5,`, "imageSize and steps are for image and video models"},
	} {
		if !strings.Contains(good, c.from) {
			t.Errorf("%s: the test text is not in the built-in file", name)
			continue
		}
		_, err := ParseConfig([]byte(strings.Replace(good, c.from, c.to, 1)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}
