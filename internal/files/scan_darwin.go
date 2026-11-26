//go:build darwin

package files

/*
#include <sys/attr.h>
#include <sys/vnode.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <string.h>
#include <stdint.h>

typedef struct {
	uint32_t nameOff;
	uint32_t nameLen;
	int64_t  size;
	int64_t  modSec;
	int64_t  modNsec;
	uint32_t objType; // VREG=1, VDIR=2, VLNK=5
} ForgeScanItem;

// forge_scan_bulk reads a batch of directory entries via Darwin's getattrlistbulk, avoiding per-file lstat
// syscalls and directory sorting. Returns:
//   >0: number of items written to items
//    0: end of directory
//   -1: I/O error
//   -2: unsupported filesystem (caller should fall back to ReadDir)
static int forge_scan_bulk(int fd, void *rawBuf, size_t rawCap,
	ForgeScanItem *items, int maxItems, char *namesBuf, size_t namesCap) {
	struct attrlist al;
	memset(&al, 0, sizeof(al));
	al.bitmapcount = ATTR_BIT_MAP_COUNT;
	al.commonattr = ATTR_CMN_RETURNED_ATTRS | ATTR_CMN_NAME | ATTR_CMN_OBJTYPE | ATTR_CMN_MODTIME;
	al.fileattr = ATTR_FILE_DATALENGTH;

	int count = getattrlistbulk(fd, &al, rawBuf, rawCap, 0);
	if (count < 0) {
		if (errno == ENOTSUP || errno == EINVAL) {
			return -2;
		}
		return -1;
	}
	if (count == 0) {
		return 0;
	}

	const char *p = (const char *)rawBuf;
	size_t namePos = 0;
	int out = 0;
	for (int i = 0; i < count && out < maxItems; i++) {
		uint32_t entryLen = 0;
		memcpy(&entryLen, p, sizeof(entryLen));
		if (entryLen < sizeof(uint32_t) + sizeof(attribute_set_t)) {
			break;
		}
		const char *cur = p + sizeof(uint32_t);
		attribute_set_t ret;
		memcpy(&ret, cur, sizeof(ret));
		cur += sizeof(ret);

		const char *namePtr = NULL;
		uint32_t nameLen = 0;
		if (ret.commonattr & ATTR_CMN_NAME) {
			attrreference_t ref;
			memcpy(&ref, cur, sizeof(ref));
			namePtr = cur + ref.attr_dataoffset;
			nameLen = ref.attr_length;
			// Strip trailing NUL included in attr_length.
			while (nameLen > 0 && namePtr[nameLen - 1] == '\0') {
				nameLen--;
			}
			cur += sizeof(ref);
		}
		uint32_t objType = 0;
		if (ret.commonattr & ATTR_CMN_OBJTYPE) {
			memcpy(&objType, cur, sizeof(objType));
			cur += sizeof(objType);
		}
		struct timespec mod = {0, 0};
		if (ret.commonattr & ATTR_CMN_MODTIME) {
			memcpy(&mod, cur, sizeof(mod));
			cur += sizeof(mod);
		}
		int64_t size = 0;
		if (ret.fileattr & ATTR_FILE_DATALENGTH) {
			off_t dl = 0;
			memcpy(&dl, cur, sizeof(dl));
			size = (int64_t)dl;
			cur += sizeof(dl);
		}

		if (namePtr != NULL && nameLen > 0 && namePos + nameLen <= namesCap) {
			memcpy(namesBuf + namePos, namePtr, nameLen);
			items[out].nameOff = (uint32_t)namePos;
			items[out].nameLen = nameLen;
			items[out].size = size;
			items[out].modSec = (int64_t)mod.tv_sec;
			items[out].modNsec = (int64_t)mod.tv_nsec;
			items[out].objType = objType;
			namePos += nameLen;
			out++;
		}
		p += entryLen;
	}
	return out;
}
*/
import "C"

import (
	"os"
	"sync"
	"time"
	"unsafe"
)

const (
	vtypeReg  = 1 // VREG
	vtypeDir  = 2 // VDIR
	vtypeLink = 5 // VLNK
)

type bulkBuffers struct {
	raw   []byte
	items []C.ForgeScanItem
	names []byte
}

var bulkPool = sync.Pool{
	New: func() any {
		return &bulkBuffers{
			raw:   make([]byte, 64<<10),
			items: make([]C.ForgeScanItem, 1024),
			names: make([]byte, 64<<10),
		}
	},
}

// skipScanDir avoids double-counting APFS firmlink mirrors (/System/Volumes/Data) and pseudo-fs (/dev)
// when scanning from "/".
func skipScanDir(scanRoot, parentPath, name string) bool {
	if scanRoot != "/" {
		return false
	}
	if parentPath == "/" && name == "dev" {
		return true
	}
	if parentPath == "/System/Volumes" && name == "Data" {
		return true
	}
	return false
}

// readDirForScan reads a directory's entries on macOS using Darwin's getattrlistbulk syscall so APFS returns names,
// object types, mod times, and logical file sizes in bulk with zero per-file lstat syscalls and zero sorting.
func readDirForScan(path string, fn func(name string, isDir, isLink bool, size int64, mod time.Time)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fd := C.int(f.Fd())
	bufs := bulkPool.Get().(*bulkBuffers)
	defer bulkPool.Put(bufs)

	for {
		n := int(C.forge_scan_bulk(
			fd,
			unsafe.Pointer(&bufs.raw[0]), C.size_t(len(bufs.raw)),
			&bufs.items[0], C.int(len(bufs.items)),
			(*C.char)(unsafe.Pointer(&bufs.names[0])), C.size_t(len(bufs.names)),
		))
		if n == 0 {
			return nil
		}
		if n == -2 {
			// Filesystem does not support getattrlistbulk; fall back to standard unsorted ReadDir.
			return readDirFallback(path, fn)
		}
		if n < 0 {
			return os.ErrPermission
		}
		for i := range n {
			it := bufs.items[i]
			name := string(bufs.names[it.nameOff : it.nameOff+it.nameLen])
			mod := time.Unix(int64(it.modSec), int64(it.modNsec))
			switch it.objType {
			case vtypeDir:
				fn(name, true, false, 0, mod)
			case vtypeLink:
				fn(name, false, true, 0, mod)
			case vtypeReg:
				fn(name, false, false, int64(it.size), mod)
			default:
				// FIFO/socket/char/block device: count as non-dir irregular item with 0 bytes.
				fn(name, false, true, 0, mod)
			}
		}
	}
}

func readDirFallback(path string, fn func(name string, isDir, isLink bool, size int64, mod time.Time)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	des, err := f.ReadDir(-1)
	for _, de := range des {
		info, ierr := de.Info()
		if ierr != nil {
			continue
		}
		mode := info.Mode()
		fn(de.Name(), realDir(mode), isLink(mode), info.Size(), info.ModTime())
	}
	return err
}
