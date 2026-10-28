package capture

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/proc"
)

// A live stream is encoded once, as an MPEG transport stream on ffmpeg's standard output, and the relay hands it to
// one ffmpeg per destination, which copies it to the platform's server without encoding it again — as OBS's
// multiple-output plugin does. Each destination connects, reconnects and falls behind on its own: one that is slow
// or down never holds up the others. A destination that joins, or rejoins, gets the stream's tables and then
// everything from the next keyframe, so its server sees a clean start.

// tsPacket is the size of a transport stream packet.
const tsPacket = 188

// tsTables follows the stream's program tables, so that a destination joining late can be given them first, and
// knows which packets begin a keyframe.
type tsTables struct {
	pat, pmt []byte // the latest of each, a whole packet
	pmtPID   int
	videoPID int
}

func newTables() tsTables { return tsTables{pmtPID: -1, videoPID: -1} }

func tsPID(p []byte) int { return int(p[1]&0x1f)<<8 | int(p[2]) }

// tsPayload is the packet's payload, after its adaptation field; nil when it has none.
func tsPayload(p []byte) []byte {
	control := p[3] >> 4 & 3
	at := 4
	if control&2 != 0 {
		at += 1 + int(p[4])
	}
	if control&1 == 0 || at >= tsPacket {
		return nil
	}
	return p[at:]
}

// section is the table section that begins in a packet: its bytes after the section length, cut to that length.
func section(p []byte) []byte {
	if p[1]&0x40 == 0 { // a table that does not begin here (ffmpeg's tables fit one packet)
		return nil
	}
	b := tsPayload(p)
	if len(b) < 1 || len(b) < 1+int(b[0])+3 {
		return nil
	}
	b = b[1+int(b[0]):] // the pointer field
	n := int(b[1]&0x0f)<<8 | int(b[2])
	if len(b) < 3+n || n < 4 {
		return nil
	}
	return b[3 : 3+n-4] // without the CRC
}

// see notes the tables a packet carries.
func (t *tsTables) see(p []byte) {
	switch pid := tsPID(p); {
	case pid == 0:
		s := section(p)
		if len(s) < 5 {
			return
		}
		t.pat = append(t.pat[:0], p...)
		for e := s[5:]; len(e) >= 4; e = e[4:] {
			if program := int(e[0])<<8 | int(e[1]); program != 0 {
				t.pmtPID = int(e[2]&0x1f)<<8 | int(e[3])
				break
			}
		}
	case pid == t.pmtPID:
		s := section(p)
		if len(s) < 9 {
			return
		}
		t.pmt = append(t.pmt[:0], p...)
		info := int(s[7]&0x0f)<<8 | int(s[8])
		for e := s[min(9+info, len(s)):]; len(e) >= 5; {
			kind, pid, n := e[0], int(e[1]&0x1f)<<8|int(e[2]), int(e[3]&0x0f)<<8|int(e[4])
			if kind == 0x1b || kind == 0x24 { // H.264 or H.265
				t.videoPID = pid
				break
			}
			e = e[min(5+n, len(e)):]
		}
	}
}

// keyframe reports whether a packet begins a keyframe: ffmpeg marks those with the random access indicator.
func (t *tsTables) keyframe(p []byte) bool {
	return tsPID(p) == t.videoPID && p[1]&0x40 != 0 && p[3]&0x20 != 0 && p[4] > 0 && p[5]&0x40 != 0
}

// ready reports whether a destination can join: the tables have been seen.
func (t *tsTables) ready() bool { return t.pat != nil && t.pmt != nil && t.videoPID >= 0 }

// relay takes the encoder's output as an io.Writer and passes it on to the destinations.
type relay struct {
	tables  tsTables
	partial []byte

	mu      sync.Mutex
	targets []*target
	closed  bool

	total   atomic.Int64
	rate    rateMeter
	options pushOptions
}

func newRelay(o pushOptions) *relay { return &relay{tables: newTables(), options: o} }

