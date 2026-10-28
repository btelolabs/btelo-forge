//go:build !windows

package vault

import "errors"

// Elsewhere secrets are kept as they are, in files only the user can read.
func protect([]byte) ([]byte, bool, error) { return nil, false, nil }

func unprotect([]byte) ([]byte, error) { return nil, errors.New("sealed secrets are read on Windows") }
