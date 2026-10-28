package transcribe

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/locale"
)

func TestParseSkipsNonSpeechAndConvertsTimes(t *testing.T) {
	lines, err := parse([]byte(`{"transcription":[{"offsets":{"from":0,"to":1500},"text":" Hello there."},
{"offsets":{"from":1500,"to":2000},"text":" [MUSIC]"},{"offsets":{"from":2000,"to":3250},"text":"你好"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Text != "Hello there." || lines[0].End != 1.5 || lines[1].Start != 2 || lines[1].Text != "你好" {
		t.Fatalf("%+v", lines)
	}
}

func TestDefaultConfigIsValid(t *testing.T) {
	c := DefaultConfig()
	for i, m := range c.Models {
		if !strings.HasPrefix(m.URL, "https://huggingface.co/") || i > 0 && m.Size < c.Models[i-1].Size {
			t.Fatalf("model %+v", m)
		}
	}
	if _, ok := c.Model(c.DefaultModel); !ok {
		t.Fatal("default model missing")
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if len(c.Available()) == 0 {
			t.Fatal("no engine for this computer")
		}
	}
}

func TestConfigErrorsArePrecise(t *testing.T) {
	base := string(DefaultJSON)
	for data, want := range map[string]string{
		strings.Replace(base, `"defaultModel": "small"`, `"defaultModel": "huge"`, 1):          "defaultModel",
		strings.Replace(base, `"-of", "{output}"`, `"-of", "{out}"`, 1):                        "unknown placeholder {out}",
		strings.Replace(base, `"{model}", "-f"`, `"x", "-f"`, 1):                               "must use {model}",
		strings.Replace(base, `whisper-bin-x64.zip"`, `whisper-bin-x64.tar"`, 1):               ".zip",
		strings.Replace(base, `"executable": "whisper-cli.exe"`, `"executable": "a/b.exe"`, 1): "file name",
		strings.Replace(base, `"id": "base"`, `"id": "tiny"`, 1):                               "models[1] (tiny)",
		strings.Replace(base, `"progress": "progress =\\s*(\\d+)%"`, `"progress": "("`, 1):     "progress",
		strings.Replace(base, `"version": 1`, `"version": 1, "colour": 2`, 1):                  "unknown field",
	} {
		if _, err := ParseConfig([]byte(data)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v, want %q", err, want)
		}
	}
}

func TestArgumentsFillTheirPlaceholders(t *testing.T) {
	got := fill([]string{"-m", "{model}", "--of={output}", "{nothing}"}, map[string]string{"model": "m.bin", "output": "o"})
	if strings.Join(got, " ") != "-m m.bin --of=o " {
		t.Fatalf("%q", got)
	}
}

// TestRunTranscribesSpeech uses a whisper-cli and model already on disk (FORGE_WHISPER_CLI, FORGE_WHISPER_MODEL and a
// speech WAV in FORGE_WHISPER_WAV); it is skipped otherwise, since the engine is a large download.
func TestRunTranscribesSpeech(t *testing.T) {
	cli, model, wav := os.Getenv("FORGE_WHISPER_CLI"), os.Getenv("FORGE_WHISPER_MODEL"), os.Getenv("FORGE_WHISPER_WAV")
	if cli == "" || model == "" || wav == "" {
		t.Skip("set FORGE_WHISPER_CLI, FORGE_WHISPER_MODEL and FORGE_WHISPER_WAV to run")
	}
	copyWav := filepath.Join(t.TempDir(), "speech.wav")
	data, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(copyWav, data, 0o644)
	var last float64
	lines, err := Run(context.Background(), DefaultConfig(), cli, model, copyWav, "auto", func(stage string, f float64) { last = f })
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || !strings.Contains(strings.ToLower(lines[0].Text), "country") || last != 1 {
		t.Fatalf("%+v progress %v", lines, last)
	}
}

func TestPrepareDownloadsAndUnpacksOnce(t *testing.T) {
	var zipped bytes.Buffer
	w := zip.NewWriter(&zipped)
	f, _ := w.Create("Release/whisper-cli.exe")
	f.Write([]byte("exe"))
	g, _ := w.Create("Release/ggml.dll")
	g.Write([]byte("dll"))
	w.Close()
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/v1/whisper-bin-x64.zip":
			rw.Write(zipped.Bytes())
		case "/ggml-tiny.bin":
			rw.Write([]byte("model"))
		default:
			http.NotFound(rw, r)
		}
	}))
	defer s.Close()
	m := NewManager(t.TempDir())
	engine := Engine{ID: "cpu", Name: locale.Text{"en": "CPU"}, URL: s.URL + "/v1/whisper-bin-x64.zip", Executable: "whisper-cli.exe"}
	sum := sha256.Sum256([]byte("model"))
	tiny := Model{ID: "tiny", Name: locale.Text{"en": "Tiny"}, URL: s.URL + "/ggml-tiny.bin", SHA256: hex.EncodeToString(sum[:])}
	cli, model, err := m.Prepare(context.Background(), engine, tiny, func(string, float64) {})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(cli); string(data) != "exe" || filepath.Base(cli) != "whisper-cli.exe" {
		t.Fatalf("engine %s %q", cli, data)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cli), "ggml.dll")); err != nil {
		t.Fatal("dll not unpacked next to the engine")
	}
	if data, _ := os.ReadFile(model); string(data) != "model" {
		t.Fatal("model")
	}
	if _, _, err := m.Prepare(context.Background(), engine, tiny, func(string, float64) {}); err != nil || hits != 2 {
		t.Fatalf("second prepare downloaded again: %d requests, %v", hits, err)
	}
	newer := engine
	newer.URL = s.URL + "/v2/whisper-bin-x64.zip"
	if p, ok := m.EnginePath(newer); ok || p == cli {
		t.Fatal("a new engine version must get its own folder")
	}
	bad := tiny
	bad.ID, bad.SHA256 = "bad", strings.Repeat("0", 64)
	if _, _, err := m.Prepare(context.Background(), engine, bad, func(string, float64) {}); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("checksum not checked: %v", err)
	}
	if _, ok := m.ModelPath(bad); ok {
		t.Fatal("a file with the wrong checksum was kept")
	}
}

func TestMissingInstalledEngineSaysHowToInstall(t *testing.T) {
	e := Engine{ID: "brew", Name: locale.Text{"en": "whisper.cpp"}, Commands: []string{"no-such-whisper-cli"}, Install: "brew install whisper-cpp"}
	_, _, err := NewManager(t.TempDir()).Prepare(context.Background(), e, DefaultConfig().Models[0], func(string, float64) {})
	var missing *MissingEngine
	if !errors.As(err, &missing) || !errors.Is(err, ErrNoEngine) || !strings.Contains(err.Error(), "brew install whisper-cpp") {
		t.Fatalf("%v", err)
	}
}
