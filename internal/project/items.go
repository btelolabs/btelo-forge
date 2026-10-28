package project

import (
	"math"
	"slices"
)

func musicSpan(m Music) (float64, float64)     { return m.Start, m.End() }
func overlaySpan(o Overlay) (float64, float64) { return o.Start, o.End }

// FindMusic returns the track and index of the music clip with id.
func (p *Project) FindMusic(id int) (track, index int, ok bool) {
	for k, t := range p.Music {
		for i, m := range t.Clips {
			if m.ID == id {
				return k, i, true
			}
		}
	}
	return 0, 0, false
}

// FindOverlay returns the track and index of the overlay with id.
func (p *Project) FindOverlay(id int) (track, index int, ok bool) {
	for k, t := range p.Overlays {
		for i, o := range t.Items {
			if o.ID == id {
				return k, i, true
			}
		}
	}
	return 0, 0, false
}

// Overlay returns the overlay with id.
func (p *Project) Overlay(id int) (Overlay, bool) {
	if k, i, ok := p.FindOverlay(id); ok {
		return p.Overlays[k].Items[i], true
	}
	return Overlay{}, false
}

// AddMusic places a file of the given total length at t, filling up to the end of the video, on the first music
// track with room; it returns the clip's id.
func (p *Project) AddMusic(path, title, credit string, total, t float64) int {
	t = math.Max(t, 0)
	length := total
	if rest := p.Duration() - t; rest >= MinLength {
		length = math.Min(total, rest)
	}
	m := Music{ID: p.id(), Path: path, Title: title, Credit: credit, Start: t, Length: math.Max(length, MinLength), Total: total, Volume: 1, Fade: true}
	k := slices.IndexFunc(p.Music, func(tr MusicTrack) bool { return free(taken(tr.Clips, -1, musicSpan), t, m.Length) })
	if k < 0 {
		p.Music = append(p.Music, MusicTrack{Volume: 1})
		k = len(p.Music) - 1
	}
	p.Music[k].Clips = append(p.Music[k].Clips, m)
	p.sortMusic(k)
	return m.ID
}

func (p *Project) sortMusic(k int) {
	slices.SortFunc(p.Music[k].Clips, func(a, b Music) int { return compare(a.Start, b.Start) })
}

func (p *Project) sortOverlays(k int) {
	slices.SortFunc(p.Overlays[k].Items, func(a, b Overlay) int { return compare(a.Start, b.Start) })
}

// MoveMusic moves clip id to track (len(Music) adds a track) into the free gap nearest start.
func (p *Project) MoveMusic(id, track int, start float64) bool {
	k, i, ok := p.FindMusic(id)
	if !ok || track < 0 || track > len(p.Music) {
		return false
	}
	m := p.Music[k].Clips[i]
	p.Music[k].Clips = slices.Delete(p.Music[k].Clips, i, i+1)
	if track == len(p.Music) {
		p.Music = append(p.Music, MusicTrack{Volume: 1})
	}
	m.Start = fit(taken(p.Music[track].Clips, -1, musicSpan), m.Length, start)
	p.Music[track].Clips = append(p.Music[track].Clips, m)
	p.sortMusic(track)
	p.prune()
	return true
}

// TrimMusicStart moves clip id's left edge to start, using or restoring the file before its offset and stopping at
// the clip before it.
func (p *Project) TrimMusicStart(id int, start float64) {
	k, i, ok := p.FindMusic(id)
	if !ok {
		return
	}
	m := &p.Music[k].Clips[i]
	before, _ := gap(taken(p.Music[k].Clips, i, musicSpan), m.Start)
	end := m.End()
	start = math.Min(start, end-MinLength)
	start = math.Max(start, math.Max(before, m.Start-m.Offset))
	m.Offset += start - m.Start
	m.Start, m.Length = start, end-start
}

// TrimMusicEnd moves clip id's right edge to end, within the file and before the next clip.
func (p *Project) TrimMusicEnd(id int, end float64) {
	k, i, ok := p.FindMusic(id)
	if !ok {
		return
	}
	m := &p.Music[k].Clips[i]
	_, after := gap(taken(p.Music[k].Clips, i, musicSpan), m.Start)
	end = math.Min(end, after)
	m.Length = math.Min(math.Max(end-m.Start, MinLength), m.Total-m.Offset)
}

