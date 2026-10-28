package files

import "golang.org/x/sys/windows"

// Desktop is the user's desktop folder, wherever Windows keeps it (OneDrive may have moved it), or "" when Windows
// does not say.
func Desktop() string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if err != nil {
		return ""
	}
	return dir
}
