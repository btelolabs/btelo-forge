package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The asset manager's file operations, through the Finder: it moves files to the Trash as the user would (so "Put
// Back" works) and puts files on the pasteboard as Finder's Copy does.

// appleList writes paths as an AppleScript list of POSIX files.
func appleList(paths []string) (string, error) {
	items := make([]string, len(paths))
	for i, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		quoted := strings.NewReplacer(`\`, `\`, `"`, `\"`).Replace(abs)
		items[i] = `POSIX file "` + quoted + `"`
	}
	return "{" + strings.Join(items, ", ") + "}", nil
}

func osascript(script string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (h macWindow) SetClipboardFiles(paths []string) error {
	if len(paths) == 0 {
		return errors.New("no files to copy")
	}
	list, err := appleList(paths)
	if err != nil {
		return err
	}
	return osascript("set the clipboard to " + list)
}

func (h macWindow) Trash(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	list, err := appleList(paths)
	if err != nil {
		return err
	}
	return osascript(`tell application "Finder" to delete ` + list)
}

// forwardFiles hands files to the app already running; on macOS the system does that itself, through the
// running application, so there is nothing to hand.
func forwardFiles([]string) bool { return false }

func takeFiles(window) {}
