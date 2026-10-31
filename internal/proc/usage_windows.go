package proc

import (
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Meter measures programs. The processor share is the change between one call and the next, so the first call for a
// program reports none.
type Meter struct {
	mu   sync.Mutex
	prev map[uint32]cpuTime
	gpu  *gpuCounters
}

type cpuTime struct {
	used uint64 // 100 ns units of processor time
	at   time.Time
}

// NewMeter starts measuring; Close ends it.
func NewMeter() *Meter { return &Meter{prev: map[uint32]cpuTime{}, gpu: openGPUCounters()} }

// Close releases what the meter holds.
func (m *Meter) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gpu.close()
	m.gpu = nil
}

var (
	psapi                = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemory = psapi.NewProc("GetProcessMemoryInfo")
)

// processMemoryCounters is PROCESS_MEMORY_COUNTERS.
type processMemoryCounters struct {
	Size                       uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// Sample measures each program in pids together with everything it started. A program that has ended is left out.
func (m *Meter) Sample(pids ...int) map[int]Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	gpuMem, gpuUse, gpuOK := m.gpu.read()
	out := map[int]Usage{}
	alive := map[uint32]bool{}
	for _, root := range pids {
		tree := append([]uint32{uint32(root)}, descendants(uint32(root))...)
		var u Usage
		var found bool
		for _, pid := range tree {
			h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
			if err != nil {
				continue
			}
			var created, exited, kernel, user windows.Filetime
			if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
				found = true
				alive[pid] = true
				used := uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)
				used += uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime)
				if before, ok := m.prev[pid]; ok {
					if wall := now.Sub(before.at); wall > 0 && used >= before.used {
						u.CPU += float64(used-before.used) * 100 / (float64(wall.Nanoseconds()) / 100) / float64(runtime.NumCPU())
					}
				}
				m.prev[pid] = cpuTime{used, now}
				var mem processMemoryCounters
				mem.Size = uint32(unsafe.Sizeof(mem))
				if r, _, _ := procGetProcessMemory.Call(uintptr(h), uintptr(unsafe.Pointer(&mem)), uintptr(mem.Size)); r != 0 {
					u.Memory += uint64(mem.WorkingSetSize)
				}
			}
			windows.CloseHandle(h)
			u.GPUMemory += gpuMem[int(pid)]
			u.GPU += gpuUse[int(pid)]
		}
		if !found {
			continue
		}
		u.GPU = min(u.GPU, 100)
		u.GPUKnown = gpuOK
		out[root] = u
	}
	for pid := range m.prev {
		if !alive[pid] {
			delete(m.prev, pid)
		}
	}
	return out
}

// gpuCounters reads the performance counters Windows keeps for each program's use of the graphics processor.
type gpuCounters struct {
	query  uintptr
	memory uintptr
	engine uintptr
	ok     bool
}

var (
	pdh                   = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery      = pdh.NewProc("PdhOpenQueryW")
	procPdhAddCounter     = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollect        = pdh.NewProc("PdhCollectQueryData")
	procPdhFormattedArray = pdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhClose          = pdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble   = 0x200
	pdhFmtLarge    = 0x400
	pdhFmtNoCap100 = 0x8000
	pdhMoreData    = 0x800007D2
)

func openGPUCounters() *gpuCounters {
	g := &gpuCounters{}
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&g.query))); r != 0 {
		return g
	}
	add := func(path string, into *uintptr) bool {
		p, _ := syscall.UTF16PtrFromString(path)
		r, _, _ := procPdhAddCounter.Call(g.query, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(into)))
		return r == 0
	}
	if !add(`\GPU Process Memory(*)\Dedicated Usage`, &g.memory) || !add(`\GPU Engine(*)\Utilization Percentage`, &g.engine) {
		procPdhClose.Call(g.query)
		g.query = 0
		return g
	}
	procPdhCollect.Call(g.query) // the first reading only starts the rates
	g.ok = true
	return g
}

func (g *gpuCounters) close() {
	if g != nil && g.query != 0 {
		procPdhClose.Call(g.query)
		g.query, g.ok = 0, false
	}
}

// counterInstance is the start of a counter instance's name, "pid_1234_", whose number is the program's id.
var counterInstance = regexp.MustCompile(`^pid_(\d+)_`)

// engineIgnored are the engines of a graphics processor that do not run models: copying memory and video.
var engineIgnored = regexp.MustCompile(`engtype_(Copy|VideoDecode|VideoEncode|VideoProcessing)`)

// read is the video memory in bytes and the processor share in percent of each program, and whether the counters work.
func (g *gpuCounters) read() (memory map[int]uint64, use map[int]float64, ok bool) {
	if g == nil || !g.ok {
		return nil, nil, false
	}
	if r, _, _ := procPdhCollect.Call(g.query); r != 0 {
		return nil, nil, false
	}
	memory, use = map[int]uint64{}, map[int]float64{}
	for name, v := range formattedArray(g.memory, pdhFmtLarge) {
		if m := counterInstance.FindStringSubmatch(name); m != nil {
			pid, _ := strconv.Atoi(m[1])
			memory[pid] += uint64(v.large)
		}
	}
	for name, v := range formattedArray(g.engine, pdhFmtDouble|pdhFmtNoCap100) {
		if m := counterInstance.FindStringSubmatch(name); m != nil && !engineIgnored.MatchString(name) {
			pid, _ := strconv.Atoi(m[1])
			use[pid] += v.double
		}
	}
	return memory, use, true
}

type counterValue struct {
	large  int64
	double float64
}

// formattedArray reads every instance of a wildcard counter: its name and value.
func formattedArray(counter uintptr, format uint32) map[string]counterValue {
	var size, count uint32
	r, _, _ := procPdhFormattedArray.Call(counter, uintptr(format), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if uint32(r) != pdhMoreData || size == 0 {
		return nil
	}
	buf := make([]byte, size)
	if r, _, _ := procPdhFormattedArray.Call(counter, uintptr(format), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0]))); r != 0 {
		return nil
	}
	// PDH_FMT_COUNTERVALUE_ITEM_W: a pointer to the name, then the value (a status, padding, and the number).
	type item struct {
		name   *uint16
		status uint32
		_      uint32
		value  [8]byte
	}
	items := unsafe.Slice((*item)(unsafe.Pointer(&buf[0])), count)
	out := make(map[string]counterValue, count)
	for _, it := range items {
		if it.status != 0 && it.status != 1 { // valid, or a new instance's first reading
			continue
		}
		out[windows.UTF16PtrToString(it.name)] = counterValue{
			large:  *(*int64)(unsafe.Pointer(&it.value[0])),
			double: *(*float64)(unsafe.Pointer(&it.value[0])),
		}
	}
	return out
}
