package ui

import (
	"os"
	"testing"
	"time"
)

// TestScreens renders every panel and settings page in both languages into BVC_SHOTS, for looking at the design.
func TestScreens(t *testing.T) {
	if os.Getenv("BVC_SHOTS") == "" {
		t.Skip("set BVC_SHOTS to a folder to render the screens")
	}
	x := newHarness(t)
	x.open(x.fixture())
	x.a.services.mcpURL = "http://127.0.0.1:38471/mcp"
	for _, lang := range []Language{English, Chinese} {
		x.a.u.lang = lang
		for tab, name := range []string{"music", "overlays", "subtitles", "export"} {
			x.a.tab = tab
			x.settle(10 * time.Second)
			x.shot(string(lang) + "-panel-" + name)
			if name == "export" {
				x.a.panelScroll[tab] = 1e6
				x.frame(Input{})
				x.shot(string(lang) + "-panel-export-end")
				x.a.panelScroll[tab] = 0
			}
		}
		x.a.seek(2)
		x.a.addText()
		x.a.selectAll()
		x.a.tab = 1
		x.frame(Input{})
		x.frame(Input{})
		x.shot(string(lang) + "-selection")
		x.a.undo()
		x.a.selectOnly(selection{})
		x.a.settingsOpen = true
		for page, name := range []string{"general", "mcp", "phone", "extensions", "about"} {
			x.a.settingsPage = page
			if page == 2 {
				x.a.settings.Phone = true
				x.a.applyServices()
			}
			x.frame(Input{})
			x.shot(string(lang) + "-settings-" + name)
		}
		x.a.settingsOpen = false
	}
}
