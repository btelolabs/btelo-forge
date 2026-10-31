package matting

import (
	"os"
	"testing"
	"time"
)

func TestTheTensorIsNormalizedInPlanes(t *testing.T) {
	rgb := make([]byte, 3*Side*Side)
	rgb[0], rgb[1], rgb[2] = 255, 0, 128
	out := tensor(rgb)
	if len(out) != 3*Side*Side {
		t.Fatalf("%d values", len(out))
	}
	near := func(a, b float32) bool { return a-b < 1e-3 && b-a < 1e-3 }
	if !near(out[0], (1-0.485)/0.229) || !near(out[Side*Side], -0.456/0.224) || !near(out[2*Side*Side], (128.0/255-0.406)/0.225) {
		t.Errorf("%v %v %v", out[0], out[Side*Side], out[2*Side*Side])
	}
}

// With FORGE_ORT and FORGE_MATTING_MODEL set, a real runtime and model look at a bright square on a dark ground.
func TestRealModel(t *testing.T) {
	library, model := os.Getenv("FORGE_ORT"), os.Getenv("FORGE_MATTING_MODEL")
	if library == "" || model == "" {
		t.Skip("FORGE_ORT and FORGE_MATTING_MODEL are not set")
	}
	n, err := load(library, model)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	rgb := make([]byte, 3*Side*Side)
	for y := 300; y < 724; y++ {
		for x := 300; x < 724; x++ {
			rgb[3*(y*Side+x)], rgb[3*(y*Side+x)+1], rgb[3*(y*Side+x)+2] = 230, 60, 40
		}
	}
	start := time.Now()
	out, err := n.Run(rgb)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%v, on %T", time.Since(start), n)
	if in, far := out[512*Side+512], out[40*Side+40]; in < 200 || far > 50 {
		t.Errorf("the middle is %d, the corner %d", in, far)
	}
}
