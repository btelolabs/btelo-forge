package project

import (
	"math"
	"slices"
)

// span is a stretch of a track taken by one item.
type span struct{ start, end float64 }

// taken lists where the items of a track other than skip sit, in time order.
func taken[T any](items []T, skip int, at func(T) (float64, float64)) []span {
	out := make([]span, 0, len(items))
	for i, it := range items {
		if i == skip {
			continue
		}
		s, e := at(it)
		out = append(out, span{s, e})
	}
	slices.SortFunc(out, func(a, b span) int {
		switch {
		case a.start < b.start:
			return -1
		case a.start > b.start:
			return 1
		}
		return 0
	})
	return out
}

// gap is the free stretch around t: from the end of the item before it to the start of the item after it.
func gap(occupied []span, t float64) (lo, hi float64) {
	lo, hi = 0, math.Inf(1)
	for _, s := range occupied {
		if s.end <= t+1e-9 {
			lo = math.Max(lo, s.end)
		} else if s.start >= t-1e-9 {
			hi = math.Min(hi, s.start)
			break
		}
	}
	return lo, hi
}

// fit places an item of the given length as close to want as the free gaps allow.
func fit(occupied []span, length, want float64) float64 {
	want = math.Max(want, 0)
	best, dist := math.Inf(1), math.Inf(1)
	try := func(lo, hi float64) {
		if hi-lo < length-1e-9 {
			return
		}
		s := math.Min(math.Max(want, lo), hi-length)
		if d := math.Abs(s - want); d < dist {
			best, dist = s, d
		}
	}
	prev := 0.0
	for _, s := range occupied {
		try(prev, s.start)
		prev = math.Max(prev, s.end)
	}
	try(prev, math.Inf(1))
	return best
}

// free reports whether [start, start+length) touches none of occupied.
func free(occupied []span, start, length float64) bool {
	for _, s := range occupied {
		if start < s.end-1e-9 && start+length > s.start+1e-9 {
			return false
		}
	}
	return start >= -1e-9
}
