package ui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The scroll areas of the tab, by place in aiState.scroll.
const (
	aiScrollMessages = iota
	aiScrollChats
	aiScrollModels
)

// openAITab shows the chat tab, of which there is one at most.
func (a *App) openAITab() {
	for i, d := range a.docs {
		if d.aiTab {
			a.switchTab(i)
			return
		}
	}
	if a.occupied() {
		a.newTab()
	}
	a.aiTab = true
	a.ai.focusIn = true
	a.requestFrame()
}

// drawAITab is the chat tab: the conversations at the left, the chat beside them.
func (a *App) drawAITab(r gfx.Rect) {
	u := &a.u
	s := &a.ai
	u.c.Fill(r, colPanel)
	// The list of models is drawn last, over the rest, which takes no clicks meanwhile.
	held := u.blocked
	u.blocked = held || s.picking
	side := float32(0)
	if r.W >= u.px(640) {
		side = min(u.px(260), r.W*0.3)
		a.drawAISide(rectAt(r.X, r.Y, side, r.H))
	}
	main := rectAt(r.X+side, r.Y, r.W-side, r.H)
	col := min(main.W-u.px(48), u.px(860))
	x := main.X + (main.W-col)/2

	ch := a.aiChoice()
	head := rectAt(x, main.Y+u.px(12), col, u.px(36))
	u.blocked = held
	pill := a.drawAIHeader(head, ch)
	u.blocked = held || s.picking
	status := rectAt(x, head.Bottom()+u.px(6), col, u.px(22))
	a.drawAIStatus(status, ch)

	composerH := a.aiComposerHeight(col)
	composer := rectAt(x, main.Bottom()-composerH-u.px(16), col, composerH)
	if s.notice != "" {
		composer.Y -= u.px(2)
	}
	above := composer.Y
	if ch.Kind == ai.KindVoice {
		above -= s.voiceH + u.px(8)
		a.drawAIVoices(rectAt(x, above, col, s.voiceH))
	}
	others := a.aiRunningOthers(ch)
	runningH := a.aiRunningHeight(len(others))
	a.drawAIRunning(rectAt(x, status.Bottom()+u.px(6), col, runningH), others)
	top := status.Bottom() + u.px(8) + runningH
	body := rectAt(x-u.px(12), top, col+u.px(24), above-top-u.px(10))
	a.drawAIMessages(body, ch)
	a.drawAIComposer(composer, ch)
	u.blocked = held
	if s.picking {
		a.drawAIPicker(main, pill, ch)
	}
}

// drawAISide lists the conversations.
func (a *App) drawAISide(r gfx.Rect) {
	u := &a.u
	s := &a.ai
	u.c.Fill(r, colBackground)
	u.c.Fill(rectAt(r.Right()-1, r.Y, 1, r.H), colBorder)
	in := r.Inset(u.px(12))
	if u.button("ai/new", rectAt(in.X, in.Y, in.W, u.px(34)), icAdd, u.t("New chat"), plain, true) {
		a.aiNewChat()
	}
	list := rectAt(in.X, in.Y+u.px(46), in.W, in.H-u.px(46))
	chats := a.aiConversations()
	if len(chats) == 0 {
		a.paragraph(list.X+u.px(4), list.Y+u.px(6), list.W-u.px(8), u.t("Your conversations are kept here, on this computer."), 12, colFaint)
		return
	}
	a.scrolledBy("ai/chats", list, &s.scroll[aiScrollChats], &s.content[aiScrollChats], func(view gfx.Rect) float32 {
		y := view.Y
		for _, c := range chats {
			row := rectAt(view.X, y, view.W, u.px(34))
			id := "ai/chat/" + c.ID
			current := s.chat != nil && s.chat.ID == c.ID
			bg := color.RGBA{}
			if current {
				bg = colSelected
			} else if u.hover(id, row) {
				bg = colSurface
			}
			u.c.Fill(row, bg)
			if s.deleting == c.ID {
				if u.button(id+"/yes", rectAt(row.Right()-u.px(150), row.Y+u.px(3), u.px(72), row.H-u.px(6)), "", u.t("Delete"), danger, true) {
					a.aiDeleteChat(c.ID)
				}
				if u.button(id+"/no", rectAt(row.Right()-u.px(74), row.Y+u.px(3), u.px(70), row.H-u.px(6)), "", u.t("Keep"), plain, true) {
					s.deleting = ""
				}
				u.label(u.face(12.5), rectAt(row.X+u.px(10), row.Y, row.W-u.px(160), row.H), c.Title, colDim, 0)
			} else {
				hot := u.hover(id, row)
				w := row.W - u.px(20)
				if hot {
					w -= u.px(28)
				}
				u.label(u.face(12.5), rectAt(row.X+u.px(10), row.Y, w, row.H), c.Title, colText, 0)
				if u.clicked(id, rectAt(row.X, row.Y, row.W-u.px(30), row.H)) {
					a.aiOpenChat(c.ID)
				}
				if hot || current {
					if u.iconButton(id+"/del", rectAt(row.Right()-u.px(30), row.Y+u.px(3), u.px(28), row.H-u.px(6)), icDelete, u.t("Delete this conversation"), false, true) {
						s.deleting = c.ID
					}
				}
			}
			y += u.px(36)
		}
		return y
	})
}

