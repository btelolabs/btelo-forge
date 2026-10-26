package ui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/project"
)

// ProductName is the name of the app in every language.
const ProductName = "Btelo Video Cut"

// Repository is where the source lives.
const Repository = "https://github.com/btelolabs/btelo-video-cut"

// settings are the preferences kept between runs.
type settings struct {
	Language    Language       `json:"language"`
	Export      project.Export `json:"export"`
	ExportDir   string         `json:"exportDir"`
	Exported    []exportRecord `json:"exported"`
	MCP         bool           `json:"mcp"`
	Phone       bool           `json:"phone"`
	PhoneKey    string         `json:"phoneKey"`
	PanelHidden bool           `json:"panelHidden"`

	SubtitleModel    string `json:"subtitleModel"`
	SubtitleLanguage string `json:"subtitleLanguage"`
	SubtitleEngine   string `json:"subtitleEngineId"`
}

type exportRecord struct {
	Path string    `json:"path"`
	At   time.Time `json:"at"`
}

const maxExportRecords = 200

func (a *App) settingsPath() string { return filepath.Join(a.stateDir, "settings.json") }

func defaultSettings() settings {
	return settings{Language: English, Export: project.Export{Quality: project.Balanced}, MCP: true}
}

func (a *App) loadSettings() {
	a.settings = defaultSettings()
	data, err := os.ReadFile(a.settingsPath())
	if errors.Is(err, os.ErrNotExist) {
		a.settings.Language = systemLanguage(a.host.Languages())
		return
	}
	s := defaultSettings()
	if err == nil {
		err = json.Unmarshal(data, &s)
	}
	if err != nil {
		a.toast(a.u.tf("Settings could not be read: %v", err), true)
		return
	}
	if !slices.Contains(languages, s.Language) {
		s.Language = English
	}
	a.settings = s
}

func (a *App) saveSettings() {
	data, err := json.MarshalIndent(a.settings, "", "  ")
	if err == nil {
		if err = os.MkdirAll(a.stateDir, 0o755); err == nil {
			tmp := a.settingsPath() + ".tmp"
			if err = os.WriteFile(tmp, data, 0o600); err == nil {
				err = os.Rename(tmp, a.settingsPath())
			}
		}
	}
	if err != nil {
		a.toast(a.u.tf("Settings could not be saved: %v", err), true)
	}
}

// rememberExport records an exported file, newest first, for the phone page and the MCP tools.
func (a *App) rememberExport(path string) {
	list := slices.DeleteFunc(a.settings.Exported, func(r exportRecord) bool { return r.Path == path })
	a.settings.Exported = append([]exportRecord{{Path: path, At: time.Now()}}, list...)
	if len(a.settings.Exported) > maxExportRecords {
		a.settings.Exported = a.settings.Exported[:maxExportRecords]
	}
	a.saveSettings()
}

// exports lists the recorded exports whose files still exist.
func (a *App) exports() []exportRecord {
	var out []exportRecord
	for _, r := range a.settings.Exported {
		if _, err := os.Stat(r.Path); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// setLanguage switches the interface and remembers the choice.
func (a *App) setLanguage(l Language) {
	a.settings.Language = l
	a.u.lang = l
	a.host.SetTitle(a.Title())
	a.saveSettings()
}

// showPanel opens the side panel on a page, for actions whose result is edited there.
func (a *App) showPanel(tab int) {
	a.tab = tab
	a.setPanelHidden(false)
}

// setPanelHidden collapses or expands the side panel and remembers it.
func (a *App) setPanelHidden(hidden bool) {
	if a.settings.PanelHidden != hidden {
		a.settings.PanelHidden = hidden
		a.saveSettings()
	}
}

// Title is the window title in the current language.
func (a *App) Title() string { return ProductName }

func newKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

// chooseExportDir asks for the default export folder.
func (a *App) chooseExportDir() {
	if dir := a.host.ChooseFolder(a.u.t("Default export folder")); dir != "" {
		a.settings.ExportDir = dir
		a.saveSettings()
	}
}
