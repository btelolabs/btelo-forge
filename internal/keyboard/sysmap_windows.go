package keyboard

import (
	"encoding/hex"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	layoutKey    = `SYSTEM\CurrentControlSet\Control\Keyboard Layout`
	scancodeName = "Scancode Map"
)

var (
	shellExecuteEx = shell32.NewProc("ShellExecuteExW")
	waitForObject  = kernel32.NewProc("WaitForSingleObject")
	closeHandle    = kernel32.NewProc("CloseHandle")
)

type windowsMap struct{}

// NewSystemMap is the system's table of remapped keys.
func NewSystemMap() SystemMap { return windowsMap{} }

func (windowsMap) Kind() MapKind { return RegistryMap }

func (windowsMap) Read() ([]Mapping, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, layoutKey, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	data, _, err := key.GetBinaryValue(scancodeName)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeScancodeMap(data)
}

// regArguments are the arguments of reg.exe that put a table under a key, or remove it when there is none.
func regArguments(key string, m []Mapping) string {
	if len(m) == 0 {
		return fmt.Sprintf(`delete "%s" /v "%s" /f`, key, scancodeName)
	}
	return fmt.Sprintf(`add "%s" /v "%s" /t REG_BINARY /d %s /f`, key, scancodeName, hex.EncodeToString(encodeScancodeMap(m)))
}

// Write has reg.exe make the change, started with the administrator's approval: this app stays an ordinary program
// and asks only for this. Reading the table back tells whether it happened, as a turned-down prompt and a failed
// reg.exe look alike from here.
func (w windowsMap) Write(m []Mapping) error {
	current, err := w.Read()
	if err != nil {
		return err
	}
	if SameRemap(current, m) {
		return nil
	}
	if err := runElevated("reg.exe", regArguments(`HKLM\`+layoutKey, m)); err != nil {
		return err
	}
	after, err := w.Read()
	if err != nil {
		return err
	}
	if !SameRemap(after, m) {
		return ErrNotWritten
	}
	return nil
}

// runElevated runs a program with the administrator's approval, unseen, and waits for it.
func runElevated(file, args string) error {
	verb, _ := syscall.UTF16PtrFromString("runas")
	program, err := syscall.UTF16PtrFromString(file)
	if err != nil {
		return err
	}
	params, err := syscall.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	const noCloseProcess, hidden = 0x40, 0
	info := struct {
		size, mask                        uint32
		window                            uintptr
		verb, file, parameters, directory *uint16
		show                              int32
		instance, idList                  uintptr
		class                             *uint16
		classKey                          uintptr
		hotKey                            uint32
		icon, process                     uintptr
	}{mask: noCloseProcess, verb: verb, file: program, parameters: params, show: hidden}
	info.size = uint32(unsafe.Sizeof(info))
	if r, _, e := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		const cancelled = syscall.Errno(1223)
		if errors.Is(e, cancelled) {
			return ErrCancelled
		}
		return fmt.Errorf("ShellExecuteEx: %w", e)
	}
	if info.process != 0 {
		const forever = 0xffffffff
		waitForObject.Call(info.process, forever)
		closeHandle.Call(info.process)
	}
	return nil
}