// drawAIHeader draws the model chooser and the buttons that load and unload it; it returns the chooser's rectangle.
func (a *App) drawAIHeader(r gfx.Rect, ch ai.Choice) gfx.Rect {
	u := &a.u
	s := &a.ai
	lang := string(u.lang)
	glyph := icMemory
	if ch.Online() {
		glyph = icCloud
	}
	name := ch.Name.In(lang)
	if series := ch.Series.In(lang); ch.External() && series != "" {
		name = series + " • " + name
	}
	if ch.Online() {
		name = ch.Provider.Name.In(lang) + " · " + name
	}
	f := u.bold(13.5)
	w := min(f.Measure(name)+u.px(70), r.W*0.62)
	pill := rectAt(r.X, r.Y, w, r.H)
	hot := u.hover("ai/pick", pill)
	bg, edge := colSurface, colBorder
	if hot || s.picking {
		bg, edge = colSurfaceHi, colBorderHi
	}
	u.c.Fill(pill, bg)
	u.c.Outline(pill, max(1, u.px(1)), edge)
	u.icon(glyph, rectAt(pill.X+u.px(6), pill.Y, u.px(28), pill.H), 16, colDim)
	u.label(f, rectAt(pill.X+u.px(36), pill.Y, pill.W-u.px(70), pill.H), name, colText, 0)
	u.icon(icMoreV, rectAt(pill.Right()-u.px(30), pill.Y, u.px(24), pill.H), 18, colDim)
	if hot {
		u.cursor = CursorHand
	}
	if u.clicked("ai/pick", pill) {
		s.picking = !s.picking
		s.pickKind, s.scroll[aiScrollModels] = "", 0
	}

	// What can be done with the model itself.
	if ch.Online() {
		if _, ok := a.aiKey(ch.Provider.ID); !ok {
			if u.button("ai/keys", rectAt(r.Right()-u.px(150), r.Y, u.px(150), r.H), icKey, u.t("Enter API key"), primary, true) {
				a.openSettingsPage(settingsAIPage)
			}
		} else if u.button("ai/keys", rectAt(r.Right()-u.px(110), r.Y, u.px(110), r.H), icKey, u.t("API key"), ghost, true) {
			a.openSettingsPage(settingsAIPage)
		}
		return pill
	}
	if ch.External() {
		return pill // the voice-over and subtitle models load when they are used, and are unloaded in Settings
	}
	st := a.ai.rt.Status(ch.ID)
	switch st.State {
	case ai.Ready:
		// The model is running, and says so plainly, with the way to stop it; unloading is for the list in Settings.
		stopText := u.t("Stop the model")
		sw := u.face(13).Measure(stopText) + u.px(48)
		stop := rectAt(r.Right()-sw, r.Y, sw, r.H)
		if u.button("ai/stop-model", stop, icStop, stopText, plain, true) {
			a.ai.rt.Unload(ch.ID)
		}
		f := u.bold(12.5)
		label := u.t("Running")
		lx := stop.X - u.px(14) - f.Measure(label)
		u.c.Circle(lx-u.px(12), r.Y+r.H/2, u.px(4.5), colSuccess)
		u.c.Text(f, lx, r.Y+(r.H-f.Height())/2, label, colSuccess)
	case ai.Downloading, ai.Loading:
		if u.button("ai/cancel", rectAt(r.Right()-u.px(110), r.Y, u.px(110), r.H), icClose, u.t("Cancel"), plain, true) {
			a.ai.rt.Unload(ch.ID)
			if s.answer != nil {
				s.answer.stop()
			}
		}
	default:
		label := u.t("Load")
		if st.State == ai.Failed {
			label = u.t("Try again")
		}
		if u.button("ai/load", rectAt(r.Right()-u.px(120), r.Y, u.px(120), r.H), icPlay, label, primary, true) {
			a.aiLoad(*ch.Local)
		}
	}
	return pill
}

// aiStageName says what is being fetched while a model is Downloading.
func (a *App) aiStageName(stage string) string {
	u := &a.u
	switch stage {
	case "engine":
		return u.t("Downloading the engine")
	case "projector":
		return u.t("Downloading the picture reader")
	}
	return u.t("Downloading the model")
}

// aiUsageText is what a loaded model takes of the computer, as a line.
func (a *App) aiUsageText(m ai.Model, st ai.Status) string {
	u := &a.u
	disk, _ := a.ai.rt.Files(m)
	parts := []string{u.tf("Disk %s", size(disk))}
	if !st.Measured {
		return strings.Join(append(parts, u.t("measuring…")), "  ·  ")
	}
	parts = append(parts, u.tf("RAM %s", size(int64(st.Usage.Memory))), u.tf("CPU %.0f%%", st.Usage.CPU))
	if st.Usage.GPUKnown {
		parts = append(parts, u.tf("GPU %.0f%%", st.Usage.GPU), u.tf("VRAM %s", size(int64(st.Usage.GPUMemory))))
	}
	return strings.Join(parts, "  ·  ")
}

