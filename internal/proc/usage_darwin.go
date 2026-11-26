//go:build darwin

package proc

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <libproc.h>
#include <sys/resource.h>
#include <mach/mach_time.h>
#include <IOKit/IOKitLib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	uint64_t cpuNS;
	uint64_t memBytes;
	int      ok;
} ForgeProcUsage;

static mach_timebase_info_data_t forge_tb;

static void forge_init_timebase(void) {
	if (forge_tb.denom == 0) {
		mach_timebase_info(&forge_tb);
		if (forge_tb.denom == 0) {
			forge_tb.numer = 1;
			forge_tb.denom = 1;
		}
	}
}

static ForgeProcUsage forge_proc_rusage(int pid) {
	ForgeProcUsage out = {0, 0, 0};
	forge_init_timebase();
	struct rusage_info_v4 ri;
	memset(&ri, 0, sizeof(ri));
	if (proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&ri) == 0) {
		uint64_t ticks = ri.ri_user_time + ri.ri_system_time;
		out.cpuNS = (ticks / forge_tb.denom) * forge_tb.numer +
		            ((ticks % forge_tb.denom) * forge_tb.numer) / forge_tb.denom;
		out.memBytes = ri.ri_phys_footprint;
		out.ok = 1;
	}
	if (!out.ok || out.memBytes == 0) {
		struct proc_taskinfo ti;
		int ret = proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &ti, sizeof(ti));
		if (ret >= (int)sizeof(ti)) {
			if (!out.ok) {
				uint64_t ticks = ti.pti_total_user + ti.pti_total_system;
				out.cpuNS = (ticks / forge_tb.denom) * forge_tb.numer +
				            ((ticks % forge_tb.denom) * forge_tb.numer) / forge_tb.denom;
			}
			if (out.memBytes == 0) {
				out.memBytes = ti.pti_resident_size;
			}
			out.ok = 1;
		}
	}
	return out;
}

typedef struct {
	int      pid;
	uint64_t gpuTimeNS;
} ForgeGPUSample;

static int parse_creator_pid(CFStringRef creator) {
	if (!creator || CFGetTypeID(creator) != CFStringGetTypeID()) {
		return 0;
	}
	char buf[128];
	if (!CFStringGetCString(creator, buf, sizeof(buf), kCFStringEncodingUTF8)) {
		return 0;
	}
	// Format: "pid 1234, ProcessName"
	if (strncmp(buf, "pid ", 4) != 0) {
		return 0;
	}
	return atoi(buf + 4);
}

// forge_read_gpu_usage enumerates IOAccelerator user clients (e.g. AGXDeviceUserClient on Apple Silicon)
// and returns per-PID accumulated GPU time in nanoseconds without requiring root privileges.
// Returns the number of entries written to *outPtr, or -1 if no IOAccelerator service was found.
static int forge_read_gpu_usage(ForgeGPUSample **outPtr) {
	*outPtr = NULL;
	io_iterator_t iter = 0;
	CFMutableDictionaryRef match = IOServiceMatching("IOAccelerator");
	if (!match) {
		return -1;
	}
	if (IOServiceGetMatchingServices(0, match, &iter) != KERN_SUCCESS) {
		return -1;
	}
	int cap = 64, count = 0, foundAccel = 0;
	ForgeGPUSample *samples = (ForgeGPUSample *)malloc(sizeof(ForgeGPUSample) * cap);
	if (!samples) {
		IOObjectRelease(iter);
		return -1;
	}
	io_service_t accel;
	while ((accel = IOIteratorNext(iter)) != 0) {
		foundAccel = 1;
		io_iterator_t children = 0;
		if (IORegistryEntryGetChildIterator(accel, kIOServicePlane, &children) == KERN_SUCCESS) {
			io_registry_entry_t child;
			while ((child = IOIteratorNext(children)) != 0) {
				CFTypeRef creatorRef = IORegistryEntryCreateCFProperty(child, CFSTR("IOUserClientCreator"), kCFAllocatorDefault, 0);
				int pid = parse_creator_pid((CFStringRef)creatorRef);
				if (creatorRef) CFRelease(creatorRef);
				if (pid > 0) {
					CFTypeRef usageRef = IORegistryEntryCreateCFProperty(child, CFSTR("AppUsage"), kCFAllocatorDefault, 0);
					if (usageRef && CFGetTypeID(usageRef) == CFArrayGetTypeID()) {
						CFArrayRef arr = (CFArrayRef)usageRef;
						CFIndex n = CFArrayGetCount(arr);
						uint64_t totalNS = 0;
						for (CFIndex i = 0; i < n; i++) {
							CFTypeRef item = CFArrayGetValueAtIndex(arr, i);
							if (item && CFGetTypeID(item) == CFDictionaryGetTypeID()) {
								CFTypeRef val = CFDictionaryGetValue((CFDictionaryRef)item, CFSTR("accumulatedGPUTime"));
								if (val && CFGetTypeID(val) == CFNumberGetTypeID()) {
									int64_t ns = 0;
									if (CFNumberGetValue((CFNumberRef)val, kCFNumberSInt64Type, &ns) && ns > 0) {
										totalNS += (uint64_t)ns;
									}
								}
							}
						}
						if (totalNS > 0) {
							if (count >= cap) {
								cap *= 2;
								ForgeGPUSample *grown = (ForgeGPUSample *)realloc(samples, sizeof(ForgeGPUSample) * cap);
								if (!grown) {
									CFRelease(usageRef);
									IOObjectRelease(child);
									break;
								}
								samples = grown;
							}
							samples[count].pid = pid;
							samples[count].gpuTimeNS = totalNS;
							count++;
						}
					}
					if (usageRef) CFRelease(usageRef);
				}
				IOObjectRelease(child);
			}
			IOObjectRelease(children);
		}
		IOObjectRelease(accel);
	}
	IOObjectRelease(iter);
	if (!foundAccel) {
		free(samples);
		return -1;
	}
	*outPtr = samples;
	return count;
}
*/
import "C"

