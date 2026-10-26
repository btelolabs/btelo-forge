package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-video-cut/internal/project"
)

// mcpInstructions tells an AI what the server is and how to use it; it is also part of the text the settings copy.
const mcpInstructions = ProductName + ` is a desktop video editor running on this computer. These tools drive the open editor window directly.
Workflow: call get_state first; ids of clips, overlays and music come from it. Times are seconds on the output timeline.
Open a video with open_video (placement "new_tab" for a separate edit, "append" to add it to the end of the main track,
"upper" to lay it over the picture from a time). Cut with split/delete_item/trim_item, change speed or clip volume with
set_clip, mix tracks with set_track, add captions and images with add_text/add_image (update_overlay to change them),
add music with search_music + add_library_music or add_music_file, make subtitles with generate_subtitles
(list_music_sources and list_subtitle_models show the choices). The music sources and the subtitle engines and models
are JSON configurations: read one with get_config (it includes a guide to the format), change it with set_config
(it is checked before it is used) and undo that with reset_config — for example to add a music site, a new model
or another ffmpeg download (name ffmpeg). Change several items at once with set_items (volume, opacity) and
delete_items. Before exporting, export_options lists the presets — from high quality to extreme compression, or fit a
size in megabytes — and set_export or export applies them. get_state reports whether ffmpeg is ready.
Check the result with frame_at (a picture of the output at a time) or screenshot (the editor window), then export with
export and poll export_status. Every change can be undone with undo.`

// mcpServer builds the MCP server whose tools act on the editor.
func (a *App) mcpServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "btelo-cut", Title: ProductName, Version: Version}, &mcp.ServerOptions{Instructions: mcpInstructions})
	a.addStateTools(s)
	a.addEditTools(s)
	a.addMediaTools(s)
	a.addConfigTools(s)
	a.addExportTools(s)
	return s
}

// Version is the app version, set at build time.
var Version = "dev"

// mcpConnection is the text Settings copies: paste it to an AI and it knows how to connect and what it can do.
func (a *App) mcpConnection() string {
	url := ""
	if a.services != nil {
		url = a.services.mcpURL
	}
	if url == "" {
		url = fmt.Sprintf("http://127.0.0.1:%d/mcp", mcpPort)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Connect to the %s video editor over MCP and use it to edit videos for me.\n\n", ProductName)
	fmt.Fprintf(&b, "MCP server (Streamable HTTP, no authentication, only reachable from this computer): %s\n\n", url)
	fmt.Fprintf(&b, "Claude Code:\n  claude mcp add --transport http btelo-cut %s\n\n", url)
	fmt.Fprintf(&b, "Codex (~/.codex/config.toml):\n  [mcp_servers.btelo-cut]\n  url = \"%s\"\n\n", url)
	fmt.Fprintf(&b, "Other clients (JSON):\n  {\"mcpServers\": {\"btelo-cut\": {\"type\": \"http\", \"url\": \"%s\"}}}\n\n", url)
	fmt.Fprintf(&b, "If your client only speaks stdio, run: npx -y mcp-remote %s\n\n", url)
	b.WriteString(mcpInstructions)
	b.WriteString("\n\nTools: ")
	b.WriteString(strings.Join(mcpToolNames, ", "))
	b.WriteString("\n")
	return b.String()
}

// mcpToolNames lists every tool, for the copied instructions; TestMCPToolsAreAllListed keeps it in step.
var mcpToolNames = []string{
	"get_state", "new_tab", "switch_tab", "close_tab", "open_video", "seek", "split", "delete_item", "delete_items", "move_item",
	"trim_item", "set_clip", "set_items", "set_track", "add_text", "add_image", "update_overlay", "add_music_file", "search_music",
	"list_music_sources", "add_library_music", "generate_subtitles", "list_subtitle_models", "subtitle_status",
	"export", "export_status", "export_options", "set_export", "list_exports", "undo", "redo", "screenshot", "frame_at",
	"get_config", "set_config", "reset_config",
}

// tool registers a handler that runs on the UI goroutine and answers with JSON.
func tool[In any](s *mcp.Server, a *App, name, description string, run func(In) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: description}, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		return result(call(a, func() (any, error) { return run(in) }))
	})
}

