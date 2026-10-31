package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"time"
)

// The services' ways of making something that takes minutes, each with its own requests. A task is submitted, asked
// after until it ends, and what it made fetched.
const (
	TaskMiniMax = "minimax-video" // MiniMax: text or a first frame to video
	TaskTripo   = "tripo"         // Tripo: a picture to a 3D model
	TaskMeshy   = "meshy"         // Meshy: a picture to a 3D model
	TaskSDVideo = "sd-video"      // sd-server of this computer: text or a first frame to video
)

// taskKinds is the kind of model each way of working makes.
var taskKinds = map[string]Kind{TaskMiniMax: KindVideo, TaskTripo: KindModel3D, TaskMeshy: KindModel3D, TaskSDVideo: KindVideo}

// TaskRequest is what a task is asked to make.
type TaskRequest struct {
	Prompt string
	Image  *Image // the picture it starts from; a 3D model needs one and a video may have one
	Video  VideoOptions
	// Version is the service's own name for how it makes the model, where it is not the model's name (Tripo's
	// model_version).
	Version string
}

// VideoOptions steer a video made on this computer; zero values leave the choice to the model.
type VideoOptions struct {
	Width, Height int
	Frames        int
	FPS           int
	Steps         int
}

// TaskResult is the file a task made.
type TaskResult struct {
	Data []byte
	Ext  string // with its dot
}

// taskState is how a task stands.
type taskState struct {
	done     bool
	fraction float64 // how far it has got, or -1 when the service does not say
	url      string  // where what it made is, once done
	data     []byte  // or what it made, for a service that sends it with the answer
	ext      string
}

// maxTaskFile bounds what is fetched of what a task made.
const maxTaskFile = 2 << 30

// taskPoll is how long to wait between asking after a task; taskLimit is how long one may take before it is given up
// on, and a service that stops answering is given taskFailures tries in a row.
var (
	taskPoll     = 3 * time.Second
	taskLimit    = 2 * time.Hour
	taskFailures = 5
)

// taskAPI is one way of working.
type taskAPI interface {
	submit(ctx context.Context, e Endpoint, req TaskRequest) (id string, err error)
	poll(ctx context.Context, e Endpoint, id string) (taskState, error)
	cancel(ctx context.Context, e Endpoint, id string)
}

func taskOf(api string, req TaskRequest) (taskAPI, bool) {
	switch api {
	case TaskMiniMax:
		return miniMaxVideo{}, true
	case TaskTripo:
		return tripo{}, true
	case TaskMeshy:
		return meshy{}, true
	case TaskSDVideo:
		return sdVideo{steps: req.Video.Steps}, true
	}
	return nil, false
}

// ErrNeedsPicture means a model that starts from a picture was asked without one.
var ErrNeedsPicture = errors.New("attach a picture for the model to work from")

// RunTask submits a task to the service, asks after it until it ends and returns what it made, reporting how far it
// has got to progress (a fraction, or -1 when the service does not say). When ctx is cancelled, the service is told.
func (e Endpoint) RunTask(ctx context.Context, api string, req TaskRequest, progress func(fraction float64)) (TaskResult, error) {
	t, ok := taskOf(api, req)
	if !ok {
		return TaskResult{}, fmt.Errorf("the app does not know the task interface %q", api)
	}
	if taskKinds[api] == KindModel3D && req.Image == nil {
		return TaskResult{}, ErrNeedsPicture
	}
	id, err := t.submit(ctx, e, req)
	if err != nil {
		return TaskResult{}, err
	}
	limit := time.NewTimer(taskLimit)
	defer limit.Stop()
	failures := 0
	for {
		st, err := t.poll(ctx, e, id)
		var refused *taskRefused
		switch {
		case err == nil:
			failures = 0
		case ctx.Err() != nil:
		case errors.As(err, &refused):
			// The service said the task failed or that the key is wrong: asking again changes nothing.
			return TaskResult{}, err
		default:
			if failures++; failures >= taskFailures {
				return TaskResult{}, err
			}
		}
		if err == nil {
			if st.done {
				progress(1)
				return e.taskFile(ctx, st)
			}
			progress(st.fraction)
		}
		select {
		case <-ctx.Done():
			// The task goes on costing money and memory unless it is told otherwise, and ctx is over by now.
			stop, end := context.WithTimeout(context.Background(), 10*time.Second)
			defer end()
			t.cancel(stop, e, id)
			return TaskResult{}, ctx.Err()
		case <-limit.C:
			return TaskResult{}, fmt.Errorf("the task did not finish in %v", taskLimit)
		case <-time.After(taskPoll):
		}
	}
}

