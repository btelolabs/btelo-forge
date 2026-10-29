package proc

import (
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var bound struct {
	once sync.Once
	job  windows.Handle
	err  error
}

// Bind ties a started helper program to this one: Windows ends it when this program ends, however that happens, so
// a helper that holds memory on the graphics card is never left running after a crash.
func Bind(p *os.Process) error {
	bound.once.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			bound.err = err
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(job)
			bound.err = err
			return
		}
		bound.job = job // kept open until this program ends, which closes it and ends the helpers
	})
	if bound.err != nil {
		return bound.err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(bound.job, h)
}
