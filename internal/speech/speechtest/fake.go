// Package speechtest stands in for the speech servers in tests, so the speech engine is tested without the real
// programs, their models or a graphics card. A test binary becomes a server when Enabled: its TestMain calls Serve.
package speechtest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	envServe = "SPEECH_FAKE_SERVER"
	envLog   = "SPEECH_FAKE_LOG"
)

// Setter is the part of testing.T that Use needs.
type Setter interface{ Setenv(key, value string) }

// Use makes the test binary, started as the speech engine during t, act as the fake server, noting each start and
// request in the file log. breeze-server notes "start <model file>" and "speak <text>|<instruction>|<seed>|
// <cfg_scale>"; audiocpp_server notes "start <model file> <family>/<task>" and "speak <text>|<instruct>|<seed>|
// <language>".
func Use(t Setter, log string) {
	t.Setenv(envServe, "1")
	t.Setenv(envLog, log)
}

// Enabled reports whether this process was started as the fake server.
func Enabled() bool { return os.Getenv(envServe) == "1" }

// Serve answers as breeze-server does, or as audiocpp_server does when given --config: a second and a half of
// 16 kHz sound for each piece of speech. The model "broken.gguf" fails to load; the text "cut" is cut off after
// the first part and "crash" ends the server.
func Serve(args []string) {
	if len(args) >= 2 && args[0] == "--config" {
		serveAudioCPP(args[1])
		return
	}
	var model, port string
	for i, a := range args {
		switch {
		case i == 0:
			model = a
		case a == "--port" && i+1 < len(args):
			port = args[i+1]
		}
	}
	note("start " + filepath.Base(model))
	loadOrFail(model)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"ok","sample_rate":16000,"ws_port":0}`)
	})
	http.HandleFunc("/v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		text := r.FormValue("text")
		note(fmt.Sprintf("speak %s|%s|%s|%s", text, r.FormValue("instruction"), r.FormValue("seed"), r.FormValue("cfg_scale")))
		if text == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"text is required"}`)
			return
		}
		w.Header().Set("Content-Type", "audio/pcm")
		w.Header().Set("X-Sample-Rate", "16000")
		w.Header().Set("X-Sample-Format", "s16le")
		sound(w, text, nil)
	})
	listen(port)
}

func serveAudioCPP(path string) {
	data, err := os.ReadFile(path)
	var c struct {
		Port    int    `json:"port"`
		Backend string `json:"backend"`
		Models  []struct {
			ID, Family, Task, Path, Mode string
		} `json:"models"`
	}
	if err != nil || json.Unmarshal(data, &c) != nil || len(c.Models) != 1 || c.Backend == "" {
		fmt.Fprintln(os.Stderr, "error: bad config")
		os.Exit(1)
	}
	m := c.Models[0]
	note(fmt.Sprintf("start %s %s/%s", filepath.Base(m.Path), m.Family, m.Task))
	loadOrFail(m.Path)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"ok","backend":"vulkan","models":1}`)
	})
	http.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"loaded":true}]}`, m.ID)
	})
	http.HandleFunc("/v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model   string `json:"model"`
			Input   string `json:"input"`
			Seed    int    `json:"seed"`
			Options struct {
				Instruct string `json:"instruct"`
				Language string `json:"language"`
			} `json:"options"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		note(fmt.Sprintf("speak %s|%s|%d|%s", req.Input, req.Options.Instruct, req.Seed, req.Options.Language))
		if req.Model != m.ID || req.Input == "" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"unknown model id or empty input","type":"server_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		// A WAV file as audio.cpp writes it, with a chunk before the samples that a reader must skip.
		head := []byte("RIFF\x00\x00\x00\x00WAVEfmt \x10\x00\x00\x00")
		head = binary.LittleEndian.AppendUint16(head, 1)
		head = binary.LittleEndian.AppendUint16(head, 1)
		head = binary.LittleEndian.AppendUint32(head, 16000)
		head = binary.LittleEndian.AppendUint32(head, 32000)
		head = binary.LittleEndian.AppendUint16(head, 2)
		head = binary.LittleEndian.AppendUint16(head, 16)
		head = append(head, "LIST\x03\x00\x00\x00abc\x00data\x80\xbb\x00\x00"...)
		sound(w, req.Input, head)
	})
	listen(strconv.Itoa(c.Port))
}

func note(s string) {
	if f, err := os.OpenFile(os.Getenv(envLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.WriteString(s + "\n")
		f.Close()
	}
}

func loadOrFail(model string) {
	if filepath.Base(model) == "broken.gguf" {
		fmt.Fprintln(os.Stderr, "error: failed to load the model: bad magic")
		os.Exit(1)
	}
	time.Sleep(100 * time.Millisecond) // loading
}

// sound writes head, then half a second of samples three times and a dangling half sample.
func sound(w http.ResponseWriter, text string, head []byte) {
	if text == "crash" {
		os.Exit(3)
	}
	w.WriteHeader(http.StatusOK)
	w.Write(head)
	chunk := make([]byte, 16000) // half a second
	for i := range chunk {
		chunk[i] = byte(i)
	}
	for range 3 {
		w.Write(chunk)
		w.(http.Flusher).Flush()
		if text == "cut" {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
	}
	w.Write([]byte{7})
}

func listen(port string) {
	if err := http.ListenAndServe("127.0.0.1:"+port, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
