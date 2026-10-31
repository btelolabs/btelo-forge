package ai

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/locale"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// TestRealLlamaServer runs a real llama-server on a real model, to see that the built-in arguments are accepted and
// that the answers, the thinking and the measurements come out right. It needs llama-server and a GGUF model
// (FORGE_LLAMA_SERVER and FORGE_LLAMA_MODEL name them, FORGE_LLAMA_ARGS replaces the engine's -ngl and the like) and is
// skipped otherwise, since both are large downloads.
func TestRealLlamaServer(t *testing.T) {
	server, model := os.Getenv("FORGE_LLAMA_SERVER"), os.Getenv("FORGE_LLAMA_MODEL")
	if server == "" || model == "" {
		t.Skip("set FORGE_LLAMA_SERVER and FORGE_LLAMA_MODEL to run against a real model")
	}
	cfg := DefaultConfig()
	engine := cfg.Engines[0]
	engine.Engine = transcribe.Engine{ID: "real", Name: locale.Text{"en": "real"}, Paths: []string{server}}
	cfg.Engines = []Engine{engine}
	rt := NewRuntime(t.TempDir(), nil)
	defer rt.Close()
	m := cfg.Models[0]
	// The model file is put where the runtime looks for it, so that nothing is downloaded.
	path, _ := rt.Manager().ModelPath(m.Model)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := linkOrCopy(model, path); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ep, err := rt.Ensure(ctx, cfg, m.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded in %s", time.Since(began).Round(time.Millisecond))

	var text, thinking strings.Builder
	stats, err := ep.Chat(ctx, []Message{{Role: User, Text: "用一句话回答：2+3等于几？"}}, Options{MaxTokens: 600}, func(c Chunk) {
		text.WriteString(c.Text)
		thinking.WriteString(c.Reasoning)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("answer %q", text.String())
	t.Logf("thinking %d bytes: %.80q", thinking.Len(), thinking.String())
	t.Logf("stats %+v", stats)
	if !strings.Contains(text.String(), "5") && !strings.Contains(text.String(), "五") {
		t.Errorf("the answer does not say 5: %q", text.String())
	}
	if strings.Contains(text.String(), "<think>") {
		t.Errorf("thinking leaked into the answer: %q", text.String())
	}
	if stats.CompletionTokens == 0 || stats.TokensPerSecond == 0 {
		t.Errorf("stats %+v", stats)
	}

	// The measurements need a moment to see the model at work.
	var st Status
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st = rt.Status(m.ID)
		if st.Measured && st.Usage.Memory > 0 && st.Usage.GPUMemory > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("usage: cpu %.1f%%, ram %d MB, vram %d MB, gpu %.1f%%, gpu known %v", st.Usage.CPU, st.Usage.Memory>>20, st.Usage.GPUMemory>>20, st.Usage.GPU, st.Usage.GPUKnown)
	if !st.Measured || st.Usage.Memory == 0 {
		t.Errorf("not measured: %+v", st)
	}
	rt.Unload(m.ID)
}

// linkOrCopy makes dst the file src, by a hard link when the two are on one volume.
func linkOrCopy(src, dst string) error {
	if os.Link(src, dst) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
