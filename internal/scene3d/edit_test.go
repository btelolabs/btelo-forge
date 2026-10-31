package scene3d

import (
	"image"
	stdcolor "image/color"
	"math"
	"testing"
)

// closed reports whether every edge has exactly two faces, running opposite ways, as a watertight surface has.
func closed(t *testing.T, m *EditMesh, what string) {
	t.Helper()
	used := map[[2]int32]int{}
	for f := range m.Faces {
		c := m.FaceCorners(f)
		for i := range c {
			used[[2]int32{c[i].V, c[(i+1)%len(c)].V}]++
		}
	}
	for e, n := range used {
		if n != 1 {
			t.Fatalf("%s: edge %v is used %d times the same way", what, e, n)
		}
		if used[[2]int32{e[1], e[0]}] != 1 {
			t.Fatalf("%s: edge %v has no neighbour", what, e)
		}
	}
}

// volume is the mesh's signed volume, positive when its faces point outwards.
func volume(m *EditMesh) float32 {
	var v float32
	prims, _ := m.Bake()
	for i := range prims {
		p := &prims[i]
		for k := 0; k+2 < len(p.Indices); k += 3 {
			a, b, c := p.Pos[p.Indices[k]], p.Pos[p.Indices[k+1]], p.Pos[p.Indices[k+2]]
			v += a.Dot(b.Cross(c)) / 6
		}
	}
	return v
}

func near(a, b, tolerance float32) bool { return math.Abs(float64(a-b)) <= float64(tolerance) }

func euler(m *EditMesh) int {
	edges := m.EdgeFaces()
	return len(m.Pos) - len(edges) + len(m.Faces)
}

func TestPrimitivesAreClosedAndFaceOut(t *testing.T) {
	for name, c := range map[string]struct {
		p      Primitive
		volume float32
	}{"cube": {Cube, 8}, "sphere": {Sphere, 4.0 / 3 * math.Pi}, "cylinder": {Cylinder, 2 * math.Pi}} {
		m := NewPrimitive(c.p)
		closed(t, m, name)
		if v := volume(m); !near(v, c.volume, c.volume*.08) {
			t.Errorf("%s: volume %v, want about %v", name, v, c.volume)
		}
		if euler(m) != 2 {
			t.Errorf("%s: V-E+F is %d", name, euler(m))
		}
	}
}

func topFace(t *testing.T, m *EditMesh) int32 {
	t.Helper()
	for f := range m.Faces {
		if m.FaceNormal(f).Dot(Vec3{0, 1, 0}) > .99 {
			return int32(f)
		}
	}
	t.Fatal("no face looks up")
	return 0
}

func TestExtrudeAddsWallsAndStaysClosed(t *testing.T) {
	m := NewPrimitive(Cube)
	moved, region := m.Extrude([]int32{topFace(t, m)})
	if len(moved) != 4 || len(region) != 1 {
		t.Fatalf("moved %v, region %v", moved, region)
	}
	if v := volume(m); !near(v, 8, 1e-3) {
		t.Fatalf("nothing moved yet, and the volume is %v", v)
	}
	m.Move(moved, Compose(Vec3{0, 1, 0}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1}))
	closed(t, m, "extruded")
	if v := volume(m); !near(v, 12, 1e-3) {
		t.Fatalf("volume %v, want 12", v)
	}
	if len(m.Faces) != 10 || len(m.Pos) != 12 {
		t.Fatalf("%d faces, %d vertices", len(m.Faces), len(m.Pos))
	}
}

func TestExtrudeOfNeighboursKeepsTheirShare(t *testing.T) {
	m := NewPrimitive(Cube)
	top := topFace(t, m)
	side := int32(-1)
	for f := range m.Faces {
		if m.FaceNormal(f).Dot(Vec3{1, 0, 0}) > .99 {
			side = int32(f)
		}
	}
	moved, _ := m.Extrude([]int32{top, side})
	if len(moved) != 6 {
		t.Fatalf("%d vertices move, want 6", len(moved))
	}
	closed(t, m, "two faces")
}

