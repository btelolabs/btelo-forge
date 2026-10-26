package win

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	enumMonitors   = user32.NewProc("EnumDisplayMonitors")
	monitorInfo    = user32.NewProc("GetMonitorInfoW")
	shcore         = syscall.NewLazyDLL("shcore.dll")
	dpiForMonitor  = shcore.NewProc("GetDpiForMonitor")
	comdlg32       = syscall.NewLazyDLL("comdlg32.dll")
	getOpenName    = comdlg32.NewProc("GetOpenFileNameW")
	openClipboard  = user32.NewProc("OpenClipboard")
	closeClipboard = user32.NewProc("CloseClipboard")
	clipboardData  = user32.NewProc("GetClipboardData")
	globalLock     = kernel32.NewProc("GlobalLock")
	globalUnlock   = kernel32.NewProc("GlobalUnlock")
	monitorsFound  []monitor
	monitorsBack   = syscall.NewCallback(func(h, _, _, _ uintptr) uintptr {
		info := struct {
			Size         uint32
			Screen, Work rect
			Flags        uint32
		}{Size: 40}
		if r, _, _ := monitorInfo.Call(h, uintptr(unsafe.Pointer(&info))); r != 0 {
			var dx, dy uint32
			if r, _, _ := dpiForMonitor.Call(h, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r != 0 || dx == 0 {
				dx = 96
			}
			monitorsFound = append(monitorsFound, monitor{work: info.Work, primary: info.Flags&1 != 0, dpi: dx})
		}
		return 1
	})
)

type monitor struct {
	work    rect
	primary bool
	dpi     uint32
}

func chooseMonitor(secondary bool) monitor {
	monitorsFound = monitorsFound[:0]
	enumMonitors.Call(0, 0, monitorsBack, 0)
	choice := monitor{work: rect{0, 0, 1600, 900}, primary: true, dpi: 96}
	for _, m := range monitorsFound {
		if m.primary != secondary {
			return m
		}
		if m.primary {
			choice = m
		}
	}
	return choice
}

// OpenFiles shows the system open dialog; filter is pairs of label and patterns such as {"Videos", "*.mp4;*.mov"}.
// It returns nil when the user cancels.
func (w *Window) OpenFiles(title string, filter [][2]string, multiple bool) []string {
	var spec []uint16
	for _, f := range filter {
		spec = append(spec, utf16.Encode([]rune(f[0]))...)
		spec = append(spec, 0)
		spec = append(spec, utf16.Encode([]rune(f[1]))...)
		spec = append(spec, 0)
	}
	spec = append(spec, 0)
	buf := make([]uint16, 64*1024)
	t, _ := syscall.UTF16PtrFromString(title)
	const explorer, mustExist, pathMustExist, multi, noChangeDir = 0x80000, 0x1000, 0x800, 0x200, 0x8
	flags := uint32(explorer | mustExist | pathMustExist | noChangeDir)
	if multiple {
		flags |= multi
	}
	ofn := struct {
		Size                   uint32
		Owner, Instance        uintptr
		Filter, CustomFilter   *uint16
		MaxCustom, FilterIndex uint32
		File                   *uint16
		MaxFile                uint32
		FileTitle              *uint16
		MaxFileTitle           uint32
		InitialDir, Title      *uint16
		Flags                  uint32
		FileOffset, FileExt    uint16
		DefExt                 *uint16
		CustData, Hook         uintptr
		Template               *uint16
		Reserved1              uintptr
		Reserved2, FlagsEx     uint32
	}{Owner: w.Handle, Filter: &spec[0], FilterIndex: 1, File: &buf[0], MaxFile: uint32(len(buf)), Title: t, Flags: flags}
	ofn.Size = uint32(unsafe.Sizeof(ofn))
	if r, _, _ := getOpenName.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
		w.input.Down = [3]bool{}
		return nil
	}
	w.input.Down = [3]bool{}
	var parts []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i == start {
			break
		}
		parts = append(parts, syscall.UTF16ToString(buf[start:i]))
		start = i + 1
	}
	if len(parts) <= 1 {
		return parts
	}
	files := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		files = append(files, filepath.Join(parts[0], name))
	}
	return files
}

// Clipboard returns the clipboard's text, or an error when there is none.
func (w *Window) Clipboard() (string, error) {
	if r, _, _ := openClipboard.Call(w.Handle); r == 0 {
		return "", errors.New("clipboard busy")
	}
	defer closeClipboard.Call()
	const unicodeText = 13
	h, _, _ := clipboardData.Call(unicodeText)
	if h == 0 {
		return "", errors.New("clipboard has no text")
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		return "", errors.New("clipboard lock failed")
	}
	defer globalUnlock.Call(h)
	n, _, _ := kernel32.NewProc("lstrlenW").Call(p)
	units := make([]uint16, n)
	if n > 0 {
		moveMemory.Call(uintptr(unsafe.Pointer(&units[0])), p, n*2)
	}
	return strings.ReplaceAll(string(utf16.Decode(units)), "\r\n", "\n"), nil
}

// Reveal opens Explorer with path selected.
func Reveal(path string) error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	return cmd.Start()
}

// Launch opens path with its default application.
func Launch(path string) error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe "` + path + `"`}
	return cmd.Start()
}

// LaunchURL opens a web page in the default browser.
func LaunchURL(url string) error {
	shellExecute := shell32.NewProc("ShellExecuteW")
	verb, _ := syscall.UTF16PtrFromString("open")
	target, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	const showNormal = 1
	if r, _, e := shellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, showNormal); r <= 32 {
		return e
	}
	return nil
}
