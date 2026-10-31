package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/speech"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// aiChoices is every model the chat can talk to: those of the models list, then the voice-over models and the speech
// recognition models, which the app keeps lists of for the video editor and which the chat runs the same way.
func (a *App) aiChoices() []ai.Choice {
	out := a.ai.config.Choices()
	for _, m := range a.speech.config.Models {
		out = append(out, ai.Choice{ID: ai.ExtID(ai.ExtSpeech, m.ID), Kind: ai.KindVoice, Ext: ai.ExtSpeech, Name: m.Name, Note: m.Note, Series: m.Series})
	}
	for _, m := range a.subtitles.config.Models {
		out = append(out, ai.Choice{ID: ai.ExtID(ai.ExtSubtitles, m.ID), Kind: ai.KindTranscribe, Ext: ai.ExtSubtitles, Name: m.Name, Note: m.Note})
	}
	return out
}

// aiFind looks a model up by id among all of aiChoices.
func (a *App) aiFind(id string) (ai.Choice, bool) {
	if id == "" {
		return ai.Choice{}, false
	}
	if ext, _, ok := strings.Cut(id, "/"); ok && (ext == ai.ExtSpeech || ext == ai.ExtSubtitles) {
		for _, ch := range a.aiChoices() {
			if ch.ID == id {
				return ch, true
			}
		}
		return ai.Choice{}, false
	}
	return a.ai.config.Choice(id)
}

// aiExtModel is the model file of a model of another part of the app, and whether it is on this computer.
func (a *App) aiExtModel(ch ai.Choice) (model transcribe.Model, have bool) {
	id := strings.TrimPrefix(ch.ID, ch.Ext+"/")
	switch ch.Ext {
	case ai.ExtSpeech:
		if m, ok := a.speech.config.Model(id); ok {
			model = m.Model
			_, have = a.speech.manager.ModelPath(m.Model)
		}
	case ai.ExtSubtitles:
		if m, ok := a.subtitles.config.Model(id); ok {
			model = m
			_, have = a.subtitles.manager.ModelPath(m)
		}
	}
	return model, have
}

// aiFileKind is what a file the user attached is.
func aiFileKind(path string) ai.Kind {
	switch {
	case isImage(path):
		return ai.KindImage
	case library.IsAudio(path):
		return ai.KindVoice
	case assets.KindOf(path) == assets.Video:
		return ai.KindVideo
	}
	return ""
}

// aiAccepts reports whether the model takes the file with a message: pictures for a model that reads them or starts
// from one, sound and video for one that recognises speech.
func aiAccepts(ch ai.Choice, path string) bool {
	switch kind := aiFileKind(path); {
	case ch.ReadsPictures() || ch.StartsFromPicture():
		return kind == ai.KindImage
	case ch.Kind == ai.KindTranscribe:
		return kind == ai.KindVoice || kind == ai.KindVideo
	}
	return false
}

// aiTakesFiles reports whether the model takes files with a message at all.
func aiTakesFiles(ch ai.Choice) bool {
	return ch.ReadsPictures() || ch.StartsFromPicture() || ch.Kind == ai.KindTranscribe
}

// aiPickExts are the files the dialog offers for the model, and what it calls them.
func (a *App) aiPickExts(ch ai.Choice) (kinds string, exts []string) {
	if ch.Kind == ai.KindTranscribe {
		exts = append(exts, library.AudioExtensions...)
		exts = append(exts, assets.VideoExtensions...)
		return a.u.t("Sound and video"), exts
	}
	return a.u.t("Pictures"), pickPictures
}

// aiExtStage says what a model of another part of the app is doing, from the stage it reports.
func (a *App) aiExtStage(ch ai.Choice, stage string) string {
	u := &a.u
	switch stage {
	case "engine":
		if ch.Kind == ai.KindVoice {
			return u.t("Downloading the voice-over engine")
		}
		return u.t("Downloading the speech engine")
	case "model":
		return u.t("Downloading the model")
	case "load":
		return u.t("Loading the model into memory")
	case "speak":
		return u.t("Speaking")
	case "audio":
		return u.t("Preparing the sound")
	case "transcribe":
		return u.t("Recognising speech")
	}
	return u.t("Thinking…")
}

// aiProgress is how a model of another part of the app reports on its way: into the answer being written, which the
// chat shows.
func (a *App) aiProgress(ans *aiAnswer) transcribe.Progress {
	return func(stage string, fraction float64) {
		ans.mu.Lock()
		ans.stage, ans.fraction = stage, fraction
		ans.mu.Unlock()
		a.host.Wake()
	}
}

// aiLastUser is the last message of the conversation when it is the user's.
func (a *App) aiLastUser() (ai.Message, bool) {
	s := &a.ai
	if s.chat != nil {
		if n := len(s.chat.Messages); n > 0 && s.chat.Messages[n-1].Role == ai.User {
			return s.chat.Messages[n-1], true
		}
	}
	return ai.Message{}, false
}