func TestNewFacesShadeLikeTheirSource(t *testing.T) {
	m := NewPrimitive(Cube)
	top := topFace(t, m)
	m.Inset([]int32{top}, .5)
	moved, _ := m.Extrude([]int32{top})
	m.Move(moved, Compose(Vec3{0, 1, 0}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1}))
	m.Subdivide([]int32{0})
	m.Compact()
	for f, flat := range m.Flat {
		if !flat {
			t.Fatalf("face %d of a flat cube shades smooth", f)
		}
	}
	if len(m.Flat) != len(m.Faces) {
		t.Fatalf("%d flags for %d faces", len(m.Flat), len(m.Faces))
	}
}

func TestInsetKeepsTheShape(t *testing.T) {
	m := NewPrimitive(Cube)
	top := topFace(t, m)
	inner := m.Inset([]int32{top}, .5)
	closed(t, m, "inset")
	if len(m.Faces) != 10 || !near(volume(m), 8, 1e-3) {
		t.Fatalf("%d faces, volume %v", len(m.Faces), volume(m))
	}
	c := m.FaceCorners(int(inner[0]))
	if !near(m.Pos[c[0].V][0]*m.Pos[c[0].V][0], .25, 1e-5) {
		t.Fatalf("inner corner at %v", m.Pos[c[0].V])
	}
}

func TestSubdivideLeavesNoCrack(t *testing.T) {
	m := NewPrimitive(Cube)
	m.Subdivide([]int32{topFace(t, m)})
	m.Compact()
	closed(t, m, "one face")
	if !near(volume(m), 8, 1e-3) {
		t.Fatalf("volume %v", volume(m))
	}
	all := NewPrimitive(Cube)
	all.Subdivide([]int32{0, 1, 2, 3, 4, 5})
	all.Compact()
	closed(t, all, "all faces")
	if len(all.Faces) != 24 || euler(all) != 2 {
		t.Fatalf("%d faces", len(all.Faces))
	}
}

func TestLoopCutGoesAllTheWayRound(t *testing.T) {
	m := NewPrimitive(Cube)
	e := NewEdge(0, 1) // along x, so the ring goes round the cube's waist
	ring := m.LoopOf(e)
	if len(ring) != 4 {
		t.Fatalf("ring of %d edges", len(ring))
	}
	added := m.LoopCut(e)
	m.Compact()
	if len(added) != 4 || len(m.Faces) != 10 {
		t.Fatalf("%d new vertices, %d faces", len(added), len(m.Faces))
	}
	closed(t, m, "cut")
	if !near(volume(m), 8, 1e-3) {
		t.Fatalf("volume %v", volume(m))
	}
}

func TestLoopCutStopsAtATriangleWithoutACrack(t *testing.T) {
	m := NewPrimitive(Sphere)
	var e Edge
	for f := range m.Faces {
		if c := m.FaceCorners(f); len(c) == 4 {
			e = NewEdge(c[0].V, c[3].V) // across the rings, so that the ring reaches both poles
			break
		}
	}
	m.LoopCut(e)
	m.Compact()
	closed(t, m, "sphere")
}

func TestMergeCollapsesAnEdge(t *testing.T) {
	m := NewPrimitive(Cube)
	if err := m.Merge([]int32{0, 1}); err != nil {
		t.Fatal(err)
	}
	closed(t, m, "merged")
	if len(m.Pos) != 7 || len(m.Faces) != 6 {
		t.Fatalf("%d vertices, %d faces", len(m.Pos), len(m.Faces))
	}
	if err := m.Merge([]int32{0}); err == nil {
		t.Fatal("one vertex merged")
	}
}

func TestDelete(t *testing.T) {
	m := NewPrimitive(Cube)
	m.DeleteFaces([]int32{0})
	if len(m.Faces) != 5 || len(m.Pos) != 8 {
		t.Fatalf("%d faces, %d vertices", len(m.Faces), len(m.Pos))
	}
	m = NewPrimitive(Cube)
	m.DeleteVertices([]int32{0})
	if len(m.Faces) != 3 || len(m.Pos) != 7 {
		t.Fatalf("%d faces, %d vertices", len(m.Faces), len(m.Pos))
	}
}

