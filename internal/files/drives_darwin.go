//go:build darwin

package files

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// Drives lists the browsable mounted volumes on macOS: the root volume (e.g. Macintosh HD at "/") and mounted
// external disks, disk images, and network shares under /Volumes, with their sizes and available space.
func Drives() []Drive {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil || n <= 0 {
		return nil
	}
	stats := make([]unix.Statfs_t, n+8)
	n, err = unix.Getfsstat(stats, unix.MNT_NOWAIT)
	if err != nil || n <= 0 {
		return nil
	}

	rootName := rootVolumeName()
	seen := map[string]bool{}
	var out []Drive
	for _, st := range stats[:n] {
		mnt := unix.ByteSliceToString(st.Mntonname[:])
		if mnt == "" {
			continue
		}
		mnt = filepath.Clean(mnt)
		if seen[mnt] {
			continue
		}
		fstype := unix.ByteSliceToString(st.Fstypename[:])
		if mnt != "/" {
			if st.Flags&unix.MNT_DONTBROWSE != 0 {
				continue
			}
			if fstype == "devfs" || fstype == "autofs" || fstype == "nullfs" || fstype == "fdesc" {
				continue
			}
			if !strings.HasPrefix(mnt, "/Volumes/") {
				continue
			}
			base := filepath.Base(mnt)
			if base == "" || strings.HasPrefix(base, ".") {
				continue
			}
		}
		seen[mnt] = true

		name := filepath.Base(mnt)
		if mnt == "/" {
			name = rootName
		}
		bsize := int64(st.Bsize)
		total := int64(st.Blocks) * bsize
		free := int64(st.Bavail) * bsize
		if free > total {
			free = total
		}
		out = append(out, Drive{
			Path:  mnt,
			Name:  name,
			Label: friendlyFSType(fstype),
			Total: total,
			Free:  free,
		})
	}

	slices.SortFunc(out, func(a, b Drive) int {
		if a.Path == "/" && b.Path != "/" {
			return -1
		}
		if b.Path == "/" && a.Path != "/" {
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// rootVolumeName finds the Finder display name of "/" by locating the symlink in /Volumes pointing to "/".
func rootVolumeName() string {
	des, err := os.ReadDir("/Volumes")
	if err == nil {
		for _, de := range des {
			if de.Type()&os.ModeSymlink == 0 {
				continue
			}
			if target, err := os.Readlink(filepath.Join("/Volumes", de.Name())); err == nil && filepath.Clean(target) == "/" {
				return de.Name()
			}
		}
	}
	return "Macintosh HD"
}

func friendlyFSType(fs string) string {
	switch strings.ToLower(fs) {
	case "apfs":
		return "APFS"
	case "hfs":
		return "Mac OS Extended"
	case "exfat":
		return "ExFAT"
	case "msdos":
		return "FAT32"
	case "ntfs":
		return "NTFS"
	case "smbfs":
		return "SMB"
	case "nfs":
		return "NFS"
	case "afpfs":
		return "AFP"
	default:
		return strings.ToUpper(fs)
	}
}
