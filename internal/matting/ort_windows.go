package matting

import (
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

// ONNX Runtime's C interface, without cgo: a table of functions whose order never changes. These are the places in the
// table of onnxruntime_c_api.h.
const (
	apiVersion = 24

	apiGetErrorMessage   = 2
	apiCreateEnv         = 3
	apiCreateSession     = 7
	apiRun               = 9
	apiCreateOptions     = 10
	apiSetExecutionMode  = 13
	apiDisableMemPattern = 17
	apiCreateTensor      = 49
	apiTensorData        = 51
	apiCreateCPUInfo     = 69
	apiReleaseStatus     = 93
	apiReleaseSession    = 95
	apiReleaseValue      = 96
	apiReleaseOptions    = 100

	onnxFloat     = 1 // ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT
	warningsOnly  = 2 // ORT_LOGGING_LEVEL_WARNING
	deviceMemory  = 0 // OrtDeviceAllocator
	defaultMemory = 0 // OrtMemTypeDefault
)

type ort struct {
	table *[512]uintptr
	dml   *syscall.Proc
	env   uintptr
	info  uintptr
	sess  uintptr
}

// call calls the function at place i; a status it returns is an error.
func (o *ort) call(i int, args ...uintptr) error {
	status, _, _ := syscall.SyscallN(o.table[i], args...)
	return o.status(status)
}

func (o *ort) status(status uintptr) error {
	if status == 0 {
		return nil
	}
	msg, _, _ := syscall.SyscallN(o.table[apiGetErrorMessage], status)
	err := fmt.Errorf("ONNX Runtime: %s", goString(msg))
	syscall.SyscallN(o.table[apiReleaseStatus], status)
	return err
}

func goString(p uintptr) string {
	var b []byte
	for ; ; p++ {
		c := *(*byte)(unsafe.Add(unsafe.Pointer(nil), p))
		if c == 0 {
			return string(b)
		}
		b = append(b, c)
	}
}

func cString(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}

// load starts ONNX Runtime from library and reads the model: on a graphics card through DirectML when that works.
func load(library, model string) (network, error) {
	// DirectML.dll lies beside it, and must be found there rather than the older one of the system.
	kernel := syscall.NewLazyDLL("kernel32.dll")
	dir, err := syscall.UTF16PtrFromString(filepath.Dir(library))
	if err != nil {
		return nil, err
	}
	kernel.NewProc("SetDllDirectoryW").Call(uintptr(unsafe.Pointer(dir)))
	dll, err := syscall.LoadDLL(library)
	if err != nil {
		return nil, fmt.Errorf("loading ONNX Runtime: %w", err)
	}
	get, err := dll.FindProc("OrtGetApiBase")
	if err != nil {
		return nil, err
	}
	base, _, _ := get.Call()
	getAPI := *(*uintptr)(unsafe.Add(unsafe.Pointer(nil), base))
	table, _, _ := syscall.SyscallN(getAPI, apiVersion)
	if table == 0 {
		return nil, fmt.Errorf("this ONNX Runtime has no version %d of its interface", apiVersion)
	}
	o := &ort{table: (*[512]uintptr)(unsafe.Add(unsafe.Pointer(nil), table))}
	o.dml, _ = dll.FindProc("OrtSessionOptionsAppendExecutionProvider_DML")
	if err := o.call(apiCreateEnv, warningsOnly, uintptr(unsafe.Pointer(cString("forge"))), uintptr(unsafe.Pointer(&o.env))); err != nil {
		return nil, err
	}
	if err := o.call(apiCreateCPUInfo, deviceMemory, defaultMemory, uintptr(unsafe.Pointer(&o.info))); err != nil {
		return nil, err
	}
	if o.dml != nil {
		if err := o.open(model, true); err == nil {
			return o, nil
		}
	}
	if err := o.open(model, false); err != nil {
		return nil, err
	}
	return o, nil
}

// open reads the model, on the card or not.
func (o *ort) open(model string, card bool) error {
	var options uintptr
	if err := o.call(apiCreateOptions, uintptr(unsafe.Pointer(&options))); err != nil {
		return err
	}
	defer syscall.SyscallN(o.table[apiReleaseOptions], options)
	if card {
		// DirectML wants nodes run in order and no planning of memory ahead.
		if err := o.call(apiDisableMemPattern, options); err != nil {
			return err
		}
		if err := o.call(apiSetExecutionMode, options, 0); err != nil {
			return err
		}
		status, _, _ := o.dml.Call(options, 0)
		if err := o.status(status); err != nil {
			return err
		}
	}
	path, err := syscall.UTF16PtrFromString(model)
	if err != nil {
		return err
	}
	return o.call(apiCreateSession, o.env, uintptr(unsafe.Pointer(path)), options, uintptr(unsafe.Pointer(&o.sess)))
}

// Run looks at a picture.
func (o *ort) Run(rgb []byte) ([]byte, error) {
	data := tensor(rgb)
	shape := [4]int64{1, 3, Side, Side}
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(&data[0])
	pin.Pin(&shape)
	var input, output uintptr
	if err := o.call(apiCreateTensor, o.info, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*4), uintptr(unsafe.Pointer(&shape)), 4, onnxFloat,
		uintptr(unsafe.Pointer(&input))); err != nil {
		return nil, err
	}
	defer syscall.SyscallN(o.table[apiReleaseValue], input)
	inName, outName := cString("input_image"), cString("output_image")
	names := [2]*byte{inName, outName}
	pin.Pin(inName)
	pin.Pin(outName)
	pin.Pin(&names)
	inputs := [1]uintptr{input}
	if err := o.call(apiRun, o.sess, 0, uintptr(unsafe.Pointer(&names[0])), uintptr(unsafe.Pointer(&inputs)), 1,
		uintptr(unsafe.Pointer(&names[1])), 1, uintptr(unsafe.Pointer(&output))); err != nil {
		return nil, err
	}
	defer syscall.SyscallN(o.table[apiReleaseValue], output)
	var values uintptr
	if err := o.call(apiTensorData, output, uintptr(unsafe.Pointer(&values))); err != nil {
		return nil, err
	}
	raw := unsafe.Slice((*float32)(unsafe.Add(unsafe.Pointer(nil), values)), Side*Side)
	out := make([]byte, Side*Side)
	for i, v := range raw {
		// The model says how sure it is on a scale without ends.
		p := 1 / (1 + math.Exp(-float64(v)))
		out[i] = uint8(p*255 + 0.5)
	}
	return out, nil
}

// Close lets go of the model.
func (o *ort) Close() { syscall.SyscallN(o.table[apiReleaseSession], o.sess) }
