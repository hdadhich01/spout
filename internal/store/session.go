package store

import (
	"os"
	"strings"
	"sync"
	"time"
)

// Session represents a single Spout run with its metadata and live byte stream.
//
// A session is shared across backends — only persistence differs. The
// `file` field is FileStore-specific (a real file handle); other backends
// leave it nil and persist their own way.
type Session struct {
	Name string `json:"name"`
	Mode string `json:"mode,omitempty"` // "pipe" or "run"

	// Grouping metadata. A streams-launched session has all three set:
	//   Job:   ml-training    (config.yaml `job:` field, the dashboard bucket)
	//   Run:   exp-2          (this invocation; shared across sibling streams)
	//   Label: training       (which pane within the run)
	// Standalone runs (spout run cmd, pipe mode) leave all three empty.
	Job   string `json:"job,omitempty"`
	Run   string `json:"run,omitempty"`
	Label string `json:"label,omitempty"`

	Command   string `json:"command,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Host      string `json:"host,omitempty"`
	User      string `json:"user,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	GitCommit string `json:"git_commit,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at,omitempty"`
	LastDataAt time.Time `json:"last_data_at,omitempty"`

	BytesRecv int64 `json:"bytes_recv"`
	Lines     int64 `json:"lines"`
	Active    bool  `json:"active"`
	ExitCode  int   `json:"exit_code"`
	HasExit   bool  `json:"has_exit"`

	// Server identity (CLI local copy only). Populated when the CLI dual-writes
	// a run to the local store — frozen at run-start time so later commands
	// (`spout share`, `spout delete`, `spout rename`) target the server the
	// run actually streamed to, even if the user's `default_server` has
	// changed since. Server-side stores leave these empty.
	//
	// ServerToken stores the env-var NAME (not the value) so the metadata
	// file is safe to share. Resolving the actual token requires the env
	// var to be set in the current process.
	ServerURL   string `json:"server_url,omitempty"`
	ServerToken string `json:"server_token,omitempty"`

	recentBytes int64     // bytes received in current window
	windowStart time.Time // start of current measurement window
	wasIdle     bool      // was the session idle before the latest burst?

	mu      sync.Mutex
	viewers map[chan []byte]struct{}
	history []byte

	// sizeCap bounds the in-memory history (rolling window). 0 = unlimited.
	// Set by the server based on `SPOUT_SESSION_CAP`. The on-disk data.raw
	// still grows past the cap in M1 — disk-side rolling window is an M3
	// hosted concern (self-host default is unlimited, no need).
	sizeCap int64

	// FileStore-specific: per-session append-only byte file. Other
	// backends leave this nil and persist via their own mechanism.
	file *os.File
}

// SetCap sets the maximum in-memory history size for this session. When
// exceeded, the oldest bytes are dropped (rolling window). 0 = unlimited.
//
// Called by the server after Store.Create to apply per-session caps from
// `SPOUT_SESSION_CAP`. Idempotent; safe to call multiple times.
func (sess *Session) SetCap(cap int64) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.sizeCap = cap
}

// RunMeta is the metadata blob a caller passes to Store.Create.
type RunMeta struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Job       string `json:"job,omitempty"`
	Run       string `json:"run,omitempty"`
	Label     string `json:"label,omitempty"`
	Command   string `json:"command,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Host      string `json:"host,omitempty"`
	User      string `json:"user,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	GitCommit string `json:"git_commit,omitempty"`

	// Server identity (CLI local copy only — see Session.ServerURL).
	ServerURL   string `json:"server_url,omitempty"`
	ServerToken string `json:"server_token,omitempty"`
}

// Status returns the current state of a session.
//
// Run mode (tmux, exit code may be recorded via OSC 9999 marker):
//
//	streaming - actively producing output
//	awaiting  - active but idle (shell prompt, waiting for input)
//	success   - ended cleanly, exit code 0
//	error     - ended with non-zero exit code
//	killed    - ended before any exit marker was captured (spout kill,
//	            terminal closed, crash, etc.)
//
// Pipe mode (no exit code available):
//
//	streaming - bytes are flowing
//	success   - pipe closed after sending data (peekStdin gates creation,
//	            so by definition bytes flowed)
func (sess *Session) Status() string {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	isRun := sess.Mode == "run"

	if !sess.Active {
		if sess.HasExit {
			if sess.ExitCode == 0 {
				return "success"
			}
			return "error"
		}
		if isRun {
			return "killed"
		}
		return "success"
	}

	// Pipe mode: only one active state.
	if !isRun {
		return "streaming"
	}

	// Run mode: exit marker + tmux still alive = command done, shell prompt.
	if sess.HasExit {
		return "awaiting"
	}
	if sess.LastDataAt.IsZero() || time.Since(sess.LastDataAt) > time.Second {
		return "awaiting"
	}
	if sess.recentBytes < 50 {
		return "awaiting"
	}
	if sess.wasIdle {
		sess.wasIdle = false
		return "awaiting"
	}
	return "streaming"
}

