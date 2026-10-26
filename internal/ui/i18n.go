package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/media"
	"github.com/btelolabs/btelo-video-cut/internal/render"
	"github.com/btelolabs/btelo-video-cut/internal/transcribe"
)

// Language is an interface language; the source strings are English.
type Language string

const (
	English Language = "en"
	Chinese Language = "zh"
)

var languages = []Language{English, Chinese}

// languageNames are shown in each language's own words.
var languageNames = []string{"English", "中文"}

// t translates a message written in English into the current language.
func (u *ctx) t(s string) string {
	if u.lang == Chinese {
		if z, ok := zh[s]; ok {
			return z
		}
	}
	return s
}

// tf translates a format string and fills it in.
func (u *ctx) tf(format string, args ...any) string { return fmt.Sprintf(u.t(format), args...) }

// ts translates a list of option labels.
func (u *ctx) ts(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = u.t(s)
	}
	return out
}

// msgs marks a list of English labels as messages to translate; it returns them unchanged.
func msgs(s ...string) []string { return s }

// describe turns an error into a message for the user, translating the ones the app knows.
func (a *App) describe(err error) string {
	var small *render.TargetTooSmall
	if errors.As(err, &small) {
		return a.u.tf("The target size is too small for this video; allow at least %.1f MB", small.MinimumMB)
	}
	var missing *transcribe.MissingEngine
	if errors.As(err, &missing) {
		if missing.Install == "" {
			return a.u.tf("The speech engine %s is not installed", missing.Engine)
		}
		return a.u.tf("The speech engine %s is not installed; install it with: %s", missing.Engine, missing.Install)
	}
	for _, known := range knownErrors {
		if errors.Is(err, known.err) {
			return a.u.t(known.message)
		}
	}
	return err.Error()
}

// knownErrors are failures whose message the interface translates instead of showing the technical text.
var knownErrors = []struct {
	err     error
	message string
}{
	{media.ErrMissingTool, "ffmpeg / ffprobe not found"},
	{render.ErrTooLong, "Too many segments to export in one go; merge some of them and try again"},
	{errToolsNotReady, "ffmpeg is still being prepared"},
	{render.ErrEncoderMissing, "This ffmpeg cannot encode the chosen format; pick another format or install a full ffmpeg build"},
	{render.ErrCredits, "The video was exported, but its credits file could not be written"},
}

var gfxShade = gfx.Alpha(colBlack, 0.55)

func rectAt(x, y, w, h float32) gfx.Rect { return gfx.Rect{X: x, Y: y, W: w, H: h} }

func rectCentered(s gfx.Rect, w, h float32) gfx.Rect {
	return gfx.Rect{X: s.X + (s.W-w)/2, Y: s.Y + (s.H-h)/2, W: w, H: h}
}

// panel draws a dialog card.
func panel(u *ctx, r gfx.Rect) {
	u.c.RoundRect(r, u.px(14), colPanel)
	u.c.RoundOutline(r, u.px(14), u.px(1), colBorder)
}

// trackWords are the genre and mood names the library uses; they are data, translated for display.
var trackWords = msgs(
	"African", "Blues", "Classical", "Contemporary", "Disco", "Electronica", "Funk", "Holiday", "Horror", "Jazz", "Latin",
	"Modern", "Musical", "Polka", "Pop", "Reggae", "Rock", "Silent Film", "Ska", "Soundtrack", "Stings", "Unclassifiable",
	"World", "Urban", "Dark", "Grooving", "Relaxed", "Bright", "Calming", "Bouncy", "Driving", "Mysterious", "Intense",
	"Eerie", "Unnerving", "Somber", "Mystical", "Uplifting", "Humorous", "Epic", "Action", "Suspenseful", "Aggressive",
)

// translateWords translates each part of a "A · B · C" list.
func (u *ctx) translateWords(s string) string {
	if u.lang == English || s == "" {
		return s
	}
	parts := strings.Split(s, " · ")
	for i, p := range parts {
		parts[i] = u.t(p)
	}
	return strings.Join(parts, " · ")
}
