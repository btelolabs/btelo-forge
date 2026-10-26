package ui

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/project"
	"github.com/btelolabs/btelo-video-cut/internal/render"
)

// exportForm is the export tab's own state: the target size being typed.
type exportForm struct {
	target string
}

// targetChoices are quick sizes for the "fit a size" preset, in megabytes.
var targetChoices = []float64{10, 25, 50, 100, 500}

// drawExportPanel draws the export tab and returns the bottom of its content.
func (a *App) drawExportPanel(r gfx.Rect) float32 {
	u := &a.u
	e := a.p.Export
	set := func(n project.Export) {
		if n != e {
			a.setExport(n)
			e = n
		}
	}
	y := r.Y
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Saved to"))
	y += u.px(26)
	f := u.face(12)
	lines := f.Wrap(a.destination(a.p), r.W)
	for _, l := range lines[:min(3, len(lines))] {
		u.c.Text(f, r.X, y, l, colText)
		y += f.Height() * 1.25
	}
	y += u.px(4)
	if u.button("export/dir", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(30)}, icFolder, u.t("Change the default folder in Settings"), ghost, true) {
		a.settingsOpen = true
	}
	y += u.px(42)

	av1 := a.encoders != nil && codecAvailable(a.encoders, project.AV1, false)
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Compression"))
	y += u.px(26)
	matched := matchingPreset(withoutHardware(e), av1)
	for i, p := range exportPresets {
		row := gfx.Rect{X: r.X, Y: y, W: r.W}
		note := u.face(11).Wrap(u.t(p.note), r.W-u.px(40))
		row.H = u.px(30) + float32(len(note))*u.face(11).Height()*1.2
		id := "export/preset/" + p.id
		selected := i == matched
		bg := colSurface
		if selected {
			bg = gfx.Mix(colSurface, colAccent, 0.18)
		} else if u.hover(id, row) {
			bg = colSurfaceHi
			u.cursor = CursorHand
		}
		u.c.RoundRect(row, u.px(7), bg)
		dot := gfx.Rect{X: row.X + u.px(10), Y: row.Y + u.px(9), W: u.px(14), H: u.px(14)}
		if selected {
			u.c.Circle(dot.X+dot.W/2, dot.Y+dot.H/2, u.px(7), colAccent)
			u.c.Circle(dot.X+dot.W/2, dot.Y+dot.H/2, u.px(3), colOnAccent)
		} else {
			u.c.RoundOutline(dot, u.px(7), u.px(1.5), colFaint)
		}
		u.label(u.bold(12.5), gfx.Rect{X: row.X + u.px(32), Y: row.Y + u.px(5), W: row.W - u.px(40), H: u.px(22)}, u.t(p.name), colText, 0)
		for k, l := range note {
			u.c.Text(u.face(11), row.X+u.px(32), row.Y+u.px(27)+float32(k)*u.face(11).Height()*1.2, l, colDim)
		}
		if u.clicked(id, row) {
			n := p.export(av1)
			if n.TargetMB > 0 && e.TargetMB > 0 {
				n.TargetMB = e.TargetMB
			}
			n.Hardware = e.Hardware && a.encoders != nil && hardwareAvailable(a.encoders, n.Codec)
			set(n)
		}
		y = row.Bottom() + u.px(4)
	}
	if matched < 0 {
		u.label(u.face(11.5), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(20)}, u.t("Custom settings (below)"), colAccentHi, 0)
		y += u.px(22)
	}
	y += u.px(10)

	if e.TargetMB > 0 {
		y = a.drawTargetSize(r, y, e, set)
	}
	y = a.drawExportDetails(r, y, e, set)
	y += u.px(4)
	u.label(u.face(11), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(18)}, u.t("These settings are remembered for your next edits"), colFaint, 0)
	return y + u.px(18)
}

