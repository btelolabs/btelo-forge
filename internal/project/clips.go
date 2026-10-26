package project

import (
	"math"
	"slices"
)

func clipSpan(c Clip) (float64, float64) { return c.Start, c.End() }

// FindClip returns the track and index of the clip with id.
func (p *Project) FindClip(id int) (track, index int, ok bool) {
	for k, t := range p.Video {
		for i, c := range t.Clips {
			if c.ID == id {
				return k, i, true
			}
		}
	}
	return 0, 0, false
}

// Clip returns the clip with id.
func (p *Project) Clip(id int) (Clip, bool) {
	if k, i, ok := p.FindClip(id); ok {
		return p.Video[k].Clips[i], true
	}
	return Clip{}, false
}

// Top returns the clip whose picture shows at t (the one on the highest track) and the source time it shows.
func (p *Project) Top(t float64) (Clip, float64, bool) {
	for k := len(p.Video) - 1; k >= 0; k-- {
		for _, c := range p.Video[k].Clips {
			if t >= c.Start && t < c.End() {
				return c, math.Min(c.In+(t-c.Start)*c.Speed, c.Out), true
			}
		}
	}
	if main := p.Video[0].Clips; len(main) > 0 && t >= p.Duration()-1e-9 {
		last := main[len(main)-1]
		if last.End() >= p.Duration()-1e-9 {
			return last, last.Out, true
		}
	}
	return Clip{}, 0, false
}

// ClipAt returns the clip on track at time t.
func (p *Project) ClipAt(track int, t float64) (Clip, bool) {
	for _, c := range p.Video[track].Clips {
		if t >= c.Start && t < c.End() {
			return c, true
		}
	}
	return Clip{}, false
}

// Append puts the whole of source at the end of the main track and returns the new clip's id.
func (p *Project) Append(source int) int {
	c := p.fullClip(source)
	p.Video[0].Clips = append(p.Video[0].Clips, c)
	p.layout()
	return c.ID
}

// Place puts the whole of source at t on the lowest upper track that has room, adding a track when none has.
func (p *Project) Place(source int, t float64) int {
	c := p.fullClip(source)
	c.Start = math.Max(t, 0)
	for k := 1; k < len(p.Video); k++ {
		if free(taken(p.Video[k].Clips, -1, clipSpan), c.Start, c.Length()) {
			p.Video[k].Clips = append(p.Video[k].Clips, c)
			p.sortTrack(k)
			return c.ID
		}
	}
	p.Video = append(p.Video, VideoTrack{Clips: []Clip{c}, Volume: 1})
	return c.ID
}

func (p *Project) sortTrack(k int) {
	slices.SortFunc(p.Video[k].Clips, func(a, b Clip) int { return compare(a.Start, b.Start) })
}

