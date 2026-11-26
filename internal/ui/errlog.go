package ui

import (
	"path/filepath"

	"github.com/btelolabs/btelo-forge/internal/errlog"
)

// errorLogPath is where runtime errors are persisted under the app's state directory.
func (a *App) errorLogPath() string {
	if a.logs != nil {
		return a.logs.Path()
	}
	if a.stateDir == "" {
		return ""
	}
	return filepath.Join(a.stateDir, "logs", "error.log")
}

// logError appends a structured entry to the persistent error log.
func (a *App) logError(scope, message string, err error) {
	if a.logs != nil {
		a.logs.Error(scope, message, err)
	}
}

// openErrorLog creates the error log file if needed and opens it in the system's default viewer (falling back to
// revealing it in Finder / Explorer).
func (a *App) openErrorLog() {
	path, err := a.ensureErrorLog()
	if err != nil {
		a.toast(a.u.tf("Could not open the error log: %v", err), true)
		return
	}
	if err := a.host.Launch(path); err != nil {
		if rerr := a.host.Reveal(path); rerr != nil {
			a.toast(a.u.tf("Could not open the error log: %v", err), true)
		}
	}
}

// revealErrorLog creates the error log file if needed and reveals it in Finder / Explorer.
func (a *App) revealErrorLog() {
	path, err := a.ensureErrorLog()
	if err != nil {
		a.toast(a.u.tf("Could not open the error log: %v", err), true)
		return
	}
	if err := a.host.Reveal(path); err != nil {
		a.toast(a.u.tf("Could not open the error log: %v", err), true)
	}
}

func (a *App) ensureErrorLog() (string, error) {
	if a.logs == nil && a.stateDir != "" {
		a.logs = errlog.ForPath(filepath.Join(a.stateDir, "logs", "error.log"))
	}
	return a.logs.EnsureFile()
}
