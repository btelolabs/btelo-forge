package assets

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"

	_ "image/gif" // the decoders image.Decode finds, with jpeg and png above

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/btelolabs/btelo-forge/internal/anim"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/thumb"
)

// Info is what reading a file found out about it, kept with its small picture.
type Info struct {
	W        int       `json:"w,omitempty"` // the picture's size in pixels: a canvas's extent, an edit's frame
	H        int       `json:"h,omitempty"`
	Duration float64   `json:"duration,omitempty"` // seconds of a video, sound, animation or edit
	Frames   int       `json:"frames,omitempty"`   // of an animation
	Animated bool      `json:"animated,omitempty"`
	Snippet  string    `json:"snippet,omitempty"` // the beginning of a text
	Peaks    []float32 `json:"peaks,omitempty"`   // a sound's loudness, PeakBins values in [0, 1]
	Problem  string    `json:"problem,omitempty"` // why the file could not be read
	Picture  string    `json:"picture,omitempty"` // the small picture's file in the cache, if it has one
}

// Box is the largest a small picture is: big enough for the largest grid at twice the screen's density.
var Box = image.Pt(thumb.Box, thumb.Box)

// PeakBins is how many loudness values a sound's card shows.
const PeakBins = 96

// SnippetRunes is how much of a text its card shows.
const SnippetRunes = 1500

// thumbsVersion is part of every cache name, so a change to how pictures are made makes new ones.
const thumbsVersion = 1

// Thumbs makes the small pictures and keeps them, with what was found out, in a folder of its own. Maps draws a
// mind map whole, at most box pixels each way; mind maps have no picture without it.
type Thumbs struct {
	dir  string
	Maps func(path string, box int) (*image.RGBA, error)
}

// NewThumbs keeps its pictures in dir.
func NewThumbs(dir string) *Thumbs { return &Thumbs{dir: dir} }

// name is the cache name of e as it is now: a changed file gets a new one.
func (t *Thumbs) name(e Entry) string {
	s := fmt.Sprintf("%d|%s|%d|%d", thumbsVersion, key(e.Path), e.Size, e.Mod.UnixNano())
	if e.Kind == MindMap {
		s += "|map" // before mind maps were known their files were kept as others, with no picture
	}
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:12])
}

// Get is the small picture of e, nil when it has none (a sound, a text), and what was found out about it, from the
// cache or made now. A file that cannot be read gets an Info saying why, and is not tried again until it changes.
func (t *Thumbs) Get(ctx context.Context, e Entry) (*image.RGBA, Info, error) {
	base := filepath.Join(t.dir, t.name(e))
	if data, err := os.ReadFile(base + ".json"); err == nil {
		var info Info
		if json.Unmarshal(data, &info) == nil {
			if info.Picture == "" {
				return nil, info, nil
			}
			if img, err := readPicture(filepath.Join(t.dir, info.Picture)); err == nil {
				return img, info, nil
			}
		}
	}
	img, info, err := t.make(ctx, e)
	if ctx.Err() != nil {
		return nil, Info{}, ctx.Err()
	}
	if err != nil {
		info = Info{Problem: err.Error()}
		img = nil
	}
	if werr := t.store(base, img, &info); werr != nil {
		return img, info, werr
	}
	return img, info, nil
}

// store writes the picture, as a JPEG when it is opaque and a PNG otherwise, and then the info naming it.
func (t *Thumbs) store(base string, img *image.RGBA, info *Info) error {
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return err
	}
	if img != nil {
		var buf bytes.Buffer
		ext := ".jpg"
		if img.Opaque() {
			if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
				return err
			}
		} else {
			ext = ".png"
			if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
				return err
			}
		}
		if err := writeAtomic(base+ext, buf.Bytes()); err != nil {
			return err
		}
		info.Picture = filepath.Base(base) + ext
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return writeAtomic(base+".json", data)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readPicture(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return toRGBA(img), nil
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, img, b.Min, draw.Src)
	return out
}

// fit is w×h scaled down to fit in box, keeping its shape, never up, and at least a pixel each way.
func fit(w, h int, box image.Point) image.Point {
	if w <= 0 || h <= 0 {
		return image.Point{}
	}
	s := math.Min(1, math.Min(float64(box.X)/float64(w), float64(box.Y)/float64(h)))
	return image.Pt(max(1, int(math.Round(float64(w)*s))), max(1, int(math.Round(float64(h)*s))))
}

