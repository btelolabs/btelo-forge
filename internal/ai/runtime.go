package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/proc"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// State is how far a model is from answering.
type State int

// The states of a model on this computer.
const (
	Unloaded    State = iota // not in memory
	Downloading              // its engine or files are being fetched
	Loading                  // the engine is started and reads the model into memory
	Ready                    // it answers
	Failed                   // it could not be loaded, or stopped; Status.Err says why
)

// selfProgram is this program, which is the matting engine.
var selfProgram = os.Executable

// loadLimit is how long reading a model into memory may take before it is given up.
const loadLimit = 10 * time.Minute

// sampleEvery is how often what the models take of the computer is measured.
const sampleEvery = 1500 * time.Millisecond

// ErrClosed means the runtime has been closed.
var ErrClosed = errors.New("the AI models were closed")

// Status is what is known of a model that is, or has been asked to be, in memory.
type Status struct {
	Model    string // the model's id
	State    State
	Stage    string  // while Downloading: "engine", "model" or "projector"
	Fraction float64 // how much of that stage is done, from 0 to 1
	Err      error   // why it is Failed
	Engine   Engine
	Pid      int
	Since    time.Time // when it became Ready
	Usage    proc.Usage
	Measured bool // Usage has been measured
}

// Runtime keeps the models that run on this computer: it downloads what they need, starts each in an engine of its
// own, measures what they take of the computer and stops them when asked. Anything that needs a model asks it here, so
// a model loaded for one use serves the others.
type Runtime struct {
	mgr     *transcribe.Manager
	meter   *proc.Meter
	client  *http.Client
	changed func()

	mu      sync.Mutex
	models  map[string]*instance
	tracked map[string]*tracked // programs of other users of models, measured with these
	leaving []*instance         // stopped, and perhaps still giving back their memory
	next    int
	closed  bool
	done    chan struct{}
}

// tracked is a program some other part of the app runs, such as the voice-over engine, that is listed and measured
// like the models here.
type tracked struct {
	pid      func() int
	usage    proc.Usage
	measured bool
}

type instance struct {
	id     string
	seq    int
	model  Model
	engine Engine

	cancel context.CancelFunc
	gone   chan struct{} // closed once nothing of it runs any more
	ready  chan struct{} // closed once it is Ready, has Failed or has been unloaded
	settle sync.Once

	// guarded by Runtime.mu
	state    State
	stage    string
	fraction float64
	err      error
	svc      *proc.Service
	since    time.Time
	usage    proc.Usage
	measured bool
}

// NewRuntime downloads engines and models under dir. changed is called, from any goroutine, when a status changes.
func NewRuntime(dir string, changed func()) *Runtime {
	if changed == nil {
		changed = func() {}
	}
	r := &Runtime{mgr: transcribe.NewManager(dir), meter: proc.NewMeter(), client: &http.Client{}, changed: changed,
		models: map[string]*instance{}, tracked: map[string]*tracked{}, done: make(chan struct{})}
	go r.measure()
	return r
}

// Track has the runtime measure a program that something else runs: pid says its process id, or 0 when it is not
// running. Usage then tells what it takes.
func (r *Runtime) Track(key string, pid func() int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tracked[key] = &tracked{pid: pid}
}

// Usage is what the tracked program takes of the computer, and false while it is not running or not yet measured.
func (r *Runtime) Usage(key string) (proc.Usage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.tracked[key]
	if t == nil || !t.measured {
		return proc.Usage{}, false
	}
	return t.usage, true
}

// Manager is where the runtime keeps what it downloads.
func (r *Runtime) Manager() *transcribe.Manager { return r.mgr }

