package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/project"
)

// The MCP server comes in one piece for each kind of work, so an AI is told only about what it is asked to do: video
// editing, drawing, recording the screen, or everything. Each holds that domain's tools and the common ones (tabs and
// a picture of the window), with instructions to match; Settings copies the setup for each.

// mcpDomainAll is the server with every tool.
const mcpDomainAll = -1

// mcpDomains are the domains Settings offers to copy, in order, named by mcpDomainNames; mcpDomainPaths are where
// each is served, under /mcp.
var (
	mcpDomains     = []int{domainVideo, domainDrawing, domainMind, domainRecording, mcpDomainAll}
	mcpDomainNames = msgs("Video editing", "Drawing", "Mind maps", "Recording", "Everything")
	mcpDomainPaths = map[int]string{domainVideo: "/video", domainDrawing: "/drawing", domainMind: "/mind", domainRecording: "/recording", mcpDomainAll: ""}
)

// mcpCommonTools are in every domain's server.
var mcpCommonTools = append([]string{"get_state", "new_tab", "switch_tab", "close_tab", "screenshot", "get_config", "set_config", "reset_config"}, mcpAssetTools...)

// mcpDomainTools are each domain's own tools; TestMCPToolsAreAllListed keeps them in step with the server.
var mcpDomainTools = map[int][]string{
	domainVideo: {"open_video", "seek", "split", "delete_item", "delete_items", "move_item", "trim_item", "set_clip",
		"set_items", "set_track", "add_text", "add_image", "add_smart_object", "update_overlay", "add_music_file", "search_music",
		"list_music_sources", "add_library_music", "generate_subtitles", "list_subtitle_models", "subtitle_status",
		"generate_speech", "list_speech_voices", "speech_status",
		"export", "export_status", "export_options", "set_export", "list_exports", "undo", "redo", "frame_at", "player_mode", "save_edit"},
	domainDrawing: {"canvas_new", "canvas_open", "canvas_state", "canvas_render", "canvas_save", "canvas_export",
		"canvas_layer", "canvas_set_layer", "canvas_select", "canvas_paint", "canvas_fill", "canvas_add_shape",
		"canvas_add_path", "canvas_set_vector", "canvas_boolean", "canvas_add_text", "canvas_filter", "canvas_transform",
		"canvas_resize", "canvas_artboard", "canvas_history", "canvas_retouch", "canvas_adjustment", "canvas_style"},
	domainMind: {"mind_new", "mind_open", "mind_state", "mind_add", "mind_update", "mind_set_cells", "mind_delete", "mind_move",
		"mind_connect", "mind_update_connectors", "mind_find", "mind_outline", "mind_render", "mind_show", "mind_theme", "mind_history", "mind_save"},
	domainRecording: {"recording_status", "start_recording", "pause_recording", "stop_recording", "take_screenshot",
		"stream_status", "show_stream", "set_stream_destination", "remove_stream_destination", "set_stream_options", "start_stream", "stop_stream", "camera_status", "set_camera"},
}

// mcpToolsOf are the tools of a domain's server, the common ones first.
func mcpToolsOf(domain int) []string {
	out := slices.Clone(mcpCommonTools)
	for _, d := range []int{domainVideo, domainDrawing, domainMind, domainRecording} {
		if domain == mcpDomainAll || domain == d {
			out = append(out, mcpDomainTools[d]...)
		}
	}
	return out
}

// mcpToolNames lists every tool.
var mcpToolNames = mcpToolsOf(mcpDomainAll)

