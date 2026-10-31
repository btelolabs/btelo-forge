package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastTasks(t *testing.T) {
	t.Helper()
	poll, failures := taskPoll, taskFailures
	taskPoll, taskFailures = time.Millisecond, 3
	t.Cleanup(func() { taskPoll, taskFailures = poll, failures })
}

func picture() *Image { return &Image{MIME: "image/png", Data: []byte("picture")} }

func TestMiniMaxMakesAVideoFromAFirstFrame(t *testing.T) {
	fastTasks(t)
	var asked map[string]any
	var polls, leaked atomic.Int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/video_generation", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("key %q", r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&asked)
		fmt.Fprint(w, `{"task_id":"7","base_resp":{"status_code":0,"status_msg":"success"}}`)
	})
	mux.HandleFunc("/v1/query/video_generation", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("task_id") != "7" {
			t.Errorf("task %q", r.URL.RawQuery)
		}
		if polls.Add(1) < 3 {
			fmt.Fprint(w, `{"status":"Processing","base_resp":{"status_code":0}}`)
			return
		}
		fmt.Fprint(w, `{"status":"Success","file_id":"9","base_resp":{"status_code":0}}`)
	})
	mux.HandleFunc("/v1/files/retrieve", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"file":{"download_url":%q},"base_resp":{"status_code":0}}`, srv.URL+"/signed/out.mp4")
	})
	mux.HandleFunc("/signed/out.mp4", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Add(1)
		}
		w.Write([]byte("video bytes"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()
	var last float64
	got, err := Endpoint{URL: srv.URL + "/v1", Key: "k", Model: "MiniMax-H3"}.RunTask(context.Background(), TaskMiniMax,
		TaskRequest{Prompt: "a fox", Image: picture()}, func(f float64) { last = f })
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "video bytes" || got.Ext != ".mp4" || last != 1 {
		t.Fatalf("%q %q, progress %v", got.Data, got.Ext, last)
	}
	if asked["model"] != "MiniMax-H3" || asked["prompt"] != "a fox" || !strings.HasPrefix(asked["first_frame_image"].(string), "data:image/png;base64,") {
		t.Fatalf("asked %v", asked)
	}
	if leaked.Load() != 0 {
		t.Fatal("the key went to the address of the file")
	}
}

func TestAServiceSayingNoEndsTheTaskAtOnce(t *testing.T) {
	fastTasks(t)
	for name, c := range map[string]struct {
		create, query, want string
	}{
		"key":        {`{"base_resp":{"status_code":2049,"status_msg":"invalid api key"}}`, "", "invalid api key"},
		"sensitive":  {`{"task_id":"7","base_resp":{"status_code":0}}`, `{"base_resp":{"status_code":1027,"status_msg":"sensitive"}}`, "sensitive"},
		"failed":     {`{"task_id":"7","base_resp":{"status_code":0}}`, `{"status":"Fail","base_resp":{"status_code":0,"status_msg":"no"}}`, "could not make the video"},
		"no task":    {`{"base_resp":{"status_code":0}}`, "", "gave no task"},
		"not a file": {`{"task_id":"7","base_resp":{"status_code":0}}`, `{"status":"Success","base_resp":{"status_code":0}}`, "without a file"},
	} {
		var polls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "query") {
				polls.Add(1)
				fmt.Fprint(w, c.query)
				return
			}
			fmt.Fprint(w, c.create)
		}))
		_, err := Endpoint{URL: srv.URL}.RunTask(context.Background(), TaskMiniMax, TaskRequest{Prompt: "x"}, func(float64) {})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if polls.Load() > 1 {
			t.Errorf("%s: asked %d times after a final answer", name, polls.Load())
		}
	}
}

func TestATaskSurvivesAFewFailedQuestionsButNotMany(t *testing.T) {
	fastTasks(t)
	var polls atomic.Int32
	limit := int32(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "image-to-3d/"):
			if polls.Add(1) <= limit {
				http.Error(w, "busy", http.StatusBadGateway)
				return
			}
			fmt.Fprintf(w, `{"status":"SUCCEEDED","progress":100,"model_urls":{"glb":"http://%s/m.glb"}}`, r.Host)
		case strings.HasSuffix(r.URL.Path, "image-to-3d"):
			fmt.Fprint(w, `{"result":"t1"}`)
		default:
			w.Write([]byte("glTF"))
		}
	}))
	defer srv.Close()
	ep := Endpoint{URL: srv.URL, Key: "k", Model: "meshy-7.1"}
	got, err := ep.RunTask(context.Background(), TaskMeshy, TaskRequest{Image: picture()}, func(float64) {})
	if err != nil || string(got.Data) != "glTF" || got.Ext != ".glb" {
		t.Fatalf("%q %v", got.Data, err)
	}
	polls.Store(0)
	limit = 1000
	if _, err := ep.RunTask(context.Background(), TaskMeshy, TaskRequest{Image: picture()}, func(float64) {}); err == nil {
		t.Fatal("a service that never answers was waited for")
	}
	if polls.Load() != int32(taskFailures) {
		t.Fatalf("asked %d times", polls.Load())
	}
}

func TestTripoUploadsThePictureAndReportsProgress(t *testing.T) {
	fastTasks(t)
	var asked map[string]any
	var upload string
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/files":
			f, h, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(f)
			upload = h.Filename + ":" + string(body)
			fmt.Fprint(w, `{"code":0,"status":"success","data":{"file_token":"file_1"}}`)
		case r.URL.Path == "/v3/generation/image-to-model":
			json.NewDecoder(r.Body).Decode(&asked)
			fmt.Fprint(w, `{"code":0,"status":"success","data":{"task_id":"t","status":"queued"}}`)
		case r.URL.Path == "/v3/tasks/t":
			if polls.Add(1) == 1 {
				fmt.Fprint(w, `{"code":0,"data":{"status":"running","progress":40}}`)
				return
			}
			fmt.Fprintf(w, `{"code":0,"data":{"status":"success","progress":100,"output":{"model_url":"http://%s/out/model.glb?sig=1"}}}`, r.Host)
		default:
			w.Write([]byte("glb bytes"))
		}
	}))
	defer srv.Close()
	var seen []float64
	got, err := Endpoint{URL: srv.URL + "/v3", Key: "k", Model: "tripo"}.RunTask(context.Background(), TaskTripo,
		TaskRequest{Image: picture(), Version: "v3.1-20260211"}, func(f float64) { seen = append(seen, f) })
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "glb bytes" || got.Ext != ".glb" {
		t.Fatalf("%q %q", got.Data, got.Ext)
	}
	if upload != "picture.png:picture" || asked["model_version"] != "v3.1-20260211" {
		t.Fatalf("upload %q, asked %v", upload, asked)
	}
	file := asked["file"].(map[string]any)
	if file["file_token"] != "file_1" || file["type"] != "png" {
		t.Fatalf("file %v", file)
	}
	if len(seen) != 2 || seen[0] != 0.4 || seen[1] != 1 {
		t.Fatalf("progress %v", seen)
	}
}

func TestA3DModelNeedsAPicture(t *testing.T) {
	_, err := Endpoint{URL: "http://127.0.0.1:1"}.RunTask(context.Background(), TaskTripo, TaskRequest{Prompt: "x"}, func(float64) {})
	if !errors.Is(err, ErrNeedsPicture) {
		t.Fatal(err)
	}
}

func TestStoppingALocalVideoTellsTheEngine(t *testing.T) {
	fastTasks(t)
	var asked map[string]any
	cancelled := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sdcpp/v1/vid_gen":
			json.NewDecoder(r.Body).Decode(&asked)
			fmt.Fprint(w, `{"id":"job_1","status":"queued"}`)
		case r.URL.Path == "/sdcpp/v1/jobs/job_1":
			fmt.Fprint(w, `{"status":"generating","preview":{"step":5}}`)
		case r.URL.Path == "/sdcpp/v1/jobs/job_1/cancel":
			cancelled <- struct{}{}
			fmt.Fprint(w, `{"cancel_requested":true}`)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var fraction atomic.Value
	_, err := Endpoint{URL: srv.URL + "/v1", Local: true}.RunTask(ctx, TaskSDVideo,
		TaskRequest{Prompt: "a fox", Video: VideoOptions{Width: 704, Height: 480, Frames: 33, FPS: 24, Steps: 20}},
		func(f float64) { fraction.Store(f); cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine was not told")
	}
	if fraction.Load() != 0.25 {
		t.Fatalf("progress %v", fraction.Load())
	}
	if asked["video_frames"] != 33.0 || asked["width"] != 704.0 || asked["fps"] != 24.0 || asked["preview"] != "proj" {
		t.Fatalf("asked %v", asked)
	}
}

func TestALocalVideoComesBackInTheAnswer(t *testing.T) {
	fastTasks(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"id":"j"}`)
			return
		}
		fmt.Fprint(w, `{"status":"completed","result":{"b64_json":"dmlkZW8=","output_format":"webm"}}`)
	}))
	defer srv.Close()
	got, err := Endpoint{URL: srv.URL + "/v1", Local: true}.RunTask(context.Background(), TaskSDVideo, TaskRequest{Prompt: "x"}, func(float64) {})
	if err != nil || string(got.Data) != "video" || got.Ext != ".webm" {
		t.Fatalf("%q %q %v", got.Data, got.Ext, err)
	}
}

