package project

import (
	"errors"
	"slices"
)

// ErrMainTrackEmpty means a change would leave the main video track without a clip.
var ErrMainTrackEmpty = errors.New("the main video track must keep at least one clip")

// Has reports whether id is a clip, music clip or overlay of the edit.
func (p *Project) Has(id int) bool {
	_, _, clip := p.FindClip(id)
	_, _, music := p.FindMusic(id)
	_, _, overlay := p.FindOverlay(id)
	return clip || music || overlay
}

// DeleteItems removes every clip, music clip and overlay in ids and returns how many it removed. It removes
// nothing when that would leave the main track empty.
func (p *Project) DeleteItems(ids []int) (int, error) {
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	mainLeft := len(p.Video[0].Clips)
	for _, id := range ids {
		if k, _, ok := p.FindClip(id); ok && k == 0 {
			mainLeft--
		}
	}
	if mainLeft <= 0 {
		return 0, ErrMainTrackEmpty
	}
	n := 0
	for _, id := range ids {
		if p.DeleteClip(id) || p.DeleteMusic(id) || p.DeleteOverlay(id) {
			n++
		}
	}
	return n, nil
}

// SetVolumes sets the volume of every clip and music clip in ids (0 to 2) and returns how many have sound.
func (p *Project) SetVolumes(ids []int, v float64) int {
	v = min(max(v, 0), 2)
	n := 0
	for _, id := range ids {
		if k, i, ok := p.FindClip(id); ok {
			p.Video[k].Clips[i].Volume = v
			n++
		} else if k, i, ok := p.FindMusic(id); ok {
			p.Music[k].Clips[i].Volume = v
			n++
		}
	}
	return n
}

// SetOpacity sets the opacity of every clip and overlay in ids (0 to 1) and returns how many have a picture.
func (p *Project) SetOpacity(ids []int, v float64) int {
	v = min(max(v, 0), 1)
	n := 0
	for _, id := range ids {
		if k, i, ok := p.FindClip(id); ok {
			p.Video[k].Clips[i].Opacity = v
			n++
		} else if k, i, ok := p.FindOverlay(id); ok {
			p.Overlays[k].Items[i].Opacity = v
			n++
		}
	}
	return n
}

// Volume reads one item's volume; ok is false when the item has no sound.
func (p *Project) Volume(id int) (v float64, ok bool) {
	if c, ok := p.Clip(id); ok {
		return c.Volume, true
	}
	if k, i, ok := p.FindMusic(id); ok {
		return p.Music[k].Clips[i].Volume, true
	}
	return 0, false
}

// Opacity reads one item's opacity; ok is false when the item has no picture.
func (p *Project) Opacity(id int) (v float64, ok bool) {
	if c, ok := p.Clip(id); ok {
		return c.Opacity, true
	}
	if o, ok := p.Overlay(id); ok {
		return o.Opacity, true
	}
	return 0, false
}
