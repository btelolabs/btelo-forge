package speech

import (
	"context"
	"io"
)

// An api starts one kind of speech server and asks it for speech.
type api interface {
	// prepare writes what the server needs to start with l on port into l.Dir and returns the values of the
	// placeholders of its arguments.
	prepare(l Launch, port int) (map[string]string, error)
	// ready reports whether the server has its model loaded and can speak.
	ready(ctx context.Context, p *process) bool
	// warm makes the server do once, before the first piece of speech, what would otherwise slow that piece.
	warm(ctx context.Context, p *process) error
	// speak writes text said in voice v to w as 16-bit mono PCM and returns its sample rate. made gets the
	// seconds of speech made so far, measured or, from a server that answers all at once, estimated.
	speak(ctx context.Context, p *process, text string, v Voice, w io.Writer, made func(seconds float64)) (int, error)
}

var apis = map[string]api{APIAudioCPP: audioCPP{}, APIBreeze: breeze{}}