// aiSpeak has a voice-over model say the last message, in the background, in the voice chosen for voice-overs, and
// keeps the recording in the assets folder, where the conversation plays it from.
func (a *App) aiSpeak(ch ai.Choice) {
	s := &a.ai
	root, err := a.aiAssetRoot()
	if err != nil {
		s.notice = a.describe(err)
		return
	}
	msg, _ := a.aiLastUser()
	text := strings.TrimSpace(msg.Text)
	sp := &a.speech
	model, ok := sp.config.Model(strings.TrimPrefix(ch.ID, ch.Ext+"/"))
	if !ok {
		s.notice = a.u.t("This model is not in the voice-over models any more")
		return
	}
	engine, ok := sp.config.Engine(model, a.settings.SpeechEngine)
	if !ok {
		s.notice = a.u.t("No engine in the voice-over voices configuration runs on this computer; add one in Settings → Extensions")
		return
	}
	if sp.job != nil {
		s.notice = a.u.t("Speech is already being generated")
		return
	}
	voice := a.speechVoice()
	ctx, stop := context.WithCancel(a.life)
	ans := &aiAnswer{stop: stop, chat: s.chat, model: ch.ID, began: time.Now(), kind: ch.Kind, stage: "engine"}
	s.answer = ans
	report := a.aiProgress(ans)
	a.background(func(context.Context) {
		defer stop()
		cli, modelPath, err := sp.manager.Prepare(ctx, engine.Engine, model.Model, report)
		var path string
		if err == nil {
			launch := speech.Launch{Engine: engine, Program: cli, Model: model, ModelPath: modelPath, Dir: filepath.Join(sp.engines, engine.ID)}
			req := speech.Request{Text: text, Voice: voice}
			cache := filepath.Join(sp.dir, speech.FileName(model, req))
			if _, made := speech.Length(cache); !made {
				_, err = sp.server.Speak(ctx, launch, req, cache, report)
			}
			if err == nil {
				var data []byte
				if data, err = os.ReadFile(cache); err == nil {
					path, err = ai.SaveAsset(root, ch.Kind, text, ".wav", data, time.Now())
				}
			}
		}
		if err == nil {
			ans.mu.Lock()
			ans.media = []ai.Media{{Kind: ch.Kind, Path: path}}
			ans.mu.Unlock()
		}
		a.post(func() { a.aiFinish(ans, ai.Stats{}, err) })
	})
}

// aiTranscribe has a speech recognition model write down what is said in the sound and video files attached to the
// last message, in the background; the text is the answer, a line to each stretch of speech with its time.
func (a *App) aiTranscribe(ch ai.Choice) {
	s := &a.ai
	msg, _ := a.aiLastUser()
	var files []string
	for _, at := range msg.Attached {
		if at.Kind == ai.KindVoice || at.Kind == ai.KindVideo {
			files = append(files, at.Path)
		}
	}
	if len(files) == 0 {
		s.notice = a.u.t("Attach a sound or video file to be written down")
		return
	}
	sub := &a.subtitles
	model, ok := sub.config.Model(strings.TrimPrefix(ch.ID, ch.Ext+"/"))
	if !ok {
		s.notice = a.u.t("This model is not in the subtitle models any more")
		return
	}
	engine, ok := a.subtitleEngine()
	if !ok {
		s.notice = a.u.t("No speech engine in the subtitle models configuration runs on this computer; add one in Settings → Extensions")
		return
	}
	config, language := sub.config, a.subtitleLanguage()
	nothing := a.u.t("No speech was recognised in the file")
	ctx, stop := context.WithCancel(a.life)
	ans := &aiAnswer{stop: stop, chat: s.chat, model: ch.ID, began: time.Now(), kind: ch.Kind, stage: "engine"}
	s.answer = ans
	report := a.aiProgress(ans)
	a.background(func(context.Context) {
		defer stop()
		cli, modelPath, err := sub.manager.Prepare(ctx, engine, model, report)
		var text strings.Builder
		for i, file := range files {
			if err != nil {
				break
			}
			var lines []transcribe.Line
			if lines, err = recogniseFile(ctx, config, cli, modelPath, file, language, report); err != nil {
				break
			}
			if len(files) > 1 {
				if i > 0 {
					text.WriteString("\n\n")
				}
				fmt.Fprintf(&text, "**%s**\n\n", filepath.Base(file))
			}
			text.WriteString(transcriptOf(lines))
		}
		if err == nil && strings.TrimSpace(text.String()) == "" {
			err = errors.New(nothing)
		}
		if err == nil {
			ans.mu.Lock()
			ans.text = text.String()
			ans.mu.Unlock()
		}
		a.post(func() { a.aiFinish(ans, ai.Stats{}, err) })
	})
}

// recogniseFile writes down the speech in a sound or video file: its sound is made into the 16 kHz mono recording the
// recogniser reads, in a folder that is removed afterwards.
func recogniseFile(ctx context.Context, config *transcribe.Config, cli, modelPath, file, language string, report transcribe.Progress) ([]transcribe.Line, error) {
	report("audio", 0)
	dir, err := os.MkdirTemp("", "btelo-speech-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	wav := filepath.Join(dir, "speech.wav")
	cmd, err := media.Command(ctx, "ffmpeg", "-y", "-i", file, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav)
	if err != nil {
		return nil, err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("preparing the sound: %w: %s", err, tailOf(out))
	}
	report("transcribe", 0)
	return transcribe.Run(ctx, config, cli, modelPath, wav, language, report)
}

// tailOf is the end of a program's output, enough to say what went wrong.
func tailOf(out []byte) string {
	text := strings.TrimSpace(string(out))
	if len(text) > 400 {
		text = "…" + text[len(text)-400:]
	}
	return text
}

// transcriptOf is recognised speech as text: a line to each stretch, led by the time it starts at.
func transcriptOf(lines []transcribe.Line) string {
	var b strings.Builder
	for _, l := range lines {
		text := strings.TrimSpace(l.Text)
		if text == "" {
			continue
		}
		s := int(l.Start)
		fmt.Fprintf(&b, "`%02d:%02d`  %s\n\n", s/60, s%60, text)
	}
	if b.Len() == 0 {
		return ""
	}
	return strings.TrimSpace(b.String())
}
