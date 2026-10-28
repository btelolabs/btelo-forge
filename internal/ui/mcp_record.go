package ui

import (
	"context"
	"errors"
	"image"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// recordingView is the recording's state for the MCP tools.
func (a *App) recordingView() map[string]any {
	r := &a.rec
	v := map[string]any{"supported": a.recorder() != nil, "recording": r.rec != nil, "starting": r.starting, "saving": r.stopping}
	if reason, checking := a.recordBlocker(); reason != "" {
		v["unavailable"] = reason
	} else if checking {
		v["checking"] = true
	}
	if r.rec != nil {
		st := r.rec.Status()
		v["path"] = r.rec.Path()
		v["paused"] = st.Paused
		v["seconds"] = st.Elapsed.Seconds()
		v["bytes"] = st.Bytes
	}
	return v
}

// awaitUI waits on a server goroutine for work started on the UI goroutine to report back.
func awaitUI[T any](ctx context.Context, result chan T, timeout time.Duration) (T, error) {
	select {
	case v := <-result:
		return v, nil
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-time.After(timeout):
		var zero T
		return zero, errBusy
	}
}

type pathResult struct {
	path string
	err  error
}

func (a *App) addRecordTools(s *mcp.Server) {
	tool(s, a, "recording_status", "Whether the screen is being recorded, and the screens and microphones that can be recorded with their indexes.", func(none) (any, error) {
		v := a.recordingView()
		var screens []map[string]any
		for i, d := range a.rec.displays {
			screens = append(screens, map[string]any{"index": i, "name": a.displayLabel(d, i), "width": d.Width, "height": d.Height, "main": d.Primary})
		}
		var mics []map[string]any
		for _, m := range a.rec.mics {
			mics = append(mics, map[string]any{"id": m.ID, "name": m.Name, "default": m.Default})
		}
		v["screens"], v["microphones"], v["settings"] = screens, mics, a.settings.Record
		keys := map[string]string{}
		for i, name := range []string{"record", "pause", "screenshot", "draw"} {
			keys[name] = a.hotkeyLabel(i)
		}
		v["hotkeys"] = keys
		if !a.rec.listed && !a.rec.listing && a.recorder() != nil {
			a.checkRecording()
			v["note"] = "the screens and microphones are being looked up; ask again in a moment"
		}
		return v, nil
	})

	type startIn struct {
		Screen       *int   `json:"screen,omitempty" jsonschema:"index from recording_status; the last used screen when left out"`
		FPS          int    `json:"fps,omitempty" jsonschema:"30 or 60 frames a second; the last used rate when left out"`
		Cursor       *bool  `json:"cursor,omitempty" jsonschema:"show the mouse pointer"`
		SystemAudio  *bool  `json:"system_audio,omitempty" jsonschema:"record the sound the computer plays"`
		Microphone   *bool  `json:"microphone,omitempty" jsonschema:"record a microphone"`
		MicrophoneID string `json:"microphone_id,omitempty" jsonschema:"id from recording_status; the system default when left out"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "start_recording", Description: "Start recording the screen, with sound, into the folder exports go to (the user's videos folder when exports go beside the original). Changed options are remembered as the user's choice. The app window minimizes unless the user turned that off; the user can stop with the recording hotkey (see recording_status). Call stop_recording to finish and open the recording for editing."},
		func(ctx context.Context, req *mcp.CallToolRequest, in startIn) (*mcp.CallToolResult, any, error) {
			done := make(chan pathResult, 1)
			_, err := call(a, func() (any, error) {
				if a.recorder() == nil {
					return nil, errors.New("screen recording is not available on this system")
				}
				if !a.rec.listed {
					return nil, errors.New("call recording_status first so the screens are looked up")
				}
				s := &a.settings.Record
				if in.Screen != nil {
					if *in.Screen < 0 || *in.Screen >= len(a.rec.displays) {
						return nil, errors.New("no screen has that index; see recording_status")
					}
					s.Display = a.rec.displays[*in.Screen].Key()
				}
				switch in.FPS {
				case 0:
				case 30, 60:
					s.FPS = in.FPS
				default:
					return nil, errors.New("fps must be 30 or 60")
				}
				for _, o := range []struct {
					in  *bool
					out *bool
				}{{in.Cursor, &s.Cursor}, {in.SystemAudio, &s.SystemAudio}, {in.Microphone, &s.Microphone}} {
					if o.in != nil {
						*o.out = *o.in
					}
				}
				if in.MicrophoneID != "" {
					s.MicrophoneID = in.MicrophoneID
				}
				a.saveSettings()
				a.startRecording(func(path string, err error) { done <- pathResult{path, err} })
				return nil, nil
			})
			if err != nil {
				return errorResult(err), nil, nil
			}
			r, err := awaitUI(ctx, done, time.Minute)
			if err == nil {
				err = r.err
			}
			if err != nil {
				return errorResult(err), nil, nil
			}
			return result(map[string]any{"recording": true, "path": r.path}, nil)
		})

	type pauseIn struct {
		Paused bool `json:"paused" jsonschema:"true pauses the recording, false resumes it"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "pause_recording", Description: "Pause or resume the screen recording; the paused time is left out of the video."},
		func(ctx context.Context, req *mcp.CallToolRequest, in pauseIn) (*mcp.CallToolResult, any, error) {
			done := make(chan error, 1)
			if _, err := call(a, func() (any, error) {
				a.pauseRecording(in.Paused, func(err error) { done <- err })
				return nil, nil
			}); err != nil {
				return errorResult(err), nil, nil
			}
			err, waitErr := awaitUI(ctx, done, time.Minute)
			if err = errors.Join(err, waitErr); err != nil {
				return errorResult(err), nil, nil
			}
			v, _ := call(a, func() (any, error) { return a.recordingView(), nil })
			return result(v, nil)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "take_screenshot", Description: "Save a PNG of the screen chosen for recording (see recording_status) into the folder recordings go to; returns the file, which add_image can put on the edit."},
		func(ctx context.Context, req *mcp.CallToolRequest, in none) (*mcp.CallToolResult, any, error) {
			done := make(chan pathResult, 1)
			if _, err := call(a, func() (any, error) {
				a.takeScreenshot(image.Rectangle{}, func(path string, err error) { done <- pathResult{path, err} })
				return nil, nil
			}); err != nil {
				return errorResult(err), nil, nil
			}
			r, err := awaitUI(ctx, done, time.Minute)
			if err == nil {
				err = r.err
			}
			if err != nil {
				return errorResult(err), nil, nil
			}
			return result(map[string]any{"path": r.path}, nil)
		})

	mcp.AddTool(s, &mcp.Tool{Name: "stop_recording", Description: "Stop the screen recording, save it as an MP4 and open it in a tab for editing; returns the file."},
		func(ctx context.Context, req *mcp.CallToolRequest, in none) (*mcp.CallToolResult, any, error) {
			done := make(chan pathResult, 1)
			if _, err := call(a, func() (any, error) {
				a.stopRecording(func(path string, err error) { done <- pathResult{path, err} })
				return nil, nil
			}); err != nil {
				return errorResult(err), nil, nil
			}
			r, err := awaitUI(ctx, done, joinWait)
			if err == nil {
				err = r.err
			}
			if err != nil {
				return errorResult(err), nil, nil
			}
			return result(map[string]any{"path": r.path, "opened": true}, nil)
		})
}