func TestTaskMistakesAreNamed(t *testing.T) {
	good := string(DefaultJSON)
	h3 := `"kind": "video", "task": "minimax-video", "name": "MiniMax H3"`
	for name, c := range map[string]struct{ to, want string }{
		"no task":  {`"kind": "video", "name": "MiniMax H3"`, "needs a task"},
		"unknown":  {`"kind": "video", "task": "nope", "name": "MiniMax H3"`, "task must be"},
		"wrong":    {`"kind": "video", "task": "meshy", "name": "MiniMax H3"`, `makes kind "model3d"`},
		"local":    {`"kind": "video", "task": "sd-video", "name": "MiniMax H3"`, "for a model on this computer"},
		"not edit": {`"kind": "video", "task": "minimax-video", "edits": true, "name": "MiniMax H3"`, "edits is for image"},
	} {
		if !strings.Contains(good, h3) {
			t.Fatal("the test text is not in the built-in file")
		}
		_, err := ParseConfig([]byte(strings.Replace(good, h3, c.to, 1)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestChoicesSayHowTheyWork(t *testing.T) {
	c := DefaultConfig()
	for id, want := range map[string]struct {
		task    string
		picture bool
	}{
		"minimax/minimax-h3":          {TaskMiniMax, true},
		"tripo/v3.1":                  {TaskTripo, true},
		"meshy/meshy-7.1":             {TaskMeshy, true},
		"wan2.2-ti2v-5b":              {TaskSDVideo, true},
		"minimax-h3-q4":               {TaskSDVideo, true},
		"sd15":                        {"", true},
		"grok/grok-imagine-image-2.0": {"", true},
		"gemini/gemini-3-pro-image":   {"", false},
		"qwen3-4b":                    {"", false},
	} {
		ch, ok := c.Choice(id)
		if !ok || ch.Task() != want.task || ch.StartsFromPicture() != want.picture {
			t.Errorf("%s: %q %v %v", id, ch.Task(), ch.StartsFromPicture(), ok)
		}
	}
}

func TestFitKeepsTheNewestMessages(t *testing.T) {
	long := strings.Repeat("字", 1000) // 1500 tokens
	var msgs []Message
	for i := 0; i < 8; i++ {
		msgs = append(msgs, Message{Role: User, Text: long}, Message{Role: Assistant, Text: long})
	}
	msgs = append(msgs, Message{Role: User, Text: "最新的问题"})
	got, left, ok := Fit(msgs, 8192)
	if !ok || left == 0 || got[len(got)-1].Text != "最新的问题" || got[0].Role != User || len(got)+left != len(msgs) {
		t.Fatalf("%d kept, %d left out, %v, first %s", len(got), left, ok, got[0].Role)
	}
	total := 0
	for _, m := range got {
		total += m.tokens()
	}
	if total > 8192*3/4 {
		t.Fatalf("%d tokens kept", total)
	}

	// All of it fits: nothing changes.
	if got, left, ok := Fit(msgs[len(msgs)-3:], 8192); !ok || left != 0 || len(got) != 3 {
		t.Fatalf("%d %d %v", len(got), left, ok)
	}
	// A message that cannot fit is refused rather than sent cut short.
	if _, _, ok := Fit([]Message{{Role: User, Text: strings.Repeat(long, 20)}}, 8192); ok {
		t.Fatal("a message too long was accepted")
	}
	// The first message of the conversation stays a system message.
	withSystem := append([]Message{{Role: System, Text: "be brief"}}, msgs...)
	if got, _, _ := Fit(withSystem, 8192); got[0].Role != System || got[1].Role != User {
		t.Fatalf("starts with %s, %s", got[0].Role, got[1].Role)
	}
	// Pictures count.
	pics := Message{Role: User, Text: "x", Attached: []Media{{Kind: KindImage}}}
	if pics.tokens() <= imageTokens {
		t.Fatal("a picture costs nothing")
	}
}

func TestRedrawSendsThePicture(t *testing.T) {
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	answer := fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, png)
	var form, kind, picture string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("path %s", r.URL.Path)
		}
		kind = r.Header.Get("Content-Type")
		if strings.HasPrefix(kind, "multipart/") {
			f, _, err := r.FormFile("image[]")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(f)
			form, picture = r.FormValue("prompt"), string(b)
		} else {
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			form, picture = in["prompt"].(string), in["image"].(map[string]any)["url"].(string)
		}
		fmt.Fprint(w, answer)
	}))
	defer srv.Close()
	source := Image{MIME: "image/png", Data: []byte("source")}
	got, err := Endpoint{URL: srv.URL + "/v1", Model: "m", Local: true}.Redraw(context.Background(), "make it blue", source, ImageOptions{Steps: 9})
	if err != nil || got.MIME != "image/png" {
		t.Fatalf("%+v %v", got, err)
	}
	if picture != "source" || !strings.Contains(form, "make it blue") || !strings.Contains(form, `"sample_steps":9`) {
		t.Fatalf("%q %q", form, picture)
	}
	if _, err = (Endpoint{URL: srv.URL + "/v1", Model: "m", Key: "k"}).Redraw(context.Background(), "make it blue", source, ImageOptions{}); err != nil {
		t.Fatal(err)
	}
	if kind != "application/json" || form != "make it blue" || !strings.HasPrefix(picture, "data:image/png;base64,") {
		t.Fatalf("%q %q %q", kind, form, picture)
	}
}