// drawAIStatus is the line under the header: what the model is doing and what it takes of the computer.
func (a *App) drawAIStatus(r gfx.Rect, ch ai.Choice) {
	u := &a.u
	f := u.face(12)
	text, col := "", colDim
	dot := colFaint
	var fraction float64 = -1
	switch {
	case ch.Online():
		if _, ok := a.aiKey(ch.Provider.ID); ok {
			text, dot = u.t("Online service · your API key is saved"), colSuccess
		} else {
			text = u.t("Online service · enter your API key to use it")
		}
	case ch.External():
		model, have := a.aiExtModel(ch)
		if have {
			text = u.t("Runs on this computer when you send  ·  the model is downloaded")
		} else {
			text = u.tf("Runs on this computer when you send  ·  %s to download the first time", size(model.Size))
		}
	default:
		st := a.ai.rt.Status(ch.ID)
		engine, _ := a.aiEngine(*ch.Local)
		where := engine.Name.In(string(u.lang))
		switch st.State {
		case ai.Ready:
			dot = colSuccess
			text = u.t("Running") + "  ·  " + a.aiUsageText(*ch.Local, st) + "  ·  " + where
		case ai.Downloading:
			dot, fraction = colAccent, st.Fraction
			text = u.tf("%s… %d%%", a.aiStageName(st.Stage), int(st.Fraction*100))
		case ai.Loading:
			dot, fraction = colAccent, -2
			text = u.t("Loading the model into memory…")
		case ai.Failed:
			dot, col = colDanger, colDangerText
			text = a.describeAI(st.Err)
		default:
			disk, installed := a.ai.rt.Files(*ch.Local)
			if installed {
				text = u.tf("Not loaded  ·  %s on disk  ·  loads when you send a message", size(disk))
			} else {
				text = u.tf("Not downloaded  ·  %s to download the first time", size(ch.Local.Size+extraSize(*ch.Local)))
			}
		}
	}
	u.c.Circle(r.X+u.px(5), r.Y+r.H/2, u.px(4), dot)
	u.c.Text(f, r.X+u.px(16), r.Y+(r.H-f.Height())/2, f.Ellipsize(text, r.W-u.px(16)), col)
	if fraction != -1 {
		bar := rectAt(r.X, r.Bottom()-u.px(2), r.W, u.px(2))
		u.c.Fill(bar, colSurface)
		if fraction >= 0 {
			u.c.Fill(rectAt(bar.X, bar.Y, bar.W*float32(fraction), bar.H), colAccent)
		} else {
			// Loading has no known length: a short bar travels along.
			t := float32(time.Now().UnixMilli()%1600) / 1600
			u.c.Fill(rectAt(bar.X+(bar.W-bar.W*0.25)*t, bar.Y, bar.W*0.25, bar.H), colAccent)
		}
	}
}

func extraSize(m ai.Model) int64 {
	var n int64
	if m.Projector != nil {
		n += m.Projector.Size
	}
	for _, p := range m.Parts {
		n += p.Size
	}
	return n
}

// aiComposerHeight is the height of the input and what is under it, which grows with the lines typed.
func (a *App) aiComposerHeight(width float32) float32 {
	u := &a.u
	lines := min(strings.Count(a.ai.input, "\n")+1, 7)
	if lines == 1 {
		return u.px(aiComposerMin) + a.aiChipsHeight() // one line: the input is exactly as tall as its buttons, so their edges meet
	}
	h := u.px(18) + float32(lines)*u.face(14).Height()*1.2 + u.px(10)
	if a.ai.notice != "" {
		h += u.px(2)
	}
	return max(h, u.px(aiComposerMin)) + a.aiChipsHeight()
}

// aiComposerMin is the height of the input with one line, and the side of the buttons beside it.
const aiComposerMin = 48

// aiChipSide is the side of the small picture of an attachment above the input.
const aiChipSide = 56

// aiChipsHeight is the height of the row of attachments above the input: none when there are none.
func (a *App) aiChipsHeight() float32 {
	if len(a.ai.attached) == 0 {
		return 0
	}
	return a.u.px(aiChipSide + 10)
}

// aiComposerRects places the parts of the composer in r: the attachments on top, and under them in a row the button that
// attaches, the input and the button that sends. The buttons are squares at the input's lower corners, as tall as a
// one-line input, so that the three share their bottom edge, and their top edge too until the input grows.
func (a *App) aiComposerRects(r gfx.Rect) (chips, attach, box, btn gfx.Rect) {
	u := &a.u
	side, gap := u.px(aiComposerMin), u.px(10)
	chips = rectAt(r.X, r.Y, r.W, a.aiChipsHeight())
	inner := rectAt(r.X, chips.Bottom(), r.W, r.H-chips.H)
	attach = rectAt(inner.X, inner.Bottom()-side, side, side)
	btn = rectAt(inner.Right()-side, inner.Bottom()-side, side, side)
	box = rectAt(attach.Right()+gap, inner.Y, btn.X-gap-attach.Right()-gap, inner.H)
	return chips, attach, box, btn
}

// drawAIChips draws the pictures attached to the message, each with a button that takes it off.
func (a *App) drawAIChips(r gfx.Rect) {
	u := &a.u
	s := &a.ai
	side := u.px(aiChipSide)
	for i, path := range s.attached {
		chip := rectAt(r.X+float32(i)*(side+u.px(8)), r.Y, side, side)
		u.c.RoundRect(chip, u.px(6), colSurface)
		if kind := aiFileKind(path); kind != ai.KindImage {
			// A sound or a video: its symbol, and the start of its name.
			u.icon(aiMediaGlyph[kind], rectAt(chip.X, chip.Y+u.px(4), side, u.px(28)), 22, colDim)
			f := u.face(10.5)
			name := f.Ellipsize(filepath.Base(path), side-u.px(8))
			u.c.Text(f, chip.X+(side-f.Measure(name))/2, chip.Bottom()-f.Height()-u.px(5), name, colFaint)
		} else if pic := a.aiPicture(path); pic.img != nil {
			b := pic.img.Bounds()
			scale := min(side/float32(b.Dx()), side/float32(b.Dy()))
			w, h := float32(b.Dx())*scale, float32(b.Dy())*scale
			u.c.Image(pic.img, rectAt(chip.X+(side-w)/2, chip.Y+(side-h)/2, w, h))
		}
		u.c.RoundOutline(chip, u.px(6), max(1, u.px(1)), colBorder)
		x := rectAt(chip.Right()-u.px(20), chip.Y+u.px(2), u.px(18), u.px(18))
		u.c.Circle(x.X+x.W/2, x.Y+x.H/2, u.px(9), colBackground)
		if u.iconButton(fmt.Sprintf("ai/unattach/%d", i), x, icClose, u.t("Remove the picture"), false, true) {
			s.attached = slices.Delete(s.attached, i, i+1)
			return
		}
	}
}

