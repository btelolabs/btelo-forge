package ui

import (
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/render"
)

// exportPreset is a complete set of export settings offered as one choice.
type exportPreset struct {
	id, name, note string
	export         func(av1 bool) project.Export
}

// defaultTargetMB is the size the "fit a size" preset starts with: what most chat apps accept.
const defaultTargetMB = 25

var exportPresets = []exportPreset{
	{"high", "High quality", "Original size, best picture", func(bool) project.Export {
		return project.Export{Quality: project.High}
	}},
	{"balanced", "Balanced", "Original size, good picture, moderate file", func(bool) project.Export {
		return project.Export{Quality: project.Balanced}
	}},
	{"small", "Small file", "H.265 at the original size, about half the size of Balanced", func(bool) project.Export {
		return project.Export{Quality: project.Small, Codec: project.HEVC, Audio: project.AudioNormal}
	}},
	{"strong", "Strong compression", "720p · 30 fps · H.265 · slow encoding, for sharing", func(bool) project.Export {
		return project.Export{Height: 720, FPS: 30, Quality: project.Smaller, Codec: project.HEVC, Speed: project.Slow, Audio: project.AudioLow}
	}},
	{"extreme", "Extreme compression", "480p · 24 fps · AV1 (H.265 when AV1 is missing) · voice-quality sound, the smallest file", func(av1 bool) project.Export {
		codec := project.HEVC
		if av1 {
			codec = project.AV1
		}
		return project.Export{Height: 480, FPS: 24, Quality: project.Smallest, Codec: codec, Speed: project.Slow, Audio: project.AudioVoice}
	}},
	{"size", "Fit a size", "Aim for a file of a chosen size, encoded in two passes", func(bool) project.Export {
		return project.Export{Quality: project.Balanced, Codec: project.HEVC, Speed: project.Slow, Audio: project.AudioNormal, TargetMB: defaultTargetMB}
	}},
}

// presetWords marks the preset texts for translation.
var presetWords = msgs("High quality", "Original size, best picture", "Balanced", "Original size, good picture, moderate file",
	"Small file", "H.265 at the original size, about half the size of Balanced", "Strong compression",
	"720p · 30 fps · H.265 · slow encoding, for sharing", "Extreme compression",
	"480p · 24 fps · AV1 (H.265 when AV1 is missing) · voice-quality sound, the smallest file", "Fit a size",
	"Aim for a file of a chosen size, encoded in two passes")

// matchingPreset is the preset e is exactly, or -1 for custom settings; the size preset matches any size.
func matchingPreset(e project.Export, av1 bool) int {
	for i, p := range exportPresets {
		want := p.export(av1)
		if want.TargetMB > 0 && e.TargetMB > 0 {
			want.TargetMB = e.TargetMB
		}
		if want == e {
			return i
		}
	}
	return -1
}

// Option lists of the export settings, with the ids the MCP tools use.
var (
	exportHeights   = []int{0, 2160, 1440, 1080, 720, 480, 360}
	heightNames     = msgs("Original", "2160p", "1440p", "1080p", "720p", "480p", "360p")
	exportRates     = []int{0, 60, 30, 24, 15}
	rateNames       = msgs("Original", "60 fps", "30 fps", "24 fps", "15 fps")
	qualityIDs      = []string{"high", "balanced", "small", "smaller", "smallest"}
	qualityNames    = msgs("Best", "Balanced", "Small", "Smaller", "Smallest")
	codecIDs        = []string{"h264", "h265", "av1"}
	codecNames      = msgs("H.264 · plays anywhere", "H.265 · smaller", "AV1 · smallest")
	speedIDs        = []string{"medium", "fast", "slow", "slowest"}
	speedOrder      = []project.Speed{project.Fast, project.Medium, project.Slow, project.Slowest}
	speedNames      = msgs("Fast", "Normal", "Slow · smaller", "Slowest · smallest")
	audioIDs        = []string{"high", "normal", "low", "voice", "none"}
	audioNames      = msgs("192 kbit/s", "128 kbit/s", "96 kbit/s", "Voice · 64 kbit/s mono", "No sound")
	codecShortNames = []string{"H.264", "H.265", "AV1"}
)

// codecAvailable reports whether codec can be encoded, in software or on the graphics card as asked.
func codecAvailable(enc media.Encoders, codec project.Codec, hardware bool) bool {
	return render.Encoder(codec, hardware, enc) != ""
}

// hardwareAvailable reports whether the graphics card can encode codec itself.
func hardwareAvailable(enc media.Encoders, codec project.Codec) bool {
	name := render.Encoder(codec, true, enc)
	return name != "" && name != render.Encoder(codec, false, enc)
}

func indexOf[T comparable](list []T, v T) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}
