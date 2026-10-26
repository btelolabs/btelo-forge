package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// withSource adds a source to a music configuration.
func withSource(t *testing.T, config string, source map[string]any) string {
	t.Helper()
	var c map[string]any
	if err := json.Unmarshal([]byte(config), &c); err != nil {
		t.Fatal(err)
	}
	c["sources"] = append(c["sources"].([]any), source)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAnAIAddsAMusicSiteThroughMCP(t *testing.T) {
	x := newHarness(t)
	s := connectMCP(t, x)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/find" || r.URL.Query().Get("q") != "rain" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"items":[{"key":"r1","name":"Rain Loop","by":"Ann","file":"/rain.mp3","secs":"12"},{"key":"r2","name":"","file":"/x.mp3"}]}`))
	}))
	defer site.Close()

	var got struct {
		JSON   string `json:"json"`
		Custom bool   `json:"custom"`
		Guide  string `json:"guide"`
	}
	s.ok("get_config", map[string]any{"name": "music_sources"}, &got)
	if got.Custom || !strings.Contains(got.JSON, `"kevin"`) || !strings.Contains(got.Guide, "Filters") {
		t.Fatalf("get_config %+v", got.Custom)
	}
	res := s.call("set_config", map[string]any{"name": "music_sources", "json": "{\n\"version\": 1,\n\"sources\": [,]}"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "line 3") {
		t.Fatalf("invalid JSON answered %v %q", res.IsError, text)
	}
	if _, err := os.Stat(x.a.configPath(configKinds[0])); err == nil {
		t.Fatal("an invalid configuration was written")
	}
	config := withSource(t, got.JSON, map[string]any{
		"id": "testsite", "name": map[string]string{"en": "Test site", "zh": "测试站"}, "kind": "music",
		"request": map[string]any{"url": site.URL + "/find", "params": map[string]string{"q": "{$query}"}},
		"results": "items",
		"fields": map[string]string{"id": "{key}", "title": "{name}", "artist": "{by}", "url": site.URL + "{file}",
			"duration": "{secs}", "license": "CC0"},
	})
	s.ok("set_config", map[string]any{"name": "music_sources", "json": config}, nil)
	if data, err := os.ReadFile(x.a.configPath(configKinds[0])); err != nil || string(data) != config {
		t.Fatalf("the configuration was not kept: %v", err)
	}
	var sources []map[string]any
	s.ok("list_music_sources", nil, &sources)
	if !slices.ContainsFunc(sources, func(m map[string]any) bool { return m["id"] == "testsite" && m["name"] == "Test site" }) {
		t.Fatalf("list_music_sources %+v", sources)
	}
	if !slices.ContainsFunc(x.a.musicChoices(), func(c musicChoice) bool { return c.id == "testsite" }) {
		t.Fatal("the music panel does not offer the new site")
	}
	var found struct {
		Tracks []trackView `json:"tracks"`
	}
	s.ok("search_music", map[string]any{"query": "rain", "source": "testsite"}, &found)
	if len(found.Tracks) != 1 || found.Tracks[0].Title != "Rain Loop" || found.Tracks[0].Duration != 12 || found.Tracks[0].Key != "testsite:r1" {
		t.Fatalf("search_music %+v", found.Tracks)
	}
	s.ok("reset_config", map[string]any{"name": "music_sources"}, nil)
	if _, err := os.Stat(x.a.configPath(configKinds[0])); !os.IsNotExist(err) {
		t.Fatal("reset_config kept the file")
	}
	if res := s.call("search_music", map[string]any{"query": "rain", "source": "testsite"}); !res.IsError {
		t.Fatal("the removed site is still searched")
	}
}

func TestAnAIAddsASubtitleModelThroughMCP(t *testing.T) {
	x := newHarness(t)
	s := connectMCP(t, x)
	var got struct {
		JSON string `json:"json"`
	}
	s.ok("get_config", map[string]any{"name": "subtitle_models"}, &got)
	var c map[string]any
	json.Unmarshal([]byte(got.JSON), &c)
	c["models"] = append(c["models"].([]any), map[string]any{"id": "large-v3", "name": "Large v3",
		"url": "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3.bin", "size": 3095033483})
	data, _ := json.Marshal(c)
	s.ok("set_config", map[string]any{"name": "subtitle_models", "json": string(data)}, nil)
	var list struct {
		Models []map[string]any `json:"models"`
	}
	s.ok("list_subtitle_models", nil, &list)
	if !slices.ContainsFunc(list.Models, func(m map[string]any) bool { return m["id"] == "large-v3" && m["downloaded"] == false }) {
		t.Fatalf("models %+v", list.Models)
	}
	if res := s.call("generate_subtitles", map[string]any{"model": "huge"}); !res.IsError {
		t.Fatal("an unknown model was accepted")
	}
	bad := strings.Replace(string(data), `"defaultModel":"small"`, `"defaultModel":"nope"`, 1)
	if res := s.call("set_config", map[string]any{"name": "subtitle_models", "json": bad}); !res.IsError {
		t.Fatal("a broken configuration was accepted")
	}
	if _, ok := x.a.subtitles.config.Model("large-v3"); !ok {
		t.Fatal("a rejected configuration replaced the working one")
	}
}

