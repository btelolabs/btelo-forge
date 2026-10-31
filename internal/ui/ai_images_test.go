package ui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/locale"
	"github.com/btelolabs/btelo-forge/internal/proc"
)

// gradientPNG is a picture a test service can "draw": red to blue across, dark to light down.
func gradientPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(255 * (w - x) / w), G: uint8(255 * y / h), B: uint8(255 * x / w), A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// useOnlineImageService adds an image model to the DeepSeek entry, whose service is a test server that answers
// "/images/generations" with a picture and everything else as a chat; it returns what the server was asked for pictures.
func useOnlineImageService(t *testing.T, x *harness, chat *onlineChat) (asked *[]map[string]any) {
	t.Helper()
	var prompts []map[string]any
	pic := base64.StdEncoding.EncodeToString(gradientPNG(t, 640, 400))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/images/generations") {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			prompts = append(prompts, body)
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": pic}}})
			return
		}
		chat.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := ai.DefaultConfig()
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "deepseek" {
			cfg.Providers[i].BaseURL = srv.URL
			cfg.Providers[i].Models = append(cfg.Providers[i].Models,
				ai.RemoteModel{ID: "paint", Model: "paint-1", Kind: ai.KindImage, Name: localeText("Paint 1"), Note: localeText("Pictures from a description")})
		}
	}
	x.a.setAIConfig(cfg)
	if err := x.a.ai.keys.Set("deepseek", "sk-test-key"); err != nil {
		t.Fatal(err)
	}
	x.a.settings.ResourceDir = t.TempDir()
	return &prompts
}