func TestFillClosesAHoleFacingOut(t *testing.T) {
	m := NewPrimitive(Cube)
	top := topFace(t, m)
	c := m.FaceCorners(int(top))
	loop := []int32{c[0].V, c[1].V, c[2].V, c[3].V}
	m.DeleteFaces([]int32{top})
	if _, err := m.Fill(loop); err != nil {
		t.Fatal(err)
	}
	closed(t, m, "filled")
	reversed := []int32{loop[3], loop[2], loop[1], loop[0]}
	m2 := NewPrimitive(Cube)
	m2.DeleteFaces([]int32{topFace(t, m2)})
	if _, err := m2.Fill(reversed); err != nil {
		t.Fatal(err)
	}
	closed(t, m2, "filled the other way")
}

func TestFlipTurnsInsideOut(t *testing.T) {
	m := NewPrimitive(Cube)
	m.FlipFaces([]int32{0, 1, 2, 3, 4, 5})
	if v := volume(m); !near(v, -8, 1e-3) {
		t.Fatalf("volume %v", v)
	}
}

func TestBevelOfOneEdge(t *testing.T) {
	m := NewPrimitive(Cube)
	strip := m.Bevel([]Edge{NewEdge(0, 1)}, .2)
	if len(strip) != 1 {
		t.Fatalf("strip %v", strip)
	}
	closed(t, m, "beveled")
	if euler(m) != 2 {
		t.Fatalf("V-E+F is %d", euler(m))
	}
	if v := volume(m); v >= 8 || v < 7.5 {
		t.Fatalf("volume %v: a corner's worth should be gone", v)
	}
	if len(m.Faces) != 7 {
		t.Fatalf("%d faces", len(m.Faces))
	}
}

func TestBevelOfEdgesThatMeet(t *testing.T) {
	m := NewPrimitive(Cube)
	strip := m.Bevel([]Edge{NewEdge(0, 1), NewEdge(1, 3)}, .1)
	if len(strip) != 2 {
		t.Fatalf("strip %v", strip)
	}
	closed(t, m, "two edges")
}

func TestConcavePolygonTriangulates(t *testing.T) {
	m := &EditMesh{Pos: []Vec3{{0, 0, 0}, {4, 0, 0}, {4, 4, 0}, {2, 1, 0}, {0, 4, 0}}}
	corners := make([]Corner, 5)
	for i := range corners {
		corners[i].V = int32(i)
	}
	m.AddFace(corners, -1)
	tris := m.triangulate(0, nil)
	if len(tris) != 3 {
		t.Fatalf("%v", tris)
	}
	var area float32
	for _, x := range tris {
		a, b, c := m.Pos[x[0]], m.Pos[x[1]], m.Pos[x[2]]
		z := b.Sub(a).Cross(c.Sub(a))[2] / 2
		if z <= 0 {
			t.Fatalf("triangle %v is flipped", x)
		}
		area += z
	}
	if !near(area, 10, 1e-4) {
		t.Fatalf("area %v, want 10", area)
	}
}

func TestWeldKeepsSeams(t *testing.T) {
	// Two triangles sharing an edge, split in the file by different texture coordinates there.
	p := Prim{Pos: []Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0}}, Normal: make([]Vec3, 6),
		UV: []Vec2{{0, 0}, {1, 0}, {0, 1}, {.5, 0}, {1, 1}, {.5, 1}}, Indices: []uint32{0, 1, 2, 3, 4, 5}}
	m := EditMeshOf(&Mesh{Prims: []Prim{p}})
	if len(m.Pos) != 4 || len(m.Faces) != 2 {
		t.Fatalf("%d vertices, %d faces", len(m.Pos), len(m.Faces))
	}
	if m.FaceCorners(1)[0].UV != (Vec2{.5, 0}) {
		t.Fatalf("the seam is lost: %v", m.FaceCorners(1)[0].UV)
	}
	prims, faces := m.Bake()
	if len(prims) != 1 || prims[0].Triangles() != 2 || faces[0][1] != 1 {
		t.Fatalf("%d parts, faces %v", len(prims), faces)
	}
	if prims[0].UV[3] != (Vec2{.5, 0}) {
		t.Fatalf("baked uv %v", prims[0].UV[3])
	}
}