// Files are the model's files on this computer, with their total size, and whether the model can be loaded without
// downloading the model itself.
func (r *Runtime) Files(m Model) (size int64, installed bool) {
	path, ok := r.mgr.ModelPath(m.Model)
	if !ok {
		return 0, false
	}
	size = fileSize(path)
	if m.Projector != nil {
		p, ok := r.mgr.ModelPath(projectorModel(m))
		if !ok {
			return size, false
		}
		size += fileSize(p)
	}
	for _, part := range m.Parts {
		p, ok := r.mgr.ModelPath(partModel(m, part))
		if !ok {
			return size, false
		}
		size += fileSize(p)
	}
	return size, true
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// projectorModel is the projector file as a downloadable model of its own.
func projectorModel(m Model) transcribe.Model {
	return transcribe.Model{ID: m.ID + "-projector", Name: m.Name, URL: m.Projector.URL, Size: m.Projector.Size, SHA256: m.Projector.SHA256}
}

// partModel is one of the model's other files as a downloadable model of its own.
func partModel(m Model, p Part) transcribe.Model {
	return transcribe.Model{ID: m.ID + "-" + p.Role, Name: m.Name, URL: p.URL, Size: p.Size, SHA256: p.SHA256}
}

// Delete removes the model's files from this computer. A model in memory is not deleted.
func (r *Runtime) Delete(m Model) error {
	r.mu.Lock()
	in := r.models[m.ID]
	busy := in != nil && in.state != Failed
	r.mu.Unlock()
	if busy {
		return errors.New("unload the model before deleting its files")
	}
	path, _ := r.mgr.ModelPath(m.Model)
	paths := []string{path}
	if m.Projector != nil {
		p, _ := r.mgr.ModelPath(projectorModel(m))
		paths = append(paths, p)
	}
	for _, part := range m.Parts {
		p, _ := r.mgr.ModelPath(partModel(m, part))
		paths = append(paths, p)
	}
	for _, p := range paths {
		for _, f := range []string{p, p + ".part"} {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		os.Remove(filepath.Dir(p)) // the folder of the file, when nothing else is in it
	}
	return nil
}

// Load starts bringing a model into memory in the background, downloading its engine and files first if they are
// missing; Statuses and Status follow its progress. With exclusive, every other model is unloaded first, which is
// how a user switches models on a computer with memory for one. Loading a model that is loading or loaded does
// nothing; one that failed is tried again.
func (r *Runtime) Load(cfg *Config, modelID, engineID string, exclusive bool) error {
	m, ok := cfg.Model(modelID)
	if !ok {
		return fmt.Errorf("unknown model %q", modelID)
	}
	e, ok := cfg.Engine(m, engineID)
	if !ok {
		return fmt.Errorf("no engine for %s runs on this computer", m.Name.In("en"))
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if in := r.models[modelID]; in != nil && in.state != Failed {
		r.mu.Unlock()
		return nil
	}
	var stopping []*instance
	if old := r.models[modelID]; old != nil {
		delete(r.models, modelID)
		stopping = append(stopping, old)
	}
	if exclusive {
		for id, other := range r.models {
			delete(r.models, id)
			stopping = append(stopping, other)
		}
	}
	for _, s := range stopping {
		r.leaving = append(r.leaving, s)
	}
	wait := slices.Clone(r.leaving)
	ctx, cancel := context.WithCancel(context.Background())
	r.next++
	in := &instance{id: modelID, seq: r.next, model: m, engine: e, cancel: cancel, gone: make(chan struct{}), ready: make(chan struct{}), state: Downloading}
	r.models[modelID] = in
	r.mu.Unlock()
	for _, s := range stopping {
		r.halt(s)
	}
	go r.run(ctx, in, wait)
	r.changed()
	return nil
}

// halt stops an instance that has been taken out of the list, and forgets it once nothing of it runs.
func (r *Runtime) halt(in *instance) {
	in.cancel()
	go func() {
		r.mu.Lock()
		svc := in.svc
		r.mu.Unlock()
		svc.Stop()
		// run closes gone when it returns, which is after the server it started has ended.
		<-in.gone
		r.mu.Lock()
		r.leaving = slices.DeleteFunc(r.leaving, func(o *instance) bool { return o == in })
		r.mu.Unlock()
		r.changed()
	}()
}

// Unload takes a model out of memory, or stops it loading, and gives its memory back.
func (r *Runtime) Unload(modelID string) {
	r.mu.Lock()
	in := r.models[modelID]
	if in == nil {
		r.mu.Unlock()
		return
	}
	delete(r.models, modelID)
	r.leaving = append(r.leaving, in)
	r.mu.Unlock()
	r.halt(in)
	r.changed()
}

// UnloadAll takes every model out of memory.
func (r *Runtime) UnloadAll() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.models))
	for id := range r.models {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.Unload(id)
	}
}

// Close unloads every model, waits for them to end and stops measuring. Later loads fail.
func (r *Runtime) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	close(r.done)
	all := make([]*instance, 0, len(r.models))
	for id, in := range r.models {
		delete(r.models, id)
		all = append(all, in)
	}
	r.leaving = append(r.leaving, all...)
	wait := slices.Clone(r.leaving)
	r.mu.Unlock()
	for _, in := range all {
		r.halt(in)
	}
	for _, in := range wait {
		<-in.gone
	}
	r.meter.Close()
}

