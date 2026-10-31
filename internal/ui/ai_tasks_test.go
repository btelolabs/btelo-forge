package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
)

// useTaskServices points the video and 3D services at a server that makes a file at once.
func useTaskServices(t *testing.T, x *harness) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/video_generation") && r.Method == http.MethodPost:
			fmt.Fprint(w, `{"task_id":"7","base_resp":{"status_code":0}}`)
		case strings.Contains(r.URL.Path, "/query/"):
			fmt.Fprint(w, `{"status":"Success","file_id":"9","base_resp":{"status_code":0}}`)
		case strings.HasSuffix(r.URL.Path, "/files/retrieve"):
			fmt.Fprintf(w, `{"file":{"download_url":"http://%s/out.mp4"},"base_resp":{"status_code":0}}`, r.Host)
		case strings.HasSuffix(r.URL.Path, "/image-to-3d"):
			fmt.Fprint(w, `{"result":"t"}`)
		case strings.Contains(r.URL.Path, "/image-to-3d/"):
			fmt.Fprintf(w, `{"status":"SUCCEEDED","model_urls":{"glb":"http://%s/out.glb"}}`, r.Host)
		default:
			w.Write([]byte("file bytes"))
		}
	}))
	t.Cleanup(srv.Close)
	cfg := ai.DefaultConfig()
	for i := range cfg.Providers {
		if id := cfg.Providers[i].ID; id == "minimax" || id == "meshy" {
			cfg.Providers[i].BaseURL = srv.URL
			if err := x.a.ai.keys.Set(id, "sk-test-key"); err != nil {
				t.Fatal(err)
			}
		}
	}
	x.a.setAIConfig(cfg)
	return srv
}

func TestAVideoIsMadeKeptAndCanBeOpened(t *testing.T) {
	x := newHarness(t)
	useTaskServices(t, x)
	ch, ok := x.a.ai.config.Choice("minimax/minimax-h3")
	if !ok {
		t.Fatal("no MiniMax H3")
	}
	x.a.aiPick(ch)
	x.a.openAITab()
	x.frame(Input{})
	x.shot("ai-video-empty")
	x.typeText("a fox in the snow")
	x.key(keyEnter, false)
	x.pump(20*time.Second, x.answered)
	x.settle(time.Second)
	x.shot("ai-video-made")
	msgs := x.a.ai.chat.Messages
	if x.a.ai.notice != "" || len(msgs) != 2 || len(msgs[1].Media) != 1 {
		t.Fatalf("notice %q, %+v", x.a.ai.notice, msgs)
	}
	m := msgs[1].Media[0]
	if m.Kind != ai.KindVideo || filepath.Ext(m.Path) != ".mp4" || filepath.Base(filepath.Dir(m.Path)) != "ai-video" {
		t.Fatalf("%+v", m)
	}
	if data, err := os.ReadFile(m.Path); err != nil || string(data) != "file bytes" {
		t.Fatalf("%q %v", data, err)
	}
}

