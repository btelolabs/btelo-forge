// Package render3d draws a scene3d asset on a graphics card through the internal/gpu adapter: physically based
// materials lit by a sky, with a sun that casts shadows, and the camera that moves around them.
package render3d

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// DefaultSkiesJSON is the built-in list of skies.
//
//go:embed skies.json
var DefaultSkiesJSON []byte

// SkiesGuide describes the file, for Settings and the MCP tools.
const SkiesGuide = `Each sky is an object in "skies":
  "id"        short and unique, it never changes
  "name"      what the 3D tool shows
  "zenith"    the colour straight up, as [red, green, blue] from 0 to 1
  "horizon"   the colour at the horizon
  "ground"    the colour below the horizon
  "sun"       the light that casts shadows:
    "elevation"  degrees above the horizon, 1 to 90
    "azimuth"    degrees around the horizon
    "color"      its colour
    "intensity"  how strong it is; 3 is bright daylight, 0 casts no shadow
    "size"       degrees across its glow in the sky`

// Sun is a sky's one strong light.
type Sun struct {
	Elevation float32    `json:"elevation"`
	Azimuth   float32    `json:"azimuth"`
	Color     [3]float32 `json:"color"`
	Intensity float32    `json:"intensity"`
	Size      float32    `json:"size"`
}

// Dir is the direction towards the sun.
func (s Sun) Dir() s3.Vec3 {
	se, ce := sincos(s.Elevation * math.Pi / 180)
	sa, ca := sincos(s.Azimuth * math.Pi / 180)
	return s3.Vec3{ce * sa, se, ce * ca}
}

// Sky is one of the listed skies.
type Sky struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Zenith  [3]float32 `json:"zenith"`
	Horizon [3]float32 `json:"horizon"`
	Ground  [3]float32 `json:"ground"`
	Sun     Sun        `json:"sun"`
}

// Skies is the file.
type Skies struct {
	Skies []Sky `json:"skies"`
}

// ParseSkies reads and checks the list.
func ParseSkies(data []byte) (Skies, error) {
	var s Skies
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return Skies{}, err
	}
	if len(s.Skies) == 0 {
		return Skies{}, errors.New("list at least one sky")
	}
	seen := map[string]bool{}
	for i, k := range s.Skies {
		where := fmt.Sprintf("sky %d", i+1)
		switch {
		case k.ID == "":
			return Skies{}, fmt.Errorf("%s: id is missing", where)
		case seen[k.ID]:
			return Skies{}, fmt.Errorf("%s: id %q is used twice", where, k.ID)
		case k.Name == "":
			return Skies{}, fmt.Errorf("%s: name is missing", k.ID)
		case k.Sun.Elevation < 1 || k.Sun.Elevation > 90:
			return Skies{}, fmt.Errorf("%s: sun elevation must be 1 to 90", k.ID)
		case k.Sun.Intensity < 0 || k.Sun.Size < 0:
			return Skies{}, fmt.Errorf("%s: sun intensity and size cannot be negative", k.ID)
		}
		seen[k.ID] = true
	}
	return s, nil
}

// Panorama is a whole sphere of light, as longitude by latitude, with four floats for each pixel.
type Panorama struct {
	W, H int
	Pix  []float32
}

const (
	panoramaW = 512
	panoramaH = 256
)

// direction is where pixel (x, y) of a W×H panorama looks.
func direction(x, y, w, h int) s3.Vec3 {
	lon := (float64(x)+.5)/float64(w)*2*math.Pi - math.Pi
	lat := math.Pi/2 - (float64(y)+.5)/float64(h)*math.Pi
	return s3.Vec3{float32(math.Cos(lat) * math.Sin(lon)), float32(math.Sin(lat)), float32(math.Cos(lat) * math.Cos(lon))}
}

