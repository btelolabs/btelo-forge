package vault

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	crypt32              = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtect     = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect   = crypt32.NewProc("CryptUnprotectData")
	procLocalFree        = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
	errEmpty             = errors.New("nothing to protect")
	cryptprotectUIForbid = uintptr(0x1)
)

type blob struct {
	size uint32
	data *byte
}

func newBlob(b []byte) *blob {
	if len(b) == 0 {
		return &blob{}
	}
	return &blob{size: uint32(len(b)), data: &b[0]}
}

// bytes copies the blob out of the memory Windows allocated, and frees that.
func (b *blob) bytes() []byte {
	out := make([]byte, b.size)
	copy(out, unsafe.Slice(b.data, b.size))
	procLocalFree.Call(uintptr(unsafe.Pointer(b.data)))
	return out
}

func protect(plain []byte) ([]byte, bool, error) {
	if len(plain) == 0 {
		return nil, false, errEmpty
	}
	var out blob
	r, _, err := procCryptProtect.Call(uintptr(unsafe.Pointer(newBlob(plain))), 0, 0, 0, 0, cryptprotectUIForbid, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, false, err
	}
	return out.bytes(), true, nil
}

func unprotect(sealed []byte) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, errEmpty
	}
	var out blob
	r, _, err := procCryptUnprotect.Call(uintptr(unsafe.Pointer(newBlob(sealed))), 0, 0, 0, 0, cryptprotectUIForbid, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	return out.bytes(), nil
}