// drawAIComposer is the input and its send button.
func (a *App) drawAIComposer(r gfx.Rect, ch ai.Choice) {
	u := &a.u
	s := &a.ai
	if s.focusIn {
		u.beginEdit("ai/input", s.input, true, len([]rune(s.input)), len([]rune(s.input)))
		s.focusIn = false
	}
	chips, attach, box, btn := a.aiComposerRects(r)
	a.drawAIChips(chips)
	// Enter sends; Shift+Enter makes a new line; pasted pictures are attached.
	send := false
	if u.focus == "ai/input" {
		for i := 0; i < len(u.keys); i++ {
			k := u.keys[i]
			if k.Code == keyEnter && !k.Shift || k.Ctrl && k.Code == 'V' && a.aiPaste() {
				u.keys = slices.Delete(u.keys, i, i+1)
				i--
				send = send || k.Code == keyEnter
			}
		}
	}
	hint := u.t("Attach pictures")
	switch {
	case ch.Kind == ai.KindTranscribe:
		hint = u.t("Attach a sound or video file")
	case ch.StartsFromPicture():
		hint = u.t("Attach a picture to work from")
	}
	if !aiTakesFiles(ch) {
		hint = u.t("This model takes no pictures or files")
	}
	canAttach := aiTakesFiles(ch) && !s.picking && s.answer == nil
	picked := u.button("ai/attach", attach, icAttach, "", plain, canAttach)
	u.hint("ai/attach", hint, attach)
	if picked {
		kinds, exts := a.aiPickExts(ch)
		a.pickFiles(pickRequest{key: "ai-attach", title: hint, kinds: kinds, exts: exts, multiple: true}, a.aiAttach)
	}
	prompt := u.t("Message the model…   Enter to send, Shift+Enter for a new line")
	switch ch.Kind {
	case ai.KindImage:
		prompt = u.t("Describe the picture…   Enter to send, Shift+Enter for a new line")
	case ai.KindVideo:
		prompt = u.t("Describe the video…   Enter to send, Shift+Enter for a new line")
	case ai.KindModel3D:
		prompt = u.t("Attach a picture, then send")
	case ai.KindVoice:
		prompt = u.t("Type what should be said…   Enter to send, Shift+Enter for a new line")
	case ai.KindTranscribe:
		prompt = u.t("Attach a sound or video file, then send")
	}
	u.textBox("ai/input", box, &s.input, prompt, true)
	if s.answer != nil {
		if u.button("ai/stop", btn, icStop, "", primary, true) {
			a.aiStop()
		}
		u.hint("ai/stop", u.t("Stop"), btn)
	} else {
		ready := strings.TrimSpace(s.input) != "" || len(s.attached) > 0
		if u.button("ai/send", btn, icSend, "", primary, ready) {
			send = true
		}
		u.hint("ai/send", u.t("Send (Enter)"), btn)
	}
	if send && !s.picking {
		a.aiSend()
	}
}

// drawAIMessages draws the conversation, scrolled to the newest unless the user scrolled up.
func (a *App) drawAIMessages(r gfx.Rect, ch ai.Choice) {
	u := &a.u
	s := &a.ai
	empty := (s.chat == nil || len(s.chat.Messages) == 0) && s.answer == nil
	if empty {
		a.drawAIWelcome(r, ch)
		return
	}
	if u.wheel > 0 && u.hover("ai/messages", r) {
		s.follow = false
	}
	if s.follow {
		s.scroll[aiScrollMessages] = 1e9
	}
	a.scrolledBy("ai/messages", r, &s.scroll[aiScrollMessages], &s.content[aiScrollMessages], func(view gfx.Rect) float32 {
		col := rectAt(view.X+u.px(12), view.Y+u.px(4), view.W-u.px(24), 0)
		y := col.Y
		var messages []ai.Message
		if s.chat != nil {
			messages = s.chat.Messages
		}
		for i, m := range messages {
			y = a.drawAIMessage(col, y, i, m)
		}
		if ans := s.answer; ans != nil {
			y = a.drawAIAnswer(col, y, len(messages), ans)
		}
		if s.notice != "" {
			y = a.drawAINotice(col, y)
		}
		return y
	})
	if end := s.content[aiScrollMessages] - r.H; s.scroll[aiScrollMessages] >= end-u.px(2) {
		s.follow = true
	}
}

// drawAIWelcome fills an empty conversation.
func (a *App) drawAIWelcome(r gfx.Rect, ch ai.Choice) {
	u := &a.u
	cx := r.X + r.W/2
	y := r.Y + max(r.H*0.28, u.px(20))
	u.icon(icAI, rectAt(cx-u.px(28), y, u.px(56), u.px(56)), 44, colFaint)
	y += u.px(70)
	title := u.t("Chat with a model that runs on this computer")
	if ch.Online() {
		title = u.t("Chat with an online model")
	}
	hint := u.t("Pick a model above. A local model is downloaded the first time, and nothing you write leaves this computer.")
	if ch.Online() {
		hint = u.t("Your messages go to the service you picked, with your API key.")
	}
	switch ch.Kind {
	case ai.KindImage:
		title = u.t("Draw with an image model")
		hint = u.t("Describe the picture you want, or attach one to change it. What it draws is kept in the ai-image folder of the Intelligence folder.")
	case ai.KindVideo:
		title = u.t("Make a video")
		hint = u.t("Describe the video, and attach a picture to start it from. It takes minutes; it is kept in the ai-video folder of the Intelligence folder.")
	case ai.KindModel3D:
		title = u.t("Make a 3D model from a picture")
		hint = u.t("Attach a picture of one thing on a plain background. It takes minutes; the model is kept in the ai-3dmodel folder of the Intelligence folder.")
	case ai.KindVoice:
		title = u.t("Turn words into speech")
		hint = u.t("Type what should be said, and pick a voice below. The recording is kept in the ai-voice folder of the Intelligence folder.")
	case ai.KindTranscribe:
		title = u.t("Write down what is said")
		hint = u.t("Attach a sound or video file with the paperclip, or drop it here. The text appears here, and nothing leaves this computer.")
	}
	f := u.bold(18)
	u.c.Text(f, cx-f.Measure(title)/2, y, title, colText)
	y += f.Height() + u.px(10)
	lines := u.face(13).Wrap(hint, min(r.W-u.px(40), u.px(520)))
	for _, l := range lines {
		u.c.Text(u.face(13), cx-u.face(13).Measure(l)/2, y, l, colDim)
		y += u.face(13).Height() * 1.4
	}
}

