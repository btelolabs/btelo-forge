package proc

import (
	"time"

	"golang.org/x/sys/windows"
)

// processInfo is the program file of the process pid and the time it began, and false when there is no such process.
func processInfo(pid int) (image string, began time.Time, ok bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", time.Time{}, false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil || code != 259 { // STILL_ACTIVE
		return "", time.Time{}, false
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return "", time.Time{}, false
	}
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return "", time.Time{}, false
	}
	return windows.UTF16ToString(buf[:n]), time.Unix(0, created.Nanoseconds()), true
}

// sameImage reports whether the program file the system names is the program: the same file.
func sameImage(image, program string) bool { return samePath(image, program) }
