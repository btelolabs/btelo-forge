package ui

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cameraView is the camera window's state for the MCP tools.
func (a *App) cameraView() map[string]any {
	c := &a.cam
	s := a.settings.Camera
	var cams []map[string]any
	chosen, _ := a.chosenCamera()
	for i, cam := range c.cams {
		cams = append(cams, map[string]any{"index": i, "name": cam.Name, "chosen": cam.ID == chosen.ID})
	}
	v := map[string]any{"shown": c.bubble != nil, "cameras": cams, "shape": cameraShapeNames[min(max(s.Shape, 0), len(cameraShapes)-1)],
		"corners_percent": s.Radius, "width": s.Width, "border": s.Border, "mirror": s.Mirror}
	if c.bubble != nil {
		r := c.bubble.o.Bounds()
		v["x"], v["y"], v["pixels"] = r.Min.X, r.Min.Y, []int{r.Dx(), r.Dy()}
	}
	if c.listErr != nil {
		v["error"] = a.describe(c.listErr)
	}
	return v
}

func (a *App) addCameraTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "camera_status", Description: "The camera window: whether the camera is shown on the screen (and so in recordings and live streams), the cameras found, and its shape, corners, size, border and place."},
		func(ctx context.Context, req *mcp.CallToolRequest, in none) (*mcp.CallToolResult, any, error) {
			done := make(chan struct{}, 1)
			if _, err := call(a, func() (any, error) {
				a.listCameras(func() { done <- struct{}{} })
				return nil, nil
			}); err != nil {
				return errorResult(err), nil, nil
			}
			awaitUI(ctx, done, 30*time.Second)
			return result(call(a, func() (any, error) { return a.cameraView(), nil }))
		})

	type cameraIn struct {
		Shown   *bool  `json:"shown,omitempty" jsonschema:"show the camera on the screen, or take it off"`
		Camera  *int   `json:"camera,omitempty" jsonschema:"index of a camera from camera_status"`
		Shape   string `json:"shape,omitempty" jsonschema:"Square, 4:3, 16:9, 3:4 upright or 9:16 upright"`
		Corners *int   `json:"corners_percent,omitempty" jsonschema:"corner radius as a percent of the shorter side, 0 to 50; a Square at 50 is a circle"`
		Width   int    `json:"width,omitempty" jsonschema:"width in logical pixels, 120 to 960"`
		Border  *int   `json:"border,omitempty" jsonschema:"white border in logical pixels, 0 to 12"`
		Mirror  *bool  `json:"mirror,omitempty" jsonschema:"flip left to right"`
		X       *int   `json:"x,omitempty" jsonschema:"left edge on the desktop, in physical pixels"`
		Y       *int   `json:"y,omitempty" jsonschema:"top edge on the desktop, in physical pixels"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "set_camera", Description: "Show the camera on the screen in a window of its own — cropped to a rounded rectangle or a circle, with an optional border — or take it off, and change its shape, size and place. Being on the screen, it is in recordings and live streams of the screen."},
		func(ctx context.Context, req *mcp.CallToolRequest, in cameraIn) (*mcp.CallToolResult, any, error) {
			done := make(chan error, 1)
			if _, err := call(a, func() (any, error) {
				s := &a.settings.Camera
				before := *s
				if in.Camera != nil {
					if *in.Camera < 0 || *in.Camera >= len(a.cam.cams) {
						return nil, errors.New("no camera has that index; see camera_status")
					}
					s.Camera = a.cam.cams[*in.Camera].ID
				}
				if in.Shape != "" {
					i := slices.Index(cameraShapeNames, in.Shape)
					if i < 0 {
						return nil, errors.New("shape must be Square, 4:3, 16:9, 3:4 upright or 9:16 upright")
					}
					s.Shape = i
				}
				if in.Corners != nil {
					if *in.Corners < 0 || *in.Corners > 50 {
						return nil, errors.New("corners_percent must be 0 to 50")
					}
					s.Radius = *in.Corners
				}
				if in.Width != 0 {
					if in.Width < cameraMinWidth || in.Width > cameraMaxWidth {
						return nil, errors.New("width must be 120 to 960")
					}
					s.Width = in.Width
				}
				if in.Border != nil {
					if *in.Border < 0 || *in.Border > cameraMaxBorder {
						return nil, errors.New("border must be 0 to 12")
					}
					s.Border = *in.Border
				}
				if in.Mirror != nil {
					s.Mirror = *in.Mirror
				}
				if in.X != nil || in.Y != nil {
					if !s.Placed && a.cam.bubble != nil {
						r := a.cam.bubble.o.Bounds()
						s.X, s.Y = r.Min.X, r.Min.Y
					}
					s.Placed = true
					if in.X != nil {
						s.X = *in.X
					}
					if in.Y != nil {
						s.Y = *in.Y
					}
					if b := a.cam.bubble; b != nil {
						b.mu.Lock()
						b.o.Move(a.cameraBounds(b.look.size))
						b.mu.Unlock()
					}
				}
				a.changeCamera(before)
				if b := a.cam.bubble; b != nil && !b.restartAt.IsZero() {
					b.restartAt = time.Time{}
					a.restartCamera()
				}
				if in.Shown != nil {
					a.showCamera(*in.Shown, func(err error) { done <- err })
				} else {
					done <- nil
				}
				return nil, nil
			}); err != nil {
				return errorResult(err), nil, nil
			}
			err, waitErr := awaitUI(ctx, done, 30*time.Second)
			if err = errors.Join(err, waitErr); err != nil {
				return errorResult(err), nil, nil
			}
			return result(call(a, func() (any, error) { return a.cameraView(), nil }))
		})
}
