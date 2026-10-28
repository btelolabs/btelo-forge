package capture

import "testing"

func TestDisplaysHaveRealSizes(t *testing.T) {
	list, err := Displays()
	if err != nil {
		t.Skip(err)
	}
	primary := 0
	for _, d := range list {
		t.Logf("%+v", d)
		if d.Width <= 0 || d.Height <= 0 || d.Device == "" {
			t.Errorf("display %+v", d)
		}
		if d.Primary {
			primary++
		}
	}
	if primary != 1 {
		t.Errorf("%d primary displays", primary)
	}
	mics, err := Microphones()
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range mics {
		t.Logf("%+v", m)
		if m.Default && i != 0 {
			t.Errorf("the default microphone is listed at %d", i)
		}
	}
}
