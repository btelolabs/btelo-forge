// CoreAudio AudioQueue output on macOS: low-latency 16-bit stereo PCM playback with a hardware sample clock.
package wave

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreAudio -framework CoreFoundation
#include <AudioToolbox/AudioToolbox.h>
#include <CoreAudio/CoreAudio.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#define WAVE_BUFFERS 8
#define WAVE_FRAMES  512
#define WAVE_BYTES   (WAVE_FRAMES * 4)

typedef struct {
	AudioQueueRef       queue;
	AudioQueueBufferRef bufs[WAVE_BUFFERS];
	int                 freeIdx[WAVE_BUFFERS];
	int                 freeCount;
	int                 inFlight;
	int                 started;
	int                 stopped;
	int                 closed;
	pthread_mutex_t     mu;
	pthread_cond_t      cond;
} waveQueue;

static void waveCallback(void *userData, AudioQueueRef q, AudioQueueBufferRef buf) {
	waveQueue *w = (waveQueue *)userData;
	pthread_mutex_lock(&w->mu);
	int idx = -1;
	for (int i = 0; i < WAVE_BUFFERS; i++) {
		if (w->bufs[i] == buf) {
			idx = i;
			break;
		}
	}
	if (idx >= 0 && w->freeCount < WAVE_BUFFERS) {
		w->freeIdx[w->freeCount++] = idx;
	}
	if (w->inFlight > 0) {
		w->inFlight--;
	}
	pthread_cond_broadcast(&w->cond);
	pthread_mutex_unlock(&w->mu);
}

static waveQueue *waveOpen(int sampleRate, OSStatus *outErr) {
	AudioObjectPropertyAddress addr = {
		kAudioHardwarePropertyDefaultOutputDevice,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain,
	};
	AudioDeviceID dev = kAudioObjectUnknown;
	UInt32 devSize = sizeof(dev);
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &addr, 0, NULL, &devSize, &dev) != noErr || dev == kAudioObjectUnknown) {
		*outErr = kAudioHardwareBadDeviceError;
		return NULL;
	}

	waveQueue *w = (waveQueue *)calloc(1, sizeof(waveQueue));
	if (!w) {
		*outErr = -108;
		return NULL;
	}
	pthread_mutex_init(&w->mu, NULL);
	pthread_cond_init(&w->cond, NULL);

	AudioStreamBasicDescription fmt;
	memset(&fmt, 0, sizeof(fmt));
	fmt.mSampleRate       = (Float64)sampleRate;
	fmt.mFormatID         = kAudioFormatLinearPCM;
	fmt.mFormatFlags      = kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked;
	fmt.mFramesPerPacket  = 1;
	fmt.mChannelsPerFrame = 2;
	fmt.mBitsPerChannel   = 16;
	fmt.mBytesPerPacket   = 4;
	fmt.mBytesPerFrame    = 4;

	OSStatus st = AudioQueueNewOutput(&fmt, waveCallback, w, NULL, NULL, 0, &w->queue);
	if (st != noErr) {
		pthread_cond_destroy(&w->cond);
		pthread_mutex_destroy(&w->mu);
		free(w);
		*outErr = st;
		return NULL;
	}
	for (int i = 0; i < WAVE_BUFFERS; i++) {
		st = AudioQueueAllocateBuffer(w->queue, WAVE_BYTES, &w->bufs[i]);
		if (st != noErr) {
			AudioQueueDispose(w->queue, true);
			pthread_cond_destroy(&w->cond);
			pthread_mutex_destroy(&w->mu);
			free(w);
			*outErr = st;
			return NULL;
		}
		w->freeIdx[i] = i;
	}
	w->freeCount = WAVE_BUFFERS;
	*outErr = noErr;
	return w;
}