// taskFile is what a finished task made.
func (e Endpoint) taskFile(ctx context.Context, st taskState) (TaskResult, error) {
	if st.data != nil {
		return TaskResult{Data: st.data, Ext: st.ext}, nil
	}
	data, err := e.download(ctx, st.url)
	if err != nil {
		return TaskResult{}, err
	}
	return TaskResult{Data: data, Ext: st.ext}, nil
}

// download fetches a file a service keeps at a signed address: the key does not go with it.
func (e Endpoint) download(ctx context.Context, address string) ([]byte, error) {
	if !validWebURL(address) {
		return nil, errors.New("the service gave an address that is not http or https")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("the file could not be fetched: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the file could not be fetched: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTaskFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTaskFile {
		return nil, errors.New("the file is too large")
	}
	if len(data) == 0 {
		return nil, errors.New("the file is empty")
	}
	return data, nil
}

func (e Endpoint) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

// taskRefused is a final answer from a service: asking again would get the same.
type taskRefused struct{ text string }

func (r *taskRefused) Error() string { return r.text }

// call sends a request with the key and reads its JSON answer into out, which may be nil.
func (e Endpoint) call(ctx context.Context, method, address, contentType string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if e.Key != "" {
		req.Header.Set("Authorization", "Bearer "+e.Key)
	}
	resp, err := e.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("could not reach the model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return refusal(resp)
	}
	if resp.StatusCode >= 300 {
		return &taskRefused{refusal(resp).Error()}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<30)).Decode(out); err != nil {
		return fmt.Errorf("the service's answer could not be read: %w", err)
	}
	return nil
}

func (e Endpoint) postJSON(ctx context.Context, address string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return e.call(ctx, http.MethodPost, address, "application/json", body, out)
}

func (e Endpoint) at(parts ...string) string {
	return strings.TrimSuffix(e.URL, "/") + "/" + strings.Join(parts, "/")
}

