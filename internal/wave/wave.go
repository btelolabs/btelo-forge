// Package wave plays 16-bit stereo PCM and reports how much of it has been heard; the sound card's progress is the
// clock the preview follows.
package wave

import "errors"

// Rate is the sample rate every stream is decoded to.
const Rate = 48000

// ErrStopped is returned by Play once stop is closed.
var ErrStopped = errors.New("playback stopped")
