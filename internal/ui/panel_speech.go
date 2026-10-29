package ui

import (
	"context"
	"errors"
	"image/color"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/speech"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// speechDescribed is the voice choice of a voice the user describes in words.
const speechDescribed = "described"

// speechIdle is how long the speech engine keeps its model in memory after the last piece of speech.
const speechIdle = 3 * time.Minute

// speechPanel is the state of the voice-over tab and its running job.
type speechPanel struct {
	config   *speech.Config
	manager  *transcribe.Manager
	server   *speech.Server
	dir      string // where the speech made is kept; projects refer to it
	engines  string // a folder for each engine's own files
	text     string
	job      *speechJob
	lastErr  string
	lastDone int
}

type speechJob struct {
	stop     context.CancelFunc
	doc      *doc
	stage    string
	fraction float64
	lines    int
	stopping bool
}

// speechLine is a piece of speech to make and where it goes on the timeline.
type speechLine struct {
	text string
	at   float64
}

func (s *speechPanel) init(cacheDir, stateDir string) {
	s.config = speech.DefaultConfig()
	s.manager = transcribe.NewManager(filepath.Join(cacheDir, "speech"))
	s.server = speech.NewServer(speechIdle)
	s.dir = filepath.Join(stateDir, "speech")
	s.engines = filepath.Join(cacheDir, "speech", "run")
}

func (s *speechPanel) busy() bool { return s.job != nil }

func (s *speechPanel) cancel() {
	if s.job != nil {
		s.job.stopping = true
		s.job.stop()
	}
}

// speechStage says what the job is doing and how far it has got; how long loading the model takes is not known.
func (a *App) speechStage(j *speechJob) (string, float64) {
	u := &a.u
	switch j.stage {
	case "engine":
		return u.t("Downloading the voice-over engine"), j.fraction
	case "model":
		return u.t("Downloading the model"), j.fraction
	case "load":
		return u.t("Loading the model into memory"), -1
	}
	if j.lines > 1 {
		return u.tf("Speaking %d of %d", min(int(j.fraction*float64(j.lines))+1, j.lines), j.lines), j.fraction
	}
	return u.t("Speaking"), j.fraction
}

// close ends the job and the engine, giving its memory back.
func (s *speechPanel) close() {
	s.cancel()
	s.server.Close()
}

func (a *App) speechModel() speech.Model {
	c := a.speech.config
	if m, ok := c.Model(a.settings.SpeechModel); ok {
		return m
	}
	m, _ := c.Model(c.DefaultModel)
	return m
}

// speechVoiceID is the chosen voice: a configured voice's id or speechDescribed.
func (a *App) speechVoiceID() string {
	c := a.speech.config
	if id := a.settings.SpeechVoice; id == speechDescribed {
		return id
	} else if _, ok := c.Voice(id); ok {
		return id
	}
	return c.DefaultVoice
}

// speechVoice is the voice that speaks: the chosen one, or the one described when there is a description.
func (a *App) speechVoice() speech.Voice {
	c := a.speech.config
	id := a.speechVoiceID()
	if id == speechDescribed {
		if d := strings.TrimSpace(a.settings.SpeechDescription); d != "" {
			return speech.Voice{ID: speechDescribed, Instruction: d}
		}
		id = c.DefaultVoice
	}
	v, _ := c.Voice(id)
	return v
}

// speechEngine is the chosen engine that runs the chosen model, or the first one that does on this computer; it is
// false when there is none.
func (a *App) speechEngine() (speech.Engine, bool) {
	return a.speech.config.Engine(a.speechModel(), a.settings.SpeechEngine)
}

// captionLines are the subtitles a voice-over can read: the text on the track of the selected text, or else on the
// last track with text, which is where generated subtitles go.
func (a *App) captionLines() []speechLine {
	if a.p == nil {
		return nil
	}
	lines := func(t project.OverlayTrack) []speechLine {
		var out []speechLine
		for _, o := range t.Items {
			if text := strings.TrimSpace(o.Body); o.Kind == project.TextOverlay && text != "" {
				out = append(out, speechLine{text: text, at: o.Start})
			}
		}
		return out
	}
	if a.sel.kind == selOverlay {
		if k, _, ok := a.p.FindOverlay(a.sel.id); ok {
			if l := lines(a.p.Overlays[k]); len(l) > 0 {
				return l
			}
		}
	}
	for k := len(a.p.Overlays) - 1; k >= 0; k-- {
		if l := lines(a.p.Overlays[k]); len(l) > 0 {
			return l
		}
	}
	return nil
}

// drawSpeechPanel draws the voice-over tab and returns the bottom of its content.
func (a *App) drawSpeechPanel(r gfx.Rect) float32 {
	u := &a.u
	s := &a.speech
	lang := string(u.lang)
	y := r.Y
	f := u.face(12)
	wrap := func(text string, face *gfx.Face, col color.RGBA) {
		for _, l := range face.Wrap(text, r.W) {
			u.c.Text(face, r.X, y, l, col)
			y += face.Height() * 1.3
		}
	}
	wrap(u.t("Turns text into natural speech on this computer with a local AI model and puts it on the timeline at the playhead. Qwen3-TTS may be used commercially; the engine and model are downloaded once."), f, colDim)
	y += u.px(10)
	u.textBox("speech/text", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(104)}, &s.text, u.t("Type what should be said; Enter starts a new line"), true)
	y += u.px(114)
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Speaker"))
	y += u.px(26)
	chosen := a.speechVoiceID()
	names, current := make([]string, 0, len(s.config.Voices)+1), len(s.config.Voices)
	for i, v := range s.config.Voices {
		names = append(names, v.Name.In(lang))
		if v.ID == chosen {
			current = i
		}
	}
	names = append(names, u.t("Describe…"))
	pick, h := u.flowChips("speech/voice", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(28)}, names, current)
	if pick != current {
		if pick == len(s.config.Voices) {
			a.settings.SpeechVoice = speechDescribed
		} else {
			a.settings.SpeechVoice = s.config.Voices[pick].ID
		}
		a.saveSettings()
	}
	y += h + u.px(6)
	if chosen == speechDescribed {
		description := a.settings.SpeechDescription
		if _, done := u.textBox("speech/describe", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(34)}, &description, u.t("Describe the voice in English, such as: An old man with a slow, husky voice"), false); description != a.settings.SpeechDescription || done {
			a.settings.SpeechDescription = description
			if done {
				a.saveSettings()
			}
		}
		y += u.px(40)
	}
	y += u.px(6)
	if s.job != nil {
		// Its progress shows in the corner of the window (drawTasks), whichever page is in front.
		u.button("speech/go", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(40)}, icVoice, u.t("Generating speech…"), primary, false)
		y += u.px(54)
	} else {
		text := strings.TrimSpace(s.text)
		if u.button("speech/go", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(40)}, icVoice, u.t("Generate speech"), primary, text != "") {
			if err := a.generateSpeech([]speechLine{{text: text, at: a.playhead}}, true); err != nil {
				a.toast(a.describe(err), true)
			}
		}
		u.hint("speech/go", u.t("Adds the speech at the playhead"), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(40)})
		y += u.px(48)
		if lines := a.captionLines(); len(lines) > 0 {
			br := gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(36)}
			if u.button("speech/captions", br, icSubtitle, u.tn(len(lines), "Read %d subtitle aloud", "Read the %d subtitles aloud", len(lines)), plain, true) {
				if err := a.generateSpeech(lines, false); err != nil {
					a.toast(a.describe(err), true)
				}
			}
			u.hint("speech/captions", u.t("Each line is spoken where it starts; select a subtitle to read its track"), br)
			y += u.px(44)
		}
		y += u.px(6)
	}
	if s.lastErr != "" {
		wrap(s.lastErr, f, colDangerText)
	}
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Model"))
	y += u.px(26)
	// The models are listed under the heading of their series, which says what they may be used for.
	modelPick := ""
	for start := 0; start < len(s.config.Models); {
		series := s.config.Models[start].Series.In(lang)
		var models []transcribe.Model
		for _, m := range s.config.Models[start:] {
			if m.Series.In(lang) != series {
				break
			}
			models = append(models, m.Model)
		}
		if series != "" {
			u.label(u.face(11.5), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(20)}, series, colDim, 0)
			y += u.px(22)
		}
		pick, bottom := a.drawModelRows("speech/model/", gfx.Rect{X: r.X, Y: y, W: r.W}, models, a.speechModel().ID, s.manager)
		if pick != "" {
			modelPick = pick
		}
		y = bottom + u.px(6)
		start += len(models)
	}
	if modelPick != "" && s.job == nil && modelPick != a.speechModel().ID {
		a.settings.SpeechModel = modelPick
		a.saveSettings()
		s.server.Stop() // give back the memory of the old model now
	}
	y += u.px(6)
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Run on"))
	y += u.px(26)
	engine, ok := a.speechEngine()
	if !ok {
		wrap(u.t("No engine in the voice-over voices configuration runs on this computer; add one in Settings → Extensions"), f, colDim)
	} else {
		var engines []transcribe.Engine
		for _, e := range s.config.Available(a.speechModel()) {
			engines = append(engines, e.Engine)
		}
		enginePick, bottom := a.drawEngineChoice("speech/engine", gfx.Rect{X: r.X, Y: y, W: r.W}, engines, engine.Engine)
		y = bottom
		if enginePick != "" && s.job == nil {
			a.settings.SpeechEngine = enginePick
			a.saveSettings()
			s.server.Stop()
		}
	}
	y += u.px(10)
	wrap(a.speechModel().License.In(lang), u.face(11), colFaint)
	return y + u.px(8)
}

