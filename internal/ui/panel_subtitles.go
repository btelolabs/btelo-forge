package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/render"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// subtitlePanel is the state of the AI subtitles tab and its running job.
type subtitlePanel struct {
	config   *transcribe.Config
	manager  *transcribe.Manager
	job      *subtitleJob
	lastErr  string
	lastDone int
}

type subtitleJob struct {
	stop     context.CancelFunc
	doc      *doc
	stage    string
	fraction float64
	stopping bool
}

func (s *subtitlePanel) busy() bool { return s.job != nil }

func (s *subtitlePanel) cancel() {
	if s.job != nil {
		s.job.stopping = true
		s.job.stop()
	}
}

// subtitleStage says what the job is doing and how far it has got.
func (a *App) subtitleStage(j *subtitleJob) (string, float64) {
	u := &a.u
	stage := map[string]string{"engine": u.t("Downloading the speech engine"), "model": u.t("Downloading the model"),
		"audio": u.t("Preparing the sound"), "transcribe": u.t("Recognising speech")}[j.stage]
	return stage, j.fraction
}

func (a *App) subtitleModel() transcribe.Model {
	c := a.subtitles.config
	if m, ok := c.Model(a.settings.SubtitleModel); ok {
		return m
	}
	m, _ := c.Model(c.DefaultModel)
	return m
}

func (a *App) subtitleLanguage() string {
	c := a.subtitles.config
	if _, ok := c.Language(a.settings.SubtitleLanguage); ok {
		return a.settings.SubtitleLanguage
	}
	return c.Languages[0].Code
}

// subtitleEngine is the chosen engine, or the first one this computer can run; it is false when there is none.
func (a *App) subtitleEngine() (transcribe.Engine, bool) {
	return a.subtitles.config.Engine(a.settings.SubtitleEngine)
}

// drawSubtitlePanel draws the AI subtitles tab and returns the bottom of its content.
func (a *App) drawSubtitlePanel(r gfx.Rect) float32 {
	u := &a.u
	s := &a.subtitles
	y := r.Y
	f := u.face(12)
	for _, l := range f.Wrap(u.t("Recognises the speech in the video on this computer with a local AI model (whisper.cpp) and adds it as subtitles on a new overlay track. The engine and model are downloaded once."), r.W) {
		u.c.Text(f, r.X, y, l, colDim)
		y += f.Height() * 1.3
	}
	y += u.px(10)
	// While it runs, its progress shows in the corner of the window (drawTasks), whichever page is in front.
	goBox := gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(40)}
	if s.job != nil {
		u.button("sub/go", goBox, icSubtitle, u.t("Generating subtitles…"), primary, false)
	} else if u.button("sub/go", goBox, icSubtitle, u.t("Generate subtitles"), primary, true) {
		if err := a.generateSubtitles(); err != nil {
			a.toast(a.describe(err), true)
		}
	}
	y += u.px(52)
	if s.lastErr != "" {
		for _, l := range f.Wrap(s.lastErr, r.W) {
			u.c.Text(f, r.X, y, l, colDangerText)
			y += f.Height() * 1.3
		}
	}
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Model"))
	y += u.px(26)
	lang := string(u.lang)
	modelPick, y := a.drawModelRows("sub/model/", gfx.Rect{X: r.X, Y: y, W: r.W}, s.config.Models, a.subtitleModel().ID, s.manager)
	if modelPick != "" && s.job == nil {
		a.settings.SubtitleModel = modelPick
		a.saveSettings()
	}
	y += u.px(6)
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Spoken language"))
	y += u.px(26)
	var labels []string
	current := 0
	for i, l := range s.config.Languages {
		labels = append(labels, l.Name.In(lang))
		if l.Code == a.subtitleLanguage() {
			current = i
		}
	}
	pick, h := u.flowChips("sub/lang", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(26)}, labels, current)
	if pick != current && s.job == nil {
		a.settings.SubtitleLanguage = s.config.Languages[pick].Code
		a.saveSettings()
	}
	y += h + u.px(14)
	engines := s.config.Available()
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Run on"))
	y += u.px(26)
	engine, ok := a.subtitleEngine()
	if !ok {
		for _, l := range f.Wrap(u.t("No speech engine in the subtitle models configuration runs on this computer; add one in Settings → Extensions"), r.W) {
			u.c.Text(f, r.X, y, l, colDim)
			y += f.Height() * 1.3
		}
		return y + u.px(8)
	}
	enginePick, y := a.drawEngineChoice("sub/engine", gfx.Rect{X: r.X, Y: y, W: r.W}, engines, engine)
	if enginePick != "" && s.job == nil {
		a.settings.SubtitleEngine = enginePick
		a.saveSettings()
	}
	return y + u.px(10)
}

