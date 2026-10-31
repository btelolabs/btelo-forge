package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/locale"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// TestMain lets the test run itself as a model engine: a program that listens on --port, says it is loading for a
// moment, and then answers like llama-server.
func TestMain(m *testing.M) {
	if os.Getenv("AI_FAKE_ENGINE") == "1" {
		fakeEngine()
		return
	}
	os.Exit(m.Run())
}

func fakeEngine() {
	port := ""
	for i, a := range os.Args {
		if (a == "--port" || a == "--listen-port") && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	if os.Getenv("AI_FAKE_EXIT") != "" {
		fmt.Println("error: unable to load model: out of memory")
		os.Exit(3)
	}
	began := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if time.Since(began) < 400*time.Millisecond {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/args", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(os.Args[1:])
	})
	mux.HandleFunc("/sdcpp/v1/models/status", func(w http.ResponseWriter, r *http.Request) {
		state := "ready"
		switch {
		case os.Getenv("AI_FAKE_SD_ERROR") != "":
			fmt.Println("error: failed to load model")
			state = "error"
		case time.Since(began) < 400*time.Millisecond:
			state = "loading"
		}
		json.NewEncoder(w).Encode(map[string]any{"state": state})
	})
	var last atomic.Value
	mux.HandleFunc("/last", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fmt.Sprint(last.Load())) })
	mux.HandleFunc("/v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		last.Store(string(body))
		var png bytes.Buffer
		pngEncode(&png, image.NewRGBA(image.Rect(0, 0, 8, 8)))
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(png.Bytes())}}})
	})
	mux.HandleFunc("/crash", func(http.ResponseWriter, *http.Request) { os.Exit(2) })
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, piece := range []string{"Hello", " ", "world"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", piece)
		}
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3},\"timings\":{\"predicted_per_second\":42.5}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	})
	http.ListenAndServe("127.0.0.1:"+port, mux)
}

// fakeSetup is a runtime whose engine is this test program, and a configuration of two models to download from a
// local server.
type fakeSetup struct {
	rt      *Runtime
	cfg     *Config
	changes atomic.Int64
	hits    atomic.Int64
	release chan struct{} // while set, downloads wait for it
}

