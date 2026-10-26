package media

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"strconv"
)

// ErrNoFrame means the decoder produced no picture at the requested time (usually past the last frame).
var ErrNoFrame = errors.New("no picture at that time")

func seconds(t float64) string { return strconv.FormatFloat(max(t, 0), 'f', 6, 64) }

// Frame decodes the picture at source time t scaled to w×h.
func Frame(ctx context.Context, path string, t float64, w, h int) (*image.RGBA, error) {
	return grab(ctx, path, t, w, h, false)
}

// Thumbnail decodes the keyframe nearest t scaled to w×h; it is much faster than Frame and good enough for strips.
func Thumbnail(ctx context.Context, path string, t float64, w, h int) (*image.RGBA, error) {
	return grab(ctx, path, t, w, h, true)
}

func grab(ctx context.Context, path string, t float64, w, h int, keyframe bool) (*image.RGBA, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid picture size %d×%d", w, h)
	}
	args := []string{"-v", "error", "-nostdin"}
	if keyframe {
		args = append(args, "-skip_frame", "nokey")
	}
	args = append(args, "-ss", seconds(t), "-i", path, "-an", "-sn", "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=bilinear", w, h), "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	out, err := run(ctx, "ffmpeg", args...)
	if err != nil {
		return nil, err
	}
	if len(out) < w*h*4 {
		return nil, ErrNoFrame
	}
	return &image.RGBA{Pix: out[:w*h*4], Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, nil
}

// Peaks returns the audio loudness envelope of path: perSecond values per second, each the peak in [0, 1].
func Peaks(ctx context.Context, path string, perSecond int) ([]float32, error) {
	const sampleRate = 8000
	if perSecond <= 0 || perSecond > sampleRate {
		return nil, fmt.Errorf("invalid waveform density %d", perSecond)
	}
	cmd, err := Command(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", path, "-vn", "-sn", "-ac", "1",
		"-ar", strconv.Itoa(sampleRate), "-f", "s16le", "pipe:1")
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	peaks, readErr := envelope(bufio.NewReaderSize(stdout, 1<<16), sampleRate/perSecond)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil && len(peaks) == 0 {
		return nil, fmt.Errorf("reading the waveform: %w", waitErr)
	}
	return peaks, nil
}

// envelope folds s16 mono samples into the peak of every bin samples.
func envelope(r io.Reader, bin int) ([]float32, error) {
	var peaks []float32
	buf := make([]byte, 2*bin)
	for {
		n, err := io.ReadFull(r, buf)
		if n >= 2 {
			peak := 0
			for i := 0; i+1 < n; i += 2 {
				v := int(int16(binary.LittleEndian.Uint16(buf[i:])))
				if v < 0 {
					v = -v
				}
				peak = max(peak, v)
			}
			peaks = append(peaks, min(float32(peak)/32767, 1))
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return peaks, nil
		}
		if err != nil {
			return peaks, err
		}
	}
}
