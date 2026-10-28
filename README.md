<p align="center"><img src="docs/logo.png" alt="Btelo Forge" width="128"></p>

<h1 align="center">Btelo Forge</h1>

<p align="center">I put the programs of my own daily work into one small native app — even the ones I used Photoshop,
CapCut and OBS Studio for — and what one of them makes, the others can hold as a smart object that follows every
change.<br>
Free, open source, offline, and driven by AI over MCP.</p>

<p align="center">
  <a href="https://github.com/btelolabs/btelo-forge/releases/latest"><b>Download</b></a> ·
  <b>English</b> · <a href="README.zh-CN.md">中文</a>
</p>

> [!NOTE]
> **Windows is where Btelo Forge is made and checked. The macOS version is experimental:** it is tested far less,
> and screen recording, live streaming, the camera on the screen, screenshots and drawing on the screen are not there
> yet; stream keys are not kept encrypted.

<p align="center"><img src="docs/demos/linked.webp" alt="A poster canvas shown on a mind map: a badge drawn on the canvas appears on the map at once"></p>

A canvas shown on a mind map, drawn over a video or pasted into another canvas stays linked to it: draw on the canvas
and every place that shows it follows. Every animation on this page was recorded by Btelo Forge itself while an AI
drove it over MCP, then cut, sped up and exported as WebP by Btelo Forge.

## Video editing

<p align="center"><img src="docs/demos/video.webp" alt="Two clips joined, one cut and sped up, music, a caption and a logo added on the timeline"></p>

- Tabs for separate edits; drop more videos to extend the main track or stack them on upper tracks.
- A multi-track timeline for video, overlays (text, images and drawings) and music; upper tracks cover lower ones.
- Split, trim and per-clip speed from 0.25× to 4×, with the sound's pitch kept.
- Volume per clip and per track, opacity for clips and overlays; box-select or Ctrl-click several items and change
  them together.
- Export presets from *High quality* to *Extreme compression*, or *Fit a size*, which aims for a file of a chosen
  size with two-pass encoding. Every setting is open: resolution, frame rate, H.264 / H.265 / AV1, quality, encoder
  speed and audio bitrate, with NVIDIA hardware encoding where there is one.
- Music from one search across Kevin MacLeod, Openverse, ccMixter, the Internet Archive and Freesound effects —
  only licences that allow commercial use, with the credits they ask for written next to each export.
- Subtitles from speech recognised on your own machine with whisper.cpp: six models, CPU or CUDA, on a track of
  their own, ready to edit.
- Preview and export share one filter graph, so the export is what you saw.

<table>
  <tr>
    <td><img src="docs/screenshots/en-select.jpg" alt="Several items selected and edited together"></td>
    <td><img src="docs/screenshots/en-export.jpg" alt="Export presets"></td>
  </tr>
</table>

## Painting and drawing

<p align="center"><img src="docs/demos/paint.webp" alt="A poster built up: a gradient, a glowing sun, vector hills, ink strokes, a star, styled text and a hue/saturation adjustment layer"></p>

**Pixels, as in Photoshop**
- Brushes and an eraser with hardness, flow, smoothing, tip shape and shape dynamics (size and angle jitter,
  scatter, a tip that turns with the stroke); twelve brush presets from pencil, charcoal and ink to bristle brushes,
  sponge and spatter, and tips made from your own pictures; the clone stamp; dodge, burn, sponge, blur, sharpen and
  smudge.
- Marquee, lasso and magic wand selections; paint bucket and gradients.
- Layers and groups with masks, 26 blend modes and locked transparency; adjustment layers that change what is under
  them without touching its pixels; layer styles — drop and inner shadow, outer and inner glow, stroke, colour
  overlay — that follow every change to the layer.
- Levels, curves, hue/saturation and more, and filters (Gaussian blur, unsharp mask, noise, mosaic), all with live
  previews; Free Transform with skew, distort, perspective and warp; a History panel.

**Vectors, as in Affinity Designer**
- Shapes (rectangle, ellipse, polygon, star, line), the pen, and a pencil that turns a freehand line into a few
  smooth curves.
- A node tool that chooses whole shapes and, after a double-click, their nodes and handles; Ctrl+T transforms just
  the chosen nodes or shapes; snapping to nodes, edges and centres.