func mix3(a, b [3]float32, t float32) [3]float32 {
	return [3]float32{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

// Build paints the sky as a panorama: colours by height, with a glow around the sun.
func (k Sky) Build() Panorama {
	p := Panorama{panoramaW, panoramaH, make([]float32, panoramaW*panoramaH*4)}
	sun := k.Sun.Dir()
	width := max(k.Sun.Size, .5) * math.Pi / 180
	for y := range p.H {
		for x := range p.W {
			d := direction(x, y, p.W, p.H)
			var c [3]float32
			if d[1] >= 0 {
				c = mix3(k.Horizon, k.Zenith, float32(math.Pow(float64(d[1]), .6)))
			} else {
				c = mix3(k.Horizon, k.Ground, min(-d[1]*6, 1))
			}
			angle := float32(math.Acos(float64(min(max(d.Dot(sun), -1), 1))))
			glow := float32(math.Exp(-float64(angle*angle)/float64(2*width*width))) * k.Sun.Intensity * 2
			at := (y*p.W + x) * 4
			for i := range 3 {
				p.Pix[at+i] = c[i] + glow*k.Sun.Color[i]
			}
			p.Pix[at+3] = 1
		}
	}
	return p
}

// Harmonics is the light of a panorama as spherical harmonics of order 2: what a matt surface receives from it, in
// nine colours, ready to be evaluated with a surface's normal.
type Harmonics [9][3]float32

// Harmonics integrates the panorama over the sphere.
func (p Panorama) Harmonics() Harmonics {
	var h Harmonics
	for y := range p.H {
		solid := float32(2 * math.Pi / float64(p.W) * math.Pi / float64(p.H) * math.Cos(math.Pi/2-(float64(y)+.5)/float64(p.H)*math.Pi))
		for x := range p.W {
			d := direction(x, y, p.W, p.H)
			b := shBasis(d)
			at := (y*p.W + x) * 4
			for i := range h {
				for c := range 3 {
					h[i][c] += p.Pix[at+c] * b[i] * solid
				}
			}
		}
	}
	// Lambert's cosine, band by band.
	bands := [9]float32{math.Pi, 2 * math.Pi / 3, 2 * math.Pi / 3, 2 * math.Pi / 3, math.Pi / 4, math.Pi / 4, math.Pi / 4, math.Pi / 4, math.Pi / 4}
	for i := range h {
		for c := range 3 {
			h[i][c] *= bands[i] / math.Pi
		}
	}
	return h
}

func shBasis(d s3.Vec3) [9]float32 {
	x, y, z := d[0], d[1], d[2]
	return [9]float32{.282095, .488603 * y, .488603 * z, .488603 * x, 1.092548 * x * y, 1.092548 * y * z, .315392 * (3*z*z - 1), 1.092548 * x * z, .546274 * (x*x - y*y)}
}

// Evaluate is the light a surface facing n receives, divided by π: the colour of a white matt surface.
func (h Harmonics) Evaluate(n s3.Vec3) [3]float32 {
	b := shBasis(n)
	var out [3]float32
	for i := range h {
		for c := range 3 {
			out[c] += h[i][c] * b[i]
		}
	}
	return out
}

// ExtractSun takes the small brightest part out of a photographed panorama and returns it as a Sun: its light then
// comes with a shadow, and is not counted twice. A panorama with no such part has a Sun of no intensity.
func (p Panorama) ExtractSun() (Panorama, Sun) {
	lum := func(i int) float32 { return .2126*p.Pix[i] + .7152*p.Pix[i+1] + .0722*p.Pix[i+2] }
	var peak, mean float32
	for i := 0; i < len(p.Pix); i += 4 {
		l := lum(i)
		peak = max(peak, l)
		mean += l
	}
	mean /= float32(p.W * p.H)
	if peak < 12*mean || peak < 2 {
		return p, Sun{}
	}
	limit := peak / 4
	out := Panorama{p.W, p.H, slices.Clone(p.Pix)}
	var light [3]float32
	var dir s3.Vec3
	var lit, weight float32
	for y := range p.H {
		solid := float32(2 * math.Pi / float64(p.W) * math.Pi / float64(p.H) * math.Cos(math.Pi/2-(float64(y)+.5)/float64(p.H)*math.Pi))
		for x := range p.W {
			at := (y*p.W + x) * 4
			l := lum(at)
			if l <= limit {
				continue
			}
			keep := limit / l
			for c := range 3 {
				light[c] += p.Pix[at+c] * (1 - keep) * solid
				out.Pix[at+c] *= keep
			}
			dir = dir.Add(direction(x, y, p.W, p.H).Scale(l * solid))
			weight += l * solid
			lit += solid
		}
	}
	dir = dir.Norm()
	strength := max(light[0], light[1], light[2])
	if weight == 0 || strength == 0 || dir[1] <= 0 {
		return p, Sun{}
	}
	return out, Sun{
		Elevation: float32(math.Asin(float64(dir[1])) * 180 / math.Pi),
		Azimuth:   float32(math.Atan2(float64(dir[0]), float64(dir[2])) * 180 / math.Pi),
		Color:     [3]float32{light[0] / strength, light[1] / strength, light[2] / strength},
		Intensity: strength,
		Size:      float32(math.Sqrt(float64(lit/math.Pi)) * 180 / math.Pi),
	}
}

// LoadHDR reads a Radiance .hdr picture.
func LoadHDR(path string) (Panorama, error) {
	f, err := os.Open(path)
	if err != nil {
		return Panorama{}, err
	}
	defer f.Close()
	return DecodeHDR(f)
}

// maxHDRSide bounds a picture's side, so that a broken header cannot ask for all the memory there is.
const maxHDRSide = 1 << 14

// DecodeHDR reads a Radiance picture.
func DecodeHDR(src io.Reader) (Panorama, error) {
	r := bufio.NewReader(src)
	magic, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(magic, "#?") {
		return Panorama{}, errors.New("this is not a Radiance HDR picture")
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return Panorama{}, errors.New("the picture's header is cut short")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "FORMAT=") && line != "FORMAT=32-bit_rle_rgbe" {
			return Panorama{}, fmt.Errorf("%s is not supported", line)
		}
	}
	size, err := r.ReadString('\n')
	if err != nil {
		return Panorama{}, errors.New("the picture's size is missing")
	}
	f := strings.Fields(size)
	if len(f) != 4 || f[0] != "-Y" || f[2] != "+X" {
		return Panorama{}, errors.New("this picture's orientation is not supported")
	}
	h, errH := strconv.Atoi(f[1])
	w, errW := strconv.Atoi(f[3])
	if errH != nil || errW != nil || w <= 0 || h <= 0 || w > maxHDRSide || h > maxHDRSide {
		return Panorama{}, errors.New("the picture's size is broken")
	}
	p := Panorama{w, h, make([]float32, w*h*4)}
	row := make([]byte, w*4)
	for y := range h {
		if err := readHDRRow(r, row, w); err != nil {
			return Panorama{}, fmt.Errorf("the picture is cut short: %w", err)
		}
		for x := range w {
			at := (y*w + x) * 4
			if e := row[x*4+3]; e != 0 {
				scale := float32(math.Ldexp(1, int(e)-136))
				p.Pix[at], p.Pix[at+1], p.Pix[at+2] = float32(row[x*4])*scale, float32(row[x*4+1])*scale, float32(row[x*4+2])*scale
			}
			p.Pix[at+3] = 1
		}
	}
	return p, nil
}

// readHDRRow reads one row of RGBE pixels, in the new run-length form or raw.
func readHDRRow(r *bufio.Reader, row []byte, w int) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return err
	}
	if w < 8 || w > 0x7fff || head[0] != 2 || head[1] != 2 || int(head[2])<<8|int(head[3]) != w {
		copy(row, head[:])
		_, err := io.ReadFull(r, row[4:])
		return err
	}
	for c := range 4 {
		for x := 0; x < w; {
			n, err := r.ReadByte()
			if err != nil {
				return err
			}
			if n > 128 {
				v, err := r.ReadByte()
				if err != nil {
					return err
				}
				count := int(n) - 128
				if x+count > w {
					return errors.New("a run reaches past its row")
				}
				for range count {
					row[x*4+c] = v
					x++
				}
				continue
			}
			if n == 0 || x+int(n) > w {
				return errors.New("a run reaches past its row")
			}
			for range n {
				v, err := r.ReadByte()
				if err != nil {
					return err
				}
				row[x*4+c] = v
				x++
			}
		}
	}
	return nil
}