// Write takes whatever the encoder wrote; it never blocks on a destination.
func (r *relay) Write(b []byte) (int, error) {
	n := len(b)
	r.total.Add(int64(n))
	r.rate.add(int64(n))
	data := append(r.partial, b...)
	// Whole packets go on; a part of one waits for the rest. Anything out of step is skipped up to the next sync byte.
	var batch []byte
	var keys []int
	for len(data) >= tsPacket {
		if data[0] != 0x47 {
			i := bytes.IndexByte(data[1:], 0x47)
			if i < 0 {
				data = data[:0]
				break
			}
			data = data[1+i:]
			continue
		}
		p := data[:tsPacket]
		r.tables.see(p)
		if r.tables.keyframe(p) {
			keys = append(keys, len(batch))
		}
		batch = append(batch, p...)
		data = data[tsPacket:]
	}
	r.partial = append(r.partial[:0], data...)
	if len(batch) == 0 {
		return n, nil
	}
	r.mu.Lock()
	targets := r.targets
	r.mu.Unlock()
	for _, t := range targets {
		if t.resync.Swap(false) {
			t.synced = false
		}
		if t.synced {
			t.push(batch)
			continue
		}
		if len(keys) == 0 || !r.tables.ready() {
			continue
		}
		start := make([]byte, 0, 2*tsPacket+len(batch)-keys[0])
		start = append(append(append(start, r.tables.pat...), r.tables.pmt...), batch[keys[0]:]...)
		if t.push(start) {
			t.synced = true
		}
	}
	return n, nil
}

// add starts sending to a destination; its id must be new.
func (r *relay) add(dest Target) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("the stream has ended")
	}
	for _, t := range r.targets {
		if t.ID == dest.ID {
			return fmt.Errorf("%s is already being streamed to", dest.Name)
		}
	}
	t := newTarget(dest, r.options)
	// The slice is replaced, never changed in place, so Write can use the one it read without the lock.
	r.targets = append(r.targets[:len(r.targets):len(r.targets)], t)
	go t.run()
	return nil
}

// remove stops sending to a destination and waits for its connection to close.
func (r *relay) remove(id string) error {
	r.mu.Lock()
	var found *target
	var kept []*target
	for _, t := range r.targets {
		if t.ID == id {
			found = t
		} else {
			kept = append(kept, t)
		}
	}
	r.targets = kept
	r.mu.Unlock()
	if found == nil {
		return errors.New("no such destination is being streamed to")
	}
	found.close()
	return nil
}

// retry connects a destination that gave up again at once.
func (r *relay) retry(id string) error {
	for _, t := range r.list() {
		if t.ID == id {
			t.retryNow()
			return nil
		}
	}
	return errors.New("no such destination is being streamed to")
}

func (r *relay) list() []*target {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.targets
}

// close ends every destination's connection, letting each send what it has, and waits for them.
func (r *relay) close() {
	r.mu.Lock()
	r.closed = true
	targets := r.targets
	r.targets = nil
	r.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.close()
		}()
	}
	wg.Wait()
}

// TargetState is how sending to a destination is going.
type TargetState int

const (
	TargetConnecting TargetState = iota // connecting to the server, or waiting for a keyframe to start with
	TargetLive                          // the server is receiving the stream
	TargetRetrying                      // the connection failed or dropped; it is tried again after a pause
	TargetFailed                        // it failed every time it was tried and is no longer tried
	TargetStopped                       // the stream to it was stopped
)

// TargetStatus is a destination's state.
type TargetStatus struct {
	ID, Name string
	State    TargetState
	Err      string        // why the last connection ended, when it failed
	Kbps     float64       // what it sends now
	Sent     int64         // bytes sent in all
	Live     time.Duration // how long the current connection has been live
	Retry    time.Time     // when it is tried again, while retrying
	Attempts int           // failed connections in a row
	Dropped  int           // times it fell so far behind that the stream skipped to the next keyframe for it
}

// pushOptions are the timings of the connections; tests make them short.
type pushOptions struct {
	retryFirst, retryMost time.Duration // the pause after the first failure, doubling up to the longest
	givingUp              int           // failures in a row after which a destination never yet live is given up
	queue                 int           // the bytes a destination may fall behind before the stream skips ahead for it
	closeWait             time.Duration // how long a closing connection may take to send what it has
}

var defaultPush = pushOptions{retryFirst: 2 * time.Second, retryMost: 30 * time.Second, givingUp: 5, queue: 8 << 20, closeWait: 5 * time.Second}

