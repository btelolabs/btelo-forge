// Package capture records the screen the way OBS Studio does on Windows: Desktop Duplication hands each frame to the
// graphics card's own encoder without copying it through the processor, and the system's sound (WASAPI loopback)
// and a microphone are captured with their device timestamps and mixed against the same clock as the picture.
// ffmpeg does the grabbing and encoding; a recording is written in Matroska segments, which survive a crash and
// make pausing a matter of starting a new one, and joined into an MP4 when it stops.
package capture

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

// Errors a recording can fail with, for the interface to explain.
var (
	ErrUnsupported     = errors.New("screen recording is not supported on this system")
	ErrNoEncoder       = errors.New("no way to record this screen worked")
	ErrNoDisplay       = errors.New("no screen that can be recorded is attached")
	ErrNoGrabber       = errors.New("this ffmpeg lacks ddagrab, the Desktop Duplication grabber")
	ErrSystemSound     = errors.New("the system's sound could not be recorded")
	ErrMicrophone      = errors.New("the microphone could not be recorded")
	ErrNothingRecorded = errors.New("nothing was recorded")
)

// minimumBuild is Windows 10 version 2004, the first that can keep a window out of screen recordings.
const minimumBuild = 19041

// OldWindowsError means Windows is older than recording needs.
type OldWindowsError struct{ Major, Minor, Build uint32 }

func (e *OldWindowsError) Error() string {
	return fmt.Sprintf("screen recording needs Windows 10 version 2004 (build %d) or later; this is Windows %d.%d build %d", minimumBuild, e.Major, e.Minor, e.Build)
}

// Display is one screen, numbered the way ffmpeg's ddagrab numbers them.
type Display struct {
	Adapter, Output int    // the graphics adapter and its output
	Device          string // the GDI name, such as \\.\DISPLAY1
	Name            string // the monitor's own name, or "" when it reports none
	X, Y            int    // where it is on the desktop, in pixels
	Width, Height   int
	Primary         bool
}

// Key identifies the display between runs.
func (d Display) Key() string { return d.Device }

// AudioDevice is a microphone or other recording device.
type AudioDevice struct {
	ID      string
	Name    string
	Default bool
}

// Encoder is the ffmpeg encoder that turns the grabbed screen into H.264.
type Encoder struct {
	Codec string
}

// Hardware reports whether the graphics card encodes, rather than the processor.
func (e Encoder) Hardware() bool { return e.Codec != "libx264" }

// encoders are tried in order: each vendor's own encoder, then Media Foundation's (which every vendor's driver
// provides), then the processor for a computer whose graphics card encodes nothing.
var encoders = []Encoder{{"h264_nvenc"}, {"h264_amf"}, {"h264_mf"}, {"libx264"}}

// Software encoding cannot keep up with large screens at high frame rates, so it records at most this.
const (
	softwareMaxWidth, softwareMaxHeight = 1920, 1080
	softwareMaxFPS                      = 30
)

// Options say what to record.
type Options struct {
	Display      Display
	FPS          int
	Cursor       bool
	SystemAudio  bool
	Microphone   bool
	MicrophoneID string // "" follows the system's default microphone
	Encoder      Encoder
	Region       image.Rectangle // the part of the display to record, in its pixels; empty is all of it
	Path         string          // the finished MP4
}

// area is the part of the display that is recorded, with even sides as H.264 needs.
func (o Options) area() image.Rectangle { return evenArea(o.Display, o.Region) }

// evenArea clips a region to the display and makes its sides even; an empty region is the whole display.
func evenArea(d Display, region image.Rectangle) image.Rectangle {
	r := region.Intersect(image.Rect(0, 0, d.Width, d.Height))
	if r.Empty() {
		r = image.Rect(0, 0, d.Width, d.Height)
	}
	r.Max.X -= r.Dx() % 2
	r.Max.Y -= r.Dy() % 2
	return r
}

// crop is ddagrab's options that grab only part of the display.
func crop(d Display, region image.Rectangle) string {
	r := evenArea(d, region)
	if r == image.Rect(0, 0, d.Width, d.Height) {
		return ""
	}
	return fmt.Sprintf(":offset_x=%d:offset_y=%d:video_size=%dx%d", r.Min.X, r.Min.Y, r.Dx(), r.Dy())
}

func (o Options) audio() bool { return o.SystemAudio || o.Microphone }

// fps is the frame rate actually recorded.
func (o Options) fps() int {
	if !o.Encoder.Hardware() {
		return min(o.FPS, softwareMaxFPS)
	}
	return o.FPS
}

