<p align="center"><img src="docs/logo.png" alt="Btelo Forge" width="128"></p>

<h1 align="center">Btelo Forge</h1>

<p align="center">剪视频、画图、录屏，一个小巧的原生软件全都能做，支持 Windows 和 macOS。<br>
免费开源、离线可用，AI 也能通过 MCP 直接操作。</p>

<p align="center">
  <a href="https://github.com/btelolabs/btelo-forge/releases/latest"><b>下载</b></a> ·
  <a href="README.md">English</a> · <b>中文</b>
</p>

<p align="center"><img src="docs/screenshots/zh-editor.jpg" alt="视频剪辑：预览、覆盖物和多轨时间线"></p>

## 视频剪辑

- 标签页，各自独立的剪辑；再拖入视频，可以接在主轨末尾，也可以叠到上层轨道。
- 多轨时间线：视频、覆盖物（文字、图片和绘图）、音乐，上层轨道盖住下层。
- 分割、修剪、单片段 0.25×–4× 变速，声音变速不变调。
- 片段和整条轨道都能调音量，片段和覆盖物都能调不透明度；框选或 Ctrl 多选后一起修改。
- 导出预设从「高画质」到「极限压缩」，另有「指定大小」：两遍编码，压到你设定的文件大小以内。所有参数都可以单独调：分辨率、帧率、H.264 / H.265 / AV1、画质、编码速度、音频码率，有 NVIDIA 显卡时用显卡编码。
- 配乐：一次搜索 Kevin MacLeod、Openverse、ccMixter、Internet Archive 和 Freesound 音效，只收录可商用的许可，导出时自动在旁边写好需要的署名。
- 字幕：本机用 whisper.cpp 识别语音，六种模型，CPU 或 CUDA；字幕放在单独的轨道上，可以继续编辑。
- 预览和导出用同一套滤镜图，看到什么就导出什么。

<table>
  <tr>
    <td><img src="docs/screenshots/zh-select.jpg" alt="多选后一起编辑"></td>
    <td><img src="docs/screenshots/zh-export.jpg" alt="导出预设"></td>
  </tr>
</table>

## 绘图

<p align="center"><img src="docs/screenshots/zh-canvas.jpg" alt="画板上的海报：位图、矢量、文字和调整图层，节点工具正在编辑一条曲线"></p>

**位图，像 Photoshop 一样**
- 画笔与橡皮擦（硬度、流量、平滑、笔尖形状）；仿制图章；减淡、加深、海绵、模糊、锐化、涂抹。
- 矩形/椭圆选框、套索、魔棒选区；油漆桶和渐变。
- 图层与编组，支持蒙版、26 种混合模式和锁定透明像素；调整图层只改变下方的显示效果、不动原像素；图层样式（投影、内阴影、外发光、内发光、描边、颜色叠加）随图层的每次修改自动更新。
- 色阶、曲线、色相/饱和度等调整，高斯模糊、USM 锐化、添加杂色、马赛克等滤镜，都带实时预览；自由变换含斜切、扭曲、透视、变形；历史记录面板。

**矢量，像 Affinity Designer 一样**
- 形状（矩形、椭圆、多边形、星形、直线）、钢笔，以及把手绘线条变成少量平滑曲线的铅笔。
- 编辑节点和控制柄的节点工具，布尔运算（相加、相减、相交、排除），吸附到节点、边和中心。
- 填充与描边设置：宽度、对齐、端点、拐角、虚线。矢量在任何缩放和导出中都保持清晰。

**画布**
- 无限画布：开始画之前不用设定尺寸。需要固定尺寸时加画板，每个画板单独导出为一张图片。
- 截图可以直接作为画布打开；也可以在视频上绘制覆盖物，画布每次保存，覆盖物随之更新。
- 画布文件完整保存所有内容：图层、矢量、文字、蒙版、效果、调整图层和画板。

<p align="center"><img src="docs/screenshots/zh-style.jpg" alt="图层样式对话框"></p>

## 录屏（Windows）

- 录制屏幕，同时录电脑声音和麦克风，做法与 OBS Studio 相同：桌面复制（Desktop Duplication）把画面直接交给显卡编码（NVIDIA NVENC、AMD AMF 或 Media Foundation），实测录制 3440×1440、每秒 60 帧只占单核处理器约 3%。
- 整个屏幕或选定区域；可以暂停和继续；在任何程序里都能用的热键（Ctrl+Shift+F9 开始和停止）；还能让本软件的窗口不出现在画面里。
- 录完直接在新标签页打开，马上可以剪辑。
- 截取整个屏幕或拖选区域，录屏或不录屏时都能直接在屏幕上绘制。

<p align="center"><img src="docs/screenshots/zh-record.jpg" alt="录屏设置"></p>

## AI 通过 MCP 操作

- 内置 MCP 服务：Claude、Codex、Cursor 等 MCP 客户端能做你用鼠标能做的事——剪辑视频、在画布上绘图、录制屏幕，并查看结果。
- 视频剪辑、绘图、录制（以及全部）各有独立的服务，AI 只会看到它需要的工具；设置里可以一键复制给 AI 的接入说明。
- 音乐网站、语音模型和 ffmpeg 下载来源都是纯 JSON 配置，可以让 AI 改，也可以自己在设置里改。

<table>
  <tr>
    <td><img src="docs/screenshots/zh-ai.jpg" alt="给 AI 用的 MCP 设置"></td>
    <td><img src="docs/screenshots/zh-music.jpg" alt="免费音乐搜索"></td>
  </tr>
</table>

## 其他

- 快捷键在设置中按类别分别自定义：通用、视频剪辑、绘图、录制。
- 手机在同一局域网里通过带私密口令的链接，播放和下载导出的视频，也能把视频传回电脑。
- 窗口在后台时不占用处理器：在有变化之前，什么都不绘制、什么都不检查。

## 下载

| 系统 | 文件 |
| --- | --- |
| Windows 10 / 11，x64 | `btelo-forge-windows-amd64.zip` |
| Windows ARM 版 | `btelo-forge-windows-arm64.zip` |
| macOS 11 及以上，Apple 芯片与 Intel | `btelo-forge-macos.zip` |

不需要另外安装任何东西：没有 ffmpeg 或版本低于 6.0 时，软件会自动下载一份静态版本到它自己的目录，只下载一次。macOS 版已签名并通过 Apple 公证。macOS 上的 AI 字幕使用 Homebrew 安装的 `whisper-cli`。

Btelo Forge 原名 Btelo Video Cut，0.1.3 及更早的版本仍用旧名（`btelo-cut-*.zip`）。第一次运行 Btelo Forge 时，原来的设置、未完成的剪辑和已下载的内容会自动迁移过来。macOS 上软件换了新的身份标识，所以会重新请求录屏权限。

## 构建

需要 Go 1.27。Windows 不需要别的；macOS 需要 Xcode 命令行工具。

```
go build ./cmd/btelo-forge
go test ./...
```

Windows 上，`scripts\dev.ps1` 会从当前源码编译并打开软件；加 `-InstallShortcut` 会在桌面放一个做同样事情的快捷方式，改完代码双击即可试用。

Windows 上是纯 Go 实现（Win32，不用 cgo），macOS 上使用 Cocoa；界面由软件自己绘制，媒体处理全部交给 ffmpeg。画布的实现见 [docs/canvas-engine.md](docs/canvas-engine.md)。

## 许可

[Apache License 2.0](LICENSE)。第三方组件遵循各自的许可，见[第三方声明](THIRD_PARTY_NOTICES.md)。