// History returns a snapshot of all bytes recorded so far.
func (sess *Session) History() []byte {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	out := make([]byte, len(sess.history))
	copy(out, sess.history)
	return out
}

// Subscribe returns a channel for live data, the history snapshot, and whether
// the session is still active. If inactive, the channel is pre-closed.
//
// The channel is bounded (256 frames). If the receiver can't keep up and the
// channel fills, the broadcast loop in Write() closes + drops the channel.
// The receiver's `range ch` loop exits cleanly; its WS handler should send a
// final WS close to the browser, which reconnects on its own.
func (sess *Session) Subscribe() (chan []byte, []byte, bool) {
	ch := make(chan []byte, 256)
	sess.mu.Lock()
	if sess.viewers == nil {
		sess.viewers = make(map[chan []byte]struct{})
	}
	snapshot := make([]byte, len(sess.history))
	copy(snapshot, sess.history)
	active := sess.Active
	if active {
		sess.viewers[ch] = struct{}{}
	} else {
		close(ch) // Signal to viewer that stream is done.
	}
	sess.mu.Unlock()
	return ch, snapshot, active
}

// Unsubscribe removes a viewer channel.
func (sess *Session) Unsubscribe(ch chan []byte) {
	sess.mu.Lock()
	delete(sess.viewers, ch)
	sess.mu.Unlock()
}

// Write appends data to the session, broadcasts to viewers, and persists to disk.
func (sess *Session) Write(data []byte) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.history = append(sess.history, data...)
	sess.BytesRecv += int64(len(data))
	sess.LastDataAt = time.Now()

	// Per-session cap (rolling window): drop oldest in-memory bytes when
	// history exceeds the cap. data.raw on disk keeps growing in M1 — the
	// disk-side rolling window is an M3 concern (self-host = unlimited).
	if sess.sizeCap > 0 && int64(len(sess.history)) > sess.sizeCap {
		drop := int64(len(sess.history)) - sess.sizeCap
		sess.history = sess.history[drop:]
	}

	// Count newlines for line metric.
	for _, b := range data {
		if b == '\n' {
			sess.Lines++
		}
	}

	// Scan for spout exit marker: \x1b]9999;<code>\x07
	// (Will migrate to TLV EXIT opcode in M1b — see ARCHITECTURE.md §4.1.)
	if code, ok := scanExitMarker(data); ok {
		sess.ExitCode = code
		sess.HasExit = true
	}

	// Track bytes in a rolling 1-second window for burst detection.
	now := time.Now()
	if now.Sub(sess.windowStart) > time.Second {
		// If we were idle (>2s gap), mark it - the next burst is likely a
		// screen redraw from tmux attach, not real command output.
		sess.wasIdle = now.Sub(sess.LastDataAt) > 2*time.Second
		sess.recentBytes = 0
		sess.windowStart = now
	}
	sess.recentBytes += int64(len(data))

	if sess.file != nil {
		sess.file.Write(data)
	}

	// Broadcast to viewers. Each viewer has a bounded channel (256 frames
	// — see Subscribe). On overflow, close + drop the slow viewer; their
	// WS handler exits via the closed channel, the browser sees a clean
	// close, and reconnects on its own. The producer is never back-pressured
	// by a slow viewer.
	for ch := range sess.viewers {
		select {
		case ch <- data:
		default:
			close(ch)
			delete(sess.viewers, ch)
		}
	}
}

// Close marks the session as ended, notifies all viewers, and closes the file.
func (sess *Session) Close() {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !sess.Active {
		return
	}
	sess.Active = false
	sess.EndedAt = time.Now()
	if sess.file != nil {
		sess.file.Close()
		sess.file = nil
	}
	// Close all viewer channels so their WebSocket handlers exit.
	for ch := range sess.viewers {
		close(ch)
	}
	sess.viewers = make(map[chan []byte]struct{})
}

// scanExitMarker looks for the spout exit marker in a byte stream.
// Format: \x1b]9999;<digits>\x07
// Returns the exit code and true if found.
//
// Note: this inline-marker scan is pre-TLV; the EXIT opcode (M1b) replaces
// it with a robust framing-level signal. Until then this is the path.
func scanExitMarker(data []byte) (int, bool) {
	const prefix = "\x1b]9999;"
	const suffix = '\x07'
	idx := strings.Index(string(data), prefix)
	if idx < 0 {
		return 0, false
	}
	rest := data[idx+len(prefix):]
	end := -1
	for i, b := range rest {
		if b == suffix {
			end = i
			break
		}
	}
	if end < 0 {
		return 0, false
	}
	codeStr := string(rest[:end])
	code := 0
	for _, c := range codeStr {
		if c < '0' || c > '9' {
			return 0, false
		}
		code = code*10 + int(c-'0')
	}
	return code, true
}
