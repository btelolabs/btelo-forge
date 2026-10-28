package ui

import (
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// TestReadmeScreens renders the screenshots of the README from a real edit of FORGE_README_VIDEO into FORGE_README_OUT,
// in English and Chinese, with FORGE_README_MUSIC (Kevin MacLeod's "The Britons") as background music and
// FORGE_README_LOGO as an image overlay. The music search asks the real sites.
func TestReadmeScreens(t *testing.T) {
	video, out := os.Getenv("FORGE_README_VIDEO"), os.Getenv("FORGE_README_OUT")
	if video == "" || out == "" {
		t.Skip("set FORGE_README_VIDEO to a video and FORGE_README_OUT to a folder to render the README screenshots")
	}
	music, logo := os.Getenv("FORGE_README_MUSIC"), os.Getenv("FORGE_README_LOGO")
	for _, lang := range []Language{English, Chinese} {
		x := newHarness(t)
		x.c.Resize(1920, 1280)
		x.scale = 1.25
		x.a.u.lang = lang
		x.a.settings.Language = lang
		x.a.lib.SetConfig(library.DefaultConfig())
		x.open(video)
		readmeEdit(t, x, lang, music, logo)

		save := func(name string) {
			x.frame(Input{})
			x.frame(Input{})
			f, err := os.Create(filepath.Join(out, string(lang)+"-"+name+".jpg"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := jpeg.Encode(f, x.c.Img, &jpeg.Options{Quality: 86}); err != nil {
				t.Fatal(err)
			}
		}
		title := x.a.p.Overlays[0].Items[0].ID
		x.a.selectOnly(selection{kind: selOverlay, id: title})
		x.a.tab = tabOverlays
		x.a.seek(2.2)
		x.settle(30 * time.Second)
		save("editor")

		var lines []int
		for _, o := range x.a.p.Overlays[1].Items {
			lines = append(lines, o.ID)
		}
		x.a.selectOnly(selection{})
		for _, id := range lines {
			x.a.addPick(id)
		}
		x.a.seek(9.2)
		x.settle(30 * time.Second)
		save("select")

		x.a.selectOnly(selection{})
		x.a.tab = tabExport
		x.a.setExport(exportPresets[3].export(x.a.av1()))
		save("export")

		x.a.tab = tabMusic
		x.a.music.query, x.a.music.asked = "", ""
		x.pump(90*time.Second, func() bool {
			x.frame(Input{})
			return len(x.a.music.results) > 0 && !x.a.music.busy
		})
		save("music")

		x.a.settingsOpen, x.a.settingsPage = true, 1
		x.a.services.mcpURL = "http://127.0.0.1:38471/mcp"
		save("ai")
		x.a.settingsOpen = false
	}
}

// readmeEdit cuts the video into a small, believable edit: a faster second half, a title, timed captions, a logo and
// background music.
func readmeEdit(t *testing.T, x *harness, lang Language, music, logo string) {
	a := x.a
	p := a.p
	length := p.Duration()
	a.change(func() {
		main := p.Video[0].Clips[0].ID
		p.SplitClip(main, length*0.55)
		p.SetSpeed(p.Video[0].Clips[1].ID, 1.5)
	})
	title, captions := "Spring in town", []string{"Morning in the village", "Red roofs in the sun", "Clouds rolling in", "Back to the square"}
	if lang == Chinese {
		title, captions = "小镇的春天", []string{"清晨的村子", "阳光下的红屋顶", "云慢慢压过来", "回到小广场"}
	}
	a.seek(0.4)
	a.addText()
	a.change(func() {
		id := a.sel.id
		p.UpdateOverlay(id, func(o *project.Overlay) {
			o.Body, o.Y, o.Size, o.Color = title, 0.2, 0.11, [3]uint8{255, 214, 10}
		})
		p.SetOverlayRange(id, 0.4, math.Min(4.2, length))
	})
	a.u.focus = ""
	var items []project.Overlay
	step := (p.Duration() - 1) / float64(len(captions))
	for i, c := range captions {
		start := 0.8 + float64(i)*step
		items = append(items, project.Overlay{Body: c, Start: start, End: start + step - 0.2, Opacity: 1})
	}
	a.change(func() { p.AddCaptions(items) })
	if logo != "" {
		a.seek(0)
		a.addImage(logo)
		a.change(func() {
			id := a.sel.id
			p.UpdateOverlay(id, func(o *project.Overlay) {
				o.X, o.Y, o.Size, o.Opacity = 0.93, 0.12, 0.13, 0.85
			})
			p.SetOverlayRange(id, 0, p.Duration())
		})
	}
	if music != "" {
		info, err := media.Probe(t.Context(), music)
		if err != nil {
			t.Fatal(err)
		}
		credit := `"The Britons" Kevin MacLeod (incompetech.com) · Licensed under Creative Commons: By Attribution 4.0`
		a.change(func() {
			id := p.AddMusic(music, "The Britons", credit, info.Duration, 0)
			p.SetMusic(id, 0.6, true)
		})
	}
	if strings.TrimSpace(title) == "" {
		t.Fatal("no title")
	}
	x.settle(60 * time.Second)
}
