package render

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/btelolabs/btelo-video-cut/internal/media"
	"github.com/btelolabs/btelo-video-cut/internal/project"
)

// Errors an export can be refused with before anything runs.
var (
	ErrEncoderMissing = errors.New("this ffmpeg cannot encode the chosen format")
	ErrTargetTooSmall = errors.New("the target size is too small for the length of the video")
)

// TargetTooSmall reports the smallest target, in megabytes, that leaves the picture a usable bitrate.
type TargetTooSmall struct{ MinimumMB float64 }

func (e *TargetTooSmall) Error() string {
	return fmt.Sprintf("%v; allow at least %.1f MB", ErrTargetTooSmall, e.MinimumMB)
}

func (e *TargetTooSmall) Unwrap() error { return ErrTargetTooSmall }

// Encoder names the ffmpeg encoder that writes codec, preferring the graphics card when hardware is set and it
// can; it is "" when no usable encoder exists.
func Encoder(codec project.Codec, hardware bool, enc media.Encoders) string {
	var choices []string
	switch codec {
	case project.HEVC:
		choices = []string{"hevc_nvenc", "libx265"}
	case project.AV1:
		choices = []string{"av1_nvenc", "libsvtav1", "libaom-av1"}
	default:
		choices = []string{"h264_nvenc", "libx264"}
	}
	if !hardware {
		choices = choices[1:]
	}
	for _, name := range choices {
		if enc[name] {
			return name
		}
	}
	return ""
}

// qualityScale is the constant-quality value per quality step for each encoder family; each family's numbers give
// roughly the same picture at the same step.
var qualityScale = map[string][5]int{
	"libx264":    {18, 23, 28, 32, 36},
	"libx265":    {22, 27, 31, 35, 39},
	"libsvtav1":  {26, 33, 40, 46, 52},
	"libaom-av1": {24, 31, 38, 44, 50},
	"h264_nvenc": {19, 24, 29, 33, 37},
	"hevc_nvenc": {21, 26, 31, 35, 39},
	"av1_nvenc":  {26, 33, 40, 46, 52},
}

// speedScale is the encoder's preset per speed (medium, fast, slow, slowest).
var speedScale = map[string][4]string{
	"libx264":    {"medium", "veryfast", "slow", "veryslow"},
	"libx265":    {"medium", "veryfast", "slow", "veryslow"},
	"libsvtav1":  {"8", "10", "6", "4"},
	"libaom-av1": {"6", "8", "4", "3"},
	"h264_nvenc": {"p5", "p3", "p6", "p7"},
	"hevc_nvenc": {"p5", "p3", "p6", "p7"},
	"av1_nvenc":  {"p5", "p3", "p6", "p7"},
}

// containerShare is the part of a target size left for the streams once the MP4 container takes its share.
const containerShare = 0.97

// minVideoKbps is the lowest picture bitrate a target size may leave.
const minVideoKbps = 60

// Job is an export ready to run: one ffmpeg command line per pass, the last one writing the file. The passes run
// in Dir, where two-pass encoders keep their statistics under a relative name (an absolute Windows path would
// clash with the colons that separate encoder options).
type Job struct {
	Passes  [][]string
	Length  float64
	Encoder string
	Dir     string
}

// Close removes the statistics the passes share.
func (j *Job) Close() {
	if j.Dir != "" {
		os.RemoveAll(j.Dir)
	}
}

// OutputRate is the frame rate of the export: the canvas rate, capped by the export setting.
func OutputRate(c project.Canvas, e project.Export) float64 {
	if e.FPS > 0 && float64(e.FPS) < c.FPS {
		return float64(e.FPS)
	}
	return c.FPS
}

