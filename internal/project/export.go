package project

import "encoding/json"

// Quality picks how hard the encoder compresses, from the best picture to the smallest file.
type Quality int

const (
	High Quality = iota
	Balanced
	Small
	Smaller
	Smallest
)

// Qualities lists every quality, best picture first.
var Qualities = []Quality{High, Balanced, Small, Smaller, Smallest}

// Codec is the video format of the export.
type Codec int

const (
	H264 Codec = iota // plays everywhere
	HEVC              // about half the size of H.264 at the same quality
	AV1               // smaller still; needs a recent player
)

// Speed trades encoding time for file size: slower presets find a smaller file at the same quality.
type Speed int

const (
	Medium Speed = iota
	Fast
	Slow
	Slowest
)

// Audio is how the sound is encoded.
type Audio int

const (
	AudioHigh   Audio = iota // AAC 192 kbit/s stereo
	AudioNormal              // AAC 128 kbit/s stereo
	AudioLow                 // AAC 96 kbit/s stereo
	AudioVoice               // AAC 64 kbit/s mono, enough for speech
	AudioNone                // no sound track
)

// Bitrate is the audio bitrate in kbit/s and the channel count; zero for no sound.
func (a Audio) Bitrate() (kbps, channels int) {
	switch a {
	case AudioNormal:
		return 128, 2
	case AudioLow:
		return 96, 2
	case AudioVoice:
		return 64, 1
	case AudioNone:
		return 0, 0
	}
	return 192, 2
}

// Export is how the result is encoded. The zero value keeps the original size and rate in balanced-looking H.264.
type Export struct {
	Height   int // the short side of the picture is at most this; 0 keeps the canvas size
	FPS      int // frames per second at most; 0 keeps the canvas rate
	Quality  Quality
	Codec    Codec
	Speed    Speed
	Audio    Audio
	Hardware bool    // encode on the graphics card when it can
	TargetMB float64 // when above zero, aim for a file of this many megabytes instead of a constant quality
}

// UnmarshalJSON also reads exports saved before codecs were a choice, when H.265 was the field "HEVC".
func (e *Export) UnmarshalJSON(data []byte) error {
	type plain Export
	var v struct {
		plain
		HEVC bool
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*e = Export(v.plain)
	if v.HEVC && e.Codec == H264 {
		e.Codec = HEVC
	}
	return nil
}

// Valid reports whether every field holds a known value.
func (e Export) Valid() bool {
	return e.Height >= 0 && e.FPS >= 0 && e.TargetMB >= 0 && e.Quality >= High && e.Quality <= Smallest &&
		e.Codec >= H264 && e.Codec <= AV1 && e.Speed >= Medium && e.Speed <= Slowest && e.Audio >= AudioHigh && e.Audio <= AudioNone
}