// target is one destination and the ffmpeg that sends to it.
type target struct {
	Target
	o pushOptions

	synced bool        // only the relay's Write uses it: the destination has had its start and gets everything
	resync atomic.Bool // set when a new connection needs a new start

	mu       sync.Mutex
	queue    [][]byte
	queued   int
	taking   bool // a connection is open and takes the stream
	state    TargetState
	err      string
	sent     int64
	rate     float64
	since    time.Time // when the current connection went live
	next     time.Time
	attempts int
	dropped  int
	everLive bool

	wake  chan struct{} // there is something in the queue
	retry chan struct{} // retry now
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func newTarget(dest Target, o pushOptions) *target {
	return &target{Target: dest, o: o, wake: make(chan struct{}, 1), retry: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
}

// push queues data for the connection; it is false when no connection takes it. A destination that has fallen too
// far behind loses what it has queued and starts again at the next keyframe.
func (t *target) push(b []byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.taking {
		return false
	}
	if t.queued+len(b) > t.o.queue {
		t.queue, t.queued = nil, 0
		t.dropped++
		t.resync.Store(true)
		return false
	}
	t.queue = append(t.queue, b)
	t.queued += len(b)
	select {
	case t.wake <- struct{}{}:
	default:
	}
	return true
}

// take hands the writer what is queued.
func (t *target) take() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	q := t.queue
	t.queue, t.queued = nil, 0
	return q
}

func (t *target) setTaking(on bool) {
	t.mu.Lock()
	t.taking, t.queue, t.queued = on, nil, 0
	t.mu.Unlock()
	if on {
		t.resync.Store(true)
	}
}

func (t *target) status() TargetStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := TargetStatus{ID: t.ID, Name: t.Name, State: t.state, Err: t.err, Kbps: t.rate, Sent: t.sent, Attempts: t.attempts, Dropped: t.dropped}
	if t.state == TargetLive {
		s.Live = time.Since(t.since)
	}
	if t.state == TargetRetrying {
		s.Retry = t.next
	}
	return s
}

func (t *target) retryNow() {
	select {
	case t.retry <- struct{}{}:
	default:
	}
}

// close stops the destination's connection and waits for it to end.
func (t *target) close() {
	t.once.Do(func() { close(t.stop) })
	<-t.done
}

func (t *target) stopping() bool {
	select {
	case <-t.stop:
		return true
	default:
		return false
	}
}

// run keeps the destination connected until it is closed: it connects, and when the connection fails or drops,
// waits longer each time and connects again. One that has never been live is given up after a few failures, since
// a wrong address or stream key never starts working by itself; the user can try it again.
func (t *target) run() {
	defer close(t.done)
	for {
		err := t.connect()
		if t.stopping() {
			t.set(func() { t.state, t.rate = TargetStopped, 0 })
			return
		}
		var wait time.Duration
		t.set(func() {
			t.attempts++
			t.err, t.rate = describePushError(err), 0
			if !t.everLive && t.attempts >= t.o.givingUp {
				t.state = TargetFailed
				return
			}
			wait = t.o.retryFirst << min(t.attempts-1, 16)
			wait = min(wait, t.o.retryMost)
			t.state, t.next = TargetRetrying, time.Now().Add(wait)
		})
		var pause <-chan time.Time
		if wait > 0 {
			pause = time.After(wait)
		}
		select {
		case <-t.stop:
			t.set(func() { t.state = TargetStopped })
			return
		case <-t.retry:
			t.set(func() { t.attempts = 0 })
		case <-pause:
		}
	}
}

func (t *target) set(fn func()) {
	t.mu.Lock()
	fn()
	t.mu.Unlock()
}

// connect runs one ffmpeg that sends the stream to the destination, until it ends; the error says why.
func (t *target) connect() error {
	t.set(func() { t.state = TargetConnecting })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, err := media.Command(ctx, "ffmpeg", pushArgs(t.URL)...)
	if err != nil {
		return err
	}
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	progress, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	t.setTaking(true)
	exited := make(chan struct{})
	go t.write(stdin, exited, cmd)
	t.follow(progress)
	err = cmd.Wait()
	close(exited)
	t.setTaking(false)
	if err != nil {
		if why := pushFailure(stderr.lines(), t.URL); why != "" {
			return errors.New(why)
		}
		return err
	}
	if t.stopping() {
		return nil
	}
	return errors.New("the server ended the stream")
}

// write passes the queue to ffmpeg; when the destination is closed it ends ffmpeg's input so it sends the rest and
// stops, and kills it should that take too long.
func (t *target) write(stdin io.WriteCloser, exited <-chan struct{}, cmd *exec.Cmd) {
	defer stdin.Close()
	for {
		select {
		case <-exited:
			return
		case <-t.stop:
			stdin.Close()
			select {
			case <-exited:
			case <-time.After(t.o.closeWait):
				proc.KillTree(cmd.Process)
			}
			return
		case <-t.wake:
			for _, b := range t.take() {
				if _, err := stdin.Write(b); err != nil {
					return
				}
			}
		}
	}
}

