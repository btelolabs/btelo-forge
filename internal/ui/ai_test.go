package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/proc"
	"github.com/btelolabs/btelo-forge/internal/vault"
)

func TestParseBlocksReadsMarkdownAsModelsWriteIt(t *testing.T) {
	text := "# Plan\n\nFirst **bold** and `code`.\n- one\n* two\n12. twelfth\n\n```go\nfunc main() {}\n\nprintln(1)\n```\nAfter\n```\nunclosed"
	got := parseBlocks(text)
	type want struct {
		kind   blockKind
		text   string
		marker string
		gap    bool
	}
	wants := []want{
		{blockHeading, "Plan", "", false},
		{blockText, "First **bold** and `code`.", "", true},
		{blockBullet, "one", "•", false},
		{blockBullet, "two", "•", false},
		{blockBullet, "twelfth", "12.", false},
		{blockCode, "func main() {}\n\nprintln(1)", "", true},
		{blockText, "After", "", false},
		{blockCode, "unclosed", "", false},
	}
	if len(got) != len(wants) {
		t.Fatalf("%d blocks: %+v", len(got), got)
	}
	for i, w := range wants {
		if g := got[i]; g.kind != w.kind || g.text != w.text || g.marker != w.marker || g.gap != w.gap {
			t.Errorf("block %d: %+v, want %+v", i, g, w)
		}
	}
	if got[0].level != 1 || got[5].lang != "go" {
		t.Errorf("level %d, language %q", got[0].level, got[5].lang)
	}
	for _, notHeading := range []string{"#hashtag", "####### seven", "# "} {
		if b := parseBlocks(notHeading); b[0].kind == blockHeading {
			t.Errorf("%q is a heading", notHeading)
		}
	}
}

func TestParseInlineKeepsUnclosedMarks(t *testing.T) {
	for in, want := range map[string][]richSpan{
		"a **b** c":  {{"a ", richPlain}, {"b", richBold}, {" c", richPlain}},
		"x `y` z":    {{"x ", richPlain}, {"y", richCode}, {" z", richPlain}},
		"2 ** 3":     {{"2 ** 3", richPlain}},
		"a `b":       {{"a `b", richPlain}},
		"**":         {{"**", richPlain}},
		"中文**加粗**结束": {{"中文", richPlain}, {"加粗", richBold}, {"结束", richPlain}},
	} {
		got := parseInline(in)
		if len(got) != len(want) {
			t.Errorf("%q: %+v", in, got)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q span %d: %+v, want %+v", in, i, got[i], want[i])
			}
		}
	}
}

func TestWrapRichBreaksWhereItShouldAndKeepsEveryCharacter(t *testing.T) {
	x := newHarness(t)
	u := &x.a.u
	face := x.a.richFace
	width := u.px(160)
	for _, text := range []string{
		"The quick brown fox jumps over the lazy dog and keeps running far away",
		"这是一段很长的中文文字，没有任何空格，必须能够在字与字之间换行显示出来。",
		"mixed 中英文 text with https://example.com/a/very/long/url/that/cannot/fit/in/one/line/at/all end",
		"",
	} {
		lines := wrapRich(parseInline(text), width, face)
		var joined strings.Builder
		for i, l := range lines {
			w := float32(0)
			for _, sp := range l.spans {
				joined.WriteString(sp.text)
				w += face(sp.style).Measure(sp.text)
			}
			if w > width+1 {
				t.Errorf("%q: line %d is %.0f wide, limit %.0f", text, i, w, width)
			}
			if i < len(lines)-1 {
				joined.WriteString(" ") // where a line was broken at a space, the space is dropped
			}
		}
		if text != "" && len(strings.Fields(strings.ReplaceAll(joined.String(), " ", ""))) != len(strings.Fields(strings.ReplaceAll(text, " ", ""))) {
			t.Errorf("%q lost characters: %q", text, joined.String())
		}
		if got := strings.ReplaceAll(joined.String(), " ", ""); got != strings.ReplaceAll(text, " ", "") {
			t.Errorf("text changed:\n got %q\nwant %q", got, strings.ReplaceAll(text, " ", ""))
		}
		if len(lines) == 0 {
			t.Errorf("%q gave no lines", text)
		}
	}
	if len(wrapRich(parseInline("一二三四五六七八九十一二三四五六七八九十"), width, face)) < 2 {
		t.Error("Chinese text was not broken")
	}
}