func TestA3DModelNeedsAPictureAndCanBeOpened(t *testing.T) {
	x := newHarness(t)
	useTaskServices(t, x)
	ch, ok := x.a.ai.config.Choice("meshy/meshy-7.1")
	if !ok {
		t.Fatal("no Meshy model")
	}
	x.a.aiPick(ch)
	x.a.openAITab()
	x.frame(Input{})
	x.shot("ai-3d-empty")

	// Words alone are not enough.
	x.typeText("a chair")
	x.key(keyEnter, false)
	x.frame(Input{})
	if x.a.ai.notice == "" || x.a.ai.answer != nil {
		t.Fatalf("notice %q", x.a.ai.notice)
	}
	x.a.ai.notice = ""
	pic := filepath.Join(t.TempDir(), "chair.png")
	if err := os.WriteFile(pic, gradientPNG(t, 64, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	x.a.aiAttach([]string{pic})
	x.frame(Input{})
	x.key(keyEnter, false)
	x.pump(20*time.Second, x.answered)
	x.settle(time.Second)
	x.shot("ai-3d-made")
	msgs := x.a.ai.chat.Messages
	if x.a.ai.notice != "" || len(msgs[len(msgs)-1].Media) != 1 {
		t.Fatalf("notice %q, %+v", x.a.ai.notice, msgs)
	}
	m := msgs[len(msgs)-1].Media[0]
	if m.Kind != ai.KindModel3D || filepath.Ext(m.Path) != ".glb" || filepath.Base(filepath.Dir(m.Path)) != "ai-3dmodel" {
		t.Fatalf("%+v", m)
	}
}

func TestAPictureGoesToAnImageModelOnlyWhenItRedraws(t *testing.T) {
	x := newHarness(t)
	pic := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(pic, gradientPNG(t, 8, 8), 0o644); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"sd15": true, "grok/grok-imagine-image-2.0": true, "gemini/gemini-3-pro-image": false, "minimax/minimax-h3": true} {
		ch, ok := x.a.ai.config.Choice(id)
		if !ok || aiAccepts(ch, pic) != want {
			t.Errorf("%s: %v %v", id, ok, aiAccepts(ch, pic))
		}
	}
}

func TestTheVoiceIsChosenInTheChat(t *testing.T) {
	x := newHarness(t)
	ch, ok := x.a.aiFind(ai.ExtID(ai.ExtSpeech, x.a.speech.config.Models[0].ID))
	if !ok {
		t.Fatal("no voice model")
	}
	x.a.aiPick(ch)
	x.a.openAITab()
	x.frame(Input{})
	x.frame(Input{})
	x.shot("ai-voices")
	if len(x.a.speech.config.Voices) < 2 || x.a.ai.voiceH <= 0 {
		t.Fatalf("%d voices, %v tall", len(x.a.speech.config.Voices), x.a.ai.voiceH)
	}
	x.c = gfx.NewCanvas(520, 700)
	x.frame(Input{})
	x.frame(Input{})
	x.shot("ai-voices-narrow")
	x.a.u.lang = "zh"
	x.frame(Input{})
	x.shot("ai-voices-narrow-zh")
}

func TestAnAnswerSaysWhatItWasWrittenWithout(t *testing.T) {
	x := newHarness(t)
	x.a.openAITab()
	x.a.ai.chat = ai.NewChat(x.a.ai.config.DefaultModel)
	x.a.ai.chat.Messages = []ai.Message{{Role: ai.User, Text: "question"}, {Role: ai.Assistant, Text: "answer", LeftOut: 12}}
	x.frame(Input{})
	x.shot("ai-left-out")
	x.a.u.lang = "zh"
	x.frame(Input{})
	x.shot("ai-left-out-zh")
}

func TestATooLongMessageIsReportedNotSent(t *testing.T) {
	x := newHarness(t)
	x.a.openAITab()
	ch := x.a.aiChoice()
	x.a.ai.chat = ai.NewChat(ch.ID)
	x.a.ai.chat.Messages = []ai.Message{{Role: ai.User, Text: strings.Repeat("字", 40000)}}
	x.a.aiAsk(ch)
	x.pump(10*time.Second, x.answered)
	x.frame(Input{})
	x.shot("ai-too-long")
	if !strings.Contains(x.a.ai.notice, "longer than the model") {
		t.Fatalf("notice %q", x.a.ai.notice)
	}
	if x.a.ai.rt.Status(ch.ID).State != ai.Unloaded {
		t.Fatal("the model was loaded for a message it cannot take")
	}
}

// TestRealLocalVideoInTheChat makes a video with a real sd-server and Wan 2.2 (FORGE_SD_SERVER, and FORGE_WAN_MODEL, its
// folder with wan.gguf, vae.safetensors and umt5.gguf), through the chat as the user does.
func TestRealLocalVideoInTheChat(t *testing.T) {
	server, dir := os.Getenv("FORGE_SD_SERVER"), os.Getenv("FORGE_WAN_MODEL")
	if server == "" || dir == "" {
		t.Skip("set FORGE_SD_SERVER and FORGE_WAN_MODEL to run against a real video model")
	}
	realVideoInTheChat(t, server, "wan2.2-ti2v-5b", filepath.Join(dir, "wan.gguf"), map[string]string{
		"vae": filepath.Join(dir, "vae.safetensors"), "t5xxl": filepath.Join(dir, "umt5.gguf"),
	}, 9, 20, "a red fox running in the snow, photo")
}

// TestRealMiniMaxH3InTheChat does the same with MiniMax H3 at the size and length of its entry, sound included
// (FORGE_SD_SERVER, and FORGE_H3_MODEL, its folder with the four files under the names they are downloaded by).
func TestRealMiniMaxH3InTheChat(t *testing.T) {
	server, dir := os.Getenv("FORGE_SD_SERVER"), os.Getenv("FORGE_H3_MODEL")
	if server == "" || dir == "" {
		t.Skip("set FORGE_SD_SERVER and FORGE_H3_MODEL to run against a real video model")
	}
	m, _ := ai.DefaultConfig().Model("minimax-h3-q4")
	parts := map[string]string{}
	for _, p := range m.Parts {
		parts[p.Role] = filepath.Join(dir, p.URL[strings.LastIndex(p.URL, "/")+1:])
	}
	made := realVideoInTheChat(t, server, m.ID, filepath.Join(dir, m.URL[strings.LastIndex(m.URL, "/")+1:]), parts, m.Frames, m.Steps,
		"a red fox trotting through falling snow, cinematic. Soft wind and gentle piano music.")
	info, err := media.Probe(context.Background(), made)
	if err != nil || !info.HasAudio || info.Width != 864 || info.Height != 480 || info.Duration < 5 {
		t.Fatalf("%+v %v", info, err)
	}
	if codec, err := media.AudioCodec(context.Background(), made); err != nil || codec != "opus" {
		t.Fatalf("sound is %q: %v", codec, err)
	}
}

// realVideoInTheChat sends a message to a video model whose files are already on this computer and returns what it made.
func realVideoInTheChat(t *testing.T, server, id, model string, parts map[string]string, frames, steps int, prompt string) string {
	x := newHarness(t)
	cfg := ai.DefaultConfig()
	for i := range cfg.Engines {
		if cfg.Engines[i].ID == "sd-vulkan" {
			cfg.Engines[i].Paths, cfg.Engines[i].URL, cfg.Engines[i].Executable = []string{server}, "", ""
		}
	}
	for i := range cfg.Models {
		if m := &cfg.Models[i]; m.ID == id {
			m.Frames, m.Steps, m.Parts = frames, steps, nil
			for j, arg := range m.Arguments {
				if path, ok := parts[strings.Trim(arg, "{}")]; ok {
					m.Arguments[j] = path
				}
			}
		}
	}
	x.a.setAIConfig(cfg)
	x.a.settings.AIEngine = "vulkan"
	m, _ := cfg.Model(id)
	path, _ := x.a.ai.rt.Manager().ModelPath(m.Model)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(model, path); err != nil {
		t.Skipf("the model could not be linked into the cache: %v", err)
	}
	ch, _ := cfg.Choice(m.ID)
	x.a.aiPick(ch)
	x.a.openAITab()
	x.frame(Input{})
	x.typeText(prompt)
	x.key(keyEnter, false)
	x.pump(20*time.Minute, x.answered)
	x.settle(time.Second)
	x.shot("ai-real-video")
	msgs := x.a.ai.chat.Messages
	if x.a.ai.notice != "" || len(msgs) != 2 || len(msgs[1].Media) != 1 {
		t.Fatalf("notice %q, %+v", x.a.ai.notice, msgs)
	}
	made := msgs[1].Media[0].Path
	info, err := os.Stat(made)
	if err != nil || info.Size() < 1000 || filepath.Ext(made) != ".webm" {
		t.Fatalf("%v %v", info, err)
	}
	t.Log(made)
	x.a.ai.rt.UnloadAll()
	return made
}
