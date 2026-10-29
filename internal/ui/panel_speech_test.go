package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/speech"
	"github.com/btelolabs/btelo-forge/internal/speech/speechtest"
)

// fakeSpeech points the voice-over at this test binary acting as audiocpp_server and breeze-server, with the models
// served by a test server: q8_0 is Qwen3-TTS on audio.cpp, the default, and q4_k is Breeze. It returns the file
// where the fake notes what it was asked.
func fakeSpeech(t *testing.T, x *harness) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("GGUF model")) }))
	t.Cleanup(models.Close)
	var c map[string]any
	if err := json.Unmarshal(speech.DefaultJSON, &c); err != nil {
		t.Fatal(err)
	}
	exeJSON, _ := json.Marshal(exe)
	c["engines"] = []any{
		json.RawMessage(`{"id": "fake-audiocpp", "api": "audio.cpp", "name": "Fake", "paths": [` + string(exeJSON) + `], "backend": "vulkan", "arguments": ["--config", "{config}"]}`),
		json.RawMessage(`{"id": "fake-breeze", "api": "breeze-server", "name": "Fake", "paths": [` + string(exeJSON) + `], "arguments": ["{model}", "--port", "{port}"]}`),
	}
	c["defaultModel"] = "q8_0"
	c["models"] = []any{
		map[string]any{"id": "q8_0", "api": "audio.cpp", "family": "qwen3_tts", "task": "vdes", "commercial": true, "name": "Q8_0", "license": "Free.", "url": models.URL + "/m.gguf", "size": 10},
		map[string]any{"id": "q4_k", "api": "breeze-server", "name": "Q4_K", "license": "Research only.", "url": models.URL + "/small.gguf", "size": 10},
	}
	data, _ := json.Marshal(c)
	config, err := speech.ParseConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	x.a.speech.config = config
	log := filepath.Join(x.dir, "speech-log.txt")
	speechtest.Use(t, log)
	return log
}

