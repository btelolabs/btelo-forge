package ui

import (
	"fmt"
	"math"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/render"
)

// exportSettings are the export settings as the MCP tools read and write them.
type exportSettings struct {
	Preset   string  `json:"preset"`
	Height   int     `json:"height"`
	FPS      int     `json:"fps"`
	Quality  string  `json:"quality"`
	Codec    string  `json:"codec"`
	Speed    string  `json:"speed"`
	Audio    string  `json:"audio"`
	Hardware bool    `json:"hardware"`
	TargetMB float64 `json:"target_mb,omitempty"`
	Encoder  string  `json:"encoder,omitempty"`
}

func (a *App) exportView(e project.Export) exportSettings {
	v := exportSettings{Preset: "custom", Height: e.Height, FPS: e.FPS, Quality: qualityIDs[e.Quality], Codec: codecIDs[e.Codec],
		Speed: speedIDs[e.Speed], Audio: audioIDs[e.Audio], Hardware: e.Hardware, TargetMB: e.TargetMB}
	if k := matchingPreset(withoutHardware(e), a.av1()); k >= 0 {
		v.Preset = exportPresets[k].id
	}
	if a.encoders != nil {
		v.Encoder = render.Encoder(e.Codec, e.Hardware, a.encoders)
	}
	return v
}

// av1 reports whether AV1 can be encoded; it is false until the encoders are known.
func (a *App) av1() bool { return a.encoders != nil && codecAvailable(a.encoders, project.AV1, false) }

// exportIn changes the export settings: a preset first, then any single setting on top of it.
type exportIn struct {
	Preset   *string  `json:"preset,omitempty" jsonschema:"high, balanced, small, strong, extreme or size (see export_options)"`
	Height   *int     `json:"height,omitempty" jsonschema:"the short side of the picture is at most this many pixels (never upscaled); 0 keeps the original; common: 2160, 1440, 1080, 720, 480, 360"`
	FPS      *int     `json:"fps,omitempty" jsonschema:"frames per second at most (never raised); 0 keeps the original; common: 60, 30, 24, 15"`
	Quality  *string  `json:"quality,omitempty" jsonschema:"high, balanced, small, smaller or smallest (ignored when target_mb is set)"`
	Codec    *string  `json:"codec,omitempty" jsonschema:"h264 (plays anywhere), h265 (smaller) or av1 (smallest)"`
	Speed    *string  `json:"speed,omitempty" jsonschema:"fast, medium, slow or slowest; slower makes smaller files"`
	Audio    *string  `json:"audio,omitempty" jsonschema:"high (192k), normal (128k), low (96k), voice (64k mono) or none"`
	Hardware *bool    `json:"hardware,omitempty" jsonschema:"encode on the NVIDIA graphics card when it can"`
	TargetMB *float64 `json:"target_mb,omitempty" jsonschema:"aim for a file of at most this many megabytes (two-pass); 0 turns it off"`
}

// apply returns e changed by in, or an error naming the first value it does not know.
func (a *App) apply(e project.Export, in exportIn) (project.Export, error) {
	pick := func(name, value string, ids []string) (int, error) {
		if k := indexOf(ids, value); k >= 0 {
			return k, nil
		}
		return 0, fmt.Errorf("unknown %s %q", name, value)
	}
	if in.Preset != nil {
		k := -1
		for i, p := range exportPresets {
			if p.id == *in.Preset {
				k = i
			}
		}
		if k < 0 {
			return e, fmt.Errorf("unknown preset %q; see export_options", *in.Preset)
		}
		hardware := e.Hardware
		e = exportPresets[k].export(a.av1())
		e.Hardware = hardware
	}
	if in.Height != nil {
		if *in.Height < 0 || *in.Height > 8640 {
			return e, fmt.Errorf("height must be 0 (original) or a pixel count up to 8640")
		}
		e.Height = *in.Height
	}
	if in.FPS != nil {
		if *in.FPS < 0 || *in.FPS > 240 {
			return e, fmt.Errorf("fps must be 0 (original) or a rate up to 240")
		}
		e.FPS = *in.FPS
	}
	if in.Quality != nil {
		k, err := pick("quality", *in.Quality, qualityIDs)
		if err != nil {
			return e, err
		}
		e.Quality = project.Quality(k)
	}
	if in.Codec != nil {
		k, err := pick("codec", *in.Codec, codecIDs)
		if err != nil {
			return e, err
		}
		e.Codec = project.Codec(k)
	}
	if in.Speed != nil {
		k, err := pick("speed", *in.Speed, speedIDs)
		if err != nil {
			return e, err
		}
		e.Speed = project.Speed(k)
	}
	if in.Audio != nil {
		k, err := pick("audio", *in.Audio, audioIDs)
		if err != nil {
			return e, err
		}
		e.Audio = project.Audio(k)
	}
	if in.Hardware != nil {
		e.Hardware = *in.Hardware
	}
	if in.TargetMB != nil {
		if *in.TargetMB < 0 || math.IsNaN(*in.TargetMB) || math.IsInf(*in.TargetMB, 0) {
			return e, fmt.Errorf("target_mb must be 0 or a positive number")
		}
		e.TargetMB = *in.TargetMB
	}
	if a.encoders != nil {
		if !codecAvailable(a.encoders, e.Codec, false) {
			return e, fmt.Errorf("this computer's ffmpeg cannot encode %s", codecIDs[e.Codec])
		}
		if e.Hardware && !hardwareAvailable(a.encoders, e.Codec) {
			return e, fmt.Errorf("the graphics card cannot encode %s on this computer", codecIDs[e.Codec])
		}
	}
	return e, nil
}

func (a *App) addExportTools(s *mcp.Server) {
	tool(s, a, "export_options", "The export presets (from the best picture to the strongest compression), every setting's choices and which formats this computer can encode.",
		func(none) (any, error) {
			var presets []map[string]string
			for _, p := range exportPresets {
				presets = append(presets, map[string]string{"id": p.id, "name": p.name, "description": p.note})
			}
			formats := map[string]any{}
			for i, id := range codecIDs {
				c := project.Codec(i)
				known := a.encoders != nil
				formats[id] = map[string]bool{"software": !known && c != project.AV1 || known && codecAvailable(a.encoders, c, false),
					"graphics_card": known && hardwareAvailable(a.encoders, c)}
			}
			return map[string]any{"presets": presets, "heights": exportHeights, "fps": exportRates, "quality": qualityIDs,
				"codecs": formats, "speed": speedIDs, "audio": audioIDs, "encoders_known": a.encoders != nil}, nil
		})
	tool(s, a, "set_export", "Change the export settings of the active edit without exporting: a preset and/or single settings (see export_options). They are remembered for new edits.",
		func(in exportIn) (any, error) {
			if a.p == nil {
				return nil, errNoProject
			}
			e, err := a.apply(a.p.Export, in)
			if err != nil {
				return nil, err
			}
			if e != a.p.Export {
				a.setExport(e)
			}
			return a.exportView(e), nil
		})
	tool(s, a, "export", "Encode the active edit to an MP4 (next to the source, or in the default export folder), optionally changing the settings first like set_export. Runs in the background; poll export_status.",
		func(in exportIn) (any, error) {
			if a.p == nil {
				return nil, errNoProject
			}
			e, err := a.apply(a.p.Export, in)
			if err != nil {
				return nil, err
			}
			if e != a.p.Export {
				a.setExport(e)
			}
			out, err := a.startExport()
			return map[string]any{"output": out, "settings": a.exportView(e)}, err
		})
}