func TestAnImageModelDrawsKeepsThePictureInItsFolderAndShowsIt(t *testing.T) {
	x := newHarness(t)
	svc := newOnlineChat(t, delta("content", "好的"), `{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":2}}`)
	prompts := useOnlineImageService(t, x, svc)
	root, _ := x.a.aiAssetRoot()
	ch, ok := x.a.ai.config.Choice("deepseek/paint")
	if !ok || ch.Kind != ai.KindImage {
		t.Fatalf("the image model: %+v %v", ch, ok)
	}
	x.a.aiPick(ch)
	x.a.openAITab()
	x.frame(Input{})
	x.shot("ai-image-empty")

	x.typeText("a red fox in the snow")
	x.key(keyEnter, false)
	if x.a.ai.answer == nil {
		t.Fatalf("Enter did not send; notice %q", x.a.ai.notice)
	}
	x.frame(Input{})
	x.shot("ai-image-drawing")
	x.pump(20*time.Second, x.answered)
	x.pump(10*time.Second, func() bool {
		for _, p := range x.a.ai.pics {
			if p.loading {
				return false
			}
		}
		return len(x.a.ai.pics) > 0
	})
	x.settle(5 * time.Second)

	if len(*prompts) != 1 || (*prompts)[0]["prompt"] != "a red fox in the snow" || (*prompts)[0]["model"] != "paint-1" {
		t.Fatalf("the service was asked %v", *prompts)
	}
	chat := x.a.ai.chat
	if chat == nil || len(chat.Messages) != 2 || len(chat.Messages[1].Media) != 1 || x.a.ai.notice != "" {
		t.Fatalf("conversation %+v, notice %q", chat, x.a.ai.notice)
	}
	m := chat.Messages[1].Media[0]
	if m.Kind != ai.KindImage || filepath.Dir(m.Path) != filepath.Join(root, "ai-image") || !strings.HasSuffix(m.Path, "-a-red-fox-in-the-snow.png") {
		t.Fatalf("the picture is kept at %+v", m)
	}
	if data, err := os.ReadFile(m.Path); err != nil || len(data) == 0 {
		t.Fatalf("the file: %v", err)
	}
	if p := x.a.ai.pics[m.Path]; p == nil || p.img == nil || p.err != nil {
		t.Fatalf("the picture was not read: %+v", p)
	}
	x.shot("ai-image-done")

	// The conversation is kept with the path, and a later question to a language model does not send the picture's
	// empty message.
	saved, err := x.a.ai.chats.Load(chat.ID)
	if err != nil || len(saved.Messages[1].Media) != 1 || saved.Messages[1].Media[0].Path != m.Path {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	llm, _ := x.a.ai.config.Choice("deepseek/deepseek-v4-flash")
	x.a.aiPick(llm)
	x.typeText("thanks")
	x.key(keyEnter, false)
	x.pump(20*time.Second, x.answered)
	for _, msg := range svc.asked[len(svc.asked)-1]["messages"].([]any) {
		if msg.(map[string]any)["content"] == "" {
			t.Fatalf("an empty message was sent: %v", svc.asked)
		}
	}
}

func TestAnImageThatCannotBeDrawnIsReportedWithARetry(t *testing.T) {
	x := newHarness(t)
	svc := newOnlineChat(t)
	useOnlineImageService(t, x, svc)
	// The service refuses.
	cfg := x.a.ai.config
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"the prompt was refused"}}`))
	}))
	defer bad.Close()
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "deepseek" {
			cfg.Providers[i].BaseURL = bad.URL
		}
	}
	ch, _ := cfg.Choice("deepseek/paint")
	x.a.aiPick(ch)
	x.a.openAITab()
	x.frame(Input{})
	x.typeText("anything")
	x.key(keyEnter, false)
	x.pump(20*time.Second, x.answered)
	x.settle(5 * time.Second)
	if !strings.Contains(x.a.ai.notice, "refused") {
		t.Fatalf("notice %q", x.a.ai.notice)
	}
	if entries, _ := os.ReadDir(filepath.Join(x.a.settings.ResourceDir, "Intelligence", "ai-image")); len(entries) != 0 {
		t.Fatalf("%d files were kept for a picture that was not made", len(entries))
	}
	x.shot("ai-image-error")
}

// pickerShot takes a picture of the list of models open on a kind.
func pickerShot(x *harness, kind ai.Kind, name string) {
	x.a.ai.pickKind = kind
	x.frame(Input{})
	x.shot(name)
}

func TestThePickerShowsEveryKindAndOneKindAtATime(t *testing.T) {
	x := newHarness(t)
	svc := newOnlineChat(t)
	useOnlineImageService(t, x, svc)
	x.a.openAITab()
	x.a.ai.picking = true
	x.frame(Input{})
	x.shot("ai-picker")
	for _, k := range ai.ChatKinds {
		if name := x.a.aiKindName(k); name == "" || name == string(k) {
			t.Errorf("%s has no title", k)
		}
	}
	if x.a.ai.pickKind != "" {
		t.Fatalf("it opened on %q, not on the kind of the model in use", x.a.ai.pickKind)
	}
	if len(x.a.ai.pickTabs) != len(ai.ChatKinds) {
		t.Fatalf("%d kinds on the bar", len(x.a.ai.pickTabs))
	}
	x.click(x.a.ai.pickTabs[slices.Index(ai.ChatKinds, ai.KindImage)])
	if x.a.ai.pickKind != ai.KindImage || !x.a.ai.picking {
		t.Fatalf("the bar chose %q, list open: %v", x.a.ai.pickKind, x.a.ai.picking)
	}
	x.shot("ai-picker-images")
}

func TestAKindWithoutModelsSaysWhereToAddOne(t *testing.T) {
	x := newHarness(t)
	cfg := ai.DefaultConfig()
	for i := range cfg.Providers {
		cfg.Providers[i].Models = slices.DeleteFunc(cfg.Providers[i].Models, func(m ai.RemoteModel) bool { return m.Kind.Or() == ai.KindImage })
	}
	cfg.Models = slices.DeleteFunc(cfg.Models, func(m ai.Model) bool { return m.Kind.Or() == ai.KindImage })
	x.a.setAIConfig(cfg)
	x.a.openAITab()
	x.a.ai.picking = true
	pickerShot(x, ai.KindImage, "ai-picker-empty")
	x.a.u.lang = "zh"
	pickerShot(x, ai.KindImage, "ai-picker-empty-zh")
	x.c = gfx.NewCanvas(520, 700)
	pickerShot(x, ai.KindImage, "ai-picker-empty-zh-narrow")
	x.a.u.lang = "en"
	pickerShot(x, ai.KindImage, "ai-picker-empty-narrow")
	if !x.a.ai.picking {
		t.Fatal("the list closed")
	}
}

// TestThePickerBarEdgesMeet checks that the kinds fill the list's width exactly, in rows that do not overlap, whatever
// the width, scale and language.
func TestThePickerBarEdgesMeet(t *testing.T) {
	x := newHarness(t)
	defer func() { x.a.u.s, x.a.u.lang = 1, "en" }()
	for _, lang := range []Language{"en", "zh"} {
		x.a.u.lang = lang
		for _, scale := range []float32{1, 1.25, 1.5, 2} {
			x.a.u.s = scale
			for _, width := range []float32{472, 508, 700} {
				r := rectAt(30, 40, width*scale, 500)
				tabs, bottom := x.a.aiKindBar(r)
				where := fmt.Sprintf("%s at %v, width %v", lang, scale, width)
				if tabs[0].X != r.X || tabs[0].Y != r.Y {
					t.Errorf("%s: the bar starts at %+v in %+v", where, tabs[0], r)
				}
				cols := 0
				for _, tab := range tabs {
					if tab.Y == tabs[0].Y {
						cols++
					}
				}
				if cols != 6 && cols != 3 {
					t.Errorf("%s: %d in a row", where, cols)
				}
				for i, tab := range tabs {
					if tab.H != tabs[0].H || tab.Bottom() > bottom {
						t.Errorf("%s: tab %d is %+v, bar ends at %v", where, i, tab, bottom)
					}
					if (i+1)%cols == 0 && abs32(tab.Right()-r.Right()) > 0.01 {
						t.Errorf("%s: a row ends at %v, not %v", where, tab.Right(), r.Right())
					}
					if (i+1)%cols != 0 && tab.Right() >= tabs[i+1].X {
						t.Errorf("%s: tabs %d and %d touch", where, i, i+1)
					}
				}
			}
		}
	}
}

func localeText(s string) locale.Text { return locale.Text{"en": s} }

// useOnlineVisionService adds a language model that reads pictures to the DeepSeek entry, whose service is chat.
func useOnlineVisionService(t *testing.T, x *harness, chat *onlineChat) {
	t.Helper()
	cfg := ai.DefaultConfig()
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "deepseek" {
			cfg.Providers[i].BaseURL = chat.URL
			cfg.Providers[i].Models = append(cfg.Providers[i].Models,
				ai.RemoteModel{ID: "eyes", Model: "eyes-1", Vision: true, Name: localeText("Eyes 1"), Note: localeText("Reads pictures")})
		}
	}
	x.a.setAIConfig(cfg)
	if err := x.a.ai.keys.Set("deepseek", "sk-test-key"); err != nil {
		t.Fatal(err)
	}
}

func TestPicturesAttachedToAMessageGoToAModelThatReadsThem(t *testing.T) {
	x := newHarness(t)
	svc := newOnlineChat(t, delta("content", "a gradient"), `{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":2}}`)
	useOnlineVisionService(t, x, svc)
	dir := t.TempDir()
	pic := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(pic, gradientPNG(t, 640, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.txt")
	os.WriteFile(notes, []byte("x"), 0o644)

	// A model that does not read pictures takes none.
	blind, _ := x.a.ai.config.Choice("deepseek/deepseek-v4-flash")
	x.a.aiPick(blind)
	x.a.openAITab()
	x.frame(Input{})
	x.a.aiAttach([]string{pic})
	if len(x.a.ai.attached) != 0 {
		t.Fatal("a picture was attached for a model that cannot read it")
	}
	if x.a.aiDropped([]string{pic}) && len(x.a.ai.attached) != 0 {
		t.Fatal("dropped")
	}

	eyes, _ := x.a.ai.config.Choice("deepseek/eyes")
	if !eyes.ReadsPictures() {
		t.Fatal("the model should read pictures")
	}
	x.a.aiPick(eyes)
	x.a.aiAttach([]string{pic, pic, notes})
	if len(x.a.ai.attached) != 1 || x.a.ai.attached[0] != pic {
		t.Fatalf("attached %v", x.a.ai.attached)
	}
	x.frame(Input{})
	x.pump(10*time.Second, func() bool { p := x.a.ai.pics[pic]; return p != nil && !p.loading })
	x.frame(Input{})
	if p := x.a.ai.pics[pic]; p.img == nil || p.err != nil {
		t.Fatalf("the attached picture was not read: %+v", p)
	}
	x.shot("ai-attached")
	x.typeText("what is this?")
	x.key(keyEnter, false)
	x.pump(20*time.Second, x.answered)
	x.settle(5 * time.Second)

	if len(x.a.ai.attached) != 0 {
		t.Fatal("the pictures stayed in the composer after sending")
	}
	messages := svc.asked[len(svc.asked)-1]["messages"].([]any)
	parts, ok := messages[len(messages)-1].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 2 || parts[0].(map[string]any)["type"] != "image_url" || parts[1].(map[string]any)["text"] != "what is this?" {
		t.Fatalf("the model was sent %v", messages[len(messages)-1])
	}
	url := parts[0].(map[string]any)["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("picture url %.40s", url)
	}
	chat := x.a.ai.chat
	if got := chat.Messages[0].Attached; len(got) != 1 || got[0].Path != pic || got[0].Kind != ai.KindImage {
		t.Fatalf("the message keeps %+v", got)
	}
	x.shot("ai-vision-answer")

	// A picture sent alone, with no words, is a message too; one whose file is gone is reported, not sent.
	x.a.aiAttach([]string{pic})
	if err := os.Remove(pic); err != nil {
		t.Fatal(err)
	}
	asked := len(svc.asked)
	x.a.aiSend()
	x.pump(20*time.Second, x.answered)
	if len(svc.asked) != asked || x.a.ai.notice == "" {
		t.Fatalf("a missing picture: asked %d times, notice %q", len(svc.asked)-asked, x.a.ai.notice)
	}
}

func TestPicturesDroppedOnTheChatAreAttachedAndOthersOpenAsAlways(t *testing.T) {
	x := newHarness(t)
	svc := newOnlineChat(t)
	useOnlineVisionService(t, x, svc)
	eyes, _ := x.a.ai.config.Choice("deepseek/eyes")
	x.a.aiPick(eyes)
	x.a.openAITab()
	x.frame(Input{})
	pic := filepath.Join(t.TempDir(), "drop.png")
	os.WriteFile(pic, gradientPNG(t, 64, 40), 0o644)
	x.a.handleDrops([]string{pic})
	if len(x.a.ai.attached) != 1 {
		t.Fatalf("attached %v", x.a.ai.attached)
	}
	if x.a.aiDropped([]string{filepath.Join(t.TempDir(), "clip.mp4")}) {
		t.Fatal("a video was taken by the chat")
	}
}

// TestRealLocalImageModelInTheChat draws with a real sd-server and model (FORGE_SD_SERVER and FORGE_SD_MODEL, an SD 1.5
// GGUF); it is skipped otherwise, as both are large downloads. It runs on the processor, small and in few steps, so that
// it does not wait for a busy graphics card.
func TestRealLocalImageModelInTheChat(t *testing.T) {
	server, model := os.Getenv("FORGE_SD_SERVER"), os.Getenv("FORGE_SD_MODEL")
	if server == "" || model == "" {
		t.Skip("set FORGE_SD_SERVER and FORGE_SD_MODEL to run against a real image model")
	}
	x := newHarness(t)
	cfg := ai.DefaultConfig()
	for i := range cfg.Engines {
		if cfg.Engines[i].API == ai.APISD && cfg.Engines[i].ID == "sd-cpu" {
			cfg.Engines[i].Paths, cfg.Engines[i].URL, cfg.Engines[i].Executable = []string{server}, "", ""
		}
	}
	for i := range cfg.Models {
		if cfg.Models[i].ID == "sd15" {
			cfg.Models[i].ImageSize, cfg.Models[i].Steps = "256x256", 6
		}
	}
	x.a.setAIConfig(cfg)
	x.a.settings.AIEngine = "cpu"
	m, _ := cfg.Model("sd15")
	path, _ := x.a.ai.rt.Manager().ModelPath(m.Model)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(model, path); err != nil {
		t.Skipf("the model could not be linked into the cache: %v", err)
	}
	ch, _ := cfg.Choice("sd15")
	x.a.aiPick(ch)
	x.a.openAITab()
	x.frame(Input{})
	x.shot("ai-sd-unloaded")
	x.typeText("a red fox in the snow")
	x.key(keyEnter, false)
	x.pump(3*time.Minute, x.answered)
	x.pump(10*time.Second, func() bool {
		for _, p := range x.a.ai.pics {
			if p.loading {
				return false
			}
		}
		return len(x.a.ai.pics) > 0
	})
	x.settle(3 * time.Second)
	msgs := x.a.ai.chat.Messages
	if x.a.ai.notice != "" || len(msgs) != 2 || len(msgs[1].Media) != 1 {
		t.Fatalf("notice %q, messages %+v", x.a.ai.notice, msgs)
	}
	p := msgs[1].Media[0].Path
	img, err := loadImage(p)
	if err != nil || img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatalf("the picture %s: %v %v", p, err, img)
	}
	root, _ := x.a.aiAssetRoot()
	if filepath.Dir(p) != filepath.Join(root, "ai-image") {
		t.Fatalf("kept at %s", p)
	}
	x.shot("ai-sd-drawn")
	// The model stays in memory until stopped, and says so.
	if st := x.a.ai.rt.Status("sd15"); st.State != ai.Ready {
		t.Fatalf("the model is %v after drawing", st.State)
	}
	pid := x.a.ai.rt.Status("sd15").Pid
	for _, c := range x.a.aiRunning() {
		if c.id == "model/sd15" {
			c.cancel()
		}
	}
	x.pump(10*time.Second, func() bool { return !proc.Alive(pid) })
	if proc.Alive(pid) {
		t.Fatal("the image model's program runs after it was stopped")
	}
}
