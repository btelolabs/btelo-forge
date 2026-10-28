// Package render turns a project into ffmpeg invocations: the export, and the preview streams that play from any point.
package render

import (
	"math"

	"github.com/btelolabs/btelo-forge/internal/project"
)

const epsilon = 1e-3

// piece is the part of a clip inside the rendered range: source [In, Out) of Path at Speed, Delay seconds after the
// range starts, heard at Gain (0 when silent) and seen at Opacity.
type piece struct {
	Path           string
	In, Out, Speed float64
	Delay          float64
	Gain           float64
	Sound          bool
	Opacity        float64
}

func (p piece) length() float64 { return (p.Out - p.In) / p.Speed }

// cue is the part of a music clip inside the rendered range.
type cue struct {
	Path                  string
	Offset, Length, Delay float64
	Volume                float64
	FadeIn, FadeOut       bool
}

func (c cue) fadeLength() float64 { return math.Min(1, c.Length/4) }

// window is the output range [from, to) cut out of a project.
type window struct {
	main       []piece // back to back from the start of the range
	upper      [][]piece
	cues       []cue
	length     float64
	mainLength float64
}

func clipPiece(p *project.Project, c project.Clip, track project.VideoTrack, from, to float64) (piece, bool) {
	a, b := math.Max(from, c.Start), math.Min(to, c.End())
	if b-a <= epsilon {
		return piece{}, false
	}
	src := p.SourceOf(c)
	gain := c.Volume * track.Volume
	return piece{
		Path:    src.Path,
		In:      c.In + (a-c.Start)*c.Speed,
		Out:     c.In + (b-c.Start)*c.Speed,
		Speed:   c.Speed,
		Delay:   a - from,
		Gain:    gain,
		Sound:   src.HasAudio && !track.Muted && gain > 0,
		Opacity: c.Opacity,
	}, true
}

// cut returns everything that plays between output times from and to.
func cut(p *project.Project, from, to float64) window {
	from, to = math.Max(from, 0), math.Min(to, p.Duration())
	w := window{length: math.Max(to-from, 0)}
	for _, c := range p.Video[0].Clips {
		if pc, ok := clipPiece(p, c, p.Video[0], from, to); ok {
			w.main = append(w.main, pc)
			w.mainLength += pc.length()
		}
	}
	for _, t := range p.Video[1:] {
		var row []piece
		for _, c := range t.Clips {
			if pc, ok := clipPiece(p, c, t, from, to); ok {
				row = append(row, pc)
			}
		}
		if len(row) > 0 {
			w.upper = append(w.upper, row)
		}
	}
	for _, t := range p.Music {
		if t.Muted {
			continue
		}
		for _, m := range t.Clips {
			a, b := math.Max(from, m.Start), math.Min(to, m.End())
			vol := m.Volume * t.Volume
			if b-a <= epsilon || vol <= 0 {
				continue
			}
			w.cues = append(w.cues, cue{
				Path: m.Path, Offset: m.Offset + a - m.Start, Length: b - a, Delay: a - from, Volume: vol,
				FadeIn: m.Fade && a-m.Start < epsilon, FadeOut: m.Fade && m.End()-b < epsilon,
			})
		}
	}
	return w
}

// audible reports whether the window has any sound.
func (w window) audible() bool {
	if len(w.cues) > 0 {
		return true
	}
	for _, pc := range w.main {
		if pc.Sound {
			return true
		}
	}
	for _, row := range w.upper {
		for _, pc := range row {
			if pc.Sound {
				return true
			}
		}
	}
	return false
}

// OutputSize is the encoded frame size for a target height (0 keeps the canvas), rounded to even numbers.
func OutputSize(c project.Canvas, height int) (int, int) {
	w, h := float64(c.Width), float64(c.Height)
	if short := min(w, h); height > 0 && float64(height) < short {
		w, h = w*float64(height)/short, h*float64(height)/short
	}
	return even(w), even(h)
}

func even(v float64) int { return max(2, int(math.Round(v/2))*2) }
