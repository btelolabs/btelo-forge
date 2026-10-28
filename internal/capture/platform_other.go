//go:build !windows

package capture

// Supported reports whether this system can record its screen.
func Supported() bool { return false }

// CheckSystem says why this system cannot record.
func CheckSystem() error { return ErrUnsupported }

// Displays lists the screens that can be recorded.
func Displays() ([]Display, error) { return nil, ErrUnsupported }

// Microphones lists the recording devices.
func Microphones() ([]AudioDevice, error) { return nil, ErrUnsupported }

func captureAudio(loopback bool, id string, origin int64, deliver func(samples []float32, frames int, at int64), opened func(error), stop <-chan struct{}) {
	opened(ErrUnsupported)
}

func counter() int64          { return 0 }
func counterFrequency() int64 { return 1 }
func wallMicros() int64       { return 0 }