func compare(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// SplitClip cuts clip id in two at timeline time t; it refuses cuts closer than MinLength to either end.
func (p *Project) SplitClip(id int, t float64) bool {
	k, i, ok := p.FindClip(id)
	if !ok {
		return false
	}
	c := p.Video[k].Clips[i]
	if t-c.Start < MinLength || c.End()-t < MinLength {
		return false
	}
	at := c.In + (t-c.Start)*c.Speed
	left, right := c, c
	left.Out = at
	right.In, right.ID, right.Start = at, p.id(), t
	p.Video[k].Clips = slices.Replace(p.Video[k].Clips, i, i+1, left, right)
	return true
}

// DeleteClip removes clip id; the main track always keeps at least one clip.
func (p *Project) DeleteClip(id int) bool {
	k, i, ok := p.FindClip(id)
	if !ok || k == 0 && len(p.Video[0].Clips) <= 1 {
		return false
	}
	p.Video[k].Clips = slices.Delete(p.Video[k].Clips, i, i+1)
	p.layout()
	p.prune()
	return true
}

// SetIn moves clip id's source start, keeping at least MinLength; on upper tracks the clip's end stays put and its
// start may not run into the clip before it.
func (p *Project) SetIn(id int, in float64) {
	k, i, ok := p.FindClip(id)
	if !ok {
		return
	}
	c := &p.Video[k].Clips[i]
	lo := 0.0
	if k > 0 {
		before, _ := gap(taken(p.Video[k].Clips, i, clipSpan), c.Start)
		lo = math.Max(0, c.In-(c.Start-before)*c.Speed)
	}
	end := c.End()
	c.In = math.Min(math.Max(in, lo), c.Out-MinLength*c.Speed)
	if k > 0 {
		c.Start = end - c.Length()
	}
	p.layout()
}

// SetOut moves clip id's source end, within the source and, on upper tracks, before the next clip.
func (p *Project) SetOut(id int, out float64) {
	k, i, ok := p.FindClip(id)
	if !ok {
		return
	}
	c := &p.Video[k].Clips[i]
	hi := p.Sources[c.Source].Duration
	if k > 0 {
		_, after := gap(taken(p.Video[k].Clips, i, clipSpan), c.Start)
		hi = math.Min(hi, c.In+(after-c.Start)*c.Speed)
	}
	c.Out = math.Max(math.Min(out, hi), c.In+MinLength*c.Speed)
	p.layout()
}

// SetSpeed changes clip id's rate; on an upper track it is refused when the longer clip would run into the next one.
func (p *Project) SetSpeed(id int, speed float64) bool {
	k, i, ok := p.FindClip(id)
	if !ok {
		return false
	}
	speed = math.Min(math.Max(speed, Speeds[0]), Speeds[len(Speeds)-1])
	c := p.Video[k].Clips[i]
	c.Speed = speed
	if k > 0 && !free(taken(p.Video[k].Clips, i, clipSpan), c.Start, c.Length()) {
		return false
	}
	p.Video[k].Clips[i].Speed = speed
	p.layout()
	return true
}

// SetClipVolume sets the gain of clip id's own sound.
func (p *Project) SetClipVolume(id int, v float64) {
	if k, i, ok := p.FindClip(id); ok {
		p.Video[k].Clips[i].Volume = math.Min(math.Max(v, 0), 2)
	}
}

// MoveClip moves clip id to track at about start. On the main track it is inserted between the clips nearest start;
// on an upper track it goes into the free gap nearest start. Moving the last main-track clip away is refused.
// track may be len(Video), which adds a new upper track.
func (p *Project) MoveClip(id, track int, start float64) bool {
	k, i, ok := p.FindClip(id)
	if !ok || track < 0 || track > len(p.Video) || k == 0 && track != 0 && len(p.Video[0].Clips) <= 1 {
		return false
	}
	c := p.Video[k].Clips[i]
	p.Video[k].Clips = slices.Delete(p.Video[k].Clips, i, i+1)
	if track == len(p.Video) {
		p.Video = append(p.Video, VideoTrack{Volume: 1})
	}
	if track == 0 {
		at := 0
		for _, o := range p.Video[0].Clips {
			if o.Start+o.Length()/2 < start {
				at++
			}
		}
		p.Video[0].Clips = slices.Insert(p.Video[0].Clips, at, c)
	} else {
		c.Start = fit(taken(p.Video[track].Clips, -1, clipSpan), c.Length(), start)
		p.Video[track].Clips = append(p.Video[track].Clips, c)
		p.sortTrack(track)
	}
	p.layout()
	p.prune()
	return true
}

// prune drops empty upper video tracks and empty overlay and music tracks, keeping at least one of each kind.
func (p *Project) prune() {
	for k := len(p.Video) - 1; k >= 1; k-- {
		if len(p.Video[k].Clips) == 0 {
			p.Video = slices.Delete(p.Video, k, k+1)
		}
	}
	for k := len(p.Overlays) - 1; k >= 0 && len(p.Overlays) > 1; k-- {
		if len(p.Overlays[k].Items) == 0 {
			p.Overlays = slices.Delete(p.Overlays, k, k+1)
		}
	}
	for k := len(p.Music) - 1; k >= 0 && len(p.Music) > 1; k-- {
		if len(p.Music[k].Clips) == 0 {
			p.Music = slices.Delete(p.Music, k, k+1)
		}
	}
}