// run brings a model into memory: it waits for the models it replaces to give their memory back, fetches what is
// missing, starts the engine and waits until the model answers.
func (r *Runtime) run(ctx context.Context, in *instance, wait []*instance) {
	defer close(in.gone)
	defer r.changed()
	defer in.settled()
	for _, o := range wait {
		select {
		case <-o.gone:
		case <-ctx.Done():
			return
		}
	}
	fail := func(err error) {
		if ctx.Err() != nil {
			return // unloaded meanwhile; there is nobody to tell
		}
		r.mu.Lock()
		in.state, in.err = Failed, err
		r.mu.Unlock()
	}
	progress := func(stage string) transcribe.Progress {
		return func(s string, f float64) {
			if s == "model" {
				s = stage
			}
			r.mu.Lock()
			in.state, in.stage, in.fraction = Downloading, s, f
			r.mu.Unlock()
			r.changed()
		}
	}
	program, modelPath, err := r.mgr.Prepare(ctx, in.engine.Engine, in.model.Model, progress("model"))
	if err != nil {
		fail(err)
		return
	}
	var projector string
	if in.model.Projector != nil {
		if _, projector, err = r.mgr.Prepare(ctx, in.engine.Engine, projectorModel(in.model), progress("projector")); err != nil {
			fail(err)
			return
		}
	}
	parts := map[string]string{}
	for _, part := range in.model.Parts {
		if _, parts[part.Role], err = r.mgr.Prepare(ctx, in.engine.Engine, partModel(in.model, part), progress(part.Role)); err != nil {
			fail(err)
			return
		}
	}
	r.mu.Lock()
	in.state, in.stage, in.fraction = Loading, "", 0
	r.mu.Unlock()
	r.changed()
	if in.model.API == APIMatting {
		// The downloaded engine is a library; this program is what runs it.
		parts["engine"] = program
		if program, err = selfProgram(); err != nil {
			fail(err)
			return
		}
	}
	svc, err := proc.StartAs("model:"+in.id, program, func(port int) ([]string, error) {
		return in.arguments(modelPath, projector, parts, port), nil
	})
	if err != nil {
		fail(fmt.Errorf("starting the model engine: %w", err))
		return
	}
	r.mu.Lock()
	in.svc = svc
	r.mu.Unlock()
	defer svc.Stop()
	if ctx.Err() != nil {
		return
	}
	if err := svc.WaitReady(ctx, loadLimit, "the model engine", func(ctx context.Context) bool { return r.healthy(ctx, svc, in.model.API) }); err != nil {
		fail(err)
		return
	}
	r.mu.Lock()
	in.state, in.since = Ready, time.Now()
	r.mu.Unlock()
	in.settled()
	r.changed()
	select {
	case <-svc.Exited:
		if ctx.Err() == nil {
			r.mu.Lock()
			in.state, in.err, in.svc = Failed, fmt.Errorf("the model engine stopped: %s", svc.Log.Last()), nil
			r.mu.Unlock()
		}
	case <-ctx.Done():
	}
}

// settled lets whoever waits for the model know that it has become Ready or will not.
func (in *instance) settled() { in.settle.Do(func() { close(in.ready) }) }

// arguments are the engine's command line for the model.
func (in *instance) arguments(modelPath, projector string, parts map[string]string, port int) []string {
	values := map[string]string{"model": modelPath, "port": strconv.Itoa(port), "context": strconv.Itoa(in.model.ContextOf())}
	for role, path := range parts {
		values[role] = path
	}
	fill := func(a string) string {
		return placeholder.ReplaceAllStringFunc(a, func(m string) string { return values[m[1:len(m)-1]] })
	}
	var args []string
	for _, a := range in.engine.Arguments {
		args = append(args, fill(a))
	}
	if projector != "" {
		args = append(args, "--mmproj", projector)
	}
	for _, a := range in.model.Arguments {
		args = append(args, fill(a))
	}
	return args
}