// shrink scales img down into Box with a sharp filter.
func shrink(img image.Image) *image.RGBA {
	b := img.Bounds()
	size := fit(b.Dx(), b.Dy(), Box)
	if size == b.Size() {
		return toRGBA(img)
	}
	out := image.NewRGBA(image.Rectangle{Max: size})
	xdraw.CatmullRom.Scale(out, out.Rect, img, b, xdraw.Src, nil)
	return out
}

// make reads e and makes its picture and info: from the picture a file of the app's ends with, or else a mind map
// through Maps and anything else with make1.
func (t *Thumbs) make(ctx context.Context, e Entry) (*image.RGBA, Info, error) {
	if e.Kind == Canvas || e.Kind == Edit || e.Kind == MindMap {
		if img, m, err := thumb.Read(e.Path); err == nil {
			return img, Info{W: m.W, H: m.H, Duration: m.Duration}, nil
		}
	}
	if e.Kind != MindMap {
		return make1(ctx, e)
	}
	if t.Maps == nil {
		return nil, Info{}, nil
	}
	img, err := t.Maps(e.Path, Box.X)
	if err != nil {
		return nil, Info{}, err
	}
	return img, Info{W: img.Rect.Dx(), H: img.Rect.Dy()}, nil
}

// make1 reads e and makes its picture and info.
func make1(ctx context.Context, e Entry) (*image.RGBA, Info, error) {
	switch e.Kind {
	case Image, Animation:
		return pictureOf(ctx, e.Path)
	case Video:
		return videoOf(ctx, e.Path)
	case Audio:
		return soundOf(ctx, e.Path)
	case Canvas:
		return canvasOf(e.Path)
	case Edit:
		return editOf(ctx, e.Path)
	case Markdown, Code, Text:
		text, _, err := ReadText(e.Path, SnippetRunes*4)
		if err != nil {
			return nil, Info{}, err
		}
		return nil, Info{Snippet: firstRunes(text, SnippetRunes)}, nil
	}
	return nil, Info{}, nil
}

// maxDecodePixels keeps a damaged or enormous picture from taking all the memory.
const maxDecodePixels = 200 << 20

// pictureOf reads a still or animated picture; formats Go cannot read go to ffmpeg.
func pictureOf(ctx context.Context, path string) (*image.RGBA, Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, Info{}, err
	}
	if a, err := anim.Decode(data); err == nil {
		first, _, err := a.Next()
		if err != nil {
			return nil, Info{}, err
		}
		return shrink(first), Info{W: a.W, H: a.H, Animated: true, Frames: a.Len(), Duration: a.Duration().Seconds()}, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return viaFFmpeg(ctx, path)
	}
	if cfg.Width*cfg.Height > maxDecodePixels {
		return nil, Info{}, fmt.Errorf("the picture is too large to show (%d×%d)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return viaFFmpeg(ctx, path)
	}
	return shrink(img), Info{W: cfg.Width, H: cfg.Height}, nil
}

// viaFFmpeg reads a picture ffmpeg understands and Go does not, such as an icon.
func viaFFmpeg(ctx context.Context, path string) (*image.RGBA, Info, error) {
	info, err := media.Probe(ctx, path)
	if err != nil {
		return nil, Info{}, err
	}
	if !info.HasVideo || info.Width <= 0 {
		return nil, Info{}, errors.New("no picture in this file")
	}
	size := fit(info.Width, info.Height, Box)
	img, err := media.Frame(ctx, path, 0, size.X, size.Y)
	if err != nil {
		return nil, Info{}, err
	}
	return img, Info{W: info.Width, H: info.Height}, nil
}

// posterTime is the moment of a video its picture shows: a little way in, past a fade from black.
func posterTime(duration float64) float64 { return math.Min(duration*0.1, 10) }

func videoOf(ctx context.Context, path string) (*image.RGBA, Info, error) {
	info, err := media.Probe(ctx, path)
	if err != nil {
		return nil, Info{}, err
	}
	out := Info{W: info.Width, H: info.Height, Duration: info.Duration}
	if !info.HasVideo || info.Width <= 0 {
		return soundOf(ctx, path) // a video file with only sound in it
	}
	size := fit(info.Width, info.Height, Box)
	img, err := media.Frame(ctx, path, posterTime(info.Duration), size.X, size.Y)
	if errors.Is(err, media.ErrNoFrame) {
		img, err = media.Frame(ctx, path, 0, size.X, size.Y)
	}
	if err != nil {
		return nil, Info{}, err
	}
	return img, out, nil
}