// drawExportTab draws the export settings scrolling above a footer, pinned to the bottom, with what will be made
// and the export button, so exporting never needs a scroll.
func (a *App) drawExportTab(r gfx.Rect) {
	u := &a.u
	info := a.exportSummary(r.W)
	footerH := float32(len(info))*u.px(20) + u.px(14+42)
	footer := gfx.Rect{X: r.X, Y: r.Bottom() - footerH, W: r.W, H: footerH}
	a.scrolled(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: max(footer.Y-r.Y-u.px(10), 0)}, a.drawExportPanel)
	u.c.Fill(gfx.Rect{X: r.X, Y: footer.Y - u.px(6), W: r.W, H: max(1, u.px(1))}, colBorder)
	y := footer.Y + u.px(4)
	for _, l := range info {
		u.label(u.face(12), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(20)}, l, colDim, 0)
		y += u.px(20)
	}
	if u.button("export/go", gfx.Rect{X: r.X, Y: footer.Bottom() - u.px(42), W: r.W, H: u.px(42)}, icExport, u.t("Export video (Ctrl+E)"), primary, a.export == nil) {
		a.startExport()
	}
}

// exportSummary describes the file the current settings make, wrapped to width.
func (a *App) exportSummary(width float32) []string {
	u := &a.u
	e := a.p.Export
	w, h := render.OutputSize(a.p.Canvas, e.Height)
	encoder := codecShortNames[e.Codec]
	if a.encoders != nil {
		if name := render.Encoder(e.Codec, e.Hardware, a.encoders); name != "" {
			encoder += " (" + name + ")"
		}
	}
	info := []string{u.tf("Output %d×%d · %s fps · %s · length %s", w, h, rate(render.OutputRate(a.p.Canvas, e)), encoder, clock(a.p.Duration()))}
	if st, err := os.Stat(a.p.Sources[0].Path); err == nil {
		line := u.tf("Original file %s", size(st.Size()))
		if e.TargetMB > 0 {
			line += " · " + u.tf("aiming for at most %s MB", trimNumber(e.TargetMB))
		}
		info = append(info, line)
	}
	var lines []string
	for _, l := range info {
		lines = append(lines, u.face(12).Wrap(l, width)...)
	}
	return lines
}

// withoutHardware compares presets on what they choose; the graphics card is a separate switch.
func withoutHardware(e project.Export) project.Export {
	e.Hardware = false
	return e
}

func (a *App) drawTargetSize(r gfx.Rect, y float32, e project.Export, set func(project.Export)) float32 {
	u := &a.u
	form := &a.exportForm
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Target size (MB)"))
	y += u.px(26)
	if u.focus != "export/target" {
		form.target = trimNumber(e.TargetMB)
	}
	if _, done := u.textBox("export/target", gfx.Rect{X: r.X, Y: y, W: u.px(110), H: u.px(32)}, &form.target, "25", false); done {
		v, err := strconv.ParseFloat(strings.TrimSpace(form.target), 64)
		if err != nil || v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
			a.toast(u.t("Type a size in megabytes, such as 25"), true)
		} else {
			n := e
			n.TargetMB = v
			set(n)
		}
	}
	names := make([]string, len(targetChoices))
	current := -1
	for i, mb := range targetChoices {
		names[i] = trimNumber(mb) + " MB"
		if mb == e.TargetMB {
			current = i
		}
	}
	if pick, _ := u.flowChips("export/targets", gfx.Rect{X: r.X + u.px(118), Y: y + u.px(2), W: r.W - u.px(118), H: u.px(28)}, names, current); pick != current {
		n := e
		n.TargetMB = targetChoices[pick]
		set(n)
	}
	y += u.px(70)
	if d := a.p.Duration(); d > 0 {
		kbps := e.TargetMB * (1 << 20) * 8 / d / 1000
		y = a.paragraph(r.X, y, r.W, u.tf("About %.0f kbit/s for %s of video; the picture adapts to fit.", kbps, clock(d)), 11.5, colFaint) + u.px(8)
	}
	return y
}