// generateSubtitles prepares the engine and model, renders the timeline's speech and recognises it in the background.
func (a *App) generateSubtitles() error {
	s := &a.subtitles
	if a.p == nil {
		return errNoProject
	}
	if s.job != nil {
		return errors.New(a.u.t("Subtitles are already being generated"))
	}
	if !render.Audible(speechOnly(a.p), 0, a.p.Duration()) {
		return errors.New(a.u.t("The video has no sound to recognise"))
	}
	engine, ok := a.subtitleEngine()
	if !ok {
		return errors.New(a.u.t("No speech engine in the subtitle models configuration runs on this computer; add one in Settings → Extensions"))
	}
	config, model, language := s.config, a.subtitleModel(), a.subtitleLanguage()
	ctx, stop := context.WithCancel(a.life)
	job := &subtitleJob{stop: stop, doc: a.doc, stage: "engine"}
	s.job, s.lastErr = job, ""
	snapshot := speechOnly(a.p.Clone())
	report := func(stage string, f float64) { a.post(func() { job.stage, job.fraction = stage, f }) }
	a.background(func(_ context.Context) {
		lines, err := a.recognise(ctx, config, snapshot, model, language, engine, report)
		a.post(func() {
			s.job = nil
			stop()
			switch {
			case errors.Is(err, context.Canceled):
				return
			case err != nil:
				s.lastErr = a.describe(err)
				a.toast(a.u.tf("Subtitles failed: %s", s.lastErr), true)
				return
			case len(lines) == 0:
				s.lastErr = a.u.t("No speech was recognised")
				return
			}
			d := job.doc
			if d.p == nil {
				return
			}
			before := d.p.Clone()
			var items []project.Overlay
			for _, l := range lines {
				items = append(items, project.Overlay{Body: l.Text, Start: l.Start, End: l.End, Opacity: 1})
			}
			d.p.AddCaptions(items)
			s.lastDone = len(items)
			if d == a.doc {
				a.committed(before)
				a.toast(a.u.tn(len(items), "Added %d subtitle on a new overlay track", "Added %d subtitles on a new overlay track", len(items)), false)
			} else {
				d.hist.Commit(before)
				d.dirty = true
			}
		})
	})
	return nil
}

// speechOnly silences the music so only the video's own sound is recognised.
func speechOnly(p *project.Project) *project.Project {
	c := p.Clone()
	for i := range c.Music {
		c.Music[i].Muted = true
	}
	return c
}

func (a *App) recognise(ctx context.Context, config *transcribe.Config, p *project.Project, model transcribe.Model, language string, engine transcribe.Engine, report transcribe.Progress) ([]transcribe.Line, error) {
	cli, modelPath, err := a.subtitles.manager.Prepare(ctx, engine, model, report)
	if err != nil {
		return nil, err
	}
	report("audio", 0)
	dir, err := os.MkdirTemp("", "btelo-subtitles-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	wav := filepath.Join(dir, "speech.wav")
	g, err := render.Build(render.Spec{Project: p, From: 0, To: p.Duration(), Audio: true})
	if err != nil {
		return nil, err
	}
	args, err := g.Args("-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav)
	if err != nil {
		return nil, err
	}
	cmd, err := media.Command(ctx, "ffmpeg", args...)
	if err != nil {
		return nil, err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("preparing the sound: %w: %s", err, out)
	}
	report("transcribe", 0)
	return transcribe.Run(ctx, config, cli, modelPath, wav, language, report)
}
