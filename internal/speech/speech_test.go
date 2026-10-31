package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/speech/speechtest"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

func TestMain(m *testing.M) {
	if speechtest.Enabled() {
		speechtest.Serve(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

type fake struct {
	t      *testing.T
	dir    string
	log    string
	server *Server
}

func newFake(t *testing.T, idle time.Duration) *fake {
	dir := t.TempDir()
	f := &fake{t: t, dir: dir, log: filepath.Join(dir, "log.txt"), server: NewServer(idle)}
	speechtest.Use(t, f.log)
	t.Cleanup(f.server.Close)
	return f
}

// launch starts the fake as the built-in engine of api, loading the model file named model.
func (f *fake) launch(api, model string) Launch {
	exe, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	c := DefaultConfig()
	l := Launch{Program: exe, ModelPath: filepath.Join(f.dir, model), Dir: filepath.Join(f.dir, "engine", api)}
	for _, e := range c.Engines {
		if e.API == api && l.Engine.API == "" {
			l.Engine = e
		}
	}
	for _, m := range c.Models {
		if m.API == api && l.Model.API == "" {
			l.Model = m
		}
	}
	return l
}

func (f *fake) lines() []string {
	data, _ := os.ReadFile(f.log)
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(data)), " ", "_"))
}

func noProgress(string, float64) {}

// stages records the stages progress goes through, checking every share is between 0 and 1.
func stages(t *testing.T) (*[]string, transcribe.Progress) {
	var seen []string
	return &seen, func(stage string, fr float64) {
		if fr < 0 || fr > 1 {
			t.Errorf("progress %v", fr)
		}
		if len(seen) == 0 || seen[len(seen)-1] != stage {
			seen = append(seen, stage)
		}
	}
}

func TestSpeakWritesAWAVFileAndKeepsTheModelLoaded(t *testing.T) {
	f := newFake(t, time.Minute)
	seen, progress := stages(t)
	out := filepath.Join(f.dir, "out", "a.wav")
	voice := Voice{Instruction: "A calm voice.", Seed: 7, Guidance: 2.5}
	seconds, err := f.server.Speak(context.Background(), f.launch(APIBreeze, "m.gguf"), Request{Text: "  你好 world  ", Voice: voice}, out, progress)
	if err != nil {
		t.Fatal(err)
	}
	if seconds != 1.5 {
		t.Errorf("seconds = %v", seconds)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 44+48000 || string(data[0:4]) != "RIFF" || string(data[8:16]) != "WAVEfmt " || string(data[36:40]) != "data" ||
		binary.LittleEndian.Uint32(data[24:]) != 16000 || binary.LittleEndian.Uint32(data[40:]) != 48000 ||
		binary.LittleEndian.Uint32(data[4:]) != 36+48000 || binary.LittleEndian.Uint16(data[22:]) != 1 || data[44+1] != 1 {
		t.Fatalf("header %v, %d bytes", data[:44], len(data))
	}
	if l, ok := Length(out); !ok || l != 1.5 {
		t.Errorf("Length = %v, %v", l, ok)
	}
	if _, ok := Length(f.log); ok {
		t.Error("a text file has a length")
	}
	if strings.Join(*seen, ",") != "load,speak" {
		t.Errorf("stages %v", *seen)
	}
	if _, err := f.server.Speak(context.Background(), f.launch(APIBreeze, "m.gguf"), Request{Text: "again", Voice: Voice{Instruction: "x"}}, filepath.Join(f.dir, "b.wav"), noProgress); err != nil {
		t.Fatal(err)
	}
	want := []string{"start_m.gguf", "speak_你好_world|A_calm_voice.|7|2.5", "speak_again|x|42|"}
	if got := f.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("server saw %q, want %q", got, want)
	}
	entries, _ := os.ReadDir(filepath.Join(f.dir, "out"))
	if len(entries) != 1 {
		t.Errorf("left %d files where the speech was written", len(entries))
	}
}

