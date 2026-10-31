package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/vault"
)

// Keys keeps the API keys of the online services in a file of their own, sealed for the signed-in user as stream
// keys are. They are kept apart from the list of models so that sharing or editing that list never shares a key.
type Keys struct {
	path   string
	mu     sync.Mutex
	sealed map[string]string
}

// OpenKeys reads the keys kept at path; there are none before the first is saved.
func OpenKeys(path string) (*Keys, error) {
	k := &Keys{path: path, sealed: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return k, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &k.sealed)
	}
	if err != nil {
		return &Keys{path: path, sealed: map[string]string{}}, fmt.Errorf("the saved API keys could not be read: %w", err)
	}
	return k, nil
}

// Has reports whether a key is saved for the service.
func (k *Keys) Has(provider string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sealed[provider] != ""
}

// Get is the service's key, or "" when there is none. A key saved by another user or on another computer cannot be
// read and gives vault.ErrCannotOpen.
func (k *Keys) Get(provider string) (string, error) {
	k.mu.Lock()
	sealed := k.sealed[provider]
	k.mu.Unlock()
	if sealed == "" {
		return "", nil
	}
	return vault.Open(sealed)
}

// Set saves the service's key; an empty key forgets it.
func (k *Keys) Set(provider, key string) error {
	key = strings.TrimSpace(key)
	sealed, err := vault.Seal(key)
	if err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	next := make(map[string]string, len(k.sealed)+1)
	for p, s := range k.sealed {
		next[p] = s
	}
	if sealed == "" {
		delete(next, provider)
	} else {
		next[provider] = sealed
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(k.path), 0o755); err != nil {
		return err
	}
	tmp := k.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, k.path); err != nil {
		return err
	}
	k.sealed = next
	return nil
}
