package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/speech"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// configKind is a JSON file that extends the app: the music sources, the speech engines and models, the voices
// that speak text, ffmpeg or the streaming platforms.
type configKind struct {
	id       string // name used by the MCP tools
	file     string
	title    string // message
	about    string // message: what the file is for, shown above it in Settings → Extensions
	defaults []byte
	guide    string
	apply    func(a *App, data []byte) error
}

// configTitles and configAbouts mark the titles and descriptions of configKinds for translation.
var (
	configTitles = msgs("Music sources", "Subtitle models", "ffmpeg", "Streaming platforms", "Voice-over voices")
	configAbouts = msgs(
		"Music sources are the websites the music panel searches. Add a site by describing its search address and how to read its answers; see the format guide.",
		"Speech engines, models and languages for AI subtitles. Point to another whisper.cpp version or another ggml model; see the format guide.",
		"Where the app gets ffmpeg, which records, streams and exports: the downloads to try and the oldest version it accepts; see the format guide.",
		"The streaming platforms offered for live streams, with their server addresses and where to find the stream key; see the format guide.",
		"Engines, models and voices for voice-overs. Add a voice by describing it in English, or point to another Qwen3-TTS or Breeze TTS 2 model; see the format guide.",
	)
)

var configKinds = []configKind{
	{
		id: "music_sources", file: "music-sources.json", title: configTitles[0], about: configAbouts[0], defaults: library.DefaultJSON, guide: library.FormatGuide,
		apply: func(a *App, data []byte) error {
			c, err := library.ParseConfig(data)
			if err != nil {
				return err
			}
			a.lib.SetConfig(c)
			a.audition.Stop()
			a.music.reset()
			return nil
		},
	},
	{
		id: "subtitle_models", file: "subtitle-models.json", title: configTitles[1], about: configAbouts[1], defaults: transcribe.DefaultJSON, guide: transcribe.FormatGuide,
		apply: func(a *App, data []byte) error {
			c, err := transcribe.ParseConfig(data)
			if err != nil {
				return err
			}
			a.subtitles.config = c
			return nil
		},
	},
	{
		id: "speech_voices", file: "speech-voices.json", title: configTitles[4], about: configAbouts[4], defaults: speech.DefaultJSON, guide: speech.FormatGuide,
		apply: func(a *App, data []byte) error {
			c, err := speech.ParseConfig(data)
			if err != nil {
				return err
			}
			a.speech.config = c
			a.speech.server.Stop()
			return nil
		},
	},
	{
		id: "ffmpeg", file: "ffmpeg.json", title: configTitles[2], about: configAbouts[2], defaults: media.DefaultToolsJSON, guide: media.FormatGuide,
		apply: func(a *App, data []byte) error {
			c, err := media.ParseToolsConfig(data)
			if err != nil {
				return err
			}
			a.tools.config = c
			if a.tools.stop != nil {
				a.prepareTools()
			}
			return nil
		},
	},
	{
		id: "streaming_platforms", file: "streaming-platforms.json", title: configTitles[3], about: configAbouts[3], defaults: capture.DefaultPlatformsJSON, guide: capture.PlatformsGuide,
		apply: func(a *App, data []byte) error {
			p, err := capture.ParsePlatforms(data)
			if err != nil {
				return err
			}
			if len(p.Platforms) == 0 {
				return errors.New("list at least one platform")
			}
			a.onAir.platforms = p
			return nil
		},
	},
}

func findConfig(id string) (configKind, error) {
	for _, k := range configKinds {
		if k.id == id {
			return k, nil
		}
	}
	return configKind{}, fmt.Errorf("unknown configuration %q (use music_sources, subtitle_models, speech_voices, ffmpeg or streaming_platforms)", id)
}

func (a *App) configPath(k configKind) string { return filepath.Join(a.stateDir, k.file) }

// configText is the configuration in effect: the user's file when there is one, otherwise the built-in one.
func (a *App) configText(k configKind) (text string, custom bool) {
	data, err := os.ReadFile(a.configPath(k))
	if err != nil {
		return string(k.defaults), false
	}
	return string(data), true
}

// loadConfigs applies the user's files at start; a file that cannot be used is reported and the built-in
// configuration is used until it is fixed.
func (a *App) loadConfigs() {
	for _, k := range configKinds {
		data, err := os.ReadFile(a.configPath(k))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil {
			err = k.apply(a, data)
		}
		if err != nil {
			a.toast(a.u.tf("%s: your file is not used (%v); fix it in Settings → Extensions", a.u.t(k.title), err), true)
		}
	}
}

// saveConfig checks text, applies it and keeps it in the user's file.
func (a *App) saveConfig(k configKind, text string) error {
	data := []byte(text)
	if err := k.apply(a, data); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(k.defaults)) {
		return a.removeConfigFile(k)
	}
	if err := os.MkdirAll(a.stateDir, 0o755); err != nil {
		return err
	}
	tmp := a.configPath(k) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.configPath(k))
}

// resetConfig goes back to the built-in configuration.
func (a *App) resetConfig(k configKind) error {
	if err := k.apply(a, k.defaults); err != nil {
		return err
	}
	return a.removeConfigFile(k)
}

func (a *App) removeConfigFile(k configKind) error {
	if err := os.Remove(a.configPath(k)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
