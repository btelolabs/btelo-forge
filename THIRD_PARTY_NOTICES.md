# Third-party notices

Btelo Forge includes or links the following components, each under its own licence.

## Built into the program

| Component | Licence |
| --- | --- |
| [Go](https://go.dev) standard library and `golang.org/x/image`, `x/sys`, `x/sync`, `x/text`, `x/time`, `x/oauth2` | BSD-3-Clause |
| [Model Context Protocol Go SDK](https://github.com/modelcontextprotocol/go-sdk) | MIT / Apache-2.0 |
| [google/jsonschema-go](https://github.com/google/jsonschema-go) | MIT |
| [segmentio/encoding](https://github.com/segmentio/encoding), [segmentio/asm](https://github.com/segmentio/asm) | MIT |
| [yosida95/uritemplate](https://github.com/yosida95/uritemplate) | BSD-3-Clause |
| [rsc.io/qr](https://github.com/rsc/qr) | BSD-3-Clause |
| [ebitengine/oto](https://github.com/ebitengine/oto), [ebitengine/purego](https://github.com/ebitengine/purego) (macOS audio) | Apache-2.0 |
| [htmx](https://htmx.org) 2.0.4 (served to phones) | 0BSD |
| [Material Icons](https://github.com/google/material-design-icons) | Apache-2.0 |

## Used but not included

- **ffmpeg / ffprobe** do all decoding and encoding and run as separate programs. The app uses a copy already on the
  computer, or downloads one on first run into its own folder: the GPL builds of
  [BtbN/FFmpeg-Builds](https://github.com/BtbN/FFmpeg-Builds) on Windows and of
  [ffmpeg.martin-riedl.de](https://ffmpeg.martin-riedl.de) on macOS, under their own licences (GPL, with x264, x265,
  SVT-AV1 and libaom). The download addresses are in `internal/media/ffmpeg.json`.
- **whisper.cpp** (MIT) and its **ggml Whisper models** (MIT, from OpenAI's Whisper) are downloaded on first use of
  AI subtitles, from the official GitHub releases and Hugging Face.
- **Music and sound effects** come from Kevin MacLeod / incompetech.com (CC BY 4.0), Openverse (CC0 and CC BY),
  ccMixter (CC BY) and the Internet Archive (CC BY and public domain). Each track keeps its own licence; the credits
  they require are written next to every export.
- System fonts (Microsoft YaHei, Segoe UI, PingFang, Hiragino and others) are read from the operating system and are
  not redistributed.
