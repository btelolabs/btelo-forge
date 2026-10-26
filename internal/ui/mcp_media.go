package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-video-cut/internal/library"
	"github.com/btelolabs/btelo-video-cut/internal/media"
	"github.com/btelolabs/btelo-video-cut/internal/project"
	"github.com/btelolabs/btelo-video-cut/internal/render"
	"github.com/btelolabs/btelo-video-cut/internal/transcribe"
)

// rememberTracks keeps search results so a tool can add one by its key later.
func (a *App) rememberTracks(tracks []library.Track) {
	if len(a.known) > 5000 {
		clear(a.known)
	}
	for _, t := range tracks {
		a.known[t.Key()] = t
	}
}

type trackView struct {
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Genre    string  `json:"genre,omitempty"`
	Duration float64 `json:"duration"`
	License  string  `json:"license"`
	Cached   bool    `json:"downloaded"`
}

func (a *App) addMediaTools(s *mcp.Server) {
	type musicFileIn struct {
		Path   string   `json:"path" jsonschema:"absolute path of a sound file"`
		Start  *float64 `json:"start,omitempty" jsonschema:"seconds; the playhead when omitted"`
		Volume *float64 `json:"volume,omitempty" jsonschema:"0 to 2"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "add_music_file", Description: "Put a sound file from this computer on a music track; it fills up to the end of the video."},
		func(ctx context.Context, req *mcp.CallToolRequest, in musicFileIn) (*mcp.CallToolResult, any, error) {
			info, err := media.Probe(ctx, in.Path)
			if err == nil && !info.HasAudio {
				err = errors.New("the file has no sound")
			}
			if err != nil {
				return errorResult(err), nil, nil
			}
			return result(call(a, func() (any, error) {
				id := 0
				err := a.edit(func(p *project.Project) error {
					at := a.playhead
					if in.Start != nil {
						at = *in.Start
					}
					id = p.AddMusic(in.Path, filepath.Base(in.Path), "", info.Duration, at)
					if in.Volume != nil {
						p.SetMusic(id, *in.Volume, true)
					}
					return nil
				})
				return map[string]int{"id": id}, err
			}))
		})

	type searchIn struct {
		Query  string `json:"query" jsonschema:"words to look for, in English works best"`
		Source string `json:"source,omitempty" jsonschema:"all (default: every music source), all_effects (every effect source) or a source id from list_music_sources"`
		Filter string `json:"filter,omitempty" jsonschema:"catalog sources only: one of the source's filters (a genre)"`
		Page   int    `json:"page,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "search_music", Description: "Search the free music and sound-effect sources (licences that allow commercial use). Returns keys for add_library_music."},
		func(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
			var p library.Page
			var err error
			switch in.Source {
			case "", srcAll:
				p, err = a.lib.SearchAll(ctx, library.KindMusic, in.Query, max(in.Page, 1))
			case "all_effects":
				p, err = a.lib.SearchAll(ctx, library.KindEffects, in.Query, max(in.Page, 1))
			default:
				if src, ok := a.lib.Config().Find(in.Source); !ok || src.Disabled {
					return errorResult(fmt.Errorf("unknown or disabled source %q; see list_music_sources", in.Source)), nil, nil
				}
				p, err = a.lib.Search(ctx, in.Source, in.Query, in.Filter, max(in.Page, 1))
			}
			if err != nil && len(p.Tracks) == 0 {
				return errorResult(err), nil, nil
			}
			return result(call(a, func() (any, error) {
				a.rememberTracks(p.Tracks)
				out := struct {
					Tracks  []trackView `json:"tracks"`
					Pages   int         `json:"pages"`
					Warning string      `json:"warning,omitempty"`
				}{Tracks: []trackView{}, Pages: p.Pages}
				for _, t := range p.Tracks {
					_, cached := a.lib.Cached(t)
					out.Tracks = append(out.Tracks, trackView{t.Key(), t.Title, t.Artist, t.Genre, math.Round(t.Duration), t.License, cached})
				}
				if err != nil {
					out.Warning = err.Error()
				}
				return out, nil
			}))
		})
	tool(s, a, "list_music_sources", "The music and sound-effect sources configured (see get_config music_sources to change them).", func(none) (any, error) {
		out := []map[string]any{}
		for _, src := range a.lib.Config().Sources {
			out = append(out, map[string]any{"id": src.ID, "name": src.Name.In("en"), "kind": src.Kind, "mode": src.Mode,
				"enabled": !src.Disabled, "inAll": !src.NotInAll, "filters": src.Filters, "note": src.Note.In("en")})
		}
		return out, nil
	})

	type libraryIn struct {
		Key    string   `json:"key" jsonschema:"key from search_music"`
		Start  *float64 `json:"start,omitempty"`
		Volume *float64 `json:"volume,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "add_library_music", Description: "Download a track found with search_music (kept for next time) and put it on a music track. Credits are written next to the export."},
		func(ctx context.Context, req *mcp.CallToolRequest, in libraryIn) (*mcp.CallToolResult, any, error) {
			t, err := call(a, func() (library.Track, error) {
				t, ok := a.known[in.Key]
				if !ok {
					return t, fmt.Errorf("unknown key %q; call search_music first", in.Key)
				}
				return t, nil
			})
			if err != nil {
				return errorResult(err), nil, nil
			}
			path, err := a.lib.Download(ctx, t)
			if err != nil {
				return errorResult(err), nil, nil
			}
			total := t.Duration
			if info, err := media.Probe(ctx, path); err == nil {
				total = info.Duration
			}
			return result(call(a, func() (any, error) {
				id := 0
				err := a.edit(func(p *project.Project) error {
					at := a.playhead
					if in.Start != nil {
						at = *in.Start
					}
					id = p.AddMusic(path, t.Title, t.Credit, total, at)
					if in.Volume != nil {
						p.SetMusic(id, *in.Volume, true)
					}
					return nil
				})
				return map[string]int{"id": id}, err
			}))
		})

	type subtitleIn struct {
		Model    string `json:"model,omitempty" jsonschema:"model id from list_subtitle_models; the last one used when omitted"`
		Language string `json:"language,omitempty" jsonschema:"spoken language code from list_subtitle_models, such as auto, zh or en"`
		Engine   string `json:"engine,omitempty" jsonschema:"engine id from list_subtitle_models"`
	}
	tool(s, a, "generate_subtitles", "Recognise the speech of the edit with a local AI model and add it as subtitles on a new overlay track. Runs in the background; poll subtitle_status.",
		func(in subtitleIn) (any, error) {
			c := a.subtitles.config
			if in.Model != "" {
				if _, ok := c.Model(in.Model); !ok {
					return nil, fmt.Errorf("unknown model %q; see list_subtitle_models", in.Model)
				}
				a.settings.SubtitleModel = in.Model
			}
			if in.Language != "" {
				if _, ok := c.Language(in.Language); !ok {
					return nil, fmt.Errorf("unknown language %q; see list_subtitle_models", in.Language)
				}
				a.settings.SubtitleLanguage = in.Language
			}
			if in.Engine != "" {
				if !slices.ContainsFunc(c.Available(), func(e transcribe.Engine) bool { return e.ID == in.Engine }) {
					return nil, fmt.Errorf("engine %q is not available on this computer; see list_subtitle_models", in.Engine)
				}
				a.settings.SubtitleEngine = in.Engine
			}
			a.saveSettings()
			return map[string]string{"status": "started"}, a.generateSubtitles()
		})
	tool(s, a, "list_subtitle_models", "The speech engines that run on this computer, the models and the spoken languages for generate_subtitles (see get_config subtitle_models to change them).",
		func(none) (any, error) {
			c := a.subtitles.config
			engine, _ := a.subtitleEngine()
			out := map[string]any{"model": a.subtitleModel().ID, "language": a.subtitleLanguage(), "engine": engine.ID}
			var engines, models, languages []map[string]any
			for _, e := range c.Available() {
				_, ready := a.subtitles.manager.EnginePath(e)
				engines = append(engines, map[string]any{"id": e.ID, "name": e.Name.In("en"), "note": e.Note.In("en"), "ready": ready})
			}
			for _, m := range c.Models {
				_, ready := a.subtitles.manager.ModelPath(m)
				models = append(models, map[string]any{"id": m.ID, "name": m.Name.In("en"), "note": m.Note.In("en"), "bytes": m.Size, "downloaded": ready})
			}
			for _, l := range c.Languages {
				languages = append(languages, map[string]any{"code": l.Code, "name": l.Name.In("en")})
			}
			out["engines"], out["models"], out["languages"] = engines, models, languages
			return out, nil
		})
	tool(s, a, "subtitle_status", "Progress of generate_subtitles: running with its stage, or the outcome of the last run.", func(none) (any, error) {
		st := map[string]any{"running": a.subtitles.job != nil}
		if j := a.subtitles.job; j != nil {
			st["stage"], st["progress"] = j.stage, j.fraction
		} else {
			st["added"], st["error"] = a.subtitles.lastDone, a.subtitles.lastErr
		}
		return st, nil
	})

	tool(s, a, "export_status", "Progress of the running export, or the outcome of the last one.", func(none) (any, error) {
		if j := a.export; j != nil {
			return map[string]any{"running": true, "output": j.out, "progress": j.progress}, nil
		}
		st := map[string]any{"running": false, "output": a.last.out}
		if a.last.err != nil {
			st["error"] = a.describe(a.last.err)
		}
		return st, nil
	})
	tool(s, a, "list_exports", "Videos exported so far, newest first.", func(none) (any, error) {
		out := []map[string]any{}
		for _, r := range a.exports() {
			info := map[string]any{"path": r.Path, "exported": r.At.Format(time.RFC3339)}
			if st, err := os.Stat(r.Path); err == nil {
				info["bytes"] = st.Size()
			}
			out = append(out, info)
		}
		return out, nil
	})

	type frameIn struct {
		Time float64 `json:"time" jsonschema:"seconds on the output timeline"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "frame_at", Description: "A PNG of the finished video at a time, with every track and overlay as the export will show it (640 pixels wide)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in frameIn) (*mcp.CallToolResult, any, error) {
			data, err := a.frameAt(ctx, in.Time)
			if err != nil {
				return errorResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: "image/png"}}}, nil, nil
		})
}