func newFake(t *testing.T) *fakeSetup {
	t.Helper()
	f := &fakeSetup{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.release != nil {
			select {
			case <-f.release:
			case <-r.Context().Done():
				return
			}
		}
		io.WriteString(w, "weights of "+r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AI_FAKE_ENGINE", "1")
	model := func(id string) Model {
		return Model{
			Model:   transcribe.Model{ID: id, Name: locale.Text{"en": id}, URL: srv.URL + "/" + id + ".gguf"},
			API:     APILlama,
			License: locale.Text{"en": "test"},
		}
	}
	vision := model("vision")
	vision.Vision, vision.Projector = true, &File{URL: srv.URL + "/vision-projector.gguf"}
	vision.Context, vision.Arguments = 4096, []string{"--flag"}
	f.cfg = &Config{Version: 1, DefaultModel: "a",
		Engines: []Engine{{
			Engine:    transcribe.Engine{ID: "fake", Name: locale.Text{"en": "fake"}, Paths: []string{os.Args[0]}},
			API:       APILlama,
			Arguments: []string{"-m", "{model}", "--port", "{port}", "-c", "{context}"},
		}},
		Models: []Model{model("a"), model("b"), vision},
	}
	f.rt = NewRuntime(t.TempDir(), func() { f.changes.Add(1) })
	t.Cleanup(f.rt.Close)
	return f
}

func (f *fakeSetup) ready(t *testing.T, id string) Endpoint {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := f.rt.Wait(ctx, id); err != nil {
		t.Fatalf("waiting for %s: %v", id, err)
	}
	ep, ok := f.rt.Endpoint(id)
	if !ok {
		t.Fatalf("%s has no endpoint", id)
	}
	return ep
}

func (f *fakeSetup) args(t *testing.T, ep Endpoint) []string {
	t.Helper()
	base := strings.TrimSuffix(ep.URL, "/v1")
	resp, err := http.Get(base + "/args")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var args []string
	json.NewDecoder(resp.Body).Decode(&args)
	return args
}

func answers(ep Endpoint) bool {
	base := strings.TrimSuffix(ep.URL, "/v1")
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(base + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestLoadDownloadsStartsAndAnswers(t *testing.T) {
	f := newFake(t)
	if _, installed := f.rt.Files(f.cfg.Models[0]); installed {
		t.Fatal("a model nobody downloaded is installed")
	}
	if err := f.rt.Load(f.cfg, "a", "", true); err != nil {
		t.Fatal(err)
	}
	if st := f.rt.Status("a"); st.State != Downloading && st.State != Loading && st.State != Ready {
		t.Fatalf("state right after loading is %v", st.State)
	}
	ep := f.ready(t, "a")
	st := f.rt.Status("a")
	if st.State != Ready || st.Pid == 0 || st.Since.IsZero() || st.Engine.ID != "fake" {
		t.Fatalf("status %+v", st)
	}
	path, _ := f.rt.Manager().ModelPath(f.cfg.Models[0].Model)
	args := f.args(t, ep)
	if len(args) != 6 || args[0] != "-m" || args[1] != path || args[2] != "--port" || args[4] != "-c" || args[5] != "8192" {
		t.Fatalf("arguments %q", args)
	}
	if size, installed := f.rt.Files(f.cfg.Models[0]); !installed || size != int64(len("weights of /a.gguf")) {
		t.Fatalf("files: %d %v", size, installed)
	}
	var got strings.Builder
	stats, err := ep.Chat(context.Background(), []Message{{Role: User, Text: "hi"}}, Options{}, func(c Chunk) { got.WriteString(c.Text) })
	if err != nil || got.String() != "Hello world" || stats.PromptTokens != 7 || stats.CompletionTokens != 3 || stats.TokensPerSecond != 42.5 {
		t.Fatalf("chat: %q %+v %v", got.String(), stats, err)
	}
	if f.changes.Load() == 0 {
		t.Fatal("nobody was told the status changed")
	}
	hits := f.hits.Load()
	f.rt.Unload("a")
	if err := f.rt.Load(f.cfg, "a", "", true); err != nil {
		t.Fatal(err)
	}
	f.ready(t, "a")
	if f.hits.Load() != hits {
		t.Fatal("a model was downloaded again")
	}
}

func TestLoadGivesAProjectorAndTheModelsOwnArguments(t *testing.T) {
	f := newFake(t)
	if err := f.rt.Load(f.cfg, "vision", "", true); err != nil {
		t.Fatal(err)
	}
	args := f.args(t, f.ready(t, "vision"))
	projector, _ := f.rt.Manager().ModelPath(projectorModel(f.cfg.Models[2]))
	n := len(args)
	if args[5] != "4096" || args[n-3] != "--mmproj" || args[n-2] != projector || args[n-1] != "--flag" {
		t.Fatalf("arguments %q", args)
	}
	if size, installed := f.rt.Files(f.cfg.Models[2]); !installed || size != int64(len("weights of /vision.gguf")+len("weights of /vision-projector.gguf")) {
		t.Fatalf("files: %d %v", size, installed)
	}
}

func TestUnloadEndsTheProgram(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "a", "", true)
	ep := f.ready(t, "a")
	f.rt.Unload("a")
	if _, ok := f.rt.Endpoint("a"); ok {
		t.Fatal("an unloaded model still has an endpoint")
	}
	if len(f.rt.Statuses()) != 0 {
		t.Fatalf("statuses %+v", f.rt.Statuses())
	}
	eventually(t, "the program to end", func() bool { return !answers(ep) })
}

func TestLoadingAnotherModelReplacesTheFirst(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "a", "", true)
	first := f.ready(t, "a")
	f.rt.Load(f.cfg, "b", "", true)
	second := f.ready(t, "b")
	if answers(first) {
		t.Fatal("the first model still runs")
	}
	if !answers(second) {
		t.Fatal("the second does not")
	}
	if list := f.rt.Statuses(); len(list) != 1 || list[0].Model != "b" {
		t.Fatalf("statuses %+v", list)
	}
}

func TestLoadingWithoutExclusiveKeepsTheOthers(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "a", "", false)
	first := f.ready(t, "a")
	f.rt.Load(f.cfg, "b", "", false)
	second := f.ready(t, "b")
	if !answers(first) || !answers(second) {
		t.Fatal("both should run")
	}
	if list := f.rt.Statuses(); len(list) != 2 || list[0].Model != "a" || list[1].Model != "b" {
		t.Fatalf("statuses %+v", list)
	}
	f.rt.UnloadAll()
	eventually(t, "both to end", func() bool { return !answers(first) && !answers(second) })
}

func TestLoadingTwiceStartsOneProgram(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "a", "", true)
	f.rt.Load(f.cfg, "a", "", true)
	f.ready(t, "a")
	if f.hits.Load() != 1 {
		t.Fatalf("%d downloads", f.hits.Load())
	}
	if len(f.rt.Statuses()) != 1 {
		t.Fatal("two instances")
	}
}