import (
	"runtime"
	"sync"
	"time"
	"unsafe"
)

type cpuSample struct {
	usedNS uint64
	at     time.Time
}

type gpuSample struct {
	usedNS uint64
	at     time.Time
}

// Meter measures programs natively on macOS using libproc (proc_pid_rusage for CPU time and physical memory
// footprint) and IOKit IOAccelerator AppUsage for per-process GPU utilization.
type Meter struct {
	mu      sync.Mutex
	prevCPU map[int]cpuSample
	prevGPU map[int]gpuSample
}

// NewMeter starts measuring; Close ends it.
func NewMeter() *Meter {
	return &Meter{
		prevCPU: map[int]cpuSample{},
		prevGPU: map[int]gpuSample{},
	}
}

// Close releases what the meter holds.
func (m *Meter) Close() {}

// readGPUUsage returns per-PID cumulative GPU time in nanoseconds and whether IOKit GPU counters were available.
func readGPUUsage() (map[int]uint64, bool) {
	var ptr *C.ForgeGPUSample
	n := int(C.forge_read_gpu_usage(&ptr))
	if n < 0 {
		return nil, false
	}
	defer C.free(unsafe.Pointer(ptr))
	out := make(map[int]uint64, n)
	if n > 0 && ptr != nil {
		items := unsafe.Slice(ptr, n)
		for _, it := range items {
			out[int(it.pid)] += uint64(it.gpuTimeNS)
		}
	}
	return out, true
}

// Sample measures each program in pids together with everything it started. A program that has ended is left out.
func (m *Meter) Sample(pids ...int) map[int]Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(pids) == 0 {
		return map[int]Usage{}
	}
	now := time.Now()
	nodes, children := snapshotProcs()
	gpuTimes, gpuOK := readGPUUsage()
	cpus := float64(runtime.NumCPU())

	out := make(map[int]Usage, len(pids))
	alive := make(map[int]bool, len(pids)*2)

	for _, root := range pids {
		if _, ok := nodes[root]; !ok {
			continue
		}
		tree := append([]int{root}, descendantsFrom(root, nodes, children)...)
		var u Usage
		var found bool
		for _, pid := range tree {
			ru := C.forge_proc_rusage(C.int(pid))
			if ru.ok == 0 {
				continue
			}
			found = true
			alive[pid] = true
			usedNS := uint64(ru.cpuNS)
			if before, ok := m.prevCPU[pid]; ok {
				if wall := now.Sub(before.at); wall > 0 && usedNS >= before.usedNS {
					u.CPU += float64(usedNS-before.usedNS) * 100 / float64(wall.Nanoseconds()) / cpus
				}
			}
			m.prevCPU[pid] = cpuSample{usedNS: usedNS, at: now}
			u.Memory += uint64(ru.memBytes)

			if gpuOK {
				gns := gpuTimes[pid]
				if before, ok := m.prevGPU[pid]; ok {
					if wall := now.Sub(before.at); wall > 0 && gns >= before.usedNS {
						u.GPU += float64(gns-before.usedNS) * 100 / float64(wall.Nanoseconds())
					}
				}
				m.prevGPU[pid] = gpuSample{usedNS: gns, at: now}
			}
		}
		if !found {
			continue
		}
		u.CPU = min(u.CPU, 100)
		u.GPU = min(u.GPU, 100)
		u.GPUKnown = gpuOK
		out[root] = u
	}

	for pid := range m.prevCPU {
		if !alive[pid] {
			delete(m.prevCPU, pid)
		}
	}
	for pid := range m.prevGPU {
		if !alive[pid] {
			delete(m.prevGPU, pid)
		}
	}
	return out
}
