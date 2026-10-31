package render3d

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// wall is an upright square, facing +z, in front of the origin.
func wall(material int, z float32) s3.Prim {
	return s3.Prim{Pos: []s3.Vec3{{-1, 0, z}, {1, 0, z}, {1, 2, z}, {-1, 2, z}}, Normal: []s3.Vec3{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}},
		UV: []s3.Vec2{{0, 1}, {1, 1}, {1, 0}, {0, 0}}, Indices: []uint32{0, 1, 2, 0, 2, 3}, Material: material}
}

// halfPicture is a PNG whose left half is opaque red and its right half transparent.
func halfPicture(t *testing.T) s3.Image {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 8 {
			im.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return s3.Image{Mime: "image/png", Data: b.Bytes()}
}

func assetOf(prims []s3.Prim, materials []s3.Material, images ...s3.Image) *s3.Asset {
	return &s3.Asset{Nodes: []s3.Node{{Parent: -1, Mesh: 0, Skin: -1, Scale: s3.Vec3{1, 1, 1}, Rotation: s3.Quat{0, 0, 0, 1}}}, Roots: []int{0},
		Meshes: []s3.Mesh{{Prims: prims}}, Materials: materials, Images: images}
}

func facing(cam *Camera) {
	cam.Yaw, cam.Pitch = 0, 0
	cam.Pos, cam.Distance = s3.Vec3{0, 1, 5}, 5
}

func TestCutOutMaterialShowsThroughItsHoles(t *testing.T) {
	m := s3.DefaultMaterial()
	m.Metallic, m.Roughness = 0, 1
	m.AlphaMode, m.BaseTex = s3.AlphaMask, s3.TexRef{Image: 0}
	r, cam := setup(t, assetOf([]s3.Prim{wall(0, 0)}, []s3.Material{m}, halfPicture(t)))
	facing(cam)
	im := shoot(t, r, cam, Options{Exposure: 1, Shadows: true})
	left, right := im.Pix[im.PixOffset(180, 135):], im.Pix[im.PixOffset(300, 135):]
	if left[0] < 2*left[2] {
		t.Fatalf("the opaque half is %v", left[:3])
	}
	if right[0] > right[2]+12 {
		t.Fatalf("the hole shows %v", right[:3])
	}
}

func TestBlendedMaterialLetsWhatIsBehindThrough(t *testing.T) {
	back := s3.DefaultMaterial()
	back.BaseColor, back.Metallic, back.Roughness = s3.Vec4{.1, .9, .1, 1}, 0, 1
	front := s3.DefaultMaterial()
	front.BaseColor, front.Metallic, front.Roughness, front.AlphaMode = s3.Vec4{.9, .1, .1, .5}, 0, 1, s3.AlphaBlend
	solid := front
	solid.AlphaMode, solid.BaseColor = s3.AlphaOpaque, s3.Vec4{.9, .1, .1, 1}
	glass, cam := setup(t, assetOf([]s3.Prim{wall(0, -.5), wall(1, .5)}, []s3.Material{back, front}))
	facing(cam)
	seen := shoot(t, glass, cam, Options{Exposure: 1})
	opaque, cam2 := setup(t, assetOf([]s3.Prim{wall(0, -.5), wall(1, .5)}, []s3.Material{back, solid}))
	facing(cam2)
	shut := shoot(t, opaque, cam2, Options{Exposure: 1})
	a, b := seen.Pix[seen.PixOffset(240, 135):], shut.Pix[shut.PixOffset(240, 135):]
	if int(a[1]) < int(b[1])+25 {
		t.Fatalf("through the glass %v, through the wall %v", a[:3], b[:3])
	}
}

// hdrFile is a width×height picture with a small bright patch high in it, in raw RGBE.
func hdrFile(w, h int) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n\n-Y %d +X %d\n", h, w)
	for y := range h {
		for x := range w {
			if y == h/4 && x == w/2 {
				b.Write([]byte{200, 200, 180, 128 + 10}) // about 1000
			} else {
				b.Write([]byte{128, 128, 128, 128}) // 0.5
			}
		}
	}
	return b.Bytes()
}

func TestHDRSkyLightsTheScene(t *testing.T) {
	p, err := DecodeHDR(bytes.NewReader(hdrFile(64, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if v := p.Pix[0]; math.Abs(float64(v-.5)) > .01 {
		t.Fatalf("a dim pixel reads %v", v)
	}
	out, sun := p.ExtractSun()
	if sun.Intensity <= 0 || sun.Elevation < 30 || sun.Elevation > 60 {
		t.Fatalf("the sun is %+v", sun)
	}
	if peak := out.Pix[(32/4*64+32)*4]; peak >= p.Pix[(32/4*64+32)*4] {
		t.Fatalf("the sun stayed in the sky: %v", peak)
	}
	r, cam := setup(t, sphere())
	if err := r.SetSky(out, sun); err != nil {
		t.Fatal(err)
	}
	im := shoot(t, r, cam, Options{Exposure: 1, Shadows: true, ShowSky: true})
	if luma(im, 240, 135) == 0 {
		t.Fatal("nothing is lit")
	}
}

func TestRunLengthHDR(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("#?RGBE\n\n-Y 1 +X 8\n")
	b.Write([]byte{2, 2, 0, 8})
	for _, v := range []byte{10, 20, 30, 128} {
		b.Write([]byte{128 + 8, v}) // the whole row, one channel at a time
	}
	p, err := DecodeHDR(&b)
	if err != nil {
		t.Fatal(err)
	}
	want := float32(10) / 256
	if math.Abs(float64(p.Pix[28]-want)) > 1e-6 {
		t.Fatalf("pixel 7 reads %v, want %v", p.Pix[28], want)
	}
}

func TestBrokenHDRFails(t *testing.T) {
	for name, text := range map[string]string{
		"not one":  "hello",
		"format":   "#?RADIANCE\nFORMAT=32-bit_rle_xyze\n\n-Y 1 +X 1\n",
		"size":     "#?RADIANCE\n\n-Y 999999 +X 1\n",
		"sideways": "#?RADIANCE\n\n+X 4 -Y 4\n",
		"short":    "#?RADIANCE\n\n-Y 2 +X 2\nab",
	} {
		if _, err := DecodeHDR(strings.NewReader(text)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// A mirrored part still shows its outside.
func TestMirroredPartFacesOut(t *testing.T) {
	a := sphere()
	a.Nodes[0].Scale = s3.Vec3{-1, 1, 1}
	r, cam := setup(t, a)
	im := shoot(t, r, cam, Options{Exposure: 1})
	c := im.Pix[im.PixOffset(240, 135):]
	if c[0] < 2*c[2] {
		t.Fatalf("the ball's middle is %v", c[:3])
	}
}

func TestPictureThatCannotBeReadIsReported(t *testing.T) {
	m := s3.DefaultMaterial()
	m.BaseTex = s3.TexRef{Image: 0}
	a := assetOf([]s3.Prim{wall(0, 0)}, []s3.Material{m}, s3.Image{Name: "broken", Data: []byte("no picture")})
	r, err := New(card(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	problems, err := r.SetAsset(a)
	if err != nil || len(problems) != 1 || !strings.Contains(problems[0], "broken") {
		t.Fatalf("%v, %v", problems, err)
	}
}
