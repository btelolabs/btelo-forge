//go:build !windows

package proc

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// processInfo is the program file of the process pid and the time it began, and false when there is no such process.
// ps names the program by its path on macOS and by its name, cut short, on Linux (see sameImage).
func processInfo(pid int) (image string, began time.Time, ok bool) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "etime=", "-o", "comm=").Output()
	if err != nil {
		return "", time.Time{}, false
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return "", time.Time{}, false
	}
	age, ok := parseElapsed(fields[0])
	if !ok {
		return "", time.Time{}, false
	}
	return strings.Join(fields[1:], " "), time.Now().Add(-age), true
}

// parseElapsed reads the elapsed time ps gives, [[dd-]hh:]mm:ss.
func parseElapsed(s string) (time.Duration, bool) {
	days := 0
	if d, rest, found := strings.Cut(s, "-"); found {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	secs := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, false
		}
		secs = secs*60 + n
	}
	return time.Duration(days*86400+secs) * time.Second, true
}

// sameImage reports whether what ps calls the program is the program: the name, which Linux cuts to 15 characters.
func sameImage(image, program string) bool {
	return strings.HasPrefix(filepath.Base(program), filepath.Base(image))
}