// The instructions: what the app is and its common tools, then each domain's part.
const (
	mcpIntro = ProductName + ` is a desktop app on this computer for editing videos, drawing, mind maps, recording the screen and
streaming live.
These tools drive its open window directly. It works in tabs, each a video edit, a drawing canvas or a mind map: get_state lists
them, new_tab, switch_tab and close_tab manage them, and screenshot shows the window as it looks. The app is
extended by JSON configurations — music_sources, subtitle_models, speech_voices, ffmpeg and streaming_platforms: get_config reads
one with a guide to its format, set_config replaces it after checking it, and reset_config goes back to the built-in.
Files are managed as in Eagle: assets_browse shows a folder in the asset manager tab, assets_list lists a folder's
files with their tags and categories, assets_tag and assets_categorize organise them, and assets_trash moves files to
the Recycle Bin.`

	mcpVideoInstructions = `Video editing: call get_state first; ids of clips, overlays and music come from it. Times are seconds on the
output timeline. Open a video with open_video (placement "new_tab" for a separate edit, "append" to add it to the end
of the main track, "upper" to lay it over the picture from a time). Cut with split/delete_item/trim_item, change speed
or clip volume with set_clip, mix tracks with set_track, add captions and images with add_text/add_image
(update_overlay to change them), put a canvas's smart object over the picture with add_smart_object (it follows
every change drawn in it), add music with search_music + add_library_music or add_music_file, make subtitles
with generate_subtitles, and a voice-over from text or from the subtitles with generate_speech (list_music_sources,
list_subtitle_models and list_speech_voices show the choices). The music sources, the subtitle engines and models
and the voice-over voices are JSON configurations: read one with get_config (it includes a guide to the format),
change it with set_config (it is checked before it is used) and undo that with reset_config — for example to add a
music site, a new model or another ffmpeg download (name ffmpeg). Change several items at once with set_items
(volume, opacity) and delete_items. Before exporting, export_options lists the presets — from high quality to extreme
compression, or fit a size in megabytes, or a silent looping GIF or WebP animation — and set_export or export
applies them. get_state reports whether ffmpeg is
ready. Check the result with frame_at (a picture of the output at a time), then export with export and poll
export_status. Every change can be undone with undo.`

	mcpDrawingInstructions = `Drawing: a canvas is a layered picture, as in Photoshop, with vector layers as in Affinity Designer, on an
infinite canvas (a frame or artboards give it fixed sizes to export). Make one with canvas_new (no size for an
infinite canvas) or open one with canvas_open, then call canvas_state: it lists the layers with their ids, kinds and
bounds, the artboards, the selection and the history. Coordinates are canvas pixels; rectangles are
[x, y, width, height]. Pixel work goes to the chosen pixel layer, within the selection: canvas_select (rect,
ellipse, polygon, wand…), canvas_paint (brush strokes along points, with the brush or a preset such as pencil or ink-brush, or erasing), canvas_fill (colour, paint bucket,
gradients), canvas_filter (blur, sharpen, noise, mosaic, levels, hue/saturation, brightness/contrast, posterize,
threshold, invert, desaturate). Vectors are layers of their own, sharp at any size: canvas_add_shape (rectangle,
ellipse, polygon, star, line), canvas_add_path (Bézier nodes, or a smooth curve through points), canvas_set_vector
(fill, stroke, width, alignment, ends, corners, dashes), canvas_boolean (add, subtract, intersect, exclude).
canvas_add_text adds text. canvas_layer and canvas_set_layer manage layers (add, group, order, merge, masks, opacity,
blend modes), and canvas_layer convert_to_smart makes the chosen layer or group a smart object, whose link id puts it
on a mind map (mind_add kind smart) or a video (add_smart_object), where it follows every change; canvas_retouch dodges, burns, sponges, blurs, sharpens or smudges along a stroke; canvas_adjustment adds
adjustment layers (levels, curves, hue/saturation…) that change what is under them without changing its pixels;
canvas_style sets a layer's effects (drop shadow, glows, stroke, colour overlay); canvas_transform moves, scales and
turns the chosen layer, or distorts it by its corners or warps it by a grid; canvas_resize changes the whole picture;
canvas_artboard manages artboards. Look at the result with canvas_render and export PNGs with canvas_export (the
picture, the selection or the artboards); the canvas saves itself, and canvas_save saves at once. canvas_history
undoes and redoes.`

	mcpMindInstructions = `Mind maps: a mind map (.btmind) is an infinite board where every object is a node — a topic (words in a
shape), a table with spreadsheet formulas, a picture or animated GIF, a video, a canvas's smart object (by its link
id: it follows every change made to it on the canvas) or another mind map shown inside (nested maps that hold each
other are shown as a warning, never followed for ever). Topics grow into trees laid out by their root's layout: map
(both sides), right or left (logic charts), down or up (org charts) or list (an outline); only free nodes have x and
y, a tree's nodes are placed by it. Connectors join any two nodes as a flowchart's arrows do, with labels, curve,
elbow or straight routes, arrowheads and dashes. Make a map with mind_new or open one with mind_open (every tool
takes path to act on a map file instead of the active tab); call mind_state for its nodes and connectors with ids
(page a large map with offset and limit, or read it as text with mind_outline). Build in batches: mind_add takes
whole trees nested by children (keys let later nodes and connectors in the same call refer to new ones) or a
Markdown outline, and connectors; a flowchart is free topics with shapes (capsule for start and end, rounded for a
step, diamond for a decision, parallelogram for input or output) joined by mind_connect. mind_update changes nodes,
mind_set_cells tables' cells, mind_move puts a node under another or frees it, mind_delete removes nodes and
connectors, mind_find searches, mind_show shows the user nodes in the window, mind_theme changes the whole map's
look, and mind_render returns a PNG to check the result. Each call is one step, undone with mind_history; the map saves itself, and mind_save saves at once.`

	mcpRecordingInstructions = `Recording: on Windows the screen can be recorded with sound: recording_status lists the screens and
microphones, start_recording begins, pause_recording pauses and resumes, and stop_recording saves the MP4 and opens it
in a tab for editing; take_screenshot saves a PNG of the screen.

Live streaming goes to several platforms at once, encoded once: stream_status lists the destinations and the
platforms that can be added, and show_stream brings the stream's tab forward for the user to watch; set_stream_destination adds or changes one (platform, server, the stream key the user
gives you — it is stored encrypted and never shown again — and whether it is streamed to), remove_stream_destination
forgets one, set_stream_options chooses the canvas (landscape, or upright for phone platforms), frame rate, bit rate
and sound, and start_stream and stop_stream go live and end it. The stream is separate from recording, as in OBS: it
shows the screen while a recording runs and black between recordings, without ever interrupting. The platforms are a
JSON configuration (get_config streaming_platforms): add a service by adding an entry, no code needed.

The camera can be shown on the screen in a window of its own, so that it is in recordings and streams: set_camera
shows or hides it and sets its shape (a square with 50% corners is a circle), size, border and place;
camera_status lists the cameras.`
)

