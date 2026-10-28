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

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// Destination is the file the export writes: "<name>.cut<ext>" in dir (beside the source when dir is empty),
// numbered when it already exists.
func Destination(source, dir, ext string) string {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	if dir == "" {
		dir = filepath.Dir(source)
	}
	for n := 1; ; n++ {
		name := base + ".cut" + ext
		if n > 1 {
			name = fmt.Sprintf("%s.cut-%d%s", base, n, ext)
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

// ErrCredits means the video was written but its credits file was not.
var ErrCredits = errors.New("the video was exported but its credits file could not be written")

// Analysing is the progress reported while a pass that cannot measure its progress runs.
const Analysing = -1.0

// Encode runs an export, reporting progress in [0, 1], or Analysing while it cannot tell; the result appears at
// output only when it succeeds. creditsHeading is the first line of the credits file written beside it when the
// music asks for attribution.
func Encode(ctx context.Context, p *project.Project, overlays []Overlay, output, creditsHeading string, progress func(float64)) error {
	enc, err := media.AvailableEncoders()
	if err != nil {
		return err
	}
	part := output + ".part"
	job, err := PlanExport(p, overlays, part, enc)
	if err != nil {
		return err
	}
	defer job.Close()
	for i, args := range job.Passes {
		k := i - job.Blind
		share := 1 / float64(len(job.Passes)-job.Blind)
		if k < 0 && progress != nil {
			progress(Analysing)
		}
		err := runPass(ctx, job, args, func(f float64) {
			if progress != nil && k >= 0 {
				progress((float64(k) + f) * share)
			}
		})
		if err != nil {
			os.Remove(part)
			return err
		}
	}
	if err := os.Rename(part, output); err != nil {
		os.Remove(part)
		return err
	}
	if credits := Credits(p); credits != "" && !p.Export.Codec.Animated() { // an animation has no sound to credit
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

// runPass runs one ffmpeg pass, reporting its own progress in [0, 1).
func runPass(ctx context.Context, job *Job, args []string, progress func(float64)) error {
	cmd, err := media.Command(ctx, "ffmpeg", args...)
	if err != nil {
		return err
	}
	cmd.Dir = job.Dir
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
		if f, ok := progressLine(scan.Text(), job.Length); ok {
			progress(f)
		}
	}
	if err := cmd.Wait(); err != nil || ctx.Err() != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg (%s): %w: %s", job.Encoder, err, strings.TrimSpace(stderr.String()))
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
