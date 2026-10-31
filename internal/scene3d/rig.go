package scene3d

import (
	"fmt"
	"math"
	"slices"
)

// AddBone adds a joint at the point head of the world to the first skin, under parent, or as the start of a skeleton for
// -1, and returns its node. A file without a skin gets one, which no mesh uses until Bind. Nodes are replaced, never
// changed in place, as the card may be reading them.
func (a *Asset) AddBone(parent int, head Vec3) int {
	worlds := a.Worlds()
	a.Nodes = slices.Clone(a.Nodes)
	n := len(a.Nodes)
	node := Node{Name: fmt.Sprintf("Bone %d", n), Parent: parent, Mesh: -1, Skin: -1, Rotation: Quat{0, 0, 0, 1}, Scale: Vec3{1, 1, 1}, Translation: head}
	world := Compose(head, Quat{0, 0, 0, 1}, Vec3{1, 1, 1})
	if parent >= 0 {
		local := worlds[parent].Inverse().Mul(world)
		node.Translation = local.Point(Vec3{})
		world = worlds[parent].Mul(Compose(node.Translation, Quat{0, 0, 0, 1}, Vec3{1, 1, 1}))
		a.Nodes[parent].Children = append(slices.Clone(a.Nodes[parent].Children), n)
	} else {
		a.Roots = append(slices.Clone(a.Roots), n)
	}
	a.Nodes = append(a.Nodes, node)
	a.Skins = slices.Clone(a.Skins)
	if len(a.Skins) == 0 {
		a.Skins = append(a.Skins, Skin{Name: "Skeleton"})
	}
	s := &a.Skins[0]
	s.Joints = append(slices.Clone(s.Joints), n)
	s.InverseBind = append(slices.Clone(s.InverseBind), world.Inverse())
	return n
}

// Bind ties the mesh of node to the first skin as the skeleton now stands: each vertex follows the bones it is nearest,
// the nearest most, four at most, and the joints' inverse bind is taken again from where they are.
func (a *Asset) Bind(node int) error {
	if len(a.Skins) == 0 || len(a.Skins[0].Joints) == 0 {
		return fmt.Errorf("there are no bones to bind to")
	}
	if a.Nodes[node].Mesh < 0 {
		return fmt.Errorf("the object has no mesh")
	}
	worlds := a.Worlds()
	a.Skins = slices.Clone(a.Skins)
	s := &a.Skins[0]
	s.InverseBind = make([]Mat4, len(s.Joints))
	mesh := worlds[node]
	if a.Nodes[node].Skin >= 0 {
		// A skinned mesh stands where its joints put it, not where its node is.
		mesh = Identity()
	}
	index := map[int]int{}
	for i, j := range s.Joints {
		index[j] = i
		s.InverseBind[i] = worlds[j].Inverse().Mul(mesh)
	}
	type segment struct {
		a, b  Vec3
		joint uint16
	}
	var segments []segment
	children := map[int]int{}
	for _, b := range a.Bones() {
		if p, ok := index[b.Parent]; ok {
			children[b.Parent]++
			segments = append(segments, segment{worlds[b.Parent].Point(Vec3{}), worlds[b.Node].Point(Vec3{}), uint16(p)})
		}
	}
	for j, i := range index {
		if children[j] == 0 {
			head := worlds[j].Point(Vec3{})
			segments = append(segments, segment{head, head, uint16(i)})
		}
	}
	slices.SortFunc(segments, func(x, y segment) int { return int(x.joint) - int(y.joint) })
	meshes := slices.Clone(a.Meshes)
	m := &meshes[a.Nodes[node].Mesh]
	m.Prims = slices.Clone(m.Prims)
	for i := range m.Prims {
		p := &m.Prims[i]
		p.Joints, p.Weights = make([][4]uint16, len(p.Pos)), make([]Vec4, len(p.Pos))
		for v := range p.Pos {
			at := mesh.Point(p.Pos[v])
			strength := map[uint16]float32{}
			for _, q := range segments {
				d := max(distanceToBone(at, q.a, q.b), 1e-5)
				w := 1 / (d * d * d * d)
				if w > strength[q.joint] {
					strength[q.joint] = w
				}
			}
			top := make([]uint16, 0, len(strength))
			for j := range strength {
				top = append(top, j)
			}
			slices.SortFunc(top, func(x, y uint16) int {
				if strength[x] != strength[y] {
					if strength[x] > strength[y] {
						return -1
					}
					return 1
				}
				return int(x) - int(y)
			})
			var sum float32
			for k := range min(len(top), 4) {
				p.Joints[v][k], p.Weights[v][k] = top[k], strength[top[k]]
				sum += strength[top[k]]
			}
			for k := range 4 {
				p.Weights[v][k] /= sum
			}
		}
	}
	a.Meshes = meshes
	a.Nodes = slices.Clone(a.Nodes)
	a.Nodes[node].Skin = 0
	return nil
}

