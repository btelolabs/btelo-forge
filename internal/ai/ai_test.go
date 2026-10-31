package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/vault"
)

func TestBuiltInConfigIsValid(t *testing.T) {
	c := DefaultConfig()
	if len(c.Choices()) < 10 {
		t.Fatalf("%d models", len(c.Choices()))
	}
	for _, id := range []string{"qwen3-8b", "gemma3-4b", "deepseek/deepseek-v4-flash", "gemini/gemini-3.8-flash", "grok/grok-4.7", "qwen/qwen3.7-plus", "qwen-intl/qwen3.7-plus"} {
		ch, ok := c.Choice(id)
		if !ok || ch.ID != id || ch.Name.In("en") == "" {
			t.Fatalf("%s: %+v %v", id, ch, ok)
		}
		if ch.Online() != strings.Contains(id, "/") {
			t.Fatalf("%s online = %v", id, ch.Online())
		}
	}
	if ch, _ := c.Choice("gemma3-12b"); !ch.Vision || ch.Local.Projector == nil {
		t.Fatal("gemma reads pictures")
	}
	if _, ok := c.Choice("deepseek/nope"); ok {
		t.Fatal("an unknown online model exists")
	}
	if _, ok := c.Choice("nope/deepseek-chat"); ok {
		t.Fatal("an unknown service exists")
	}
	if m, _ := c.Model("qwen3-4b"); m.ContextOf() != 8192 {
		t.Fatal("context")
	}
	m, _ := c.Model("qwen3-4b")
	if _, ok := c.Engine(m, ""); !ok {
		t.Fatal("no engine runs on this computer")
	}
}