- Combine shapes as in Photoshop — unite, subtract front, intersect, exclude — from the tools' bars with several
  vector layers chosen: the result stays true vector, every shape still editable, and combining is instant however
  large the layers are; Merge shape components bakes them into one outline.
- Fill and stroke settings — width, alignment, caps, joins, dashes. Vectors stay sharp at any zoom and in export.

**The canvas**
- Infinite: start drawing without choosing a size. Add artboards for fixed-size pictures, each exported on its own.
- Slices: K marks the chosen layers, groups and artboards, adding .png to their names, and File › Export slices
  writes each as a PNG beside the canvas file. Names follow the Krita named-assets rules — `assets/fish.png` into a
  folder, `fish_300x200.png` at a fixed size, a `200x300/assets` group handing its folder and size to what is in it —
  and the ⓘ by each slice says what it will export and why.
- Open a screenshot as a canvas, or draw over a video as an overlay that follows every save of its canvas.
- A canvas file keeps everything — layers, vectors, text, masks, effects, adjustment layers and artboards.

<table>
  <tr>
    <td><img src="docs/screenshots/en-canvas.jpg" alt="A poster on an artboard: pixel, vector, text and adjustment layers, with the node tool on a curve"></td>
    <td><img src="docs/screenshots/en-style.jpg" alt="The Layer Style dialog"></td>
  </tr>
</table>

## Mind maps and flowcharts

<p align="center"><img src="docs/demos/mind.webp" alt="A mind map grown branch by branch, with a budget table whose total follows its cells and a linked poster canvas"></p>

- Mind maps and flowcharts on one infinite board, as XMind and Whimsical together: a tree's topic can have connectors,
  and a flowchart's box can grow a tree. Trees lay out as mind maps, logic charts, org charts or outlines.
- Everything on the board is a node: topics and flowchart shapes, free text, tables with formulas (=SUM(B2:B9)),
  pictures and animated GIFs, videos that play where they are, smart objects from your canvases, which follow every
  change you draw, and other mind maps shown inside — a map that ends up inside itself shows a warning instead.
- Keys as in XMind: Tab adds a child, Enter a sibling, typing replaces a topic's words, arrows move between topics.
  Drag a topic onto another to move it there, or out onto the board to free it; drag from the dots at a box's sides
  to draw a connector, or onto empty board for a new connected box.
- Built for hundreds of thousands of nodes: the board is drawn from tiles on every core, only what is in view, with
  an overview of the whole board. Files are small: pictures and videos are referred to, never copied.
- Connectors find their own way round the nodes between their ends; drag a connector's middle to shape its route.
- Turn a branch into a task list: tick its topics off, and every level shows how much of what is under it is done.
- Six themes, from MindNode's dark look to white, pastel bubbles, ocean blue, sepia and neon, a click apart.
- Mind maps show in the asset manager with a picture of the whole map.

## Files

<p align="center"><img src="docs/demos/assets.webp" alt="Pictures, videos, canvases and mind maps as a grid: moved through with the arrows, previewed with their tags, searched by tag, shown as a list"></p>

- Folder lists side by side, driven as gdu and vim are: hjkl or the arrows move, e opens another list beside. Each
  list shows its folder as rows, or as a grid of pictures filtered by kind, category or tag.
- i analyses what takes the space, as gdu does: the list is sorted by size with a bar for each item, and a pie of
  the folder sits under it. Move down the list with the keys or the mouse and the pie turns the item under the
  cursor to twelve o'clock, on a spring; click a wedge to go to its item.
- Copies, moves and deletions run in the background with their progress shown; deleted items go to the Recycle Bin,
  and u brings them back.

<p align="center"><img src="docs/screenshots/en-files-analyse.gif" alt="A folder analysed: the pie under the list turns to the item under the cursor"></p>

## Screen recording (Windows)

<p align="center"><img src="docs/demos/record.webp" alt="The recording dialog: 60 frames a second and a region picked, a canvas drawn on while it records, a pause, then the recording opened in a tab to cut"></p>

- Records a screen with the computer's sound and a microphone, the way OBS Studio does: Desktop Duplication hands
  frames straight to the graphics card's encoder (NVIDIA NVENC, AMD AMF or Media Foundation), so recording a
  3440×1440 screen at 60 fps takes about 3% of one processor core.