// PlanExport builds the passes that encode p with its overlays into output.
func PlanExport(p *project.Project, overlays []Overlay, output string, enc media.Encoders) (*Job, error) {
	e := p.Export
	name := Encoder(e.Codec, e.Hardware, enc)
	if name == "" {
		return nil, ErrEncoderMissing
	}
	w, h := OutputSize(p.Canvas, e.Height)
	spec := Spec{Project: p, From: 0, To: p.Duration(), Width: w, Height: h, FPS: OutputRate(p.Canvas, e), Video: true, Overlays: overlays}
	audioKbps, channels := e.Audio.Bitrate()
	spec.Audio = audioKbps > 0
	g, err := Build(spec)
	if err != nil {
		return nil, err
	}
	job := &Job{Length: g.Length, Encoder: name}
	video := []string{"-c:v", name}
	twoPass := false
	if e.TargetMB > 0 {
		kbps, err := videoBitrate(e.TargetMB, g.Length, audioKbps, g.AudioOut != "")
		if err != nil {
			return nil, err
		}
		video = append(video, rateArgs(name, kbps)...)
		twoPass = name == "libx264" || name == "libx265" || name == "libaom-av1"
	} else {
		video = append(video, qualityArgs(name, qualityScale[name][e.Quality])...)
	}
	video = append(video, speedArgs(name, speedScale[name][e.Speed])...)
	video = append(video, "-pix_fmt", "yuv420p")
	if e.Codec == project.HEVC {
		video = append(video, "-tag:v", "hvc1")
	}
	if twoPass {
		dir, err := os.MkdirTemp("", "btelo-passes-*")
		if err != nil {
			return nil, err
		}
		job.Dir = dir
		const log = "pass"
		first := spec
		first.Audio = false
		g1, err := Build(first)
		if err != nil {
			job.Close()
			return nil, err
		}
		args, err := g1.Args(append(append(append([]string{}, video...), passArgs(name, 1, log)...),
			"-an", "-progress", "pipe:1", "-nostats", "-f", "null", os.DevNull)...)
		if err != nil {
			job.Close()
			return nil, err
		}
		job.Passes = append(job.Passes, args)
		video = append(video, passArgs(name, 2, log)...)
	} else if name == "libx265" {
		video = append(video, "-x265-params", "log-level=error")
	}
	final := video
	if g.AudioOut != "" {
		final = append(final, "-c:a", "aac", "-b:a", strconv.Itoa(audioKbps)+"k", "-ac", strconv.Itoa(channels))
	}
	final = append(final, "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", "-f", "mp4", output)
	args, err := g.Args(final...)
	if err != nil {
		job.Close()
		return nil, err
	}
	job.Passes = append(job.Passes, args)
	return job, nil
}

// videoBitrate shares a target size between sound and picture.
func videoBitrate(targetMB, length float64, audioKbps int, audio bool) (int, error) {
	if !audio {
		audioKbps = 0
	}
	total := targetMB * (1 << 20) * 8 * containerShare / length / 1000
	video := total - float64(audioKbps)
	if video < minVideoKbps {
		need := (float64(minVideoKbps+audioKbps) * 1000 * length / 8 / containerShare) / (1 << 20)
		return 0, &TargetTooSmall{MinimumMB: math.Ceil(need*10) / 10}
	}
	return int(video), nil
}

func qualityArgs(name string, q int) []string {
	switch name {
	case "libaom-av1":
		return []string{"-crf", strconv.Itoa(q), "-b:v", "0"}
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		return []string{"-rc", "vbr", "-cq", strconv.Itoa(q), "-b:v", "0"}
	}
	return []string{"-crf", strconv.Itoa(q)}
}

func rateArgs(name string, kbps int) []string {
	rate := strconv.Itoa(kbps) + "k"
	switch name {
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		return []string{"-rc", "vbr", "-multipass", "fullres", "-b:v", rate, "-maxrate", strconv.Itoa(kbps*3/2) + "k", "-bufsize", strconv.Itoa(kbps*2) + "k"}
	}
	return []string{"-b:v", rate}
}

func speedArgs(name, preset string) []string {
	switch name {
	case "libaom-av1":
		return []string{"-cpu-used", preset, "-row-mt", "1", "-tiles", "2x2"}
	}
	return []string{"-preset", preset}
}

func passArgs(name string, pass int, log string) []string {
	if name == "libx265" {
		return []string{"-x265-params", fmt.Sprintf("log-level=error:pass=%d:stats=%s.x265", pass, log)}
	}
	return []string{"-pass", strconv.Itoa(pass), "-passlogfile", log}
}