// follow reads ffmpeg's progress reports: the destination is live once bytes reach its server.
func (t *target) follow(r io.Reader) {
	var meter rateMeter
	var size, reported int64
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, value, _ := strings.Cut(sc.Text(), "=")
		switch key {
		case "total_size":
			size, _ = strconv.ParseInt(value, 10, 64)
		case "progress":
			if size <= 0 {
				continue
			}
			meter.add(size - reported)
			t.set(func() {
				if t.state != TargetLive {
					t.state, t.since, t.everLive, t.err = TargetLive, time.Now(), true, ""
				}
				t.attempts, t.sent, t.rate = 0, t.sent+size-reported, meter.kbps()
			})
			reported = size
		}
	}
	io.Copy(io.Discard, r)
}

// pushArgs copy the transport stream from standard input to the destination: FLV over RTMP or RTMPS, the transport
// stream itself over SRT. A network that stalls for ten seconds ends the connection, so that it is made again.
func pushArgs(address string) []string {
	format := "flv"
	if strings.HasPrefix(strings.ToLower(address), "srt://") {
		format = "mpegts"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostats", "-progress", "pipe:1", "-stats_period", "1",
		"-probesize", "1000000", "-analyzeduration", "1000000", "-f", "mpegts", "-i", "pipe:0", "-map", "0", "-c", "copy"}
	if format == "flv" {
		args = append(args, "-flvflags", "no_duration_filesize")
	}
	return append(args, "-rw_timeout", "10000000", "-f", format, address)
}

// describePushError is the error to show for a failed connection.
func describePushError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// pushFailure picks the line of ffmpeg's errors that explains a failed connection, without the stream key.
func pushFailure(lines []string, address string) string {
	return strings.ReplaceAll(ffmpegFailure(lines), address, redact(address))
}

// ffmpegFailure picks the line of ffmpeg's errors that explains why it failed — the first: what follows only says
// that it could not go on — without ffmpeg's "[rtmp @ 0x…]" prefix.
func ffmpegFailure(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			if i := strings.Index(line, "] "); i >= 0 {
				line = line[i+2:]
			}
		}
		if line != "" {
			return line
		}
	}
	return ""
}

// redact is a destination's address without its stream key, to show in messages.
func redact(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return "the destination"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// rateMeter measures a rate over the last few seconds.
type rateMeter struct {
	mu      sync.Mutex
	start   time.Time
	bytes   int64
	current float64
}

func (m *rateMeter) add(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.start.IsZero() {
		m.start = now
	}
	m.bytes += n
	if d := now.Sub(m.start); d >= 2*time.Second {
		m.current = float64(m.bytes) * 8 / d.Seconds() / 1000
		m.start, m.bytes = now, 0
	}
}

func (m *rateMeter) kbps() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Target is a place a live stream is sent to.
type Target struct {
	ID   string // tells destinations apart while streaming
	Name string
	URL  string // the server's address with the stream key: rtmp://, rtmps:// or srt://
}

// ErrTargetAddress means a destination's address is not one a stream can be sent to.
var ErrTargetAddress = errors.New("a stream is sent to an rtmp://, rtmps:// or srt:// address")

// Check reports whether the destination's address can be streamed to.
func (t Target) Check() error {
	u, err := url.Parse(strings.TrimSpace(t.URL))
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s: %w", t.Name, ErrTargetAddress)
	}
	switch strings.ToLower(u.Scheme) {
	case "rtmp", "rtmps", "srt":
		return nil
	}
	return fmt.Errorf("%s: %w", t.Name, ErrTargetAddress)
}

// StreamURL joins a server's address and a stream key the way streaming software does: the key is the last part
// of the path. A key that is empty leaves the address as it is, for platforms that give one address with the key
// in it.
func StreamURL(server, key string) string {
	server, key = strings.TrimSpace(server), strings.TrimSpace(key)
	switch {
	case key == "":
		return server
	case strings.HasPrefix(strings.ToLower(server), "srt://"):
		// SRT carries the key as the stream id.
		sep := "?"
		if strings.Contains(server, "?") {
			sep = "&"
		}
		return server + sep + "streamid=" + url.QueryEscape(key)
	case strings.HasSuffix(server, "/"):
		return server + key
	}
	return server + "/" + key
}
