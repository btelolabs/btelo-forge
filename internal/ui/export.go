package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/render"
)

type exportJob struct {
	cancel   context.CancelFunc
	out      string
	progress float64
	measured time.Time // when the progress began to be measured, for the time left
	stopping bool
}

// lastExport is the outcome of the most recent export, for the MCP tools.
type lastExport struct {
	out string
	err error
	at  time.Time
}

// destination is where the current edit exports to: the export folder, or beside the source.
func (a *App) destination(p *project.Project) string {
	return render.Destination(p.Sources[0].Path, a.exportDir(), render.Extension(p.Export))
}

// setExport changes the edit's export settings and remembers them as the default for new edits.
func (a *App) setExport(e project.Export) {
	a.change(func() { a.p.Export = e })
	a.settings.Export = e
	a.saveSettings()
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}

// startExport renders the overlays at the output size and encodes the current edit; it returns the output path.
func (a *App) startExport() (string, error) {
	if a.p == nil {
		return "", errors.New(a.u.t("Open a video first"))
	}
	if a.export != nil {
		return "", errors.New(a.u.t("An export is already running"))
	}
	a.player.Stop()
	a.audition.Stop()
	snapshot := a.p.Clone()
	out := a.destination(snapshot)
	if dir := filepath.Dir(out); a.exportDir() != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.toast(a.u.tf("Export failed: %s", err), true)
			return "", err
		}
	}
	ctx, cancel := context.WithCancel(a.life)
	now := time.Now()
	job := &exportJob{cancel: cancel, out: out, measured: now}
	a.export = job
	a.background(func(_ context.Context) {
		err := a.encode(ctx, snapshot, out, func(f float64) {
			a.post(func() {
				if job.progress == render.Analysing && f >= 0 {
					job.measured = time.Now()
				}
				job.progress = f
			})
		})
		a.post(func() {
			a.export = nil
			a.last = lastExport{out: out, err: err, at: time.Now()}
			switch {
			case errors.Is(err, context.Canceled):
				a.toast(a.u.t("Export cancelled"), false)
			case err != nil && !errors.Is(err, render.ErrCredits):
				a.toast(a.u.tf("Export failed: %s", a.describe(err)), true)
			default:
				if err != nil {
					a.toast(a.describe(err), true)
				}
				a.rememberExport(out)
				a.toastActions(a.u.tf("Exported %s", filepath.Base(out)), false, []toastAction{
					{a.u.t("Show in folder"), func() { a.host.Reveal(out) }},
					{a.u.t("Play"), func() { a.host.Launch(out) }},
				})
			}
		})
	})
	return out, nil
}

func (a *App) encode(ctx context.Context, p *project.Project, out string, progress func(float64)) error {
	tmp, err := os.MkdirTemp("", "btelo-forge-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	w, h := render.OutputSize(p.Canvas, p.Export.Height)
	end := p.Duration()
	var overlays []render.Overlay
	for _, tr := range p.Overlays {
		if tr.Hidden {
			continue
		}
		for _, o := range tr.Items {
			if o.Start >= end {
				continue
			}
			img, ok := a.overlayImage(o, w, h)
			if !ok {
				continue
			}
			path := filepath.Join(tmp, fmt.Sprintf("overlay-%d.png", o.ID))
			if err := img.WritePNG(path); err != nil {
				return err
			}
			overlays = append(overlays, render.Overlay{Path: path, X: img.At.X, Y: img.At.Y, Start: o.Start, End: math.Min(o.End, end)})
		}
	}
	return render.Encode(ctx, p, overlays, out, a.u.t("Music used in this video:"), progress)
}

func (a *App) drawExportModal() {
	job := a.export
	if job == nil {
		return
	}
	u := &a.u
	u.c.Fill(a.screen, gfxShade)
	w, h := min(u.px(460), a.screen.W-u.px(40)), u.px(190)
	r := rectCentered(a.screen, w, h)
	panel(u, r)
	in := r.Inset(u.px(22))
	u.label(u.bold(16), gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(24)}, u.t("Exporting…"), colText, 0)
	u.label(u.face(12), gfx.Rect{X: in.X, Y: in.Y + u.px(28), W: in.W, H: u.px(20)}, filepath.Base(job.out), colDim, 0)
	bar := gfx.Rect{X: in.X, Y: in.Y + u.px(66), W: in.W, H: u.px(10)}
	// The first pass of a GIF cannot tell how far it is (render.Analysing): a block sweeps the bar while it runs.
	u.progressBar(bar, job.progress)
	var status string
	if job.progress == render.Analysing {
		status = u.t("Choosing the colours…")
	} else {
		status = fmt.Sprintf("%d%%", int(job.progress*100))
		if el := time.Since(job.measured).Seconds(); job.progress > 0.02 {
			status += u.tf(" · about %s left", shortClock(math.Max(el/job.progress-el, 0)))
		}
	}
	if job.stopping {
		status = u.t("Cancelling…")
	}
	u.label(u.face(12.5), gfx.Rect{X: in.X, Y: bar.Bottom() + u.px(8), W: in.W, H: u.px(20)}, status, colDim, 0)
	if u.button("export/cancel", gfx.Rect{X: in.Right() - u.px(90), Y: in.Bottom() - u.px(34), W: u.px(90), H: u.px(34)}, "", u.t("Cancel"), plain, !job.stopping) {
		job.stopping = true
		job.cancel()
	}
	u.keys = nil
}
