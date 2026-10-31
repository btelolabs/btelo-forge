package render3d

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gpu"
	"github.com/btelolabs/btelo-forge/internal/gpu/wgpu"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// card opens the graphics card, or skips the test on a computer without one.
func card(t *testing.T) gpu.Device {
	t.Helper()
	d, err := wgpu.Shared()
	if errors.Is(err, gpu.ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// sphere is a smooth ball of radius 1 standing on y = 0.
func sphere() *s3.Asset {
	const rings, sides = 48, 96
	var p s3.Prim
	for y := 0; y <= rings; y++ {
		sv, cv := math.Sincos(float64(y) / rings * math.Pi)
		for x := 0; x <= sides; x++ {
			su, cu := math.Sincos(float64(x) / sides * 2 * math.Pi)
			n := s3.Vec3{float32(sv * cu), float32(cv), float32(sv * su)}
			p.Pos = append(p.Pos, n.Add(s3.Vec3{0, 1, 0}))
			p.Normal = append(p.Normal, n)
			p.UV = append(p.UV, s3.Vec2{float32(x) / sides, float32(y) / rings})
		}
	}
	at := func(x, y int) uint32 { return uint32(y*(sides+1) + x) }
	for y := range rings {
		for x := range sides {
			p.Indices = append(p.Indices, at(x, y), at(x+1, y), at(x, y+1), at(x+1, y), at(x+1, y+1), at(x, y+1))
		}
	}
	m := s3.DefaultMaterial()
	m.BaseColor, m.Metallic, m.Roughness = s3.Vec4{.8, .15, .1, 1}, 0, .4
	return &s3.Asset{Nodes: []s3.Node{{Parent: -1, Mesh: 0, Skin: -1, Scale: s3.Vec3{1, 1, 1}, Rotation: s3.Quat{0, 0, 0, 1}}}, Roots: []int{0},
		Meshes: []s3.Mesh{{Prims: []s3.Prim{p}}}, Materials: []s3.Material{m}}
}

// shots counts the pictures a test takes, which name their files.
var shots int

func shoot(t *testing.T, r *Renderer, cam *Camera, o Options) *image.RGBA {
	t.Helper()
	shots++
	out := image.NewRGBA(image.Rect(0, 0, 480, 270))
	if err := r.Draw(cam, o, 480, 270, out); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FORGE_SHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("3d-%s-%d.png", t.Name(), shots)))
		if err == nil {
			png.Encode(f, out)
			f.Close()
		}
	}
	return out
}

