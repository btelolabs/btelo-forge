package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"math"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-video-cut/internal/media"
)

// mcpSession connects a real MCP client to the editor's own server while the test keeps drawing frames.
type mcpSession struct {
	x  *harness
	cs *mcp.ClientSession
}

func connectMCP(t *testing.T, x *harness) *mcpSession {
	t.Helper()
	if x.a.services == nil || x.a.services.mcpURL == "" {
		t.Fatalf("MCP server not running: %+v", x.a.services)
	}
	var cs *mcp.ClientSession
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		cs, err = client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: x.a.services.mcpURL, MaxRetries: -1}, nil)
	}()
	x.pump(20*time.Second, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return &mcpSession{x, cs}
}

// call runs a tool, drawing frames until it answers.
func (s *mcpSession) call(name string, args map[string]any) *mcp.CallToolResult {
	s.x.t.Helper()
	var res *mcp.CallToolResult
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err = s.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	}()
	s.x.pump(2*time.Minute, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if err != nil {
		s.x.t.Fatalf("%s: %v", name, err)
	}
	return res
}

func (s *mcpSession) ok(name string, args map[string]any, into any) {
	s.x.t.Helper()
	res := s.call(name, args)
	text := ""
	if len(res.Content) > 0 {
		if tc, isText := res.Content[0].(*mcp.TextContent); isText {
			text = tc.Text
		}
	}
	if res.IsError {
		s.x.t.Fatalf("%s failed: %s", name, text)
	}
	if into != nil {
		if err := json.Unmarshal([]byte(text), into); err != nil {
			s.x.t.Fatalf("%s answered %q: %v", name, text, err)
		}
	}
}

func TestMCPToolsAreAllListed(t *testing.T) {
	x := newHarness(t)
	s := connectMCP(t, x)
	var names []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := s.cs.ListTools(context.Background(), nil)
		if err == nil {
			for _, tool := range res.Tools {
				names = append(names, tool.Name)
			}
		}
	}()
	x.pump(20*time.Second, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	sort.Strings(names)
	listed := slices.Clone(mcpToolNames)
	sort.Strings(listed)
	if !slices.Equal(names, listed) {
		t.Fatalf("server tools %v\nlisted in the copied instructions %v", names, listed)
	}
	text := x.a.mcpConnection()
	if !strings.Contains(text, x.a.services.mcpURL) || !strings.Contains(text, "claude mcp add") {
		t.Fatalf("connection text %s", text)
	}
}

func TestAnAIEditsAndExportsThroughMCP(t *testing.T) {
	x := newHarness(t)
	s := connectMCP(t, x)
	dir := x.mediaDir()
	a := clipFile(t, dir, "a.mp4", "testsrc2=size=640x360", "4")
	b := clipFile(t, dir, "b.mp4", "color=c=blue:size=640x360", "2")

	var st stateView
	s.ok("open_video", map[string]any{"path": a}, &st)
	if st.Project == nil || math.Abs(st.Project.Duration-4) > 0.05 {
		t.Fatalf("open_video state %+v", st.Project)
	}
	s.ok("open_video", map[string]any{"path": b, "placement": "append"}, &st)
	if len(st.Project.Video[0].Clips) != 2 || math.Abs(st.Project.Duration-6) > 0.05 {
		t.Fatalf("append %+v", st.Project.Video)
	}
	var pv projectView
	s.ok("split", map[string]any{"time": 2}, &pv)
	clips := pv.Video[0].Clips
	if len(clips) != 3 {
		t.Fatalf("split left %d clips", len(clips))
	}
	s.ok("set_clip", map[string]any{"id": clips[1].ID, "speed": 2}, nil)
	s.ok("delete_item", map[string]any{"id": clips[2].ID}, &pv)
	if math.Abs(pv.Duration-3) > 0.05 {
		t.Fatalf("after speed and delete the edit is %v s", pv.Duration)
	}
	var added map[string]int
	s.ok("add_text", map[string]any{"text": "Hello", "start": 0.5, "end": 2.5, "y": 0.2, "color": "#ffd60a", "background": true}, &added)
	s.ok("get_state", nil, &st)
	o := st.Project.Overlays[0].Items[0]
	if o.ID != added["id"] || o.Text != "Hello" || o.Color != "#ffd60a" || !o.Background || math.Abs(o.End-2.5) > 1e-9 || math.Abs(o.Y-0.2) > 1e-9 {
		t.Fatalf("overlay %+v", o)
	}
	s.ok("set_track", map[string]any{"kind": "video", "index": 0, "volume": 0.5}, &pv)
	if pv.Video[0].Volume != 0.5 {
		t.Fatal("set_track did not change the volume")
	}
	if res := s.call("move_item", map[string]any{"id": 99999, "track": 0, "start": 0}); !res.IsError {
		t.Fatal("moving a missing item should fail")
	}

	frame := s.call("frame_at", map[string]any{"time": 1})
	img, isImage := frame.Content[0].(*mcp.ImageContent)
	if frame.IsError || !isImage {
		t.Fatalf("frame_at %+v", frame.Content)
	}
	pic, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil || pic.Bounds().Dx() != 640 {
		t.Fatalf("frame %v %v", pic.Bounds(), err)
	}
	if shot := s.call("screenshot", nil); shot.IsError {
		t.Fatal("screenshot failed")
	}

	s.ok("undo", nil, nil)
	s.ok("get_state", nil, &st)
	if st.Project.Video[0].Volume != 1 {
		t.Fatal("undo did not revert set_track")
	}
	var options struct {
		Presets []map[string]string `json:"presets"`
	}
	s.ok("export_options", nil, &options)
	if len(options.Presets) != len(exportPresets) {
		t.Fatalf("export_options %+v", options)
	}
	var set exportSettings
	s.ok("set_export", map[string]any{"preset": "strong", "audio": "voice"}, &set)
	if set.Preset != "custom" || set.Height != 720 || set.FPS != 30 || set.Codec != "h265" || set.Audio != "voice" {
		t.Fatalf("set_export %+v", set)
	}
	s.ok("set_export", map[string]any{"preset": "extreme"}, &set)
	if set.Preset != "extreme" || set.Height != 480 || set.Quality != "smallest" {
		t.Fatalf("set_export %+v", set)
	}
	if res := s.call("set_export", map[string]any{"codec": "mpeg2"}); !res.IsError {
		t.Fatal("an unknown codec was accepted")
	}
	var out struct {
		Output   string         `json:"output"`
		Settings exportSettings `json:"settings"`
	}
	s.ok("export", map[string]any{"preset": "balanced", "height": 180, "quality": "small"}, &out)
	if out.Settings.Height != 180 || out.Settings.Quality != "small" || out.Settings.Codec != "h264" {
		t.Fatalf("export settings %+v", out.Settings)
	}
	var status map[string]any
	deadline := time.Now().Add(time.Minute)
	for {
		s.ok("export_status", nil, &status)
		if status["running"] == false || time.Now().After(deadline) {
			break
		}
	}
	if status["error"] != nil || status["output"] != out.Output {
		t.Fatalf("export status %+v", status)
	}
	info, err := media.Probe(context.Background(), out.Output)
	if err != nil || info.Height != 180 || math.Abs(info.Duration-3) > 0.15 {
		t.Fatalf("exported %+v %v", info, err)
	}
	var exports []map[string]any
	s.ok("list_exports", nil, &exports)
	if len(exports) != 1 || exports[0]["path"] != out.Output {
		t.Fatalf("list_exports %+v", exports)
	}
	if x.a.settings.Export.Height != 180 {
		t.Fatal("the export settings were not remembered")
	}
}
