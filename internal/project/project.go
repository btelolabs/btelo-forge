// Package project is the edit model: source videos placed on video tracks, overlays (text and images) and music
// on their own tracks, and export settings. Start/End are output-timeline seconds; In/Out/Offset are seconds
// inside a source file.
package project

import (
	"math"
	"slices"
)

// MinLength is the shortest clip or item an edit may leave, in output seconds.
const MinLength = 0.1

// Speeds are the playback rates offered for a clip.
var Speeds = []float64{0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 3, 4}

// Source is a video file used by the edit.
type Source struct {
	Path          string
	Duration      float64
	Width, Height int
	FPS           float64
	HasVideo      bool
	HasAudio      bool
}

// Clip plays Sources[Source] from In to Out at Speed, starting at Start on the timeline.
type Clip struct {
	ID      int
	Source  int
	In, Out float64
	Speed   float64
	Start   float64
	Volume  float64
}

// Length is the clip's duration on the timeline.
func (c Clip) Length() float64 { return (c.Out - c.In) / c.Speed }

// End is where the clip stops on the timeline.
func (c Clip) End() float64 { return c.Start + c.Length() }

// VideoTrack is a row of clips. Track 0 is the main track, whose clips play back to back from 0; clips on higher
// tracks sit wherever they are placed and cover everything below them.
type VideoTrack struct {
	Clips  []Clip
	Muted  bool
	Volume float64 // gain of the whole track's sound, on top of each clip's own
}

// Music is a sound file placed on the timeline.
type Music struct {
	ID     int
	Path   string
	Title  string
	Credit string // attribution the license asks for, empty when none
	Start  float64
	Offset float64
	Length float64
	Total  float64 // full duration of the file
	Volume float64
	Fade   bool
}

// End is where the clip stops on the timeline.
func (m Music) End() float64 { return m.Start + m.Length }

// MusicTrack is a row of music clips; all music tracks are mixed together.
type MusicTrack struct {
	Clips  []Music
	Muted  bool
	Volume float64
}

// OverlayKind tells text from images.
type OverlayKind int

const (
	TextOverlay OverlayKind = iota
	ImageOverlay
)

// Overlay is text or an image shown from Start to End, centred at (X, Y) as fractions of the frame. Size is the
// text's line height or the image's height, as a fraction of the frame height.
type Overlay struct {
	ID      int
	Kind    OverlayKind
	Start   float64
	End     float64
	X, Y    float64
	Size    float64
	Body    string
	Color   [3]uint8
	Outline bool
	Box     bool
	Image   string
	Aspect  float64 // image width over height
}

// OverlayTrack is a row of overlays; later tracks draw on top of earlier ones and every overlay is above the video.
type OverlayTrack struct {
	Items  []Overlay
	Hidden bool
}

// Canvas is the size and rate of the output picture; every clip is fitted into it.
type Canvas struct {
	Width, Height int
	FPS           float64
}

// Quality picks how hard the encoder compresses.
type Quality int

const (
	High Quality = iota
	Balanced
	Small
)

// Export is how the result is encoded.
type Export struct {
	Height  int // 0 keeps the canvas height
	Quality Quality
	HEVC    bool
}

// Project is everything an edit consists of.
type Project struct {
	Sources  []Source
	Canvas   Canvas
	Video    []VideoTrack
	Overlays []OverlayTrack
	Music    []MusicTrack
	Export   Export
	NextID   int
}

// New starts an edit that plays the whole of src, with an empty overlay track and an empty music track.
func New(src Source, export Export) *Project {
	p := &Project{
		Sources:  []Source{src},
		Canvas:   Canvas{Width: src.Width, Height: src.Height, FPS: math.Min(src.FPS, 60)},
		Video:    []VideoTrack{{Volume: 1}},
		Overlays: []OverlayTrack{{}},
		Music:    []MusicTrack{{Volume: 1}},
		Export:   export,
		NextID:   1,
	}
	p.Video[0].Clips = []Clip{p.fullClip(0)}
	return p
}