// SetMusic changes clip id's volume and fades.
func (p *Project) SetMusic(id int, volume float64, fade bool) {
	if k, i, ok := p.FindMusic(id); ok {
		p.Music[k].Clips[i].Volume = math.Min(math.Max(volume, 0), 2)
		p.Music[k].Clips[i].Fade = fade
	}
}

// DeleteMusic removes clip id.
func (p *Project) DeleteMusic(id int) bool {
	k, i, ok := p.FindMusic(id)
	if ok {
		p.Music[k].Clips = slices.Delete(p.Music[k].Clips, i, i+1)
		p.prune()
	}
	return ok
}

// SplitMusic cuts clip id in two at t.
func (p *Project) SplitMusic(id int, t float64) bool {
	k, i, ok := p.FindMusic(id)
	if !ok {
		return false
	}
	m := p.Music[k].Clips[i]
	if t-m.Start < MinLength || m.End()-t < MinLength {
		return false
	}
	left, right := m, m
	left.Length = t - m.Start
	right.ID, right.Start, right.Offset, right.Length = p.id(), t, m.Offset+(t-m.Start), m.End()-t
	left.Fade, right.Fade = m.Fade, m.Fade
	p.Music[k].Clips = slices.Replace(p.Music[k].Clips, i, i+1, left, right)
	return true
}

// placeOverlay puts o on the first overlay track with room for it (adding one when none has) and returns its id.
func (p *Project) placeOverlay(o Overlay) int {
	o.ID = p.id()
	k := slices.IndexFunc(p.Overlays, func(tr OverlayTrack) bool {
		return free(taken(tr.Items, -1, overlaySpan), o.Start, o.End-o.Start)
	})
	if k < 0 {
		p.Overlays = append(p.Overlays, OverlayTrack{})
		k = len(p.Overlays) - 1
	}
	p.Overlays[k].Items = append(p.Overlays[k].Items, o)
	p.sortOverlays(k)
	return o.ID
}

// span picks where a new overlay of the given length goes: at t, pulled back to end with the video when it can.
func (p *Project) span(t, length float64) (float64, float64) {
	t = math.Max(t, 0)
	end := t + length
	if d := p.Duration(); end > d && d-length >= 0 && t < d {
		end = math.Max(d, t+MinLength)
	}
	return t, end
}

// AddText places a caption at t lasting up to three seconds.
func (p *Project) AddText(body string, t float64) int {
	start, end := p.span(t, 3)
	return p.placeOverlay(Overlay{Kind: TextOverlay, Body: body, Start: start, End: end, X: 0.5, Y: 0.82, Size: 0.075, Color: [3]uint8{255, 255, 255}, Outline: true, Opacity: 1})
}

// AddImage places an image (width over height aspect) at t for five seconds, a third of the frame high.
func (p *Project) AddImage(path string, aspect, t float64) int {
	start, end := p.span(t, 5)
	return p.placeOverlay(Overlay{Kind: ImageOverlay, Image: path, Aspect: aspect, Start: start, End: end, X: 0.5, Y: 0.5, Size: 0.35, Opacity: 1})
}

// AddDrawing places a drawing over the whole frame at t for five seconds: image is its flattened picture, the
// size of the frame, and canvas the layered file it is drawn in.
func (p *Project) AddDrawing(image, canvas string, aspect, t float64) int {
	start, end := p.span(t, 5)
	return p.placeOverlay(Overlay{Kind: ImageOverlay, Image: image, Canvas: canvas, Aspect: aspect, Start: start, End: end,
		X: 0.5, Y: 0.5, Size: 1, Opacity: 1})
}

// AddCaptions adds timed text on a new overlay track of its own, styled as subtitles, and returns the track.
func (p *Project) AddCaptions(lines []Overlay) int {
	tr := OverlayTrack{}
	for _, l := range lines {
		if l.End-l.Start < MinLength {
			continue
		}
		tr.Items = append(tr.Items, Overlay{ID: p.id(), Kind: TextOverlay, Body: l.Body, Start: l.Start, End: l.End,
			X: 0.5, Y: 0.9, Size: 0.06, Color: [3]uint8{255, 255, 255}, Box: true, Opacity: 1})
	}
	if len(p.Overlays) == 1 && len(p.Overlays[0].Items) == 0 {
		p.Overlays[0] = tr
		return 0
	}
	p.Overlays = append(p.Overlays, tr)
	return len(p.Overlays) - 1
}

