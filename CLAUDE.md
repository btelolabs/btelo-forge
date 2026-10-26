# btelo-video-cut — notes for AI assistants

- Pure Go desktop app: Win32 without cgo (`internal/win`), Cocoa through cgo on macOS (`internal/mac`). No web views,
  no Wails. The UI is immediate-mode, drawn in software (`internal/ui`, `internal/gfx`).
- ffmpeg does all media work; preview and export share one filter graph (`internal/render`), so what is shown is
  what is exported.
- Music sites and speech engines/models are JSON (`internal/library/sources.json`, `internal/transcribe/models.json`),
  editable in Settings → Extensions and through the MCP tools `get_config` / `set_config` / `reset_config`. Add a site
  or model by changing the JSON, not the Go code; extend the template filters or fields only when JSON cannot say it,
  and keep `FormatGuide` in step.
- Every interface string goes through `u.t` / `u.tf` / `msgs` and needs a Chinese entry in `i18n_zh.go`
  (`TestEveryMessageIsTranslated`). Every MCP tool is listed in `mcpToolNames` (`TestMCPToolsAreAllListed`).
- Checks before handing work over: `gofmt -l .` empty, `go vet ./...`, `go test ./...` (needs ffmpeg on PATH). UI
  screens render with `BVC_SHOTS=<folder> go test ./internal/ui -run TestScreens`; a real window with
  `btelo-video-cut.exe -background -capture out.png`.
- Tests never reach the internet: point the sources at an `httptest` server (see `sourcesAt` in the tests).
- Commands given to the user are PowerShell.
