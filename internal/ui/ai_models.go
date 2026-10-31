package ui

import (
	"image/color"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/proc"
)

// aiKindTitles name the kinds of model, in the order of ai.Kinds.
var aiKindTitles = msgs("LLM and multimodal", "Text to image", "Text to speech", "Speech to text", "Image to 3D", "Text or image to video", "Find the subject")

// aiKindName is what a kind of model is called in the lists.
func (a *App) aiKindName(k ai.Kind) string {
	i := slices.Index(ai.Kinds, k)
	if i < 0 {
		return string(k)
	}
	return a.u.t(aiKindTitles[i])
}

// aiRow is one model in Settings → AI models, whichever part of the app uses it.
type aiRow struct {
	key       string
	kind      ai.Kind
	name      string
	note      string
	detail    string // the second line, when it is not the usual: what it costs, or how large it is
	disk      int64
	installed bool
	state     ai.State
	stage     string
	fraction  float64
	err       error
	inUse     bool // it is working now, without being held in memory: a subtitle model during a job
	usage     proc.Usage
	measured  bool
	load      func() // bring it into memory; nil when it loads by itself when needed
	unload    func() // take it out of memory; nil when it is not
	remove    func() // delete its files; nil when that is not offered here
}

// aiRows lists every AI model the app has or can fetch: the chat models, the voice-over models and the subtitle
// models. Those on this computer come first.
func (a *App) aiRows() (installed, available []aiRow) {
	u := &a.u
	lang := string(u.lang)
	for _, m := range a.ai.config.Models {
		m := m
		st := a.ai.rt.Status(m.ID)
		disk, have := a.ai.rt.Files(m)
		row := aiRow{key: "chat/" + m.ID, kind: m.Kind.Or(), name: m.Name.In(lang), note: m.Note.In(lang), disk: disk, installed: have,
			state: st.State, stage: st.Stage, fraction: st.Fraction, err: st.Err, usage: st.Usage, measured: st.Measured}
		row.load = func() { a.aiLoad(m) }
		if st.State != ai.Unloaded && st.State != ai.Failed {
			row.unload = func() { a.ai.rt.Unload(m.ID) }
		}
		if have && st.State != ai.Ready && st.State != ai.Loading && st.State != ai.Downloading {
			row.remove = func() {
				if err := a.ai.rt.Delete(m); err != nil {
					a.toast(a.describeAI(err), true)
				}
				a.ai.removing = ""
			}
		}
		if have || st.State != ai.Unloaded {
			installed = append(installed, row)
		} else {
			row.disk = m.Size + extraSize(m)
			available = append(available, row)
		}
	}
	for _, e := range a.aiOrphans() {
		pid := e.Pid
		kind := ai.KindLLM
		if strings.HasPrefix(e.Label, "speech:") {
			kind = ai.KindVoice
		}
		installed = append(installed, aiRow{key: "orphan/" + strconv.Itoa(pid), kind: kind, name: a.aiLabelName(e.Label), state: ai.Ready, installed: true,
			detail: u.t("Left running by an earlier run") + "  ·  " + u.tf("process %d", pid), unload: func() { a.aiKillOrphan(pid) }})
	}
	running, _, _ := a.speech.server.Info()
	for _, m := range a.speech.config.Models {
		path, have := a.speech.manager.ModelPath(m.Model)
		if !have {
			continue
		}
		row := aiRow{key: "speech/" + m.ID, kind: ai.KindVoice, name: m.Name.In(lang), note: m.Note.In(lang), disk: fileSizeOf(path), installed: true}
		if _, pid, on := a.speech.server.Info(); on && running == m.ID && pid != 0 {
			row.state = ai.Ready
			row.usage, row.measured = a.ai.rt.Usage("speech")
			row.unload = a.speech.server.Stop
		}
		installed = append(installed, row)
	}
	for _, m := range a.subtitles.config.Models {
		path, have := a.subtitles.manager.ModelPath(m)
		if !have {
			continue
		}
		installed = append(installed, aiRow{key: "subtitles/" + m.ID, kind: ai.KindTranscribe, name: m.Name.In(lang), note: m.Note.In(lang),
			disk: fileSizeOf(path), installed: true, inUse: a.subtitles.busy() && a.settings.SubtitleModel == m.ID})
	}
	return installed, available
}

