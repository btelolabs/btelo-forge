package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/proc"
)

// aiOrphanCheck is how often the programs an earlier run left running are looked for again: each look asks the system
// about every one of them.
const aiOrphanCheck = 2 * time.Second

// aiOrphans are the model programs an earlier run left running and that still run, found when the app started and
// looked at again as time goes by.
func (a *App) aiOrphans() []proc.Entry {
	s := &a.ai
	if len(s.orphans) > 0 && time.Since(s.orphansAt) > aiOrphanCheck {
		s.orphans, s.orphansAt = proc.Orphans(), time.Now()
	}
	return s.orphans
}

// aiLabelName is the name of the model a helper program was started for, from the label it was registered with.
func (a *App) aiLabelName(label string) string {
	lang := string(a.u.lang)
	kind, id, _ := strings.Cut(label, ":")
	switch kind {
	case "model":
		if m, ok := a.ai.config.Model(id); ok {
			return m.Name.In(lang)
		}
	case "speech":
		if m, ok := a.speech.config.Model(id); ok {
			return m.Name.In(lang)
		}
	}
	return label
}

// aiCostText is what a running program takes of the computer, as one short line.
func (a *App) aiCostText(u proc.Usage, measured bool) string {
	t := &a.u
	if !measured {
		return t.t("measuring…")
	}
	parts := []string{t.tf("RAM %s", size(int64(u.Memory)))}
	if u.GPUKnown {
		parts = append(parts, t.tf("VRAM %s", size(int64(u.GPUMemory))))
	}
	return strings.Join(parts, "  ·  ")
}

// aiRunning lists the model programs running, as cards in the window's corner whichever page is in front: a model of the
// list, the voice-over engine, and what an earlier run left behind. Each can be stopped from its card.
func (a *App) aiRunning() []task {
	u := &a.u
	var out []task
	for _, st := range a.ai.rt.Statuses() {
		if st.State != ai.Ready {
			continue
		}
		id := st.Model
		name := id
		if m, ok := a.ai.config.Model(id); ok {
			name = m.Name.In(string(u.lang))
		}
		out = append(out, task{id: "model/" + id, glyph: icAI, title: name, running: true, cancelHint: u.t("Stop the model"),
			status: u.t("Running") + "  ·  " + a.aiCostText(st.Usage, st.Measured), fraction: -1,
			cancel: func() { a.ai.rt.Unload(id) }, open: a.openAITab})
	}
	if model, pid, ok := a.speech.server.Info(); ok && pid != 0 {
		name := model
		if m, found := a.speech.config.Model(model); found {
			name = m.Name.In(string(u.lang))
		}
		usage, measured := a.ai.rt.Usage("speech")
		out = append(out, task{id: "speech-engine", glyph: icVoice, title: name, running: true, cancelHint: u.t("Stop the model"),
			status: u.t("Running") + "  ·  " + a.aiCostText(usage, measured), fraction: -1, cancel: a.speech.server.Stop})
	}
	for _, e := range a.aiOrphans() {
		pid := e.Pid
		out = append(out, task{id: "orphan/" + strconv.Itoa(pid), glyph: icAI, title: a.aiLabelName(e.Label), running: true, cancelHint: u.t("Stop the model"),
			status: u.t("Left running by an earlier run"), fraction: -1, cancel: func() { a.aiKillOrphan(pid) }})
	}
	return out
}

// aiKillOrphan ends a model program an earlier run left running.
func (a *App) aiKillOrphan(pid int) {
	proc.KillOrphan(pid)
	a.ai.orphans, a.ai.orphansAt = proc.Orphans(), time.Now()
	a.requestFrame()
}

// aiTasks are the cards for the model programs running. They are left out while the chat is in front, which lists them
// itself in its own place (drawAIRunning), so that no card sits over its input.
func (a *App) aiTasks() []task {
	if a.aiTab {
		return nil
	}
	return a.aiRunning()
}

// aiRunningOthers are the programs running that the chat's header does not show: all but the local model it talks to.
func (a *App) aiRunningOthers(ch ai.Choice) []task {
	var out []task
	for _, t := range a.aiRunning() {
		if !ch.External() && t.id == "model/"+ch.ID {
			continue
		}
		out = append(out, t)
	}
	return out
}

// aiRunningRow is the height of a line of drawAIRunning.
const aiRunningRow = 30

// aiRunningHeight is the height drawAIRunning takes for n programs, with the space under it.
func (a *App) aiRunningHeight(n int) float32 {
	if n == 0 {
		return 0
	}
	return a.u.px(float32(aiRunningRow*n + 6))
}

// drawAIRunning lists the model programs running that the header does not show, each with the way to stop it, under
// the status line of the chat.
func (a *App) drawAIRunning(r gfx.Rect, items []task) {
	u := &a.u
	for i, t := range items {
		row := rectAt(r.X, r.Y+float32(i)*u.px(aiRunningRow), r.W, u.px(aiRunningRow-2))
		u.c.Fill(row, colSurface)
		u.c.Outline(row, max(1, u.px(1)), colBorder)
		u.c.Circle(row.X+u.px(14), row.Y+row.H/2, u.px(4.5), colSuccess)
		nf := u.bold(12.5)
		u.c.Text(nf, row.X+u.px(28), row.Y+(row.H-nf.Height())/2, t.title, colText)
		sf := u.face(12)
		label := u.t("Stop the model")
		bw := u.face(13).Measure(label) + u.px(48) // the symbol, the words and room round them
		btn := rectAt(row.Right()-bw-u.px(4), row.Y+u.px(2), bw, row.H-u.px(4))
		sx := row.X + u.px(28) + nf.Measure(t.title) + u.px(12)
		u.c.Text(sf, sx, row.Y+(row.H-sf.Height())/2, sf.Ellipsize(t.status, max(btn.X-sx-u.px(8), 0)), colDim)
		if u.button("ai/run/"+t.id, btn, icStop, label, plain, t.cancel != nil) {
			t.cancel()
		}
	}
}