func (p *Project) id() int {
	p.NextID++
	return p.NextID - 1
}

func (p *Project) fullClip(source int) Clip {
	return Clip{ID: p.id(), Source: source, In: 0, Out: p.Sources[source].Duration, Speed: 1, Volume: 1}
}

// Clone returns a deep copy, used for undo history.
func (p *Project) Clone() *Project {
	c := *p
	c.Sources = slices.Clone(p.Sources)
	c.Video = make([]VideoTrack, len(p.Video))
	for i, t := range p.Video {
		c.Video[i] = VideoTrack{Clips: slices.Clone(t.Clips), Muted: t.Muted, Volume: t.Volume}
	}
	c.Overlays = make([]OverlayTrack, len(p.Overlays))
	for i, t := range p.Overlays {
		c.Overlays[i] = OverlayTrack{Items: slices.Clone(t.Items), Hidden: t.Hidden}
	}
	c.Music = make([]MusicTrack, len(p.Music))
	for i, t := range p.Music {
		c.Music[i] = MusicTrack{Clips: slices.Clone(t.Clips), Muted: t.Muted, Volume: t.Volume}
	}
	return &c
}

// Equal reports whether two projects describe the same edit.
func Equal(a, b *Project) bool {
	if a.Canvas != b.Canvas || a.Export != b.Export || !slices.Equal(a.Sources, b.Sources) ||
		len(a.Video) != len(b.Video) || len(a.Overlays) != len(b.Overlays) || len(a.Music) != len(b.Music) {
		return false
	}
	for i := range a.Video {
		if a.Video[i].Muted != b.Video[i].Muted || a.Video[i].Volume != b.Video[i].Volume || !slices.Equal(a.Video[i].Clips, b.Video[i].Clips) {
			return false
		}
	}
	for i := range a.Overlays {
		if a.Overlays[i].Hidden != b.Overlays[i].Hidden || !slices.Equal(a.Overlays[i].Items, b.Overlays[i].Items) {
			return false
		}
	}
	for i := range a.Music {
		if a.Music[i].Muted != b.Music[i].Muted || a.Music[i].Volume != b.Music[i].Volume || !slices.Equal(a.Music[i].Clips, b.Music[i].Clips) {
			return false
		}
	}
	return true
}

// Duration is the length of the output: the end of the last clip on any video track.
func (p *Project) Duration() float64 {
	end := 0.0
	for _, t := range p.Video {
		for _, c := range t.Clips {
			end = math.Max(end, c.End())
		}
	}
	return end
}

// ContentEnd is where the last thing of any kind ends, which may lie past Duration.
func (p *Project) ContentEnd() float64 {
	end := p.Duration()
	for _, t := range p.Overlays {
		for _, o := range t.Items {
			end = math.Max(end, o.End)
		}
	}
	for _, t := range p.Music {
		for _, m := range t.Clips {
			end = math.Max(end, m.End())
		}
	}
	return end
}

// layout packs the main track's clips back to back from 0.
func (p *Project) layout() {
	t := 0.0
	for i := range p.Video[0].Clips {
		p.Video[0].Clips[i].Start = t
		t += p.Video[0].Clips[i].Length()
	}
}

// Source of a clip.
func (p *Project) SourceOf(c Clip) Source { return p.Sources[c.Source] }

// AddSource registers a file, reusing its entry when it is already known, and returns its index.
func (p *Project) AddSource(src Source) int {
	if i := slices.IndexFunc(p.Sources, func(s Source) bool { return s.Path == src.Path }); i >= 0 {
		return i
	}
	p.Sources = append(p.Sources, src)
	return len(p.Sources) - 1
}

// Locate returns the main-track clip playing at t and the source time it shows; t is clamped to the main track.
func (p *Project) Locate(t float64) (int, float64) {
	clips := p.Video[0].Clips
	for i, c := range clips {
		if t < c.End() || i == len(clips)-1 {
			local := math.Min(math.Max(t-c.Start, 0), c.Length())
			return i, math.Min(c.In+local*c.Speed, c.Out)
		}
	}
	return 0, 0
}
