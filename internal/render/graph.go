package render

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/project"
)

// Overlay is a pre-rendered caption or image placed at (X, Y) of the output frame between Start and End.
type Overlay struct {
	Path       string
	X, Y       int
	Start, End float64
}

// Spec selects what one ffmpeg run produces.
type Spec struct {
	Project       *project.Project
	From, To      float64
	Width, Height int
	FPS           float64
	Video, Audio  bool
	PixFmt        string
	Overlays      []Overlay
}

// Graph is the inputs and filter graph of one run; VideoOut and AudioOut are its labelled outputs, empty when absent.
type Graph struct {
	Inputs   []string
	Filter   string
	VideoOut string
	AudioOut string
	Length   float64
}

// maxCommandLine keeps the arguments below the Windows command-line limit of 32767 characters.
const maxCommandLine = 30000

// ErrEmpty means the range contains nothing to play.
var ErrEmpty = errors.New("nothing to play in this range")

// ErrSilent means an audio-only run was asked for a range with no sound.
var ErrSilent = errors.New("no sound in this range")

// ErrTooLong means the edit has so many pieces that ffmpeg's command line would exceed the Windows limit.
var ErrTooLong = errors.New("too many segments for one ffmpeg command line")

// Audible reports whether anything is heard between output times from and to.
func Audible(p *project.Project, from, to float64) bool { return cut(p, from, to).audible() }