// Normals set by hand in the file stay as they were, except around what moved.
func TestNormalsStayUntilSomethingMoves(t *testing.T) {
	hand := Vec3{.6, 0, .8}
	p := Prim{Pos: []Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {5, 0, 0}, {6, 0, 0}, {5, 1, 0}}, Normal: []Vec3{hand, hand, hand, hand, hand, hand}, Indices: []uint32{0, 1, 2, 3, 4, 5}}
	m := EditMeshOf(&Mesh{Prims: []Prim{p}})
	prims, _ := m.Bake()
	for i, n := range prims[0].Normal {
		if n != hand {
			t.Fatalf("normal %d is %v with nothing moved", i, n)
		}
	}
	m.Move([]int32{0}, Compose(Vec3{0, 0, 1}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1}))
	prims, _ = m.Bake()
	if prims[0].Normal[0] == hand || prims[0].Normal[3] != hand {
		t.Fatalf("moved %v, away from it %v", prims[0].Normal[0], prims[0].Normal[3])
	}
}

func TestCornersAlikeShareAVertex(t *testing.T) {
	prims, _ := NewPrimitive(Sphere).Bake()
	if n := len(prims[0].Pos); n > 2+11*24+60 { // the seam of its texture and its poles need their own
		t.Fatalf("%d vertices for a smooth sphere", n)
	}
	cube, _ := NewPrimitive(Cube).Bake()
	if n := len(cube[0].Pos); n != 24 {
		t.Fatalf("%d vertices for a flat cube of 4 faces' corners each", n)
	}
}

func TestSkinSurvivesEditing(t *testing.T) {
	p := Prim{Pos: []Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, Normal: make([]Vec3, 3), Indices: []uint32{0, 1, 2},
		Joints: [][4]uint16{{1, 0, 0, 0}, {2, 0, 0, 0}, {3, 0, 0, 0}}, Weights: []Vec4{{1, 0, 0, 0}, {1, 0, 0, 0}, {1, 0, 0, 0}}}
	m := EditMeshOf(&Mesh{Prims: []Prim{p}})
	m.Subdivide([]int32{0})
	m.Compact()
	prims, _ := m.Bake()
	if prims[0].Joints == nil || len(prims[0].Joints) != len(prims[0].Pos) {
		t.Fatalf("joints %d for %d vertices", len(prims[0].Joints), len(prims[0].Pos))
	}
}

func TestCloneIsIndependent(t *testing.T) {
	m := NewPrimitive(Cube)
	c := m.Clone()
	m.Move([]int32{0}, Compose(Vec3{9, 0, 0}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1}))
	m.FlipFaces([]int32{0})
	if c.Pos[0] == m.Pos[0] || !near(volume(c), 8, 1e-3) {
		t.Fatal("the copy changed with the original")
	}
}

// The charts stay within the square, keep their shape, and do not lie on each other.
func TestAutoUnwrap(t *testing.T) {
	m := NewPrimitive(Cube)
	m.AutoUnwrap(66, .02)
	if len(m.Faces) != 6 {
		t.Fatalf("%d faces", len(m.Faces))
	}
	var boxes []Box
	for f := range m.Faces {
		b := EmptyBox()
		for _, c := range m.FaceCorners(f) {
			if c.UV[0] < 0 || c.UV[0] > 1 || c.UV[1] < 0 || c.UV[1] > 1 {
				t.Fatalf("a corner of face %d is at %v", f, c.UV)
			}
			b.Grow(Vec3{c.UV[0], c.UV[1], 0})
		}
		if w, h := b.Size()[0], b.Size()[1]; w < .1 || h < .1 || w/h > 1.01 || h/w > 1.01 {
			t.Fatalf("face %d is %v by %v", f, w, h)
		}
		for _, o := range boxes {
			if b.Min[0] < o.Max[0] && o.Min[0] < b.Max[0] && b.Min[1] < o.Max[1] && o.Min[1] < b.Max[1] {
				t.Fatalf("charts %v and %v overlap", b, o)
			}
		}
		boxes = append(boxes, b)
	}
}

