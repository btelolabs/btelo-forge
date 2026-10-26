<p align="center"><img src="docs/logo.jpg" alt="btelo Cut" width="160"></p>

<h1 align="center">btelo Cut</h1>

<p align="center">A fast, simple desktop video editor for Windows and macOS — and one that AI can drive.</p>

<p align="center"><b>English</b> · <a href="README.zh-CN.md">中文</a></p>

---

## Download

Get the latest build from **[Releases](https://github.com/btelolabs/btelo-video-cut/releases/latest)**:

| System | File |
| --- | --- |
| Windows 10 / 11 (x64) | `btelo-cut-windows-amd64.zip` |
| Windows on ARM | `btelo-cut-windows-arm64.zip` |
| macOS 11 or later (Apple silicon and Intel) | `btelo-cut-macos.zip` |

btelo Cut uses **ffmpeg** for all decoding and encoding. Install it once:

- Windows: `winget install ffmpeg` (or put `ffmpeg.exe` and `ffprobe.exe` next to `btelo-video-cut.exe`)
- macOS: `brew install ffmpeg`

On macOS the app is not notarised yet: the first time, right-click **btelo Cut.app** and choose **Open**.

## What it does

- **Tabs** like a browser — each tab is its own edit. `Ctrl+T` opens one, drop a video in to start.
- **Several videos in one edit** — drop more videos on the window and they join the end of the main track. Drop one
  over an upper video track and it is laid on top of the picture from the playhead.
- **Multi-track timeline** — video, overlay (text and images) and music tracks, as many of each as you like, with
  overlays above the video and music below. Drag clips between tracks, drag their ends to trim, scroll and zoom.
  Upper video tracks cover the ones below.
- **Cut and speed** — split at the playhead (`S`), delete, trim, and 0.5×–4× speed per clip with pitch-preserved sound.
- **Volume everywhere** — every clip has its own volume; select a track's header to set the whole track's volume or
  mute it.
- **Text and images** — add text (`T`) or images (drop them, or paste with `Ctrl+V`), set when each shows, drag it in
  the picture, change size, colour, outline and background.
- **Free music, aggregated** — search Kevin MacLeod's library (much of it also in the YouTube Audio Library),
  Openverse, ccMixter and the Internet Archive in one go, plus Freesound effects. Only licences that allow
  commercial use are shown. Preview with one click (it streams while it downloads), downloads are cached, and the
  credits each track needs are written next to your export.
- **AI subtitles** — recognises speech on your computer with whisper.cpp; pick from six models, from Tiny (75 MB) to
  Turbo (1.6 GB), and run on the CPU or an NVIDIA GPU. Subtitles arrive on their own overlay track, ready to edit.
- **Export and compress** — original, 1080p, 720p or 480p; high quality, balanced or small file; H.264 or H.265.
  Exports are saved next to the original as `name.cut.mp4` (or in a default folder you choose), and your last
  settings are remembered.
- **Undo and autosave** — every change can be undone; unfinished edits come back when you open the same video again.
- **Extensible with JSON** — the music sites and the speech engines, models and languages are JSON files you can
  change in **Settings → Extensions** (or have an AI change over MCP), so new sites, models and engine versions need
  no new release.
- **English and Chinese** interface, switchable in Settings.

## Let AI edit for you (MCP)

btelo Cut runs a local [Model Context Protocol](https://modelcontextprotocol.io) server, so Claude, Codex, Cursor or
any MCP client can open videos, cut, add text, images, music and subtitles, look at frames of the result and export.

Open **Settings → AI (MCP)** and press **Copy the setup for your AI**, then paste it to your AI — it contains the
address and everything the AI needs to know. For Claude Code that is:

```
claude mcp add --transport http btelo-cut http://127.0.0.1:38471/mcp
```

The server only listens on this computer. Tools: `get_state`, `open_video`, `split`, `delete_item`, `move_item`,
`trim_item`, `set_clip`, `set_track`, `add_text`, `add_image`, `update_overlay`, `add_music_file`, `search_music`,
`add_library_music`, `generate_subtitles`, `export`, `frame_at`, `screenshot`, `undo`, `get_config`, `set_config`,
`reset_config` and more.

## Extend it with JSON

Two JSON files decide what btelo Cut can reach. Edit them in **Settings → Extensions** — saving checks the file
first and applies it at once — or ask your AI to do it with `get_config` / `set_config`:

- **Music sources** — every site the music panel searches: its search address and parameters, where the results
  are in its JSON answer, and templates that turn a result into a track (`{title}`, `{files.0.url|path}`,
  `{tags|split:,|take:2|join: · }` …). Sites that list everything in one file (like Kevin MacLeod's) are searched on
  your computer; sites that need two requests (like the Internet Archive) are supported too.
- **Subtitle models** — the whisper.cpp engines (a zip to download, or a program already installed), the models
  (any ggml file, with an optional SHA-256 checksum), the spoken languages and the engine's command line. Point an
  address at a newer whisper.cpp release and it is downloaded into its own folder.

**Copy guide** next to the editor copies the full description of the format. The built-in files are in
[`internal/library/sources.json`](internal/library/sources.json) and
[`internal/transcribe/models.json`](internal/transcribe/models.json).

## Download exports on your phone

In **Settings → Phone**, turn on sharing and scan the QR code with your phone (same Wi-Fi). The page lists every
video you exported so you can play or download it. The address carries a private key, so only people you give it to
can open it.

## Shortcuts

| Key | Action |
| --- | --- |
| Space | Play / pause |
| S | Split at the playhead |
| Delete | Delete the selected clip, music or overlay |
| T | Add text at the playhead |
| Ctrl+V | Paste an image, files or text |
| ← / → | One frame back / forward (Shift: one second) |
| Ctrl+Z / Ctrl+Y | Undo / redo |
| Ctrl+E | Export |
| Ctrl+T / Ctrl+W | New tab / close tab |
| Ctrl+O | Open videos |
| Wheel / Shift+wheel / Ctrl+wheel | Scroll tracks / scroll time / zoom the timeline |

On macOS use ⌘ instead of Ctrl.

## About the YouTube Audio Library

YouTube's Audio Library has no public interface — it needs a YouTube Studio sign-in, and its old public download
links no longer work — so btelo Cut searches libraries that allow direct downloads. To use a track from the YouTube
Audio Library, download it in YouTube Studio and drop the file on the window (or import it under **My files**).

## Build from source

Go 1.27 or later. Windows needs nothing else; macOS needs the Xcode command-line tools.

```
go build -ldflags "-H windowsgui" -o btelo-video-cut.exe ./cmd/btelo-video-cut   # Windows
CGO_ENABLED=1 go build -o btelo-video-cut ./cmd/btelo-video-cut                  # macOS
go test ./...
```

| Folder | What is in it |
| --- | --- |
| `cmd/btelo-video-cut` | Entry point and the per-platform window glue |
| `internal/win`, `internal/mac` | Native windows: Win32 (no cgo) and Cocoa |
| `internal/ui` | The editor: tabs, timeline, panels, settings, MCP tools |
| `internal/project` | The edit model: sources, tracks, clips, overlays, music, undo, autosave |
| `internal/render` | ffmpeg filter graphs shared by preview and export |
| `internal/playback`, `internal/wave` | Preview playback timed by the sound card |
| `internal/library` | Music libraries, search, downloads and cache |
| `internal/transcribe` | whisper.cpp subtitles |
| `internal/web` | The phone download page |
| `internal/gfx`, `internal/caption` | Software rendering and the text/image overlays |

## Licence

btelo Cut is **free to use**, including for commercial videos, and its source is open to read and change for your own
use. **Redistributing or selling it is not allowed.** See [LICENSE](LICENSE). Third-party components keep their own
licences — see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
