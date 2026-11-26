package wave

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestAudioQueuePlayClockAndStop(t *testing.T) {
	out, err := Open()
	if err != nil {
		t.Skip("no audio device:", err)
	}
	defer out.Close()

	// 0.3s of silence at 48kHz stereo s16le.
	pcm := make([]byte, (Rate*3/10)*4)
	stop := make(chan struct{})
	start := time.Now()
	if err := out.Play(bytes.NewReader(pcm), stop); err != nil {
		t.Fatalf("Play: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 200*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("0.3s clip played in %v", elapsed)
	}
	if played := out.Played(); played < int64(Rate*2/10) {
		t.Fatalf("Played() = %d, expected >= %d", played, Rate*2/10)
	}
}

func TestAudioQueueStopsPromptly(t *testing.T) {
	out, err := Open()
	if err != nil {
		t.Skip("no audio device:", err)
	}
	defer out.Close()

	// 3s of silence; stop after 80ms.
	pcm := make([]byte, Rate*3*4)
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- out.Play(bytes.NewReader(pcm), stop)
	}()
	time.Sleep(80 * time.Millisecond)
	stopAt := time.Now()
	close(stop)
	err = <-done
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("Play returned %v, want ErrStopped", err)
	}
	if took := time.Since(stopAt); took > 250*time.Millisecond {
		t.Fatalf("stop took %v, expected < 250ms", took)
	}
}

func TestAudioQueueConcurrentCloseDuringPlay(t *testing.T) {
	out, err := Open()
	if err != nil {
		t.Skip("no audio device:", err)
	}

	pcm := make([]byte, Rate*2*4)
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- out.Play(bytes.NewReader(pcm), stop)
	}()
	time.Sleep(60 * time.Millisecond)
	if err := out.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("Play after Close returned %v, want ErrStopped", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Play did not return after Close")
	}
	// Idempotent Close and safe Played after Close.
	if err := out.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	_ = out.Played()
}