func distanceToBone(p, a, b Vec3) float32 {
	ab := b.Sub(a)
	t := float32(0)
	if l := ab.Dot(ab); l > 0 {
		t = min(max(p.Sub(a).Dot(ab)/l, 0), 1)
	}
	return p.Sub(a.Add(ab.Scale(t))).Length()
}

// keyTolerance is how near in seconds a key has to be to count as at that time.
const keyTolerance = 1e-3

// baked is the channel with straight lines between its keys, for changing one: a spline's own handles would no longer
// fit, and it keeps the shape it had at each key.
func (c *Channel) baked() Channel {
	out := *c
	out.Times, out.Values = slices.Clone(c.Times), nil
	if c.Interp != CubicSpline {
		out.Values = slices.Clone(c.Values)
		return out
	}
	out.Interp = Linear
	for k := range c.Times {
		out.Values = append(out.Values, c.Values[(k*3+1)*c.Width:(k*3+2)*c.Width]...)
	}
	return out
}

// setKey puts values at time t, replacing a key there.
func (c *Channel) setKey(t float32, values []float32) {
	at, _ := slices.BinarySearch(c.Times, t)
	if at > 0 && t-c.Times[at-1] < keyTolerance {
		at--
	}
	if at < len(c.Times) && c.Times[at]-t < keyTolerance {
		copy(c.Values[at*c.Width:], values)
		return
	}
	c.Times = slices.Insert(c.Times, at, t)
	c.Values = slices.Insert(c.Values, at*c.Width, values...)
}

// KeyNode records the node's place and turn at time t of clip. The clip's lists are replaced, never changed in place.
func (a *Asset) KeyNode(clip, node int, t float32, at Vec3, turn Quat) {
	a.Clips = slices.Clone(a.Clips)
	c := &a.Clips[clip]
	c.Channels = slices.Clone(c.Channels)
	for _, want := range []struct {
		path   Path
		values []float32
	}{{PathTranslation, at[:]}, {PathRotation, turn[:]}} {
		i := slices.IndexFunc(c.Channels, func(ch Channel) bool { return ch.Node == node && ch.Path == want.path })
		if i < 0 {
			c.Channels = append(c.Channels, Channel{Node: node, Path: want.path, Width: len(want.values)})
			i = len(c.Channels) - 1
		}
		ch := c.Channels[i].baked()
		ch.setKey(t, want.values)
		c.Channels[i] = ch
	}
}

// UnkeyNode takes out the node's keys at time t, and reports whether there were any.
func (a *Asset) UnkeyNode(clip, node int, t float32) bool {
	a.Clips = slices.Clone(a.Clips)
	c := &a.Clips[clip]
	c.Channels = slices.Clone(c.Channels)
	found := false
	kept := c.Channels[:0:0]
	for i := range c.Channels {
		ch := c.Channels[i]
		if ch.Node == node && ch.Path != PathWeights {
			ch = ch.baked()
			for k := len(ch.Times) - 1; k >= 0; k-- {
				if d := ch.Times[k] - t; d < keyTolerance && d > -keyTolerance {
					ch.Times = slices.Delete(ch.Times, k, k+1)
					ch.Values = slices.Delete(ch.Values, k*ch.Width, (k+1)*ch.Width)
					found = true
				}
			}
			if len(ch.Times) == 0 {
				continue
			}
		}
		kept = append(kept, ch)
	}
	c.Channels = kept
	return found
}