// drawAINotice shows what could not be done, with a button to try again.
func (a *App) drawAINotice(col gfx.Rect, y float32) float32 {
	u := &a.u
	s := &a.ai
	f := u.face(13)
	lines := f.Wrap(s.notice, col.W-u.px(100))
	h := max(float32(len(lines))*f.Height()*1.35+u.px(16), u.px(40))
	box := rectAt(col.X, y+u.px(6), col.W, h)
	u.c.Fill(box, gfx.Mix(colSurface, colDanger, 0.14))
	u.c.Outline(box, max(1, u.px(1)), gfx.Mix(colBorder, colDanger, 0.4))
	ty := box.Y + u.px(8)
	for _, l := range lines {
		u.c.Text(f, box.X+u.px(12), ty, l, colDangerText)
		ty += f.Height() * 1.35
	}
	last := s.chat != nil && len(s.chat.Messages) > 0 && s.chat.Messages[len(s.chat.Messages)-1].Role == ai.User
	if last && s.answer == nil {
		if u.button("ai/retry", rectAt(box.Right()-u.px(88), box.Y+(box.H-u.px(28))/2, u.px(80), u.px(28)), icRestore, u.t("Retry"), plain, true) {
			s.notice, s.follow = "", true
			a.aiAsk(a.aiChoice())
		}
	}
	return box.Bottom() + u.px(8)
}

// drawAIMessage draws one message of the conversation from y and returns the y below it.
func (a *App) drawAIMessage(col gfx.Rect, y float32, i int, m ai.Message) float32 {
	u := &a.u
	if m.Role == ai.User {
		return a.drawAIUser(col, y, i, m.Text, m.Attached) + u.px(14)
	}
	if m.LeftOut > 0 {
		f := u.face(11.5)
		for _, l := range f.Wrap(u.tf("Written without the %d earliest messages, which the model has no room for", m.LeftOut), col.W-u.px(40)) {
			u.c.Text(f, col.X+u.px(40), y, l, colFaint)
			y += f.Height() * 1.3
		}
		y += u.px(4)
	}
	return a.drawAIReply(col, y, i, m.Text, m.Reasoning, m.Media, false, m.Stats) + u.px(14)
}

// aiThumbSide is the longest side of a picture the user attached, as the conversation shows it.
const aiThumbSide = 150

// drawAIUser draws the user's pictures, in a row at the right, and under them their words in a bubble.
func (a *App) drawAIUser(col gfx.Rect, y float32, i int, text string, attached []ai.Media) float32 {
	u := &a.u
	if len(attached) > 0 {
		x := col.Right()
		rowH := float32(0)
		for j := len(attached) - 1; j >= 0; j-- {
			w, h := u.px(aiThumbSide), u.px(aiThumbSide)*0.66
			if attached[j].Kind != ai.KindImage {
				// A sound or a video: a card with its name.
				w, h = u.px(aiThumbSide), u.px(48)
				if x-w < col.X {
					break
				}
				r := rectAt(x-w, y, w, h)
				x = r.X - u.px(8)
				rowH = max(rowH, h)
				u.c.RoundRect(r, u.px(8), colSurface)
				u.c.RoundOutline(r, u.px(8), max(1, u.px(1)), colBorder)
				u.icon(aiMediaGlyph[attached[j].Kind], rectAt(r.X+u.px(4), r.Y, u.px(32), r.H), 20, colDim)
				u.label(u.face(11.5), rectAt(r.X+u.px(38), r.Y, r.W-u.px(44), r.H), filepath.Base(attached[j].Path), colText, 0)
				if u.clicked(fmt.Sprintf("ai/thumb/%d/%d", i, j), r) {
					a.host.Reveal(attached[j].Path)
				}
				continue
			}
			pic := a.aiPicture(attached[j].Path)
			if pic.img != nil {
				b := pic.img.Bounds()
				scale := min(u.px(aiThumbSide)/float32(max(b.Dx(), b.Dy())), 1)
				w, h = float32(b.Dx())*scale, float32(b.Dy())*scale
			}
			if x-w < col.X {
				break
			}
			r := rectAt(x-w, y, w, h)
			x = r.X - u.px(8)
			rowH = max(rowH, h)
			if pic.img != nil {
				u.c.Image(pic.img, r)
			} else {
				u.c.RoundRect(r, u.px(6), colSurface)
				if pic.err != nil {
					u.icon(icImage, r, 22, colDangerText)
				}
			}
			u.c.Outline(r, max(1, u.px(1)), colBorder)
			if u.clicked(fmt.Sprintf("ai/thumb/%d/%d", i, j), r) {
				a.host.Reveal(attached[j].Path)
			}
		}
		y += rowH
		if text == "" {
			return y
		}
		y += u.px(8)
	}
	f := u.face(aiTextSize)
	maxW := col.W * 0.78
	pad := u.px(12)
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		lines = append(lines, f.Wrap(para, maxW-2*pad)...)
	}
	w := float32(0)
	for _, l := range lines {
		w = max(w, f.Measure(l))
	}
	h := float32(len(lines))*f.Height()*1.45 + 2*u.px(9)
	bubble := rectAt(col.Right()-w-2*pad, y, w+2*pad, h)
	u.c.RoundRect(bubble, u.px(10), colSelected)
	ty := bubble.Y + u.px(9)
	for _, l := range lines {
		u.c.Text(f, bubble.X+pad, ty, l, colText)
		ty += f.Height() * 1.45
	}
	return bubble.Bottom()
}

