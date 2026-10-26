package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/project"
	"github.com/btelolabs/btelo-video-cut/internal/render"
)

type exportJob struct {
	cancel   context.CancelFunc
	out      string
	progress float64
	started  time.Time
	stopping bool
}

// lastExport is the outcome of the most recent export, for the MCP tools.
type lastExport struct {
	out string
	err error
	at  time.Time
}

var resolutionNames = msgs("Original", "1080p", "720p", "480p")
var resolutionHeights = []int{0, 1080, 720, 480}
var qualityNames = msgs("High quality", "Balanced", "Small file")
var codecNames = msgs("H.264 · plays anywhere", "H.265 · smaller")

// destination is where the current edit exports to: the default folder from the settings, or beside the source.
func (a *App) destination(p *project.Project) string {
	return render.Destination(p.Sources[0].Path, a.settings.ExportDir)
}

// setExport changes the edit's export settings and remembers them as the default for new edits.
func (a *App) setExport(e project.Export) {
	a.change(func() { a.p.Export = e })
	a.settings.Export = e
	a.saveSettings()
}

// drawExportPanel draws the export tab and returns the bottom of its content.
func (a *App) drawExportPanel(r gfx.Rect) float32 {
	u := &a.u
	e := a.p.Export
	y := r.Y
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Saved to"))
	y += u.px(26)
	out := a.destination(a.p)
	f := u.face(12)
	lines := f.Wrap(out, r.W)
	for _, l := range lines[:min(3, len(lines))] {
		u.c.Text(f, r.X, y, l, colText)
		y += f.Height() * 1.25
	}
	y += u.px(4)
	if u.button("export/dir", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(30)}, icFolder, u.t("Change the default folder in Settings"), ghost, true) {
		a.settingsOpen = true
	}
	y += u.px(42)

	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Resolution"))
	y += u.px(26)
	res := 0
	for k, h := range resolutionHeights {
		if h == e.Height {
			res = k
		}
	}
	if pick := u.chips("export/res", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(30)}, u.ts(resolutionNames), res, true); pick != res {
		if h := resolutionHeights[pick]; h == 0 || h < a.p.Canvas.Height {
			e.Height = h
			a.setExport(e)
		} else {
			a.toast(u.tf("The original is only %dp; it is never upscaled", a.p.Canvas.Height), false)
		}
	}
	y += u.px(42)
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Quality / compression"))
	y += u.px(26)
	if pick := u.chips("export/quality", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(30)}, u.ts(qualityNames), int(e.Quality), true); pick != int(e.Quality) {
		e.Quality = project.Quality(pick)
		a.setExport(e)
	}
	y += u.px(42)
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Codec"))
	y += u.px(26)
	codec := 0
	if e.HEVC {
		codec = 1
	}
	if pick := u.chips("export/codec", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(30)}, u.ts(codecNames), codec, true); pick != codec {
		e.HEVC = pick == 1
		a.setExport(e)
	}
	y += u.px(46)

	w, h := render.OutputSize(a.p.Canvas, a.p.Export.Height)
	clips, music, overlays := 0, 0, 0
	for _, t := range a.p.Video {
		clips += len(t.Clips)
	}
	for _, t := range a.p.Music {
		music += len(t.Clips)
	}
	for _, t := range a.p.Overlays {
		overlays += len(t.Items)
	}
	codecName := "H.264"
	if a.p.Export.HEVC {
		codecName = "H.265"
	}
	info := []string{
		u.tf("Output %d×%d · %s · length %s", w, h, codecName, clock(a.p.Duration())),
		u.tf("%d clips · %d music clips · %d overlays", clips, music, overlays),
	}
	if st, err := os.Stat(a.p.Sources[0].Path); err == nil {
		info = append(info, u.tf("Original %s · for the smallest file pick Small file and H.265", size(st.Size())))
	}
	for _, l := range info {
		u.label(u.face(12), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(20)}, l, colDim, 0)
		y += u.px(22)
	}
	y += u.px(12)
	if u.button("export/go", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(42)}, icExport, u.t("Export video (Ctrl+E)"), primary, a.export == nil) {
		a.startExport()
	}
	y += u.px(52)
	u.label(u.face(11), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(18)}, u.t("These settings are remembered for your next edits"), colFaint, 0)
	return y + u.px(18)
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
	if dir := filepath.Dir(out); a.settings.ExportDir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.toast(a.u.tf("Export failed: %s", err), true)
			return "", err
		}
	}
	ctx, cancel := context.WithCancel(a.life)
	job := &exportJob{cancel: cancel, out: out, started: time.Now()}
	a.export = job
	a.background(func(_ context.Context) {
		err := a.encode(ctx, snapshot, out, func(f float64) { a.post(func() { job.progress = f }) })
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
	tmp, err := os.MkdirTemp("", "btelo-video-cut-*")
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
	u.c.RoundRect(bar, u.px(5), colSurface)
	u.c.RoundRect(gfx.Rect{X: bar.X, Y: bar.Y, W: max(bar.W*float32(job.progress), bar.H), H: bar.H}, u.px(5), colAccent)
	status := fmt.Sprintf("%d%%", int(job.progress*100))
	if el := time.Since(job.started).Seconds(); job.progress > 0.02 {
		status += u.tf(" · about %s left", shortClock(math.Max(el/job.progress-el, 0)))
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
