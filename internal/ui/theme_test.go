package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mail"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
	"github.com/btelolabs/btelo-forge/internal/scene3d"
)

type fakeThemeHost struct {
	*fakeHost
	sysDark bool
	calls   []bool
}

func (h *fakeThemeHost) SystemDark() bool  { return h.sysDark }
func (h *fakeThemeHost) SetDark(dark bool) { h.calls = append(h.calls, dark) }

func TestThemeDefaultAndPersistence(t *testing.T) {
	t.Cleanup(func() { applyTheme(true) })

	dir := t.TempDir()
	h := &fakeThemeHost{fakeHost: &fakeHost{}, sysDark: false}
	a := &App{host: h, stateDir: dir}

	// 1. Fresh install defaults to ThemeSystem.
	a.loadSettings()
	if a.settings.Theme != ThemeSystem {
		t.Fatalf("fresh install theme = %q, want %q", a.settings.Theme, ThemeSystem)
	}

	// 2. Existing settings.json without "theme" also defaults to ThemeSystem.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"language":"en"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadSettings()
	if a.settings.Theme != ThemeSystem {
		t.Fatalf("settings without theme field = %q, want %q", a.settings.Theme, ThemeSystem)
	}

	// 3. Invalid theme value falls back to ThemeSystem.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"language":"en","theme":"neon"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadSettings()
	if a.settings.Theme != ThemeSystem {
		t.Fatalf("invalid theme = %q, want %q", a.settings.Theme, ThemeSystem)
	}

	// 4. Persistence round-trip for explicit light, dark and system choices.
	for _, want := range []Theme{ThemeLight, ThemeDark, ThemeSystem} {
		a.setTheme(want)
		b := &App{host: h, stateDir: dir}
		b.loadSettings()
		if b.settings.Theme != want {
			t.Fatalf("round-trip theme = %q, want %q", b.settings.Theme, want)
		}
	}
}

func TestThemeEffectiveDarkAndLiveSync(t *testing.T) {
	t.Cleanup(func() { applyTheme(true) })

	dir := t.TempDir()
	h := &fakeThemeHost{fakeHost: &fakeHost{}, sysDark: false}
	a := &App{host: h, stateDir: dir}
	a.loadSettings()

	// ThemeSystem follows OS: currently light.
	if a.effectiveDark() {
		t.Fatal("ThemeSystem with sysDark=false: effectiveDark() = true, want false")
	}
	a.syncTheme()
	if isDarkTheme() {
		t.Fatal("after syncTheme (light): isDarkTheme() = true, want false")
	}
	if colBackground != lightPalette.background {
		t.Fatalf("colBackground = %v, want light %v", colBackground, lightPalette.background)
	}
	if len(h.calls) != 1 || h.calls[0] != false {
		t.Fatalf("SetDark calls = %v, want [false]", h.calls)
	}

	// Repeated syncTheme without change does not re-notify host.
	a.syncTheme()
	if len(h.calls) != 1 {
		t.Fatalf("redundant syncTheme called SetDark again: %v", h.calls)
	}

	// Live OS theme change to dark updates palette and host immediately.
	h.sysDark = true
	if !a.effectiveDark() {
		t.Fatal("ThemeSystem with sysDark=true: effectiveDark() = false, want true")
	}
	a.syncTheme()
	if !isDarkTheme() || colBackground != darkPalette.background {
		t.Fatalf("after OS switch to dark: isDarkTheme=%v, colBackground=%v", isDarkTheme(), colBackground)
	}
	if len(h.calls) != 2 || h.calls[1] != true {
		t.Fatalf("SetDark calls after live OS switch = %v, want [false true]", h.calls)
	}

	// Explicit ThemeLight overrides dark OS.
	a.setTheme(ThemeLight)
	if a.effectiveDark() || isDarkTheme() {
		t.Fatal("ThemeLight with sysDark=true should stay light")
	}
	if len(h.calls) != 3 || h.calls[2] != false {
		t.Fatalf("SetDark calls after ThemeLight = %v, want [false true false]", h.calls)
	}

	// OS changing to light then dark while ThemeLight is chosen causes no change.
	h.sysDark = false
	a.syncTheme()
	h.sysDark = true
	a.syncTheme()
	if len(h.calls) != 3 || isDarkTheme() {
		t.Fatalf("OS changes should not affect explicit ThemeLight: calls=%v isDark=%v", h.calls, isDarkTheme())
	}

	// Explicit ThemeDark overrides light OS.
	h.sysDark = false
	a.setTheme(ThemeDark)
	if !a.effectiveDark() || !isDarkTheme() {
		t.Fatal("ThemeDark with sysDark=false should stay dark")
	}
	if len(h.calls) != 4 || h.calls[3] != true {
		t.Fatalf("SetDark calls after ThemeDark = %v, want [false true false true]", h.calls)
	}

	// Switching back to ThemeSystem picks up the current light OS immediately.
	a.setTheme(ThemeSystem)
	if a.effectiveDark() || isDarkTheme() {
		t.Fatal("ThemeSystem with sysDark=false should immediately return to light")
	}
	if len(h.calls) != 5 || h.calls[4] != false {
		t.Fatalf("SetDark calls after returning to ThemeSystem = %v, want [false true false true false]", h.calls)
	}

	// Host without themeHost falls back to dark mode.
	plainApp := &App{host: &fakeHost{}, stateDir: dir}
	plainApp.settings = defaultSettings()
	if !plainApp.effectiveDark() {
		t.Fatal("host without themeHost should default to dark")
	}
}

