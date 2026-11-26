package main

import "github.com/btelolabs/btelo-forge/internal/mac"

// The asset manager's file operations, natively through Cocoa: NSPasteboard file URLs and NSFileManager Trash
// (which preserves Finder "Put Back" without requiring Automation permissions or spawning osascript).

func (h macWindow) SetClipboardFiles(paths []string) error { return h.w.SetClipboardFiles(paths) }
func (h macWindow) Trash(paths []string) error             { return mac.Trash(paths) }

// forwardFiles hands files to the app already running; on macOS Launch Services delivers them to the running
// application's NSApplicationDelegate (application:openURLs: / openFiles: -> Input.Opened).
func forwardFiles([]string) bool { return false }

func takeFiles(window) {}