// mcpInstructionsFor are the instructions of a domain's server.
func mcpInstructionsFor(domain int) string {
	parts := []string{mcpIntro}
	for _, p := range []struct {
		domain int
		text   string
	}{{domainVideo, mcpVideoInstructions}, {domainDrawing, mcpDrawingInstructions}, {domainMind, mcpMindInstructions}, {domainRecording, mcpRecordingInstructions}} {
		if domain == mcpDomainAll || domain == p.domain {
			parts = append(parts, p.text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// mcpServer builds the MCP server whose tools act on the window: a domain's, or with every tool.
func (a *App) mcpServer(domain int) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: mcpServerName(domain), Title: ProductName, Version: Version},
		&mcp.ServerOptions{Instructions: mcpInstructionsFor(domain)})
	a.addStateTools(s)
	a.addEditTools(s)
	a.addMediaTools(s)
	a.addConfigTools(s)
	a.addExportTools(s)
	a.addRecordTools(s)
	a.addLiveTools(s)
	a.addCameraTools(s)
	a.addDrawTools(s)
	a.addDrawToolsMore(s)
	a.addMindTools(s)
	a.addAssetTools(s)
	if domain != mcpDomainAll {
		keep := mcpToolsOf(domain)
		var drop []string
		for _, name := range mcpToolNames {
			if !slices.Contains(keep, name) {
				drop = append(drop, name)
			}
		}
		s.RemoveTools(drop...)
	}
	return s
}

// Version is the app version, set at build time.
var Version = "dev"

// mcpServerName is the name a domain's server is added to an AI under.
func mcpServerName(domain int) string {
	if p := mcpDomainPaths[domain]; p != "" {
		return "btelo-forge-" + p[1:]
	}
	return "btelo-forge"
}

// mcpTasks say what an AI is asked to do with each domain's server.
var mcpTasks = map[int]string{domainVideo: "edit videos", domainDrawing: "draw and edit pictures",
	domainMind: "think in mind maps and flowcharts", domainRecording: "record the screen", mcpDomainAll: "edit videos, draw, think in mind maps and record the screen"}

// mcpConnection is the text Settings copies for a domain: paste it to an AI and it knows how to connect and what it
// can do there.
func (a *App) mcpConnection(domain int) string {
	base := ""
	if a.services != nil {
		base = a.services.mcpURL
	}
	if base == "" {
		base = fmt.Sprintf("http://127.0.0.1:%d/mcp", mcpPort)
	}
	url, name := base+mcpDomainPaths[domain], mcpServerName(domain)
	var b strings.Builder
	fmt.Fprintf(&b, "Connect to the %s app over MCP and use it to %s for me.\n\n", ProductName, mcpTasks[domain])
	fmt.Fprintf(&b, "MCP server (Streamable HTTP, no authentication, only reachable from this computer): %s\n\n", url)
	fmt.Fprintf(&b, "Claude Code:\n  claude mcp add --transport http %s %s\n\n", name, url)
	fmt.Fprintf(&b, "Codex (~/.codex/config.toml):\n  [mcp_servers.%s]\n  url = \"%s\"\n\n", name, url)
	fmt.Fprintf(&b, "Other clients (JSON):\n  {\"mcpServers\": {\"%s\": {\"type\": \"http\", \"url\": \"%s\"}}}\n\n", name, url)
	fmt.Fprintf(&b, "If your client only speaks stdio, run: npx -y mcp-remote %s\n\n", url)
	b.WriteString(mcpInstructionsFor(domain))
	b.WriteString("\n\nTools: ")
	b.WriteString(strings.Join(mcpToolsOf(domain), ", "))
	b.WriteString("\n")
	return b.String()
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
	tool(s, a, "save_edit", "Save the active edit as a .btcut file (in the default export folder the first time, then to the same file), to open again with open_video.",
		func(none) (any, error) {
			path, err := a.saveEditFile()
			if err != nil {
				return nil, err
			}
			return map[string]string{"path": path}, nil
		})
	type watchIn struct {
		On bool `json:"on" jsonschema:"true hides every tool, leaving the picture and a seek bar; false brings the editor back"`
	}
	tool(s, a, "player_mode", "Show the active edit as a video player, or the editor again.", func(in watchIn) (any, error) {
		if a.p == nil {
			return nil, errNoProject
		}
		a.setWatching(in.On)
		return map[string]bool{"player_mode": a.watching()}, nil
	})
}
