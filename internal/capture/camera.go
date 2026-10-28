package capture

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

// A camera is shown on the screen in a window of its own, so that recordings and streams of the screen have it,
// as a streamer's face in a corner. ffmpeg reads it through DirectShow, crops it to the window's shape and scales
// it to the window's size; the frames come out as RGBA.

// Camera is a video capture device.
type Camera struct {
	Name string
	ID   string // DirectShow's unique name for it, which tells apart cameras of the same name
}

// ErrNoCamera means the camera could not be opened: it is unplugged, or another program is using it.
var ErrNoCamera = errors.New("the camera could not be opened")

// videoCategory is DirectShow's category of video capture devices, which virtual cameras are listed under too.
const videoCategory = "{860bb310-5d01-11d0-bd3b-00a0c911ce86}"

var (
	// ffmpeg prefixes each line with what printed it: "[dshow @ 0x…]" up to version 8, "[in#0 @ 0x…]" from 9.
	deviceLine      = regexp.MustCompile(`^\[[^\]@]+ @ [^\]]*\]\s+"(.*)" \((video|audio|none|audio, video|video, audio)\)\s*$`)
	alternativeLine = regexp.MustCompile(`^\[[^\]@]+ @ [^\]]*\]\s+Alternative name "(.*)"\s*$`)
	modeLine        = regexp.MustCompile(`(vcodec|pixel_format)=(\S+)\s+min s=(\d+)x(\d+) fps=([\d.]+)\s+max s=(\d+)x(\d+) fps=([\d.]+)`)
)

// Cameras lists the cameras, virtual ones (such as OBS's) included.
func Cameras(ctx context.Context) ([]Camera, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	out, err := dshowList(ctx, "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	if err != nil {
		return nil, err
	}
	return parseCameras(out), nil
}

// dshowList runs an ffmpeg that only lists, and returns what it printed; ffmpeg ends such a run with an error.
func dshowList(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd, err := media.Command(ctx, "ffmpeg", append([]string{"-hide_banner", "-nostdin"}, args...)...)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	cmd.Stderr = &buf
	cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return buf.Bytes(), nil
}

func parseCameras(out []byte) []Camera {
	var cams []Camera
	var name, kind string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if m := deviceLine.FindStringSubmatch(line); m != nil {
			name, kind = m[1], m[2]
			continue
		}
		m := alternativeLine.FindStringSubmatch(line)
		if m == nil || name == "" {
			continue
		}
		id := m[1]
		// A virtual camera is listed as of no kind; its category says it is a camera.
		if strings.Contains(kind, "video") || kind == "none" && strings.Contains(strings.ToLower(id), videoCategory) {
			cams = append(cams, Camera{Name: name, ID: id})
		}
		name = ""
	}
	return cams
}

// cameraMode is a way a camera can deliver its picture.
type cameraMode struct {
	codec  string // "mjpeg", or "" for a raw pixel format
	pixels string // the raw pixel format, when codec is ""
	size   image.Point
	fps    float64
}

func parseModes(out []byte) []cameraMode {
	var modes []cameraMode
	for _, m := range modeLine.FindAllStringSubmatch(string(out), -1) {
		w, _ := strconv.Atoi(m[6])
		h, _ := strconv.Atoi(m[7])
		fps, _ := strconv.ParseFloat(m[8], 64)
		mode := cameraMode{size: image.Pt(w, h), fps: fps}
		if m[1] == "vcodec" {
			mode.codec = m[2]
		} else {
			mode.pixels = m[2]
		}
		if !slices.Contains(modes, mode) {
			modes = append(modes, mode)
		}
	}
	return modes
}

// chooseMode picks how to read a camera for a picture of size: the smallest mode at 30 frames a second that still
// covers it once cropped to its shape, MJPEG before raw pixels (which fill a USB link at high sizes), and up to
// 1080p. Without a fast enough mode it takes the fastest. It is false when the camera lists no modes.
func chooseMode(modes []cameraMode, size image.Point) (cameraMode, bool) {
	covers := func(m cameraMode) bool {
		// Cropping to size's shape keeps the whole of one side of the camera's picture.
		w, h := m.size.X, m.size.Y
		if w*size.Y > h*size.X {
			w = h * size.X / size.Y
		} else {
			h = w * size.Y / size.X
		}
		return w >= size.X && h >= size.Y
	}
	usable := slices.DeleteFunc(slices.Clone(modes), func(m cameraMode) bool {
		return m.size.X*m.size.Y > 1920*1080 || m.codec != "" && m.codec != "mjpeg"
	})
	if len(usable) == 0 {
		return cameraMode{}, false
	}
	better := func(a, b cameraMode) int {
		fastA, fastB := a.fps >= 29, b.fps >= 29
		switch {
		case fastA != fastB:
			if fastA {
				return -1
			}
			return 1
		case covers(a) != covers(b):
			if covers(a) {
				return -1
			}
			return 1
		}
		areaA, areaB := a.size.X*a.size.Y, b.size.X*b.size.Y
		if areaA != areaB {
			if covers(a) { // both cover: the smaller costs less
				return areaA - areaB
			}
			return areaB - areaA // neither covers: the larger is sharper
		}
		if (a.codec == "mjpeg") != (b.codec == "mjpeg") {
			if a.codec == "mjpeg" {
				return -1
			}
			return 1
		}
		return 0
	}
	slices.SortStableFunc(usable, better)
	return usable[0], true
}