// waveAcquire waits up to timeoutMs for a free buffer index, returning -1 on timeout or -2 if stopped/closed.
static int waveAcquire(waveQueue *w, int timeoutMs) {
	pthread_mutex_lock(&w->mu);
	if (w->stopped || w->closed) {
		pthread_mutex_unlock(&w->mu);
		return -2;
	}
	if (w->freeCount == 0 && timeoutMs > 0) {
		struct timespec ts;
		clock_gettime(CLOCK_REALTIME, &ts);
		ts.tv_sec  += timeoutMs / 1000;
		ts.tv_nsec += (long)(timeoutMs % 1000) * 1000000L;
		if (ts.tv_nsec >= 1000000000L) {
			ts.tv_sec += 1;
			ts.tv_nsec -= 1000000000L;
		}
		pthread_cond_timedwait(&w->cond, &w->mu, &ts);
	}
	int res = -1;
	if (w->stopped || w->closed) {
		res = -2;
	} else if (w->freeCount > 0) {
		res = w->freeIdx[--w->freeCount];
	}
	pthread_mutex_unlock(&w->mu);
	return res;
}

static OSStatus waveEnqueue(waveQueue *w, int idx, const void *pcm, int length) {
	pthread_mutex_lock(&w->mu);
	if (w->stopped || w->closed) {
		if (w->freeCount < WAVE_BUFFERS) {
			w->freeIdx[w->freeCount++] = idx;
		}
		pthread_mutex_unlock(&w->mu);
		return -1;
	}
	AudioQueueBufferRef buf = w->bufs[idx];
	if (length > WAVE_BYTES) {
		length = WAVE_BYTES;
	}
	memcpy(buf->mAudioData, pcm, (size_t)length);
	buf->mAudioDataByteSize = (UInt32)length;
	w->inFlight++;
	OSStatus st = AudioQueueEnqueueBuffer(w->queue, buf, 0, NULL);
	if (st != noErr) {
		w->inFlight--;
		if (w->freeCount < WAVE_BUFFERS) {
			w->freeIdx[w->freeCount++] = idx;
		}
		pthread_mutex_unlock(&w->mu);
		return st;
	}
	if (!w->started) {
		st = AudioQueueStart(w->queue, NULL);
		if (st == noErr) {
			w->started = 1;
		}
	}
	pthread_mutex_unlock(&w->mu);
	return st;
}

// waveWaitDrain waits up to timeoutMs for all in-flight buffers to finish; returns 1 when drained, 0 if still playing, -2 if stopped.
static int waveWaitDrain(waveQueue *w, int timeoutMs) {
	pthread_mutex_lock(&w->mu);
	if (w->stopped || w->closed) {
		pthread_mutex_unlock(&w->mu);
		return -2;
	}
	if (w->inFlight == 0) {
		pthread_mutex_unlock(&w->mu);
		return 1;
	}
	struct timespec ts;
	clock_gettime(CLOCK_REALTIME, &ts);
	ts.tv_sec  += timeoutMs / 1000;
	ts.tv_nsec += (long)(timeoutMs % 1000) * 1000000L;
	if (ts.tv_nsec >= 1000000000L) {
		ts.tv_sec += 1;
		ts.tv_nsec -= 1000000000L;
	}
	pthread_cond_timedwait(&w->cond, &w->mu, &ts);
	int res = (w->stopped || w->closed) ? -2 : (w->inFlight == 0 ? 1 : 0);
	pthread_mutex_unlock(&w->mu);
	return res;
}

static long long wavePlayed(waveQueue *w) {
	pthread_mutex_lock(&w->mu);
	if (w->closed || !w->started || !w->queue) {
		pthread_mutex_unlock(&w->mu);
		return -1;
	}
	AudioTimeStamp ts;
	memset(&ts, 0, sizeof(ts));
	OSStatus st = AudioQueueGetCurrentTime(w->queue, NULL, &ts, NULL);
	pthread_mutex_unlock(&w->mu);
	if (st != noErr || !(ts.mFlags & kAudioTimeStampSampleTimeValid) || ts.mSampleTime < 0) {
		return -1;
	}
	return (long long)ts.mSampleTime;
}