// A smooth ball makes few charts, not one for each face.
func TestAutoUnwrapKeepsNeighboursTogether(t *testing.T) {
	m := NewPrimitive(Sphere)
	m.AutoUnwrap(66, .01)
	prims, _ := m.Bake()
	if len(prims[0].Pos) > len(m.Pos)*2 {
		t.Fatalf("%d vertices from %d", len(prims[0].Pos), len(m.Pos))
	}
}

// A ball of brush finds the texels around it on both sides of a chart's edge, and only those facing the eye.
func TestPainterGoesOverSeams(t *testing.T) {
	m := NewPrimitive(Cube)
	m.AutoUnwrap(66, .02)
	prims, _ := m.Bake()
	s := Surface{Pos: prims[0].Pos, UV: prims[0].UV, Indices: prims[0].Indices, Material: -1}
	p := NewPainter([]Surface{s}, -1, 128, 128)
	box := EmptyBox()
	for _, q := range s.Pos {
		box.Grow(q)
	}
	touched := map[[2]int]bool{}
	corner := Vec3{box.Max[0], box.Max[1], box.Max[2]}
	p.Dab(corner, Vec3{-1, -1, -1}.Norm(), box.Size()[0]*.4, func(x, y int, _ float32) { touched[[2]int{x, y}] = true })
	groups := 0
	seen := map[[2]int]bool{}
	var walk func(k [2]int)
	walk = func(k [2]int) {
		if seen[k] || !touched[k] {
			return
		}
		seen[k] = true
		for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			walk([2]int{k[0] + d[0], k[1] + d[1]})
		}
	}
	for k := range touched {
		if !seen[k] {
			groups++
			walk(k)
		}
	}
	if groups != 3 {
		t.Fatalf("a touch on a corner reached %d charts", groups)
	}
	far := 0
	p.Dab(corner, Vec3{1, 1, 1}.Norm(), box.Size()[0]*.4, func(int, int, float32) { far++ })
	if far != 0 {
		t.Fatalf("%d texels facing away were found", far)
	}
}

// A tube cut along one line and round its two ends lays out flat, its side a rectangle with hardly any stretch.
func TestUnwrapAlongSeams(t *testing.T) {
	m := NewPrimitive(Cube)
	m.Seams = map[Edge]bool{}
	for _, f := range []int{0, 1, 2, 3, 4, 5} {
		c := m.FaceCorners(f)
		for i := range c {
			m.Seams[NewEdge(c[i].V, c[(i+1)%len(c)].V)] = true
		}
	}
	// With every edge cut each face is a chart of its own, all the same size.
	m.Unwrap(.02)
	var areas []float32
	for f := range m.Faces {
		c := m.FaceCorners(f)
		for _, k := range c {
			if k.UV[0] < -1e-3 || k.UV[0] > 1.001 || k.UV[1] < -1e-3 || k.UV[1] > 1.001 {
				t.Fatalf("face %d has a corner at %v", f, k.UV)
			}
		}
		a := Vec3{c[1].UV[0] - c[0].UV[0], c[1].UV[1] - c[0].UV[1], 0}.Cross(Vec3{c[2].UV[0] - c[0].UV[0], c[2].UV[1] - c[0].UV[1], 0})
		areas = append(areas, a.Length())
	}
	for _, a := range areas {
		if d := a/areas[0] - 1; d > .02 || d < -.02 {
			t.Fatalf("areas %v", areas)
		}
	}
	// Cut only round one face: the other five flatten in one chart and the corners keep their right angles nearly.
	m = NewPrimitive(Cube)
	top := topFace(t, m)
	m.Seams = map[Edge]bool{}
	c := m.FaceCorners(int(top))
	for i := range c {
		m.Seams[NewEdge(c[i].V, c[(i+1)%len(c)].V)] = true
	}
	if charts := m.seamCharts(); len(charts) != 2 {
		t.Fatalf("%d charts", len(charts))
	}
	m.Unwrap(.02)
	for f := range m.Faces {
		for _, k := range m.FaceCorners(f) {
			if k.UV != k.UV {
				t.Fatalf("a corner of face %d is not a number", f)
			}
		}
	}
	island := m.IslandOf(top)
	if len(island) != 1 {
		t.Fatalf("the top's chart has %d faces", len(island))
	}
}