// drawExportDetails draws every individual export setting.
func (a *App) drawExportDetails(r gfx.Rect, y float32, e project.Export, set func(project.Export)) float32 {
	u := &a.u
	u.heading(gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(22)}, u.t("Details"))
	y += u.px(28)
	row := func(title string) {
		u.label(u.face(12), gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(18)}, title, colDim, 0)
		y += u.px(22)
	}
	chips := func(id string, names []string, current int, enabled []bool) int {
		pick, h := u.flowChipsSome(id, gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(28)}, u.ts(names), current, enabled)
		y += h + u.px(12)
		return pick
	}

	row(u.t("Resolution (short side, never upscaled)"))
	if k := indexOf(exportHeights, e.Height); true {
		if pick := chips("export/res", heightNames, k, nil); pick != k {
			n := e
			n.Height = exportHeights[pick]
			set(n)
		}
	}
	row(u.t("Frame rate"))
	if k := indexOf(exportRates, e.FPS); true {
		if pick := chips("export/fps", rateNames, k, nil); pick != k {
			n := e
			n.FPS = exportRates[pick]
			set(n)
		}
	}
	row(u.t("Format"))
	available := make([]bool, len(codecIDs))
	for i := range codecIDs {
		available[i] = a.encoders == nil && project.Codec(i) != project.AV1 || a.encoders != nil && codecAvailable(a.encoders, project.Codec(i), false)
	}
	if pick := chips("export/codec", codecNames, int(e.Codec), available); pick != int(e.Codec) {
		n := e
		n.Codec = project.Codec(pick)
		n.Hardware = n.Hardware && hardwareAvailable(a.encoders, n.Codec)
		set(n)
	}
	if !available[project.AV1] && a.encoders != nil {
		y = a.paragraph(r.X, y-u.px(6), r.W, u.t("AV1 needs an ffmpeg built with libsvtav1 or libaom (the full builds have them)."), 11, colFaint) + u.px(8)
	}
	row(u.t("Picture quality"))
	if e.TargetMB > 0 {
		y = a.paragraph(r.X, y, r.W, u.t("Set by the target size"), 11.5, colFaint) + u.px(10)
	} else if pick := chips("export/quality", qualityNames, int(e.Quality), nil); pick != int(e.Quality) {
		n := e
		n.Quality = project.Quality(pick)
		set(n)
	}
	row(u.t("Encoding speed (slower makes smaller files)"))
	if k := indexOf(speedOrder, e.Speed); true {
		if pick := chips("export/speed", speedNames, k, nil); pick != k {
			n := e
			n.Speed = speedOrder[pick]
			set(n)
		}
	}
	row(u.t("Sound"))
	if pick := chips("export/audio", audioNames, int(e.Audio), nil); pick != int(e.Audio) {
		n := e
		n.Audio = project.Audio(pick)
		set(n)
	}
	hw := a.encoders != nil && hardwareAvailable(a.encoders, e.Codec)
	label := u.t("Encode on the NVIDIA graphics card (much faster, slightly larger files)")
	if !hw {
		label = u.tf("Graphics card encoding is not available for %s on this computer", codecShortNames[e.Codec])
	}
	cb := gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(26)}
	if hw {
		if on := u.checkbox("export/hardware", cb, label, e.Hardware); on != e.Hardware {
			n := e
			n.Hardware = on
			set(n)
		}
	} else {
		u.label(u.face(11.5), cb, label, colFaint, 0)
	}
	y += u.px(32)
	if limit := u.checkbox("export/limit", gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(26)}, u.t("Limit the file size"), e.TargetMB > 0); limit != (e.TargetMB > 0) {
		n := e
		n.TargetMB = 0
		if limit {
			n.TargetMB = defaultTargetMB
		}
		set(n)
	}
	return y + u.px(40)
}

// trimNumber writes a number without trailing zeros.
func trimNumber(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// rate writes a frame rate, with decimals only when it has them.
func rate(fps float64) string {
	if fps == math.Round(fps) {
		return fmt.Sprintf("%.0f", fps)
	}
	return fmt.Sprintf("%.2f", fps)
}
