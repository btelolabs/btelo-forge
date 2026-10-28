// Package vault keeps secrets, such as stream keys, in files without writing them as they are: on Windows they are
// encrypted for the signed-in user with the Data Protection API, so another account, or a copy of the file on
// another computer, cannot read them.
package vault

import (
	"encoding/base64"
	"errors"
	"strings"
)

// prefix marks a sealed secret, so that a secret written before sealing existed, or on a system without it, still
// reads.
const prefix = "sealed:"

// ErrCannotOpen means a sealed secret cannot be read here: it was sealed by another user or on another computer.
var ErrCannotOpen = errors.New("the secret was saved by another user or on another computer")

// Seal returns the secret in a form to write to a file; an empty secret stays empty.
func Seal(secret string) (string, error) {
	if secret == "" {
		return "", nil
	}
	data, ok, err := protect([]byte(secret))
	if err != nil {
		return "", err
	}
	if !ok {
		return secret, nil
	}
	return prefix + base64.StdEncoding.EncodeToString(data), nil
}

// Open reads what Seal wrote.
func Open(sealed string) (string, error) {
	rest, ok := strings.CutPrefix(sealed, prefix)
	if !ok {
		return sealed, nil
	}
	data, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return "", ErrCannotOpen
	}
	plain, err := unprotect(data)
	if err != nil {
		return "", ErrCannotOpen
	}
	return string(plain), nil
}
