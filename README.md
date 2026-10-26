<p align="center"><img src="docs/logo.png" alt="Btelo Video Cut" width="128"></p>

<h1 align="center">Btelo Video Cut</h1>

<p align="center">A small, fast video editor for Windows and macOS. Native, offline, and scriptable by AI.</p>

<p align="center">
  <a href="https://github.com/btelolabs/btelo-video-cut/releases/latest"><b>Download</b></a> ·
  <b>English</b> · <a href="README.zh-CN.md">中文</a>
</p>

<p align="center"><img src="docs/screenshots/en-editor.jpg" alt="The editor: preview, overlays and a multi-track timeline"></p>

## Features

**Editing**
- Tabs for separate edits; drop more videos to extend the main track or stack them on upper tracks.
- Multi-track timeline for video, overlays (text and images) and music. Upper tracks cover lower ones.
- Split, trim and per-clip speed from 0.25× to 4×, with pitch-preserved sound.
- Per-clip and per-track volume. Opacity for clips and overlays.
- Box-select or Ctrl-click several items, then delete them or set their volume and opacity together.
- Preview and export use the same filter graph, so the export matches what you saw.

**Compression**
- Presets from *High quality* to *Extreme compression*, plus *Fit a size*, which aims for a file of a chosen size using two-pass encoding.
- Every setting is open: resolution, frame rate, H.264 / H.265 / AV1, five quality steps, encoder speed and audio bitrate.
- NVIDIA hardware encoding where available. Your last settings are remembered.

**Sound and subtitles**
- One search across Kevin MacLeod, Openverse, ccMixter, the Internet Archive and Freesound effects.
- Only licences that allow commercial use are shown, and the attributions they require are written next to each export.
- Speech recognised on your machine with whisper.cpp. Six models, CPU or CUDA. Subtitles land on their own track, ready to edit.

**Beyond the window**
- A built-in MCP server lets Claude, Codex, Cursor or any MCP client open, cut, caption, score and export videos. Every feature has a tool.
- Your phone can play and download exports, and upload clips back to the computer, over the local network with a private link.
- Music sites, speech models and the ffmpeg download source are plain JSON, editable in Settings or by an AI.

<table>
  <tr>
    <td><img src="docs/screenshots/en-select.jpg" alt="Several items selected and edited together"></td>
    <td><img src="docs/screenshots/en-export.jpg" alt="Export presets"></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/en-music.jpg" alt="Free music search"></td>
    <td><img src="docs/screenshots/en-ai.jpg" alt="MCP settings for AI tools"></td>
  </tr>
</table>

## Download

| System | File |
| --- | --- |
| Windows 10 / 11, x64 | `btelo-cut-windows-amd64.zip` |
| Windows on ARM | `btelo-cut-windows-arm64.zip` |
| macOS 11+, Apple silicon and Intel | `btelo-cut-macos.zip` |

There is nothing else to install. If ffmpeg is missing or older than 6.0, a static build is downloaded once into the
app's own folder. The macOS app is signed and notarised by Apple. On macOS, AI subtitles use `whisper-cli` from
Homebrew.

## Build

Requires Go 1.27. Windows needs nothing else; macOS needs the Xcode command-line tools.

```
go build ./cmd/btelo-video-cut
go test ./...
```

Pure Go on Windows (Win32, no cgo) and Cocoa on macOS. The interface is drawn in software, and ffmpeg does all
media work.

## Licence

[Apache License 2.0](LICENSE). Third-party components keep their own licences; see
[third-party notices](THIRD_PARTY_NOTICES.md).