func TestAudioCPPIsConfiguredWarmedAndToldTheLanguage(t *testing.T) {
	f := newFake(t, time.Minute)
	seen, progress := stages(t)
	l := f.launch(APIAudioCPP, "voice.gguf")
	out := filepath.Join(f.dir, "a.wav")
	seconds, err := f.server.Speak(context.Background(), l, Request{Text: "大家好，world", Voice: Voice{Instruction: "A calm voice.", Seed: 7}}, out, progress)
	if err != nil {
		t.Fatal(err)
	}
	if length, ok := Length(out); !ok || length != 1.5 || seconds != 1.5 {
		t.Fatalf("length %v %v, seconds %v", length, ok, seconds)
	}
	if data, _ := os.ReadFile(out); len(data) != 44+48000 || data[44+1] != 1 {
		t.Fatalf("the samples do not follow the header: %d bytes", len(data))
	}
	if _, err := f.server.Speak(context.Background(), l, Request{Text: "Thanks for watching.", Voice: Voice{Instruction: "x"}}, filepath.Join(f.dir, "b.wav"), noProgress); err != nil {
		t.Fatal(err)
	}
	want := []string{"start_voice.gguf_qwen3_tts/vdes", "speak_Hello.||42|en", "speak_大家好，world|A_calm_voice.|7|zh", "speak_Thanks_for_watching.|x|42|en"}
	if got := f.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("server saw %q, want %q", got, want)
	}
	if strings.Join(*seen, ",") != "load,speak" {
		t.Errorf("stages %v", *seen)
	}
	config, err := os.ReadFile(filepath.Join(l.Dir, "server.json"))
	if err != nil || !strings.Contains(string(config), `"backend": "vulkan"`) || !strings.Contains(string(config), `"lazy_load": false`) ||
		!strings.Contains(string(config), filepath.ToSlash(l.ModelPath)) {
		t.Fatalf("config %s, %v", config, err)
	}
}

func TestAnotherModelRestartsTheServerAndIdleStopsIt(t *testing.T) {
	f := newFake(t, 300*time.Millisecond)
	speak := func(api, model, out string) {
		t.Helper()
		if _, err := f.server.Speak(context.Background(), f.launch(api, model), Request{Text: "hi", Voice: Voice{Instruction: "x"}}, filepath.Join(f.dir, out), noProgress); err != nil {
			t.Fatal(err)
		}
	}
	speak(APIBreeze, "a.gguf", "a.wav")
	speak(APIBreeze, "b.gguf", "b.wav")
	if got := f.lines(); len(got) != 4 || got[0] != "start_a.gguf" || got[2] != "start_b.gguf" {
		t.Fatalf("server saw %q", got)
	}
	if !f.server.Running() {
		t.Fatal("not running after speaking")
	}
	f.server.Stop()
	if f.server.Running() {
		t.Fatal("running after Stop")
	}
	speak(APIBreeze, "b.gguf", "c.wav")
	deadline := time.Now().Add(5 * time.Second)
	for f.server.Running() {
		if time.Now().After(deadline) {
			t.Fatal("the idle server was not stopped")
		}
		time.Sleep(20 * time.Millisecond)
	}
	speak(APIBreeze, "b.gguf", "d.wav")
	if got := f.lines(); len(got) != 8 || got[4] != "start_b.gguf" || got[6] != "start_b.gguf" {
		t.Fatalf("server saw %q", got)
	}
	// Another kind of engine replaces the running one.
	speak(APIAudioCPP, "b.gguf", "e.wav")
	if got := f.lines(); len(got) != 11 || got[8] != "start_b.gguf_qwen3_tts/vdes" {
		t.Fatalf("server saw %q", got)
	}
}