// KeyTimes lists the times of the keys of clip, of node alone unless it is -1.
func (a *Asset) KeyTimes(clip, node int) []float32 {
	var out []float32
	c := &a.Clips[clip]
	for i := range c.Channels {
		if node < 0 || c.Channels[i].Node == node {
			out = append(out, c.Channels[i].Times...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SolveIK turns the bones above node, chain of them counting itself, for node to reach target, with nodes the pose to start
// from, which it changes (cyclic coordinate descent: each joint in turn swings what hangs from it towards the target). It
// reports the nodes it turned.
func (a *Asset) SolveIK(nodes []Node, node, chain int, target Vec3) []int {
	var joints []int
	for n := nodes[node].Parent; n >= 0 && len(joints)+1 < chain; n = nodes[n].Parent {
		joints = append(joints, n)
	}
	for range 24 {
		for _, j := range joints {
			worlds := worldsOf(nodes, a.Roots)
			tip := worlds[node].Point(Vec3{})
			if tip.Sub(target).Length() < 1e-5 {
				return joints
			}
			pivot := worlds[j].Point(Vec3{})
			from, to := tip.Sub(pivot).Norm(), target.Sub(pivot).Norm()
			axis := from.Cross(to)
			sin := axis.Length()
			if sin < 1e-6 {
				continue
			}
			half := math.Atan2(float64(sin), float64(from.Dot(to))) / 2
			s, c := math.Sincos(half)
			axis = axis.Scale(float32(s) / sin)
			turn := Quat{axis[0], axis[1], axis[2], float32(c)}
			up := Quat{0, 0, 0, 1}
			if p := nodes[j].Parent; p >= 0 {
				_, up, _ = Decompose(worlds[p])
			}
			back := Quat{-up[0], -up[1], -up[2], up[3]}
			nodes[j].Rotation = quatMul(quatMul(quatMul(back, turn), up), nodes[j].Rotation).normal()
		}
	}
	return joints
}

// PaintWeight changes how much vertices of one part of a mesh follow a joint of its skin: those at places within radius
// of center, by strength at the middle, fading to the edge, and negative to take away. A vertex's weights still add up to
// one, and one with four joints already gives up its weakest for a new one.
func PaintWeight(p *Prim, places []Vec3, joint uint16, center Vec3, radius, strength float32) {
	for v, place := range places {
		d := place.Sub(center).Length()
		if d > radius {
			continue
		}
		t := 1 - d/radius
		change := strength * t * t * (3 - 2*t)
		slot := -1
		weakest := 0
		for k := range 4 {
			if p.Weights[v][k] > 0 && p.Joints[v][k] == joint {
				slot = k
			}
			if p.Weights[v][k] < p.Weights[v][weakest] {
				weakest = k
			}
		}
		if slot < 0 {
			if change <= 0 {
				continue
			}
			slot = weakest
			p.Joints[v][slot], p.Weights[v][slot] = joint, 0
		}
		p.Weights[v][slot] = min(max(p.Weights[v][slot]+change, 0), 1)
		rest := float32(0)
		for k := range 4 {
			if k != slot {
				rest += p.Weights[v][k]
			}
		}
		left := 1 - p.Weights[v][slot]
		if rest <= 0 {
			p.Weights[v][slot] = 1 // nothing else holds it
			continue
		}
		for k := range 4 {
			if k != slot {
				p.Weights[v][k] *= left / rest
			}
		}
	}
}

// WeightOf is how much a vertex of a part follows a joint.
func WeightOf(p *Prim, v int, joint uint16) float32 {
	var sum float32
	for k := range 4 {
		if p.Joints[v][k] == joint {
			sum += p.Weights[v][k]
		}
	}
	return sum
}
