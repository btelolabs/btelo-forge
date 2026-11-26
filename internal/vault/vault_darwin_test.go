//go:build darwin

package vault

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestKeychainPersistenceAndTamper uses an isolated test keychain service so it exercises real Keychain storage
// without mutating the user's production key, and verifies round-trip across cache reset and tamper rejection.
func TestKeychainPersistenceAndTamper(t *testing.T) {
	origService := keychainService
	testService := fmt.Sprintf("com.btelolabs.forge.vault.test.%d", os.Getpid())
	keyMu.Lock()
	origCached := cachedKey
	keychainService = testService
	cachedKey = nil
	keyMu.Unlock()
	t.Cleanup(func() {
		_ = keychainDelete(testService, keychainAccount)
		keyMu.Lock()
		keychainService = origService
		cachedKey = origCached
		keyMu.Unlock()
	})

	secret := "rtmp-secret-key-测试-12345"
	sealed, err := Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, prefix) || strings.Contains(sealed, secret) {
		t.Fatalf("expected sealed secret, got %q", sealed)
	}

	// Clear the in-memory cache so Open must read the master key back from the macOS Keychain.
	keyMu.Lock()
	cachedKey = nil
	keyMu.Unlock()

	got, err := Open(sealed)
	if err != nil || got != secret {
		t.Fatalf("Open after cache clear = %q, %v; want %q", got, err, secret)
	}

	// Non-deterministic encryption (fresh random nonce each time).
	sealed2, err := Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if sealed2 == sealed {
		t.Fatal("Seal produced identical ciphertext for two calls")
	}

	// Flipping a byte in the sealed payload must fail authentication with ErrCannotOpen.
	b := []byte(sealed)
	if b[len(b)-2] == 'A' {
		b[len(b)-2] = 'B'
	} else {
		b[len(b)-2] = 'A'
	}
	if _, err := Open(string(b)); err != ErrCannotOpen {
		t.Fatalf("tampered secret returned %v, want ErrCannotOpen", err)
	}
}

// TestIsolatedHomeNeverBlocks verifies that running with an isolated temporary HOME (as UI tests do)
// never hangs on a modal macOS Keychain prompt.
func TestIsolatedHomeNeverBlocks(t *testing.T) {
	origService := keychainService
	keyMu.Lock()
	origCached := cachedKey
	keychainService = fmt.Sprintf("com.btelolabs.forge.vault.isolated.%d", os.Getpid())
	cachedKey = nil
	keyMu.Unlock()
	t.Cleanup(func() {
		_ = keychainDelete(keychainService, keychainAccount)
		keyMu.Lock()
		keychainService = origService
		cachedKey = origCached
		keyMu.Unlock()
	})
	t.Setenv("HOME", t.TempDir())
	sealed, err := Seal("test-secret")
	if err != nil {
		t.Fatalf("Seal under isolated HOME failed: %v", err)
	}
	if !strings.HasPrefix(sealed, prefix) || strings.Contains(sealed, "test-secret") {
		t.Fatalf("expected sealed secret under isolated HOME, got %q", sealed)
	}
	got, err := Open(sealed)
	if err != nil || got != "test-secret" {
		t.Fatalf("Open under isolated HOME = %q, %v", got, err)
	}
}
