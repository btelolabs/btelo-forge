package scene3d

import "math"

// Vec2, Vec3 and Vec4 are points, directions and colours; a Quat is x, y, z, w.
type (
	Vec2 [2]float32
	Vec3 [3]float32
	Vec4 [4]float32
	Quat [4]float32
)

// Mat4 is a transform, column after column, as glTF and WGSL store it.
type Mat4 [16]float32

// Identity changes nothing.
func Identity() Mat4 { return Mat4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1} }

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a Vec3) Scale(s float32) Vec3 { return Vec3{a[0] * s, a[1] * s, a[2] * s} }
func (a Vec3) Dot(b Vec3) float32   { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a Vec3) Length() float32      { return float32(math.Sqrt(float64(a.Dot(a)))) }
func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

// Norm is a in length 1; a of no length stays as it is.
func (a Vec3) Norm() Vec3 {
	l := a.Length()
	if l == 0 {
		return a
	}
	return a.Scale(1 / l)
}

// Mul is a after b: b is applied to a point first.
func (a Mat4) Mul(b Mat4) Mat4 {
	var o Mat4
	for c := range 4 {
		for r := range 4 {
			for k := range 4 {
				o[c*4+r] += a[k*4+r] * b[c*4+k]
			}
		}
	}
	return o
}

// Point transforms a position.
func (m Mat4) Point(v Vec3) Vec3 {
	return Vec3{m[0]*v[0] + m[4]*v[1] + m[8]*v[2] + m[12], m[1]*v[0] + m[5]*v[1] + m[9]*v[2] + m[13], m[2]*v[0] + m[6]*v[1] + m[10]*v[2] + m[14]}
}

// Dir transforms a direction, without the translation.
func (m Mat4) Dir(v Vec3) Vec3 {
	return Vec3{m[0]*v[0] + m[4]*v[1] + m[8]*v[2], m[1]*v[0] + m[5]*v[1] + m[9]*v[2], m[2]*v[0] + m[6]*v[1] + m[10]*v[2]}
}

// Normal transforms a surface direction: by the inverse transpose, which stays right under uneven scales.
func (m Mat4) Normal(v Vec3) Vec3 {
	x := Vec3{m[0], m[1], m[2]}
	y := Vec3{m[4], m[5], m[6]}
	z := Vec3{m[8], m[9], m[10]}
	return y.Cross(z).Scale(v[0]).Add(z.Cross(x).Scale(v[1])).Add(x.Cross(y).Scale(v[2])).Norm().Scale(sign(x.Dot(y.Cross(z))))
}

func sign(v float32) float32 {
	if v < 0 {
		return -1
	}
	return 1
}

// Inverse undoes a transform of a rotation, a scale and a translation; a transform that flattens things has none, and
// gives the identity.
func (m Mat4) Inverse() Mat4 {
	x, y, z := Vec3{m[0], m[1], m[2]}, Vec3{m[4], m[5], m[6]}, Vec3{m[8], m[9], m[10]}
	det := m.Determinant()
	if det == 0 {
		return Identity()
	}
	a, b, c := y.Cross(z).Scale(1/det), z.Cross(x).Scale(1/det), x.Cross(y).Scale(1/det)
	t := Vec3{m[12], m[13], m[14]}
	return Mat4{a[0], b[0], c[0], 0, a[1], b[1], c[1], 0, a[2], b[2], c[2], 0, -a.Dot(t), -b.Dot(t), -c.Dot(t), 1}
}

// Determinant is negative for a transform that mirrors.
func (m Mat4) Determinant() float32 {
	return Vec3{m[0], m[1], m[2]}.Cross(Vec3{m[4], m[5], m[6]}).Dot(Vec3{m[8], m[9], m[10]})
}

