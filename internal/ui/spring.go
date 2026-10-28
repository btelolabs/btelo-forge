package ui

import "math"

// spring is a value pulled toward a target as by a damped spring, for motion that eases in and out, carries on a
// little and settles as a thing with weight does, and that takes a new target midway without a jolt, keeping its
// speed.
type spring struct {
	pos, vel float64
}

// springStep is the longest step a spring is moved by at once: shorter than any frame, so a slow frame does not
// make it unstable.
const springStep = 1.0 / 480

// step moves the spring dt seconds toward target. freq is how many times a second it would swing were it not
// damped, which sets how quick it is; ratio is how damped it is, 1 settling without passing the target and less
// passing it a little before settling. It reports whether the spring is still moving; once it is not, it rests
// exactly on the target.
func (s *spring) step(target, dt, freq, ratio float64) bool {
	w := 2 * math.Pi * freq
	for dt > 0 {
		h := min(dt, springStep)
		dt -= h
		// Semi-implicit Euler: the speed first, then the position with the new speed, which keeps the energy
		// from growing.
		s.vel += (-w*w*(s.pos-target) - 2*ratio*w*s.vel) * h
		s.pos += s.vel * h
	}
	if math.Abs(s.pos-target) < 1e-4 && math.Abs(s.vel) < 1e-3 {
		s.pos, s.vel = target, 0
		return false
	}
	return true
}
