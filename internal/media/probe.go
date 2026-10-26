package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Info describes a media file as displayed: width and height already account for rotation.
type Info struct {
	Duration      float64
	Width, Height int
	FPS           float64
	HasVideo      bool
	HasAudio      bool
}

type probeOutput struct {
	Streams []struct {
		CodecType    string            `json:"codec_type"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		RFrameRate   string            `json:"r_frame_rate"`
		Duration     string            `json:"duration"`
		Tags         map[string]string `json:"tags"`
		Disposition  map[string]int    `json:"disposition"`
		SideData     []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe reads the duration, display size, frame rate and stream kinds of path.
func Probe(ctx context.Context, path string) (Info, error) {
	out, err := run(ctx, "ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", path)
	if err != nil {
		return Info{}, err
	}
	return parseProbe(out)
}

func parseProbe(out []byte) (Info, error) {
	var p probeOutput
	if err := json.Unmarshal(out, &p); err != nil {
		return Info{}, fmt.Errorf("parsing ffprobe output: %w", err)
	}
	var info Info
	info.Duration = number(p.Format.Duration)
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if info.HasVideo || s.Disposition["attached_pic"] == 1 {
				continue
			}
			info.HasVideo = true
			info.Width, info.Height = s.Width, s.Height
			rotation := number(s.Tags["rotate"])
			for _, d := range s.SideData {
				if d.Rotation != 0 {
					rotation = d.Rotation
				}
			}
			if r := math.Mod(math.Abs(rotation), 180); r > 45 && r < 135 {
				info.Width, info.Height = info.Height, info.Width
			}
			info.FPS = rate(s.AvgFrameRate)
			if info.FPS <= 0 || info.FPS > 240 {
				info.FPS = rate(s.RFrameRate)
			}
			if info.Duration <= 0 {
				info.Duration = number(s.Duration)
			}
		case "audio":
			info.HasAudio = true
			if info.Duration <= 0 {
				info.Duration = number(s.Duration)
			}
		}
	}
	if !info.HasVideo && !info.HasAudio {
		return Info{}, errors.New("the file has no audio or video streams")
	}
	if info.Duration <= 0 {
		return Info{}, errors.New("the duration cannot be read")
	}
	if info.HasVideo && (info.FPS <= 0 || info.FPS > 240) {
		info.FPS = 30
	}
	return info, nil
}

func number(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func rate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return number(s)
	}
	d := number(den)
	if d == 0 {
		return 0
	}
	return number(num) / d
}