// onlineChat is a service that answers every chat with a fixed streamed answer, and keeps what it was asked.
type onlineChat struct {
	*httptest.Server
	asked []map[string]any
	auth  string
}

func newOnlineChat(t *testing.T, events ...string) *onlineChat {
	t.Helper()
	c := &onlineChat{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		c.asked = append(c.asked, body)
		c.auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			io.WriteString(w, "data: "+e+"\n\n")
			w.(http.Flusher).Flush()
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(c.Close)
	return c
}

// delta is a streamed piece of an answer.
func delta(field, text string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{field: text}}}})
	return string(b)
}

// useOnlineService points the DeepSeek entry of the model list at a test server and saves a key for it.
func useOnlineService(t *testing.T, x *harness, url string, key bool) {
	t.Helper()
	cfg := ai.DefaultConfig()
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "deepseek" {
			cfg.Providers[i].BaseURL = url
		}
	}
	x.a.setAIConfig(cfg)
	if key {
		if err := x.a.ai.keys.Set("deepseek", "sk-test-key"); err != nil {
			t.Fatal(err)
		}
	}
	ch, _ := cfg.Choice("deepseek/deepseek-v4-flash")
	x.a.aiPick(ch)
}

func (x *harness) answered() bool { return x.a.ai.answer == nil }

func TestChatWithAnOnlineModelStreamsRendersAndKeepsTheConversation(t *testing.T) {
	x := newHarness(t)
	answer := "## 步骤\n\n先看 **重点**，再运行 `go test`：\n\n- 第一项\n- 第二项\n\n```go\nfmt.Println(\"hi\")\n```\n"
	svc := newOnlineChat(t, delta("reasoning_content", "让我想想。"), delta("content", answer[:20]), delta("content", answer[20:]),
		`{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":31}}`)
	useOnlineService(t, x, svc.URL, true)

	x.frame(Input{})
	x.a.openAITab()
	x.frame(Input{})
	if !x.a.aiTab {
		t.Fatal("the chat tab did not open")
	}
	x.shot("ai-empty")
	x.typeText("你好，帮我写个计划")
	if x.a.ai.input != "你好，帮我写个计划" {
		t.Fatalf("input %q", x.a.ai.input)
	}
	x.key(keyEnter, false)
	if x.a.ai.answer == nil {
		t.Fatalf("Enter did not send; notice %q", x.a.ai.notice)
	}
	x.pump(20*time.Second, x.answered)
	x.settle(5 * time.Second)

	chat := x.a.ai.chat
	if chat == nil || len(chat.Messages) != 2 {
		t.Fatalf("conversation %+v", chat)
	}
	if m := chat.Messages[1]; m.Role != ai.Assistant || m.Text != answer || m.Reasoning != "让我想想。" || m.Stats == nil || m.Stats.CompletionTokens != 31 {
		t.Fatalf("answer %+v", m)
	}
	if svc.auth != "Bearer sk-test-key" || len(svc.asked) != 1 || svc.asked[0]["model"] != "deepseek-v4-flash" {
		t.Fatalf("request %q %v", svc.auth, svc.asked)
	}
	if x.a.ai.input != "" {
		t.Fatalf("the input was not emptied: %q", x.a.ai.input)
	}
	x.a.ai.opened[1] = true
	x.frame(Input{})
	x.shot("ai-answer")

	// The conversation was saved and is listed; a new chat starts empty and the old one opens again.
	list, err := x.a.ai.chats.List()
	if err != nil || len(list) != 1 || list[0].Title != "你好，帮我写个计划" {
		t.Fatalf("saved %+v %v", list, err)
	}
	id := chat.ID
	x.a.aiNewChat()
	x.frame(Input{})
	if x.a.ai.chat != nil {
		t.Fatal("a new chat has the old conversation")
	}
	x.a.aiOpenChat(id)
	x.frame(Input{})
	if x.a.ai.chat == nil || len(x.a.ai.chat.Messages) != 2 {
		t.Fatal("the conversation did not open again")
	}
	// The second turn carries the whole conversation, without the thinking.
	x.typeText("继续")
	x.key(keyEnter, false)
	x.pump(20*time.Second, x.answered)
	asked := svc.asked[1]["messages"].([]any)
	if len(asked) != 3 || asked[1].(map[string]any)["content"] != answer {
		t.Fatalf("second request %v", asked)
	}
	if strings.Contains(string(mustJSONBytes(asked)), "让我想想") {
		t.Fatal("the thinking was sent back")
	}
	// Deleting needs a confirmation.
	x.a.ai.deleting = id
	x.frame(Input{})
	x.shot("ai-delete")
	x.a.aiDeleteChat(id)
	if list, _ := x.a.ai.chats.List(); len(list) != 0 || x.a.ai.chat != nil {
		t.Fatalf("after deleting: %+v %v", list, x.a.ai.chat)
	}
}

