package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/render"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
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

// systemLanguage is the interface language for the system's most preferred language, or English when we do not
// have that one.
func systemLanguage(tags []string) Language {
	if len(tags) == 0 {
		return English
	}
	primary, _, _ := strings.Cut(strings.ReplaceAll(tags[0], "_", "-"), "-")
	if l := Language(strings.ToLower(primary)); slices.Contains(languages, l) {
		return l
	}
	return English
}

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

// tn translates a message about n things, written in English in its singular and its plural, and fills it in:
// English says one thing in the singular; Chinese, with one form for both, is keyed by the plural.
func (u *ctx) tn(n int, one, many string, args ...any) string {
	if u.lang == Chinese {
		if z, ok := zh[many]; ok {
			return fmt.Sprintf(z, args...)
		}
	}
	if n == 1 {
		return fmt.Sprintf(one, args...)
	}
	return fmt.Sprintf(many, args...)
}

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

// tc translates a message that means something else elsewhere, such as Photoshop's "Screen" blend mode against a
// computer screen; its Chinese entry is keyed context|message.
func (u *ctx) tc(context, s string) string {
	if u.lang == Chinese {
		if z, ok := zh[context+"|"+s]; ok {
			return z
		}
	}
	return s
}

// msgsIn marks a list of labels to translate with tc in a context; it returns them unchanged.
func msgsIn(context string, s ...string) []string { return s }

// tcs translates a list with tc.
func (u *ctx) tcs(context string, list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = u.tc(context, s)
	}
	return out
}

// describe turns an error into a message for the user, translating the ones the app knows.
func (a *App) describe(err error) string {
	var small *render.TargetTooSmall
	if errors.As(err, &small) {
		return a.u.tf("The target size is too small for this video; allow at least %.1f MB", small.MinimumMB)
	}
	var old *capture.OldWindowsError
	if errors.As(err, &old) {
		return a.u.tf("Screen recording needs Windows 10 version 2004 or later; this computer runs Windows %d.%d (build %d)", old.Major, old.Minor, old.Build)
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
	{project.ErrNotEdit, "This is not an edit file, or it is damaged"},
	{project.ErrSourceChanged, "The video this edit starts from has changed since it was saved"},
	{render.ErrEncoderMissing, "This ffmpeg cannot encode the chosen format; pick another format or install a full ffmpeg build"},
	{render.ErrCredits, "The video was exported, but its credits file could not be written"},
	{paint.ErrHidden, "The chosen layer is hidden; show it to paint on it"},
	{paint.ErrGroup, "Choose a layer, not a group, in the layers panel"},
	{paint.ErrNoLayer, "Choose a layer in the layers panel"},
	{paint.ErrNothingBelow, "There is no layer below to merge into"},
	{paint.ErrLastLayer, "A picture keeps at least one layer"},
	{paint.ErrTooBig, "The picture is too large; its sides can be up to 16384 pixels"},
	{paint.ErrNotACanvas, "This is not a canvas file, or it is damaged"},
	{paint.ErrVectorLayer, "The chosen layer is a vector layer: rasterize it to paint on it"},
	{paint.ErrNotVector, "Choose a vector layer, or a layer with a vector mask"},
	{paint.ErrBoardInside, "An artboard stays at the top of the layers, not inside a group"},
	{paint.ErrMergeBoard, "An artboard cannot be merged; ungroup it first"},
	{paint.ErrNoClip, "The chosen layer has no vector mask"},
	{paint.ErrNoShape, "Choose the nodes of a shape after the first one"},
	{paint.ErrSmartBoard, "An artboard cannot be a smart object, nor be in one"},
	{paint.ErrNotSmart, "The chosen layer is not a smart object"},
	{capture.ErrUnsupported, "Screen recording is not supported on this system"},
	{capture.ErrNoEncoder, "Neither the graphics card nor ffmpeg's own encoder could record this screen"},
	{capture.ErrNoDisplay, "No screen that can be recorded is attached to this computer"},
	{capture.ErrNoGrabber, "This ffmpeg cannot grab the screen (it was built without ddagrab); choose a full ffmpeg build in Settings → Extensions"},
	{capture.ErrSystemSound, "The computer's sound could not be recorded"},
	{capture.ErrMicrophone, "The microphone could not be recorded; check that it is plugged in and that Windows lets apps use it (Settings → Privacy → Microphone)"},
	{capture.ErrNothingRecorded, "Nothing was recorded"},
	{capture.ErrNoTargets, "Choose at least one destination to stream to"},
	{capture.ErrTargetAddress, "The server address must start with rtmp://, rtmps:// or srt://"},
}

var gfxShade = gfx.Alpha(colBlack, 0.55)

func rectAt(x, y, w, h float32) gfx.Rect { return gfx.Rect{X: x, Y: y, W: w, H: h} }

func rectCentered(s gfx.Rect, w, h float32) gfx.Rect {
	return gfx.Rect{X: s.X + (s.W-w)/2, Y: s.Y + (s.H-h)/2, W: w, H: h}
}

// panel draws a dialog card.
func panel(u *ctx, r gfx.Rect) {
	u.c.Fill(r, colPanel)
	u.c.Outline(r, u.px(1), colBorder)
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