- The whole screen or a region; pause and resume; hotkeys that work from any program (Ctrl+Shift+F9 starts and
  stops). The editor's own window is recorded too, so its use can be shown; it can also keep itself out of the
  picture. The recording toolbar never appears in it.
- Recordings open in a tab, ready to cut.
- Screenshots of a screen or a dragged region, and drawing on the screen itself, with or without recording.

## Live streaming (Windows)

<p align="center"><img src="docs/demos/live.webp" alt="Destinations added — YouTube and Bilibili kept off, two servers on — then live on both, the stream showing the screen being recorded"></p>

- Streams to several platforms at once — YouTube, Twitch, Bilibili, Douyin, Kuaishou, WeChat Channels, Huya, Douyu,
  or any RTMP, RTMPS or SRT server. The picture is encoded once on the graphics card, so each extra platform costs
  only upload.
- Streaming and recording are separate, as in OBS: the stream shows the screen while it is recorded and black between
  recordings, and stopping a recording, or recording another part of the screen, never interrupts it.
- Each platform connects, reconnects and falls behind on its own; one that drops never holds up the others, and can
  be added or taken off while live. Stream keys are kept encrypted for your Windows account.
- The platforms are a JSON configuration: add one in Settings → Extensions, or let an AI add it.
- Your camera can sit on the screen in a corner, as a circle or a rounded rectangle with a border, so recordings and
  streams of the screen have it. Drag it to move it, drag its corner or turn the wheel over it to resize it.

## AI control over MCP

- A built-in MCP server lets Claude, Codex, Cursor or any MCP client do what you can do with the mouse: edit videos,
  paint and draw on canvases, build and keep up mind maps, record the screen, and look at the result.
- Each kind of work has its own server — video editing, drawing, mind maps, recording, or everything — so an AI hears only
  about the tools it needs. Settings copies the setup for it, ready to paste.
- Music sites, speech models, streaming platforms and the ffmpeg download source are plain JSON that an AI (or you,
  in Settings) can change. Stream keys given to an AI are stored encrypted and never shown to it again.

<table>
  <tr>
    <td><img src="docs/screenshots/en-ai.jpg" alt="MCP settings for AI tools"></td>
    <td><img src="docs/screenshots/en-music.jpg" alt="Free music search"></td>
  </tr>
</table>

## Also

- Keyboard shortcuts chosen in Settings for each kind of work on its own: general, video editing, drawing and
  recording.
- Your phone can play and download exports, and send clips back to the computer, over the local network with a
  private link.
- A window in the background uses no processor time: nothing is drawn or checked until something changes.

## Download

| System | File |
| --- | --- |
| Windows 10 / 11, x64 | `btelo-forge-windows-amd64.zip` |
| Windows on ARM | `btelo-forge-windows-arm64.zip` |
| macOS 11+, Apple silicon and Intel (experimental) | `btelo-forge-macos.zip` |

There is nothing else to install. The app uses its own ffmpeg, so that recording, streaming and exporting work the
same on every computer: a static build is downloaded once into the app's own folder (Settings → Extensions → ffmpeg
can allow the one installed on the computer instead). The macOS app is signed and notarised by Apple. On macOS, AI subtitles use `whisper-cli` from
Homebrew.

Btelo Forge was called Btelo Video Cut; releases up to 0.1.3 carry that name (`btelo-cut-*.zip`). Its settings,
unfinished edits and downloads move over the first time Btelo Forge runs. On macOS the app has a new identity, so it
asks again for permission to record the screen.

## Build

Requires Go 1.27. Windows needs nothing else; macOS needs the Xcode command-line tools.

```
go build ./cmd/btelo-forge
go test ./...
```

On Windows, `scripts\dev.ps1` builds the working tree and opens it; `-InstallShortcut` puts a desktop shortcut
that does the same, so a change can be tried with a double-click.

Pure Go on Windows (Win32, no cgo) and Cocoa on macOS. The interface is drawn in software, and ffmpeg does all
media work. [docs/canvas-engine.md](docs/canvas-engine.md) describes how the canvas works.

## Licence

[Apache License 2.0](LICENSE). Third-party components keep their own licences; see
[third-party notices](THIRD_PARTY_NOTICES.md).
