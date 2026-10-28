package capture

import (
	"context"
	"errors"
	"image"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const listedDevices = `[dshow @ 0000000002874dc0] "W4DS" (video)
[dshow @ 0000000002874dc0]   Alternative name "@device_pnp_\\?\usb#vid_09da&pid_2700&mi_00#8&2e4c5b39&0&0000#{65e8773d-8f56-11d0-a3b9-00a0c9223196}\global"
[dshow @ 0000000002874dc0] "OBS Virtual Camera" (none)
[dshow @ 0000000002874dc0]   Alternative name "@device_sw_{860BB310-5D01-11D0-BD3B-00A0C911CE86}\{A3FCE0F5-3493-419F-958A-ABA1250EC20B}"
[dshow @ 0000000002874dc0] "麦克风 (W4DS)" (audio)
[dshow @ 0000000002874dc0]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{B457A85A-A566-464B-A673-B3EAA70267EC}"
[dshow @ 0000000002874dc0] "ByteCast VirtualAudio" (none)
[dshow @ 0000000002874dc0]   Alternative name "@device_sw_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\{197FE7D9-E6A4-0D8B-6475-A5003C439997}"
Error opening input file dummy.`

func TestParseCameras(t *testing.T) {
	// ffmpeg 8 prefixes the lines with "[dshow @ 0x…]", ffmpeg 9 with "[in#0 @ 0x…]".
	for _, listing := range []string{listedDevices, strings.ReplaceAll(listedDevices, "[dshow @ ", "[in#0 @ ")} {
		cams := parseCameras([]byte(strings.ReplaceAll(listing, "\n", "\r\n")))
		if len(cams) != 2 || cams[0].Name != "W4DS" || !strings.HasPrefix(cams[0].ID, `@device_pnp_\\?\usb#vid_09da`) || cams[1].Name != "OBS Virtual Camera" {
			t.Fatalf("%+v from\n%s", cams, listing)
		}
	}
}

const listedModes = `[dshow @ 0] DirectShow video device options (from video devices)
[dshow @ 0]  Pin "捕获" (alternative pin name "0")
[dshow @ 0]   vcodec=mjpeg  min s=1920x1080 fps=2 max s=1920x1080 fps=60.0002
[dshow @ 0]   vcodec=mjpeg  min s=1920x1080 fps=2 max s=1920x1080 fps=60.0002 (pc, bt470bg/bt709/unknown, center)
[dshow @ 0]   vcodec=mjpeg  min s=3264x2448 fps=20 max s=3264x2448 fps=20
[dshow @ 0]   vcodec=mjpeg  min s=1280x720 fps=5 max s=1280x720 fps=60.0002
[dshow @ 0]   vcodec=mjpeg  min s=640x480 fps=5 max s=640x480 fps=60.0002
[dshow @ 0]   pixel_format=yuyv422  min s=640x480 fps=5 max s=640x480 fps=30
[dshow @ 0]   pixel_format=yuyv422  min s=1280x720 fps=5 max s=1280x720 fps=10
[dshow @ 0]   vcodec=h264  min s=1280x720 fps=5 max s=1280x720 fps=30`

func TestChooseCameraMode(t *testing.T) {
	modes := parseModes([]byte(listedModes))
	if len(modes) != 7 {
		t.Fatalf("%d modes: %+v", len(modes), modes)
	}
	for _, c := range []struct {
		size image.Point
		want cameraMode
	}{
		{image.Pt(320, 320), cameraMode{codec: "mjpeg", size: image.Pt(640, 480), fps: 60.0002}},
		{image.Pt(400, 400), cameraMode{codec: "mjpeg", size: image.Pt(640, 480), fps: 60.0002}},
		{image.Pt(560, 560), cameraMode{codec: "mjpeg", size: image.Pt(1280, 720), fps: 60.0002}}, // 640×480 crops to 480×480
		{image.Pt(1280, 720), cameraMode{codec: "mjpeg", size: image.Pt(1280, 720), fps: 60.0002}},
		{image.Pt(2000, 2000), cameraMode{codec: "mjpeg", size: image.Pt(1920, 1080), fps: 60.0002}}, // none covers: the sharpest
	} {
		if got, ok := chooseMode(modes, c.size); !ok || got != c.want {
			t.Errorf("for %v: %+v, want %+v", c.size, got, c.want)
		}
	}
	// A camera with only raw pixels, and only slow above VGA.
	raw := parseModes([]byte(`  pixel_format=yuyv422  min s=640x480 fps=5 max s=640x480 fps=30
  pixel_format=yuyv422  min s=1280x720 fps=5 max s=1280x720 fps=10`))
	if got, _ := chooseMode(raw, image.Pt(800, 450)); got.size != image.Pt(640, 480) || got.pixels != "yuyv422" {
		t.Errorf("raw: %+v", got)
	}
	if strings.Join(raw[0].args(), " ") != "-pixel_format yuyv422 -video_size 640x480 -framerate 30" {
		t.Errorf("args %v", raw[0].args())
	}
	if _, ok := chooseMode(nil, image.Pt(100, 100)); ok {
		t.Error("a mode was chosen from none")
	}
}

func TestCameraArgs(t *testing.T) {
	args := strings.Join(cameraArgs(CameraOptions{Camera: Camera{ID: "@device_pnp_x"}, Size: image.Pt(400, 300), Mirror: true}, []string{"-vcodec", "mjpeg"}), " ")
	for _, want := range []string{"-f dshow -rtbufsize 64M -vcodec mjpeg -i video=@device_pnp_x",
		`crop=w=min(iw\,ih*400/300):h=min(ih\,iw*300/400),scale=400:300:flags=bicubic,hflip,format=rgba`, "-f rawvideo -pix_fmt rgba pipe:1"} {
		if !strings.Contains(args, want) {
			t.Errorf("lacks %q:\n%s", want, args)
		}
	}
}

// TestShowARealCamera reads the first camera that works, where there is one.
func TestShowARealCamera(t *testing.T) {
	needFFmpeg(t)
	if !Supported() {
		t.Skip(ErrUnsupported)
	}
	cams, err := Cameras(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, cam := range cams {
		var frames atomic.Int32
		var bad atomic.Bool
		size := image.Pt(320, 240)
		f, err := StartCamera(CameraOptions{Camera: cam, Size: size, Mirror: true}, func(rgba []byte) {
			if len(rgba) != size.X*size.Y*4 {
				bad.Store(true)
			}
			frames.Add(1)
		})
		if errors.Is(err, ErrNoCamera) {
			t.Logf("%s: %v", cam.Name, err) // a virtual camera with nothing behind it, or one in use
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		f.Stop()
		if n := frames.Load(); n < 10 || bad.Load() {
			t.Errorf("%s: %d frames in a second, wrong size %v", cam.Name, n, bad.Load())
		}
		if f.Err() != nil {
			t.Errorf("a camera that was stopped reports %v", f.Err())
		}
		return
	}
	t.Skip("no camera that works is attached")
}