// settingsAI is the AI models page of Settings: every model and what it takes, and the keys of the online services.
func (a *App) settingsAI(r gfx.Rect) {
	u := &a.u
	s := &a.ai
	a.scrolledBy("settings/ai", r, &s.page[0], &s.page[1], func(view gfx.Rect) float32 {
		y := view.Y
		y = a.paragraph(view.X, y, view.W, u.t("The AI models of this app, wherever they are used. Models on this computer are loaded when needed and stay in memory until you unload them; their cost is measured while they run."), 12.5, colDim) + u.px(10)
		if s.keysErr != nil {
			y = a.paragraph(view.X, y, view.W, a.describe(s.keysErr), 12.5, colDangerText) + u.px(6)
		}
		y = a.settingsAIEngine(view, y)
		installed, available := a.aiRows()
		u.c.Text(u.bold(13), view.X, y, u.t("Installed models"), colText)
		y += u.px(26)
		y = a.drawAIRowsByKind(view, y, installed)
		if len(installed) == 0 {
			y = a.paragraph(view.X, y, view.W, u.t("No model has been downloaded yet. Pick one below, or in the chat."), 12.5, colFaint) + u.px(6)
		}
		if len(available) > 0 {
			y += u.px(14)
			u.c.Text(u.bold(13), view.X, y, u.t("More models to download"), colText)
			y += u.px(26)
			y = a.drawAIRowsByKind(view, y, available)
		}
		y += u.px(18)
		y = a.settingsMail(view, y)
		u.c.Text(u.bold(13), view.X, y, u.t("Online services"), colText)
		y += u.px(22)
		y = a.paragraph(view.X, y, view.W, u.t("Enter an API key to chat with a service's models. The key is kept on this computer, protected for your Windows account, and sent only to that service."), 12.5, colDim) + u.px(8)
		for _, p := range s.config.Providers {
			y = a.drawAIProvider(view, y, p)
		}
		y = a.paragraph(view.X, y+u.px(8), view.W, u.t("The list of models, services and engines can be changed in Settings → Extensions."), 12, colFaint)
		return y
	})
}

// drawAIRowsByKind draws the models under a heading for each kind of model that has any, in the order of ai.Kinds.
func (a *App) drawAIRowsByKind(view gfx.Rect, y float32, rows []aiRow) float32 {
	u := &a.u
	for _, kind := range ai.Kinds {
		first := true
		for _, row := range rows {
			if row.kind != kind {
				continue
			}
			if first {
				first = false
				u.c.Text(u.bold(12), view.X, y+u.px(2), a.aiKindName(kind), colDim)
				y += u.px(24)
			}
			y = a.drawAIRow(view, y, row)
		}
	}
	return y
}

// settingsAIEngine lets the user choose where chat models compute: the graphics card or the processor.
func (a *App) settingsAIEngine(view gfx.Rect, y float32) float32 {
	u := &a.u
	lang := string(u.lang)
	var probe ai.Model
	if len(a.ai.config.Models) > 0 {
		probe = a.ai.config.Models[0]
	}
	engines := a.ai.config.Available(probe)
	if len(engines) < 2 {
		return y
	}
	current, _ := a.aiEngine(probe)
	names, selected := make([]string, len(engines)), 0
	for i, e := range engines {
		names[i] = e.Name.In(lang)
		if e.ID == current.ID {
			selected = i
		}
	}
	u.c.Text(u.bold(13), view.X, y, u.t("Chat models run on"), colText)
	y += u.px(24)
	pick, h := u.flowChips("settings/ai/engine", rectAt(view.X, y, view.W, u.px(30)), names, selected)
	if pick != selected {
		a.settings.AIEngine = engines[pick].ID
		a.saveSettings()
		a.ai.rt.UnloadAll() // what is loaded runs on the engine it was started with
	}
	y += h + u.px(2)
	y = a.paragraph(view.X, y, view.W, current.Note.In(lang), 12, colFaint)
	return y + u.px(14)
}