// frameAt renders one output frame at t through the same graph as the export; it runs on a server goroutine.
func (a *App) frameAt(ctx context.Context, t float64) ([]byte, error) {
	type job struct {
		p        *project.Project
		overlays []render.Overlay
		dir      string
		w, h     int
	}
	j, err := call(a, func() (job, error) {
		if a.p == nil {
			return job{}, errNoProject
		}
		p := a.p.Clone()
		w := 640
		h := max(2, int(math.Round(float64(w)*float64(p.Canvas.Height)/float64(p.Canvas.Width)/2))*2)
		dir, err := os.MkdirTemp("", "btelo-frame-*")
		if err != nil {
			return job{}, err
		}
		var overlays []render.Overlay
		for _, o := range p.Visible(t) {
			img, ok := a.overlayImage(o, w, h)
			if !ok {
				continue
			}
			path := filepath.Join(dir, fmt.Sprintf("o%d.png", o.ID))
			if err := img.WritePNG(path); err != nil {
				return job{}, err
			}
			overlays = append(overlays, render.Overlay{Path: path, X: img.At.X, Y: img.At.Y, Start: t - 1, End: t + 1})
		}
		return job{p, overlays, dir, w, h}, nil
	})
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(j.dir)
	t = math.Min(math.Max(t, 0), j.p.Duration()-1/j.p.Canvas.FPS)
	g, err := render.Build(render.Spec{Project: j.p, From: t, To: t + 1/j.p.Canvas.FPS + 0.05, Width: j.w, Height: j.h, FPS: j.p.Canvas.FPS, Video: true, Overlays: j.overlays})
	if err != nil {
		return nil, err
	}
	args, err := g.Args("-frames:v", "1", "-f", "image2pipe", "-c:v", "png", "pipe:1")
	if err != nil {
		return nil, err
	}
	cmd, err := media.Command(ctx, "ffmpeg", args...)
	if err != nil {
		return nil, err
	}
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rendering the frame: %w", err)
	}
	return data, nil
}
