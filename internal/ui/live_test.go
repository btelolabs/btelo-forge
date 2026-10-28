package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/media"
)

func TestAnAIConfiguresStreamingWithoutSeeingTheKey(t *testing.T) {
	if !capture.Supported() {
		t.Skip(capture.ErrUnsupported)
	}
	x := newHarness(t)
	a := x.a
	s := connectMCPAt(t, x, mcpDomainPaths[domainRecording])
	const key = "live_398812_SECRETkey"
	var dest struct {
		ID      string `json:"id"`
		Server  string `json:"server"`
		HasKey  bool   `json:"has_key"`
		Enabled bool   `json:"enabled"`
	}
	s.ok("set_stream_destination", map[string]any{"platform": "bilibili", "key": key}, &dest)
	if dest.Server != "rtmp://live-push.bilivideo.com/live-bvc/" || !dest.HasKey || !dest.Enabled {
		t.Fatalf("the new destination is %+v", dest)
	}
	if res := s.call("set_stream_destination", map[string]any{"platform": "custom", "server": "http://example.com/live"}); !res.IsError {
		t.Error("an address that is not rtmp, rtmps or srt was taken")
	}
	if res := s.call("set_stream_destination", map[string]any{"platform": "nowhere"}); !res.IsError {
		t.Error("an unknown platform was taken")
	}
	s.ok("set_stream_destination", map[string]any{"id": dest.ID, "name": "B站主号", "enabled": false}, &dest)
	if dest.Enabled || !dest.HasKey {
		t.Fatalf("after changing it: %+v", dest)
	}

	// The key is in no answer and not in the settings file as it is, yet the app reads it back.
	res := s.call("stream_status", nil)
	text, _ := json.Marshal(res.Content)
	if strings.Contains(string(text), key) || !strings.Contains(string(text), "B站主号") || !strings.Contains(string(text), "wechat-channels") {
		t.Errorf("stream_status: %s", text)
	}
	data, err := os.ReadFile(a.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), key) {
		t.Error("the stream key is written as it is in the settings file")
	}
	a.loadSettings()
	if got := a.settings.Live.Destinations; len(got) != 1 || string(got[0].Key) != key || got[0].Name != "B站主号" {
		t.Errorf("read back %+v", got)
	}

	// A platform is added by configuration alone, and offered at once.
	var cfg struct {
		JSON string `json:"json"`
	}
	s.ok("get_config", map[string]any{"name": "streaming_platforms"}, &cfg)
	mine := `{"id": "my-relay", "name": {"en": "My relay", "zh": "我的中转"}, "servers": [{"name": "Home", "url": "srt://192.168.1.20:9000"}], "maxKbps": 3000},`
	changed := strings.Replace(cfg.JSON, `"platforms": [`, `"platforms": [`+mine, 1)
	s.ok("set_config", map[string]any{"name": "streaming_platforms", "json": changed}, nil)
	s.ok("set_stream_destination", map[string]any{"platform": "my-relay", "key": "cam1"}, &dest)
	if dest.Server != "srt://192.168.1.20:9000" {
		t.Errorf("the configured platform's server is %q", dest.Server)
	}
	if res := s.call("set_config", map[string]any{"name": "streaming_platforms", "json": `{"version": 1, "platforms": [{"id": "x"}]}`}); !res.IsError {
		t.Error("a platform without a name was taken")
	}
	var view struct {
		Notes []string `json:"notes"`
	}
	s.ok("set_stream_options", map[string]any{"kbps": 6000}, &view)
	if !strings.Contains(strings.Join(view.Notes, "\n"), "My relay takes at most 3000") {
		t.Errorf("notes %q", view.Notes)
	}
	s.ok("reset_config", map[string]any{"name": "streaming_platforms"}, nil)
	if _, ok := a.onAir.platforms.Find("my-relay"); ok {
		t.Error("the reset kept the added platform")
	}

	// The dialog, with the destinations and with one being changed.
	for _, lang := range []Language{English, Chinese} {
		a.u.lang = lang
		a.openLive()
		x.frame(Input{})
		x.frame(Input{})
		x.shot("live-tab-" + string(lang))
		a.onAir.edit = a.editFor(0)
		x.frame(Input{})
		x.frame(Input{})
		x.shot("live-destination-" + string(lang))
		a.onAir.edit = nil
	}
}

