// Package media runs ffmpeg and ffprobe: probing, single frames, thumbnails, waveforms and decoding streams.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/btelolabs/btelo-forge/internal/proc"
)

// ErrMissingTool means no usable ffmpeg and ffprobe were found or installed.
var ErrMissingTool = errors.New("ffmpeg / ffprobe not found")

var (
	chosen     atomic.Pointer[Tools] // set by Use once the app has found or installed ffmpeg
	searchOnce sync.Once
	searched   map[string]string
)

// Use makes every command run the given programs.
func Use(t Tools) { chosen.Store(&t) }

// tool is the path of ffmpeg or ffprobe: the pair given to Use, or else whatever is next to the program or on PATH.
func tool(name string) (string, error) {
	if t := chosen.Load(); t != nil {
		if name == "ffprobe" {
			return t.FFprobe, nil
		}
		return t.FFmpeg, nil
	}
	searchOnce.Do(func() {
		searched = map[string]string{}
		for _, t := range candidates(&ToolsConfig{}, "") {
			if fileExists(t.FFmpeg) && fileExists(t.FFprobe) {
				searched["ffmpeg"], searched["ffprobe"] = t.FFmpeg, t.FFprobe
				return
			}
		}
	})
	if p, ok := searched[name]; ok {
		return p, nil
	}
	return "", ErrMissingTool
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
