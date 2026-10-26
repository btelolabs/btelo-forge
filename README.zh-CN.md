<p align="center"><img src="docs/logo.jpg" alt="btelo 剪辑" width="160"></p>

<h1 align="center">btelo 剪辑</h1>

<p align="center">简单、流畅的 Windows / macOS 桌面视频剪辑工具，也能让 AI 直接操作。</p>

<p align="center"><a href="README.md">English</a> · <b>中文</b></p>

---

## 下载

在 **[Releases](https://github.com/btelolabs/btelo-video-cut/releases/latest)** 下载最新版：

| 系统 | 文件 |
| --- | --- |
| Windows 10 / 11（x64） | `btelo-cut-windows-amd64.zip` |
| Windows ARM 版 | `btelo-cut-windows-arm64.zip` |
| macOS 11 及以上（Apple 芯片和 Intel） | `btelo-cut-macos.zip` |

btelo 剪辑用 **ffmpeg** 解码和编码，需要先安装一次：

- Windows：`winget install ffmpeg`（或者把 `ffmpeg.exe`、`ffprobe.exe` 放在 `btelo-video-cut.exe` 旁边）
- macOS：`brew install ffmpeg`

macOS 版暂未公证：第一次打开时，请右键点 **btelo Cut.app**，选「打开」。

## 功能

- **像浏览器一样的标签页**：每个标签页是一个独立的剪辑，`Ctrl+T` 新建，把视频拖进来就能开始。
- **一个剪辑里放多段视频**：再拖入视频会接在主视频轨末尾；拖到上层视频轨上，就从播放头开始叠在画面上面。
- **多轨时间线**：视频轨、覆盖物轨（文字和图片）、音乐轨都可以有很多条；覆盖物在视频上方，音乐在下方。片段可以在轨道间
  拖动，拖两端修剪，时间线可滚动、缩放。上层视频轨会盖住下层。
- **剪辑和变速**：在播放头分割（`S`）、删除、修剪，每个片段可设 0.5×–4× 速度，声音变速不变调。
- **处处可调音量**：每个片段有自己的音量；点轨道头选中轨道，可以调整整条轨道的音量或静音。
- **文字和图片**：加文字（`T`）或图片（拖进来，或 `Ctrl+V` 粘贴），设定显示时长，在画面里拖动位置，可调大小、颜色、描边和底色。
- **免费音乐聚合搜索**：同时搜索 Kevin MacLeod 曲库（很多也收录在 YouTube 音频库）、Openverse、ccMixter 和 Internet Archive，
  外加 Freesound 音效，只收录可商用的许可。点一下就能试听（边下边播），下载后会缓存；需要署名的曲子，导出时自动生成署名文件。
- **AI 字幕**：用 whisper.cpp 在本机识别语音，6 个模型可选，从 Tiny（75 MB）到 Turbo（1.6 GB），可以用 CPU 或 NVIDIA
  显卡运行。字幕放在一条单独的覆盖物轨道上，可以继续编辑。
- **导出与压缩**：原始 / 1080p / 720p / 480p，高画质 / 均衡 / 小体积，H.264 / H.265。导出文件保存在原视频旁边，名为
  `原文件名.cut.mp4`（也可以在设置里指定默认目录），并且会记住上次的导出设置。
- **撤销和自动保存**：所有改动都能撤销；没导出的剪辑会自动保存，下次打开同一个视频时自动恢复。
- **用 JSON 扩展**：音乐网站，以及语音引擎、模型和语言，都写在 JSON 配置里。在 **设置 → 扩展** 里就能修改（也可以让 AI
  通过 MCP 修改），新增网站、换模型或升级引擎版本都不需要等新版本。
- **中英文界面**，在设置里切换。

## 让 AI 帮你剪（MCP）

btelo 剪辑内置一个本地 [MCP](https://modelcontextprotocol.io) 服务，Claude、Codex、Cursor 等支持 MCP 的 AI 可以打开视频、剪辑、
加文字、图片、音乐和字幕、查看成片画面并导出。

打开 **设置 → AI（MCP）**，点 **复制给 AI 的连接与使用说明**，把复制的内容发给 AI 即可，AI 就能操作软件。以 Claude Code 为例：

```
claude mcp add --transport http btelo-cut http://127.0.0.1:38471/mcp
```

这个服务只在本机监听。提供的工具包括 `get_state`、`open_video`、`split`、`delete_item`、`move_item`、`trim_item`、`set_clip`、
`set_track`、`add_text`、`add_image`、`update_overlay`、`add_music_file`、`search_music`、`add_library_music`、
`generate_subtitles`、`export`、`frame_at`、`screenshot`、`undo`、`get_config`、`set_config`、`reset_config` 等。

## 用 JSON 扩展

btelo 剪辑能连到哪些网站、用哪些模型，由两份 JSON 配置决定。可以在 **设置 → 扩展** 里修改，保存时会先检查，通过后立即
生效；也可以让 AI 用 `get_config` / `set_config` 来改：

- **音乐来源**：音乐面板搜索的每个网站。写明它的搜索地址和参数、结果在返回 JSON 里的位置，再用模板把每条结果变成一首曲子
  （如 `{title}`、`{files.0.url|path}`、`{tags|split:,|take:2|join: · }`）。像 Kevin MacLeod 这样一个文件列出全部曲目的
  网站，会在本机搜索；像 Internet Archive 这样要请求两次的网站也支持。
- **字幕模型**：whisper.cpp 引擎（要下载的 zip，或系统里已安装的程序）、模型（任意 ggml 文件，可选 SHA-256 校验）、
  可识别的语言，以及引擎的命令行。把下载地址换成更新的 whisper.cpp 版本，它会下载到单独的文件夹里。

编辑器旁的 **复制格式说明** 会复制完整的格式说明。内置配置见
[`internal/library/sources.json`](internal/library/sources.json) 和
[`internal/transcribe/models.json`](internal/transcribe/models.json)。

## 在手机上下载导出的视频

在 **设置 → 手机** 里打开共享，用手机扫二维码（需要连同一个 Wi-Fi）。网页会列出所有导出过的视频，可以直接播放或下载。网址里
带有私密口令，只有拿到网址的人才能打开。

## 快捷键

| 按键 | 作用 |
| --- | --- |
| 空格 | 播放 / 暂停 |
| S | 在播放头处分割 |
| Delete | 删除选中的片段、音乐或覆盖物 |
| T | 在播放头添加文字 |
| Ctrl+V | 粘贴图片、文件或文字 |
| ← / → | 前后一帧（Shift：一秒） |
| Ctrl+Z / Ctrl+Y | 撤销 / 重做 |
| Ctrl+E | 导出 |
| Ctrl+T / Ctrl+W | 新建 / 关闭标签页 |
| Ctrl+O | 打开视频 |
| 滚轮 / Shift+滚轮 / Ctrl+滚轮 | 上下滚动轨道 / 左右滚动时间 / 缩放时间线 |

macOS 上用 ⌘ 代替 Ctrl。

## 关于 YouTube 音频库

YouTube 音频库没有公开接口：必须登录 YouTube Studio，旧的公开下载地址也已失效。所以 btelo 剪辑搜索的是可以直接下载的曲库。
想用 YouTube 音频库里的曲子：先在 YouTube Studio 下载，再把文件拖进窗口（或在「本地导入」里导入）。

## 从源码构建

需要 Go 1.27 及以上。Windows 不需要别的；macOS 需要 Xcode 命令行工具。

```
go build -ldflags "-H windowsgui" -o btelo-video-cut.exe ./cmd/btelo-video-cut   # Windows
CGO_ENABLED=1 go build -o btelo-video-cut ./cmd/btelo-video-cut                  # macOS
go test ./...
```

## 许可

btelo 剪辑**可以免费使用**，做商业视频也可以；源代码公开，可以阅读、修改后自己使用。**禁止再分发和销售。**详见
[LICENSE](LICENSE)。第三方组件遵循各自的许可，见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