// generateSpeech makes each line into speech in the background and puts it on the timeline where the line goes,
// as one change; listen plays a single result as soon as it is placed. Lines made before are not made again.
func (a *App) generateSpeech(lines []speechLine, listen bool) error {
	s := &a.speech
	if a.p == nil {
		return errNoProject
	}
	if s.job != nil {
		return errors.New(a.u.t("Speech is already being generated"))
	}
	lines = slices.DeleteFunc(slices.Clone(lines), func(l speechLine) bool { return strings.TrimSpace(l.text) == "" })
	if len(lines) == 0 {
		return errors.New(a.u.t("Type what should be said"))
	}
	engine, ok := a.speechEngine()
	if !ok {
		return errors.New(a.u.t("No engine in the voice-over voices configuration runs on this computer; add one in Settings → Extensions"))
	}
	model, voice := a.speechModel(), a.speechVoice()
	ctx, stop := context.WithCancel(a.life)
	stage := "engine" // the first step still to do, until Prepare and Speak report their own
	if _, ok := s.manager.EnginePath(engine.Engine); ok {
		stage = "model"
		if _, ok := s.manager.ModelPath(model.Model); ok {
			stage = "load"
			if s.server.Running() {
				stage = "speak"
			}
		}
	}
	job := &speechJob{stop: stop, doc: a.doc, stage: stage, lines: len(lines)}
	s.job, s.lastErr, s.lastDone = job, "", 0
	type made struct {
		path    string
		title   string
		seconds float64
		at      float64
	}
	a.background(func(_ context.Context) {
		var done []made
		report := func(stage string, f float64) { a.post(func() { job.stage, job.fraction = stage, f }) }
		cli, modelPath, err := s.manager.Prepare(ctx, engine.Engine, model.Model, report)
		launch := speech.Launch{Engine: engine, Program: cli, Model: model, ModelPath: modelPath, Dir: filepath.Join(s.engines, engine.ID)}
		for i, l := range lines {
			if err != nil {
				break
			}
			req := speech.Request{Text: l.text, Voice: voice}
			path := filepath.Join(s.dir, speech.FileName(model, req))
			seconds, ok := speech.Length(path)
			if !ok {
				seconds, err = s.server.Speak(ctx, launch, req, path, func(stage string, f float64) {
					if stage == "speak" {
						f = (float64(i) + f) / float64(len(lines))
					}
					report(stage, f)
				})
			}
			if err == nil {
				done = append(done, made{path: path, title: speechTitle(l.text), seconds: seconds, at: l.at})
				report("speak", float64(i+1)/float64(len(lines)))
			}
		}
		a.post(func() {
			s.job = nil
			stop()
			switch {
			case errors.Is(err, context.Canceled):
			case err != nil:
				s.lastErr = a.describe(err)
				a.toast(a.u.tf("Speech failed: %s", s.lastErr), true)
			}
			d := job.doc
			if d.p == nil || len(done) == 0 {
				return
			}
			before := d.p.Clone()
			added, last := 0, 0
			for _, m := range done {
				if hasSpeech(d.p, m.path, m.at) {
					continue // already voiced here
				}
				last = d.p.AddSpeech(m.path, m.title, m.seconds, m.at)
				added++
			}
			s.lastDone = added
			if added == 0 {
				if err == nil {
					a.toast(a.u.t("This speech is already on the timeline"), false)
				}
				return
			}
			if d == a.doc {
				a.sel = selection{kind: selMusic, id: last}
				a.committed(before)
				if err == nil && len(lines) > 1 {
					a.toast(a.u.tn(added, "Added %d piece of speech", "Added %d pieces of speech", added), false)
				}
				if listen && len(done) == 1 {
					a.player.Stop()
					a.audition.Play("speech/"+done[0].path, done[0].path, "")
				}
			} else {
				d.hist.Commit(before)
				d.dirty = true
			}
		})
	})
	return nil
}

// hasSpeech reports whether the recording at path already starts at t on a music track.
func hasSpeech(p *project.Project, path string, t float64) bool {
	for _, tr := range p.Music {
		for _, m := range tr.Clips {
			if m.Path == path && m.Offset == 0 && math.Abs(m.Start-t) < 0.01 {
				return true
			}
		}
	}
	return false
}

// speechTitle names a piece of speech on the timeline after its words, on one line and cut short.
func speechTitle(text string) string {
	words := []rune(strings.Join(strings.Fields(text), " "))
	if len(words) > 40 {
		return string(words[:39]) + "…"
	}
	return string(words)
}

// speechStatus describes the running job or the last outcome, for the MCP tools.
func (a *App) speechStatus() map[string]any {
	st := map[string]any{"running": a.speech.job != nil, "engine_loaded": a.speech.server.Running()}
	if j := a.speech.job; j != nil {
		st["stage"], st["progress"], st["lines"] = j.stage, j.fraction, j.lines
	} else {
		st["added"], st["error"] = a.speech.lastDone, a.speech.lastErr
	}
	return st
}
