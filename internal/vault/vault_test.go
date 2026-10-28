package vault

import (
	"runtime"
	"strings"
	"testing"
)

func TestSealAndOpen(t *testing.T) {
	for _, secret := range []string{"live_1234567890abcdef", "?streamname=live_1_2&key=秘密&schedule=rtmp", "x"} {
		sealed, err := Seal(secret)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" && (!strings.HasPrefix(sealed, prefix) || len(secret) > 4 && strings.Contains(sealed, secret)) {
			t.Errorf("%q is written as %q", secret, sealed)
		}
		if got, err := Open(sealed); err != nil || got != secret {
			t.Errorf("opened %q, %v; want %q", got, err, secret)
		}
	}
	if sealed, _ := Seal(""); sealed != "" {
		t.Errorf("an empty secret is written as %q", sealed)
	}
	// A secret written before sealing reads as it is; one sealed elsewhere does not read.
	if got, _ := Open("plain-key"); got != "plain-key" {
		t.Errorf("a plain secret reads as %q", got)
	}
	if _, err := Open(prefix + "bm90IHNlYWxlZA=="); err != ErrCannotOpen {
		t.Errorf("a foreign secret: %v", err)
	}
}