// drawAIReply draws a model's words from y: its thinking, which opens and closes, then the answer with its
// Markdown laid out, then what it cost. live marks the answer being written.
func (a *App) drawAIReply(col gfx.Rect, y float32, i int, text, thinking string, media []ai.Media, live bool, stats *ai.Stats) float32 {
	u := &a.u
	s := &a.ai
	icon := rectAt(col.X, y+u.px(2), u.px(28), u.px(28))
	u.c.Circle(icon.X+icon.W/2, icon.Y+icon.H/2, u.px(13), colSurface)
	u.icon(icAI, icon, 16, colDim)
	x := col.X + u.px(40)
	w := col.W - u.px(40)
	if thinking != "" {
		id := fmt.Sprintf("ai/think/%d", i)
		open, set := s.opened[i]
		if !set {
			open = live && text == "" // shown while it is all there is, then folded away
		}
		head := rectAt(x, y, min(w, u.px(220)), u.px(24))
		glyph := icMoreV
		if open {
			glyph = icLessV
		}
		label := u.t("Thinking")
		if live && text == "" {
			label = u.t("Thinking…")
		}
		hot := u.hover(id, head)
		if hot {
			u.cursor = CursorHand
		}
		fg := colFaint
		if hot {
			fg = colDim
		}
		u.icon(icThink, rectAt(head.X, head.Y, u.px(22), head.H), 15, fg)
		u.label(u.face(12.5), rectAt(head.X+u.px(24), head.Y, head.W-u.px(48), head.H), label, fg, 0)
		u.icon(glyph, rectAt(head.Right()-u.px(24), head.Y, u.px(22), head.H), 16, fg)
		if u.clicked(id, head) {
			s.opened[i] = !open
		}
		y += head.H + u.px(2)
		if open {
			f := u.face(12.5)
			var lines []string
			for _, para := range strings.Split(strings.TrimSpace(thinking), "\n") {
				lines = append(lines, f.Wrap(para, w-u.px(20))...)
			}
			top := y
			for _, l := range lines {
				u.c.Text(f, x+u.px(14), y, l, colFaint)
				y += f.Height() * 1.4
			}
			u.c.Fill(rectAt(x+u.px(2), top, u.px(2), y-top), colBorderHi)
			y += u.px(8)
		}
	}
	if text != "" {
		laid := a.laidText(text, w)
		a.drawLaid(laid, x, y, w)
		y += laid.height
	}
	if len(media) > 0 {
		y = a.drawAIMedia(x, y+u.px(2), w, i, media)
	}
	if !live && text != "" {
		y += u.px(4)
		cap := ""
		if stats != nil && stats.CompletionTokens > 0 {
			cap = u.tf("%d tokens · %.1f tokens/s · %.1f s", stats.CompletionTokens, stats.TokensPerSecond, stats.Elapsed.Seconds())
		}
		if u.iconButton(fmt.Sprintf("ai/copy/%d", i), rectAt(x-u.px(4), y, u.px(26), u.px(24)), icCopy, u.t("Copy the answer"), false, true) {
			a.copyText(text)
		}
		if cap != "" {
			u.c.Text(u.face(11), x+u.px(28), y+(u.px(24)-u.face(11).Height())/2, cap, colFaint)
		}
		y += u.px(24)
	}
	return y
}

// drawAIAnswer draws the answer being written.
func (a *App) drawAIAnswer(col gfx.Rect, y float32, i int, ans *aiAnswer) float32 {
	u := &a.u
	ans.mu.Lock()
	text, thinking, waiting, stage, fraction := ans.text, ans.thinking, ans.waiting, ans.stage, ans.fraction
	ans.mu.Unlock()
	if text == "" && thinking == "" {
		label := u.t("Thinking…")
		switch ans.kind {
		case ai.KindImage:
			label = u.t("Drawing…")
		case ai.KindVideo, ai.KindModel3D:
			label = u.t("Making the video…")
			if ans.kind == ai.KindModel3D {
				label = u.t("Making the 3D model…")
			}
			if fraction >= 0 {
				label = u.tf("%s %d%%", strings.TrimSuffix(label, "…"), int(fraction*100))
			}
		case ai.KindVoice, ai.KindTranscribe:
			ch, _ := a.aiFind(ans.model)
			label = a.aiExtStage(ch, stage)
			if stage == "engine" || stage == "model" || stage == "transcribe" || stage == "speak" {
				label = u.tf("%s… %d%%", label, int(fraction*100))
			}
		}
		if waiting {
			st := a.ai.rt.Status(ans.model)
			label = u.t("Loading the model…")
			if st.State == ai.Downloading {
				label = u.tf("%s… %d%%", a.aiStageName(st.Stage), int(st.Fraction*100))
			}
		}
		icon := rectAt(col.X, y+u.px(2), u.px(28), u.px(28))
		u.c.Circle(icon.X+icon.W/2, icon.Y+icon.H/2, u.px(13), colSurface)
		u.icon(icAI, icon, 16, colDim)
		dots := strings.Repeat(".", 1+int(time.Now().UnixMilli()/400%3))
		u.c.Text(u.face(13), col.X+u.px(40), y+u.px(8), strings.TrimSuffix(label, "…")+dots, colDim)
		return y + u.px(40)
	}
	return a.drawAIReply(col, y, i, text, thinking, nil, true, nil) + u.px(14)
}