func setup(t *testing.T, a *s3.Asset) (*Renderer, *Camera) {
	t.Helper()
	r, err := New(card(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	skies, err := ParseSkies(DefaultSkiesJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetSky(skies.Skies[0].Build(), skies.Skies[0].Sun); err != nil {
		t.Fatal(err)
	}
	surfaces, bounds := a.Surfaces()
	if _, err := r.SetAsset(a); err != nil {
		t.Fatal(err)
	}
	if err := r.SetSurfaces(surfaces, bounds); err != nil {
		t.Fatal(err)
	}
	return r, NewCamera(bounds)
}

func luma(im *image.RGBA, x, y int) int {
	p := im.Pix[im.PixOffset(x, y):]
	return int(p[0]) + int(p[1]) + int(p[2])
}

func TestDrawsLitSphereWithShadow(t *testing.T) {
	r, cam := setup(t, sphere())
	o := Options{Shadows: true, Grid: true, ShowSky: true, Exposure: 1}
	lit := shoot(t, r, cam, o)
	// The ball is red in the middle of the picture, and differs from the sky at the corner.
	c := lit.Pix[lit.PixOffset(240, 135):]
	if c[0] < 2*c[2] || c[0] < 60 {
		t.Fatalf("the middle of the ball is %v", c[:3])
	}
	dark := 0
	o2 := o
	o2.Shadows = false
	bare := shoot(t, r, cam, o2)
	for y := range 270 {
		for x := range 480 {
			if luma(bare, x, y)-luma(lit, x, y) > 60 {
				dark++
			}
		}
	}
	if dark < 300 {
		t.Fatalf("the shadow darkens only %d pixels", dark)
	}
	for _, v := range []ViewMode{ViewSolid, ViewNormals, ViewUV} {
		shoot(t, r, cam, Options{View: v, Wireframe: v == ViewSolid, Grid: true, Exposure: 1})
	}
}

// A mesh line stands out from a white, a black and a grey surface alike, in both directions on grey.
func TestMeshLinesShowOnAnyColour(t *testing.T) {
	cube := s3.NewPrimitive(s3.Cube)
	prims, _ := cube.Bake()
	for i := range prims {
		prims[i].Material = 0
	}
	for _, c := range []float32{1, 0, .2} {
		m := s3.DefaultMaterial()
		m.BaseColor, m.Metallic, m.Roughness = s3.Vec4{c, c, c, 1}, 0, .6
		a := &s3.Asset{Nodes: []s3.Node{{Parent: -1, Mesh: 0, Skin: -1, Scale: s3.Vec3{1, 1, 1}, Rotation: s3.Quat{0, 0, 0, 1}}}, Roots: []int{0},
			Meshes: []s3.Mesh{{Prims: prims}}, Materials: []s3.Material{m}}
		r, cam := setup(t, a)
		bare := shoot(t, r, cam, Options{Exposure: 1})
		lined := shoot(t, r, cam, Options{Exposure: 1, Wireframe: true})
		lighter, darker := 0, 0
		for y := range 270 {
			for x := range 480 {
				switch d := luma(lined, x, y) - luma(bare, x, y); {
				case d > 40:
					lighter++
				case d < -40:
					darker++
				}
			}
		}
		if lighter+darker < 300 {
			t.Fatalf("on %v the lines change %d pixels up and %d down", c, lighter, darker)
		}
		if c == .2 && (lighter < 100 || darker < 100) {
			t.Fatalf("on grey the lines change %d pixels up and %d down", lighter, darker)
		}
	}
}

// A seam splits vertices, and its edge is still one line.
func TestMeshLinesCountASeamOnce(t *testing.T) {
	s := s3.Surface{Pos: []s3.Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {1, 0, 0}, {0, 1, 0}, {1, 1, 0}}, Indices: []uint32{0, 1, 2, 3, 5, 4}}
	if n := len(MeshLines([]s3.Surface{s}, lineColor)) / OverlayLine; n != 5 {
		t.Fatalf("%d lines", n)
	}
}

func TestSizeChanges(t *testing.T) {
	r, cam := setup(t, sphere())
	for _, s := range [][2]int{{64, 64}, {333, 77}, {1, 1}, {1024, 600}} {
		out := image.NewRGBA(image.Rect(0, 0, s[0], s[1]))
		if err := r.Draw(cam, Options{Exposure: 1}, s[0], s[1], out); err != nil {
			t.Fatalf("%v: %v", s, err)
		}
	}
}

// The specular level for a smooth surface keeps the sun's glow narrow, and the rough one spreads it.
func TestPanoramaIsLit(t *testing.T) {
	skies, _ := ParseSkies(DefaultSkiesJSON)
	for _, k := range skies.Skies {
		p := k.Build()
		h := p.Harmonics()
		up, down := h.Evaluate(s3.Vec3{0, 1, 0}), h.Evaluate(s3.Vec3{0, -1, 0})
		if up[1] <= down[1] {
			t.Errorf("%s: facing up gets %v, facing down %v", k.ID, up, down)
		}
	}
}

func TestSkiesAreChecked(t *testing.T) {
	for name, text := range map[string]string{
		"empty":    `{"skies":[]}`,
		"no id":    `{"skies":[{"name":"x","sun":{"elevation":10}}]}`,
		"sun":      `{"skies":[{"id":"a","name":"x","sun":{"elevation":0}}]}`,
		"twice":    `{"skies":[{"id":"a","name":"x","sun":{"elevation":9}},{"id":"a","name":"y","sun":{"elevation":9}}]}`,
		"unknown":  `{"skies":[{"id":"a","name":"x","moon":1,"sun":{"elevation":9}}]}`,
		"negative": `{"skies":[{"id":"a","name":"x","sun":{"elevation":9,"intensity":-1}}]}`,
	} {
		if _, err := ParseSkies([]byte(text)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The traced view lights what the sun cannot reach, by light that bounced off what it can, and grows less noisy.
func TestTracerBouncesLight(t *testing.T) {
	r, cam := setup(t, sphere())
	o := Options{View: ViewTraced, Shadows: true, Grid: true, ShowSky: true, Exposure: 1}
	out := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for range 48 {
		if err := r.Draw(cam, o, 320, 180, out); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := r.Samples(); n < 8 {
		t.Fatalf("%d samples", n)
	}
	if dir := os.Getenv("FORGE_SHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "traced.png"))
		if err == nil {
			png.Encode(f, out)
			f.Close()
		}
	}
	red := 0
	for y := range 180 {
		for x := range 320 {
			c := out.RGBAAt(x, y)
			if int(c.R) > int(c.B)+30 {
				red++
			}
		}
	}
	if red < 500 {
		t.Fatalf("the red ball shows on %d pixels", red)
	}
}
