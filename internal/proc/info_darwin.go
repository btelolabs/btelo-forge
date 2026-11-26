//go:build darwin

package proc

/*
#include <libproc.h>
#include <sys/proc.h>
#include <stdlib.h>

// bsdInfo reads PROC_PIDTBSDINFO for pid. Returns 1 if alive (not zombie), 0 otherwise.
static int forge_bsd_info(int pid, int *ppid, int64_t *startSec, int32_t *startUsec, char *comm, int commLen) {
	struct proc_bsdinfo info;
	int ret = proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info));
	if (ret <= 0 || ret < (int)sizeof(info)) {
		return 0;
	}
	if (info.pbi_status == SZOMB) {
		return 0;
	}
	if (ppid) *ppid = (int)info.pbi_ppid;
	if (startSec) *startSec = (int64_t)info.pbi_start_tvsec;
	if (startUsec) *startUsec = (int32_t)info.pbi_start_tvusec;
	if (comm && commLen > 0) {
		const char *src = info.pbi_name[0] ? info.pbi_name : info.pbi_comm;
		int i = 0;
		for (; i < commLen - 1 && src[i]; i++) {
			comm[i] = src[i];
		}
		comm[i] = '\0';
	}
	return 1;
}

static int forge_pid_path(int pid, char *buf, uint32_t bufSize) {
	return proc_pidpath(pid, buf, bufSize);
}
*/
import "C"

import (
	"path/filepath"
	"time"
	"unsafe"
)

// processInfo returns the executable path and start time of pid via Darwin libproc, or ok=false if the process does
// not exist or has exited (including unreaped zombies).
func processInfo(pid int) (image string, began time.Time, ok bool) {
	if pid <= 0 {
		return "", time.Time{}, false
	}
	var startSec C.int64_t
	var startUsec C.int32_t
	var comm [64]C.char
	if C.forge_bsd_info(C.int(pid), nil, &startSec, &startUsec, &comm[0], C.int(len(comm))) == 0 {
		return "", time.Time{}, false
	}
	began = time.Unix(int64(startSec), int64(startUsec)*1000)
	var pathBuf [C.PROC_PIDPATHINFO_MAXSIZE]C.char
	if n := C.forge_pid_path(C.int(pid), &pathBuf[0], C.uint32_t(len(pathBuf))); n > 0 {
		image = C.GoStringN(&pathBuf[0], n)
	} else {
		image = C.GoString(&comm[0])
	}
	return image, began, true
}

// sameImage reports whether the executable path (or name) reported by libproc refers to program, accounting for
// macOS firmlink/symlink aliases such as /var vs /private/var and /tmp vs /private/tmp.
func sameImage(image, program string) bool {
	if image == "" || program == "" {
		return false
	}
	if samePath(image, program) {
		return true
	}
	// Resolve symlinks when both paths are absolute (e.g. /var -> /private/var).
	if filepath.IsAbs(image) && filepath.IsAbs(program) {
		ri, err1 := filepath.EvalSymlinks(image)
		rp, err2 := filepath.EvalSymlinks(program)
		if err1 == nil && err2 == nil && samePath(ri, rp) {
			return true
		}
		return false
	}
	// Fallback when proc_pidpath fell back to a short process name.
	return samePath(filepath.Base(image), filepath.Base(program))
}

// listAllPids returns all currently visible process IDs via proc_listallpids.
func listAllPids() []int {
	n := C.proc_listallpids(nil, 0)
	if n <= 0 {
		return nil
	}
	buf := make([]C.int, int(n)+64)
	got := C.proc_listallpids(unsafe.Pointer(&buf[0]), C.int(len(buf)*int(unsafe.Sizeof(C.int(0)))))
	if got <= 0 {
		return nil
	}
	if int(got) > len(buf) {
		got = C.int(len(buf))
	}
	out := make([]int, 0, int(got))
	for _, p := range buf[:got] {
		if p > 0 {
			out = append(out, int(p))
		}
	}
	return out
}

type procNode struct {
	ppid    int
	startUs int64
}

// snapshotProcs reads the parent PID and start timestamp for every live (non-zombie) process.
func snapshotProcs() (map[int]procNode, map[int][]int) {
	pids := listAllPids()
	nodes := make(map[int]procNode, len(pids))
	children := make(map[int][]int, len(pids)/4)
	for _, pid := range pids {
		var ppid C.int
		var startSec C.int64_t
		var startUsec C.int32_t
		if C.forge_bsd_info(C.int(pid), &ppid, &startSec, &startUsec, nil, 0) == 0 {
			continue
		}
		nodes[pid] = procNode{
			ppid:    int(ppid),
			startUs: int64(startSec)*1_000_000 + int64(startUsec),
		}
		if int(ppid) != pid {
			children[int(ppid)] = append(children[int(ppid)], pid)
		}
	}
	return nodes, children
}

// descendants returns all live descendant PIDs of root, verifying start times to guard against PID reuse.
func descendants(root int) []int {
	nodes, children := snapshotProcs()
	if _, ok := nodes[root]; !ok {
		return nil
	}
	return descendantsFrom(root, nodes, children)
}

func descendantsFrom(root int, nodes map[int]procNode, children map[int][]int) []int {
	var out []int
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		pnode, ok := nodes[parent]
		if !ok {
			continue
		}
		for _, child := range children[parent] {
			if seen[child] {
				continue
			}
			cnode, ok := nodes[child]
			if !ok || cnode.startUs < pnode.startUs {
				continue
			}
			seen[child] = true
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}
