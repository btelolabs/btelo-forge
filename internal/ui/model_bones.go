package ui

import (
	"cmp"
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/render3d"
	"github.com/btelolabs/btelo-forge/internal/scene3d"
)

// The shape of a bone, as Blender's octahedral ones but slimmer: it is widest a little way from its head, then narrows to
// its tail.
const (
	boneWaist = .1  // how far along the bone it is widest
	boneWidth = .06 // the half width there, of its length
	boneLeaf  = .5  // a bone with nothing after it is this long, of the one before
	boneEdge  = .6  // half the width of the outline, in pixels
	boneThin  = .2  // and at most this share of the bone's half width on screen, so that a thin bone is not all outline
)

// boneShape is a bone to draw, from one joint's place to where it points.
type boneShape struct {
	head, tail, side scene3d.Vec3 // side is the joint's own sideways direction, to turn with it
	node             int
}

// skeletonShapes works out the bones in the pose at worlds. A joint has no tail of its own, so a bone points to the child
// that the longest chain goes through, or on along the bone before for a joint with no children; the other children are
// joined by the thin lines of links, as a pair of (from, to) points. A joint at the top of the skeleton gets links only:
// it is a handle on the ground for moving the whole, often far from the body, and as a bone it would be the biggest.
func skeletonShapes(as *scene3d.Asset, worlds []scene3d.Mat4) (shapes []boneShape, links [][2]scene3d.Vec3) {
	bones := as.Bones()
	children := map[int][]int{}
	parent := map[int]int{}
	for _, b := range bones {
		parent[b.Node] = b.Parent
		if b.Parent >= 0 {
			children[b.Parent] = append(children[b.Parent], b.Node)
		}
	}
	at := func(n int) scene3d.Vec3 { return worlds[n].Point(scene3d.Vec3{}) }
	var below func(n int) int
	below = func(n int) int {
		count := 1
		for _, c := range children[n] {
			count += below(c)
		}
		return count
	}
	for _, b := range bones {
		head := at(b.Node)
		if b.Parent < 0 && len(children[b.Node]) > 0 {
			for _, c := range children[b.Node] {
				links = append(links, [2]scene3d.Vec3{head, at(c)})
			}
			continue
		}
		var tail scene3d.Vec3
		if kids := children[b.Node]; len(kids) > 0 {
			main := kids[0]
			for _, c := range kids[1:] {
				if d := below(c) - below(main); d > 0 || d == 0 && at(c).Sub(head).Length() > at(main).Sub(head).Length() {
					main = c
				}
			}
			tail = at(main)
			for _, c := range kids {
				if c != main {
					links = append(links, [2]scene3d.Vec3{head, at(c)})
				}
			}
		} else if p := parent[b.Node]; p >= 0 {
			tail = head.Add(head.Sub(at(p)).Scale(boneLeaf))
		}
		if tail.Sub(head).Length() > 0 {
			shapes = append(shapes, boneShape{head, tail, worlds[b.Node].Dir(scene3d.Vec3{1, 0, 0}), b.Node})
		}
	}
	return shapes, links
}

// modelSkeleton draws the bones in the pose at worlds as seen by cam in a view w×h, far ones first as they are drawn over
// the body with no depth to go by: the faces towards the eye, shaded, and their edges as thin strips.
func modelSkeleton(as *scene3d.Asset, worlds []scene3d.Mat4, cam *render3d.Camera, w, h, scale float32, chosen int) (tris, lines []float32) {
	shapes, links := skeletonShapes(as, worlds)
	base := scene3d.Vec3{.76, .82, .84}
	link := [4]float32{.76, .82, .84, .75}
	for _, l := range links {
		lines = append(lines, l[0][0], l[0][1], l[0][2], l[1][0], l[1][1], l[1][2], link[0], link[1], link[2], link[3])
	}
	slices.SortFunc(shapes, func(a, b boneShape) int {
		da := a.head.Add(a.tail).Scale(.5).Sub(cam.Pos).Length()
		db := b.head.Add(b.tail).Scale(.5).Sub(cam.Pos).Length()
		return cmp.Compare(db, da)
	})
	vertex := func(p scene3d.Vec3, c [4]float32, dx, dy float32) {
		tris = append(tris, p[0], p[1], p[2], c[0], c[1], c[2], c[3], dx, dy)
	}
	edge := func(a, b scene3d.Vec3, half float32) {
		ax, ay, ok1 := cam.Project(a, w, h)
		bx, by, ok2 := cam.Project(b, w, h)
		length := float32(math.Hypot(float64(bx-ax), float64(by-ay)))
		if !ok1 || !ok2 || length < 1e-3 {
			return
		}
		// Across the edge on the screen, whose y goes the other way to the overlay's.
		dx, dy := (ay-by)/length*half, (ax-bx)/length*half
		c := [4]float32{.03, .04, .05, .9}
		vertex(a, c, dx, dy)
		vertex(b, c, dx, dy)
		vertex(b, c, -dx, -dy)
		vertex(a, c, dx, dy)
		vertex(b, c, -dx, -dy)
		vertex(a, c, -dx, -dy)
	}
	for _, s := range shapes {
		axis := s.tail.Sub(s.head)
		length := axis.Length()
		axis = axis.Scale(1 / length)
		u := s.side.Sub(axis.Scale(s.side.Dot(axis)))
		if u.Length() < 1e-4 {
			u = axis.Cross(scene3d.Vec3{0, 1, 0})
			if u.Length() < 1e-4 {
				u = axis.Cross(scene3d.Vec3{1, 0, 0})
			}
		}
		u = u.Norm()
		v := axis.Cross(u)
		waist := s.head.Add(axis.Scale(length * boneWaist))
		r := length * boneWidth
		ring := [4]scene3d.Vec3{waist.Add(u.Scale(r)), waist.Add(v.Scale(r)), waist.Sub(u.Scale(r)), waist.Sub(v.Scale(r))}
		middle := s.head.Add(s.tail).Scale(.5)
		type line struct{ a, b scene3d.Vec3 }
		seen := map[line]bool{}
		for _, tip := range []scene3d.Vec3{s.head, s.tail} {
			for i := range ring {
				p, q := ring[i], ring[(i+1)%4]
				n := q.Sub(p).Cross(tip.Sub(p)).Norm()
				centre := p.Add(q).Add(tip).Scale(1. / 3)
				if n.Dot(centre.Sub(middle)) < 0 {
					n = n.Scale(-1)
				}
				eye := cam.Pos.Sub(centre).Norm()
				if n.Dot(eye) <= 0 {
					continue
				}
				light := .55 + .45*n.Dot(eye)
				c := [4]float32{base[0] * light, base[1] * light, base[2] * light, 1}
				if s.node == chosen {
					c = [4]float32{1 * light, .55 * light, .1 * light, 1}
				}
				vertex(p, c, 0, 0)
				vertex(q, c, 0, 0)
				vertex(tip, c, 0, 0)
				for _, e := range []line{{p, q}, {q, tip}, {tip, p}} {
					if !seen[e] && !seen[line{e.b, e.a}] {
						seen[e] = true
					}
				}
			}
		}
		half := boneEdge * scale
		if x0, y0, ok := cam.Project(ring[0], w, h); ok {
			if x1, y1, ok := cam.Project(ring[2], w, h); ok {
				half = min(half, float32(math.Hypot(float64(x1-x0), float64(y1-y0)))/2*boneThin)
			}
		}
		for e := range seen {
			edge(e.a, e.b, half)
		}
	}
	return tris, lines
}
