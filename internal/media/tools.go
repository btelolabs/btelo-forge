// Package media runs ffmpeg and ffprobe: probing, single frames, thumbnails, waveforms and decoding streams.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/btelolabs/btelo-video-cut/internal/proc"
)

// ErrMissingTool means ffmpeg or ffprobe is neither next to the program nor on PATH.
var ErrMissingTool = errors.New("ffmpeg / ffprobe not found: put them next to the program, or install them (winget install ffmpeg, or brew install ffmpeg on a Mac)")

var (
	toolOnce  sync.Once
	toolPaths map[string]string
)

func tool(name string) (string, error) {
	toolOnce.Do(func() {
		toolPaths = map[string]string{}
		dir := ""
		if exe, err := os.Executable(); err == nil {
			dir = filepath.Dir(exe)
		}
		for _, n := range []string{"ffmpeg", "ffprobe"} {
			if dir != "" {
				if p := filepath.Join(dir, n+exeSuffix()); fileExists(p) {
					toolPaths[n] = p
					continue
				}
			}
			if p, err := exec.LookPath(n); err == nil {
				toolPaths[n] = p
				continue
			}
			// Apps started from Finder do not get the shell's PATH, so look where Homebrew installs too.
			for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
				if p := filepath.Join(dir, n); runtime.GOOS == "darwin" && fileExists(p) {
					toolPaths[n] = p
					break
				}
			}
		}
	})
	if p, ok := toolPaths[name]; ok {
		return p, nil
	}
	return "", ErrMissingTool
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Command builds a hidden-window ffmpeg or ffprobe process.
func Command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	path, err := tool(name)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	proc.Hide(cmd)
	return cmd, nil
}

// Available reports whether both tools were found.
func Available() error {
	if _, err := tool("ffmpeg"); err != nil {
		return err
	}
	_, err := tool("ffprobe")
	return err
}

// run executes a tool and returns stdout, folding the tail of stderr into the error.
func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd, err := Command(ctx, name, args...)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, tail(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// tail keeps the last few lines of a tool's stderr for error messages.
func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.TrimSpace(strings.Join(lines, " | "))
}