// aiRowStateText says what a model is doing, and the colour to say it in.
func (a *App) aiRowStateText(row aiRow) (string, color.RGBA) {
	u := &a.u
	switch row.state {
	case ai.Ready:
		return u.t("Running"), colSuccess
	case ai.Downloading:
		return u.tf("%s… %d%%", a.aiStageName(row.stage), int(row.fraction*100)), colAccent
	case ai.Loading:
		return u.t("Loading…"), colAccent
	case ai.Failed:
		return u.t("Failed"), colDanger
	}
	switch {
	case row.inUse:
		return u.t("In use"), colAccent
	case row.installed:
		return u.t("Not running"), colFaint
	}
	return "", colFaint
}

// drawAIRow draws one model of the list from y and returns the y below it.
func (a *App) drawAIRow(view gfx.Rect, y float32, row aiRow) float32 {
	u := &a.u
	s := &a.ai
	h := u.px(66)
	box := rectAt(view.X, y, view.W, h)
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	left := box.X + u.px(12)
	state, stateCol := a.aiRowStateText(row)
	u.c.Circle(left+u.px(4), box.Y+u.px(19), u.px(4), stateCol)
	nameF := u.bold(13)
	u.c.Text(nameF, left+u.px(16), box.Y+u.px(10), row.name, colText)
	kf := u.face(11)
	if state != "" { // on the name's own line, centred on it
		u.c.Text(kf, left+u.px(16)+nameF.Measure(row.name)+u.px(10), box.Y+u.px(10)+(nameF.Height()-kf.Height())/2, state, stateCol)
	}

	// The second line: what it costs while it runs, or how large it is.
	f := u.face(12)
	var detail string
	switch {
	case row.detail != "":
		detail = row.detail
	case row.state == ai.Failed && row.err != nil:
		detail = a.describeAI(row.err)
	case row.state == ai.Ready:
		detail = u.tf("Disk %s", size(row.disk))
		if row.measured {
			detail += "  ·  " + u.tf("RAM %s", size(int64(row.usage.Memory))) + "  ·  " + u.tf("CPU %.0f%%", row.usage.CPU)
			if row.usage.GPUKnown {
				detail += "  ·  " + u.tf("GPU %.0f%%", row.usage.GPU) + "  ·  " + u.tf("VRAM %s", size(int64(row.usage.GPUMemory)))
			}
		} else {
			detail += "  ·  " + u.t("measuring…")
		}
	case row.installed:
		detail = u.tf("Disk %s", size(row.disk)) + "  ·  " + row.note
	default:
		detail = u.tf("%s to download", size(row.disk)) + "  ·  " + row.note
	}
	dc := colDim
	if row.state == ai.Failed {
		dc = colDangerText
	}
	buttonsW := u.px(240)
	u.c.Text(f, left+u.px(16), box.Y+u.px(36), f.Ellipsize(detail, box.W-buttonsW-u.px(30)), dc)

	// The buttons, from the right.
	bx := box.Right() - u.px(10)
	btn := func(id, glyph, text string, kind buttonKind, w float32) bool {
		bx -= w
		clicked := u.button(id, rectAt(bx, box.Y+(h-u.px(30))/2, w, u.px(30)), glyph, text, kind, true)
		bx -= u.px(6)
		return clicked
	}
	id := "settings/ai/" + row.key
	switch {
	case s.removing == row.key && row.remove != nil:
		if btn(id+"/keep", "", u.t("Keep"), plain, u.px(64)) {
			s.removing = ""
		}
		if btn(id+"/delete", "", u.t("Delete files"), danger, u.px(110)) {
			row.remove()
		}
	default:
		if row.remove != nil {
			if u.iconButton(id+"/remove", rectAt(bx-u.px(30), box.Y+(h-u.px(30))/2, u.px(30), u.px(30)), icDelete, u.t("Delete the downloaded files"), false, true) {
				s.removing = row.key
			}
			bx -= u.px(36)
		}
		switch {
		case row.unload != nil:
			label := u.t("Unload")
			if row.state == ai.Downloading || row.state == ai.Loading {
				label = u.t("Cancel")
			}
			if btn(id+"/unload", icPower, label, plain, u.px(104)) {
				row.unload()
			}
		case row.load != nil:
			label, glyph := u.t("Load"), icPlay
			if !row.installed {
				label, glyph = u.t("Download"), icDownload
			}
			if row.state == ai.Failed {
				label = u.t("Try again")
			}
			if btn(id+"/load", glyph, label, plain, u.px(110)) {
				row.load()
			}
		}
	}
	if row.state == ai.Downloading {
		bar := rectAt(box.X, box.Bottom()-u.px(2), box.W, u.px(2))
		u.c.Fill(rectAt(bar.X, bar.Y, bar.W*float32(row.fraction), bar.H), colAccent)
	}
	return y + h + u.px(6)
}

