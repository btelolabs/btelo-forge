package xlsx

import (
	"testing"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

func TestParsePrintArea(t *testing.T) {
	cases := []struct {
		in   string
		want formula.Area
		ok   bool
	}{
		{"Sheet1!$A$1:$C$9", formula.Area{R1: 0, C1: 0, R2: 8, C2: 2}, true},
		{"'My sheet'!$B$2", formula.Area{R1: 1, C1: 1, R2: 1, C2: 1}, true},
		{"$1:$2", formula.Area{R1: 0, C1: 0, R2: 1, C2: MaxCols - 1}, true},
		{"$A:$B", formula.Area{R1: 0, C1: 0, R2: MaxRows - 1, C2: 1}, true},
		{"nonsense!", formula.Area{}, false},
		{"$C$3:$A$1", formula.Area{}, false},
	}
	for _, c := range cases {
		got, ok := parsePrintArea(c.in)
		if ok != c.ok || ok && got != c.want {
			t.Errorf("%q: %+v %v", c.in, got, ok)
		}
	}
}
