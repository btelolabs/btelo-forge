// Package matting is the engine that tells what in a picture is its subject: a server running a BiRefNet model through
// ONNX Runtime, on a graphics card when one can do it and otherwise on the processor. It is a mode of this program,
// started and stopped by ai.Runtime as a model engine like any, so a runtime that crashes takes only itself down.
package matting

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Side is the side of the square picture the model looks at.
const Side = 1024

// Network reads a picture and says how much of it is subject.
type network interface {
	// Run takes Side×Side pixels of red, green and blue and returns the same number of values, 0 to 255.
	Run(rgb []byte) ([]byte, error)
	Close()
}

// The model expects the colours ImageNet was normalized with.
var (
	mean = [3]float32{0.485, 0.456, 0.406}
	std  = [3]float32{0.229, 0.224, 0.225}
)

// tensor is rgb as the model takes it: three planes.
func tensor(rgb []byte) []float32 {
	out := make([]float32, 3*Side*Side)
	for i := range Side * Side {
		for c := range 3 {
			out[c*Side*Side+i] = (float32(rgb[3*i+c])/255 - mean[c]) / std[c]
		}
	}
	return out
}

// Serve answers on port until the program is ended: /health says 200 once the model has been loaded and run once, as
// the first run is by far the slowest; POST /matte takes Side×Side RGB bytes and returns Side×Side bytes.
func Serve(port int, library, model string) error {
	var ready atomic.Bool
	var loaded network
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "loading", http.StatusServiceUnavailable)
		}
	})
	mux.HandleFunc("POST /matte", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		rgb, err := io.ReadAll(io.LimitReader(r.Body, 3*Side*Side+1))
		if err != nil || len(rgb) != 3*Side*Side {
			http.Error(w, "the picture must be 1024×1024 RGB bytes", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		out, err := loaded.Run(rgb)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(out)
	})
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	go func() {
		n, err := load(library, model)
		if err == nil {
			_, err = n.Run(make([]byte, 3*Side*Side))
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		loaded = n
		ready.Store(true)
	}()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	defer srv.Shutdown(context.Background())
	return srv.Serve(listener)
}
