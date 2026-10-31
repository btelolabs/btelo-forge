package scene3d

import (
	"math"
	"slices"
)

// WorldsAt is every node's transform in the world at time t of clip, the asset's own pose left as it is. Nodes the clip
// does not move stay where their Translation, Rotation and Scale put them.
func (a *Asset) WorldsAt(clip int, t float32) []Mat4 { return worldsOf(a.NodesAt(clip, t), a.Roots) }

// NodesAt is the nodes with their places at time t of clip, or as they stand for -1.
func (a *Asset) NodesAt(clip int, t float32) []Node {
	nodes := slices.Clone(a.Nodes)
	if clip < 0 {
		return nodes
	}
	c := &a.Clips[clip]
	for i := range c.Channels {
		ch := &c.Channels[i]
		if ch.Node < 0 || ch.Node >= len(nodes) || len(ch.Times) == 0 {
			continue
		}
		n := &nodes[ch.Node]
		switch ch.Path {
		case PathTranslation:
			n.Translation = Vec3(ch.sample(t))
		case PathScale:
			n.Scale = Vec3(ch.sample(t))
		case PathRotation:
			n.Rotation = ch.rotation(t)
		}
	}
	return nodes
}

// keys finds the two keys around t, how far between them it is, and the time between them. Before the first key and
// after the last it holds on to that key.
func (c *Channel) keys(t float32) (from, to int, share, span float32) {
	last := len(c.Times) - 1
	if t <= c.Times[0] {
		return 0, 0, 0, 0
	}
	if t >= c.Times[last] {
		return last, last, 0, 0
	}
	to, _ = slices.BinarySearch(c.Times, t)
	if c.Times[to] == t {
		return to, to, 0, 0
	}
	from = to - 1
	span = c.Times[to] - c.Times[from]
	return from, to, (t - c.Times[from]) / span, span
}

// sample is the first three numbers of the channel's value at t, which is all of a translation or a scale.
func (c *Channel) sample(t float32) [3]float32 {
	from, to, share, span := c.keys(t)
	var out [3]float32
	for i := range out {
		switch c.Interp {
		case CubicSpline:
			out[i] = c.spline(i, from, to, share, span)
		case Step:
			out[i] = c.Values[from*c.Width+i]
		default:
			a, b := c.Values[from*c.Width+i], c.Values[to*c.Width+i]
			out[i] = a + (b-a)*share
		}
	}
	return out
}

// spline is number i of a cubic spline's value: each key holds its way in, its value and its way out.
func (c *Channel) spline(i, from, to int, u, span float32) float32 {
	at := func(key, part int) float32 { return c.Values[(key*3+part)*c.Width+i] }
	if from == to {
		return at(from, 1)
	}
	u2, u3 := u*u, u*u*u
	return (2*u3-3*u2+1)*at(from, 1) + (u3-2*u2+u)*span*at(from, 2) + (-2*u3+3*u2)*at(to, 1) + (u3-u2)*span*at(to, 0)
}

func (c *Channel) rotation(t float32) Quat {
	from, to, share, span := c.keys(t)
	key := func(k int) Quat {
		v := c.Values[k*c.Width:]
		if c.Interp == CubicSpline {
			v = c.Values[(k*3+1)*c.Width:]
		}
		return Quat{v[0], v[1], v[2], v[3]}
	}
	switch c.Interp {
	case CubicSpline:
		var q Quat
		for i := range q {
			q[i] = c.spline(i, from, to, share, span)
		}
		return q.normal()
	case Step:
		return key(from).normal()
	}
	return slerp(key(from), key(to), share)
}

func (q Quat) normal() Quat {
	l := float32(math.Sqrt(float64(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])))
	if l == 0 {
		return Quat{0, 0, 0, 1}
	}
	return Quat{q[0] / l, q[1] / l, q[2] / l, q[3] / l}
}

