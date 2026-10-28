package proc

import (
	"os"
	"syscall"
	"unsafe"
)

// KillTree ends a helper program and every program it started, and theirs. Killing only the helper would leave
// the real work running where the helper is a launcher, as package managers' shims for ffmpeg are: the shim dies
// and the ffmpeg it started goes on grabbing the screen or holding a file.
func KillTree(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	tree := descendants(uint32(p.Pid))
	err := p.Kill()
	for _, pid := range tree {
		if h, e := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, pid); e == nil {
			syscall.TerminateProcess(h, 1)
			syscall.CloseHandle(h)
		}
	}
	return err
}

// descendants lists the programs started by pid, directly or not. A process names its parent by id alone, and ids
// are reused, so only processes started after their parent count as its children.
func descendants(root uint32) []uint32 {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(snap)
	children := map[uint32][]uint32{}
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		if e.ProcessID != e.ParentProcessID {
			children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
		}
	}
	var out []uint32
	queue := []uint32{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		born, ok := started(parent)
		if !ok {
			continue
		}
		for _, child := range children[parent] {
			if t, ok := started(child); ok && t >= born {
				out = append(out, child)
				queue = append(queue, child)
			}
		}
	}
	return out
}

// started is when a process began, in Windows file time.
func started(pid uint32) (int64, bool) {
	const queryLimited = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	h, err := syscall.OpenProcess(queryLimited, false, pid)
	if err != nil {
		return 0, false
	}
	defer syscall.CloseHandle(h)
	var created, exited, kernel, user syscall.Filetime
	if syscall.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return 0, false
	}
	return created.Nanoseconds(), true
}
