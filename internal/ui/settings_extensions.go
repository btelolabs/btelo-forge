package ui

import (
	"regexp"
	"strconv"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// extensionsPage edits the JSON configurations in Settings.
type extensionsPage struct {
	tab   int
	text  map[string]string // the editor's text, loaded when first shown
	saved map[string]string // the text in effect, to know whether there are unsaved changes
	err   map[string]string
	view  map[string]*codeView
}

// forget drops the editor's copy of a configuration, so it shows what is in effect next time.
func (e *extensionsPage) forget(id string) {
	delete(e.text, id)
	delete(e.saved, id)
	delete(e.err, id)
	if v := e.view[id]; v != nil {
		v.reset()
	}
}

var jsonLine = regexp.MustCompile(`line (\d+), column`)

func (a *App) settingsExtensions(r gfx.Rect) {
	u := &a.u
	e := &a.extensions
	if e.text == nil {
		e.text, e.saved, e.err, e.view = map[string]string{}, map[string]string{}, map[string]string{}, map[string]*codeView{}
	}
	titles := make([]string, len(configKinds))
	for i, k := range configKinds {
		titles[i] = u.t(k.title)
	}
	y := r.Y
	if pick := u.chips("settings/ext/tab", rectAt(r.X, y, min(r.W, u.px(320)), u.px(30)), titles, e.tab, true); pick != e.tab {
		e.tab = pick
		u.focus = ""
	}
	k := configKinds[e.tab]
	if _, ok := e.text[k.id]; !ok {
		text, _ := a.configText(k)
		e.text[k.id], e.saved[k.id] = text, text
		e.view[k.id] = &codeView{}
	}
	_, custom := a.configText(k)
	y += u.px(40)
	about := u.t("Music sources are the websites the music panel searches. Add a site by describing its search address and how to read its answers; see the format guide.")
	if k.id == "subtitle_models" {
		about = u.t("Speech engines, models and languages for AI subtitles. Point to another whisper.cpp version or another ggml model; see the format guide.")
	}
	y = a.paragraph(r.X, y, r.W, about, 12, colDim) + u.px(4)
	where := u.t("Using the built-in configuration")
	if custom {
		where = u.tf("Using your file: %s", a.configPath(k))
	}
	u.label(u.face(11.5), rectAt(r.X, y, r.W-u.px(250), u.px(28)), where, colFaint, 0)
	if u.button("settings/ext/guide", rectAt(r.Right()-u.px(244), y, u.px(120), u.px(28)), icCopy, u.t("Copy guide"), ghost, true) {
		a.copyText(k.guide)
	}
	if u.button("settings/ext/reveal", rectAt(r.Right()-u.px(120), y, u.px(120), u.px(28)), icFolder, u.t("Show file"), ghost, custom) {
		a.host.Reveal(a.configPath(k))
	}
	y += u.px(36)
	footer := u.px(84)
	save := false
	for i := 0; i < len(u.keys); i++ {
		if key := u.keys[i]; key.Ctrl && key.Code == 'S' {
			save = true
			u.keys = append(u.keys[:i], u.keys[i+1:]...)
			i--
		}
	}
	text := e.text[k.id]
	errLine := 0
	if m := jsonLine.FindStringSubmatch(e.err[k.id]); m != nil {
		errLine, _ = strconv.Atoi(m[1])
	}
	if u.codeBox("settings/ext/code/"+k.id, rectAt(r.X, y, r.W, r.Bottom()-y-footer), &text, e.view[k.id], errLine) {
		e.text[k.id] = text
	}
	y = r.Bottom() - footer + u.px(10)
	dirty := e.text[k.id] != e.saved[k.id]
	msg, col := u.t("Saving checks the JSON first and applies it at once. An AI connected over MCP can change it too (get_config, set_config)."), colFaint
	switch {
	case e.err[k.id] != "":
		msg, col = e.err[k.id], gfx.Hex(0xffb4b2)
	case dirty:
		msg, col = u.t("Unsaved changes"), colWarning
	}
	f := u.face(11.5)
	lines := f.Wrap(msg, r.W)
	for i, l := range lines[:min(len(lines), 2)] {
		u.c.Text(f, r.X, y+float32(i)*f.Height()*1.2, l, col)
	}
	y += u.px(36)
	if u.button("settings/ext/save", rectAt(r.Right()-u.px(160), y, u.px(160), u.px(34)), icCheck, u.t("Save (Ctrl+S)"), primary, dirty) || save && dirty {
		if err := a.saveConfig(k, e.text[k.id]); err != nil {
			e.err[k.id] = err.Error()
		} else {
			e.saved[k.id], e.err[k.id] = e.text[k.id], ""
			a.toast(u.tf("%s saved", u.t(k.title)), false)
		}
	}
	if u.button("settings/ext/discard", rectAt(r.Right()-u.px(296), y, u.px(128), u.px(34)), "", u.t("Discard changes"), plain, dirty) {
		e.text[k.id], e.err[k.id] = e.saved[k.id], ""
		e.view[k.id].reset()
		u.focus = ""
	}
	builtIn := string(k.defaults)
	if u.button("settings/ext/restore", rectAt(r.X, y, u.px(170), u.px(34)), icRestore, u.t("Restore defaults"), plain, custom || e.text[k.id] != builtIn) {
		if err := a.resetConfig(k); err != nil {
			e.err[k.id] = err.Error()
		} else {
			e.view[k.id].remember(&editor{text: []rune(e.text[k.id])}, false)
			e.text[k.id], e.saved[k.id], e.err[k.id] = builtIn, builtIn, ""
			u.focus = ""
			a.toast(u.tf("%s restored to the defaults; Ctrl+Z in the editor brings your version back", u.t(k.title)), false)
		}
	}
}