func soundOf(ctx context.Context, path string) (*image.RGBA, Info, error) {
	info, err := media.Probe(ctx, path)
	if err != nil {
		return nil, Info{}, err
	}
	out := Info{Duration: info.Duration}
	if info.Duration > 0 {
		per := max(1, int(math.Ceil(PeakBins/info.Duration)))
		if peaks, err := media.Peaks(ctx, path, per); err == nil {
			out.Peaks = bins(peaks, PeakBins)
		}
	}
	return nil, out, nil
}

// bins folds values into n, each the largest it covers.
func bins(values []float32, n int) []float32 {
	if len(values) == 0 {
		return nil
	}
	out := make([]float32, n)
	for i := range out {
		from, to := i*len(values)/n, (i+1)*len(values)/n
		to = max(to, from+1)
		for _, v := range values[min(from, len(values)-1):min(to, len(values))] {
			out[i] = max(out[i], v)
		}
	}
	return out
}

func canvasOf(path string) (*image.RGBA, Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, Info{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, Info{}, err
	}
	d, err := paint.Load(f, st.Size())
	if err != nil {
		return nil, Info{}, err
	}
	ext := d.Extent()
	info := Info{W: ext.Dx(), H: ext.Dy()}
	if ext.Empty() {
		return nil, info, nil // nothing drawn yet
	}
	return shrink(d.Reduced(max(Box.X, Box.Y))), info, nil
}

func editOf(ctx context.Context, path string) (*image.RGBA, Info, error) {
	p, err := project.ReadFile(path)
	if err != nil {
		return nil, Info{}, err
	}
	info := Info{W: p.Canvas.Width, H: p.Canvas.Height, Duration: p.Duration()}
	c := p.Video[0].Clips[0]
	src := p.SourceOf(c)
	size := fit(src.Width, src.Height, Box)
	img, err := media.Frame(ctx, src.Path, c.In+math.Min(c.Out-c.In, 2)*0.5, size.X, size.Y)
	if err != nil {
		// The video has moved: the edit is still listed, without its picture.
		return nil, info, nil
	}
	return img, info, nil
}

// ReadText reads up to limit bytes of a text file: UTF-8, or UTF-16 with its byte order mark. truncated says the
// file goes on. A file with NUL bytes in it is not text.
func ReadText(path string, limit int) (text string, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	buf := make([]byte, limit+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	truncated = n > limit
	data := buf[:min(n, limit)]
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}), bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		big := data[0] == 0xfe
		data = data[2:]
		units := make([]uint16, len(data)/2)
		for i := range units {
			if big {
				units[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
			} else {
				units[i] = uint16(data[2*i+1])<<8 | uint16(data[2*i])
			}
		}
		text = string(utf16.Decode(units))
	default:
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		if bytes.IndexByte(data, 0) >= 0 {
			return "", false, ErrBinary
		}
		// A character cut in two at the limit is not a fault of the file.
		for i := 0; truncated && i < utf8.UTFMax-1 && len(data) > 0; i++ {
			if r, _ := utf8.DecodeLastRune(data); r != utf8.RuneError {
				break
			}
			data = data[:len(data)-1]
		}
		text = strings.ToValidUTF8(string(data), "�")
	}
	return strings.ReplaceAll(text, "\r\n", "\n"), truncated, nil
}

// ErrBinary means a file meant to be text holds binary data.
var ErrBinary = errors.New("this file is not text")

func firstRunes(s string, n int) string {
	i := 0
	for k := range s {
		if i == n {
			return s[:k]
		}
		i++
	}
	return s
}

// FormatSize writes a byte count as people read it.
func FormatSize(n int64) string {
	switch {
	case n >= 1<<30:
		return strconv.FormatFloat(float64(n)/(1<<30), 'f', 2, 64) + " GB"
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 0, 64) + " KB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

// Peek is what is known of e without making anything: its cached info, or for a picture the size its header
// gives. It is cheap enough to ask of every file of a folder, so the grid can be laid out before the pictures come.
func (t *Thumbs) Peek(e Entry) (Info, bool) {
	if data, err := os.ReadFile(filepath.Join(t.dir, t.name(e)+".json")); err == nil {
		var info Info
		if json.Unmarshal(data, &info) == nil {
			return info, true
		}
	}
	if e.Kind != Image && e.Kind != Animation {
		return Info{}, false
	}
	f, err := os.Open(e.Path)
	if err != nil {
		return Info{}, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return Info{}, false
	}
	return Info{W: cfg.Width, H: cfg.Height}, true
}