func mustJSONBytes(v any) []byte { b, _ := json.Marshal(v); return b }

func TestShiftEnterMakesANewLineAndEnterSends(t *testing.T) {
	x := newHarness(t)
	svc := newOnlineChat(t, delta("content", "ok"))
	useOnlineService(t, x, svc.URL, true)
	x.a.openAITab()
	x.frame(Input{})
	x.typeText("a")
	x.frame(Input{Shift: true, Keys: []Key{{Code: keyEnter, Shift: true}}})
	x.typeText("b")
	if x.a.ai.input != "a\nb" || x.a.ai.answer != nil {
		t.Fatalf("input %q, answering %v", x.a.ai.input, x.a.ai.answer != nil)
	}
	x.key(keyEnter, false)
	x.pump(20*time.Second, x.answered)
	if got := x.a.ai.chat.Messages[0].Text; got != "a\nb" {
		t.Fatalf("sent %q", got)
	}
}

func TestChatWithoutAKeyOpensTheKeyPage(t *testing.T) {
	x := newHarness(t)
	svc := newOnlineChat(t, delta("content", "never"))
	useOnlineService(t, x, svc.URL, false)
	x.a.openAITab()
	x.frame(Input{})
	x.typeText("hi")
	x.key(keyEnter, false)
	if x.a.ai.answer != nil || len(svc.asked) != 0 {
		t.Fatal("a message was sent without a key")
	}
	if !x.a.settingsOpen || x.a.settingsPage != settingsAIPage {
		t.Fatalf("settings open %v on page %d", x.a.settingsOpen, x.a.settingsPage)
	}
	if x.a.ai.input != "hi" {
		t.Fatalf("the message was lost: %q", x.a.ai.input)
	}
	x.frame(Input{})
	x.shot("ai-settings-nokey")
}

func TestAnErrorFromTheServiceIsShownWithARetry(t *testing.T) {
	x := newHarness(t)
	failing := true
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":{"message":"Invalid API key"}}`)
			return
		}
		io.WriteString(w, "data: "+delta("content", "fine now")+"\n\ndata: [DONE]\n\n")
	}))
	defer svc.Close()
	useOnlineService(t, x, svc.URL, true)
	x.a.openAITab()
	x.frame(Input{})
	x.typeText("hello")
	x.key(keyEnter, false)
	x.pump(20*time.Second, x.answered)
	x.frame(Input{})
	if !strings.Contains(x.a.ai.notice, "API") {
		t.Fatalf("notice %q", x.a.ai.notice)
	}
	if n := len(x.a.ai.chat.Messages); n != 1 {
		t.Fatalf("%d messages", n)
	}
	x.shot("ai-error")
	failing = false
	x.a.ai.notice = ""
	x.a.aiAsk(x.a.aiChoice())
	x.pump(20*time.Second, x.answered)
	if m := x.a.ai.chat.Messages; len(m) != 2 || m[1].Text != "fine now" {
		t.Fatalf("after retry %+v", m)
	}
}

