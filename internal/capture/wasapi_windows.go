package capture

import (
	"errors"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	clsidEnumerator = guid{0xbcde0395, 0xe52f, 0x467c, [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidEnumerator   = guid{0xa95664d2, 0x9614, 0x4f35, [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	iidAudioClient  = guid{0x1cb9ad4c, 0xdbfa, 0x4c32, [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	iidCapture      = guid{0xc8adbd64, 0xe71e, 0x48a0, [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}
	subtypeFloat    = guid{0x00000003, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	keyFriendlyName = propertyKey{guid{0xa45c254e, 0xdf1c, 0x4efd, [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, 14}
)

// Method indexes in the Core Audio interfaces.
const (
	enumeratorEnumEndpoints   = 3
	enumeratorDefaultEndpoint = 4
	enumeratorGetDevice       = 5
	collectionCount           = 3
	collectionItem            = 4
	deviceActivate            = 3
	deviceOpenProperties      = 4
	deviceGetID               = 5
	propertiesGetValue        = 5
	clientInitialize          = 3
	clientStart               = 10
	clientStop                = 11
	clientGetService          = 14
	captureGetBuffer          = 3
	captureReleaseBuffer      = 4
	captureNextPacket         = 5
)

const (
	flowRender, flowCapture = 0, 1
	roleConsole             = 0
	stateActive             = 1
	clsctxAll               = 0x17
	errNotFound             = 0x80070490 // E_NOTFOUND: no such endpoint, for example no default microphone
	bufferSilent            = 0x2
)

type propertyKey struct {
	ID  guid
	Pid uint32
}

type waveFormat struct {
	Tag            uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
	ValidBits      uint16
	ChannelMask    uint32
	SubFormat      guid
}

// enumerator opens the system's audio endpoint list on a thread already in the COM apartment.
func enumerator() (*object, error) {
	var e *object
	hr := callProc(coCreate, uintptr(unsafe.Pointer(&clsidEnumerator)), 0, clsctxAll, uintptr(unsafe.Pointer(&iidEnumerator)), uintptr(unsafe.Pointer(&e)))
	return e, hresult("CoCreateInstance(MMDeviceEnumerator)", hr)
}

// Microphones lists the active recording devices, the system default first.
func Microphones() ([]AudioDevice, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	done, err := comApartment()
	if err != nil {
		return nil, err
	}
	defer done()
	e, err := enumerator()
	if err != nil {
		return nil, err
	}
	defer e.release()
	def := ""
	var d *object
	if hr := e.call(enumeratorDefaultEndpoint, flowCapture, roleConsole, uintptr(unsafe.Pointer(&d))); int32(hr) >= 0 {
		def, _ = deviceID(d)
		d.release()
	}
	var all *object
	if err := hresult("EnumAudioEndpoints", e.call(enumeratorEnumEndpoints, flowCapture, stateActive, uintptr(unsafe.Pointer(&all)))); err != nil {
		return nil, err
	}
	defer all.release()
	var n uint32
	if err := hresult("IMMDeviceCollection::GetCount", all.call(collectionCount, uintptr(unsafe.Pointer(&n)))); err != nil {
		return nil, err
	}
	var list []AudioDevice
	for i := range n {
		var dev *object
		if int32(all.call(collectionItem, uintptr(i), uintptr(unsafe.Pointer(&dev)))) < 0 {
			continue
		}
		id, err := deviceID(dev)
		name := friendlyName(dev)
		dev.release()
		if err != nil {
			continue
		}
		if name == "" {
			name = id
		}
		m := AudioDevice{ID: id, Name: name, Default: id == def}
		if m.Default {
			list = append([]AudioDevice{m}, list...)
		} else {
			list = append(list, m)
		}
	}
	return list, nil
}

func deviceID(d *object) (string, error) {
	var p *uint16
	if err := hresult("IMMDevice::GetId", d.call(deviceGetID, uintptr(unsafe.Pointer(&p)))); err != nil {
		return "", err
	}
	defer coTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return utf16At(p), nil
}

func friendlyName(d *object) string {
	var store *object
	const readOnly = 0
	if int32(d.call(deviceOpenProperties, readOnly, uintptr(unsafe.Pointer(&store)))) < 0 {
		return ""
	}
	defer store.release()
	var v struct {
		Type     uint16
		_        [3]uint16
		Value    *uint16
		Reserved uintptr
	}
	if int32(store.call(propertiesGetValue, uintptr(unsafe.Pointer(&keyFriendlyName)), uintptr(unsafe.Pointer(&v)))) < 0 {
		return ""
	}
	defer propClear.Call(uintptr(unsafe.Pointer(&v)))
	const wideString = 31 // VT_LPWSTR
	if v.Type != wideString {
		return ""
	}
	return utf16At(v.Value)
}

// wasapiStream captures one endpoint in shared mode, converted by Windows to 48 kHz stereo float: the system's sound
// through loopback, or a microphone.
type wasapiStream struct {
	device, client, capture *object
	id                      string
}

// openStream opens the endpoint id, or the default one of its kind when id is empty.
func openStream(e *object, loopback bool, id string) (*wasapiStream, error) {
	flow := uintptr(flowCapture)
	if loopback {
		flow = flowRender
	}
	s := &wasapiStream{}
	var hr uintptr
	if id == "" {
		hr = e.call(enumeratorDefaultEndpoint, flow, roleConsole, uintptr(unsafe.Pointer(&s.device)))
	} else {
		p, err := syscall.UTF16PtrFromString(id)
		if err != nil {
			return nil, err
		}
		hr = e.call(enumeratorGetDevice, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&s.device)))
	}
	if uint32(hr) == errNotFound {
		return nil, errNoDevice
	}
	if err := hresult("open audio endpoint", hr); err != nil {
		return nil, err
	}
	s.id, _ = deviceID(s.device)
	if err := s.start(loopback); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

var errNoDevice = errors.New("no such audio device")

func (s *wasapiStream) start(loopback bool) error {
	if err := hresult("IMMDevice::Activate", s.device.call(deviceActivate, uintptr(unsafe.Pointer(&iidAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&s.client)))); err != nil {
		return err
	}
	f := waveFormat{
		Tag: 0xfffe, Channels: channels, SamplesPerSec: sampleRate, BitsPerSample: 32, Size: 22, ValidBits: 32,
		ChannelMask: 0x3, SubFormat: subtypeFloat,
	}
	f.BlockAlign = f.Channels * f.BitsPerSample / 8
	f.AvgBytesPerSec = f.SamplesPerSec * uint32(f.BlockAlign)
	const (
		streamLoopback    = 0x00020000
		autoConvertPCM    = 0x80000000
		srcDefaultQuality = 0x08000000
		shared            = 0
		buffer100ns       = 2_000_000 // 200 ms
	)
	flags := uintptr(autoConvertPCM | srcDefaultQuality)
	if loopback {
		flags |= streamLoopback
	}
	if err := hresult("IAudioClient::Initialize", s.client.call(clientInitialize, shared, flags, buffer100ns, 0, uintptr(unsafe.Pointer(&f)), 0)); err != nil {
		return err
	}
	if err := hresult("IAudioClient::GetService", s.client.call(clientGetService, uintptr(unsafe.Pointer(&iidCapture)), uintptr(unsafe.Pointer(&s.capture)))); err != nil {
		return err
	}
	return hresult("IAudioClient::Start", s.client.call(clientStart))
}

// read hands every packet waiting in the endpoint's buffer to deliver: its samples, or nil for silence, and the
// performance counter time of its first frame in 100 ns units.
func (s *wasapiStream) read(deliver func(samples []float32, frames int, at100ns int64)) error {
	for {
		var next uint32
		if err := hresult("GetNextPacketSize", s.capture.call(captureNextPacket, uintptr(unsafe.Pointer(&next)))); err != nil {
			return err
		}
		if next == 0 {
			return nil
		}
		var data unsafe.Pointer
		var frames, flags uint32
		var position, qpc uint64
		hr := s.capture.call(captureGetBuffer, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)),
			uintptr(unsafe.Pointer(&position)), uintptr(unsafe.Pointer(&qpc)))
		if err := hresult("IAudioCaptureClient::GetBuffer", hr); err != nil {
			return err
		}
		var samples []float32
		if flags&bufferSilent == 0 && frames > 0 {
			samples = unsafe.Slice((*float32)(data), int(frames)*channels)
		}
		deliver(samples, int(frames), int64(qpc))
		if err := hresult("IAudioCaptureClient::ReleaseBuffer", s.capture.call(captureReleaseBuffer, uintptr(frames))); err != nil {
			return err
		}
	}
}

func (s *wasapiStream) close() {
	if s.client != nil && s.capture != nil {
		s.client.call(clientStop)
	}
	s.capture.release()
	s.client.release()
	s.device.release()
}

// currentDefault is the id of the default endpoint of a kind, or "" when there is none.
func currentDefault(e *object, loopback bool) string {
	flow := uintptr(flowCapture)
	if loopback {
		flow = flowRender
	}
	var d *object
	if int32(e.call(enumeratorDefaultEndpoint, flow, roleConsole, uintptr(unsafe.Pointer(&d)))) < 0 {
		return ""
	}
	defer d.release()
	id, _ := deviceID(d)
	return id
}

// captureAudio runs one source until stop is closed, handing each packet to deliver with its time in sample frames
// since the counter reading origin. It reports once through opened whether the device could be opened. It follows
// the system default device when id is empty, and reopens the device after it was unplugged, changed or failed;
// while there is no device the source is silent, which the mixer fills in.
func captureAudio(loopback bool, id string, origin int64, deliver func(samples []float32, frames int, at int64), opened func(error), stop <-chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	done, err := comApartment()
	if err != nil {
		opened(err)
		return
	}
	defer done()
	e, err := enumerator()
	if err != nil {
		opened(err)
		return
	}
	defer e.release()
	freq := counterFrequency()
	originTicks := origin * 10_000_000 / freq // the origin in the 100 ns units WASAPI uses
	// A chosen device that cannot be opened is an error the user should hear about; a missing default device (no
	// speakers or no microphone plugged in yet) is silence until one appears.
	s, err := openStream(e, loopback, id)
	if err != nil && (id != "" || !errors.Is(err, errNoDevice)) {
		opened(err)
		return
	}
	opened(nil)
	defer func() {
		if s != nil {
			s.close()
		}
	}()
	lastTry, lastCheck := time.Now(), time.Now()
	tick := time.NewTicker(captureInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		now := time.Now()
		if s == nil {
			if now.Sub(lastTry) < time.Second {
				continue
			}
			lastTry = now
			if s, err = openStream(e, loopback, id); err != nil {
				s = nil
				continue
			}
		}
		err := s.read(func(samples []float32, frames int, at100ns int64) {
			deliver(samples, frames, (at100ns-originTicks)*sampleRate/10_000_000)
		})
		if err != nil {
			// Usually AUDCLNT_E_DEVICE_INVALIDATED: the device was unplugged, disabled or its format changed.
			s.close()
			s, lastTry = nil, now
			continue
		}
		if id == "" && now.Sub(lastCheck) >= 2*time.Second {
			lastCheck = now
			if current := currentDefault(e, loopback); current != "" && current != s.id {
				s.close()
				s, lastTry = nil, time.Time{}
			}
		}
	}
}