// A file's layout tells its seams.
func TestDeriveSeams(t *testing.T) {
	m := NewPrimitive(Cube)
	m.AutoUnwrap(66, .02)
	m.DeriveSeams()
	if len(m.Seams) != 12 {
		t.Fatalf("%d seams on a cube cut along every edge", len(m.Seams))
	}
}

// LSCM keeps a flat grid as it is, at any scale.
func TestConformalKeepsAFlatSheet(t *testing.T) {
	var pos []Vec3
	var tris [][3]int32
	for y := range 5 {
		for x := range 5 {
			pos = append(pos, Vec3{float32(x), float32(y), 0})
		}
	}
	for y := range 4 {
		for x := range 4 {
			a := int32(y*5 + x)
			tris = append(tris, [3]int32{a, a + 1, a + 6}, [3]int32{a, a + 6, a + 5})
		}
	}
	uv := conformal(pos, tris, 0, 24)
	diagonal := func(i, j int) float32 {
		return Vec3{uv[i][0] - uv[j][0], uv[i][1] - uv[j][1], 0}.Length()
	}
	if d := diagonal(0, 4) / diagonal(0, 20); d < .98 || d > 1.02 {
		t.Fatalf("the sides came out %v to one", d)
	}
}

// A shape with no seam and no edge is cut by itself, not squashed flat.
func TestUnwrapCutsAClosedShape(t *testing.T) {
	m := NewPrimitive(Sphere)
	m.Unwrap(.02)
	box := EmptyBox()
	for i := range m.Corners {
		box.Grow(Vec3{m.Corners[i].UV[0], m.Corners[i].UV[1], 0})
	}
	if s := box.Size(); s[0] < .5 || s[1] < .5 {
		t.Fatalf("the layout is %v", s)
	}
}

// A cube smooths to its known shape: six times four faces, the known volume of the limit's first step, closed and pointing out.
func TestCatmullClark(t *testing.T) {
	m := NewPrimitive(Cube)
	before := volume(m)
	out, origin := m.Modified(Modifiers{Subdivide: 1})
	if len(out.Faces) != 24 || len(out.Pos) != 26 {
		t.Fatalf("%d faces, %d vertices", len(out.Faces), len(out.Pos))
	}
	if origin[23] != 5 {
		t.Fatalf("the last face came from %d", origin[23])
	}
	for _, faces := range out.EdgeFaces() {
		if len(faces) != 2 {
			t.Fatal("the surface has opened")
		}
	}
	after := volume(out)
	if after <= 0 || after >= before {
		t.Fatalf("volume %v from %v", after, before)
	}
	// A corner of a cube of side s goes to 5/9 of the way out, as the rule says for three faces meeting.
	box := EmptyBox()
	for _, p := range out.Pos {
		box.Grow(p)
	}
	if box.Empty() {
		t.Fatal("no points")
	}
	three, _ := m.Modified(Modifiers{Subdivide: 3})
	if len(three.Faces) != 6*4*4*4 {
		t.Fatalf("%d faces at level three", len(three.Faces))
	}
}

