package playback

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync/atomic"

	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/render"
)

// timeline is the edited timeline, rendered through the same filter graph as the export.
type timeline struct{ p *project.Project }

func (t timeline) length() float64 { return t.p.Duration() }
func (t timeline) span() float64   { return span }
func (t timeline) rate() float64   { return FPS }
func (t timeline) speed() float64  { return 1 }
func (t timeline) picture() bool   { return true }

func (t timeline) audible(from, to float64) bool { return render.Audible(t.p, from, to) }

func (t timeline) video(from, to float64, w, h int) ([]string, error) {
	g, err := render.Build(render.Spec{Project: t.p, From: from, To: to, Width: w, Height: h, FPS: FPS, Video: true, PixFmt: "rgba"})
	if err != nil {
		return nil, err
	}
	return g.Args("-f", "rawvideo", "pipe:1")
}

func (t timeline) audio(from, to float64) ([]string, error) {
	g, err := render.Build(render.Spec{Project: t.p, From: from, To: to, Audio: true})
	if err != nil {
		return nil, err
	}
	return g.Args("-f", "s16le", "-ar", "48000", "-ac", "2", "pipe:1")
}

// File is a video file played as it is, with no edit between it and the screen: one decoder for its pictures at
// its own frame rate, scaled by ffmpeg straight to the size shown, and one for its sound, from where it starts to
// its end. Measured on 4K HEVC, decoding on the processor beats the graphics card's decoders, which have to copy
// every picture back before it can be scaled, so the decoder is ffmpeg's own.
type File struct {
	Path     string
	Duration float64
	FPS      float64 // the file's frame rate; 0 when it does not say
	HasVideo bool
	HasAudio bool
	// Speed is how many seconds of the file play in one; 0 is 1.
	Speed float64
}

// MaxFileFPS is the most pictures a second a file is shown at; one with more has some left out.
const MaxFileFPS = 120

func (f File) length() float64 { return f.Duration }
func (f File) span() float64   { return math.Inf(1) } // a file needs no graph rebuilt: one pair plays it to its end
func (f File) picture() bool   { return f.HasVideo }

func (f File) rate() float64 {
	if f.FPS <= 0 || math.IsNaN(f.FPS) {
		return FPS
	}
	return min(f.FPS, MaxFileFPS)
}

func (f File) speed() float64 {
	if f.Speed <= 0 {
		return 1
	}
	return f.Speed
}

func (f File) audible(from, to float64) bool { return f.HasAudio && to > from }

func num(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

func (f File) video(from, to float64, w, h int) ([]string, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid picture size %d×%d", w, h)
	}
	// The timestamps are made to start at zero and run at the speed before fps picks the pictures, so picture k is
	// at from + k/rate × speed of the file, whatever its own timing; ffmpeg turns the file upright as it decodes.
	vf := fmt.Sprintf("setpts=(PTS-STARTPTS)/%s,fps=%s,scale=%d:%d:flags=bicubic,format=rgba", num(f.speed()), num(f.rate()), w, h)
	return []string{"-v", "error", "-nostdin", "-ss", num(from), "-t", num(to - from), "-i", f.Path,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-vf", vf, "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1"}, nil
}

func (f File) audio(from, to float64) ([]string, error) {
	// The sound is padded with silence to the end of the pictures: it drives the clock, and a sound track a little
	// shorter than the video must not end the playing early.
	af := "apad"
	if t := render.Tempo(f.speed()); t != "" {
		af = t + "," + af
	}
	return []string{"-v", "error", "-nostdin", "-ss", num(from), "-i", f.Path,
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-af", af, "-t", num((to - from) / f.speed()),
		"-f", "s16le", "-ar", "48000", "-ac", "2", "pipe:1"}, nil
}

// gain scales s16 sound by a volume that may change while it plays.
type gain struct {
	r      io.Reader
	volume *atomic.Uint64
}

func (g *gain) Read(p []byte) (int, error) {
	if len(p) < 2 {
		return g.r.Read(p)
	}
	n, err := g.r.Read(p[:len(p)&^1])
	if n%2 == 1 { // half a sample: the other half is read too, so no sample is ever split between two reads
		m, rerr := io.ReadFull(g.r, p[n:n+1])
		n += m
		if err == nil {
			err = rerr // a stream that ends inside a sample loses that byte, which n&^1 drops
		}
	}
	scale(p[:n&^1], math.Float64frombits(g.volume.Load()))
	return n &^ 1, err
}

// scale multiplies little-endian s16 samples by v, clipping at full scale.
func scale(pcm []byte, v float64) {
	if v == 1 {
		return
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		s := float64(int16(binary.LittleEndian.Uint16(pcm[i:]))) * v
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(min(max(math.Round(s), math.MinInt16), math.MaxInt16))))
	}
}
