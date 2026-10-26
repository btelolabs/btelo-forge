package media

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Encoders are the video encoders this computer's ffmpeg can actually use.
type Encoders map[string]bool

// Software encoders are listed by ffmpeg; hardware encoders are listed whenever ffmpeg was built with them, so they
// are tried on a few frames to find out whether the graphics card and driver can run them.
var (
	softwareEncoders = []string{"libx264", "libx265", "libsvtav1", "libaom-av1"}
	hardwareEncoders = []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc"}
)

var (
	encodersMu sync.Mutex
	probed     = map[string]Encoders{} // by ffmpeg path
)

// AvailableEncoders finds the usable encoders of the ffmpeg in use and remembers them; it takes a second or two the
// first time for each ffmpeg.
func AvailableEncoders() (Encoders, error) {
	path, err := tool("ffmpeg")
	if err != nil {
		return nil, err
	}
	encodersMu.Lock()
	defer encodersMu.Unlock()
	if enc, ok := probed[path]; ok {
		return enc, nil
	}
	enc, err := probeEncoders(context.Background())
	if err != nil {
		return nil, err
	}
	probed[path] = enc
	return enc, nil
}

func probeEncoders(ctx context.Context) (Encoders, error) {
	cmd, err := Command(ctx, "ffmpeg", "-hide_banner", "-encoders")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && len(f[0]) == 6 && f[0][0] == 'V' {
			listed[f[1]] = true
		}
	}
	found := Encoders{}
	for _, name := range softwareEncoders {
		found[name] = listed[name]
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, name := range hardwareEncoders {
		if !listed[name] {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := tryEncoder(ctx, name)
			mu.Lock()
			found[name] = ok
			mu.Unlock()
		}()
	}
	wg.Wait()
	return found, nil
}

// tryEncoder encodes a few frames with name and reports whether it worked.
func tryEncoder(ctx context.Context, name string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd, err := Command(ctx, "ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "color=c=gray:s=640x360:r=30:d=0.2",
		"-pix_fmt", "yuv420p", "-c:v", name, "-f", "null", "-")
	if err != nil {
		return false
	}
	return cmd.Run() == nil
}
