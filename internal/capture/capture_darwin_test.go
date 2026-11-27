//go:build darwin

package capture

import (
	"context"
	"errors"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDarwinSystemAndClocks(t *testing.T) {
	if !Supported() {
		t.Fatal("Supported() = false on darwin")
	}
	if err := CheckSystem(); err != nil {
		t.Fatalf("CheckSystem() = %v", err)
	}
	if freq := counterFrequency(); freq != 10_000_000 {
		t.Fatalf("counterFrequency() = %d, want 10000000", freq)
	}
	c0 := counter()
	time.Sleep(20 * time.Millisecond)
	c1 := counter()
	dt := time.Duration(c1-c0) * 100 * time.Nanosecond
	if dt < 10*time.Millisecond || dt > 5*time.Second {
		t.Fatalf("counter delta = %v (c0=%d c1=%d)", dt, c0, c1)
	}
	w0 := wallMicros()
	now := time.Now().UnixMicro()
	if diff := now - w0; diff < -1_000_000 || diff > 1_000_000 {
		t.Fatalf("wallMicros()=%d differs from time.Now()=%d", w0, now)
	}
}

func TestDarwinDisplaysMicrophonesAndCameras(t *testing.T) {
	displays, err := Displays()
	if errors.Is(err, ErrNoDisplay) {
		t.Log("no active display attached (headless VM); skipping display checks")
	} else if err != nil {
		t.Fatalf("Displays(): %v", err)
	} else {
		if len(displays) == 0 {
			t.Fatal("Displays() returned empty list")
		}
		primary := 0
		for _, d := range displays {
			t.Logf("display: %+v", d)
			if d.Width <= 0 || d.Height <= 0 || !strings.HasPrefix(d.Device, "CGDisplay:") {
				t.Errorf("invalid display %+v", d)
			}
			if displayIDOf(d) == 0 {
				t.Errorf("displayIDOf(%+v) == 0", d)
			}
			if d.Primary {
				primary++
				if d.X != 0 || d.Y != 0 {
					t.Errorf("primary display at (%d,%d), want (0,0)", d.X, d.Y)
				}
			}
			sx, sy, sw, sh := displaySourceRect(d, struct{ MinX, MinY, W, H int }{100, 50, 400, 200})
			if sx < 0 || sy < 0 || sw <= 0 || sh <= 0 {
				t.Errorf("displaySourceRect returned (%v,%v,%v,%v)", sx, sy, sw, sh)
			}
		}
		if primary != 1 {
			t.Errorf("found %d primary displays, want 1", primary)
		}
	}

	mics, err := Microphones()
	if err != nil {
		t.Fatalf("Microphones(): %v", err)
	}
	for i, m := range mics {
		t.Logf("mic: %+v", m)
		if m.ID == "" || m.Name == "" {
			t.Errorf("mic[%d] missing id/name: %+v", i, m)
		}
		if m.Default && i != 0 {
			t.Errorf("default microphone at index %d, want 0", i)
		}
	}

	cams, err := Cameras(context.Background())
	if err != nil {
		t.Fatalf("Cameras(): %v", err)
	}
	for i, c := range cams {
		t.Logf("camera: %+v", c)
		if c.ID == "" || c.Name == "" {
			t.Errorf("cam[%d] missing id/name: %+v", i, c)
		}
	}

	wins := Windows()
	t.Logf("Windows() returned %d rects", len(wins))
	for i, w := range wins {
		if i < 10 {
			t.Logf("win[%d]: %s %v", i, w.Title, w.Rect)
		}
		if w.Rect.Dx() < 8 || w.Rect.Dy() < 8 {
			t.Errorf("win[%d] too small: %+v", i, w)
		}
		if strings.HasPrefix(w.Title, "Window Server") {
			t.Errorf("win[%d] leaked Window Server entry: %+v", i, w)
		}
		if len(displays) > 0 {
			d0 := displays[0]
			if w.Rect.Dx() >= d0.Width*9/10 && w.Rect.Dy() >= d0.Height*9/10 &&
				(w.Title == "Dock" || w.Title == "Notification Center") {
				t.Errorf("win[%d] leaked full-screen system backdrop: %+v", i, w)
			}
		}
	}
}

func TestDarwinVideoToolboxArgsAndProbe(t *testing.T) {
	encs := platformEncoders()
	if len(encs) < 2 || encs[0].Codec != "h264_videotoolbox" || encs[len(encs)-1].Codec != "libx264" {
		t.Fatalf("platformEncoders() = %+v", encs)
	}
	recEnc := strings.Join(encodeArgs(Encoder{Codec: "h264_videotoolbox"}, 60), " ")
	for _, want := range []string{"-c:v h264_videotoolbox", "-q:v 65", "-profile:v high", "-realtime 1", "-g 120"} {
		if !strings.Contains(recEnc, want) {
			t.Errorf("encodeArgs lacks %q:\n%s", want, recEnc)
		}
	}
	strEnc := strings.Join(streamEncodeArgs(Encoder{Codec: "h264_videotoolbox"}, 30, 4500), " ")
	for _, want := range []string{"-c:v h264_videotoolbox", "-profile:v high", "-realtime 1", "-constant_bit_rate 1", "-b:v 4500k", "-maxrate 4500k", "-bufsize 9000k", "-g 60"} {
		if !strings.Contains(strEnc, want) {
			t.Errorf("streamEncodeArgs lacks %q:\n%s", want, strEnc)
		}
	}
	d := Display{Width: 2560, Height: 1440}
	recArgs := strings.Join(darwinRecordArgs(Options{Display: d, FPS: 30, Encoder: Encoder{Codec: "h264_videotoolbox"}}, 1920, 1080, "tcp://127.0.0.1:1234", "tcp://127.0.0.1:5678", "out.mkv"), " ")
	for _, want := range []string{
		"-f rawvideo -pix_fmt nv12 -video_size 1920x1080 -framerate 30 -i tcp://127.0.0.1:1234",
		"-f f32le -ar 48000 -ac 2 -i tcp://127.0.0.1:5678",
		"-map 0:v -map 1:a -c:a flac",
		"-c:v h264_videotoolbox",
		"-f matroska out.mkv",
	} {
		if !strings.Contains(recArgs, want) {
			t.Errorf("darwinRecordArgs lacks %q:\n%s", want, recArgs)
		}
	}
	if strings.Contains(recArgs, "-thread_queue_size") {
		t.Errorf("darwinRecordArgs must not pass input -thread_queue_size (removed in FFmpeg 9+):\n%s", recArgs)
	}

	// Verify VideoToolbox 4096px clamping (e.g., 16" MBP 4112x2658 and 5K 5120x2880).
	if got := darwinRecordSize(image.Pt(4112, 2658), true); got != image.Pt(4096, 2648) {
		t.Errorf("darwinRecordSize(4112x2658, hw) = %v, want 4096x2648", got)
	}
	if got := darwinRecordSize(image.Pt(5120, 2880), true); got != image.Pt(4096, 2304) {
		t.Errorf("darwinRecordSize(5120x2880, hw) = %v, want 4096x2304", got)
	}
	if got := darwinRecordSize(image.Pt(3840, 2160), true); got != image.Pt(3840, 2160) {
		t.Errorf("darwinRecordSize(3840x2160, hw) = %v, want 3840x2160", got)
	}

	needFFmpeg(t)
	e, err := ProbeStreamEncoder(context.Background(), image.Pt(1280, 720), 30)
	if err != nil {
		t.Fatalf("ProbeStreamEncoder: %v", err)
	}
	if e.Codec != "h264_videotoolbox" && e.Codec != "libx264" {
		t.Errorf("ProbeStreamEncoder picked unexpected codec %q", e.Codec)
	}
	if e.Codec != "h264_videotoolbox" {
		t.Logf("h264_videotoolbox unavailable (likely virtualized CI VM); using fallback %s", e.Codec)
	}
	// Must succeed even on displays wider than 4096px (e.g. 4112x2658 Retina).
	if err := tryDarwinEncoder(context.Background(), image.Pt(4112, 2658), 30, e); err != nil {
		t.Fatalf("tryDarwinEncoder(4112x2658, %s): %v", e.Codec, err)
	}
}

func TestDarwinNV12LetterboxAndBGRA(t *testing.T) {
	// Letterbox a 4x4 frame (with padded strides) inside an 8x6 canvas.
	dstW, dstH := 8, 6
	srcW, srcH := 4, 4
	yStride, uvStride := 8, 6
	ySrc := make([]byte, yStride*srcH)
	for y := range srcH {
		for x := range srcW {
			ySrc[y*yStride+x] = byte(100 + y*10 + x)
		}
	}
	uvSrc := make([]byte, uvStride*(srcH/2))
	for y := range srcH / 2 {
		for x := range srcW {
			uvSrc[y*uvStride+x] = byte(200 + y*10 + x)
		}
	}
	dst := make([]byte, dstW*dstH*3/2)
	fillNV12Black(dst, dstW, dstH)
	blitNV12Letterbox(dst, dstW, dstH, ySrc, yStride, uvSrc, uvStride, srcW, srcH)

	// padX = ((8-4)/2)&^1 = 2, padY = ((6-4)/2)&^1 = 0
	if dst[0] != 16 || dst[1] != 16 || dst[6] != 16 || dst[7] != 16 {
		t.Fatalf("left/right Y border corrupted: %v", dst[:8])
	}
	if dst[2] != 100 || dst[5] != 103 || dst[3*dstW+2] != 130 {
		t.Fatalf("inner Y pixels wrong: row0=%v row3=%v", dst[:8], dst[3*dstW:4*dstW])
	}
	luma := dstW * dstH
	if dst[luma+0] != 128 || dst[luma+2] != 200 || dst[luma+5] != 203 {
		t.Fatalf("UV plane wrong: %v", dst[luma:luma+8])
	}

	// Verify shared parallel bgraToRGBA across > 1 band.
	const n = 1<<18 + 37
	src := make([]byte, n*4)
	out := make([]byte, n*4)
	for i := range n {
		src[i*4+0] = byte(i)
		src[i*4+1] = byte(i >> 3)
		src[i*4+2] = byte(i >> 6)
		src[i*4+3] = 0
	}
	bgraToRGBA(out, src)
	for _, i := range []int{0, 1, 1<<18 - 1, 1 << 18, n - 1} {
		d, s := out[i*4:i*4+4], src[i*4:i*4+4]
		if d[0] != s[2] || d[1] != s[1] || d[2] != s[0] || d[3] != 255 {
			t.Fatalf("bgraToRGBA[%d]: got %v from %v", i, d, s)
		}
	}
}

func TestDarwinBlackCanvasRTMPStreamWithVideoToolbox(t *testing.T) {
	needFFmpeg(t)
	canvas := image.Pt(640, 360)
	e, err := ProbeStreamEncoder(context.Background(), canvas, 30)
	if err != nil {
		t.Fatal(err)
	}
	srv := startRTMPServer(t, freePort(t), "vt")
	s, err := StartStream(StreamOptions{
		Size:    canvas,
		FPS:     30,
		Kbps:    1500,
		Encoder: e,
		Targets: []Target{{ID: "0", Name: "local", URL: srv.url}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "RTMP server goes live with VideoToolbox", 15*time.Second, func() bool {
		st := s.Status()
		return len(st.Targets) == 1 && st.Targets[0].State == TargetLive && st.Kbps > 0
	})
	time.Sleep(time.Second)
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	got := srv.received(t)
	if got["video"].codec != "h264" || got["audio"].codec != "aac" || got["video"].duration < 0.5 {
		t.Fatalf("RTMP server received %+v", got)
	}
}

func TestDarwinPermissionPreflightDoesNotHang(t *testing.T) {
	d := Display{Device: "CGDisplay:1", Output: 1, Width: 1920, Height: 1080, Primary: true}
	if displays, err := Displays(); err == nil && len(displays) > 0 {
		d = displays[0]
	}

	if !hasScreenPermission() {
		start := time.Now()
		if _, err := ProbeEncoder(context.Background(), d, 30); !errors.Is(err, ErrScreenPermission) {
			t.Errorf("ProbeEncoder without permission: got %v, want ErrScreenPermission", err)
		}
		if _, err := GrabScreenshot(context.Background(), d, image.Rect(0, 0, 100, 100), false, false); !errors.Is(err, ErrScreenPermission) {
			t.Errorf("GrabScreenshot without permission: got %v, want ErrScreenPermission", err)
		}
		if _, err := GrabDisplays(context.Background(), []Display{d}, false, false); !errors.Is(err, ErrScreenPermission) {
			t.Errorf("GrabDisplays without permission: got %v, want ErrScreenPermission", err)
		}
		if err := Screenshot(context.Background(), d, image.Rect(0, 0, 100, 100), false, filepath.Join(t.TempDir(), "s.png")); !errors.Is(err, ErrScreenPermission) {
			t.Errorf("Screenshot without permission: got %v, want ErrScreenPermission", err)
		}
		if _, err := Start(Options{Display: d, FPS: 30, Encoder: Encoder{Codec: "h264_videotoolbox"}, Path: filepath.Join(t.TempDir(), "r.mp4")}); !errors.Is(err, ErrScreenPermission) {
			t.Errorf("Start without permission: got %v, want ErrScreenPermission", err)
		}
		opened := make(chan error, 1)
		stop := make(chan struct{})
		go captureAudio(true, "", counter(), func([]float32, int, int64) {}, func(err error) { opened <- err }, stop)
		if err := <-opened; !errors.Is(err, ErrScreenPermission) {
			t.Errorf("captureAudio(loopback=true) without permission: got %v, want ErrScreenPermission", err)
		}
		close(stop)
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("screen permission preflight took %v, expected instant fail", elapsed)
		}
	}

	if err := ensureCameraPermission(); errors.Is(err, ErrCameraPermission) {
		cams, _ := Cameras(context.Background())
		cam := Camera{Name: "test", ID: "test"}
		if len(cams) > 0 {
			cam = cams[0]
		}
		_, err := StartCamera(CameraOptions{Camera: cam, Size: image.Pt(160, 120)}, func([]byte) {})
		if !errors.Is(err, ErrCameraPermission) || !errors.Is(err, ErrNoCamera) {
			t.Errorf("StartCamera without permission: got %v, want ErrNoCamera+ErrCameraPermission", err)
		}
	}

	if err := ensureMicPermission(); errors.Is(err, ErrMicPermission) {
		opened := make(chan error, 1)
		stop := make(chan struct{})
		go captureAudio(false, "", counter(), func([]float32, int, int64) {}, func(err error) { opened <- err }, stop)
		if err := <-opened; !errors.Is(err, ErrMicPermission) {
			t.Errorf("captureAudio(loopback=false) without permission: got %v, want ErrMicPermission", err)
		}
		close(stop)
	}
}