// slerp goes the short way from a to b.
func slerp(a, b Quat, u float32) Quat {
	dot := a[0]*b[0] + a[1]*b[1] + a[2]*b[2] + a[3]*b[3]
	if dot < 0 {
		dot, b = -dot, Quat{-b[0], -b[1], -b[2], -b[3]}
	}
	wa, wb := 1-u, u
	if dot < .9995 {
		angle := math.Acos(float64(dot))
		sin := math.Sin(angle)
		wa = float32(math.Sin(float64(1-u)*angle) / sin)
		wb = float32(math.Sin(float64(u)*angle) / sin)
	}
	return Quat{wa*a[0] + wb*b[0], wa*a[1] + wb*b[1], wa*a[2] + wb*b[2], wa*a[3] + wb*b[3]}.normal()
}

// Bone is a joint of a skin and the joint it hangs from, as nodes; Parent is -1 for one that hangs from no joint.
type Bone struct{ Node, Parent int }

// Bones lists the skins' joints, each once.
func (a *Asset) Bones() []Bone {
	joint := map[int]bool{}
	var out []Bone
	for i := range a.Skins {
		for _, j := range a.Skins[i].Joints {
			joint[j] = true
		}
	}
	for j := range a.Nodes {
		if !joint[j] {
			continue
		}
		parent := a.Nodes[j].Parent
		for parent >= 0 && !joint[parent] {
			parent = a.Nodes[parent].Parent
		}
		out = append(out, Bone{j, parent})
	}
	return out
}

// strider is the topmost joint that clip carries across the ground from its start to its end, or -1: what a walk moves
// forward, which a game takes over and turns into the character's own motion.
func (a *Asset) strider(clip int) int {
	bones := a.Bones()
	if len(bones) == 0 {
		return -1
	}
	from, to := a.WorldsAt(clip, 0), a.WorldsAt(clip, a.Clips[clip].Duration())
	box := EmptyBox()
	for _, b := range bones {
		box.Grow(from[b.Node].Point(Vec3{}))
	}
	least := box.Size().Length() * .02
	depth := func(n int) int {
		d := 0
		for n = a.Nodes[n].Parent; n >= 0; n = a.Nodes[n].Parent {
			d++
		}
		return d
	}
	best := -1
	for _, b := range bones {
		d := to[b.Node].Point(Vec3{}).Sub(from[b.Node].Point(Vec3{}))
		if float32(math.Hypot(float64(d[0]), float64(d[2]))) > least && (best < 0 || depth(b.Node) < depth(best)) {
			best = b.Node
		}
	}
	return best
}

// WorldsInPlace is WorldsAt with what the clip carries across the ground taken out, so that the character stays on the
// spot and only its body moves.
func (a *Asset) WorldsInPlace(clip int, t float32) []Mat4 {
	worlds := a.WorldsAt(clip, t)
	n := a.strider(clip)
	if n < 0 {
		return worlds
	}
	d := worlds[n].Point(Vec3{}).Sub(a.WorldsAt(clip, 0)[n].Point(Vec3{}))
	back := Compose(Vec3{-d[0], 0, -d[2]}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1})
	for i := range worlds {
		worlds[i] = back.Mul(worlds[i])
	}
	return worlds
}

// KeysAt is the strength of the shape keys of each node that clip moves, at time t.
func (a *Asset) KeysAt(clip int, t float32) map[int][]float32 {
	var out map[int][]float32
	c := &a.Clips[clip]
	for i := range c.Channels {
		ch := &c.Channels[i]
		if ch.Path != PathWeights || len(ch.Times) == 0 || ch.Node < 0 || ch.Node >= len(a.Nodes) {
			continue
		}
		parts := 1
		if ch.Interp == CubicSpline {
			parts = 3
		}
		count := len(ch.Values) / (len(ch.Times) * parts)
		wide := *ch
		wide.Width = count
		if out == nil {
			out = map[int][]float32{}
		}
		out[ch.Node] = wide.weights(t)
	}
	return out
}

// weights is every number of the channel's value at t.
func (c *Channel) weights(t float32) []float32 {
	from, to, share, span := c.keys(t)
	out := make([]float32, c.Width)
	for i := range out {
		switch c.Interp {
		case CubicSpline:
			out[i] = c.spline(i, from, to, share, span)
		case Step:
			out[i] = c.Values[from*c.Width+i]
		default:
			a, b := c.Values[from*c.Width+i], c.Values[to*c.Width+i]
			out[i] = a + (b-a)*share
		}
	}
	return out
}
