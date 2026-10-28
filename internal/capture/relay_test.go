package capture

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
}

// testStream is a few seconds of a transport stream like the encoder's: H.264 with a keyframe every second, and AAC.
func testStream(t *testing.T, seconds int) []byte {
	t.Helper()
	cmd, err := media.Command(context.Background(), "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", fmt.Sprint(seconds), "-c:v", "libx264", "-preset", "ultrafast", "-g", "30", "-c:a", "aac", "-f", "mpegts", "pipe:1")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTablesFindTheVideoAndItsKeyframes(t *testing.T) {
	needFFmpeg(t)
	ts := testStream(t, 3)
	tables := newTables()
	keys := 0
	for p := ts; len(p) >= tsPacket; p = p[tsPacket:] {
		if p[0] != 0x47 {
			t.Fatal("the stream is out of step")
		}
		tables.see(p[:tsPacket])
		if tables.keyframe(p[:tsPacket]) {
			keys++
		}
	}
	if !tables.ready() || tables.videoPID != 0x100 {
		t.Fatalf("tables %+v", tables)
	}
	if keys != 3 {
		t.Errorf("%d keyframes in three seconds with one a second", keys)
	}
}

// fedRelay passes a stream through a relay in pieces of uneven size, as a pipe delivers it.
func fedRelay(r *relay, ts []byte) {
	for i, n := 0, 1000; i < len(ts); i, n = i+n, n%7000+977 {
		r.Write(ts[i:min(i+n, len(ts))])
	}
}

func TestADestinationStartsWithTheTablesAndAKeyframe(t *testing.T) {
	needFFmpeg(t)
	ts := testStream(t, 3)
	r := newRelay(defaultPush)
	dest := newTarget(Target{ID: "a"}, defaultPush)
	dest.taking = true
	r.targets = []*target{dest}
	// Some junk before the stream is skipped.
	r.Write([]byte{1, 2, 3})
	fedRelay(r, ts)
	got := bytes.Join(dest.take(), nil)
	if len(got)%tsPacket != 0 || len(got) == 0 {
		t.Fatalf("%d bytes passed on", len(got))
	}
	tables := newTables()
	tables.see(got[:tsPacket])
	tables.see(got[tsPacket : 2*tsPacket])
	if !tables.ready() || !tables.keyframe(got[2*tsPacket:3*tsPacket]) {
		t.Fatal("the destination does not start with the tables and a keyframe")
	}
	if !bytes.HasSuffix(ts, got[2*tsPacket:]) {
		t.Error("after its start the destination does not get the whole stream")
	}

	// One that falls too far behind loses its queue, and what it gets next starts again at a keyframe.
	small := defaultPush
	small.queue = 64 * tsPacket
	slow := newTarget(Target{ID: "b"}, small)
	slow.taking = true
	r = newRelay(small)
	r.targets = []*target{slow}
	i := 0
	for ; i < len(ts) && slow.dropped == 0; i += 1000 {
		r.Write(ts[i:min(i+1000, len(ts))])
	}
	if slow.dropped == 0 {
		t.Fatal("a destination that fell behind was not skipped ahead")
	}
	for ; i < len(ts) && slow.queued == 0; i += 1000 {
		r.Write(ts[i:min(i+1000, len(ts))])
	}
	got = bytes.Join(slow.take(), nil)
	tables = newTables()
	if len(got) >= 3*tsPacket {
		tables.see(got[:tsPacket])
		tables.see(got[tsPacket : 2*tsPacket])
	}
	if !tables.ready() || !tables.keyframe(got[2*tsPacket:3*tsPacket]) {
		t.Errorf("after skipping ahead the destination gets %d bytes that do not start at a keyframe", len(got))
	}
}

// freePort is a local port nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// rtmpServer is ffmpeg receiving one stream over RTMP, as a platform's server does, and saving it.
type rtmpServer struct {
	url, path string
	cmd       *exec.Cmd
	done      chan struct{} // closed when ffmpeg has ended
}

func startRTMPServer(t *testing.T, port int, name string) *rtmpServer {
	t.Helper()
	s := &rtmpServer{url: fmt.Sprintf("rtmp://127.0.0.1:%d/live/secret-%s", port, name), path: filepath.Join(t.TempDir(), name+".flv"), done: make(chan struct{})}
	cmd, err := media.Command(context.Background(), "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-listen", "1", "-i", s.url, "-c", "copy", s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.cmd = cmd
	go func() {
		cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-s.done
	})
	// ffmpeg accepts a single connection, so nothing may knock to see whether it listens yet: a destination that
	// connects before it does is simply retried.
	return s
}

// received waits for the server to finish and reports the codecs of what it saved, with the file's length as each
// stream's (FLV keeps no lengths of streams).
func (s *rtmpServer) received(t *testing.T) map[string]streamInfo {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(15 * time.Second):
		t.Fatalf("the server at %s did not see the stream end", s.url)
	}
	if _, err := os.Stat(s.path); err != nil {
		t.Fatalf("the server at %s saved nothing: %v", s.url, err)
	}
	found := streams(t, s.path)
	cmd, err := media.Command(context.Background(), "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", s.path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	length, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	for kind, info := range found {
		info.duration = length
		found[kind] = info
	}
	return found
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func targetState(r *relay, id string) TargetStatus {
	for _, t := range r.list() {
		if t.ID == id {
			return t.status()
		}
	}
	return TargetStatus{State: -1}
}

func TestOneStreamGoesToSeveralServersAndReconnects(t *testing.T) {
	needFFmpeg(t)
	push := pushOptions{retryFirst: 300 * time.Millisecond, retryMost: time.Second, givingUp: 5, queue: 8 << 20, closeWait: 5 * time.Second}
	r := newRelay(push)
	first := startRTMPServer(t, freePort(t), "first")
	latePort := freePort(t)
	late := &rtmpServer{url: fmt.Sprintf("rtmp://127.0.0.1:%d/live/secret-late", latePort)}
	// A server that hangs up on every connection, as one does that refuses the stream key.
	refusing, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer refusing.Close()
	go func() {
		for {
			c, err := refusing.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	nowhere := fmt.Sprintf("rtmp://%s/live/secret-nowhere", refusing.Addr())
	for _, dest := range []Target{{ID: "first", Name: "First", URL: first.url}, {ID: "late", Name: "Late", URL: late.url}, {ID: "nowhere", Name: "Nowhere", URL: nowhere}} {
		if err := r.add(dest); err != nil {
			t.Fatal(err)
		}
	}
	// The encoder: twelve seconds at the speed of a live stream.
	source, err := media.Command(context.Background(), "ffmpeg", "-hide_banner", "-loglevel", "error", "-re",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-g", "30", "-c:a", "aac", "-flush_packets", "1", "-f", "mpegts", "pipe:1")
	if err != nil {
		t.Fatal(err)
	}
	source.Stdout = r
	if err := source.Start(); err != nil {
		t.Fatal(err)
	}
	defer source.Process.Kill()

	waitFor(t, "the first server receives the stream", 10*time.Second, func() bool { return targetState(r, "first").State == TargetLive })
	waitFor(t, "the late server's connection fails", 10*time.Second, func() bool { return targetState(r, "late").State == TargetRetrying })
	if st := targetState(r, "late"); st.Err == "" || strings.Contains(st.Err, "secret") {
		t.Errorf("the failure is explained as %q", st.Err)
	}
	late = startRTMPServer(t, latePort, "late")
	waitFor(t, "the late server receives the stream once it is up", 10*time.Second, func() bool { return targetState(r, "late").State == TargetLive })
	waitFor(t, "the destination that refuses the stream is given up", 30*time.Second, func() bool { return targetState(r, "nowhere").State == TargetFailed })
	if st := targetState(r, "first"); st.State != TargetLive || st.Sent == 0 || st.Live <= 0 {
		t.Errorf("the first server's state %+v", st)
	}
	// Removing one destination leaves the others streaming.
	if err := r.remove("nowhere"); err != nil {
		t.Fatal(err)
	}
	if err := source.Wait(); err != nil {
		t.Fatal(err)
	}
	r.close()

	for _, s := range []*rtmpServer{first, late} {
		got := s.received(t)
		if got["video"].codec != "h264" || got["audio"].codec != "aac" {
			t.Errorf("%s received %+v", s.url, got)
		}
	}
	if d := first.received(t)["video"].duration; d < 10 {
		t.Errorf("the first server received %.1f s of the 12", d)
	}
	if d := late.received(t)["video"].duration; d < 2 {
		t.Errorf("the late server received %.1f s", d)
	}
}

func TestStreamURLAndAddresses(t *testing.T) {
	for _, c := range []struct{ server, key, want string }{
		{"rtmp://a.rtmp.youtube.com/live2", "abcd-1234", "rtmp://a.rtmp.youtube.com/live2/abcd-1234"},
		{"rtmp://live-push.bilivideo.com/live-bvc/", "?streamname=x&key=y", "rtmp://live-push.bilivideo.com/live-bvc/?streamname=x&key=y"},
		{" rtmps://live-api-s.facebook.com:443/rtmp/ ", " FB-1 ", "rtmps://live-api-s.facebook.com:443/rtmp/FB-1"},
		{"rtmp://push.example.com/live/full-key", "", "rtmp://push.example.com/live/full-key"},
		{"srt://ingest.example.com:9000", "live/k", "srt://ingest.example.com:9000?streamid=live%2Fk"},
	} {
		if got := StreamURL(c.server, c.key); got != c.want {
			t.Errorf("StreamURL(%q, %q) = %q, want %q", c.server, c.key, got, c.want)
		}
	}
	for _, bad := range []string{"", "http://example.com/live", "rtmp://", "example.com/live/key"} {
		if err := (Target{Name: "x", URL: bad}).Check(); err == nil {
			t.Errorf("%q was taken as a stream address", bad)
		}
	}
	if err := (Target{URL: "rtmps://x.example.com/app/key"}).Check(); err != nil {
		t.Error(err)
	}
	why := pushFailure([]string{"[tcp @ 000001] Connection to tcp://127.0.0.1:1 failed: refused", "[out#0/flv @ 0] Error opening output rtmp://h/app/KEY: I/O error"}, "rtmp://h/app/KEY")
	if why != "Connection to tcp://127.0.0.1:1 failed: refused" {
		t.Errorf("explained as %q", why)
	}
	if why := pushFailure([]string{"Error opening output rtmp://h/app/KEY: I/O error"}, "rtmp://h/app/KEY"); strings.Contains(why, "KEY") {
		t.Errorf("the stream key is shown: %q", why)
	}
}
