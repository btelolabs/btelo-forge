# btelo-forge (Btelo Forge) — notes for AI assistants

- Pure Go desktop app: Win32 without cgo (`internal/win`), Cocoa through cgo on macOS (`internal/mac`). No web views,
  no Wails. The UI is immediate-mode, drawn in software (`internal/ui`, `internal/gfx`).
- ffmpeg does all media work; preview and export share one filter graph (`internal/render`), so what is shown is
  what is exported.
- Music sites, speech engines/models and streaming platforms are JSON (`internal/library/sources.json`,
  `internal/transcribe/models.json`, `internal/capture/platforms.json`), editable in Settings → Extensions and
  through the MCP tools `get_config` / `set_config` / `reset_config`. Add a site, model or platform by changing the
  JSON, not the Go code; extend the template filters or fields only when JSON cannot say it, and keep `FormatGuide`
  (`PlatformsGuide`) in step.
- AI models (`internal/ai`): `ai.Runtime` is the one place that downloads, starts, measures and stops models (an engine
  program per local model, started with `proc.Start` and measured with `proc.Meter`); anything that needs a model asks it
  (`Ensure`, `Endpoint`) and the voice-over engine is listed beside the chat models through `Track`. The chat window and
  Settings → AI models (`internal/ui/ai*.go`) are views of it. Every model has a `Kind` (`ai.Kinds`: llm, image, voice,
  transcribe, model3d, video), which lists and the picker group by; what a kind makes is saved by `ai.SaveAsset` into its
  folder of the assets folder (`ai-image`, `ai-voice`, `ai-3dmodel`, `ai-video`; `aiAssetRoot`) and a message refers to
  it by path (`ai.Message.Media`), never by bytes. A new kind needs its API implemented before the config may name it
  (`onlineKinds`, the local check in `Config.check`). Every model program is started with `proc.StartAs(label, …)` and
  written in `running.json` (`proc.SetRegistry`) while it runs, so the next run lists what a crash left behind
  (`proc.Orphans`, shown by `aiRunning`: a card in the corner, in the chat's own list while the chat is in front, and in
  Settings) and can end it; the chat's header says Running with a Stop button, Unload lives in Settings, and closing the
  chat tab or the app stops what it started. Models, engines and online services are the JSON
  `ai_models`; API keys are never in it but in `ai-keys.json`, sealed with `vault`. Tests use the test binary itself as a
  fake engine (`TestMain` in `internal/ai`); `TestRealLlamaServer` and `TestRealLocalModelInTheChat` run a real
  llama-server and model when `FORGE_LLAMA_SERVER` and `FORGE_LLAMA_MODEL` are set.
  What takes minutes is a task (`internal/ai/tasks.go`, `Endpoint.RunTask`): submitted, asked after and fetched, one `taskAPI` per
  way of working (`TaskMiniMax`, `TaskTripo`, `TaskMeshy`, `TaskSDVideo`, sd-server's own job interface); a video or 3D model of a
  service names its `task` in the JSON, and a local video model is `api: sd-server` with `kind: video`. MiniMax H3 needs `--max-vram`: without it the engine fills the card and a step of 864x480 takes 130 s instead of 12 (`TestRealMiniMaxH3InTheChat`); its sound arrives as PCM, which `media.OpusWebM` turns into what a .webm may hold. Stopping a task tells the
  service where it can be told. A picture attached to an image model is redrawn (`Endpoint.Redraw`), and a local model gets only the
  newest messages that fit what it remembers (`ai.Fit`, kept on the answer as `LeftOut`). Online model names go out of date: check
  the service's documentation, as a name in `models.json` was never tried with a real key.
- Mail (`internal/mail`, `internal/ui/mail*.go`): several mailboxes in one SQLite database in the Intelligence folder (`mail/mail.db`), so
  the combined inbox and a choice of mailboxes are one query. It speaks IMAP with an app password, as OAuth for Gmail would need
  Google's review for every user. Microsoft takes no password, so Outlook cannot be bound (`mailRefused`). Mailboxes are `mailProviders`, the most used in the US first. There is deliberately no way to send or delete: no SMTP, no `\Deleted`, no `EXPUNGE`, no `COPY`
  (`TestNothingCanSendOrDelete`), and a reply is saved with `APPEND` to the drafts. A change on a server starts only from a click, goes
  in the log and can be undone. A model's answer is read strictly (`ParseVerdict`), as a stranger's mail steered it; only models of this
  computer read mail, through `ai.Runtime`. Tests use the IMAP server in `internal/mail/mailtest`.
- Work in the background shows as cards in the window's corner (`tasks.go`); models that stay in memory are behind a small liquid orb there, and open as a list under the pointer (`tasks_orb.go`). The orb's frame rate is low on purpose, as it redraws the whole window.
- A pinned tab opens again with the app (`tab_pins.go`): a new kind of tab needs its line in `tabSpec` and `restoreTab`.
- The user's work is kept in the resource folder (`resourceDir`, `~/Btelo Forge` unless chosen in Settings → General), in
  one folder per start page group: `Tools`, `Creative`, `Documents`, `Intelligence`, in English in every language. A tool
  saves a new file with `categoryDir`; never in the system's Videos, Pictures or Documents.
- A tool is a card on the start page that opens in a tab of its own, as the resource manager, the live stream and the
  keyboard tool do; there is no entry for it in Settings, which holds preferences only.
- Files are opened and saved through the file picker (`internal/ui/file_pick.go`: `pickFiles`, `pickDir`, `pickSaveAs`), the
  resource manager's own list in a dialog; only its button reaches the system's dialogs (`TestOnlyThePickerOpensSystemDialogs`).
- System features go behind an adapter, one implementation per system: see `keyboard.Adapter` and `keyboard.SystemMap`
  (`internal/keyboard`). The keyboard hook is opt-in, turned on only by the user's click in the Keyboard tab: neither a
  default, a configuration nor an MCP tool may start it, and only the user applies the remaps (the registry on Windows, hidutil on macOS).
- Every interface string goes through `u.t` / `u.tf` / `msgs` and needs a Chinese entry in `i18n_zh.go`
  (`TestEveryMessageIsTranslated`). Every MCP tool is listed under its domain in `mcpDomainTools`
  (`TestMCPToolsAreAllListed`), which decides which server (`/mcp/video`, `/mcp/drawing`, `/mcp/mind`, `/mcp/recording`, `/mcp/office`) offers it.
- Checks before handing work over: `gofmt -l .` empty, `go vet ./...`, `go test ./...` (needs ffmpeg on PATH). UI
  screens render with `FORGE_SHOTS=<folder> go test ./internal/ui -run TestScreens`; a real window with
  `btelo-forge.exe -background -capture out.png`. The README's file manager animation is recorded
  by `FORGE_DEMO=<folder> go test ./internal/ui -run TestFilesPieDemo` (copy the GIFs into `docs/screenshots`).
- Office documents (`internal/office`, `internal/ui/office*.go`): .docx, .xlsx, .pptx and PDF are edited in the file's own
  XML or objects, so what the app does not understand survives a save (`ox`, `opc`); never rebuild a file from a simpler
  model. Children go in the schema's order (`ox.Node.Ensure`), as Office refuses a file otherwise. A format registers itself
  with `registerOffice`, and its Chinese strings are in `i18n_zh_<format>.go`. `tools/officecheck` holds Python scripts that
  check the files with independent libraries; no test needs them. Charts are one package, `internal/office/chart`: it reads
  a chart part (with `Source.Range` the workbook's cells, otherwise the part's own copy of them) and draws on a `gfx.Canvas`, so
  Word, Excel, PowerPoint and paper share it; the part is never written. Theme colours are `internal/office/dml`. CSV and TSV
  are `xlsx.OpenDelimited`: only plain numbers become numbers, and a save keeps the file's encoding, separator and line ends.
- Photoshop files (`internal/psd`, which knows nothing of the canvas; `paint.FromPSD`, `Doc.ToPSD`): what a file or canvas cannot carry across is counted in `paint.PSDLoss` and told to the person, never dropped silently. `tools/officecheck` is not used; check a change against `psd-tools` in a throwaway venv.
- Filters that have one (`Filter.card`) run on a graphics card when `paint.SetAccelerator` has one and it is free (`internal/gpufx`, WGSL that repeats the Go steps; `TestTheCardAndTheProcessorAgree` holds them to three levels), and on the processor otherwise: never wait for the card, as the 3D tool holds `modelCardTurn` while it is open.
- Smart filters (`paint.FilterStack`) belong to smart objects only and are worked out tile by tile, kept until `treeState` changes.
- Select Subject and Remove Background ask a model of kind `matting`, whose engine is this program in `-matting-server` mode (`internal/matting`, ONNX Runtime without cgo; the function table places come from `onnxruntime_c_api.h`, so update them with the runtime in `models.json`). `TestRealModel` runs it with `FORGE_ORT` and `FORGE_MATTING_MODEL` set. Windows only for now.
- The 3D tool (`internal/scene3d`, `internal/render3d`, `internal/ui/model*.go`): an asset is data shaped after glTF, drawn
  through the adapter `gpu.Device` (`internal/gpu`, implemented on WebGPU in `internal/gpu/wgpu`), so a Vulkan implementation
  can sit beside it; shaders are WGSL. Windows uses the library's Vulkan side, as its DirectX 12 side refuses the shaders.
  Modelling works on `scene3d.EditMesh`, polygons with vertices welded and seams kept in the corners; a change is one undo step of
  a whole copy, and `Bake` turns it back into parts, keeping the file's normals for what did not move.
  FBX (`scene3d/fbx*.go`, read and written in pure Go) is checked against Blender in both directions, as writing and reading
  it here alone hides a wrong reading of the format: a cluster's `Transform` is the mesh seen from the bone, not the mesh's place.
  Undo in a 3D tab has two levels: `modelEdit.undo` while modelling, and `modelDoc.undo` (whole `modelShot`s; lists are replaced, not
  changed in place, as the card's goroutine reads them through `cardAsset`). Modifiers apply on leaving the mesh, as no file holds them.
  One graphics device stays open for the process (`gpuwgpu.Shared`): the library does not survive closing one and opening another.
  Skies are JSON (`internal/render3d/skies.json`). Tests without a graphics card skip what needs one; a model is looked at with
  `FORGE_MODEL=<file> FORGE_SHOTS=<folder> go test ./internal/ui -run TestRealCardShowsAModel`.
- Tests never reach the internet: point the sources at an `httptest` server (see `sourcesAt` in the tests).
- Every file of the app's own formats (.btcanvas, .btmind, .btcut) ends with a small JPEG and a footer (`internal/thumb`), made
  when it is saved, so the file manager and Explorer read a few kilobytes instead of opening it: a canvas holds it as its
  zip's comment, a map after its deflated body, an edit after its JSON (`thumb.Strip`). Explorer gets it through
  `packaging/windows/thumbnailer`, the one piece of C on Windows: run its `build.ps1` after changing it and commit the DLLs
  in `internal/win`, which the program embeds and registers with the file types. Finder has no such piece yet.
- Printing goes through `internal/printing`: a display list of pages in points (paths, text as outlines, pictures,
  gradients), read by the preview, the PDF writer and the GDI backend alike. A format adds a `printSource` in
  `internal/ui` (`print_sources.go`, `office_sheet_print.go`). `gfx.NewRecorder` makes a canvas that records shapes, not
  pixels: code that touches `c.Img` directly must ask `c.Recorder()` first. Real jobs are tested only against the
  Microsoft Print to PDF queue, with `Target.Output` set; never send a test to a physical printer. `tools/officecheck/print_check.py`
  counts images and paths in an exported PDF.
- Commands given to the user are PowerShell.
- The app was called Btelo Video Cut: `cmd/btelo-forge/migrate.go` moves its old data folders on the first run.
  The icons are drawn by `packaging/make_icon.py` (an anvil and a spark, flat, like the start page's card symbols);
  regenerate the Windows resources with the `go-winres simply` command in that script after changing them.