func TestSpeakReportsFailures(t *testing.T) {
	for _, api := range []string{APIBreeze, APIAudioCPP} {
		t.Run(api, func(t *testing.T) {
			f := newFake(t, time.Minute)
			ctx := context.Background()
			voice := Voice{Instruction: "x"}
			_, err := f.server.Speak(ctx, f.launch(api, "broken.gguf"), Request{Text: "hi", Voice: voice}, filepath.Join(f.dir, "a.wav"), noProgress)
			if err == nil || !strings.Contains(err.Error(), "bad magic") {
				t.Fatalf("loading a broken model: %v", err)
			}
			if _, err := f.server.Speak(ctx, f.launch(api, "m.gguf"), Request{Text: " \n ", Voice: voice}, filepath.Join(f.dir, "a.wav"), noProgress); err == nil {
				t.Fatal("empty text was spoken")
			}
			out := filepath.Join(f.dir, "cut.wav")
			if _, err := f.server.Speak(ctx, f.launch(api, "m.gguf"), Request{Text: "cut", Voice: voice}, out, noProgress); err == nil || !strings.Contains(err.Error(), "before the speech was finished") {
				t.Fatalf("a cut stream: %v", err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("a cut stream left a file")
			}
			if _, err := f.server.Speak(ctx, f.launch(api, "m.gguf"), Request{Text: "crash", Voice: voice}, out, noProgress); err == nil {
				t.Fatal("a crashed server made speech")
			}
			if f.server.Running() {
				t.Fatal("a crashed server is still counted as running")
			}
			if _, err := f.server.Speak(ctx, f.launch(api, "m.gguf"), Request{Text: "fine", Voice: voice}, out, noProgress); err != nil {
				t.Fatalf("after a crash: %v", err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := f.server.Speak(cancelled, f.launch(api, "m.gguf"), Request{Text: "hi", Voice: voice}, out, noProgress); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled: %v", err)
			}
			f.server.Close()
			if _, err := f.server.Speak(ctx, f.launch(api, "m.gguf"), Request{Text: "hi", Voice: voice}, out, noProgress); err == nil {
				t.Fatal("spoke after Close")
			}
		})
	}
}

func TestReadWAVHeaderTakesOnly16BitMono(t *testing.T) {
	wav := func(format, channels, bits uint16, extra string) []byte {
		b := []byte("RIFF\x00\x00\x00\x00WAVE" + extra + "fmt \x10\x00\x00\x00")
		b = binary.LittleEndian.AppendUint16(b, format)
		b = binary.LittleEndian.AppendUint16(b, channels)
		b = binary.LittleEndian.AppendUint32(b, 24000)
		b = binary.LittleEndian.AppendUint32(b, 48000)
		b = binary.LittleEndian.AppendUint16(b, 2)
		b = binary.LittleEndian.AppendUint16(b, bits)
		return append(b, "data\x00\x00\x00\x00"...)
	}
	if rate, err := readWAVHeader(bytes.NewReader(wav(1, 1, 16, "JUNK\x01\x00\x00\x00x\x00"))); err != nil || rate != 24000 {
		t.Fatalf("rate %d, %v", rate, err)
	}
	for _, bad := range [][]byte{wav(3, 1, 32, ""), wav(1, 2, 16, ""), []byte("RIFF\x00\x00\x00\x00WAVEdata\x00\x00\x00\x00"), []byte(`{"error":1}`)} {
		if _, err := readWAVHeader(bytes.NewReader(bad)); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}

func TestDefaultConfigIsValid(t *testing.T) {
	c := DefaultConfig()
	for _, m := range c.Models {
		if len(m.SHA256) != 64 || m.License.In("zh") == m.License.In("en") || m.Commercial != (m.API == APIAudioCPP) {
			t.Errorf("model %+v", m)
		}
	}
	if m, _ := c.Model(c.DefaultModel); !m.Commercial {
		t.Error("the default model may not be used commercially")
	}
	if _, ok := c.Voice(c.DefaultVoice); !ok {
		t.Fatal("default voice missing")
	}
	for _, v := range c.Voices {
		if v.Name.In("zh") == v.Name.In("en") {
			t.Errorf("voice %s has no Chinese name", v.ID)
		}
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		for _, m := range c.Models {
			if _, ok := c.Engine(m, ""); !ok {
				t.Errorf("no engine runs %s on this computer", m.ID)
			}
		}
	}
	for _, e := range c.Engines {
		if e.URL != "" && len(e.SHA256) != 64 {
			t.Errorf("engine %s is downloaded without a checksum", e.ID)
		}
	}
}

func TestConfigErrorsArePrecise(t *testing.T) {
	base := string(DefaultJSON)
	for data, want := range map[string]string{
		strings.Replace(base, `"defaultModel": "qwen3-design-q8_0"`, `"defaultModel": "q9"`, 1):                                    "defaultModel",
		strings.Replace(base, `"defaultVoice": "warm"`, `"defaultVoice": "cold"`, 1):                                               "defaultVoice",
		strings.Replace(base, `"--port", "{port}"`, `"--port", "{p}"`, 1):                                                          "unknown placeholder {p}",
		strings.Replace(base, `"--port", "{port}"`, `"--port", "8080"`, 1):                                                         "must use {port}",
		strings.Replace(base, `["--config", "{config}", "--no-ui"]`, `["--no-ui"]`, 1):                                             "must use {config}",
		strings.Replace(base, `"backend": "vulkan",`, ``, 1):                                                                       "backend",
		strings.Replace(base, `"api": "audio.cpp",`, `"api": "sapi",`, 1):                                                          "api must be",
		strings.Replace(base, `"family": "qwen3_tts",`, ``, 1):                                                                     "family and task",
		strings.Replace(base, `"id": "steady"`, `"id": "warm"`, 1):                                                                 "voices[1] (warm)",
		strings.Replace(base, `"id": "breeze-q6_k"`, `"id": "breeze-q4_k"`, 1):                                                     "models[4] (breeze-q4_k)",
		strings.Replace(base, `"executable": "breeze-server.exe"`, `"executable": "../b.exe"`, 1):                                  "file name",
		strings.Replace(base, `"sha256": "d1741e16154f99ef96382a36d5e89ccf12fd4fda6ec7dcdcdbd122d82f525c11"`, `"sha256": "x"`, 1):  "sha256",
		strings.Replace(base, `"instruction": "A warm, gentle young woman with a clear, calm delivery."`, `"instruction": " "`, 1): "instruction",
		strings.Replace(base, `"id": "warm",`, `"id": "warm", "guidance": 9,`, 1):                                                  "guidance",
		strings.Replace(base, `"version": 1`, `"version": 1, "colour": 2`, 1):                                                      "unknown field",
	} {
		if _, err := ParseConfig([]byte(data)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want an error about %q, got %v", want, err)
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	c := DefaultConfig()
	qwen, _ := c.Model("qwen3-design-q8_0")
	breeze, _ := c.Model("breeze-q4_k")
	if e, ok := c.Engine(qwen, "breeze-vulkan"); !ok || e.API != APIAudioCPP {
		t.Errorf("Qwen3 got engine %+v", e)
	}
	if e, ok := c.Engine(breeze, ""); !ok || e.ID != "breeze-vulkan" {
		t.Errorf("Breeze got engine %+v", e)
	}
}

func TestLanguage(t *testing.T) {
	for text, want := range map[string]string{"大家好": "zh", "Hello, 世界": "zh", "Hello there.": "en", "こんにちは世界": "ja", "안녕하세요": "ko", "123 !": ""} {
		if got := Language(text); got != want {
			t.Errorf("Language(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestEstimateAndFileName(t *testing.T) {
	zh, en := Estimate("你好，欢迎使用配音功能。"), Estimate("Hello there, this is a test of the voice.")
	if zh < 2 || zh > 4 || en < 2.5 || en > 5 || Estimate("") != 1 {
		t.Fatalf("estimates %v %v", zh, en)
	}
	m := Model{Model: transcribe.Model{ID: "q8_0", URL: "https://example.com/m.gguf"}}
	a := FileName(m, Request{Text: "hi", Voice: Voice{Instruction: "x", Seed: 1}})
	same := FileName(m, Request{Text: " hi\n", Voice: Voice{Instruction: "x", Seed: 1}})
	other := FileName(m, Request{Text: "hi", Voice: Voice{Instruction: "x", Seed: 2}})
	if a != same || a == other || !strings.HasSuffix(a, ".wav") || len(a) != 28 {
		t.Fatalf("%s %s %s", a, same, other)
	}
	if FileName(m, Request{Text: "hi", Voice: Voice{Instruction: "x"}}) != FileName(m, Request{Text: "hi", Voice: Voice{Instruction: "x", Seed: DefaultSeed}}) {
		t.Error("no seed and the default seed are different recordings")
	}
}

func TestWAVFileTrimsTheSilenceAfterTheLastWord(t *testing.T) {
	samples := func(n int, v int16) []byte {
		b := make([]byte, 0, n*2)
		for range n {
			b = binary.LittleEndian.AppendUint16(b, uint16(v))
		}
		return b
	}
	path := filepath.Join(t.TempDir(), "a.wav")
	f, err := newWAVFile(path)
	if err != nil {
		t.Fatal(err)
	}
	all := append(append(append(samples(8000, 0), samples(8000, -3000)...), samples(16000, 40)...), 9) // quiet, speech, near silence, half a sample
	for len(all) > 0 {                                                                                 // in pieces that cut samples in half
		k := min(len(all), 1001)
		f.Write(all[:k])
		all = all[k:]
	}
	seconds, err := f.finish(16000)
	if err != nil || seconds != 1+keepAfter {
		t.Fatalf("%v s, %v", seconds, err)
	}
	if l, ok := Length(path); !ok || l != seconds {
		t.Fatalf("Length %v %v", l, ok)
	}
	silent, _ := newWAVFile(filepath.Join(t.TempDir(), "b.wav"))
	silent.Write(samples(16000, 3))
	if _, err := silent.finish(16000); err == nil {
		t.Fatal("silence was kept as speech")
	}
	silent.abort()
}
