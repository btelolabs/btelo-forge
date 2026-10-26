package ui

import (
	"fmt"
	"image/color"
	"slices"

	"rsc.io/qr"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

var settingsPages = msgs("General", "AI (MCP)", "Phone", "Extensions", "About")

const settingsExtensionsPage = 3

// drawSettings is the settings dialog opened from the end of the tab strip.
func (a *App) drawSettings() {
	if !a.settingsOpen {
		return
	}
	u := &a.u
	u.c.Fill(a.screen, gfxShade)
	w, h := min(u.px(760), a.screen.W-u.px(40)), min(u.px(560), a.screen.H-u.px(40))
	if a.settingsPage == settingsExtensionsPage {
		w, h = min(u.px(1040), a.screen.W-u.px(40)), min(u.px(760), a.screen.H-u.px(40))
	}
	r := rectCentered(a.screen, w, h)
	panel(u, r)
	in := r.Inset(u.px(22))
	u.label(u.bold(17), rectAt(in.X, in.Y, in.W-u.px(40), u.px(26)), u.t("Settings"), colText, 0)
	if u.iconButton("settings/close", rectAt(in.Right()-u.px(30), in.Y-u.px(2), u.px(30), u.px(30)), icClose, u.t("Close"), false, true) {
		a.settingsOpen = false
	}
	nav := rectAt(in.X, in.Y+u.px(44), u.px(150), in.H-u.px(44))
	for i, name := range settingsPages {
		row := rectAt(nav.X, nav.Y+float32(i)*u.px(38), nav.W, u.px(34))
		id := fmt.Sprintf("settings/page/%d", i)
		bg, fg := colPanel, colDim
		if i == a.settingsPage {
			bg, fg = gfx.Mix(colSurface, colAccent, 0.2), colAccentHi
		} else if u.hover(id, row) {
			bg, fg = colSurface, colText
			u.cursor = CursorHand
		}
		u.c.RoundRect(row, u.px(7), bg)
		u.label(u.face(13), rectAt(row.X+u.px(12), row.Y, row.W-u.px(16), row.H), u.t(name), fg, 0)
		if u.clicked(id, row) {
			a.settingsPage = i
		}
	}
	body := rectAt(nav.Right()+u.px(22), nav.Y, in.Right()-nav.Right()-u.px(22), nav.H)
	u.c.Push(body.Inset(-u.px(2)))
	switch a.settingsPage {
	case 0:
		a.settingsGeneral(body)
	case 1:
		a.settingsMCP(body)
	case 2:
		a.settingsPhone(body)
	case settingsExtensionsPage:
		a.settingsExtensions(body)
	case 4:
		a.settingsAbout(body)
	}
	u.c.Pop()
	for _, k := range u.keys {
		if k.Code == keyEscape && u.focus == "" {
			a.settingsOpen = false
		}
	}
	u.keys = nil
}

// paragraph draws wrapped text and returns the y below it.
func (a *App) paragraph(x, y, w float32, text string, size float32, col color.RGBA) float32 {
	u := &a.u
	f := u.face(size)
	for _, l := range f.Wrap(text, w) {
		u.c.Text(f, x, y, l, col)
		y += f.Height() * 1.35
	}
	return y
}

func (a *App) settingsGeneral(r gfx.Rect) {
	u := &a.u
	y := r.Y
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Language"))
	y += u.px(28)
	current := slices.Index(languages, a.settings.Language)
	if pick := u.chips("settings/lang", rectAt(r.X, y, min(r.W, u.px(280)), u.px(32)), languageNames, current, true); pick != current {
		a.setLanguage(languages[pick])
	}
	y += u.px(52)
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Default export folder"))
	y += u.px(28)
	dir := a.settings.ExportDir
	if dir == "" {
		dir = u.t("Next to the original video")
	}
	box := rectAt(r.X, y, r.W, u.px(36))
	u.c.RoundRect(box, u.px(7), colSurface)
	u.label(u.face(12.5), box.Inset(u.px(10)), dir, colText, 0)
	y += u.px(44)
	if u.button("settings/dir", rectAt(r.X, y, u.px(150), u.px(32)), icFolder, u.t("Choose folder…"), plain, true) {
		a.chooseExportDir()
	}
	if a.settings.ExportDir != "" && u.button("settings/dir/reset", rectAt(r.X+u.px(158), y, u.px(200), u.px(32)), "", u.t("Next to the original video"), ghost, true) {
		a.settings.ExportDir = ""
		a.saveSettings()
	}
	y += u.px(48)
	a.paragraph(r.X, y, r.W, u.t("Export quality, size and codec are remembered from your last export and used for new edits."), 12, colFaint)
}

func (a *App) settingsMCP(r gfx.Rect) {
	u := &a.u
	y := a.paragraph(r.X, r.Y, r.W, u.t("Any AI that speaks MCP (Claude, Codex, Cursor and others) can drive this app: open videos, cut, add text, images, music and subtitles, look at the result and export. Copy the text below and give it to your AI — that is all it needs."), 12.5, colDim)
	y += u.px(10)
	if on := u.checkbox("settings/mcp", rectAt(r.X, y, r.W, u.px(26)), u.t("Let AI tools control btelo Cut (only from this computer)"), a.settings.MCP); on != a.settings.MCP {
		a.settings.MCP = on
		a.saveSettings()
		a.applyServices()
	}
	y += u.px(38)
	s := a.services
	switch {
	case !a.settings.MCP:
		a.paragraph(r.X, y, r.W, u.t("Turned off."), 12.5, colFaint)
		return
	case s == nil || s.mcp == nil:
		msg := u.t("The MCP server could not start")
		if s != nil && s.mcpErr != nil {
			msg += ": " + s.mcpErr.Error()
		}
		a.paragraph(r.X, y, r.W, msg, 12.5, gfx.Hex(0xffb4b2))
		return
	}
	u.label(u.face(12), rectAt(r.X, y, u.px(90), u.px(30)), u.t("Address"), colDim, 0)
	addr := rectAt(r.X+u.px(90), y, r.W-u.px(90), u.px(30))
	u.c.RoundRect(addr, u.px(6), colSurface)
	u.label(u.face(12.5), addr.Inset(u.px(8)), s.mcpURL, colAccentHi, 0)
	y += u.px(44)
	if u.button("settings/mcp/copy", rectAt(r.X, y, r.W, u.px(38)), icCopy, u.t("Copy the setup for your AI"), primary, true) {
		a.copyText(a.mcpConnection())
	}
	y += u.px(50)
	text := a.mcpConnection()
	box := rectAt(r.X, y, r.W, r.Bottom()-y)
	u.c.RoundRect(box, u.px(8), gfx.Mix(colSurface, colBlack, 0.3))
	u.c.Push(box.Inset(u.px(10)))
	a.paragraph(box.X+u.px(12), box.Y+u.px(10), box.W-u.px(24), text, 11, colDim)
	u.c.Pop()
}

func (a *App) copyText(text string) {
	if err := a.host.SetClipboard(text); err != nil {
		a.toast(a.u.tf("Copying failed: %v", err), true)
		return
	}
	a.toast(a.u.t("Copied"), false)
}

func (a *App) settingsPhone(r gfx.Rect) {
	u := &a.u
	y := a.paragraph(r.X, r.Y, r.W, u.t("Open a page on your phone to play and download the videos you exported. The phone must be on the same Wi-Fi; the address contains a private key, so only people you give it to can use it."), 12.5, colDim)
	y += u.px(10)
	if on := u.checkbox("settings/phone", rectAt(r.X, y, r.W, u.px(26)), u.t("Share exported videos with my phone"), a.settings.Phone); on != a.settings.Phone {
		a.settings.Phone = on
		a.saveSettings()
		a.applyServices()
	}
	y += u.px(38)
	s := a.services
	if !a.settings.Phone {
		return
	}
	if s == nil || s.phone == nil {
		msg := u.t("The phone service could not start")
		if s != nil && s.phoneErr != nil {
			msg += ": " + s.phoneErr.Error()
		}
		a.paragraph(r.X, y, r.W, msg, 12.5, gfx.Hex(0xffb4b2))
		return
	}
	if len(s.phoneURL) == 0 {
		a.paragraph(r.X, y, r.W, u.t("This computer has no local network address; connect it to Wi-Fi or a router first."), 12.5, gfx.Hex(0xffb4b2))
		return
	}
	url := s.phoneURL[0]
	if code, err := qr.Encode(url, qr.M); err == nil {
		side := min(u.px(190), r.Bottom()-y-u.px(60))
		cell := max(1, int(side)/(code.Size+4))
		px := float32(cell)
		q := rectAt(r.X, y, px*float32(code.Size+4), px*float32(code.Size+4))
		u.c.Fill(q, colWhite)
		for yy := 0; yy < code.Size; yy++ {
			for xx := 0; xx < code.Size; xx++ {
				if code.Black(xx, yy) {
					u.c.Fill(rectAt(q.X+px*float32(xx+2), q.Y+px*float32(yy+2), px, px), colBlack)
				}
			}
		}
		tx := q.Right() + u.px(18)
		ty := a.paragraph(tx, y, r.Right()-tx, u.t("Scan with the phone camera, or type one of these addresses into its browser:"), 12, colDim)
		for _, link := range s.phoneURL {
			ty = a.paragraph(tx, ty+u.px(4), r.Right()-tx, link, 11.5, colAccentHi)
		}
		if u.button("settings/phone/copy", rectAt(tx, ty+u.px(10), u.px(160), u.px(32)), icCopy, u.t("Copy address"), plain, true) {
			a.copyText(url)
		}
		y = max(q.Bottom(), ty+u.px(42)) + u.px(16)
	}
	a.paragraph(r.X, y, r.W, u.tf("%d exported videos are listed.", len(a.exports())), 12, colFaint)
}

func (a *App) settingsAbout(r gfx.Rect) {
	u := &a.u
	y := r.Y
	mark := rectAt(r.X, y, u.px(64), u.px(64))
	drawLogo(u, mark)
	u.label(u.bold(16), rectAt(mark.Right()+u.px(14), y+u.px(8), r.W, u.px(24)), a.Title(), colText, 0)
	u.label(u.face(12), rectAt(mark.Right()+u.px(14), y+u.px(36), r.W, u.px(20)), u.tf("Version %s", Version), colDim, 0)
	y += u.px(84)
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Source code"))
	y += u.px(26)
	link := rectAt(r.X, y, r.W, u.px(22))
	hot := u.hover("settings/repo", link)
	col := colAccentHi
	if hot {
		col = colAccent
		u.cursor = CursorHand
	}
	u.label(u.face(13), link, Repository, col, 0)
	if u.clicked("settings/repo", link) {
		a.host.LaunchURL(Repository)
	}
	y += u.px(36)
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Licence"))
	y += u.px(26)
	y = a.paragraph(r.X, y, r.W, u.t("btelo Cut is free to use, including for commercial videos. You may read the source and change it for your own use. Redistributing it, selling it, or selling copies or modified versions is not allowed. See LICENSE in the repository for the full terms."), 12.5, colDim)
	y += u.px(14)
	a.paragraph(r.X, y, r.W, u.t("Music from the built-in libraries keeps its own licence (CC0 or CC BY); credits for it are written next to each export."), 12, colFaint)
}
