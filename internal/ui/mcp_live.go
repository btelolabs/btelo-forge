package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// liveView is the stream's state for the MCP tools. Stream keys are never in it: only whether a destination has one.
func (a *App) liveView() map[string]any {
	l := &a.onAir
	s := a.settings.Live
	canvas := s.canvas()
	v := map[string]any{
		"supported": capture.Supported(), "live": l.stream != nil, "starting": l.starting, "stopping": l.stopping,
		"canvas": fmt.Sprintf("%dx%d", canvas.X, canvas.Y), "fps": s.fps(), "kbps": s.kbps(),
		"system_audio": s.SystemAudio, "microphone": s.Microphone, "record_when_starting": s.Record,
	}
	if reason := a.liveBlocker(); reason != "" && l.stream == nil {
		v["cannot_start"] = reason
	}
	var notes []string
	for _, n := range a.liveNotes() {
		notes = append(notes, n.text)
	}
	v["notes"] = notes
	var dests []map[string]any
	for _, d := range s.Destinations {
		dv := map[string]any{"id": d.ID, "platform": d.Platform, "name": a.destinationName(d), "server": d.Server, "has_key": d.Key != "", "enabled": d.On}
		if p := a.destinationProblem(d); p != "" {
			dv["problem"] = p
		}
		if t, ok := a.targetStatus(d.ID); ok {
			text, _ := a.describeTarget(t)
			dv["state"], dv["kbps"] = text, t.Kbps
		}
		dests = append(dests, dv)
	}
	v["destinations"] = dests
	if l.stream != nil {
		st := l.stream.Status()
		v["seconds"], v["encoder_kbps"], v["showing_screen"] = st.Elapsed.Seconds(), st.Kbps, st.Screen
		if st.ScreenErr != nil {
			v["screen_error"] = st.ScreenErr.Error()
		}
	}
	return v
}