func speechCalls(log string) []string {
	data, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// speechClips are the music clips made of generated speech.
func speechClips(p *project.Project, dir string) []project.Music {
	var out []project.Music
	for _, t := range p.Music {
		for _, m := range t.Clips {
			if filepath.Dir(m.Path) == dir {
				out = append(out, m)
			}
		}
	}
	return out
}

func (x *harness) waitSpeech() {
	x.t.Helper()
	x.pump(time.Minute, func() bool { return !x.a.speech.busy() })
}

func TestVoiceOverIsPlacedAtThePlayheadAndMadeOnce(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	log := fakeSpeech(t, x)
	x.a.tab = tabSpeech
	x.a.seek(1)
	x.a.speech.text = "  你好，世界  "
	x.frame(Input{})
	if err := x.a.generateSpeech([]speechLine{{text: x.a.speech.text, at: x.a.playhead}}, false); err != nil {
		t.Fatal(err)
	}
	x.frame(Input{})
	if !x.a.speech.busy() {
		t.Fatal("no job is shown while the speech is made")
	}
	x.waitSpeech()
	if x.a.speech.lastErr != "" {
		t.Fatal(x.a.speech.lastErr)
	}
	clips := speechClips(x.a.p, x.a.speech.dir)
	if len(clips) != 1 {
		t.Fatalf("clips %+v", x.a.p.Music)
	}
	m := clips[0]
	if m.Start != 1 || m.Length != 1.5 || m.Total != 1.5 || m.Fade || m.Title != "你好，世界" || m.Volume != 1 {
		t.Fatalf("clip %+v", m)
	}
	if x.a.sel.kind != selMusic || x.a.sel.id != m.ID {
		t.Fatal("the new speech is not selected")
	}
	voice, _ := x.a.speech.config.Voice(x.a.speech.config.DefaultVoice)
	want := []string{"start m.gguf qwen3_tts/vdes", "speak Hello.||42|en", "speak 你好，世界|" + voice.Instruction + "|42|zh"}
	if got := speechCalls(log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("engine was asked %q, want %q", got, want)
	}
	x.a.undo()
	if len(speechClips(x.a.p, x.a.speech.dir)) != 0 {
		t.Fatal("undo left the speech")
	}
	x.a.redo()

	// The same words in the same voice come from the file made before; at the same place they are not doubled.
	x.a.generateSpeech([]speechLine{{text: "你好，世界", at: 1}}, false)
	x.waitSpeech()
	x.a.generateSpeech([]speechLine{{text: "你好，世界", at: 3}}, false)
	x.waitSpeech()
	if n := len(speechClips(x.a.p, x.a.speech.dir)); n != 2 {
		t.Fatalf("%d clips after asking again at the same and another place", n)
	}
	if n := len(speechCalls(log)); n != 3 {
		t.Fatalf("the engine made the same speech again: %q", speechCalls(log))
	}

	// Another model is a new engine with the model loaded; a described voice is spoken as described.
	x.a.settings.SpeechModel, x.a.settings.SpeechVoice, x.a.settings.SpeechDescription = "q4_k", speechDescribed, "A robot."
	x.a.generateSpeech([]speechLine{{text: "hello", at: 0}}, false)
	x.waitSpeech()
	if got := speechCalls(log); len(got) != 5 || got[3] != "start small.gguf" || got[4] != "speak hello|A robot.|42|" {
		t.Fatalf("engine was asked %q", got)
	}
	x.a.speech.server.Stop()
}

func TestVoiceOverReadsTheSubtitlesThroughMCP(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	log := fakeSpeech(t, x)
	s := connectMCP(t, x)
	if res := s.call("generate_speech", map[string]any{"subtitles": true}); !res.IsError {
		t.Fatal("reading subtitles of an edit without any was accepted")
	}
	x.a.change(func() {
		x.a.p.AddCaptions([]project.Overlay{{Body: "First line.", Start: 0.5, End: 2}, {Body: " ", Start: 2, End: 3}, {Body: "Second line.", Start: 3, End: 4}})
	})
	for _, bad := range []map[string]any{
		{"text": "hi", "subtitles": true},
		{},
		{"text": "hi", "voice": "nobody"},
		{"text": "hi", "voice": "warm", "description": "A robot."},
		{"text": "hi", "model": "huge"},
	} {
		if res := s.call("generate_speech", bad); !res.IsError {
			t.Fatalf("%v was accepted", bad)
		}
	}
	var list struct {
		Voice  string           `json:"voice"`
		Voices []map[string]any `json:"voices"`
		Models []map[string]any `json:"models"`
	}
	s.ok("list_speech_voices", nil, &list)
	if list.Voice != "warm" || len(list.Voices) != len(x.a.speech.config.Voices) || len(list.Models) != 2 || list.Models[0]["downloaded"] != false ||
		list.Models[0]["commercial"] != true || list.Models[1]["commercial"] != false {
		t.Fatalf("list %+v", list)
	}
	var started struct {
		Lines int `json:"lines"`
	}
	s.ok("generate_speech", map[string]any{"subtitles": true, "voice": "steady"}, &started)
	if started.Lines != 2 {
		t.Fatalf("started %+v", started)
	}
	x.waitSpeech()
	var status map[string]any
	s.ok("speech_status", nil, &status)
	if status["running"] != false || status["added"] != float64(2) || status["error"] != "" || status["engine_loaded"] != true {
		t.Fatalf("status %v", status)
	}
	clips := speechClips(x.a.p, x.a.speech.dir)
	if len(clips) != 2 || clips[0].Start != 0.5 || clips[1].Start != 3 || clips[0].Title != "First line." {
		t.Fatalf("clips %+v", clips)
	}
	steady, _ := x.a.speech.config.Voice("steady")
	if got := speechCalls(log); len(got) != 4 || got[2] != "speak First line.|"+steady.Instruction+"|42|en" || got[3] != "speak Second line.|"+steady.Instruction+"|42|en" {
		t.Fatalf("engine was asked %q", got)
	}
	x.a.undo()
	if len(speechClips(x.a.p, x.a.speech.dir)) != 0 {
		t.Fatal("the subtitles' speech is not one change")
	}
	var got struct {
		JSON string `json:"json"`
	}
	s.ok("get_config", map[string]any{"name": "speech_voices"}, &got)
	if !strings.Contains(got.JSON, `"voices"`) {
		t.Fatalf("speech_voices config %q", got.JSON)
	}
}

func TestVoiceOverFailureIsShownAndKeepsWhatWasMade(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	fakeSpeech(t, x)
	x.a.generateSpeech([]speechLine{{text: "fine", at: 0}, {text: "crash", at: 2}}, false)
	x.waitSpeech()
	if x.a.speech.lastErr == "" || !strings.Contains(x.a.speech.lastErr, "stopped") {
		t.Fatalf("error %q", x.a.speech.lastErr)
	}
	if clips := speechClips(x.a.p, x.a.speech.dir); len(clips) != 1 || clips[0].Title != "fine" {
		t.Fatalf("clips %+v", clips)
	}
	if x.a.speech.server.Running() {
		t.Fatal("a crashed engine is still counted as running")
	}
}
