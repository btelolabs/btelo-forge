package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func makeClip(t *testing.T, path string, sound ...string) {
	t.Helper()
	args := []string{"-y", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=24:duration=0.5"}
	if len(sound) > 0 {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=32000:duration=0.5", "-c:a", sound[0])
	}
	if _, err := run(context.Background(), "ffmpeg", append(args, "-c:v", "libvpx", "-f", "matroska", path)...); err != nil {
		t.Skipf("ffmpeg could not make the clip: %v", err)
	}
}

func TestPCMSoundInAWebMBecomesOpus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "made.webm")
	makeClip(t, path, "pcm_s16le")
	if err := OpusWebM(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if got, err := AudioCodec(context.Background(), path); err != nil || got != "opus" {
		t.Fatalf("sound is %q: %v", got, err)
	}
	if left, _ := filepath.Glob(path + ".*"); len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

func TestAWebMWithoutPCMSoundIsLeftAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "silent.webm")
	makeClip(t, path)
	before, _ := os.ReadFile(path)
	if err := OpusWebM(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("the file changed")
	}
}