func TestAnEngineThatStopsWhileLoadingFailsWithItsLastWords(t *testing.T) {
	f := newFake(t)
	t.Setenv("AI_FAKE_EXIT", "1")
	f.rt.Load(f.cfg, "a", "", true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := f.rt.Wait(ctx, "a")
	if err == nil || !strings.Contains(err.Error(), "stopped while loading") || !strings.Contains(err.Error(), "out of memory") {
		t.Fatalf("err = %v", err)
	}
	if st := f.rt.Status("a"); st.State != Failed || st.Err == nil {
		t.Fatalf("status %+v", st)
	}
	// Trying again, now that the engine works, replaces the failure.
	t.Setenv("AI_FAKE_EXIT", "")
	f.rt.Load(f.cfg, "a", "", true)
	f.ready(t, "a")
}

func TestAModelThatCrashesIsFailed(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "a", "", true)
	ep := f.ready(t, "a")
	http.Get(strings.TrimSuffix(ep.URL, "/v1") + "/crash")
	eventually(t, "the crash to show", func() bool { return f.rt.Status("a").State == Failed })
	st := f.rt.Status("a")
	if st.Err == nil || !strings.Contains(st.Err.Error(), "stopped") || st.Pid != 0 {
		t.Fatalf("status %+v", st)
	}
	if _, ok := f.rt.Endpoint("a"); ok {
		t.Fatal("a crashed model has an endpoint")
	}
}

func TestUnloadingWhileDownloadingReleasesWhoWaits(t *testing.T) {
	f := newFake(t)
	f.release = make(chan struct{})
	f.rt.Load(f.cfg, "a", "", true)
	eventually(t, "the download to start", func() bool { return f.hits.Load() == 1 })
	done := make(chan error, 1)
	go func() { done <- f.rt.Wait(context.Background(), "a") }()
	time.Sleep(50 * time.Millisecond)
	f.rt.Unload("a")
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "unloaded") {
			t.Fatalf("wait returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait is still waiting for a model that was unloaded")
	}
}

func TestEnsureLoadsAndWaitsWithoutUnloadingOthers(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "a", "", true)
	first := f.ready(t, "a")
	ep, err := f.rt.Ensure(context.Background(), f.cfg, "b", "")
	if err != nil {
		t.Fatal(err)
	}
	if !answers(ep) || !answers(first) {
		t.Fatal("both models should answer")
	}
	if _, err := f.rt.Ensure(context.Background(), f.cfg, "nope", ""); err == nil {
		t.Fatal("an unknown model was ensured")
	}
}

func TestDeleteRemovesFilesButNotOfALoadedModel(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "vision", "", true)
	f.ready(t, "vision")
	m := f.cfg.Models[2]
	if err := f.rt.Delete(m); err == nil {
		t.Fatal("the files of a loaded model were deleted")
	}
	f.rt.Unload("vision")
	path, _ := f.rt.Manager().ModelPath(m.Model)
	os.WriteFile(path+".part", []byte("half"), 0o644)
	if err := f.rt.Delete(m); err != nil {
		t.Fatal(err)
	}
	if _, installed := f.rt.Files(m); installed {
		t.Fatal("still installed")
	}
	if _, err := os.Stat(path + ".part"); err == nil {
		t.Fatal("a half-downloaded file was left")
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		t.Fatal("the model's folder was left")
	}
}