// MoveOverlay moves overlay id to track (len(Overlays) adds a track) into the free gap nearest start.
func (p *Project) MoveOverlay(id, track int, start float64) bool {
	k, i, ok := p.FindOverlay(id)
	if !ok || track < 0 || track > len(p.Overlays) {
		return false
	}
	o := p.Overlays[k].Items[i]
	p.Overlays[k].Items = slices.Delete(p.Overlays[k].Items, i, i+1)
	if track == len(p.Overlays) {
		p.Overlays = append(p.Overlays, OverlayTrack{})
	}
	length := o.End - o.Start
	o.Start = fit(taken(p.Overlays[track].Items, -1, overlaySpan), length, start)
	o.End = o.Start + length
	p.Overlays[track].Items = append(p.Overlays[track].Items, o)
	p.sortOverlays(track)
	p.prune()
	return true
}

// SetOverlayRange sets overlay id's span, keeping it at least MinLength long and clear of its neighbours.
func (p *Project) SetOverlayRange(id int, start, end float64) {
	k, i, ok := p.FindOverlay(id)
	if !ok {
		return
	}
	o := &p.Overlays[k].Items[i]
	lo, hi := gap(taken(p.Overlays[k].Items, i, overlaySpan), (o.Start+o.End)/2)
	start = math.Max(start, lo)
	end = math.Min(math.Max(end, start+MinLength), hi)
	start = math.Min(start, end-MinLength)
	o.Start, o.End = start, end
}

// UpdateOverlay replaces overlay id's look and position, keeping its id, kind and span.
func (p *Project) UpdateOverlay(id int, change func(*Overlay)) {
	if k, i, ok := p.FindOverlay(id); ok {
		o := p.Overlays[k].Items[i]
		change(&o)
		o.ID, o.Kind, o.Start, o.End = p.Overlays[k].Items[i].ID, p.Overlays[k].Items[i].Kind, p.Overlays[k].Items[i].Start, p.Overlays[k].Items[i].End
		o.X, o.Y = math.Min(math.Max(o.X, 0), 1), math.Min(math.Max(o.Y, 0), 1)
		p.Overlays[k].Items[i] = o
	}
}

// DeleteOverlay removes overlay id.
func (p *Project) DeleteOverlay(id int) bool {
	k, i, ok := p.FindOverlay(id)
	if ok {
		p.Overlays[k].Items = slices.Delete(p.Overlays[k].Items, i, i+1)
		p.prune()
	}
	return ok
}

// SplitOverlay cuts overlay id in two at t; both halves keep the same content.
func (p *Project) SplitOverlay(id int, t float64) bool {
	k, i, ok := p.FindOverlay(id)
	if !ok {
		return false
	}
	o := p.Overlays[k].Items[i]
	if t-o.Start < MinLength || o.End-t < MinLength {
		return false
	}
	left, right := o, o
	left.End = t
	right.ID, right.Start = p.id(), t
	p.Overlays[k].Items = slices.Replace(p.Overlays[k].Items, i, i+1, left, right)
	return true
}

// SetTrackVolume sets a video (music false) or music track's gain and mute.
func (p *Project) SetTrackVolume(music bool, track int, volume float64, muted bool) {
	volume = math.Min(math.Max(volume, 0), 2)
	if music {
		if track >= 0 && track < len(p.Music) {
			p.Music[track].Volume, p.Music[track].Muted = volume, muted
		}
		return
	}
	if track >= 0 && track < len(p.Video) {
		p.Video[track].Volume, p.Video[track].Muted = volume, muted
	}
}

// Visible lists the overlays showing at t, bottom first.
func (p *Project) Visible(t float64) []Overlay {
	var out []Overlay
	for _, tr := range p.Overlays {
		if tr.Hidden {
			continue
		}
		for _, o := range tr.Items {
			if t >= o.Start && t < o.End {
				out = append(out, o)
			}
		}
	}
	return out
}