func jsonText(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return string(data), err
}

type none struct{}

type clipView struct {
	ID      int     `json:"id"`
	Source  string  `json:"source"`
	In      float64 `json:"in"`
	Out     float64 `json:"out"`
	Speed   float64 `json:"speed"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Volume  float64 `json:"volume"`
	Opacity float64 `json:"opacity"`
}

type videoTrackView struct {
	Index  int        `json:"index"`
	Main   bool       `json:"main"`
	Muted  bool       `json:"muted"`
	Volume float64    `json:"volume"`
	Clips  []clipView `json:"clips"`
}

type overlayView struct {
	ID         int     `json:"id"`
	Kind       string  `json:"kind"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Size       float64 `json:"size"`
	Text       string  `json:"text,omitempty"`
	Color      string  `json:"color,omitempty"`
	Outline    bool    `json:"outline,omitempty"`
	Background bool    `json:"background,omitempty"`
	Image      string  `json:"image,omitempty"`
	Opacity    float64 `json:"opacity"`
}

type overlayTrackView struct {
	Index  int           `json:"index"`
	Hidden bool          `json:"hidden"`
	Items  []overlayView `json:"items"`
}

type musicView struct {
	ID     int     `json:"id"`
	Title  string  `json:"title"`
	Path   string  `json:"path"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Offset float64 `json:"offset"`
	Volume float64 `json:"volume"`
	Fade   bool    `json:"fade"`
}

type musicTrackView struct {
	Index  int         `json:"index"`
	Muted  bool        `json:"muted"`
	Volume float64     `json:"volume"`
	Clips  []musicView `json:"clips"`
}

type projectView struct {
	Duration float64            `json:"duration"`
	Playhead float64            `json:"playhead"`
	Canvas   project.Canvas     `json:"canvas"`
	Video    []videoTrackView   `json:"video_tracks"`
	Overlays []overlayTrackView `json:"overlay_tracks"`
	Music    []musicTrackView   `json:"music_tracks"`
	Export   exportSettings     `json:"export_settings"`
	Output   string             `json:"export_to"`
}

type tabView struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	Active  bool   `json:"active"`
	Opening string `json:"opening,omitempty"`
}

type stateView struct {
	Tabs     []tabView    `json:"tabs"`
	Project  *projectView `json:"project,omitempty"`
	Language Language     `json:"language"`
	FFmpeg   ffmpegView   `json:"ffmpeg"`
}

// ffmpegView tells an AI whether videos can be opened yet.
type ffmpegView struct {
	Ready    bool    `json:"ready"`
	Version  string  `json:"version,omitempty"`
	Path     string  `json:"path,omitempty"`
	Progress float64 `json:"download_progress,omitempty"`
	Status   string  `json:"status,omitempty"`
}

func colorHex(c [3]uint8) string { return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]) }

func (a *App) state() stateView {
	var s stateView
	s.Language = a.settings.Language
	s.FFmpeg = ffmpegView{Ready: a.tools.ready, Version: a.tools.found.Version, Path: a.tools.found.FFmpeg}
	if a.tools.downloading {
		s.FFmpeg.Progress = a.tools.fraction
	}
	s.FFmpeg.Status, _ = a.toolsStatus()
	for i, d := range a.docs {
		s.Tabs = append(s.Tabs, tabView{Index: i, Title: d.title(a), Active: d == a.doc, Opening: d.opening})
	}
	if a.p == nil {
		return s
	}
	p := a.p
	v := &projectView{Duration: p.Duration(), Playhead: a.playhead, Canvas: p.Canvas, Output: a.destination(p)}
	v.Export = a.exportView(p.Export)
	for k, t := range p.Video {
		tv := videoTrackView{Index: k, Main: k == 0, Muted: t.Muted, Volume: t.Volume, Clips: []clipView{}}
		for _, c := range t.Clips {
			tv.Clips = append(tv.Clips, clipView{ID: c.ID, Source: p.SourceOf(c).Path, In: c.In, Out: c.Out, Speed: c.Speed, Start: c.Start, End: c.End(), Volume: c.Volume, Opacity: c.Opacity})
		}
		v.Video = append(v.Video, tv)
	}
	for k, t := range p.Overlays {
		tv := overlayTrackView{Index: k, Hidden: t.Hidden, Items: []overlayView{}}
		for _, o := range t.Items {
			ov := overlayView{ID: o.ID, Kind: "text", Start: o.Start, End: o.End, X: o.X, Y: o.Y, Size: o.Size, Opacity: o.Opacity}
			if o.Kind == project.ImageOverlay {
				ov.Kind, ov.Image = "image", o.Image
			} else {
				ov.Text, ov.Color, ov.Outline, ov.Background = o.Body, colorHex(o.Color), o.Outline, o.Box
			}
			tv.Items = append(tv.Items, ov)
		}
		v.Overlays = append(v.Overlays, tv)
	}
	for k, t := range p.Music {
		tv := musicTrackView{Index: k, Muted: t.Muted, Volume: t.Volume, Clips: []musicView{}}
		for _, m := range t.Clips {
			tv.Clips = append(tv.Clips, musicView{ID: m.ID, Title: m.Title, Path: m.Path, Start: m.Start, End: m.End(), Offset: m.Offset, Volume: m.Volume, Fade: m.Fade})
		}
		v.Music = append(v.Music, tv)
	}
	s.Project = v
	return s
}

// waitFor polls done on the UI goroutine until it holds or timeout passes; it runs on a server goroutine.
func waitFor(a *App, timeout time.Duration, done func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := call(a, func() (bool, error) { return done() })
		if err != nil || ok {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %v", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (a *App) addStateTools(s *mcp.Server) {
	tool(s, a, "get_state", "Tabs, and for the active tab every track with its clips, overlays and music (with ids), the playhead, duration, canvas and export settings.",
		func(none) (any, error) { return a.state(), nil })
	tool(s, a, "new_tab", "Open an empty tab and make it active.", func(none) (any, error) {
		a.newTab()
		return a.state().Tabs, nil
	})
	type tabIn struct {
		Index int `json:"index" jsonschema:"tab index from get_state"`
	}
	tool(s, a, "switch_tab", "Make another tab active.", func(in tabIn) (any, error) {
		if in.Index < 0 || in.Index >= len(a.docs) {
			return nil, fmt.Errorf("no tab %d", in.Index)
		}
		a.switchTab(in.Index)
		return a.state().Tabs, nil
	})
	tool(s, a, "close_tab", "Close a tab; its edit is saved and comes back when the same video is opened again.", func(in tabIn) (any, error) {
		if in.Index < 0 || in.Index >= len(a.docs) {
			return nil, fmt.Errorf("no tab %d", in.Index)
		}
		a.closeTab(in.Index)
		return a.state().Tabs, nil
	})
	tool(s, a, "undo", "Undo the last change in the active tab.", func(none) (any, error) {
		if a.p == nil || !a.hist.CanUndo() {
			return nil, fmt.Errorf("nothing to undo")
		}
		a.undo()
		return a.state().Project, nil
	})
	tool(s, a, "redo", "Redo the last undone change.", func(none) (any, error) {
		if a.p == nil || !a.hist.CanRedo() {
			return nil, fmt.Errorf("nothing to redo")
		}
		a.redo()
		return a.state().Project, nil
	})
	type seekIn struct {
		Time float64 `json:"time" jsonschema:"seconds on the output timeline"`
	}
	tool(s, a, "seek", "Move the playhead.", func(in seekIn) (any, error) {
		if a.p == nil {
			return nil, errNoProject
		}
		a.seek(in.Time)
		return map[string]float64{"playhead": a.playhead}, nil
	})
}
