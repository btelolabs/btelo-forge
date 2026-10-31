package win

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	syswin "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Explorer's thumbnails of the app's own files come from a small DLL (packaging/windows/thumbnailer), embedded in
// the program and unpacked on registering. Explorer's process for it keeps the file open, so an updated DLL is a
// file of another name, not the same file written again.
//
// COM ignores classes declared per user in a process running elevated, and a thumbnail is asked for by Explorer's
// own level: where Explorer runs elevated, as under the built-in Administrator, the class is declared for the machine
// too, which only an elevated program can do and only such an Explorer needs.

const (
	// thumbnailerCLSID names the DLL's object; keep it in step with thumbnailer.c.
	thumbnailerCLSID = "{6F2B7C1E-93A4-4D58-B1E6-2C7D40A9F315}"
	// thumbnailShellEx is the interface Explorer looks up under an extension's ShellEx key: IThumbnailProvider.
	thumbnailShellEx = "{e357fccd-a995-4576-b01f-234630154e96}"
)

// ThumbnailerPath is where the DLL of this program lives in dir.
func ThumbnailerPath(dir string) string {
	sum := sha256.Sum256(thumbnailerDLL)
	return filepath.Join(dir, "thumbnailer-"+hex.EncodeToString(sum[:6])+".dll")
}

// InstallThumbnailer unpacks the DLL into dir, unless it is there, and removes those of earlier versions that no
// process holds any more.
func InstallThumbnailer(dir string) error {
	path := ThumbnailerPath(dir)
	if st, err := os.Stat(path); err != nil || st.Size() != int64(len(thumbnailerDLL)) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, thumbnailerDLL, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	old, err := filepath.Glob(filepath.Join(dir, "thumbnailer-*.dll"))
	if err != nil {
		return err
	}
	for _, p := range old {
		if p != path {
			os.Remove(p) // one still loaded goes on a later install
		}
	}
	return nil
}

func (r AssocRoot) thumbnailerKey() string { return r.classes() + `\CLSID\` + thumbnailerCLSID }

func (r AssocRoot) thumbnailShell(ext string) string {
	return r.classes() + `\` + ext + `\ShellEx\` + thumbnailShellEx
}

// registerThumbnails makes the DLL the source of the association's extensions' thumbnails.
func registerThumbnails(r AssocRoot, a Association) error {
	if r.Thumbnailer == "" {
		return nil
	}
	key := r.thumbnailerKey()
	for _, kv := range [][3]string{
		{key, "", r.Name + " thumbnails"},
		{key + `\InprocServer32`, "", r.Thumbnailer},
		{key + `\InprocServer32`, "ThreadingModel", "Apartment"},
	} {
		if err := setString(kv[0], kv[1], kv[2]); err != nil {
			return err
		}
		if elevated() {
			if err := setStringIn(registry.LOCAL_MACHINE, kv[0], kv[1], kv[2]); err != nil {
				return err
			}
		}
	}
	// Explorer marks a thumbnail with the program's icon unless told otherwise; the picture is the file's own.
	if err := setString(r.classes()+`\`+a.ProgID, "TypeOverlay", ""); err != nil {
		return err
	}
	for _, ext := range a.Exts {
		if err := setString(r.thumbnailShell(ext), "", thumbnailerCLSID); err != nil {
			return fmt.Errorf("thumbnails of %s: %w", ext, err)
		}
	}
	return nil
}

// unregisterThumbnails takes back what registerThumbnails wrote for the association.
func unregisterThumbnails(r AssocRoot, a Association) error {
	for _, ext := range a.Exts {
		if err := deleteTree(r.classes() + `\` + ext + `\ShellEx`); err != nil {
			return err
		}
	}
	if elevated() {
		if err := deleteTreeIn(registry.LOCAL_MACHINE, r.thumbnailerKey()); err != nil {
			return err
		}
	}
	return deleteTree(r.thumbnailerKey())
}

// elevated reports whether the program runs with an administrator's rights.
func elevated() bool { return syswin.GetCurrentProcessToken().IsElevated() }

// thumbnailsRegistered reports whether Explorer is pointed at this program's DLL, and the DLL is there.
func thumbnailsRegistered(r AssocRoot) bool {
	if r.Thumbnailer == "" {
		return true
	}
	if _, err := os.Stat(r.Thumbnailer); errors.Is(err, os.ErrNotExist) {
		return false
	}
	if elevated() && !strings.EqualFold(getStringIn(registry.LOCAL_MACHINE, r.thumbnailerKey()+`\InprocServer32`, ""), r.Thumbnailer) {
		return false
	}
	return strings.EqualFold(getString(r.thumbnailerKey()+`\InprocServer32`, ""), r.Thumbnailer)
}