func TestConfigMistakesAreNamed(t *testing.T) {
	good := string(DefaultJSON)
	for name, c := range map[string]struct{ from, to, want string }{
		"version": {`"version": 1`, `"version": 2`, "version must be 1"},
		"default": {`"defaultModel": "qwen3-4b"`, `"defaultModel": "nope"`, "defaultModel"},
		"api": {`"id": "cpu",
      "api": "llama-server"`, `"id": "cpu",
      "api": "other"`, "api must be"},
		"placeholder": {`"-c", "{context}", "-np", "1", "-ngl", "999"`, `"-c", "{ctx}", "-np", "1", "-ngl", "999"`, "unknown placeholder {ctx}"},
		"vision": {`"vision": true,
      "url": "https://huggingface.co/ggml-org/gemma-3-4b`, `"vision": true,
      "x": 1,
      "url": "https://huggingface.co/ggml-org/gemma-3-4b`, "unknown field"},
		"base":  {`"https://api.x.ai/v1"`, `"api.x.ai"`, "baseUrl"},
		"model": {`"model": "grok-build-0.1"`, `"model": ""`, "model (the name"},
	} {
		if !strings.Contains(good, c.from) {
			t.Fatalf("%s: the test text is not in the built-in file", name)
		}
		_, err := ParseConfig([]byte(strings.Replace(good, c.from, c.to, 1)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	if _, err := ParseConfig([]byte(`{"version":1,"defaultModel":"x/y","providers":[]}`)); err == nil {
		t.Fatal("a default that does not exist")
	}
}

func TestVisionNeedsAProjector(t *testing.T) {
	var raw map[string]any
	json.Unmarshal(DefaultJSON, &raw)
	for _, m := range raw["models"].([]any) {
		if m := m.(map[string]any); m["id"] == "gemma3-4b" {
			delete(m, "projector")
		}
	}
	data, _ := json.Marshal(raw)
	if _, err := ParseConfig(data); err == nil || !strings.Contains(err.Error(), "projector") {
		t.Fatalf("err = %v", err)
	}
}

func TestKeysAreSealedAndKeptApartPerService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "keys.json")
	k, err := OpenKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	if k.Has("deepseek") {
		t.Fatal("a key out of nothing")
	}
	if err := k.Set("deepseek", "  sk-secret-1 "); err != nil {
		t.Fatal(err)
	}
	k.Set("grok", "xai-secret-2")
	if got, err := k.Get("deepseek"); err != nil || got != "sk-secret-1" {
		t.Fatalf("got %q %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	if sealingWorks() && strings.Contains(string(raw), "secret") {
		t.Fatalf("a key is written as it is: %s", raw)
	}
	again, err := OpenKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Get("grok"); got != "xai-secret-2" || !again.Has("deepseek") {
		t.Fatal("keys were not kept")
	}
	if err := again.Set("deepseek", ""); err != nil || again.Has("deepseek") || !again.Has("grok") {
		t.Fatalf("forgetting one key: %v", err)
	}
	if got, err := again.Get("deepseek"); got != "" || err != nil {
		t.Fatalf("%q %v", got, err)
	}
	os.WriteFile(path, []byte("{broken"), 0o600)
	if k, err := OpenKeys(path); err == nil || k == nil || k.Has("grok") {
		t.Fatalf("a damaged file: %v", err)
	}
}

// sealingWorks says whether this system seals secrets; where it does not, a key is kept as it is.
func sealingWorks() bool {
	s, _ := vault.Seal("x")
	return s != "x"
}

func TestChatStreamsThinkingAndTextAndSendsTheKey(t *testing.T) {
	var got map[string]any
	var auth string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		time.Sleep(30 * time.Millisecond) // long enough for the clock to tell, so a speed can be worked out
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": keep-alive\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{"role":"assistant","reasoning_content":"Let me "}}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{"reasoning_content":"think."}}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"你好"}}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"，世界"}}],"usage":{"prompt_tokens":5,"completion_tokens":4}}`+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer s.Close()
	ep := Endpoint{URL: s.URL + "/v1/", Key: "sk-test", Model: "m1"}
	temp := 0.2
	var text, thinking strings.Builder
	stats, err := ep.Chat(context.Background(), []Message{
		{Role: System, Text: "be brief"},
		{Role: User, Text: "hi", Images: []Image{{MIME: "image/png", Data: []byte{1, 2, 3}}}},
		{Role: Assistant, Text: "hello", Reasoning: "never sent"},
	}, Options{Temperature: &temp, MaxTokens: 50}, func(c Chunk) { text.WriteString(c.Text); thinking.WriteString(c.Reasoning) })
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "你好，世界" || thinking.String() != "Let me think." {
		t.Fatalf("text %q thinking %q", text.String(), thinking.String())
	}
	if stats.PromptTokens != 5 || stats.CompletionTokens != 4 || stats.TokensPerSecond <= 0 {
		t.Fatalf("stats %+v", stats)
	}
	if auth != "Bearer sk-test" || got["model"] != "m1" || got["stream"] != true || got["temperature"] != 0.2 || got["max_tokens"] != 50.0 {
		t.Fatalf("request %v %q", got, auth)
	}
	if _, has := got["stream_options"]; has {
		t.Fatal("an online service was sent what only a local engine takes")
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 3 || msgs[0].(map[string]any)["content"] != "be brief" {
		t.Fatalf("messages %v", msgs)
	}
	parts := msgs[1].(map[string]any)["content"].([]any)
	img := parts[0].(map[string]any)["image_url"].(map[string]any)["url"]
	if img != "data:image/png;base64,AQID" || parts[1].(map[string]any)["text"] != "hi" {
		t.Fatalf("parts %v", parts)
	}
	if strings.Contains(string(mustJSON(got)), "never sent") {
		t.Fatal("thinking was sent back")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestChatReportsWhatTheServiceRefusedWith(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"key":     {401, `{"error":{"message":"Incorrect API key provided"}}`, "did not accept the API key: Incorrect API key"},
		"quota":   {429, `{"error":{"message":"You exceeded your current quota"}}`, "too many requests, or the quota is used up: You exceeded"},
		"context": {400, `{"error":{"message":"request (9000 tokens) exceeds the available context size (8192 tokens)"}}`, "exceeds the available context size"},
		"string":  {500, `{"error":"model crashed"}`, "model crashed"},
		"list":    {400, `[{"error":{"message":"API key not valid"}}]`, "API key not valid"},
		"plain":   {502, `<html>bad gateway</html>`, "HTTP 502"},
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			io.WriteString(w, c.body)
		}))
		_, err := Endpoint{URL: s.URL}.Chat(context.Background(), []Message{{Role: User, Text: "x"}}, Options{}, func(Chunk) {})
		s.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestChatReportsAnErrorInTheStreamAndACutOffAnswer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"par"}}]}`+"\n\n")
		io.WriteString(w, `data: {"error":{"message":"overloaded"}}`+"\n\n")
	}))
	defer s.Close()
	var text string
	_, err := Endpoint{URL: s.URL}.Chat(context.Background(), []Message{{Role: User, Text: "x"}}, Options{}, func(c Chunk) { text += c.Text })
	if err == nil || err.Error() != "overloaded" || text != "par" {
		t.Fatalf("%q %v", text, err)
	}
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"par"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer cut.Close()
	if _, err := (Endpoint{URL: cut.URL}).Chat(context.Background(), []Message{{Role: User, Text: "x"}}, Options{}, func(Chunk) {}); err == nil || !strings.Contains(err.Error(), "cut off") {
		t.Fatalf("err = %v", err)
	}
}

func TestChatStopsWhenCancelled(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"a"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	done := make(chan error, 1)
	go func() {
		_, err := Endpoint{URL: s.URL}.Chat(ctx, []Message{{Role: User, Text: "x"}}, Options{}, func(Chunk) {})
		done <- err
	}()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the chat did not stop")
	}
}

func TestCompleteJoinsTheAnswer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `data: {"choices":[{"delta":{"content":" Hi "}}]}`+"\n\n"+`data: {"choices":[{"delta":{"content":"there\n"}}]}`+"\n\n")
	}))
	defer s.Close()
	got, err := Endpoint{URL: s.URL}.Complete(context.Background(), []Message{{Role: User, Text: "x"}}, Options{})
	if err != nil || got != "Hi there" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestChatsAreKeptListedAndDeleted(t *testing.T) {
	store := NewChats(filepath.Join(t.TempDir(), "chats"))
	empty := NewChat("qwen3-4b")
	if err := store.Save(empty); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.List(); len(list) != 0 {
		t.Fatal("an empty conversation was kept")
	}
	first := NewChat("qwen3-4b")
	first.Messages = []Message{{Role: User, Text: "第一个问题\n还有第二行"}, {Role: Assistant, Text: "答案", Reasoning: "想了想"}}
	first.Updated = time.Now().Add(-time.Hour)
	second := NewChat("deepseek/deepseek-v4-flash")
	second.Messages = []Message{{Role: User, Text: strings.Repeat("长", 80)}}
	for _, c := range []*Chat{first, second} {
		if err := store.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.List()
	if err != nil || len(list) != 2 || list[0].ID != second.ID || list[1].Title != "第一个问题" {
		t.Fatalf("%+v %v", list, err)
	}
	if n := len([]rune(list[0].Title)); n != 40 || !strings.HasSuffix(list[0].Title, "…") {
		t.Fatalf("title %q", list[0].Title)
	}
	got, err := store.Load(first.ID)
	if err != nil || len(got.Messages) != 2 || got.Messages[1].Reasoning != "想了想" || got.Model != "qwen3-4b" {
		t.Fatalf("%+v %v", got, err)
	}
	os.WriteFile(filepath.Join(store.dir, "0123456789abcdef.json"), []byte("{broken"), 0o600)
	if list, _ := store.List(); len(list) != 2 {
		t.Fatal("a damaged file spoils the list")
	}
	if err := store.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(first.ID); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
	if _, err := store.Load(first.ID); err == nil {
		t.Fatal("a deleted conversation loads")
	}
	for _, bad := range []string{"../x", "", "ZZZZ", first.ID + "/.."} {
		if _, err := store.Load(bad); err == nil {
			t.Fatalf("%q loaded", bad)
		}
		if err := store.Delete(bad); err == nil {
			t.Fatalf("%q deleted", bad)
		}
	}
}