// videoArgs are the ffmpeg arguments that set up the graphics card for ddagrab, and the filter graph that grabs the
// picture. t0 is the wall-clock time, in microseconds, of the recording's first audio frame: the first video frame is
// stamped with how long after t0 it was grabbed (ffmpeg's RTCTIME reads the same clock), and later frames keep
// ddagrab's own precise spacing.
func videoArgs(o Options, t0 int64) (pre []string, graph string) {
	d, e, fps := o.Display, o.Encoder, o.fps()
	cursor := "0"
	if o.Cursor {
		cursor = "1"
	}
	pre = []string{"-init_hw_device", fmt.Sprintf("d3d11va=grab:%d", d.Adapter), "-filter_hw_device", "grab"}
	chain := []string{
		fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d:draw_mouse=%s%s", d.Output, fps, cursor, crop(d, o.Region)),
		fmt.Sprintf("settb=1/1000000,setpts='PTS-STARTPTS+if(eq(N,0),st(0,RTCTIME-%d),ld(0))'", t0),
	}
	// Hardware encoders take the frames on the graphics card as they are; the processor's needs them in memory.
	if !e.Hardware() {
		chain = append(chain, "hwdownload", "format=bgra")
		if a := o.area(); a.Dx() > softwareMaxWidth || a.Dy() > softwareMaxHeight {
			chain = append(chain, fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease:force_divisible_by=2:flags=fast_bilinear", softwareMaxWidth, softwareMaxHeight))
		}
		chain = append(chain, "format=yuv420p")
	}
	return pre, strings.Join(chain, ",") + "[v]"
}

// encodeArgs choose quality-based rate control: a still screen costs almost nothing and motion gets what it needs.
func encodeArgs(e Encoder, fps int) []string {
	gop := strconv.Itoa(fps * 2) // a keyframe every two seconds keeps seeking in the editor quick
	switch e.Codec {
	case "h264_nvenc":
		return []string{"-c:v", "h264_nvenc", "-preset", "p4", "-tune", "hq", "-rc", "vbr", "-cq", "23", "-b:v", "0", "-profile:v", "high", "-g", gop}
	case "h264_amf":
		return []string{"-c:v", "h264_amf", "-usage", "transcoding", "-quality", "balanced", "-rc", "cqp", "-qp_i", "22", "-qp_p", "24", "-qp_b", "26", "-g", gop}
	case "h264_mf":
		return []string{"-c:v", "h264_mf", "-hw_encoding", "1", "-rate_control", "quality", "-quality", "70", "-g", gop}
	default:
		return []string{"-c:v", "libx264", "-preset", "superfast", "-crf", "23", "-g", gop}
	}
}

// recordArgs is the whole ffmpeg command line of one segment; audio is the address ffmpeg reads the mixed sound
// from, or "" for none.
func recordArgs(o Options, t0 int64, audio, out string) []string {
	pre, graph := videoArgs(o, t0)
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	args = append(args, pre...)
	if audio != "" {
		// The format is given in full, so ffmpeg need not wait for seconds of sound to analyse it first.
		args = append(args, "-thread_queue_size", "4096", "-probesize", "32", "-analyzeduration", "0", "-f", "f32le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "-i", audio)
	}
	args = append(args, "-filter_complex", graph, "-map", "[v]")
	if audio != "" {
		// Segments keep the sound lossless: it is cut to the sample and encoded once, when they are joined.
		args = append(args, "-map", "0:a", "-c:a", "flac")
	}
	args = append(args, "-fps_mode", "passthrough")
	args = append(args, encodeArgs(o.Encoder, o.fps())...)
	// Matroska holds a cluster in memory until it is complete; one second each keeps the size shown up to date and
	// leaves at most a second to lose in a crash.
	return append(args, "-flush_packets", "1", "-cluster_time_limit", "1000", "-f", "matroska", out)
}

// ProbeEncoder finds the best encoder that works for the display on this computer by recording half a second with
// each in turn; the first try usually succeeds, so this takes about a second.
func ProbeEncoder(ctx context.Context, d Display, fps int) (Encoder, error) {
	if !Supported() {
		return Encoder{}, ErrUnsupported
	}
	if err := hasGrabber(ctx); err != nil {
		return Encoder{}, err
	}
	var errs []error
	for _, e := range encoders {
		err := tryEncoder(ctx, Options{Display: d, FPS: fps, Encoder: e})
		if err == nil {
			return e, nil
		}
		if ctx.Err() != nil {
			return Encoder{}, ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s: %w", e.Codec, err))
	}
	return Encoder{}, fmt.Errorf("%w: %w", ErrNoEncoder, errors.Join(errs...))
}

// hasGrabber checks that the ffmpeg in use has ddagrab; a build made without Direct3D 11 lacks it.
func hasGrabber(ctx context.Context) error {
	cmd, err := media.Command(ctx, "ffmpeg", "-hide_banner", "-filters")
	if err != nil {
		return err
	}
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[1] == "ddagrab" {
			return nil
		}
	}
	return ErrNoGrabber
}

func tryEncoder(ctx context.Context, o Options) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	pre, graph := videoArgs(o, wallMicros())
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, pre...)
	args = append(args, "-filter_complex", graph, "-map", "[v]", "-t", "0.5", "-fps_mode", "passthrough")
	args = append(args, encodeArgs(o.Encoder, o.fps())...)
	args = append(args, "-f", "null", "-")
	cmd, err := media.Command(ctx, "ffmpeg", args...)
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(stderr.String()))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