// rtmpServer is ffmpeg taking one stream as a platform's server does, and saving it.
type rtmpServer struct {
	url, path string
	done      chan struct{}
}

func startRTMPServer(t *testing.T, name string) *rtmpServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	s := &rtmpServer{url: fmt.Sprintf("rtmp://127.0.0.1:%d/live", port), path: filepath.Join(t.TempDir(), name+".flv"), done: make(chan struct{})}
	cmd, err := media.Command(context.Background(), "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-listen", "1", "-i", s.url+"/secret-"+name, "-c", "copy", s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() { killServer(cmd, s.done) })
	return s
}

func killServer(cmd *exec.Cmd, done chan struct{}) {
	select {
	case <-done:
	default:
		cmd.Cancel()
		<-done
	}
}

// wait draws frames for a while, as the window does while it is open.
func (x *harness) wait(d time.Duration) {
	until := time.Now().Add(d)
	x.pump(d+time.Minute, func() bool { return time.Now().After(until) })
}

func (x *harness) liveStatus() capture.StreamStatus {
	if x.a.onAir.stream == nil {
		return capture.StreamStatus{}
	}
	return x.a.onAir.stream.Status()
}

func (x *harness) allLive() bool {
	st := x.liveStatus()
	for _, t := range st.Targets {
		if t.State != capture.TargetLive {
			return false
		}
	}
	return len(st.Targets) > 0
}