// drawLaid draws laid out text with its top left at (x, y).
func (a *App) drawLaid(l *aiLaid, x, y, w float32) {
	u := &a.u
	mono := u.mono(aiCodeSize)
	for _, it := range l.items {
		switch it.kind {
		case itemMarker:
			u.c.Text(u.face(aiTextSize), x+it.x, y+it.y, it.text, colDim)
		case itemText:
			px := x + it.x
			for _, sp := range it.spans {
				f := a.richFace(sp.style)
				if it.size != 0 && it.size != aiTextSize {
					f = u.bold(it.size)
				}
				dy := (u.face(aiTextSize).Height() - f.Height()) / 2
				if sp.style == richCode {
					tw := f.Measure(sp.text)
					u.c.RoundRect(rectAt(px-u.px(2), y+it.y+dy-u.px(1), tw+u.px(4), f.Height()+u.px(2)), u.px(3), colSurfaceHi)
					px = u.c.Text(f, px, y+it.y+dy, sp.text, colText) + u.px(2)
					continue
				}
				col := colText
				if sp.style == richBold {
					col = colPrimaryHi
				}
				px = u.c.Text(f, px, y+it.y+dy, sp.text, col)
			}
		case itemCodeBox:
			box := rectAt(x+it.x, y+it.y, it.w, it.h)
			u.c.RoundRect(box, u.px(6), colBackground)
			u.c.RoundOutline(box, u.px(6), max(1, u.px(1)), colBorder)
		case itemCodeHead:
			head := rectAt(x+it.x, y+it.y, it.w, it.h)
			if len(it.spans) > 0 && it.spans[0].text != "" {
				u.c.Text(u.face(11.5), head.X+u.px(10), head.Y+(head.H-u.face(11.5).Height())/2, it.spans[0].text, colFaint)
			}
			id := fmt.Sprintf("ai/code/%p/%d", l, int(it.y))
			if u.iconButton(id, rectAt(head.Right()-u.px(30), head.Y+u.px(1), u.px(26), head.H-u.px(2)), icCopy, u.t("Copy the code"), false, true) {
				a.copyText(it.text)
			}
		case itemCodeLine:
			u.c.Text(mono, x+it.x, y+it.y, it.text, colText)
		}
	}
}

// aiChoiceState says what a model needs before it can be used, and in what colour.
func (a *App) aiChoiceState(ch ai.Choice) (string, color.RGBA) {
	u := &a.u
	if ch.Online() {
		if _, ok := a.aiKey(ch.Provider.ID); ok {
			return u.t("API key saved"), colSuccess
		}
		return u.t("API key needed"), colFaint
	}
	if ch.External() {
		model, have := a.aiExtModel(ch)
		if have {
			return u.t("Downloaded"), colDim
		}
		return u.tf("%s to download", size(model.Size)), colFaint
	}
	st := a.ai.rt.Status(ch.ID)
	disk, installed := a.ai.rt.Files(*ch.Local)
	switch {
	case st.State == ai.Ready:
		return u.t("Running"), colSuccess
	case st.State == ai.Downloading || st.State == ai.Loading:
		return u.t("Loading…"), colAccent
	case installed:
		return u.tf("Downloaded · %s", size(disk)), colDim
	}
	return u.tf("%s to download", size(ch.Local.Size+extraSize(*ch.Local))), colFaint
}

// aiKindBar lays the kinds of model out in r, side by side when the longest name fits and in two rows of three when it
// does not, and returns where each goes, in the order of ai.ChatKinds, and the y below them.
func (a *App) aiKindBar(r gfx.Rect) ([]gfx.Rect, float32) {
	u := &a.u
	f := u.face(12)
	widest := float32(0)
	for _, k := range ai.ChatKinds {
		widest = max(widest, f.Measure(a.aiKindName(k)))
	}
	gap, h := u.px(4), u.px(30)
	cols := len(ai.ChatKinds)
	if (r.W-gap*float32(cols-1))/float32(cols) < widest+u.px(12) {
		cols = 3
	}
	w := (r.W - gap*float32(cols-1)) / float32(cols)
	tabs := make([]gfx.Rect, len(ai.ChatKinds))
	for i := range tabs {
		c, row := i%cols, i/cols
		left := r.X + (w+gap)*float32(c)
		right := left + w
		if c == cols-1 {
			right = r.Right()
		}
		tabs[i] = rectAt(left, r.Y+(h+gap)*float32(row), right-left, h)
	}
	return tabs, tabs[len(tabs)-1].Bottom()
}

// drawAIKindBar draws the kinds of model and takes clicks on them.
func (a *App) drawAIKindBar(tabs []gfx.Rect, kind ai.Kind) {
	u := &a.u
	s := &a.ai
	f := u.face(12)
	for i, k := range ai.ChatKinds {
		tab := tabs[i]
		id := "ai/kind/" + string(k)
		hot := u.hover(id, tab)
		switch {
		case k == kind:
			u.c.Fill(tab, colSelected)
		case hot:
			u.c.Fill(tab, colSurfaceHi)
		}
		if hot {
			u.cursor = CursorHand
		}
		name, col := a.aiKindName(k), colDim
		if k == kind {
			col = colText
		}
		name = f.Ellipsize(name, tab.W-u.px(8))
		u.c.Text(f, tab.X+(tab.W-f.Measure(name))/2, tab.Y+(tab.H-f.Height())/2, name, col)
		if u.clicked(id, tab) {
			s.pickKind, s.scroll[aiScrollModels] = k, 0
		}
	}
}

