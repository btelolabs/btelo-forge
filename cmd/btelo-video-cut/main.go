// Command btelo-video-cut is a desktop video editor: drop videos in, cut, speed up, stack tracks, add music, text,
// images and AI subtitles, and export a compressed "<name>.cut.mp4".
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/media"
	"github.com/btelolabs/btelo-video-cut/internal/ui"
)

// version is set by the release build.
var version = "dev"

// window is what the editor needs from the platform's window; each platform provides one.
type window interface {
	ui.Host
	Poll() bool
	Input() ui.Input
	Wait(timeout time.Duration)
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
	w, err := openWindow("btelo Cut", 1440, 900, background)
	if err != nil {
		return err
	}
	defer w.Close()
	app := ui.New(w, fonts, filepath.Join(cache, "btelo-video-cut"), filepath.Join(config, "btelo-video-cut"))
	defer app.Close()
	if err := media.Available(); err != nil {
		app.Notice(err.Error())
	}
	for _, f := range files {
		app.Open(f)
	}
	canvas := gfx.NewCanvas(1, 1)
	var next time.Duration
	draw := func(in ui.Input) error {
		width, height := w.Size()
		if width == 0 || height == 0 {
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
		timeout := time.Second
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