func TestExtensionsPageEditsChecksAndSaves(t *testing.T) {
	x := newHarness(t)
	x.a.settingsOpen, x.a.settingsPage = true, settingsExtensionsPage
	x.frame(Input{})
	const id = "settings/ext/code/music_sources"
	e := &x.a.extensions
	start := e.text["music_sources"]
	if start != string(configKinds[0].defaults) {
		t.Fatal("the editor does not show the configuration in effect")
	}
	x.a.u.focus, x.a.u.edit = id, editor{text: []rune(start), multiline: true}
	x.frame(Input{Keys: []Key{{Code: keyHome, Ctrl: true}}})
	x.frame(Input{Typed: []rune("x")})
	if e.text["music_sources"] != "x"+start {
		t.Fatal("typing did not edit the text")
	}
	x.frame(Input{Keys: []Key{{Code: 'S', Ctrl: true}}})
	if e.err["music_sources"] == "" || !strings.Contains(e.err["music_sources"], "line 1") {
		t.Fatalf("saving broken JSON gave %q", e.err["music_sources"])
	}
	if x.a.lib.Config().Sources[0].ID != "kevin" {
		t.Fatal("broken JSON replaced the configuration")
	}
	x.a.u.focus, x.a.u.edit = id, editor{text: []rune(e.text["music_sources"]), multiline: true}
	x.frame(Input{Keys: []Key{{Code: 'Z', Ctrl: true}}})
	if e.text["music_sources"] != start {
		t.Fatal("undo did not restore the text")
	}
	disabled := strings.Replace(start, `"kind": "music",
      "note": {
        "en": "CC0 / CC BY music from Openverse`, `"kind": "music",
      "disabled": true,
      "note": {
        "en": "CC0 / CC BY music from Openverse`, 1)
	if disabled == start {
		t.Fatal("the test edit did not apply; the built-in file changed shape")
	}
	x.a.u.edit = editor{text: []rune(disabled), multiline: true, caret: len([]rune(disabled)), anchor: len([]rune(disabled))}
	x.frame(Input{Typed: []rune(" ")})
	x.frame(Input{Keys: []Key{{Code: 'S', Ctrl: true}}})
	if e.err["music_sources"] != "" {
		t.Fatal(e.err["music_sources"])
	}
	if s, _ := x.a.lib.Config().Find("openverse"); !s.Disabled {
		t.Fatal("the saved configuration is not in effect")
	}
	if slices.ContainsFunc(x.a.musicChoices(), func(c musicChoice) bool { return c.id == "openverse" }) {
		t.Fatal("a disabled source is still offered")
	}
}

func TestBrokenConfigFileFallsBackToTheBuiltIn(t *testing.T) {
	x := newHarness(t)
	os.MkdirAll(x.a.stateDir, 0o755)
	os.WriteFile(x.a.configPath(configKinds[1]), []byte(`{"version": 1}`), 0o644)
	x.a.loadConfigs()
	if len(x.a.subtitles.config.Models) == 0 {
		t.Fatal("a broken file left no models")
	}
	if text, custom := x.a.configText(configKinds[1]); !custom || text != `{"version": 1}` {
		t.Fatal("the broken file should stay for the user to fix")
	}
}

func TestCodeEditorMovesByLinesAndWords(t *testing.T) {
	x := newHarness(t)
	f := x.a.u.mono(12.5)
	e := editor{text: []rune("ab\nabcd\nx"), multiline: true}
	if got := e.moveLines(4, -1, f); got != 1 {
		t.Fatalf("up from line 2 column 1 = %d", got)
	}
	if got := e.moveLines(7, 1, f); got != 9 {
		t.Fatalf("down to a shorter line = %d", got)
	}
	if got := e.moveLines(1, 5, f); got != 9 {
		t.Fatalf("down past the end = %d", got)
	}
	if a, b := wordAround([]rune(`"page_size": 20`), 4); a != 1 || b != 10 {
		t.Fatalf("word %d %d", a, b)
	}
}