// drawAINoModels says that a kind of model has none yet and where to add one, centered on r, the width of the list
// with room for a scroll bar it does not need, and returns the y below it.
func (a *App) drawAINoModels(r gfx.Rect, y float32) float32 {
	u := &a.u
	center := func(f *gfx.Face, text string, col color.RGBA) {
		text = f.Ellipsize(text, r.W-u.px(16))
		u.c.Text(f, r.X+(r.W-f.Measure(text))/2, y, text, col)
		y += f.Height() + u.px(8)
	}
	y += u.px(48)
	center(u.bold(14), u.t("No models of this kind yet"), colText)
	center(u.face(12), u.t("Add one to ai_models in Settings → Extensions"), colDim)
	y += u.px(8)
	bw := u.px(170)
	if u.button("ai/extensions", rectAt(r.X+(r.W-bw)/2, y, bw, u.px(32)), "", u.t("Open Extensions"), plain, true) {
		a.ai.picking = false
		a.openSettingsPage(settingsExtensionsPage)
	}
	return y + u.px(40)
}

// drawAIPicker is the list of models under the chooser: a bar with the kinds of model, always all of them, and under
// it the models of the kind chosen there, those of this computer and then each online service's. It opens on the kind
// of the model in use.
func (a *App) drawAIPicker(main, pill gfx.Rect, current ai.Choice) {
	u := &a.u
	s := &a.ai
	lang := string(u.lang)
	w := min(u.px(520), main.W-u.px(24))
	h := min(u.px(560), main.H-pill.Bottom()-u.px(24))
	r := rectAt(max(pill.X, main.X+u.px(12)), pill.Bottom()+u.px(6), w, h)
	if r.Right() > main.Right()-u.px(12) {
		r.X = main.Right() - u.px(12) - w
	}
	u.c.Fill(r, colSurface)
	u.c.Outline(r, max(1, u.px(1)), colBorderHi)
	if u.pressed && !r.Contains(u.pressX, u.pressY) && !pill.Contains(u.pressX, u.pressY) {
		s.picking = false
	}
	for _, k := range u.keys {
		if k.Code == keyEscape {
			s.picking = false
		}
	}
	kind := s.pickKind
	if kind == "" {
		kind = current.Kind
	}
	inner := r.Inset(u.px(6))
	tabs, below := a.aiKindBar(inner)
	s.pickTabs = tabs
	a.drawAIKindBar(tabs, kind)
	below += u.px(6)
	a.scrolledBy("ai/models", rectAt(inner.X, below, inner.W, inner.Bottom()-below), &s.scroll[aiScrollModels], &s.content[aiScrollModels], func(view gfx.Rect) float32 {
		y := view.Y
		row := func(ch ai.Choice, state string, stateCol color.RGBA) {
			rr := rectAt(view.X, y, view.W, u.px(44))
			id := "ai/choose/" + ch.ID
			selected := ch.ID == current.ID
			bg := color.RGBA{}
			if selected {
				bg = colSelected
			} else if u.hover(id, rr) {
				bg = colSurfaceHi
				u.cursor = CursorHand
			}
			u.c.Fill(rr, bg)
			name := ch.Name.In(lang)
			if ch.Vision {
				name += "  ·  " + u.t("reads pictures")
			}
			u.c.Text(u.bold(13), rr.X+u.px(10), rr.Y+u.px(5), u.bold(13).Ellipsize(name, rr.W-u.px(170)), colText)
			u.c.Text(u.face(11.5), rr.X+u.px(10), rr.Y+u.px(24), u.face(11.5).Ellipsize(ch.Note.In(lang), rr.W-u.px(170)), colFaint)
			sf := u.face(11.5)
			state = sf.Ellipsize(state, u.px(150))
			u.c.Text(sf, rr.Right()-u.px(10)-sf.Measure(state), rr.Y+(rr.H-sf.Height())/2, state, stateCol)
			if u.clicked(id, rr) {
				a.aiPick(ch)
				s.picking = false
			}
			y += u.px(46)
		}
		group, listed := "", false
		for _, ch := range a.aiChoices() {
			if ch.Kind != kind {
				continue
			}
			listed = true
			// Models of one series, or of one online service, are listed under its name.
			label := ""
			switch {
			case ch.Online():
				label = ch.Provider.Name.In(lang)
			case ch.External():
				label = ch.Series.In(lang)
			default:
				label = ch.Local.Series.In(lang)
			}
			if label != "" && label != group {
				u.c.Text(u.face(12), view.X+u.px(10), y+u.px(2), label, colDim)
				y += u.px(22)
			}
			group = label
			state, col := a.aiChoiceState(ch)
			row(ch, state, col)
		}
		if !listed {
			y = a.drawAINoModels(inner, y)
		}
		return y
	})
}

// drawAIVoices is the choice of voices over the input of a voice-over model, the same choice as in the voice-over panel.
func (a *App) drawAIVoices(r gfx.Rect) {
	u := &a.u
	c := a.speech.config
	names := make([]string, len(c.Voices))
	current := -1
	id := a.speechVoiceID()
	for i, v := range c.Voices {
		names[i] = v.Name.In(string(u.lang))
		if v.ID == id {
			current = i
		}
	}
	pick, h := u.flowChips("ai/voice", rectAt(r.X, r.Y, r.W, u.px(28)), names, current)
	a.ai.voiceH = h
	if pick >= 0 && pick != current {
		a.settings.SpeechVoice = c.Voices[pick].ID
		a.saveSettings()
	}
}