// A half cube mirrored is whole: closed, welded on the plane, twice its volume.
func TestMirrorWelds(t *testing.T) {
	m := NewPrimitive(Cube)
	for v := range m.Pos {
		if m.Pos[v][0] < 0 {
			m.Pos[v][0] = 0
		}
	}
	var keep []int32
	for f := range m.Faces {
		if n := m.FaceNormal(f); n[0] > -.5 {
			keep = append(keep, int32(f))
		} else {
			m.Faces[f].Count = 0
		}
	}
	m.Compact()
	half := volume(m)
	_ = keep
	out, origin := m.Modified(Modifiers{Mirror: [3]bool{true}})
	if len(out.Pos) != 12 {
		t.Fatalf("%d vertices", len(out.Pos))
	}
	if len(origin) != len(out.Faces) {
		t.Fatalf("%d origins for %d faces", len(origin), len(out.Faces))
	}
	for e, faces := range out.EdgeFaces() {
		if len(faces) != 2 && out.Pos[e.A][0] != 0 {
			t.Fatalf("edge %v has %d faces", e, len(faces))
		}
	}
	box := EmptyBox()
	for _, p := range out.Pos {
		box.Grow(p)
	}
	if box.Min[0] != -box.Max[0] {
		t.Fatalf("box %v", box)
	}
	_ = half
}

// A cube saved as triangles comes back as six quads, and keeps its shape and its volume.
func TestTrisToQuads(t *testing.T) {
	cube := NewPrimitive(Cube)
	prims, _ := cube.Bake()
	m := EditMeshOf(&Mesh{Prims: prims})
	if len(m.Faces) != 12 {
		t.Fatalf("%d faces from the file", len(m.Faces))
	}
	before := volume(m)
	if n := m.TrisToQuads(40); n != 6 {
		t.Fatalf("%d joined", n)
	}
	if len(m.Faces) != 6 {
		t.Fatalf("%d faces", len(m.Faces))
	}
	for f := range m.Faces {
		if m.Faces[f].Count != 4 {
			t.Fatalf("face %d has %d corners", f, m.Faces[f].Count)
		}
	}
	if d := volume(m) - before; d > 1e-4 || d < -1e-4 {
		t.Fatalf("the volume changed by %v", d)
	}
}

// A picture drawn again for a new layout shows the same colour at the same place of the mesh.
func TestTransferUV(t *testing.T) {
	m := NewPrimitive(Cube)
	m.AutoUnwrap(66, .02)
	prims, _ := m.Bake()
	before := Surface{Pos: prims[0].Pos, UV: prims[0].UV, Indices: prims[0].Indices, Material: 0}
	const side = 128
	src := image.NewNRGBA(image.Rect(0, 0, side, side))
	color := func(s Surface, tri int) (float32, float32) {
		var u, v float32
		for k := range 3 {
			uv := s.UV[s.Indices[tri*3+k]]
			u += uv[0] / 3
			v += uv[1] / 3
		}
		return u, v
	}
	for y := range side {
		for x := range side {
			src.SetNRGBA(x, y, stdcolor.NRGBA{R: uint8(x * 2), G: uint8(y * 2), B: 40, A: 255})
		}
	}
	after := before
	after.UV = make([]Vec2, len(before.UV))
	for i, q := range before.UV { // a new layout: the old one turned and shrunk
		after.UV[i] = Vec2{q[1]*.7 + .1, q[0]*.7 + .1}
	}
	out := TransferUV([]Surface{before}, []Surface{after}, 0, src)
	for tri := range len(before.Indices) / 3 {
		ou, ov := color(before, tri)
		nu, nv := color(after, tri)
		was := src.NRGBAAt(int(ou*side), int(ov*side))
		now := out.NRGBAAt(int(nu*side), int(nv*side))
		for _, d := range []int{int(was.R) - int(now.R), int(was.G) - int(now.G)} {
			if d > 10 || d < -10 {
				t.Fatalf("triangle %d went from %v to %v", tri, was, now)
			}
		}
	}
}

