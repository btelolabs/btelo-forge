package wave

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"unsafe"
)

var (
	winmm         = syscall.NewLazyDLL("winmm.dll")
	waveOpen      = winmm.NewProc("waveOutOpen")
	wavePrepare   = winmm.NewProc("waveOutPrepareHeader")
	waveWrite     = winmm.NewProc("waveOutWrite")
	waveReset     = winmm.NewProc("waveOutReset")
	waveUnprepare = winmm.NewProc("waveOutUnprepareHeader")
	waveClose     = winmm.NewProc("waveOutClose")
	wavePosition  = winmm.NewProc("waveOutGetPosition")
	kernel        = syscall.NewLazyDLL("kernel32.dll")
	createEvent   = kernel.NewProc("CreateEventW")
	waitEvent     = kernel.NewProc("WaitForSingleObject")
	closeHandle   = kernel.NewProc("CloseHandle")
	virtualAlloc  = kernel.NewProc("VirtualAlloc")
	virtualFree   = kernel.NewProc("VirtualFree")
	moveMemory    = kernel.NewProc("RtlMoveMemory")
)

const (
	bufferFrames = 2048
	bufferCount  = 8
	headerSize   = unsafe.Sizeof(header{})
)

type header struct {
	Data             uintptr
	Length, Recorded uint32
	User             uintptr
	Flags, Loops     uint32
	Next, Reserved   uintptr
}

type buffer struct {
	memory              uintptr
	prepared, submitted bool
}

// Output is one open WinMM device. Play runs on one goroutine; Played and Close may be called from any.
type Output struct {
	mu            sync.Mutex
	handle, event uintptr
	buffers       [bufferCount]buffer
	next          int
	written       int64
}

func mm(op string, r uintptr) error {
	if r == 0 {
		return nil
	}
	return fmt.Errorf("%s: WinMM error %d", op, r)
}

// Open opens the default output device.
func Open() (*Output, error) {
	o := &Output{}
	event, _, err := createEvent.Call(0, 0, 0, 0)
	if event == 0 {
		return nil, fmt.Errorf("CreateEventW: %w", err)
	}
	o.event = event
	format := struct {
		Tag, Channels           uint16
		Rate, ByteRate          uint32
		BlockAlign, Bits, Extra uint16
	}{Tag: 1, Channels: 2, Rate: Rate, ByteRate: Rate * 4, BlockAlign: 4, Bits: 16}
	const callbackEvent = 0x00050000
	r, _, _ := waveOpen.Call(uintptr(unsafe.Pointer(&o.handle)), 0xffffffff, uintptr(unsafe.Pointer(&format)), o.event, 0, callbackEvent)
	if err := mm("waveOutOpen", r); err != nil {
		return nil, errors.Join(err, o.Close())
	}
	for i := range o.buffers {
		b := &o.buffers[i]
		mem, _, allocErr := virtualAlloc.Call(0, headerSize+bufferFrames*4, 0x3000, 4)
		if mem == 0 {
			return nil, errors.Join(fmt.Errorf("VirtualAlloc: %w", allocErr), o.Close())
		}
		b.memory = mem
		h := header{Data: mem + headerSize, Length: bufferFrames * 4}
		moveMemory.Call(mem, uintptr(unsafe.Pointer(&h)), headerSize)
		if err := mm("waveOutPrepareHeader", call(wavePrepare, o.handle, mem, headerSize)); err != nil {
			return nil, errors.Join(err, o.Close())
		}
		b.prepared = true
	}
	return o, nil
}

func call(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

// write queues one block of up to bufferFrames frames, waiting for a free buffer.
func (o *Output) write(pcm []byte, stop <-chan struct{}) error {
	b := &o.buffers[o.next]
	for b.submitted && o.flags(b)&1 == 0 { // WHDR_DONE
		select {
		case <-stop:
			return ErrStopped
		default:
		}
		waitEvent.Call(o.event, 20)
	}
	select {
	case <-stop:
		return ErrStopped
	default:
	}
	moveMemory.Call(b.memory+headerSize, uintptr(unsafe.Pointer(&pcm[0])), uintptr(len(pcm)))
	length := uint32(len(pcm))
	moveMemory.Call(b.memory+unsafe.Offsetof(header{}.Length), uintptr(unsafe.Pointer(&length)), 4)
	if err := mm("waveOutWrite", call(waveWrite, o.handle, b.memory, headerSize)); err != nil {
		return err
	}
	b.submitted = true
	o.mu.Lock()
	o.written += int64(len(pcm) / 4)
	o.mu.Unlock()
	o.next = (o.next + 1) % bufferCount
	return nil
}

func (o *Output) flags(b *buffer) uint32 {
	var f uint32
	moveMemory.Call(uintptr(unsafe.Pointer(&f)), b.memory+unsafe.Offsetof(header{}.Flags), 4)
	return f
}

// Play streams little-endian s16 stereo PCM from r until it ends or stop closes, then waits for the tail to be heard.
func (o *Output) Play(r io.Reader, stop <-chan struct{}) error {
	block := make([]byte, bufferFrames*4)
	for {
		n, err := io.ReadFull(r, block)
		n -= n % 4
		if n > 0 {
			if werr := o.write(block[:n], stop); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	for o.Played() < o.written {
		select {
		case <-stop:
			return ErrStopped
		default:
		}
		waitEvent.Call(o.event, 20)
	}
	return nil
}

// Played is the number of frames the device has played since Open.
func (o *Output) Played() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.handle == 0 {
		return o.written
	}
	t := struct {
		Type  uint32
		Value uint32
		Pad   [4]byte
	}{Type: 2} // TIME_SAMPLES
	if call(wavePosition, o.handle, uintptr(unsafe.Pointer(&t)), unsafe.Sizeof(t)) != 0 || t.Type != 2 {
		return 0
	}
	return int64(t.Value)
}

// Close stops playback and releases the device.
func (o *Output) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	var errs []error
	if o.handle != 0 {
		errs = append(errs, mm("waveOutReset", call(waveReset, o.handle)))
		for i := range o.buffers {
			if b := &o.buffers[i]; b.prepared {
				errs = append(errs, mm("waveOutUnprepareHeader", call(waveUnprepare, o.handle, b.memory, headerSize)))
				b.prepared = false
			}
		}
		errs = append(errs, mm("waveOutClose", call(waveClose, o.handle)))
		o.handle = 0
	}
	for i := range o.buffers {
		if b := &o.buffers[i]; b.memory != 0 {
			virtualFree.Call(b.memory, 0, 0x8000)
			b.memory = 0
		}
	}
	if o.event != 0 {
		closeHandle.Call(o.event)
		o.event = 0
	}
	return errors.Join(errs...)
}