// TestThemeShots renders full-window (1440×900) Light and Dark screenshots across key screens into FORGE_SHOTS.
func TestThemeShots(t *testing.T) {
	if os.Getenv("FORGE_SHOTS") == "" {
		t.Skip("set FORGE_SHOTS to a folder to render theme screenshots")
	}
	t.Cleanup(func() { applyTheme(true) })

	// 1. Start page & Settings dialogs (Light & Dark, en & zh).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.settings.MCP = true
		x.a.applyServices()
		for _, tc := range []struct {
			name  string
			theme Theme
		}{
			{"light", ThemeLight},
			{"dark", ThemeDark},
		} {
			x.a.setTheme(tc.theme)
			for _, lang := range []Language{English, Chinese} {
				x.a.u.lang = lang
				x.a.settingsOpen = false
				x.frame(Input{})
				x.frame(Input{})
				x.shot(tc.name + "-" + string(lang) + "-start")

				x.a.settingsOpen = true
				x.a.settingsPage = settingsGeneralPage
				x.frame(Input{})
				x.shot(tc.name + "-" + string(lang) + "-settings-general")
			}
		}
		// Settings -> MCP, Phone, Hotkeys, File types, AI models, Extensions, About (Light, en)
		x.a.setTheme(ThemeLight)
		x.a.u.lang = English
		x.a.settings.Phone = true
		x.a.applyServices()
		x.a.settingsOpen = true
		for _, sp := range []struct {
			name string
			page int
		}{
			{"mcp", settingsMCPPage},
			{"phone", settingsPhonePage},
			{"hotkeys", settingsHotkeysPage},
			{"filetypes", settingsFileTypesPage},
			{"ai", settingsAIPage},
			{"extensions", settingsExtensionsPage},
			{"about", settingsAboutPage},
		} {
			x.a.settingsPage = sp.page
			x.frame(Input{})
			x.frame(Input{})
			x.shot("light-en-settings-" + sp.name)
		}
		x.a.settingsPage = settingsHotkeysPage
		x.a.keysDomain = domainGeneral
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-settings-hotkeys-general")
		x.a.settingsOpen = false
	}

	// 2. Video Editor with timeline & side panel (Light & Dark).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.open(x.fixture())
		x.a.seek(2)
		x.a.addText()
		x.a.tab = 1 // Overlays panel with text controls
		x.settle(10 * time.Second)
		for _, tc := range []struct {
			name  string
			theme Theme
		}{
			{"light", ThemeLight},
			{"dark", ThemeDark},
		} {
			x.a.setTheme(tc.theme)
			x.frame(Input{})
			x.frame(Input{})
			x.shot(tc.name + "-en-video-editor")
		}
	}

	// 3. File Manager (Light).
	{
		x, _ := filesHarness(t, map[string]string{
			"Design/":           "",
			"Exports/":          "",
			"README.md":         "# Btelo Forge\n\nLight mode visual verification.\n",
			"main.go":           "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n",
			"config.json":       "{\n  \"theme\": \"light\",\n  \"language\": \"en\"\n}\n",
			"notes.txt":         "Release checklist for Light Mode.\n",
			"archive.zip":       "PK\x05\x06",
			"presentation.pptx": "fake-pptx",
			"report.pdf":        "%PDF-1.4\n",
			"photo.png":         "png-bytes",
			"clip.mp4":          "mp4-bytes",
		})
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-files")
	}

	// 4. Art Canvas (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		x.frame(Input{})
		x.click(x.startCard("empty/canvas"))
		x.settle(5 * time.Second)
		x.frame(Input{})
		x.shot("light-en-art-canvas")
	}

	// 5. AI Chat (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		answer := "## Light Mode Review\n\nCheck **contrast** and `surface` hierarchy:\n\n- Crisp hairline borders\n- Calm neutral chrome\n\n```go\nfunc applyTheme(dark bool) {\n\tdarkTheme = dark\n}\n```\n"
		svc := newOnlineChat(t,
			delta("reasoning_content", "Checking palette tokens and border contrast for light mode."),
			delta("content", answer[:24]),
			delta("content", answer[24:]),
			`{"choices":[],"usage":{"prompt_tokens":18,"completion_tokens":42}}`)
		useOnlineService(t, x, svc.URL, true)
		x.frame(Input{})
		x.a.openAITab()
		x.frame(Input{})
		x.typeText("Summarize the Light Mode design tokens")
		x.key(keyEnter, false)
		x.pump(20*time.Second, x.answered)
		x.settle(5 * time.Second)
		x.a.ai.opened[1] = true
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-ai-chat")
	}

	// 6. Office Word + Print preview dialog (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		_ = wordOpen(t, x)
		x.tapKey('B', true, false)
		x.typeIn("Quarterly Design Review")
		x.tapKey('B', true, false)
		x.tapKey(keyEnter, false, false)
		x.typeIn("Unified Light Mode keeps UI chrome calm and crisp while document paper stays clean white.")
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-office-word")

		x.key('P', true)
		x.pump(10*time.Second, func() bool {
			return x.a.print != nil && x.a.print.preview != nil
		})
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-print")
	}

	// 7. Office Spreadsheet (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		ed := newSheetTab(t, x)
		demoSheet(t, x, ed)
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-office-sheet")
	}

	// 8. Office Slides (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		e := slidesDoc(t, x)
		s := e.slide()
		if shapes := e.d.Shapes(s); len(shapes) > 0 {
			mx, my := slideMid(e, shapes[0])
			x.click(gfx.Rect{X: mx, Y: my})
			x.typeWords("Btelo Forge Light Mode")
			x.frame(Input{Keys: []Key{{Code: keyEscape}}})
		}
		_, _ = e.d.AddShape(s, "rect", pptx.Box{X: 120, Y: 360, W: 280, H: 140})
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-office-slides")
	}

	// 9. Office PDF (Light).
	{
		x, e := pdfHarness(t, 3)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		if text := e.textOf(0); text != nil {
			if ms := text.Search("quick brown fox"); len(ms) > 0 {
				_, _ = e.doc.AddAnnot(0, pdf.AnnotSpec{Kind: "Highlight", Quads: text.Quads(ms[0].From, ms[0].To), Color: [3]float64{1, 0.9, 0.2}})
			}
		}
		_, _ = e.doc.AddAnnot(0, pdf.AnnotSpec{Kind: "Square", Rect: pdf.Rect{X0: 80, Y0: 300, X1: 240, Y1: 380}, Color: [3]float64{0.2, 0.45, 0.9}, Width: 2})
		e.touched(0)
		e.rev++
		settlePDF(x, e)
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-office-pdf")
	}

	// 10. Empty Video Editor (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		dir := x.mediaDir()
		if err := os.MkdirAll(filepath.Join(dir, "Holiday"), 0o755); err != nil {
			t.Fatal(err)
		}
		clipFile(t, dir, "intro.mp4", "testsrc2=size=640x360", "2")
		x.a.settings.Files.VideoDir = dir
		x.a.newEditor()
		x.pump(10*time.Second, func() bool {
			x.frame(Input{})
			return len(x.a.videoListPane().rows) == 2
		})
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-editor-empty")
	}

	// 11. Video Player tab (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		path := clipFile(t, x.mediaDir(), "watch.mp4", "testsrc2=size=640x360", "6")
		m := x.playing(path)
		x.pump(10*time.Second, func() bool { return m.position() > 0.4 })
		x.key(keySpace, false)
		x.mx, x.my = 720, 820
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-movie")
	}

	// 12. Mind Map tab + Theme panel (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		if err := x.a.newMindMap(); err != nil {
			t.Fatal(err)
		}
		md := x.a.board
		x.settleMind()
		x.press(keyTab, "")
		x.typeText("Goals")
		x.press(keyEnter, "")
		x.press(keyEnter, "")
		x.typeText("Risks")
		x.press(keyEnter, "")
		x.settleMind()
		x.frame(Input{})
		x.shot("light-en-mind")

		md.themes = true
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-mind-themes")
	}

	// 13. 3D Assets tab: empty state & cube with panels (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		x.a.openModelTab()
		settleFrames(t, x, "model failed notice", func() bool { return x.a.models.failed != "" })
		x.frame(Input{})
		x.shot("light-en-model-empty")

		x.a.addPrimitive(x.a.model, scene3d.Cube)
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-model-cube")
	}

	// 14. Mail tab: setup, inbox/reader, compose (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		x.a.settings.MailEvery = -1
		x.a.openMailTab()
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-mail-setup")
	}
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		bindTwo(t, x)
		s := &x.a.mail
		id := s.list[0].ID
		_ = s.mailer.Store.SaveOrganised(id, "m", mail.Verdict{Summary: "A short summary of the message", Tier: mail.TierMust, Why: "they wait for an answer"})
		s.view, s.viewChosen, s.stale, s.current = mailAll, true, true, id
		x.pump(5*time.Second, func() bool { return !s.stale })
		x.settle(200 * time.Millisecond)
		x.frame(Input{})
		x.shot("light-en-mail-inbox")

		x.a.mailWriteNew()
		s.letter.to = "ann@a.com"
		s.letter.subject = "Friday dinner"
		s.letter.text = "Sounds great — see you at 7pm!"
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-mail-compose")
	}

	// 15. Keyboard Remapper tab (Light).
	{
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		x.a.openKeyboardTab()
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-keyboard-tab")
	}

	// 16. Go Live tab (Light).
	if capture.Supported() {
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		x.a.settings.Live.Destinations = []liveDestination{
			{ID: "d1", Platform: "bilibili", Name: "Bilibili Main", Server: "rtmp://live-push.bilivideo.com/live-bvc/", Key: sealedText("secret"), On: true},
			{ID: "d2", Platform: "youtube", Name: "YouTube", Server: "rtmp://a.rtmp.youtube.com/live2", Key: sealedText("secret2"), On: false},
		}
		x.a.openLive()
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-live-tab")
	}

	// 17. Screen Recording dialog (Light).
	if capture.Supported() {
		x := newHarness(t)
		x.c.Resize(1440, 900)
		x.a.setTheme(ThemeLight)
		x.a.openRecorder()
		x.pump(15*time.Second, func() bool { return x.a.rec.listed })
		x.frame(Input{})
		x.frame(Input{})
		x.shot("light-en-record-dialog")
	}
}
