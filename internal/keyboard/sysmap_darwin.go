package keyboard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type hidMap struct{}

// NewSystemMap is the system's table of remapped keys: hidutil's, kept by a login item.
func NewSystemMap() SystemMap { return hidMap{} }

func (hidMap) Kind() MapKind { return HIDMap }

func hidAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", hidAgentLabel+".plist"), nil
}

// Read is the table of the login item, which is what the next sign-in applies; hidutil tells only what is in effect.
func (hidMap) Read() ([]Mapping, error) {
	path, err := hidAgentPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hidTable(string(data)), nil
}

// Write changes the keys at once and keeps them for the sign-ins to come. It needs no approval.
func (hidMap) Write(m []Mapping) error {
	argument, err := hidArgument(m)
	if err != nil {
		return err
	}
	if out, err := exec.Command("/usr/bin/hidutil", "property", "--set", argument).CombinedOutput(); err != nil {
		return fmt.Errorf("hidutil: %s", strings.TrimSpace(string(out)))
	}
	path, err := hidAgentPath()
	if err != nil {
		return err
	}
	if len(m) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hidAgent(argument)), 0o644)
}
