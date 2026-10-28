<p align="center"><img src="docs/logo.png" alt="Btelo Forge" width="128"></p>

<h1 align="center">Btelo Forge</h1>

<p align="center">A small, fast studio for Windows and macOS: video editing, painting and vector drawing, and screen recording. Native, offline, and scriptable by AI.</p>

<p align="center">
  <a href="https://github.com/btelolabs/btelo-forge/releases/latest"><b>Download</b></a> ·
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

**Screen recording** (Windows)
- Records a screen with the computer's sound and a microphone, the way OBS Studio does: Desktop Duplication hands frames straight to the graphics card's encoder (NVIDIA NVENC, AMD AMF or Media Foundation), so recording a 3440×1440 screen at 60 fps takes about 3% of one processor core.
- Pause and resume, Ctrl+Shift+F9 to stop from anywhere, and the editor window can keep itself out of the picture.
- Recordings are saved in the export folder (or your Videos folder) and open in a tab, ready to cut.
- Screenshots of a screen or a dragged region, and drawing on the screen itself, with or without recording.

**Painting**
- A layered canvas in the manner of Photoshop: brushes and eraser with hardness, flow, smoothing and tip shape; marquee, lasso and magic wand selections; layers with groups, masks, 26 blend modes and lock transparency.
- Free Transform with skew, distort, perspective and warp, crop, image and canvas size, flips and rotations; paint bucket, gradients, shapes, clone stamp and editable text layers.
- Retouching brushes (dodge, burn, sponge, blur, sharpen, smudge), adjustment layers with masks, and layer styles (drop and inner shadow, outer and inner glow, stroke, colour overlay).
- Adjustments (levels, curves, hue/saturation and more) and filters (Gaussian blur, unsharp mask, noise, mosaic) with live previews, and a History panel.
- Vectors in the manner of Affinity Designer: shapes (rectangle, ellipse, polygon, star, line), the pen, a pencil that fits smooth curves, a node tool for nodes and handles, booleans (add, subtract, intersect, exclude), snapping, and fill and stroke settings (width, alignment, caps, joins, dashes). Vectors stay sharp at any zoom and in export.
- An infinite canvas with no size to choose first, and artboards: fixed-size areas exported as pictures of their own.
- Open a screenshot as a canvas, or draw over the video as an overlay: the drawing follows every save of its canvas.

**Compression**
- Presets from *High quality* to *Extreme compression*, plus *Fit a size*, which aims for a file of a chosen size using two-pass encoding.
- Every setting is open: resolution, frame rate, H.264 / H.265 / AV1, five quality steps, encoder speed and audio bitrate.
- NVIDIA hardware encoding where available. Your last settings are remembered.

**Sound and subtitles**
- One search across Kevin MacLeod, Openverse, ccMixter, the Internet Archive and Freesound effects.
- Only licences that allow commercial use are shown, and the attributions they require are written next to each export.
- Speech recognised on your machine with whisper.cpp. Six models, CPU or CUDA. Subtitles land on their own track, ready to edit.

**Beyond the window**
- A built-in MCP server lets Claude, Codex, Cursor or any MCP client edit videos, draw on canvases and record the screen. Every feature has a tool, and each kind of work has its own server and setup to copy, so an AI hears only about the tools it needs.
- Keyboard shortcuts are chosen in Settings for each kind of work on its own: general, video editing, drawing and recording.
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
| Windows 10 / 11, x64 | `btelo-forge-windows-amd64.zip` |
| Windows on ARM | `btelo-forge-windows-arm64.zip` |
| macOS 11+, Apple silicon and Intel | `btelo-forge-macos.zip` |

There is nothing else to install. If ffmpeg is missing or older than 6.0, a static build is downloaded once into the
app's own folder. The macOS app is signed and notarised by Apple. On macOS, AI subtitles use `whisper-cli` from
Homebrew.

Btelo Forge was called Btelo Video Cut. Its settings, unfinished edits and downloads move over the first time Btelo
Forge runs. On macOS the app has a new identity, so it asks again for permission to record the screen.

## Build

Requires Go 1.27. Windows needs nothing else; macOS needs the Xcode command-line tools.

```
go build ./cmd/btelo-forge
go test ./...
```

On Windows, `scripts\dev.ps1` builds the working tree and opens it; `-InstallShortcut` puts a desktop shortcut
that does the same, so a change can be tried with a double-click.

Pure Go on Windows (Win32, no cgo) and Cocoa on macOS. The interface is drawn in software, and ffmpeg does all
media work.

## Licence

[Apache License 2.0](LICENSE). Third-party components keep their own licences; see
[third-party notices](THIRD_PARTY_NOTICES.md).