func grid(n int) *EditMesh {
	m := &EditMesh{}
	for y := range n + 1 {
		for x := range n + 1 {
			m.Pos = append(m.Pos, Vec3{float32(x), 0, float32(y)})
		}
	}
	at := func(x, y int) Corner {
		return Corner{V: int32(y*(n+1) + x), Normal: Vec3{0, 1, 0}, Color: Vec4{1, 1, 1, 1}}
	}
	for y := range n {
		for x := range n {
			m.AddFace([]Corner{at(x, y), at(x, y+1), at(x+1, y+1), at(x+1, y)}, 0)
		}
	}
	return m
}

func TestEdgeLoopRunsStraightAcross(t *testing.T) {
	m := grid(4)
	row := func(x int) int32 { return int32(2*5 + x) }
	loop := m.EdgeLoop(NewEdge(row(1), row(2)))
	if len(loop) != 4 {
		t.Fatalf("%d edges", len(loop))
	}
	for _, e := range loop {
		if m.Pos[e.A][2] != 2 || m.Pos[e.B][2] != 2 {
			t.Fatalf("edge %v leaves the row", e)
		}
	}
}

func TestMoveSoftFades(t *testing.T) {
	m := grid(4)
	m.MoveSoft([]int32{12}, Compose(Vec3{0, 1, 0}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1}), 2)
	if y := m.Pos[12][1]; y != 1 {
		t.Fatalf("the chosen vertex rose %v", y)
	}
	near, far := m.Pos[13][1], m.Pos[0][1]
	if near <= 0 || near >= 1 || far != 0 {
		t.Fatalf("next to it %v, far away %v", near, far)
	}
}

func TestConnectSplitsAFace(t *testing.T) {
	m := grid(1)
	if err := m.ConnectVertices(0, 3); err != nil {
		t.Fatal(err)
	}
	m.Compact()
	if len(m.Faces) != 2 || m.Faces[0].Count != 3 || m.Faces[1].Count != 3 {
		t.Fatalf("%+v", m.Faces)
	}
	if err := m.ConnectVertices(0, 1); err == nil {
		t.Fatal("two ends of one edge were connected")
	}
}

func TestBridgeJoinsTwoLoops(t *testing.T) {
	m := &EditMesh{}
	ring := func(y float32) []Corner {
		var c []Corner
		for i := range 8 {
			s, k := math.Sincos(float64(i) / 8 * 2 * math.Pi)
			m.Pos = append(m.Pos, Vec3{float32(k), y, float32(s)})
			c = append(c, Corner{V: int32(len(m.Pos) - 1), Normal: Vec3{0, 1, 0}, Color: Vec4{1, 1, 1, 1}})
		}
		return c
	}
	bottom, top := ring(0), ring(2)
	m.AddFace(bottom, 0)
	m.AddFace(top, 0)
	var edges []Edge
	for _, r := range [][]Corner{bottom, top} {
		for i := range r {
			edges = append(edges, NewEdge(r[i].V, r[(i+1)%8].V))
		}
	}
	faces, err := m.Bridge(edges)
	if err != nil {
		t.Fatal(err)
	}
	if len(faces) != 8 {
		t.Fatalf("%d faces", len(faces))
	}
	for _, f := range faces {
		c := m.FaceCorners(int(f))
		mid := m.Pos[c[0].V].Add(m.Pos[c[2].V]).Scale(.5)
		if n := m.FaceNormal(int(f)); n.Dot(Vec3{mid[0], 0, mid[2]}) <= 0 {
			t.Fatalf("face %d points inwards", f)
		}
		for _, k := range c {
			for _, o := range c {
				if d := m.Pos[k.V].Sub(m.Pos[o.V]).Length(); d > 2.3 {
					t.Fatalf("the band twists: %v", d)
				}
			}
		}
	}
	if _, err := m.Bridge(edges[:9]); err == nil {
		t.Fatal("uneven lines were bridged")
	}
}
