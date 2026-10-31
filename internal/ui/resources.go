package ui

import (
	"os"
	"path/filepath"
)

// The kinds of work: the groups of the start page, and the folders of the resource folder that what they make is
// saved in.
const (
	categoryTools = iota
	categoryCreative
	categoryDocuments
	categoryIntelligence
)

// categoryFolders name those folders, in English in every language.
var categoryFolders = [...]string{"Tools", "Creative", "Documents", "Intelligence"}

// resourceDir is the folder the user's work is kept in: the one they chose, or else a folder of the app's own in their
// home folder, which unlike Documents is not synced by OneDrive, as hours of recordings should not be.
func (a *App) resourceDir() (string, error) {
	if a.settings.ResourceDir != "" {
		return a.settings.ResourceDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ProductName), nil
}

// categoryDir is the folder of the resource folder for a kind of work. It may not exist yet: what writes there makes it.
func (a *App) categoryDir(category int) (string, error) {
	root, err := a.resourceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, categoryFolders[category]), nil
}

// ensureResourceFolders makes the resource folder with its folders for each kind of work, and returns it.
func (a *App) ensureResourceFolders() (string, error) {
	root, err := a.resourceDir()
	if err != nil {
		return "", err
	}
	for _, name := range categoryFolders {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			return "", err
		}
	}
	return root, nil
}

// chooseResourceDir asks for the resource folder.
func (a *App) chooseResourceDir() {
	a.pickDir(pickRequest{title: a.u.t("Resource folder"), dir: a.settings.ResourceDir}, a.setResourceDir)
}

// setResourceDir moves the resource folder, "" being the default one; what is saved already stays where it is. The
// next resource manager opens at the new folder.
func (a *App) setResourceDir(dir string) {
	a.settings.ResourceDir = dir
	a.settings.Files.ResourcesOpened = false
	a.saveSettings()
	if _, err := a.ensureResourceFolders(); err != nil {
		a.toast(a.u.tf("The resource folder could not be made: %s", a.describe(err)), true)
	}
}

// exportDir is the folder videos are exported to, "" for beside each original: the folder the user chose, or else
// the resource folder's.
func (a *App) exportDir() string {
	if a.settings.ExportBeside {
		return ""
	}
	if a.settings.ExportDir != "" {
		return a.settings.ExportDir
	}
	dir, err := a.categoryDir(categoryCreative)
	if err != nil {
		return ""
	}
	return dir
}
