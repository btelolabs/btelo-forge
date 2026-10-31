//go:build !windows

package proc

import (
	"testing"
	"time"
)

func TestParseElapsedReadsPsTimes(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"00:05": 5 * time.Second, "12:34": 12*time.Minute + 34*time.Second,
		"01:02:03": time.Hour + 2*time.Minute + 3*time.Second, "2-03:04:05": 48*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second,
	} {
		if got, ok := parseElapsed(in); !ok || got != want {
			t.Errorf("%q: %v %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "5", "a:b", "1:2:3:4", "x-01:02"} {
		if _, ok := parseElapsed(bad); ok {
			t.Errorf("%q was read", bad)
		}
	}
}