// Compose is the transform of a translation, a rotation and a scale, applied scale first.
func Compose(t Vec3, r Quat, s Vec3) Mat4 {
	x, y, z, w := r[0], r[1], r[2], r[3]
	return Mat4{
		(1 - 2*(y*y+z*z)) * s[0], 2 * (x*y + z*w) * s[0], 2 * (x*z - y*w) * s[0], 0,
		2 * (x*y - z*w) * s[1], (1 - 2*(x*x+z*z)) * s[1], 2 * (y*z + x*w) * s[1], 0,
		2 * (x*z + y*w) * s[2], 2 * (y*z - x*w) * s[2], (1 - 2*(x*x+y*y)) * s[2], 0,
		t[0], t[1], t[2], 1}
}

// Decompose is Compose's inverse, for a transform that has a shear-free rotation and scale, as glTF requires.
func Decompose(m Mat4) (t Vec3, r Quat, s Vec3) {
	t = Vec3{m[12], m[13], m[14]}
	x, y, z := Vec3{m[0], m[1], m[2]}, Vec3{m[4], m[5], m[6]}, Vec3{m[8], m[9], m[10]}
	s = Vec3{x.Length(), y.Length(), z.Length()}
	if x.Cross(y).Dot(z) < 0 {
		s[0] = -s[0]
	}
	for i, c := range []*Vec3{&x, &y, &z} {
		if s[i] != 0 {
			*c = c.Scale(1 / s[i])
		}
	}
	// From the rotation matrix, by its largest term, which keeps the square root away from zero.
	root := func(v float32) float32 { return float32(math.Sqrt(float64(max(v, 0)))) / 2 }
	switch tr := x[0] + y[1] + z[2]; {
	case tr > 0:
		w := root(1 + tr)
		r = Quat{(y[2] - z[1]) / (4 * w), (z[0] - x[2]) / (4 * w), (x[1] - y[0]) / (4 * w), w}
	case x[0] >= y[1] && x[0] >= z[2]:
		q := root(1 + x[0] - y[1] - z[2])
		r = Quat{q, (y[0] + x[1]) / (4 * q), (z[0] + x[2]) / (4 * q), (y[2] - z[1]) / (4 * q)}
	case y[1] >= z[2]:
		q := root(1 - x[0] + y[1] - z[2])
		r = Quat{(y[0] + x[1]) / (4 * q), q, (z[1] + y[2]) / (4 * q), (z[0] - x[2]) / (4 * q)}
	default:
		q := root(1 - x[0] - y[1] + z[2])
		r = Quat{(z[0] + x[2]) / (4 * q), (z[1] + y[2]) / (4 * q), q, (x[1] - y[0]) / (4 * q)}
	}
	return t, r, s
}

// Box is an axis-aligned bounding box; the empty one, EmptyBox, contains nothing.
type Box struct{ Min, Max Vec3 }

// EmptyBox grows from nothing.
func EmptyBox() Box {
	const m = math.MaxFloat32
	return Box{Vec3{m, m, m}, Vec3{-m, -m, -m}}
}

// Empty reports whether the box has seen no point.
func (b Box) Empty() bool { return b.Min[0] > b.Max[0] }

// Grow takes p in.
func (b *Box) Grow(p Vec3) {
	for i := range 3 {
		b.Min[i] = min(b.Min[i], p[i])
		b.Max[i] = max(b.Max[i], p[i])
	}
}

// Join takes o in.
func (b *Box) Join(o Box) {
	if !o.Empty() {
		b.Grow(o.Min)
		b.Grow(o.Max)
	}
}

// Center is the box's middle.
func (b Box) Center() Vec3 { return b.Min.Add(b.Max).Scale(.5) }

// Size is the box's extent along each axis.
func (b Box) Size() Vec3 { return b.Max.Sub(b.Min) }

// area is half the box's surface, which is all the cost of a split needs.
func (b Box) area() float32 {
	d := b.Size()
	if d[0] < 0 {
		return 0
	}
	return d[0]*d[1] + d[1]*d[2] + d[2]*d[0]
}
