package capture

import (
	"bytes"
	"encoding/binary"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tone is n stereo frames of value v.
func tone(n int, v float32) []float32 {
	s := make([]float32, n*channels)
	for i := range s {
		s[i] = v
	}
	return s
}

func decode(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

func TestTrackSnapsJitterAndKeepsGaps(t *testing.T) {
	tr := newTrack()
	tr.add(tone(480, 0.5), 480, 1000)
	tr.add(tone(480, 0.5), 480, 1000+480+7) // a few frames of timestamp jitter: continues the first packet
	if tr.end != 1000+960 {
		t.Fatalf("end %d after jitter, want %d", tr.end, 1000+960)
	}
	tr.add(tone(480, 0.5), 480, 1960+sampleRate/10) // 100 ms later: a real gap
	if tr.chunks[2].at != 1960+sampleRate/10 {
		t.Fatalf("the gap was closed: chunk at %d", tr.chunks[2].at)
	}
	tr.add(tone(480, 0.5), 480, tr.end-2000) // overlaps what is already there by more than it has: dropped
	if len(tr.chunks) != 3 {
		t.Fatalf("%d chunks after a stale packet", len(tr.chunks))
	}
	if p := tr.takePeak(); p != 0.5 {
		t.Errorf("peak %v", p)
	}
	if p := tr.takePeak(); p != 0 {
		t.Errorf("peak %v after taking it", p)
	}
}

func TestMixerFillsSilenceAndSums(t *testing.T) {
	a, b := newTrack(), newTrack()
	a.add(tone(4800, 0.25), 4800, 0)    // the first 100 ms
	b.add(tone(2400, 0.5), 2400, 2400)  // 50–100 ms
	b.add(nil, 2400, 4800)              // a silent packet
	a.add(tone(4800, 0.75), 4800, 9600) // 200–300 ms, after a gap
	b.add(tone(4800, 0.75), 4800, 9600) // and loud enough together to clip
	m := newMixer([]*track{a, b})
	var out bytes.Buffer
	if err := m.advance(&out, 14400+m.latency); err != nil {
		t.Fatal(err)
	}
	got := decode(out.Bytes())
	if len(got) != 14400*channels {
		t.Fatalf("%d samples, want %d", len(got), 14400*channels)
	}
	at := func(frame int) float32 { return got[frame*channels] }
	for _, c := range []struct {
		frame int
		want  float32
	}{{0, 0.25}, {2399, 0.25}, {2400, 0.75}, {4799, 0.75}, {4800, 0}, {9599, 0}, {9600, 1}, {14399, 1}} {
		if v := at(c.frame); v != c.want {
			t.Errorf("frame %d = %v, want %v", c.frame, v, c.want)
		}
	}
	if len(a.chunks) != 0 || len(b.chunks) != 0 {
		t.Errorf("mixed chunks were kept: %d and %d", len(a.chunks), len(b.chunks))
	}
	// A packet that arrives after its span was written is too late to be heard.
	a.add(tone(480, 1), 480, 100)
	out.Reset()
	if err := m.advance(&out, 14400+480+m.latency); err != nil {
		t.Fatal(err)
	}
	if v := decode(out.Bytes()); len(v) != 480*channels || slices.Max(v) != 0 {
		t.Errorf("late packet: %d samples, max %v", len(v), slices.Max(v))
	}
}

func TestRecordArgs(t *testing.T) {
	d := Display{Adapter: 1, Output: 2, Width: 3840, Height: 2160}
	hw := strings.Join(recordArgs(Options{Display: d, FPS: 60, Cursor: true, Encoder: Encoder{Codec: "h264_nvenc"}}, 123, "tcp://127.0.0.1:9", "out.mkv"), " ")
	for _, want := range []string{
		"-init_hw_device d3d11va=grab:1 -filter_hw_device grab",
		"-f f32le -ar 48000 -ac 2 -i tcp://127.0.0.1:9",
		"ddagrab=output_idx=2:framerate=60:draw_mouse=1,settb=1/1000000,setpts='PTS-STARTPTS+if(eq(N,0),st(0,RTCTIME-123),ld(0))'[v]",
		"-map [v] -map 0:a -c:a flac", "-c:v h264_nvenc", "-g 120", "-f matroska out.mkv",
	} {
		if !strings.Contains(hw, want) {
			t.Errorf("hardware command lacks %q:\n%s", want, hw)
		}
	}
	if strings.Contains(hw, "hwdownload") {
		t.Errorf("hardware frames are copied to memory:\n%s", hw)
	}
	sw := strings.Join(recordArgs(Options{Display: d, FPS: 60, Encoder: Encoder{Codec: "libx264"}}, 0, "", "out.mkv"), " ")
	for _, want := range []string{
		"ddagrab=output_idx=2:framerate=30:draw_mouse=0,", "hwdownload,format=bgra,scale=w=1920:h=1080",
		"format=yuv420p[v]", "-c:v libx264", "-g 60",
	} {
		if !strings.Contains(sw, want) {
			t.Errorf("software command lacks %q:\n%s", want, sw)
		}
	}
	if strings.Contains(sw, "-c:a") || strings.Contains(sw, "f32le") {
		t.Errorf("a recording without sound has a sound input:\n%s", sw)
	}
}

func TestParseSpan(t *testing.T) {
	probe := []byte(`{"streams": [
		{"codec_type": "video", "start_time": "0.300000", "tags": {"DURATION": "00:00:02.333000000"}},
		{"codec_type": "audio", "start_time": "-0.021000", "tags": {"DURATION": "00:00:01.983000000"}}]}`)
	sp, ok := parseSpan(probe)
	if !ok || sp.start != 0.3 || !sp.sound || sp.end() != 1.983 {
		t.Errorf("span %+v %v, want 0.3 to 1.983 with sound", sp, ok)
	}
	// Without sound the picture's own end is used; an hour-long segment parses its hours.
	sp, ok = parseSpan([]byte(`{"streams": [{"codec_type": "video", "start_time": "0.25", "tags": {"DURATION": "01:02:03.500000000"}}]}`))
	if !ok || sp.sound || sp.end() != 3723.5 {
		t.Errorf("span %+v %v", sp, ok)
	}
	if _, ok := parseSpan([]byte(`{"streams": [{"codec_type": "audio", "start_time": "0", "tags": {"DURATION": "00:00:01.0"}}]}`)); ok {
		t.Error("a segment without picture was kept")
	}
}

func TestJoinCutsEverySegmentToItsSpan(t *testing.T) {
	// Paths as this system writes them; the list names them with forward slashes on every system.
	paths := []string{filepath.FromSlash("C:/rec/it's.part1.mkv"), filepath.FromSlash("C:/rec/it's.part2.mkv")}
	spans := []span{{start: 0.3, videoEnd: 2.333, soundEnd: 1.983, sound: true}, {start: 0.267, videoEnd: 2.3}}
	list := concatList(paths, spans)
	want := `file 'C:/rec/it'\''s.part1.mkv'` + "\ninpoint 0.300000\noutpoint 1.983000\n" +
		`file 'C:/rec/it'\''s.part2.mkv'` + "\ninpoint 0.267000\noutpoint 2.300000\n"
	if list != want {
		t.Errorf("list\n%s\nwant\n%s", list, want)
	}
	args := strings.Join(joinArgs("list.txt", paths, spans, "out.mp4"), " ")
	for _, want := range []string{
		"-f concat -safe 0 -i list.txt -i " + paths[0] + " -filter_complex",
		"[1:a]atrim=start=0.300000:end=1.983000,asetpts=PTS-STARTPTS[a0];",
		"anullsrc=r=48000:cl=stereo,atrim=duration=2.033000,asetpts=PTS-STARTPTS[a1];",
		"[a0][a1]concat=n=2:v=0:a=1[a]", "-map 0:v -map [a] -c:v copy -c:a aac",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("join command lacks %q:\n%s", want, args)
		}
	}
	quiet := strings.Join(joinArgs("list.txt", paths, []span{{start: 0.3}, {start: 0.2}}, "out.mp4"), " ")
	if strings.Contains(quiet, "atrim") || !strings.HasSuffix(quiet, "-map 0:v -c copy -avoid_negative_ts make_zero out.mp4") {
		t.Errorf("join without sound: %s", quiet)
	}
}