func TestLoadRefusesWhatCannotRun(t *testing.T) {
	f := newFake(t)
	if err := f.rt.Load(f.cfg, "nope", "", true); err == nil {
		t.Fatal("an unknown model loaded")
	}
	cfg := *f.cfg
	cfg.Engines = []Engine{{Engine: transcribe.Engine{ID: "x", Name: locale.Text{"en": "x"}, Paths: []string{"x"}, OS: []string{"plan9"}}, API: APILlama, Arguments: []string{"{model}", "{port}"}}}
	if err := f.rt.Load(&cfg, "a", "", true); err == nil || !strings.Contains(err.Error(), "no engine") {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingInstalledEngineIsReportedWithHowToInstallIt(t *testing.T) {
	f := newFake(t)
	cfg := *f.cfg
	cfg.Engines = []Engine{{Engine: transcribe.Engine{ID: "brew", Name: locale.Text{"en": "llama.cpp"}, Commands: []string{"no-such-llama-server"}, Install: "brew install llama.cpp"},
		API: APILlama, Arguments: []string{"-m", "{model}", "--port", "{port}"}}}
	f.rt.Load(&cfg, "a", "", true)
	err := f.rt.Wait(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "brew install llama.cpp") {
		t.Fatalf("err = %v", err)
	}
}

func TestMeasurementsReachTheStatus(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "a", "", true)
	f.ready(t, "a")
	eventually(t, "a measurement", func() bool { return f.rt.Status("a").Measured })
	if u := f.rt.Status("a").Usage; u.Memory == 0 {
		t.Fatalf("usage %+v", u)
	}
}

func TestClosedRuntimeRefusesToLoad(t *testing.T) {
	f := newFake(t)
	f.rt.Load(f.cfg, "a", "", true)
	ep := f.ready(t, "a")
	f.rt.Close()
	if answers(ep) {
		t.Fatal("a model survived Close")
	}
	if err := f.rt.Load(f.cfg, "a", "", true); err != ErrClosed {
		t.Fatalf("err = %v", err)
	}
}

func TestEngineArgumentsFillPlaceholders(t *testing.T) {
	in := &instance{model: Model{Context: 0, Arguments: []string{"--x"}}, engine: Engine{Arguments: []string{"-m", "{model}", "--port", "{port}", "-c", "{context}"}}}
	got := strings.Join(in.arguments("/m.gguf", "", nil, 8080), " ")
	if got != "-m /m.gguf --port 8080 -c "+strconv.Itoa(DefaultContext)+" --x" {
		t.Fatal(got)
	}
	if got := strings.Join(in.arguments("/m.gguf", "/p.gguf", nil, 1), " "); !strings.Contains(got, "--mmproj /p.gguf --x") {
		t.Fatal(got)
	}
}

func TestTrackedProgramsAreMeasuredWhileTheyRun(t *testing.T) {
	f := newFake(t)
	var pid atomic.Int64
	f.rt.Track("speech", func() int { return int(pid.Load()) })
	if _, ok := f.rt.Usage("speech"); ok {
		t.Fatal("usage before anything ran")
	}
	f.rt.Load(f.cfg, "a", "", true)
	f.ready(t, "a")
	pid.Store(int64(f.rt.Status("a").Pid)) // any program that runs will do
	eventually(t, "the tracked program to be measured", func() bool { _, ok := f.rt.Usage("speech"); return ok })
	if u, _ := f.rt.Usage("speech"); u.Memory == 0 {
		t.Fatalf("usage %+v", u)
	}
	pid.Store(0)
	eventually(t, "the measurement to be forgotten", func() bool { _, ok := f.rt.Usage("speech"); return !ok })
	if _, ok := f.rt.Usage("nothing"); ok {
		t.Fatal("usage of an unknown program")
	}
}

func pngEncode(w io.Writer, img image.Image) { png.Encode(w, img) }

// imageFake is the fake runtime with an engine of stable-diffusion.cpp's kind and an image model of three files.
func imageFake(t *testing.T) (*fakeSetup, Model) {
	t.Helper()
	f := newFake(t)
	srv := f.cfg.Models[0].URL[:strings.LastIndex(f.cfg.Models[0].URL, "/")]
	f.cfg.Engines = append(f.cfg.Engines, Engine{
		Engine:    transcribe.Engine{ID: "sd-fake", Name: locale.Text{"en": "fake sd"}, Paths: []string{os.Args[0]}},
		API:       APISD,
		Arguments: []string{"--listen-port", "{port}"},
	})
	m := Model{
		Model:     transcribe.Model{ID: "paint", Name: locale.Text{"en": "Paint"}, URL: srv + "/paint.gguf"},
		API:       APISD,
		Kind:      KindImage,
		License:   locale.Text{"en": "test"},
		Parts:     []Part{{Role: "vae", File: File{URL: srv + "/paint-vae.safetensors"}}, {Role: "llm", File: File{URL: srv + "/paint-llm.gguf"}}},
		Arguments: []string{"--diffusion-model", "{model}", "--vae", "{vae}", "--llm", "{llm}"},
		ImageSize: "640x384",
		Steps:     7,
	}
	f.cfg.Models = append(f.cfg.Models, m)
	return f, m
}

func TestAnImageModelIsFetchedWithItsPartsStartedWithThemAndAskedForAPicture(t *testing.T) {
	f, m := imageFake(t)
	if size, installed := f.rt.Files(m); installed || size != 0 {
		t.Fatalf("files before loading: %d %v", size, installed)
	}
	if err := f.rt.Load(f.cfg, "paint", "", true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := f.rt.Wait(ctx, "paint"); err != nil {
		t.Fatal(err)
	}
	if st := f.rt.Status("paint"); st.State != Ready || st.Engine.ID != "sd-fake" {
		t.Fatalf("status %+v", st)
	}
	size, installed := f.rt.Files(m)
	if !installed || size == 0 {
		t.Fatalf("files after loading: %d %v", size, installed)
	}
	ep, ok := f.rt.Endpoint("paint")
	if !ok {
		t.Fatal("no endpoint")
	}
	// The engine was started with the model and each part where its role stands.
	resp, err := http.Get(strings.TrimSuffix(ep.URL, "/v1") + "/args")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	json.NewDecoder(resp.Body).Decode(&args)
	resp.Body.Close()
	line := strings.Join(args, " ")
	for _, want := range []string{"--listen-port", "--diffusion-model ", "paint.gguf", "--vae ", "paint-vae.safetensors", "--llm ", "paint-llm.gguf"} {
		if !strings.Contains(line, want) {
			t.Errorf("arguments %q lack %q", line, want)
		}
	}
	if strings.Contains(line, "{") {
		t.Errorf("a placeholder is left in %q", line)
	}
	// A picture is asked for with the model's own size and steps.
	pic, err := ep.Generate(ctx, "a fox", ImageOptions{Size: m.ImageSize, Steps: m.Steps})
	if err != nil || pic.MIME != "image/png" {
		t.Fatalf("picture %v %s", err, pic.MIME)
	}
	resp, err = http.Get(strings.TrimSuffix(ep.URL, "/v1") + "/last")
	if err != nil {
		t.Fatal(err)
	}
	var asked map[string]any
	json.NewDecoder(resp.Body).Decode(&asked)
	resp.Body.Close()
	if asked["size"] != "640x384" || !strings.Contains(asked["prompt"].(string), `"sample_steps":7`) || !strings.HasPrefix(asked["prompt"].(string), "a fox ") {
		t.Fatalf("the engine was asked %v", asked)
	}
	// Deleting takes every file.
	f.rt.Unload("paint")
	waitFor(t, func() bool { return f.rt.Status("paint").State == Unloaded })
	if err := f.rt.Delete(m); err != nil {
		t.Fatal(err)
	}
	if _, installed := f.rt.Files(m); installed {
		t.Fatal("files remain after Delete")
	}
	for _, p := range m.Parts {
		if path, ok := f.rt.Manager().ModelPath(partModel(m, p)); ok {
			t.Errorf("%s remains", path)
		}
	}
}

func TestAnImageModelThatCannotLoadFailsWithWhatTheEnginePrinted(t *testing.T) {
	f, _ := imageFake(t)
	t.Setenv("AI_FAKE_SD_ERROR", "1")
	if err := f.rt.Load(f.cfg, "paint", "", true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := f.rt.Wait(ctx, "paint")
	if err == nil || !strings.Contains(err.Error(), "failed to load model") {
		t.Fatalf("err = %v", err)
	}
}

func TestEngineArgumentsGiveEachPartWhereItsRoleStands(t *testing.T) {
	in := &instance{model: Model{Arguments: []string{"--d", "{model}", "--vae", "{vae}"}}, engine: Engine{Arguments: []string{"--listen-port", "{port}"}}}
	got := strings.Join(in.arguments("/m", "", map[string]string{"vae": "/v"}, 9), " ")
	if got != "--listen-port 9 --d /m --vae /v" {
		t.Fatal(got)
	}
}

func TestTheBackendChosenForOneKindOfModelHoldsForAnother(t *testing.T) {
	c := DefaultConfig()
	llm, _ := c.Model("qwen3-4b")
	img, _ := c.Model("sd15")
	if e, ok := c.Engine(llm, "cpu"); !ok || e.API != APILlama || e.Backend != "cpu" {
		t.Fatalf("llm on cpu: %+v", e)
	}
	if e, ok := c.Engine(img, "cpu"); !ok || e.API != APISD || e.Backend != "cpu" {
		t.Fatalf("image on cpu: %+v", e)
	}
	if e, ok := c.Engine(img, "vulkan"); !ok || e.API != APISD || e.Backend != "vulkan" {
		t.Fatalf("image on vulkan: %+v", e)
	}
	if e, ok := c.Engine(img, "nonsense"); !ok || e.API != APISD {
		t.Fatalf("image on an unknown choice: %+v", e)
	}
}

// waitFor waits up to ten seconds for cond.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("the condition was not met in time")
	}
}
