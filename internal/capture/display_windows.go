package capture

import (
	"runtime"
	"syscall"
	"unsafe"
)

var (
	dxgi              = syscall.NewLazyDLL("dxgi.dll")
	createFactory1    = dxgi.NewProc("CreateDXGIFactory1")
	user32            = syscall.NewLazyDLL("user32.dll")
	configBufferSizes = user32.NewProc("GetDisplayConfigBufferSizes")
	queryConfig       = user32.NewProc("QueryDisplayConfig")
	configDeviceInfo  = user32.NewProc("DisplayConfigGetDeviceInfo")
	displaySettings   = user32.NewProc("EnumDisplaySettingsW")
)

var iidFactory1 = guid{0x770aae78, 0xf26f, 0x4dba, [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}

// Method indexes in the DXGI interfaces.
const (
	factoryEnumAdapters1 = 12
	adapterEnumOutputs   = 7
	outputGetDesc        = 7
)

type outputDesc struct {
	DeviceName [32]uint16
	Desktop    struct{ Left, Top, Right, Bottom int32 }
	Attached   int32
	Rotation   uint32
	Monitor    uintptr
}

// Displays lists the screens in the order ffmpeg's ddagrab numbers them: by graphics adapter, then by output.
func Displays() ([]Display, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	done, err := comApartment()
	if err != nil {
		return nil, err
	}
	defer done()
	var factory *object
	if err := hresult("CreateDXGIFactory1", callProc(createFactory1, uintptr(unsafe.Pointer(&iidFactory1)), uintptr(unsafe.Pointer(&factory)))); err != nil {
		return nil, err
	}
	defer factory.release()
	names := monitorNames()
	var list []Display
	const notFound = 0x887A0002 // DXGI_ERROR_NOT_FOUND ends both enumerations
	for a := 0; ; a++ {
		var adapter *object
		hr := factory.call(factoryEnumAdapters1, uintptr(a), uintptr(unsafe.Pointer(&adapter)))
		if uint32(hr) == notFound {
			break
		}
		if err := hresult("EnumAdapters1", hr); err != nil {
			return nil, err
		}
		for o := 0; ; o++ {
			var output *object
			hr := adapter.call(adapterEnumOutputs, uintptr(o), uintptr(unsafe.Pointer(&output)))
			if uint32(hr) == notFound {
				break
			}
			if err := hresult("EnumOutputs", hr); err != nil {
				adapter.release()
				return nil, err
			}
			var desc outputDesc
			hr = output.call(outputGetDesc, uintptr(unsafe.Pointer(&desc)))
			output.release()
			if err := hresult("IDXGIOutput::GetDesc", hr); err != nil {
				adapter.release()
				return nil, err
			}
			if desc.Attached == 0 {
				continue
			}
			device := syscall.UTF16ToString(desc.DeviceName[:])
			r := desc.Desktop
			d := Display{
				Adapter: a, Output: o, Device: device, Name: names[device],
				X: int(r.Left), Y: int(r.Top), Width: int(r.Right - r.Left), Height: int(r.Bottom - r.Top),
				Primary: r.Left == 0 && r.Top == 0,
			}
			// A process that is not DPI aware gets scaled desktop coordinates from DXGI; the display mode is
			// always in real pixels.
			if x, y, w, h, ok := displayMode(desc.DeviceName[:]); ok {
				d.X, d.Y, d.Width, d.Height = x, y, w, h
			}
			list = append(list, d)
		}
		adapter.release()
	}
	if len(list) == 0 {
		return nil, ErrNoDisplay
	}
	return list, nil
}

// displayMode is a display's position on the desktop and size, in pixels, from its current mode.
func displayMode(device []uint16) (x, y, w, h int, ok bool) {
	var mode [220]byte // DEVMODEW
	const size, position, width, height = 68, 76, 172, 176
	*(*uint16)(unsafe.Pointer(&mode[size])) = uint16(len(mode))
	const currentSettings = ^uintptr(0) // ENUM_CURRENT_SETTINGS
	if callProc(displaySettings, uintptr(unsafe.Pointer(&device[0])), currentSettings, uintptr(unsafe.Pointer(&mode[0]))) == 0 {
		return 0, 0, 0, 0, false
	}
	at := func(off int) int32 { return *(*int32)(unsafe.Pointer(&mode[off])) }
	return int(at(position)), int(at(position + 4)), int(at(width)), int(at(height)), true
}

func callProc(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

type luid struct {
	Low  uint32
	High int32
}

type pathInfo struct {
	SourceAdapter luid
	SourceID      uint32
	_             [2]uint32
	TargetAdapter luid
	TargetID      uint32
	_             [9]uint32
	Flags         uint32
}

type configHeader struct {
	Type, Size uint32
	Adapter    luid
	ID         uint32
}

// monitorNames maps each display's GDI device name (\\.\DISPLAY1) to the monitor's own name, as Windows shows it in
// the display settings; a monitor that reports no name is left out.
func monitorNames() map[string]string {
	names := map[string]string{}
	const activeOnly = 2
	var paths, modes uint32
	if callProc(configBufferSizes, activeOnly, uintptr(unsafe.Pointer(&paths)), uintptr(unsafe.Pointer(&modes))) != 0 || paths == 0 {
		return names
	}
	pathList := make([]pathInfo, paths)
	modeList := make([][64]byte, modes)
	if callProc(queryConfig, activeOnly, uintptr(unsafe.Pointer(&paths)), uintptr(unsafe.Pointer(&pathList[0])),
		uintptr(unsafe.Pointer(&modes)), uintptr(unsafe.Pointer(&modeList[0])), 0) != 0 {
		return names
	}
	for _, p := range pathList[:paths] {
		source := struct {
			configHeader
			Device [32]uint16
		}{configHeader: configHeader{Type: 1, Adapter: p.SourceAdapter, ID: p.SourceID}}
		source.Size = uint32(unsafe.Sizeof(source))
		target := struct {
			configHeader
			Flags, Technology uint32
			Maker, Product    uint16
			Connector         uint32
			Friendly          [64]uint16
			MonitorDevicePath [128]uint16
		}{configHeader: configHeader{Type: 2, Adapter: p.TargetAdapter, ID: p.TargetID}}
		target.Size = uint32(unsafe.Sizeof(target))
		if callProc(configDeviceInfo, uintptr(unsafe.Pointer(&source))) != 0 || callProc(configDeviceInfo, uintptr(unsafe.Pointer(&target))) != 0 {
			continue
		}
		if name := syscall.UTF16ToString(target.Friendly[:]); name != "" {
			names[syscall.UTF16ToString(source.Device[:])] = name
		}
	}
	return names
}