// drawAIProvider draws an online service and the entry of its API key.
func (a *App) drawAIProvider(view gfx.Rect, y float32, p ai.Provider) float32 {
	u := &a.u
	s := &a.ai
	lang := string(u.lang)
	_, saved := a.aiKey(p.ID)
	editing := s.keyFor == p.ID
	h := u.px(54)
	if editing {
		h += u.px(42)
	}
	box := rectAt(view.X, y, view.W, h)
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	top := rectAt(box.X, box.Y, box.W, u.px(54))
	dot := colFaint
	state := u.t("No API key")
	if saved {
		dot, state = colSuccess, u.t("API key saved")
	}
	u.c.Circle(top.X+u.px(16), top.Y+u.px(19), u.px(4), dot)
	nameF := u.bold(13)
	u.c.Text(nameF, top.X+u.px(28), top.Y+u.px(10), p.Name.In(lang), colText)
	names := make([]string, len(p.Models))
	for i, m := range p.Models {
		names[i] = m.Name.In(lang)
	}
	u.c.Text(u.face(12), top.X+u.px(28), top.Y+u.px(31), u.face(12).Ellipsize(state+"  ·  "+strings.Join(names, ", "), top.W-u.px(330)), colDim)

	id := "settings/ai/key/" + p.ID
	bx := box.Right() - u.px(10)
	btn := func(suffix, glyph, text string, kind buttonKind, w float32) bool {
		bx -= w
		clicked := u.button(id+suffix, rectAt(bx, top.Y+(top.H-u.px(30))/2, w, u.px(30)), glyph, text, kind, true)
		bx -= u.px(6)
		return clicked
	}
	if !editing {
		label := u.t("Enter key")
		if saved {
			label = u.t("Change key")
		}
		if btn("/enter", icKey, label, plain, u.px(118)) {
			s.keyFor, s.keyText = p.ID, ""
			u.beginEdit(id+"/box", "", false, 0, 0)
		}
		if saved && btn("/forget", "", u.t("Forget"), ghost, u.px(70)) {
			if err := s.keys.Set(p.ID, ""); err != nil {
				a.toast(a.u.tf("The key could not be removed: %v", err), true)
			}
		}
		if p.KeyURL != "" && btn("/get", icLaunch, u.t("Get a key"), ghost, u.px(104)) {
			a.host.LaunchURL(p.KeyURL)
		}
		return y + h + u.px(6)
	}
	entry := rectAt(box.X+u.px(12), top.Bottom(), box.W-u.px(24), u.px(34))
	save := false
	if u.focus == id+"/box" {
		for _, k := range u.keys {
			save = save || k.Code == keyEnter && strings.TrimSpace(s.keyText) != ""
		}
	}
	u.secretBox(id+"/box", rectAt(entry.X, entry.Y, entry.W-u.px(170), entry.H), &s.keyText, u.t("Paste the API key here"))
	if u.button(id+"/save", rectAt(entry.Right()-u.px(160), entry.Y, u.px(78), entry.H), "", u.t("Save"), primary, strings.TrimSpace(s.keyText) != "") {
		save = true
	}
	if u.button(id+"/cancel", rectAt(entry.Right()-u.px(76), entry.Y, u.px(76), entry.H), "", u.t("Cancel"), plain, true) {
		s.keyFor, s.keyText = "", ""
	}
	if save {
		if err := s.keys.Set(p.ID, s.keyText); err != nil {
			a.toast(a.u.tf("The key could not be saved: %v", err), true)
		} else {
			a.toast(a.u.tf("The key of %s is saved", p.Name.In(lang)), false)
		}
		s.keyFor, s.keyText = "", ""
	}
	return y + h + u.px(6)
}

func fileSizeOf(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}