// dataURL is a picture in the form a JSON request carries it.
func dataURL(img Image) string {
	return "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

// extFromURL is the extension of the file at an address, or fallback when it has none.
func extFromURL(address, fallback string) string {
	if u, err := url.Parse(address); err == nil {
		if ext := strings.ToLower(path.Ext(u.Path)); ext != "" {
			return ext
		}
	}
	return fallback
}

// miniMaxVideo is MiniMax's video interface. See https://platform.minimax.io/docs/api-reference/video-generation-t2v.
type miniMaxVideo struct{}

type miniMaxStatus struct {
	Code int    `json:"status_code"`
	Text string `json:"status_msg"`
}

// check turns the service's own status into an error: it answers 200 to nearly everything.
func (s miniMaxStatus) check() error {
	switch s.Code {
	case 0:
		return nil
	case 1002: // too many requests: it passes
		return errors.New(s.Text)
	}
	return &taskRefused{fmt.Sprintf("the service refused: %s (%d)", s.Text, s.Code)}
}

func (miniMaxVideo) submit(ctx context.Context, e Endpoint, req TaskRequest) (string, error) {
	body := map[string]any{"model": e.Model, "prompt": req.Prompt}
	if req.Image != nil {
		body["first_frame_image"] = dataURL(*req.Image)
	}
	var out struct {
		TaskID string        `json:"task_id"`
		Base   miniMaxStatus `json:"base_resp"`
	}
	if err := e.postJSON(ctx, e.at("video_generation"), body, &out); err != nil {
		return "", err
	}
	if err := out.Base.check(); err != nil {
		return "", err
	}
	if out.TaskID == "" {
		return "", errors.New("the service gave no task")
	}
	return out.TaskID, nil
}

func (miniMaxVideo) poll(ctx context.Context, e Endpoint, id string) (taskState, error) {
	var out struct {
		Status string        `json:"status"`
		FileID string        `json:"file_id"`
		Base   miniMaxStatus `json:"base_resp"`
	}
	address := e.at("query", "video_generation") + "?task_id=" + url.QueryEscape(id)
	if err := e.call(ctx, http.MethodGet, address, "", nil, &out); err != nil {
		return taskState{}, err
	}
	if err := out.Base.check(); err != nil {
		return taskState{}, err
	}
	switch out.Status {
	case "Success":
		if out.FileID == "" {
			return taskState{}, &taskRefused{"the service finished without a file"}
		}
		var file struct {
			File struct {
				URL string `json:"download_url"`
			} `json:"file"`
			Base miniMaxStatus `json:"base_resp"`
		}
		address := e.at("files", "retrieve") + "?file_id=" + url.QueryEscape(out.FileID)
		if err := e.call(ctx, http.MethodGet, address, "", nil, &file); err != nil {
			return taskState{}, err
		}
		if err := file.Base.check(); err != nil {
			return taskState{}, err
		}
		if file.File.URL == "" {
			return taskState{}, &taskRefused{"the service gave no address for the file"}
		}
		return taskState{done: true, url: file.File.URL, ext: ".mp4"}, nil
	case "Fail":
		return taskState{}, &taskRefused{"the service could not make the video: " + out.Base.Text}
	}
	return taskState{fraction: -1}, nil
}

// cancel does nothing: the service has no way to stop a video.
func (miniMaxVideo) cancel(context.Context, Endpoint, string) {}

// tripo is Tripo's interface. See https://developers.tripo3d.com.
type tripo struct{}

type tripoAnswer[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func (a tripoAnswer[T]) check() error {
	if a.Code != 0 {
		return &taskRefused{fmt.Sprintf("the service refused: %s (%d)", a.Message, a.Code)}
	}
	return nil
}

// upload sends the picture, which the interface takes by a token only, and returns the token and the file's type.
func (tripo) upload(ctx context.Context, e Endpoint, img Image) (token, kind string, err error) {
	kind = strings.TrimPrefix(img.MIME, "image/")
	if kind == "jpeg" {
		kind = "jpg"
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="picture.%s"`, kind))
	header.Set("Content-Type", img.MIME)
	part, err := w.CreatePart(header)
	if err != nil {
		return "", "", err
	}
	if _, err := part.Write(img.Data); err != nil {
		return "", "", err
	}
	if err := w.Close(); err != nil {
		return "", "", err
	}
	var out tripoAnswer[struct {
		Token string `json:"file_token"`
	}]
	if err := e.call(ctx, http.MethodPost, e.at("files"), w.FormDataContentType(), body.Bytes(), &out); err != nil {
		return "", "", err
	}
	if err := out.check(); err != nil {
		return "", "", err
	}
	if out.Data.Token == "" {
		return "", "", errors.New("the service gave no token for the picture")
	}
	return out.Data.Token, kind, nil
}

func (t tripo) submit(ctx context.Context, e Endpoint, req TaskRequest) (string, error) {
	token, kind, err := t.upload(ctx, e, *req.Image)
	if err != nil {
		return "", err
	}
	version := req.Version
	if version == "" {
		version = e.Model
	}
	body := map[string]any{"file": map[string]string{"type": kind, "file_token": token}, "model_version": version}
	var out tripoAnswer[struct {
		TaskID string `json:"task_id"`
	}]
	if err := e.postJSON(ctx, e.at("generation", "image-to-model"), body, &out); err != nil {
		return "", err
	}
	if err := out.check(); err != nil {
		return "", err
	}
	if out.Data.TaskID == "" {
		return "", errors.New("the service gave no task")
	}
	return out.Data.TaskID, nil
}

func (tripo) poll(ctx context.Context, e Endpoint, id string) (taskState, error) {
	var out tripoAnswer[struct {
		Status   string  `json:"status"`
		Progress float64 `json:"progress"`
		Output   struct {
			URL string `json:"model_url"`
		} `json:"output"`
		ErrorMessage string `json:"error_message"`
	}]
	if err := e.call(ctx, http.MethodGet, e.at("tasks", url.PathEscape(id)), "", nil, &out); err != nil {
		return taskState{}, err
	}
	if err := out.check(); err != nil {
		return taskState{}, err
	}
	switch out.Data.Status {
	case "success":
		if out.Data.Output.URL == "" {
			return taskState{}, &taskRefused{"the service finished without a model"}
		}
		return taskState{done: true, url: out.Data.Output.URL, ext: extFromURL(out.Data.Output.URL, ".glb")}, nil
	case "failed", "cancelled":
		return taskState{}, &taskRefused{"the service could not make the model: " + out.Data.ErrorMessage}
	}
	return taskState{fraction: out.Data.Progress / 100}, nil
}

// cancel does nothing: the service has no way to stop a task.
func (tripo) cancel(context.Context, Endpoint, string) {}

// meshy is Meshy's interface. See https://docs.meshy.ai/api/image-to-3d.
type meshy struct{}

func (meshy) submit(ctx context.Context, e Endpoint, req TaskRequest) (string, error) {
	body := map[string]any{"image_url": dataURL(*req.Image), "ai_model": e.Model, "target_formats": []string{"glb"}}
	var out struct {
		Result string `json:"result"`
	}
	if err := e.postJSON(ctx, e.at("image-to-3d"), body, &out); err != nil {
		return "", err
	}
	if out.Result == "" {
		return "", errors.New("the service gave no task")
	}
	return out.Result, nil
}

func (meshy) poll(ctx context.Context, e Endpoint, id string) (taskState, error) {
	var out struct {
		Status   string  `json:"status"`
		Progress float64 `json:"progress"`
		URLs     struct {
			GLB string `json:"glb"`
		} `json:"model_urls"`
		Error struct {
			Message string `json:"message"`
		} `json:"task_error"`
	}
	if err := e.call(ctx, http.MethodGet, e.at("image-to-3d", url.PathEscape(id)), "", nil, &out); err != nil {
		return taskState{}, err
	}
	switch out.Status {
	case "SUCCEEDED":
		if out.URLs.GLB == "" {
			return taskState{}, &taskRefused{"the service finished without a model"}
		}
		return taskState{done: true, url: out.URLs.GLB, ext: ".glb"}, nil
	case "FAILED", "CANCELED":
		return taskState{}, &taskRefused{"the service could not make the model: " + out.Error.Message}
	}
	return taskState{fraction: out.Progress / 100}, nil
}

// cancel takes a task off the queue, which gives its credits back; one that has started cannot be stopped.
func (meshy) cancel(ctx context.Context, e Endpoint, id string) {
	e.call(ctx, http.MethodDelete, e.at("image-to-3d", url.PathEscape(id)), "", nil, nil)
}

// sdVideo is the job interface of sd-server: POST /sdcpp/v1/vid_gen, then GET /sdcpp/v1/jobs/{id}.
type sdVideo struct {
	steps int // how many steps were asked for, which progress is measured against
}

// root is the server's address without the "/v1" of its OpenAI-style interface.
func (sdVideo) root(e Endpoint) string {
	return strings.TrimSuffix(strings.TrimSuffix(e.URL, "/"), "/v1")
}

func (s sdVideo) submit(ctx context.Context, e Endpoint, req TaskRequest) (string, error) {
	// The engine starts from the same seed each time unless told otherwise, which makes the same video.
	// Only a job with a preview says which step it is on.
	body := map[string]any{"prompt": req.Prompt, "output_format": "webm", "seed": -1, "preview": "proj"}
	v := req.Video
	for name, value := range map[string]int{"width": v.Width, "height": v.Height, "video_frames": v.Frames, "fps": v.FPS} {
		if value > 0 {
			body[name] = value
		}
	}
	if v.Steps > 0 {
		body["sample_params"] = map[string]any{"sample_steps": v.Steps}
	}
	if req.Image != nil {
		body["init_image"] = dataURL(*req.Image)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := e.postJSON(ctx, s.root(e)+"/sdcpp/v1/vid_gen", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("the engine gave no job")
	}
	return out.ID, nil
}

func (s sdVideo) poll(ctx context.Context, e Endpoint, id string) (taskState, error) {
	var out struct {
		Status  string `json:"status"`
		Preview *struct {
			Step float64 `json:"step"`
		} `json:"preview"`
		Result struct {
			B64    string `json:"b64_json"`
			Format string `json:"output_format"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := e.call(ctx, http.MethodGet, s.root(e)+"/sdcpp/v1/jobs/"+url.PathEscape(id), "", nil, &out); err != nil {
		return taskState{}, err
	}
	switch out.Status {
	case "completed":
		data, err := base64.StdEncoding.DecodeString(out.Result.B64)
		if err != nil {
			return taskState{}, &taskRefused{"the video could not be decoded: " + err.Error()}
		}
		if len(data) == 0 {
			return taskState{}, &taskRefused{"the engine finished without a video"}
		}
		ext := ".webm"
		if out.Result.Format != "" {
			ext = "." + out.Result.Format
		}
		return taskState{done: true, data: data, ext: ext}, nil
	case "failed", "cancelled":
		return taskState{}, &taskRefused{"the engine could not make the video: " + string(out.Error)}
	}
	fraction := -1.0
	if out.Preview != nil && s.steps > 0 {
		fraction = min(out.Preview.Step/float64(s.steps), 1)
	}
	return taskState{fraction: fraction}, nil
}

func (s sdVideo) cancel(ctx context.Context, e Endpoint, id string) {
	e.call(ctx, http.MethodPost, s.root(e)+"/sdcpp/v1/jobs/"+url.PathEscape(id)+"/cancel", "application/json", []byte("{}"), nil)
}