func (a *App) addLiveTools(s *mcp.Server) {
	tool(s, a, "stream_status", "The live stream: whether it is live, its canvas, frame rate and bit rate, the destinations (with their state while live; stream keys are never shown, only whether one is set) and the platforms that can be added (from the streaming_platforms configuration).", func(none) (any, error) {
		v := a.liveView()
		var platforms []map[string]any
		lang := string(a.u.lang)
		for _, p := range a.onAir.platforms.Platforms {
			var servers []map[string]string
			for _, sv := range p.Servers {
				servers = append(servers, map[string]string{"name": sv.Name.In(lang), "url": sv.URL})
			}
			platforms = append(platforms, map[string]any{"id": p.ID, "name": p.Name.In(lang), "servers": servers, "help": p.Help.In(lang),
				"page": p.Page, "max_kbps": p.MaxKbps, "portrait": p.Portrait})
		}
		v["platforms"] = platforms
		return v, nil
	})

	type destIn struct {
		ID       string `json:"id,omitempty" jsonschema:"a destination's id from stream_status to change it; left out adds a new one"`
		Platform string `json:"platform,omitempty" jsonschema:"a platform id from stream_status; needed for a new destination"`
		Name     string `json:"name,omitempty" jsonschema:"what to call it; the platform's name when left out"`
		Server   string `json:"server,omitempty" jsonschema:"the rtmp://, rtmps:// or srt:// server address; the platform's first server when left out"`
		Key      string `json:"key,omitempty" jsonschema:"the stream key the user gave; it is stored encrypted and never shown again"`
		Enabled  *bool  `json:"enabled,omitempty" jsonschema:"whether to stream to it; while live it joins or leaves the stream at once"`
	}
	tool(s, a, "set_stream_destination", "Add a place to stream to, or change one: its platform, server address, stream key, name, and whether it is streamed to. Every enabled destination gets the same stream. Returns the destination without its key.", func(in destIn) (any, error) {
		ds := a.settings.Live.Destinations
		i := slices.IndexFunc(ds, func(d liveDestination) bool { return d.ID == in.ID })
		var d liveDestination
		switch {
		case in.ID != "" && i < 0:
			return nil, errors.New("no destination has that id; see stream_status")
		case i >= 0:
			d = ds[i]
		default:
			if in.Platform == "" {
				return nil, errors.New("give the platform of the new destination; see stream_status for the ids")
			}
			d = liveDestination{On: true}
		}
		if in.Platform != "" {
			p, ok := a.onAir.platforms.Find(in.Platform)
			if !ok {
				return nil, fmt.Errorf("no platform %q is configured; see stream_status, or add it to streaming_platforms with set_config", in.Platform)
			}
			if p.ID != d.Platform && in.Server == "" && len(p.Servers) > 0 {
				d.Server = p.Servers[0].URL
			}
			d.Platform = p.ID
		}
		if in.Server != "" {
			d.Server = strings.TrimSpace(in.Server)
		}
		if in.Name != "" {
			d.Name = strings.TrimSpace(in.Name)
		}
		if in.Key != "" {
			d.Key = sealedText(strings.TrimSpace(in.Key))
		}
		wantOn := d.On
		if in.Enabled != nil {
			wantOn = *in.Enabled
		}
		if i < 0 {
			d.On = wantOn
			if err := a.saveDestination(-1, d); err != nil {
				return nil, err
			}
			i = len(a.settings.Live.Destinations) - 1
		} else {
			if err := a.saveDestination(i, d); err != nil {
				return nil, err
			}
			if err := a.setDestinationOn(i, wantOn); err != nil {
				return nil, err
			}
		}
		saved := a.settings.Live.Destinations[i]
		return map[string]any{"id": saved.ID, "platform": saved.Platform, "name": a.destinationName(saved), "server": saved.Server,
			"has_key": saved.Key != "", "enabled": saved.On}, nil
	})

	type idIn struct {
		ID string `json:"id" jsonschema:"a destination's id from stream_status"`
	}
	tool(s, a, "remove_stream_destination", "Forget a destination and its stream key; while live it leaves the stream and the others go on.", func(in idIn) (any, error) {
		i := slices.IndexFunc(a.settings.Live.Destinations, func(d liveDestination) bool { return d.ID == in.ID })
		if i < 0 {
			return nil, errors.New("no destination has that id; see stream_status")
		}
		return map[string]any{"removed": in.ID}, a.removeDestination(i)
	})

	type optionsIn struct {
		Canvas      string `json:"canvas,omitempty" jsonschema:"1920x1080 or 1280x720 for landscape, 1080x1920 or 720x1280 upright for phone platforms"`
		FPS         int    `json:"fps,omitempty" jsonschema:"30 or 60"`
		Kbps        int    `json:"kbps,omitempty" jsonschema:"the picture's bit rate, 500 to 50000; 2500–8000 is usual"`
		SystemAudio *bool  `json:"system_audio,omitempty" jsonschema:"stream the sound the computer plays"`
		Microphone  *bool  `json:"microphone,omitempty" jsonschema:"stream the default or chosen microphone"`
		Record      *bool  `json:"record_when_starting,omitempty" jsonschema:"start recording the screen when the stream starts without a recording, so the screen is on the stream"`
	}
	tool(s, a, "set_stream_options", "Choose how the stream is made: its canvas, frame rate, bit rate and sound. They apply to the next stream; while live they cannot change.", func(in optionsIn) (any, error) {
		if a.onAir.stream != nil || a.onAir.starting {
			return nil, errors.New("end the stream to change how it is made")
		}
		s := a.settings.Live
		if in.Canvas != "" {
			var c image.Point
			if _, err := fmt.Sscanf(strings.ToLower(in.Canvas), "%dx%d", &c.X, &c.Y); err != nil || !slices.Contains(liveCanvases, c) {
				return nil, errors.New("canvas must be 1920x1080, 1280x720, 1080x1920 or 720x1280")
			}
			s.Width, s.Height = c.X, c.Y
		}
		if in.FPS != 0 {
			if !slices.Contains(liveRates, in.FPS) {
				return nil, errors.New("fps must be 30 or 60")
			}
			s.FPS = in.FPS
		}
		if in.Kbps != 0 {
			if in.Kbps < 500 || in.Kbps > 50000 {
				return nil, errors.New("kbps must be between 500 and 50000")
			}
			s.Kbps = in.Kbps
		}
		for _, o := range []struct{ in, out *bool }{{in.SystemAudio, &s.SystemAudio}, {in.Microphone, &s.Microphone}, {in.Record, &s.Record}} {
			if o.in != nil {
				*o.out = *o.in
			}
		}
		a.settings.Live = s
		a.saveSettings()
		return a.liveView(), nil
	})

	mcp.AddTool(s, &mcp.Tool{Name: "start_stream", Description: "Go live to every enabled destination at once. The stream shows the screen while it is recorded and black otherwise; unless record_when_starting is off, a recording starts with it (see start_recording). Stopping or restarting the recording never interrupts the stream. Check each destination's state with stream_status."},
		func(ctx context.Context, req *mcp.CallToolRequest, in none) (*mcp.CallToolResult, any, error) {
			done := make(chan error, 1)
			if _, err := call(a, func() (any, error) {
				a.startLive(func(err error) { done <- err })
				return nil, nil
			}); err != nil {
				return errorResult(err), nil, nil
			}
			err, waitErr := awaitUI(ctx, done, time.Minute)
			if err = errors.Join(err, waitErr); err != nil {
				return errorResult(err), nil, nil
			}
			v, _ := call(a, func() (any, error) { return a.liveView(), nil })
			return result(v, nil)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "stop_stream", Description: "End the live stream on every destination. A recording that is running goes on."},
		func(ctx context.Context, req *mcp.CallToolRequest, in none) (*mcp.CallToolResult, any, error) {
			done := make(chan error, 1)
			if _, err := call(a, func() (any, error) {
				a.stopLive(func(err error) { done <- err })
				return nil, nil
			}); err != nil {
				return errorResult(err), nil, nil
			}
			err, waitErr := awaitUI(ctx, done, time.Minute)
			if err = errors.Join(err, waitErr); err != nil {
				return errorResult(err), nil, nil
			}
			return result(map[string]any{"live": false}, nil)
		})
}