// healthy asks the engine whether the model is loaded: it answers 503 while it is still being read.
func (r *Runtime) healthy(ctx context.Context, svc *proc.Service, api string) bool {
	base := svc.Base
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	path := "/health"
	if api == APISD {
		path = "/sdcpp/v1/models/status"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return false
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	if api != APISD {
		return true
	}
	// sd-server answers at once, with the state of the model it is reading.
	var status struct {
		State string `json:"state"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status) != nil {
		return false
	}
	if status.State == "error" {
		svc.Stop() // it stays up with nothing loaded; ending it has the load report why, from what it printed
	}
	return status.State == "ready"
}

// Status is what is known of a model; the zero value, Unloaded, when it was not asked to load.
func (r *Runtime) Status(modelID string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	if in := r.models[modelID]; in != nil {
		return in.status()
	}
	return Status{Model: modelID}
}

// Statuses lists the models that are loading, loaded or failed, in the order they were asked for.
func (r *Runtime) Statuses() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]*instance, 0, len(r.models))
	for _, in := range r.models {
		list = append(list, in)
	}
	slices.SortFunc(list, func(a, b *instance) int { return a.seq - b.seq })
	out := make([]Status, len(list))
	for i, in := range list {
		out[i] = in.status()
	}
	return out
}

// status is called with the runtime's lock held.
func (in *instance) status() Status {
	return Status{Model: in.id, State: in.state, Stage: in.stage, Fraction: in.fraction, Err: in.err, Engine: in.engine,
		Pid: in.svc.Pid(), Since: in.since, Usage: in.usage, Measured: in.measured}
}

// Wait returns once the model is Ready, or fails with why it could not be. The model must have been asked for.
func (r *Runtime) Wait(ctx context.Context, modelID string) error {
	r.mu.Lock()
	in := r.models[modelID]
	r.mu.Unlock()
	if in == nil {
		return fmt.Errorf("the model %q is not loaded", modelID)
	}
	select {
	case <-in.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch in.state {
	case Ready:
		return nil
	case Failed:
		return in.err
	}
	return fmt.Errorf("the model %q was unloaded", modelID)
}

// Ensure loads the model if it is not loaded and waits until it answers, for a use that needs it at once. Unlike a
// user's switch it leaves the other models in memory.
func (r *Runtime) Ensure(ctx context.Context, cfg *Config, modelID, engineID string) (Endpoint, error) {
	if err := r.Load(cfg, modelID, engineID, false); err != nil {
		return Endpoint{}, err
	}
	if err := r.Wait(ctx, modelID); err != nil {
		return Endpoint{}, err
	}
	ep, ok := r.Endpoint(modelID)
	if !ok {
		return Endpoint{}, fmt.Errorf("the model %q was unloaded", modelID)
	}
	return ep, nil
}

// Endpoint is where to ask a model that is Ready.
func (r *Runtime) Endpoint(modelID string) (Endpoint, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	in := r.models[modelID]
	if in == nil || in.state != Ready || in.svc == nil {
		return Endpoint{}, false
	}
	return Endpoint{URL: in.svc.Base + "/v1", Model: modelID, Client: r.client, Local: true}, true
}

// measure samples what the loaded models and the tracked programs take of the computer, until the runtime is closed.
func (r *Runtime) measure() {
	tick := time.NewTicker(sampleEvery)
	defer tick.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-tick.C:
		}
		r.mu.Lock()
		pids := map[int][]func(proc.Usage){} // who wants the measurement of each program
		for _, in := range r.models {
			if pid := in.svc.Pid(); pid != 0 {
				pids[pid] = append(pids[pid], func(u proc.Usage) { in.usage, in.measured = u, true })
			}
		}
		pidFuncs := map[*tracked]func() int{}
		for _, t := range r.tracked {
			pidFuncs[t] = t.pid
		}
		r.mu.Unlock()
		// A tracked program's id is asked outside the lock, as the code that knows it has locks of its own.
		for t, pid := range pidFuncs {
			if id := pid(); id != 0 {
				pids[id] = append(pids[id], func(u proc.Usage) { t.usage, t.measured = u, true })
			} else {
				r.mu.Lock()
				t.measured = false
				r.mu.Unlock()
			}
		}
		if len(pids) == 0 {
			continue
		}
		list := make([]int, 0, len(pids))
		for pid := range pids {
			list = append(list, pid)
		}
		usage := r.meter.Sample(list...)
		r.mu.Lock()
		for pid, set := range pids {
			if u, ok := usage[pid]; ok {
				for _, f := range set {
					f(u)
				}
			}
		}
		r.mu.Unlock()
		r.changed()
	}
}
