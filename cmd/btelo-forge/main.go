// Command btelo-forge is Btelo Forge, a desktop app for making videos and pictures: a video editor (cut, speed up,
// stack tracks, add music, text, images and AI subtitles, and export a compressed "<name>.cut.mp4"), a layered
// canvas for painting and vector drawing, and screen recording and screenshots, all of it also driven by AI over MCP.
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/ui"
)

// version is set by the release build.
var version = "dev"

// window is what the editor needs from the platform's window; each platform provides one.
type window interface {
	ui.Host
	Poll() bool
	Input() ui.Input
	Wait(timeout time.Duration) // a negative timeout waits for input or Wake only
	Size() (int, int)
	Scale() float32
	Present(rgba []byte, width, height int) error
	SetRefresh(func())
	Close()
}

func main() {
	background := flag.Bool("background", false, "open without taking the focus (on a secondary display on Windows), for automated runs")
	capture := flag.String("capture", "", "save the window to this PNG once everything has loaded, then quit")
	deadline := flag.Duration("deadline", 2*time.Minute, "with -capture, give up after this long")
	flag.Parse()
	ui.Version = version
	if err := run(*background, *capture, *deadline, flag.Args()); err != nil {
		alert(err.Error())
		os.Exit(1)
	}
}

func run(background bool, capture string, deadline time.Duration, files []string) error {
	fonts, err := gfx.LoadSystemFonts()
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	// Files opened from Explorer while the app runs become tabs of its window; automated runs keep to themselves.
	interactive := !background && capture == ""
	for i, f := range files {
		if abs, err := filepath.Abs(f); err == nil {
			files[i] = abs
		}
	}
	if interactive && forwardFiles(files) {
		return nil
	}
	w, err := openWindow(ui.ProductName, 1440, 900, background)
	if err != nil {
		return err
	}
	defer w.Close()
	// The data folders under the app's current name, moved from its old one on the first run.
	cacheDir, moves := dataDir(cache)
	stateDir, stateMoves := dataDir(config)
	for old, now := range stateMoves {
		moves[old] = now
	}
	// An edit whose paths could not be rewritten still opens, its moved pictures and music shown as missing, so a
	// failure here does not stop the app.
	_ = rewritePaths(filepath.Join(stateDir, "edits"), moves)
	app := ui.New(w, fonts, cacheDir, stateDir)
	defer app.Close()
	app.OpenFromSystem(files)
	if interactive {
		takeFiles(w)
	}
	canvas := gfx.NewCanvas(1, 1)
	var next time.Duration
	draw := func(in ui.Input) error {
		width, height := w.Size()
		if width == 0 || height == 0 {
			// Minimized or hidden: nothing to draw, but background work and a recording still need looking after.
			next = app.Tick(in)
			return nil
		}
		canvas.Resize(width, height)
		next = app.Frame(in, canvas, w.Scale())
		return w.Present(canvas.Img.Pix, width, height)
	}
	w.SetRefresh(func() {
		in := w.Input()
		draw(ui.Input{X: in.X, Y: in.Y, Down: in.Down})
	})
	started := time.Now()
	settled := time.Time{}
	for w.Poll() {
		if err := draw(w.Input()); err != nil {
			return err
		}
		if capture != "" {
			if app.Idle() {
				if settled.IsZero() {
					settled = time.Now()
				}
				if time.Since(settled) > 700*time.Millisecond {
					return save(capture, canvas)
				}
			} else {
				settled = time.Time{}
			}
			if time.Since(started) > deadline {
				return fmt.Errorf("-capture: still loading after %v", deadline)
			}
			if next <= 0 || next > 50*time.Millisecond {
				next = 50 * time.Millisecond
			}
		}
		// With nothing to animate the window sleeps until input or a Wake from background work: an idle or
		// inactive window costs nothing.
		timeout := time.Duration(-1)
		if next > 0 {
			timeout = next
		}
		w.Wait(timeout)
	}
	return nil
}

func save(path string, c *gfx.Canvas) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, c.Img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
