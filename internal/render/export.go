package render

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-video-cut/internal/media"
	"github.com/btelolabs/btelo-video-cut/internal/project"
)

// crf is the constant-quality factor per quality step; x265 needs about five more for the same look.
var crf = map[project.Quality]int{project.High: 18, project.Balanced: 23, project.Small: 28}

// Destination is the file the export writes: "<name>.cut.mp4" in dir (beside the source when dir is empty),
// numbered when it already exists.
func Destination(source, dir string) string {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	if dir == "" {
		dir = filepath.Dir(source)
	}
	for n := 1; ; n++ {
		name := base + ".cut.mp4"
		if n > 1 {
			name = fmt.Sprintf("%s.cut-%d.mp4", base, n)
		}
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path
		}
	}
}

// Credits lists the attributions the music in p asks for, one per line, without duplicates.
func Credits(p *project.Project) string {
	seen := map[string]bool{}
	var lines []string
	for _, t := range p.Music {
		for _, m := range t.Clips {
			if m.Credit != "" && !seen[m.Credit] {
				seen[m.Credit] = true
				lines = append(lines, m.Credit)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// ExportArgs is the full ffmpeg command line that encodes p with its captions into output.
func ExportArgs(p *project.Project, overlays []Overlay, output string) ([]string, float64, error) {
	w, h := OutputSize(p.Canvas, p.Export.Height)
	g, err := Build(Spec{Project: p, From: 0, To: p.Duration(), Width: w, Height: h, FPS: p.Canvas.FPS, Video: true, Audio: true, Overlays: overlays})
	if err != nil {
		return nil, 0, err
	}
	var enc []string
	if p.Export.HEVC {
		enc = []string{"-c:v", "libx265", "-preset", "medium", "-crf", strconv.Itoa(crf[p.Export.Quality] + 5), "-tag:v", "hvc1", "-x265-params", "log-level=error"}
	} else {
		enc = []string{"-c:v", "libx264", "-preset", "medium", "-crf", strconv.Itoa(crf[p.Export.Quality])}
	}
	enc = append(enc, "-pix_fmt", "yuv420p")
	if g.AudioOut != "" {
		enc = append(enc, "-c:a", "aac", "-b:a", "192k")
	}
	enc = append(enc, "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", "-f", "mp4", output)
	args, err := g.Args(enc...)
	return args, g.Length, err
}

// ErrCredits means the video was written but its credits file was not.
var ErrCredits = errors.New("the video was exported but its credits file could not be written")

// Encode runs an export, reporting progress in [0, 1]; the result appears at output only when it succeeds.
// creditsHeading is the first line of the credits file written beside it when the music asks for attribution.
func Encode(ctx context.Context, p *project.Project, overlays []Overlay, output, creditsHeading string, progress func(float64)) error {
	part := output + ".part"
	args, length, err := ExportArgs(p, overlays, part)
	if err != nil {
		return err
	}
	cmd, err := media.Command(ctx, "ffmpeg", args...)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scan := bufio.NewScanner(stdout)
	for scan.Scan() {
		if f, ok := progressLine(scan.Text(), length); ok && progress != nil {
			progress(f)
		}
	}
	runErr := cmd.Wait()
	if runErr != nil || ctx.Err() != nil {
		os.Remove(part)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	if err := os.Rename(part, output); err != nil {
		os.Remove(part)
		return err
	}
	if credits := Credits(p); credits != "" {
		path := strings.TrimSuffix(output, filepath.Ext(output)) + ".credits.txt"
		if err := os.WriteFile(path, []byte(creditsHeading+"\r\n"+strings.ReplaceAll(credits, "\n", "\r\n")+"\r\n"), 0o644); err != nil {
			return fmt.Errorf("%w: %w", ErrCredits, err)
		}
	}
	if progress != nil {
		progress(1)
	}
	return nil
}

// progressLine reads ffmpeg's "-progress" key=value output; out_time_us and out_time_ms are both microseconds.
func progressLine(line string, length float64) (float64, bool) {
	key, value, ok := strings.Cut(line, "=")
	if !ok || (key != "out_time_us" && key != "out_time_ms") || length <= 0 {
		return 0, false
	}
	us, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || us < 0 {
		return 0, false
	}
	return min(float64(us)/1e6/length, 0.999), true
}