// CameraOptions say what to show of a camera.
type CameraOptions struct {
	Camera Camera
	Size   image.Point // the picture, in pixels; the camera is cropped to its shape
	Mirror bool        // flipped left to right, as people expect to see themselves
}

// CameraFeed is a camera being read. Its frames go to the function given to StartCamera, on a goroutine of its own.
type CameraFeed struct {
	opts   CameraOptions
	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	err error
}

// StartCamera opens a camera; frame receives each picture as RGBA, Size large, and must not keep it after it
// returns. It returns once the camera delivers, or fails to.
func StartCamera(o CameraOptions, frame func(rgba []byte)) (*CameraFeed, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if o.Size.X <= 0 || o.Size.Y <= 0 {
		return nil, errors.New("the camera's picture needs a size")
	}
	out, _ := dshowList(context.Background(), "-list_options", "true", "-f", "dshow", "-i", "video="+o.Camera.ID)
	var tries [][]string
	if mode, ok := chooseMode(parseModes(out), o.Size); ok {
		tries = append(tries, mode.args())
	}
	tries = append(tries, nil) // the camera's own choice
	var errs []error
	for _, mode := range tries {
		f, err := startCamera(o, mode, frame)
		if err == nil {
			return f, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("%w: %s: %w", ErrNoCamera, o.Camera.Name, errors.Join(errs...))
}

func (m cameraMode) args() []string {
	args := []string{"-video_size", fmt.Sprintf("%dx%d", m.size.X, m.size.Y), "-framerate", strconv.FormatFloat(min(m.fps, 30), 'f', -1, 64)}
	if m.codec != "" {
		return append([]string{"-vcodec", m.codec}, args...)
	}
	return append([]string{"-pixel_format", m.pixels}, args...)
}

// cameraArgs read the camera in a mode and give its picture cropped to the size's shape, scaled to it, as RGBA.
func cameraArgs(o CameraOptions, mode []string) []string {
	w, h := o.Size.X, o.Size.Y
	filters := []string{
		fmt.Sprintf(`crop=w=min(iw\,ih*%d/%d):h=min(ih\,iw*%d/%d)`, w, h, h, w),
		fmt.Sprintf("scale=%d:%d:flags=bicubic", w, h),
	}
	if o.Mirror {
		filters = append(filters, "hflip")
	}
	filters = append(filters, "format=rgba")
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "dshow", "-rtbufsize", "64M"}
	args = append(args, mode...)
	return append(args, "-i", "video="+o.Camera.ID, "-vf", strings.Join(filters, ","), "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
}

func startCamera(o CameraOptions, mode []string, frame func([]byte)) (*CameraFeed, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd, err := media.Command(ctx, "ffmpeg", cameraArgs(o, mode)...)
	var out io.ReadCloser
	if err == nil {
		out, err = cmd.StdoutPipe()
	}
	stderr := &tailBuffer{}
	if err == nil {
		cmd.Stderr = stderr
		err = cmd.Start()
	}
	if err != nil {
		cancel()
		return nil, err
	}
	f := &CameraFeed{opts: o, cancel: cancel, done: make(chan struct{})}
	first := make(chan error, 1)
	go func() {
		defer close(f.done)
		defer cancel()
		buf := make([]byte, o.Size.X*o.Size.Y*4)
		var readErr error
		for n := 0; ; n++ {
			if _, readErr = io.ReadFull(out, buf); readErr != nil {
				break
			}
			if n == 0 {
				first <- nil
			}
			frame(buf)
		}
		cmd.Wait()
		err := readErr
		if tail := stderr.String(); tail != "" {
			err = errors.New(tail)
		}
		if ctx.Err() == nil {
			f.mu.Lock()
			f.err = err
			f.mu.Unlock()
		}
		select {
		case first <- err:
		default:
		}
	}()
	select {
	case err := <-first:
		if err != nil {
			<-f.done
			return nil, err
		}
	case <-time.After(15 * time.Second):
		f.Stop()
		return nil, errors.New("the camera sent no picture")
	}
	return f, nil
}

// Done is closed when the camera stops, by Stop or by itself; Err says why it stopped by itself.
func (f *CameraFeed) Done() <-chan struct{} { return f.done }

// Err is why the camera stopped by itself (unplugged, or taken by another program), or nil.
func (f *CameraFeed) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// Options are what the feed shows.
func (f *CameraFeed) Options() CameraOptions { return f.opts }

// Stop closes the camera and waits for it to let go.
func (f *CameraFeed) Stop() {
	f.cancel()
	<-f.done
}
