package media

import (
	"context"
	"os"
	"strings"
)

// OpusWebM gives a .webm file with PCM sound, as a video model's engine writes it, the Opus sound the format requires, so
// that players other than ffmpeg open it. The picture is copied; a file without PCM sound is left as it is.
func OpusWebM(ctx context.Context, path string) error {
	codec, err := AudioCodec(ctx, path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(codec, "pcm_") {
		return nil
	}
	tmp := path + ".opus.webm"
	defer os.Remove(tmp)
	if _, err := run(ctx, "ffmpeg", "-y", "-v", "error", "-i", path, "-map", "0", "-c:v", "copy", "-c:a", "libopus", "-b:a", "192k", tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// AudioCodec is the name of the first sound track's codec, "" for a file without sound.
func AudioCodec(ctx context.Context, path string) (string, error) {
	out, err := run(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name", "-of", "csv=p=0", path)
	return strings.TrimSpace(string(out)), err
}
