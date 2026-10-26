package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-video-cut/internal/project"
)

var errNoProject = errors.New("the active tab has no video; call open_video first")

type contextT = context.Context

func parseColor(s string) ([3]uint8, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		return [3]uint8{}, fmt.Errorf("color %q is not #rrggbb", s)
	}
	return [3]uint8{uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

// edit applies fn to the active project as one undo step.
func (a *App) edit(fn func(p *project.Project) error) error {
	if a.p == nil {
		return errNoProject
	}
	var err error
	a.change(func() { err = fn(a.p) })
	return err
}

func (a *App) addEditTools(s *mcp.Server) {
	type openIn struct {
		Path      string  `json:"path" jsonschema:"absolute path of the video file"`
		Placement string  `json:"placement,omitempty" jsonschema:"new_tab (default when the active tab is empty), append (end of the main track) or upper (over the picture from at)"`
		At        float64 `json:"at,omitempty" jsonschema:"start time for placement upper"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "open_video", Description: "Open a video: in a new tab, appended to the main track, or on an upper track that covers the picture."},
		func(ctx contextT, req *mcp.CallToolRequest, in openIn) (*mcp.CallToolResult, any, error) {
			_, err := call(a, func() (any, error) {
				if !a.tools.ready {
					text, _ := a.toolsStatus()
					return nil, errors.New(text)
				}
				switch in.Placement {
				case "", "append":
				case "new_tab":
					if a.p != nil {
						a.newTab()
					}
				case "upper":
					if a.p == nil {
						return nil, errNoProject
					}
					a.seek(in.At)
				default:
					return nil, fmt.Errorf("unknown placement %q", in.Placement)
				}
				if a.opening != "" {
					return nil, errors.New("another file is still opening")
				}
				a.openVideo(in.Path, in.Placement == "upper")
				return nil, nil
			})
			if err == nil {
				err = waitFor(a, 2*time.Minute, func() (bool, error) { return a.opening == "", nil })
			}
			return result(call(a, func() (any, error) {
				if err != nil {
					return nil, err
				}
				if a.p == nil {
					return nil, fmt.Errorf("%s could not be opened", filepath.Base(in.Path))
				}
				return a.state(), nil
			}))
		})

	type splitIn struct {
		Time   float64 `json:"time" jsonschema:"where to cut, in seconds"`
		ItemID int     `json:"item_id,omitempty" jsonschema:"clip, music or overlay to cut; the main-track clip at time when omitted"`
	}
	tool(s, a, "split", "Cut a clip, music clip or overlay in two at a time.", func(in splitIn) (any, error) {
		err := a.edit(func(p *project.Project) error {
			ok := false
			switch {
			case in.ItemID == 0:
				i, _ := p.Locate(in.Time)
				ok = p.SplitClip(p.Video[0].Clips[i].ID, in.Time)
			default:
				ok = p.SplitClip(in.ItemID, in.Time) || p.SplitMusic(in.ItemID, in.Time) || p.SplitOverlay(in.ItemID, in.Time)
			}
			if !ok {
				return errors.New("cannot split there: no such item or too close to its ends")
			}
			return nil
		})
		return a.state().Project, err
	})
	type idIn struct {
		ID int `json:"id" jsonschema:"item id from get_state"`
	}
	tool(s, a, "delete_item", "Delete a clip, music clip or overlay. Main-track clips close up; the last main clip cannot be deleted.", func(in idIn) (any, error) {
		err := a.edit(func(p *project.Project) error {
			if p.DeleteClip(in.ID) || p.DeleteMusic(in.ID) || p.DeleteOverlay(in.ID) {
				return nil
			}
			return fmt.Errorf("no deletable item %d", in.ID)
		})
		return a.state().Project, err
	})
	type moveIn struct {
		ID    int     `json:"id"`
		Track int     `json:"track" jsonschema:"track index within the item's kind; the number of tracks adds a new one; video track 0 is the main track"`
		Start float64 `json:"start" jsonschema:"wanted start time; on the main track the clip is inserted between the clips nearest it"`
	}
	tool(s, a, "move_item", "Move a clip, music clip or overlay to a track and time; it slides into the nearest free gap.", func(in moveIn) (any, error) {
		err := a.edit(func(p *project.Project) error {
			if _, _, ok := p.FindClip(in.ID); ok {
				if !p.MoveClip(in.ID, in.Track, in.Start) {
					return errors.New("that move is not possible")
				}
				return nil
			}
			if _, _, ok := p.FindMusic(in.ID); ok {
				p.MoveMusic(in.ID, in.Track, in.Start)
				return nil
			}
			if _, _, ok := p.FindOverlay(in.ID); ok {
				p.MoveOverlay(in.ID, in.Track, in.Start)
				return nil
			}
			return fmt.Errorf("no item %d", in.ID)
		})
		return a.state().Project, err
	})
	type trimIn struct {
		ID    int      `json:"id"`
		Start *float64 `json:"start,omitempty" jsonschema:"new start time of the item on the timeline"`
		End   *float64 `json:"end,omitempty" jsonschema:"new end time of the item on the timeline"`
	}
	tool(s, a, "trim_item", "Move the start or end of a clip, music clip or overlay, keeping within the file and its neighbours.", func(in trimIn) (any, error) {
		err := a.edit(func(p *project.Project) error {
			if c, ok := p.Clip(in.ID); ok {
				if in.Start != nil {
					p.SetIn(in.ID, c.In+(*in.Start-c.Start)*c.Speed)
				}
				if c, _ = p.Clip(in.ID); in.End != nil {
					p.SetOut(in.ID, c.Out+(*in.End-c.End())*c.Speed)
				}
				return nil
			}
			if _, _, ok := p.FindMusic(in.ID); ok {
				if in.Start != nil {
					p.TrimMusicStart(in.ID, *in.Start)
				}
				if in.End != nil {
					p.TrimMusicEnd(in.ID, *in.End)
				}
				return nil
			}
			if o, ok := p.Overlay(in.ID); ok {
				start, end := o.Start, o.End
				if in.Start != nil {
					start = *in.Start
				}
				if in.End != nil {
					end = *in.End
				}
				p.SetOverlayRange(in.ID, start, end)
				return nil
			}
			return fmt.Errorf("no item %d", in.ID)
		})
		return a.state().Project, err
	})
	type clipIn struct {
		ID      int      `json:"id" jsonschema:"video clip id"`
		Speed   *float64 `json:"speed,omitempty" jsonschema:"0.25 to 4"`
		Volume  *float64 `json:"volume,omitempty" jsonschema:"gain of the clip's own sound, 0 to 2"`
		Opacity *float64 `json:"opacity,omitempty" jsonschema:"0 to 1; on the main track it fades to black, above it the tracks below show through"`
	}
	tool(s, a, "set_clip", "Change a video clip's speed, the volume of its own sound or its opacity.", func(in clipIn) (any, error) {
		err := a.edit(func(p *project.Project) error {
			if _, ok := p.Clip(in.ID); !ok {
				return fmt.Errorf("no video clip %d", in.ID)
			}
			if in.Speed != nil && !p.SetSpeed(in.ID, *in.Speed) {
				return errors.New("no room on this track for the slower clip")
			}
			if in.Volume != nil {
				p.SetClipVolume(in.ID, *in.Volume)
			}
			if in.Opacity != nil {
				p.SetOpacity([]int{in.ID}, *in.Opacity)
			}
			return nil
		})
		return a.state().Project, err
	})
	type idsIn struct {
		IDs []int `json:"ids" jsonschema:"ids of clips, music clips and overlays from get_state"`
	}
	tool(s, a, "delete_items", "Delete several clips, music clips and overlays in one undoable step. Nothing is deleted when it would empty the main track.",
		func(in idsIn) (any, error) {
			if err := a.knownItems(in.IDs); err != nil {
				return nil, err
			}
			var n int
			err := a.edit(func(p *project.Project) error {
				var err error
				n, err = p.DeleteItems(in.IDs)
				return err
			})
			return map[string]any{"deleted": n, "project": a.state().Project}, err
		})
	type itemsIn struct {
		IDs     []int    `json:"ids" jsonschema:"ids of clips, music clips and overlays from get_state"`
		Volume  *float64 `json:"volume,omitempty" jsonschema:"0 to 2, for the items with sound (video and music clips)"`
		Opacity *float64 `json:"opacity,omitempty" jsonschema:"0 to 1, for the items with a picture (video clips and overlays)"`
	}
	tool(s, a, "set_items", "Set the volume and/or opacity of several items at once, in one undoable step; items without sound or picture are skipped.",
		func(in itemsIn) (any, error) {
			if err := a.knownItems(in.IDs); err != nil {
				return nil, err
			}
			if in.Volume == nil && in.Opacity == nil {
				return nil, errors.New("give volume, opacity or both")
			}
			changed := map[string]int{}
			err := a.edit(func(p *project.Project) error {
				if in.Volume != nil {
					changed["volume"] = p.SetVolumes(in.IDs, *in.Volume)
				}
				if in.Opacity != nil {
					changed["opacity"] = p.SetOpacity(in.IDs, *in.Opacity)
				}
				return nil
			})
			return map[string]any{"changed": changed, "project": a.state().Project}, err
		})
	type trackIn struct {
		Kind   string   `json:"kind" jsonschema:"video, music or overlay"`
		Index  int      `json:"index"`
		Volume *float64 `json:"volume,omitempty" jsonschema:"track gain 0 to 2 (video and music)"`
		Muted  *bool    `json:"muted,omitempty" jsonschema:"video and music"`
		Hidden *bool    `json:"hidden,omitempty" jsonschema:"overlay tracks"`
	}
	tool(s, a, "set_track", "Set a whole track's volume or mute it, or hide an overlay track.", func(in trackIn) (any, error) {
		err := a.edit(func(p *project.Project) error {
			switch in.Kind {
			case "video", "music":
				music := in.Kind == "music"
				if music && (in.Index < 0 || in.Index >= len(p.Music)) || !music && (in.Index < 0 || in.Index >= len(p.Video)) {
					return fmt.Errorf("no %s track %d", in.Kind, in.Index)
				}
				vol, muted := a.trackVolume(music, in.Index)
				if in.Volume != nil {
					vol = *in.Volume
				}
				if in.Muted != nil {
					muted = *in.Muted
				}
				p.SetTrackVolume(music, in.Index, vol, muted)
			case "overlay":
				if in.Index < 0 || in.Index >= len(p.Overlays) {
					return fmt.Errorf("no overlay track %d", in.Index)
				}
				if in.Hidden != nil {
					p.Overlays[in.Index].Hidden = *in.Hidden
				}
			default:
				return fmt.Errorf("unknown track kind %q", in.Kind)
			}
			return nil
		})
		return a.state().Project, err
	})
	a.addOverlayTools(s)
}

type overlayIn struct {
	ID         int      `json:"id,omitempty" jsonschema:"overlay id (update_overlay only)"`
	Text       *string  `json:"text,omitempty" jsonschema:"caption text; new lines allowed"`
	Path       string   `json:"path,omitempty" jsonschema:"image file (add_image only)"`
	Start      *float64 `json:"start,omitempty" jsonschema:"seconds"`
	End        *float64 `json:"end,omitempty" jsonschema:"seconds"`
	X          *float64 `json:"x,omitempty" jsonschema:"centre, 0 (left) to 1 (right)"`
	Y          *float64 `json:"y,omitempty" jsonschema:"centre, 0 (top) to 1 (bottom)"`
	Size       *float64 `json:"size,omitempty" jsonschema:"text line height or image height as a fraction of the frame height"`
	Color      *string  `json:"color,omitempty" jsonschema:"text colour #rrggbb"`
	Outline    *bool    `json:"outline,omitempty"`
	Background *bool    `json:"background,omitempty" jsonschema:"dark box behind the text"`
	Opacity    *float64 `json:"opacity,omitempty" jsonschema:"0 (invisible) to 1 (solid)"`
}

func (in overlayIn) apply(p *project.Project, id int) error {
	var colour [3]uint8
	if in.Color != nil {
		c, err := parseColor(*in.Color)
		if err != nil {
			return err
		}
		colour = c
	}
	p.UpdateOverlay(id, func(o *project.Overlay) {
		if in.Text != nil && o.Kind == project.TextOverlay {
			o.Body = *in.Text
		}
		if in.X != nil {
			o.X = *in.X
		}
		if in.Y != nil {
			o.Y = *in.Y
		}
		if in.Size != nil {
			o.Size = min(max(*in.Size, 0.02), 1)
		}
		if in.Color != nil {
			o.Color = colour
		}
		if in.Outline != nil {
			o.Outline = *in.Outline
		}
		if in.Background != nil {
			o.Box = *in.Background
		}
		if in.Opacity != nil {
			o.Opacity = min(max(*in.Opacity, 0), 1)
		}
	})
	if in.Start != nil || in.End != nil {
		o, _ := p.Overlay(id)
		start, end := o.Start, o.End
		if in.Start != nil {
			start = *in.Start
		}
		if in.End != nil {
			end = *in.End
		}
		p.SetOverlayRange(id, start, end)
	}
	return nil
}

func (a *App) addOverlayTools(s *mcp.Server) {
	tool(s, a, "add_text", "Add a caption at start (default: the playhead) for three seconds unless end is given. Returns its id.", func(in overlayIn) (any, error) {
		if in.Text == nil || strings.TrimSpace(*in.Text) == "" {
			return nil, errors.New("text is required")
		}
		id := 0
		err := a.edit(func(p *project.Project) error {
			at := a.playhead
			if in.Start != nil {
				at = *in.Start
			}
			id = p.AddText(*in.Text, at)
			in.Start = nil
			return in.apply(p, id)
		})
		return map[string]int{"id": id}, err
	})
	tool(s, a, "add_image", "Add an image file over the picture at start (default: the playhead) for five seconds unless end is given. Returns its id.", func(in overlayIn) (any, error) {
		if a.p == nil {
			return nil, errNoProject
		}
		img, err := a.media.image(in.Path)
		if err != nil {
			return nil, err
		}
		id := 0
		err = a.edit(func(p *project.Project) error {
			at := a.playhead
			if in.Start != nil {
				at = *in.Start
			}
			id = p.AddImage(in.Path, float64(img.Rect.Dx())/float64(img.Rect.Dy()), at)
			in.Start = nil
			return in.apply(p, id)
		})
		return map[string]int{"id": id}, err
	})
	tool(s, a, "update_overlay", "Change a caption's text, colour, outline, background, or any overlay's position, size or time.", func(in overlayIn) (any, error) {
		err := a.edit(func(p *project.Project) error {
			if _, ok := p.Overlay(in.ID); !ok {
				return fmt.Errorf("no overlay %d", in.ID)
			}
			return in.apply(p, in.ID)
		})
		return a.state().Project, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "screenshot", Description: "A PNG of the editor window as it is now."},
		func(ctx contextT, req *mcp.CallToolRequest, in none) (*mcp.CallToolResult, any, error) {
			data, err := call(a, func() ([]byte, error) {
				if a.u.c == nil {
					return nil, errors.New("the window has not been drawn yet")
				}
				var b bytes.Buffer
				err := png.Encode(&b, a.u.c.Img)
				return b.Bytes(), err
			})
			if err != nil {
				return errorResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: "image/png"}}}, nil, nil
		})
}

func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

// result turns a value or error from call into a JSON tool answer.
func result(v any, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return errorResult(err), nil, nil
	}
	data, err := jsonText(v)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: data}}}, nil, nil
}

// knownItems checks that every id names an item of the active edit.
func (a *App) knownItems(ids []int) error {
	if a.p == nil {
		return errNoProject
	}
	if len(ids) == 0 {
		return errors.New("give at least one id")
	}
	for _, id := range ids {
		if !a.p.Has(id) {
			return fmt.Errorf("no clip, music clip or overlay %d", id)
		}
	}
	return nil
}
