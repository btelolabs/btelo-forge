package ui

import (
	"fmt"
	"image/color"
	"slices"

	"rsc.io/qr"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

var settingsPages = msgs("General", "AI (MCP)", "Phone", "Hotkeys", "File types", "AI models", "Extensions", "About")

// The settings pages, by their place in settingsPages.
const (
	settingsGeneralPage = iota
	settingsMCPPage
	settingsPhonePage
	settingsHotkeysPage
	settingsFileTypesPage
	settingsAIPage
	settingsExtensionsPage
	settingsAboutPage
)

// visibleSettingsPages are the settings pages, in order.
func (a *App) visibleSettingsPages() []int {
	pages := make([]int, len(settingsPages))
	for i := range pages {
		pages[i] = i
	}
	return pages
}

// drawSettings is the settings dialog opened from the end of the tab strip.
func (a *App) drawSettings() {
	if !a.settingsOpen || a.settingsPage != settingsHotkeysPage {
		// Waiting for a key ends when its page is left, so the window's own keys come back.
		a.keyCapture = nil
		if a.hotkeys.capturing >= 0 {
			a.hotkeys.capturing = -1
			a.applyHotkeys()
		}
	}
	if !a.settingsOpen {
		return
	}
	u := &a.u
	u.c.Fill(a.screen, gfxShade)
	w, h := min(u.px(1040), a.screen.W-u.px(40)), min(u.px(760), a.screen.H-u.px(40))
	r := rectCentered(a.screen, w, h)
	panel(u, r)
	in := r.Inset(u.px(22))
	u.label(u.bold(17), rectAt(in.X, in.Y, in.W-u.px(40), u.px(26)), u.t("Settings"), colText, 0)
	if u.iconButton("settings/close", rectAt(in.Right()-u.px(30), in.Y-u.px(2), u.px(30), u.px(30)), icClose, u.t("Close"), false, true) {
		a.settingsOpen = false
	}
	nav := rectAt(in.X, in.Y+u.px(44), u.px(150), in.H-u.px(44))
	for row, i := range a.visibleSettingsPages() {
		name := settingsPages[i]
		row := rectAt(nav.X, nav.Y+float32(row)*u.px(38), nav.W, u.px(34))
		id := fmt.Sprintf("settings/page/%d", i)
		bg, fg := colPanel, colDim
		if i == a.settingsPage {
			bg, fg = colSelected, colText
		} else if u.hover(id, row) {
			bg, fg = colSurface, colText
			u.cursor = CursorHand
		}
		u.c.Fill(row, bg)
		u.label(u.face(13), rectAt(row.X+u.px(12), row.Y, row.W-u.px(16), row.H), u.t(name), fg, 0)
		if u.clicked(id, row) {
			a.settingsPage = i
		}
	}
	body := rectAt(nav.Right()+u.px(22), nav.Y, in.Right()-nav.Right()-u.px(22), nav.H)
	u.c.Push(body.Inset(-u.px(2)))
	switch a.settingsPage {
	case settingsGeneralPage:
		a.settingsGeneral(body)
	case settingsMCPPage:
		a.settingsMCP(body)
	case settingsPhonePage:
		a.settingsPhone(body)
	case settingsHotkeysPage:
		a.settingsHotkeys(body)
	case settingsFileTypesPage:
		a.settingsFileTypes(body)
	case settingsAIPage:
		a.settingsAI(body)
	case settingsExtensionsPage:
		a.settingsExtensions(body)
	case settingsAboutPage:
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

// tabPlaces are where the tabs can run, in the order of the choice in Settings.
var tabPlaces = msgs("Along the top", "Down the left side")

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
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Tabs"))
	y += u.px(28)
	place := 0
	if a.settings.VerticalTabs {
		place = 1
	}
	if pick := u.chips("settings/tabs", rectAt(r.X, y, min(r.W, u.px(280)), u.px(32)), u.ts(tabPlaces), place, true); pick != place {
		a.settings.VerticalTabs = pick == 1
		a.saveSettings()
	}
	y += u.px(52)
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Resource folder"))
	y += u.px(28)
	root, err := a.resourceDir()
	if err != nil {
		root = err.Error()
	}
	box := rectAt(r.X, y, r.W, u.px(36))
	u.c.Fill(box, colSurface)
	u.label(u.face(12.5), box.Inset(u.px(10)), root, colText, 0)
	y += u.px(44)
	if u.button("settings/resources", rectAt(r.X, y, u.px(150), u.px(32)), icFolder, u.t("Choose folder…"), plain, true) {
		a.chooseResourceDir()
	}
	if a.settings.ResourceDir != "" && u.button("settings/resources/reset", rectAt(r.X+u.px(158), y, u.px(200), u.px(32)), "", u.t("Use the default folder"), ghost, true) {
		a.setResourceDir("")
	}
	y += u.px(42)
	y = a.paragraph(r.X, y, r.W, u.t("New work is saved in its folder here: Tools, Creative, Documents or Intelligence. Changing this moves nothing that is already saved."), 12, colFaint)
	y += u.px(18)
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Default export folder"))
	y += u.px(28)
	dir := a.exportDir()
	if dir == "" {
		dir = u.t("Next to the original video")
	}
	box = rectAt(r.X, y, r.W, u.px(36))
	u.c.Fill(box, colSurface)
	u.label(u.face(12.5), box.Inset(u.px(10)), dir, colText, 0)
	y += u.px(44)
	if u.button("settings/dir", rectAt(r.X, y, u.px(150), u.px(32)), icFolder, u.t("Choose folder…"), plain, true) {
		a.chooseExportDir()
	}
	if !a.settings.ExportBeside && u.button("settings/dir/beside", rectAt(r.X+u.px(158), y, u.px(200), u.px(32)), "", u.t("Next to the original video"), ghost, true) {
		a.settings.ExportDir, a.settings.ExportBeside = "", true
		a.saveSettings()
	}
	if (a.settings.ExportBeside || a.settings.ExportDir != "") && u.button("settings/dir/reset", rectAt(r.X+u.px(366), y, u.px(200), u.px(32)), "", u.t("In the resource folder"), ghost, true) {
		a.settings.ExportDir, a.settings.ExportBeside = "", false
		a.saveSettings()
	}
	y += u.px(48)
	y = a.paragraph(r.X, y, r.W, u.t("Export quality, size and codec are remembered from your last export and used for new edits."), 12, colFaint)
	y += u.px(18)
	u.heading(rectAt(r.X, y, r.W, u.px(22)), "ffmpeg")
	y += u.px(26)
	info, failed := a.toolsStatus()
	col := colDim
	if failed {
		col = colDangerText
	}
	if info == "" {
		info = u.tf("Version %s · %s", a.tools.found.Version, a.tools.found.FFmpeg)
	}
	y = a.paragraph(r.X, y, r.W, info, 12, col)
	a.paragraph(r.X, y+u.px(4), r.W, u.t("Found on this computer or downloaded once into the app's own folder; where it comes from is set in Settings → Extensions."), 11.5, colFaint)
}

func (a *App) settingsMCP(r gfx.Rect) {
	u := &a.u
	y := a.paragraph(r.X, r.Y, r.W, u.t("Any AI that speaks MCP (Claude, Codex, Cursor and others) can drive this app: edit videos, draw on canvases and record the screen, look at the result and export. Choose what your AI will do, copy the text below and give it to your AI — that is all it needs."), 12.5, colDim)
	y += u.px(10)
	if on := u.checkbox("settings/mcp", rectAt(r.X, y, r.W, u.px(26)), u.tf("Let AI tools control %s (only from this computer)", ProductName), a.settings.MCP); on != a.settings.MCP {
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
		a.paragraph(r.X, y, r.W, msg, 12.5, colDangerText)
		return
	}
	u.label(u.face(12), rectAt(r.X, y, u.px(90), u.px(30)), u.t("Address"), colDim, 0)
	addr := rectAt(r.X+u.px(90), y, r.W-u.px(90), u.px(30))
	u.c.Fill(addr, colSurface)
	u.label(u.face(12.5), addr.Inset(u.px(8)), s.mcpURL, colAccentHi, 0)
	y += u.px(44)
	// Each kind of work has its own server and setup, so the AI hears only about the tools it needs.
	names := u.ts(mcpDomainNames)
	at := max(slices.Index(mcpDomains, a.mcpDomain), 0)
	if pick := u.tabs("settings/mcp/domain", rectAt(r.X, y, r.W, u.px(34)), names, at); pick != at {
		a.mcpDomain, a.mcpScroll = mcpDomains[pick], 0
	}
	domain := mcpDomains[max(slices.Index(mcpDomains, a.mcpDomain), 0)]
	y += u.px(44)
	if u.button("settings/mcp/copy", rectAt(r.X, y, r.W, u.px(38)), icCopy, u.tf("Copy the setup for your AI: %s", names[slices.Index(mcpDomains, domain)]), primary, true) {
		a.copyText(a.mcpConnection(domain))
	}
	y += u.px(50)
	box := rectAt(r.X, y, r.W, r.Bottom()-y)
	u.c.Fill(box, gfx.Mix(colSurface, colBlack, 0.3))
	inner := box.Inset(u.px(10))
	if u.hover("settings/mcp/text", box) && u.wheel != 0 {
		a.mcpScroll -= u.wheel * u.px(60)
		u.wheel = 0
	}
	u.c.Push(inner)
	bottom := a.paragraph(box.X+u.px(12), inner.Y-a.mcpScroll, box.W-u.px(30), a.mcpConnection(domain), 11, colDim)
	u.c.Pop()
	content := bottom + a.mcpScroll - inner.Y
	a.mcpScroll = min(max(a.mcpScroll, 0), max(content-inner.H, 0))
	if content > inner.H {
		h := max(inner.H*inner.H/content, u.px(24))
		sy := inner.Y + (inner.H-h)*a.mcpScroll/(content-inner.H)
		u.c.Fill(rectAt(box.Right()-u.px(7), sy, u.px(3), h), colSurfaceHi)
	}
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
	y := a.paragraph(r.X, r.Y, r.W, u.t("Open a page on your phone to play and download the videos you exported, and to send videos, sound and pictures from the phone to this computer. The phone must be on the same Wi-Fi; the address contains a private key, so only people you give it to can use it."), 12.5, colDim)
	y += u.px(10)
	if on := u.checkbox("settings/phone", rectAt(r.X, y, r.W, u.px(26)), u.t("Connect my phone (download exports, upload from the phone)"), a.settings.Phone); on != a.settings.Phone {
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
		a.paragraph(r.X, y, r.W, msg, 12.5, colDangerText)
		return
	}
	if len(s.phoneURL) == 0 {
		a.paragraph(r.X, y, r.W, u.t("This computer has no local network address; connect it to Wi-Fi or a router first."), 12.5, colDangerText)
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
		for i, link := range s.phoneURL {
			ty += u.px(6)
			cb := rectAt(r.Right()-u.px(34), ty, u.px(30), u.px(30))
			end := a.paragraph(tx, ty+u.px(4), cb.X-tx-u.px(8), link, 11.5, colAccentHi)
			if u.iconButton(fmt.Sprintf("settings/phone/copy/%d", i), cb, icCopy, u.t("Copy address"), false, true) {
				a.copyText(link)
			}
			ty = max(end, cb.Bottom())
		}
		y = max(q.Bottom(), ty) + u.px(18)
	}
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Uploads from the phone are saved in"))
	y += u.px(26)
	dir, err := a.categoryDir(categoryTools)
	if err != nil {
		dir = err.Error()
	}
	box := rectAt(r.X, y, r.W-u.px(160), u.px(32))
	u.c.Fill(box, colSurface)
	u.label(u.face(12), box.Inset(u.px(10)), dir, colText, 0)
	if u.button("settings/phone/dir", rectAt(box.Right()+u.px(8), y, u.px(152), u.px(32)), icFolder, u.t("Choose folder…"), plain, true) {
		a.chooseResourceDir()
	}
	y += u.px(38)
	y = a.paragraph(r.X, y, r.W, u.t("This is the Tools folder of the resource folder (Settings → General)."), 11.5, colFaint)
	a.paragraph(r.X, y+u.px(6), r.W, u.tf("%d exported videos are listed.", len(a.exports())), 12, colFaint)
}

func (a *App) settingsAbout(r gfx.Rect) {
	u := &a.u
	y := r.Y
	mark := rectAt(r.X, y, u.px(64), u.px(64))
	drawLogo(u, mark)
	u.label(u.bold(16), rectAt(mark.Right()+u.px(14), y+u.px(8), r.W, u.px(24)), a.Title(), colText, 0)
	u.label(u.face(12), rectAt(mark.Right()+u.px(14), y+u.px(36), r.W, u.px(20)), u.tf("Version %s", Version), colDim, 0)
	y += u.px(84)
	y = a.settingsUpdates(r, y)
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
	y = a.paragraph(r.X, y, r.W, u.tf("%s is open source under the Apache License 2.0: free to use, modify and redistribute, including commercially. See LICENSE in the repository for the full terms.", ProductName), 12.5, colDim)
	y += u.px(14)
	a.paragraph(r.X, y, r.W, u.t("Music from the built-in libraries keeps its own licence (CC0 or CC BY); credits for it are written next to each export."), 12, colFaint)
}