func num(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

// atempo chains tempo filters so each stays within 0.5–2, where the filter sounds cleanest.
func atempo(speed float64) string {
	var parts []string
	for speed >= 2-epsilon {
		parts = append(parts, "atempo=2")
		speed /= 2
	}
	for speed <= 0.5+epsilon {
		parts = append(parts, "atempo=0.5")
		speed /= 0.5
	}
	if speed < 1-epsilon || speed > 1+epsilon {
		parts = append(parts, "atempo="+num(speed))
	}
	return strings.Join(parts, ",")
}

const audioFormat = "aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo"

// builder accumulates inputs and filter chains.
type builder struct {
	inputs  []string
	filters []string
	n       int
}

func (b *builder) input(args ...string) int {
	b.inputs = append(b.inputs, args...)
	b.n++
	return b.n - 1
}

func (b *builder) add(format string, args ...any) {
	b.filters = append(b.filters, fmt.Sprintf(format, args...))
}

// fitted scales a picture into the frame, keeping its shape, with black bars where it does not fill it.
func fitted(w, h int) string {
	return fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=bicubic,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,setsar=1", w, h, w, h)
}

// sound is the chain that turns a piece's audio into the common format at its gain and length.
func sound(pc piece) string {
	chain := []string{"asetpts=PTS-STARTPTS"}
	if t := atempo(pc.Speed); t != "" {
		chain = append(chain, t)
	}
	l := num(pc.length())
	return strings.Join(append(chain, "volume="+num(pc.Gain), audioFormat, "apad=whole_dur="+l, "atrim=end="+l), ",")
}

// Build assembles the inputs and filter graph for spec.
func Build(spec Spec) (Graph, error) {
	w := cut(spec.Project, spec.From, spec.To)
	if w.length <= epsilon {
		return Graph{}, ErrEmpty
	}
	b := &builder{}
	audio := spec.Audio && w.audible()
	video := ""
	var mix []string
	mainIn := make([]int, len(w.main))
	for i, pc := range w.main {
		mainIn[i] = b.input("-ss", num(pc.In), "-t", num(pc.Out-pc.In), "-i", pc.Path)
	}
	if spec.Video {
		video = b.mainVideo(spec, w, mainIn)
	}
	if audio {
		mix = append(mix, b.mainSound(w, mainIn))
	}
	k := 0
	for _, row := range w.upper {
		for _, pc := range row {
			in := b.input("-ss", num(pc.In), "-t", num(pc.Out-pc.In), "-i", pc.Path)
			if spec.Video {
				b.add("[%d:v]setpts=(PTS-STARTPTS)/%s+%s/TB,fps=%s,%s,%s[uv%d]", in, num(pc.Speed), num(pc.Delay), num(spec.FPS), fitted(spec.Width, spec.Height), translucent(pc.Opacity), k)
				next := fmt.Sprintf("[up%d]", k)
				b.add("%s[uv%d]overlay=eof_action=pass:enable='between(t,%s,%s)'%s", video, k, num(pc.Delay), num(pc.Delay+pc.length()), next)
				video = next
			}
			if audio && pc.Sound {
				b.add("[%d:a]%s,adelay=%d:all=1[ua%d]", in, sound(pc), int(pc.Delay*1000+0.5), k)
				mix = append(mix, fmt.Sprintf("[ua%d]", k))
			}
			k++
		}
	}
	if audio {
		for i, c := range w.cues {
			in := b.input("-ss", num(c.Offset), "-t", num(c.Length), "-i", c.Path)
			chain := []string{"asetpts=PTS-STARTPTS", audioFormat, "volume=" + num(c.Volume)}
			if c.FadeIn {
				chain = append(chain, "afade=t=in:d="+num(c.fadeLength()))
			}
			if c.FadeOut {
				chain = append(chain, fmt.Sprintf("afade=t=out:st=%s:d=%s", num(c.Length-c.fadeLength()), num(c.fadeLength())))
			}
			chain = append(chain, fmt.Sprintf("adelay=%d:all=1", int(c.Delay*1000+0.5)))
			b.add("[%d:a]%s[mu%d]", in, strings.Join(chain, ","), i)
			mix = append(mix, fmt.Sprintf("[mu%d]", i))
		}
	}
	g := Graph{Length: w.length}
	if audio {
		if len(mix) == 1 {
			b.add("%satrim=end=%s[aout]", mix[0], num(w.length))
		} else {
			b.add("%samix=inputs=%d:duration=first:dropout_transition=0:normalize=0,alimiter=limit=0.95:level=0,atrim=end=%s[aout]",
				strings.Join(mix, ""), len(mix), num(w.length))
		}
		g.AudioOut = "[aout]"
	}
	if spec.Video {
		for i, o := range spec.Overlays {
			in := b.input("-i", o.Path)
			next := fmt.Sprintf("[ov%d]", i)
			b.add("%s[%d:v]overlay=x=%d:y=%d:eof_action=repeat:enable='between(t,%s,%s)'%s", video, in, o.X, o.Y, num(o.Start-spec.From), num(o.End-spec.From), next)
			video = next
		}
		pix := spec.PixFmt
		if pix == "" {
			pix = "yuv420p"
		}
		b.add("%sformat=%s[vout]", video, pix)
		g.VideoOut = "[vout]"
	}
	if g.VideoOut == "" && g.AudioOut == "" {
		return Graph{}, ErrSilent
	}
	g.Inputs, g.Filter = b.inputs, strings.Join(b.filters, ";")
	return g, nil
}

// translucent formats an upper-track picture with its opacity as alpha, so the tracks below show through.
func translucent(opacity float64) string {
	if opacity >= 1 {
		return "format=yuv420p"
	}
	return "format=yuva420p,colorchannelmixer=aa=" + num(max(opacity, 0))
}

// dimmed fades a main-track picture towards the black beneath it; it is empty for a solid picture.
func dimmed(opacity float64) string {
	if opacity >= 1 {
		return ""
	}
	o := num(max(opacity, 0))
	return "colorchannelmixer=rr=" + o + ":gg=" + o + ":bb=" + o + ","
}

// mainVideo joins the main track's pieces back to back, padded with black when the upper tracks run past them.
func (b *builder) mainVideo(spec Spec, w window, inputs []int) string {
	if len(w.main) == 0 {
		b.add("color=c=black:s=%dx%d:r=%s:d=%s,format=yuv420p[base]", spec.Width, spec.Height, num(spec.FPS), num(w.length))
		return "[base]"
	}
	var c strings.Builder
	for i, pc := range w.main {
		in := inputs[i]
		b.add("[%d:v]setpts=(PTS-STARTPTS)/%s,fps=%s,%s,%sformat=yuv420p[mv%d]", in, num(pc.Speed), num(spec.FPS), fitted(spec.Width, spec.Height), dimmed(pc.Opacity), i)
		fmt.Fprintf(&c, "[mv%d]", i)
	}
	fmt.Fprintf(&c, "concat=n=%d:v=1:a=0", len(w.main))
	if pad := w.length - w.mainLength; pad > epsilon {
		fmt.Fprintf(&c, ",tpad=stop_mode=add:stop_duration=%s:color=black", num(pad))
	}
	b.add("%s[base]", c.String())
	return "[base]"
}

// mainSound joins the main track's sound, with silence for pieces that have none, to the length of the range.
func (b *builder) mainSound(w window, inputs []int) string {
	heard := false
	for _, pc := range w.main {
		heard = heard || pc.Sound
	}
	if !heard {
		b.add("anullsrc=r=48000:cl=stereo,atrim=end=%s[am]", num(w.length))
		return "[am]"
	}
	var c strings.Builder
	for i, pc := range w.main {
		if pc.Sound {
			b.add("[%d:a]%s[ma%d]", inputs[i], sound(pc), i)
		} else {
			b.add("anullsrc=r=48000:cl=stereo,atrim=end=%s[ma%d]", num(pc.length()), i)
		}
		fmt.Fprintf(&c, "[ma%d]", i)
	}
	fmt.Fprintf(&c, "concat=n=%d:v=0:a=1,apad=whole_dur=%s[am]", len(w.main), num(w.length))
	b.add("%s", c.String())
	return "[am]"
}

// Args joins the global flags, inputs, graph and output options, refusing command lines Windows cannot run.
func (g Graph) Args(output ...string) ([]string, error) {
	args := []string{"-v", "error", "-nostdin", "-y"}
	args = append(args, g.Inputs...)
	args = append(args, "-filter_complex", g.Filter)
	if g.VideoOut != "" {
		args = append(args, "-map", g.VideoOut)
	}
	if g.AudioOut != "" {
		args = append(args, "-map", g.AudioOut)
	}
	args = append(args, output...)
	n := 0
	for _, a := range args {
		n += len(a) + 3
	}
	if n > maxCommandLine {
		return nil, fmt.Errorf("%w (%d characters)", ErrTooLong, n)
	}
	return args, nil
}