static void waveStopNow(waveQueue *w) {
	pthread_mutex_lock(&w->mu);
	if (w->closed) {
		pthread_mutex_unlock(&w->mu);
		return;
	}
	w->stopped = 1;
	AudioQueueRef q = w->queue;
	pthread_cond_broadcast(&w->cond);
	pthread_mutex_unlock(&w->mu);
	if (q) {
		AudioQueueStop(q, true);
	}
}

static void waveDispose(waveQueue *w) {
	if (!w) {
		return;
	}
	AudioQueueRef q = w->queue;
	w->queue = NULL;
	if (q) {
		AudioQueueStop(q, true);
		AudioQueueDispose(q, true);
	}
	pthread_cond_destroy(&w->cond);
	pthread_mutex_destroy(&w->mu);
	free(w);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

const bufferFrames = C.WAVE_FRAMES

// Output is one CoreAudio AudioQueue stream. Play runs on one goroutine; Played and Close may be called from any.
type Output struct {
	mu       sync.Mutex
	q        *C.waveQueue
	refs     int // number of active users plus 1 while open; q is disposed when refs hits 0
	written  atomic.Int64
	lastPlay atomic.Int64
	closed   bool
}

// Open prepares playback on the default output device.
func Open() (*Output, error) {
	var st C.OSStatus
	q := C.waveOpen(C.int(Rate), &st)
	if q == nil {
		return nil, fmt.Errorf("AudioQueueNewOutput: OSStatus %d", int(st))
	}
	return &Output{q: q, refs: 1}, nil
}

func (o *Output) retain() *C.waveQueue {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.q == nil {
		return nil
	}
	o.refs++
	return o.q
}

func (o *Output) release(q *C.waveQueue) {
	o.mu.Lock()
	o.refs--
	dispose := o.refs == 0 && o.q == q
	if dispose {
		o.q = nil
	}
	o.mu.Unlock()
	if dispose {
		C.waveDispose(q)
	}
}

// Play streams little-endian s16 stereo PCM from r until it ends or stop closes, then waits for the tail to be heard.
func (o *Output) Play(r io.Reader, stop <-chan struct{}) error {
	q := o.retain()
	if q == nil {
		return ErrStopped
	}
	defer o.release(q)

	// Wake and stop the queue immediately when stop closes.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			C.waveStopNow(q)
		case <-done:
		}
	}()

	block := make([]byte, bufferFrames*4)
	for {
		n, err := io.ReadFull(r, block)
		n -= n % 4
		if n > 0 {
			for {
				select {
				case <-stop:
					return ErrStopped
				default:
				}
				idx := C.waveAcquire(q, 20)
				if idx == -2 {
					return ErrStopped
				}
				if idx < 0 {
					continue
				}
				st := C.waveEnqueue(q, idx, unsafe.Pointer(&block[0]), C.int(n))
				runtime.KeepAlive(block)
				if st == -1 {
					return ErrStopped
				}
				if st != 0 {
					return fmt.Errorf("AudioQueueEnqueueBuffer: OSStatus %d", int(st))
				}
				o.written.Add(int64(n / 4))
				break
			}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			return err
		}
	}

	// Wait for queued audio buffers to finish playing.
	for {
		select {
		case <-stop:
			return ErrStopped
		default:
		}
		r := C.waveWaitDrain(q, 20)
		if r == -2 {
			return ErrStopped
		}
		if r == 1 {
			break
		}
	}
	return nil
}

// Played is the number of frames the device has played since Open.
func (o *Output) Played() int64 {
	q := o.retain()
	written := o.written.Load()
	if q == nil {
		return max(o.lastPlay.Load(), written)
	}
	t := int64(C.wavePlayed(q))
	o.release(q)
	if t < 0 {
		return o.lastPlay.Load()
	}
	if t > written {
		t = written
	}
	for {
		prev := o.lastPlay.Load()
		if t <= prev {
			return prev
		}
		if o.lastPlay.CompareAndSwap(prev, t) {
			return t
		}
	}
}

// Close stops playback and releases the AudioQueue once all in-flight calls return.
func (o *Output) Close() error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	q := o.q
	o.mu.Unlock()
	if q != nil {
		C.waveStopNow(q)
		o.release(q)
	}
	return nil
}