func TestTheStreamFollowsRecordingsWithoutStopping(t *testing.T) {
	x := recordingHarness(t)
	a := x.a
	a.settings.Record.Minimize, a.settings.Record.Region = false, false
	servers := []*rtmpServer{startRTMPServer(t, "a"), startRTMPServer(t, "b")}
	a.settings.Live = defaultLiveSettings()
	a.settings.Live.Width, a.settings.Live.Height, a.settings.Live.Kbps, a.settings.Live.Record = 1280, 720, 2500, false
	for i, s := range servers {
		if err := a.saveDestination(-1, liveDestination{Platform: "custom", Name: fmt.Sprint("server ", i), Server: s.url, Key: sealedText("secret-" + string(rune('a'+i))), On: true}); err != nil {
			t.Fatal(err)
		}
	}
	// Through the dialog's start button: the stream starts black, since nothing is recorded.
	a.openLive()
	x.frame(Input{})
	done := make(chan error, 1)
	a.startLive(func(err error) { done <- err })
	x.pump(time.Minute, func() bool { return len(done) > 0 })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	x.pump(30*time.Second, x.allLive)
	if !x.allLive() || x.liveStatus().Screen {
		t.Fatalf("status %+v", x.liveStatus())
	}
	x.frame(Input{})
	x.shot("live-running")

	for round := range 2 {
		started := make(chan error, 1)
		a.startRecording(func(_ string, err error) { started <- err })
		x.pump(time.Minute, func() bool { return len(started) > 0 })
		if err := <-started; err != nil {
			t.Fatal(err)
		}
		x.pump(15*time.Second, func() bool { return x.liveStatus().Screen })
		if !x.liveStatus().Screen {
			t.Fatalf("round %d: the recording is not on the stream: %+v", round, x.liveStatus())
		}
		x.wait(3 * time.Second)
		if round == 0 {
			// The tab previews what the platforms get: the screen, not black.
			x.frame(Input{})
			p := a.onAir.preview
			lo, hi := uint8(255), uint8(0)
			for i := 0; p != nil && i < len(p.Pix); i += 4 {
				lo, hi = min(lo, p.Pix[i+1]), max(hi, p.Pix[i+1])
			}
			if p == nil || hi-lo < 20 {
				t.Errorf("the tab previews no screen while it is recorded (levels %d to %d)", lo, hi)
			}
			x.shot("live-tab-recording")
		}
		saved := make(chan error, 1)
		a.stopRecording(func(_ string, err error) { saved <- err })
		if x.liveStatus().Screen {
			t.Errorf("round %d: the stream still shows the screen once the recording stops", round)
		}
		x.pump(time.Minute, func() bool { return len(saved) > 0 })
		if err := <-saved; err != nil {
			t.Fatal(err)
		}
		x.wait(2 * time.Second)
		if !x.allLive() {
			t.Fatalf("round %d: the stream did not go on after the recording: %+v", round, x.liveStatus())
		}
	}
	elapsed := x.liveStatus().Elapsed.Seconds()
	ended := make(chan error, 1)
	a.stopLive(func(err error) { ended <- err })
	x.pump(time.Minute, func() bool { return len(ended) > 0 })
	if err := <-ended; err != nil || a.onAir.stream != nil {
		t.Fatalf("ending: %v", err)
	}
	for _, tt := range a.toasts {
		if tt.err {
			t.Errorf("error shown: %s", tt.text)
		}
	}
	for _, s := range servers {
		select {
		case <-s.done:
		case <-time.After(15 * time.Second):
			t.Fatalf("%s did not see the stream end", s.url)
		}
		info, err := media.Probe(context.Background(), s.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Width != 1280 || info.Height != 720 || !info.HasAudio || info.Duration < elapsed-4 {
			t.Errorf("%s received %+v of a %.1f s stream", s.url, info, elapsed)
		}
	}
}

func TestTheNewTabOffersStreamingAndTheCamera(t *testing.T) {
	if !capture.Supported() {
		t.Skip(capture.ErrUnsupported)
	}
	x := newHarness(t)
	x.h.overlaysOn = true
	x.frame(Input{})
	x.click(x.startCard("empty/live"))
	if !x.a.liveTab || len(x.a.docs) != 1 {
		t.Fatal("the live card did not turn the empty tab into the stream's tab")
	}
	// There is one stream tab: opening it again from elsewhere brings it forward.
	x.a.newTab()
	x.a.newTab()
	x.a.openLive()
	streamTabs := 0
	for _, d := range x.a.docs {
		if d.liveTab {
			streamTabs++
		}
	}
	if streamTabs != 1 || !x.a.liveTab || len(x.a.docs) != 3 {
		t.Fatalf("%d stream tabs among %d; the one in front is it: %v", streamTabs, len(x.a.docs), x.a.liveTab)
	}
	x.a.switchTab(1)
	var card startCard
	for _, c := range x.a.startCards() {
		if c.id == "empty/live" {
			card = c
		}
	}
	if card.extra == nil || !card.extra.enabled {
		t.Fatal("the live card has no camera button")
	}
	card.extra.run()
	if !x.a.cam.open {
		t.Error("the camera button did not open the camera's settings")
	}
	x.a.cam.open = false
	// The tab strip has no streaming or camera buttons; while nothing is live it shows no stream either.
	if r := x.a.liveControlRect(100, 0, 30); r.W != 0 {
		t.Errorf("the tab strip keeps %v for a stream that is not live", r)
	}

	// In a window too low for every card, the page scrolls down to the last one.
	x.c.Resize(560, 900)
	x.frame(Input{})
	live, body := x.startCard("empty/live"), x.a.layout().body
	if live.Bottom() <= body.Bottom() {
		t.Fatalf("the live card %v fits in %v; the window is not too low", live, body)
	}
	x.mx, x.my = body.X+body.W/2, body.Y+body.H/2
	for range 20 {
		x.frame(Input{Wheel: -1})
	}
	if shown := live.Bottom() - x.a.startScroll; shown > body.Bottom() || x.a.startScroll <= 0 {
		t.Errorf("scrolled by %v, the live card ends at %v, below the window's %v", x.a.startScroll, shown, body.Bottom())
	}
	x.shot("start-narrow-scrolled")
}