func TestStoppingKeepsWhatWasWritten(t *testing.T) {
	x := newHarness(t)
	started, release := make(chan struct{}), make(chan struct{})
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: "+delta("content", "half an ans")+"\n\n")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer svc.Close()
	defer close(release)
	useOnlineService(t, x, svc.URL, true)
	x.a.openAITab()
	x.frame(Input{})
	x.typeText("go")
	x.key(keyEnter, false)
	<-started
	x.pump(10*time.Second, func() bool {
		a := x.a.ai.answer
		if a == nil {
			return true
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.text != ""
	})
	x.shot("ai-streaming")
	x.a.aiStop()
	x.pump(10*time.Second, x.answered)
	m := x.a.ai.chat.Messages
	if len(m) != 2 || m[1].Text != "half an ans" || x.a.ai.notice != "" {
		t.Fatalf("messages %+v, notice %q", m, x.a.ai.notice)
	}
}

func TestKeysAreEnteredAndForgottenInSettings(t *testing.T) {
	x := newHarness(t)
	x.a.openSettingsPage(settingsAIPage)
	x.frame(Input{})
	x.shot("ai-settings")
	id := "settings/ai/key/grok"
	x.a.ai.keyFor = "grok"
	x.a.u.beginEdit(id+"/box", "", false, 0, 0)
	x.frame(Input{})
	x.typeText("xai-secret")
	if x.a.ai.keyText != "xai-secret" {
		t.Fatalf("typed %q", x.a.ai.keyText)
	}
	x.a.ai.page[0] = 1e9 // scrolled to the online services
	x.frame(Input{})
	x.shot("ai-settings-keyentry")
	x.key(keyEnter, false)
	if key, ok := x.a.aiKey("grok"); !ok || key != "xai-secret" {
		t.Fatalf("saved key %q %v", key, ok)
	}
	if x.a.ai.keyFor != "" || x.a.ai.keyText != "" {
		t.Fatal("the entry stayed open")
	}
	raw, _ := os.ReadFile(filepath.Join(x.a.stateDir, "ai-keys.json"))
	if sealed, _ := vault.Seal("x"); sealed != "x" && strings.Contains(string(raw), "xai-secret") {
		t.Fatalf("the key is written as it is: %s", raw)
	}
	x.frame(Input{})
	x.a.ai.page[0] = 1e9
	x.frame(Input{})
	x.shot("ai-settings-key")
	if err := x.a.ai.keys.Set("grok", ""); err != nil || x.a.ai.keys.Has("grok") {
		t.Fatalf("forgetting: %v", err)
	}
}

func TestAIModelListIsAConfigurationAndReloadsTheChoices(t *testing.T) {
	x := newHarness(t)
	k, err := findConfig("ai_models")
	if err != nil {
		t.Fatal(err)
	}
	if err := x.a.saveConfig(k, `{"version":1,"defaultModel":"p/only","providers":[{"id":"p","name":"P","baseUrl":"http://127.0.0.1:1/v1","models":[{"id":"only","name":"Only","model":"x"}]}]}`); err != nil {
		t.Fatal(err)
	}
	if got := x.a.aiChoice(); got.ID != "p/only" {
		t.Fatalf("choice %q", got.ID)
	}
	x.a.openAITab()
	x.frame(Input{})
	if err := x.a.saveConfig(k, `{"version":1,"defaultModel":"nope"}`); err == nil {
		t.Fatal("a configuration without its default model was accepted")
	}
	if err := x.a.resetConfig(k); err != nil {
		t.Fatal(err)
	}
	if x.a.ai.config.DefaultModel != "qwen3-4b" {
		t.Fatal("reset did not bring the built-in list back")
	}
}

func TestStartPageOffersTheAIModels(t *testing.T) {
	x := newHarness(t)
	x.frame(Input{})
	for _, c := range x.a.startCards() {
		if c.id == "empty/ai" {
			if !c.enabled {
				t.Fatal("the card is disabled")
			}
			c.run()
			x.frame(Input{})
			if !x.a.aiTab {
				t.Fatal("the card did not open the chat")
			}
			if title := x.a.doc.title(x.a); title != "Local models" {
				t.Fatalf("tab title %q", title)
			}
			x.a.openAITab()
			count := 0
			for _, d := range x.a.docs {
				if d.aiTab {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("%d chat tabs", count)
			}
			return
		}
	}
	t.Fatal("no card for the AI models")
}

func TestTheModelsHaveAGroupOfTheirOwnOnTheStartPage(t *testing.T) {
	x := newHarness(t)
	for _, c := range x.a.startCards() {
		if c.id == "empty/ai" {
			if got := startGroups[c.group]; got != "Intelligence" {
				t.Fatalf("the card is in %q", got)
			}
			return
		}
	}
	t.Fatal("no card for the AI models")
}

// TestComposerEdgesMeet checks that the send button is as tall as a one-line input and that both share their bottom edge
// whatever the number of lines, at several scales.
func TestComposerEdgesMeet(t *testing.T) {
	x := newHarness(t)
	for _, scale := range []float32{1, 1.25, 1.5, 2} {
		x.a.u.s = scale
		for _, pictures := range []int{0, 2} {
			x.a.ai.attached = make([]string, pictures)
			for _, input := range []string{"", "one", "one\ntwo", "1\n2\n3\n4\n5\n6\n7\n8\n9"} {
				x.a.ai.input = input
				r := rectAt(100, 500, 800, x.a.aiComposerHeight(800))
				chips, attach, box, btn := x.a.aiComposerRects(r)
				where := fmt.Sprintf("scale %v, %d pictures, %q", scale, pictures, input)
				if box.Bottom() != btn.Bottom() || box.Bottom() != attach.Bottom() || box.Bottom() != r.Bottom() {
					t.Errorf("%s: input ends at %v, buttons at %v and %v", where, box.Bottom(), attach.Bottom(), btn.Bottom())
				}
				if !strings.Contains(input, "\n") && (box.Y != btn.Y || box.H != btn.H || box.Y != attach.Y || box.H != attach.H) {
					t.Errorf("%s: input is %+v, buttons %+v and %+v", where, box, attach, btn)
				}
				if attach.X != r.X || btn.Right() != r.Right() || box.X <= attach.Right() || box.Right() >= btn.X {
					t.Errorf("%s: %+v, %+v and %+v are not side by side in %+v", where, attach, box, btn, r)
				}
				if (pictures == 0) != (chips.H == 0) || chips.Bottom() > box.Y || chips.Y != r.Y {
					t.Errorf("%s: the pictures %+v are not above the input %+v", where, chips, box)
				}
			}
		}
	}
	x.a.ai.attached = nil
}

func TestNarrowWindowHidesTheSidebar(t *testing.T) {
	x := newHarness(t)
	x.a.openAITab()
	x.c = gfx.NewCanvas(520, 700)
	x.frame(Input{})
	x.shot("ai-narrow")
}

// TestRealLocalModelInTheChat drives the chat with a real llama-server and model (FORGE_LLAMA_SERVER and
// FORGE_LLAMA_MODEL, as for the ai package's test); it is skipped otherwise, as both are large downloads.
func TestRealLocalModelInTheChat(t *testing.T) {
	server, model := os.Getenv("FORGE_LLAMA_SERVER"), os.Getenv("FORGE_LLAMA_MODEL")
	if server == "" || model == "" {
		t.Skip("set FORGE_LLAMA_SERVER and FORGE_LLAMA_MODEL to run against a real model")
	}
	x := newHarness(t)
	cfg := ai.DefaultConfig()
	cfg.Engines[0].Paths, cfg.Engines[0].URL, cfg.Engines[0].Executable = []string{server}, "", ""
	x.a.setAIConfig(cfg)
	m := cfg.Models[0]
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
	x.shot("ai-local-unloaded")

	// Sending a message loads the model first.
	x.typeText("用一句话回答：2+3等于几？")
	x.key(keyEnter, false)
	x.pump(2*time.Second, func() bool {
		return x.a.ai.rt.Status(m.ID).State == ai.Loading || x.a.ai.rt.Status(m.ID).State == ai.Ready
	})
	x.shot("ai-local-loading")
	x.pump(3*time.Minute, x.answered)
	if n := len(x.a.ai.chat.Messages); n != 2 || !strings.Contains(x.a.ai.chat.Messages[1].Text, "5") {
		t.Fatalf("messages %+v, notice %q", x.a.ai.chat.Messages, x.a.ai.notice)
	}
	// The cost shows once it has been measured.
	x.pump(30*time.Second, func() bool { st := x.a.ai.rt.Status(m.ID); return st.Measured && st.Usage.GPUMemory > 0 })
	x.settle(2 * time.Second)
	x.a.ai.opened[1] = false
	x.frame(Input{})
	x.shot("ai-local-loaded")
	x.a.ai.picking = true
	x.frame(Input{})
	x.shot("ai-local-picker")
	x.a.ai.picking = false
	x.a.openSettingsPage(settingsAIPage)
	x.a.ai.page[0] = 0
	x.frame(Input{})
	x.shot("ai-local-settings")

	// While it runs it is a card in the corner, whichever page is in front, and the card stops it.
	x.a.settingsOpen = false
	x.frame(Input{})
	var card *task
	for _, c := range x.a.aiRunning() {
		if c.id == "model/"+m.ID {
			card = &c
		}
	}
	if card == nil || !card.running || card.title != m.Name.In("en") || !strings.HasPrefix(card.status, "Running") {
		t.Fatalf("the running model is not listed: %+v", x.a.aiRunning())
	}
	x.shot("ai-local-running")
	pid := x.a.ai.rt.Status(m.ID).Pid
	card.cancel()
	x.pump(10*time.Second, func() bool { return x.a.ai.rt.Status(m.ID).State == ai.Unloaded })
	x.frame(Input{})
	if len(x.a.aiRunning()) != 0 {
		t.Fatalf("it is still listed after the model stopped: %+v", x.a.aiRunning())
	}
	x.pump(10*time.Second, func() bool { return !proc.Alive(pid) })
	if proc.Alive(pid) {
		t.Fatalf("the model's program %d still runs after it was stopped", pid)
	}
}

func TestAIScreensInChinese(t *testing.T) {
	x := newHarness(t)
	x.a.u.lang = Chinese
	cfg := ai.DefaultConfig()
	x.a.setAIConfig(cfg)
	ch, _ := cfg.Choice("qwen3-8b")
	x.a.aiPick(ch)
	x.a.openAITab()
	x.frame(Input{})
	x.shot("ai-zh-chat")
	x.a.ai.picking = true
	x.frame(Input{})
	x.shot("ai-zh-picker")
	x.a.ai.scroll[aiScrollModels] = 1e9 // the end of the list: the models of other kinds
	x.frame(Input{})
	x.shot("ai-zh-picker-end")
	x.a.ai.picking = false
	x.a.openSettingsPage(settingsAIPage)
	x.frame(Input{})
	x.shot("ai-zh-settings")
}
