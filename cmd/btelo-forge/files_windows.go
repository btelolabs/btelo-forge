package main

import (
	"os"

	"github.com/btelolabs/btelo-forge/internal/ui"
	"github.com/btelolabs/btelo-forge/internal/win"
)

// The asset manager's file operations.

func (h winWindow) SetClipboardFiles(paths []string) error { return h.w.SetClipboardFiles(paths) }
func (h winWindow) Trash(paths []string) error             { return win.Trash(paths) }

// forwardFiles hands files to the app already running, reporting whether one took them.
func forwardFiles(paths []string) bool { return win.ForwardFiles(paths) }

// takeFiles makes w the window later starts of the app hand their files to.
func takeFiles(w window) {
	if h, ok := w.(winWindow); ok {
		h.w.TakeFiles()
	}
}

// The file types opened from Explorer, declared for the current user.

func assocRoot() (win.AssocRoot, error) {
	exe, err := os.Executable()
	if err != nil {
		return win.AssocRoot{}, err
	}
	return win.UserRoot(ui.ProductName, exe), nil
}

func associations(types []ui.FileType) []win.Association {
	out := make([]win.Association, len(types))
	for i, t := range types {
		out[i] = win.Association{ProgID: t.ProgID, Name: t.Name, Exts: t.Exts, Own: t.Own}
	}
	return out
}

func (h winWindow) RegisterFileTypes(types []ui.FileType) error {
	r, err := assocRoot()
	if err != nil {
		return err
	}
	return win.Register(r, associations(types), "Edit videos, draw, record the screen and manage assets")
}

func (h winWindow) UnregisterFileTypes(types []ui.FileType) error {
	r, err := assocRoot()
	if err != nil {
		return err
	}
	return win.Unregister(r, associations(types))
}

func (h winWindow) FileTypeRegistered(t ui.FileType) bool {
	r, err := assocRoot()
	return err == nil && win.Registered(r, associations([]ui.FileType{t})[0])
}

func (h winWindow) FileTypeDefaults(t ui.FileType) int {
	r, err := assocRoot()
	if err != nil {
		return 0
	}
	n := 0
	for _, ext := range t.Exts {
		if win.DefaultFor(r, ext, t.ProgID) {
			n++
		}
	}
	return n
}

func (h winWindow) OpenDefaultApps() error { return win.OpenDefaultApps(ui.ProductName) }
