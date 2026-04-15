package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Session represents a single spout run with its metadata and live data.
type Session struct {
	Name      string    `json:"name"`
	Mode      string    `json:"mode,omitempty"` // "pipe" or "run"
	Command   string    `json:"command,omitempty"`
	Dir       string    `json:"dir,omitempty"`
	Host      string    `json:"host,omitempty"`
	User      string    `json:"user,omitempty"`
	GitBranch string    `json:"git_branch,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at,omitempty"`
	LastDataAt time.Time `json:"last_data_at,omitempty"`
	BytesRecv  int64     `json:"bytes_recv"`
	Lines      int64     `json:"lines"`
	Active     bool      `json:"active"`
	ExitCode   int       `json:"exit_code"`
	HasExit    bool      `json:"has_exit"`

	recentBytes int64     // bytes received in current window
	windowStart time.Time // start of current measurement window
	wasIdle     bool      // was the session idle before the latest burst?

	mu      sync.Mutex
	viewers map[chan []byte]struct{}
	history []byte
	file    *os.File
}

// Store manages multiple sessions with file-backed persistence.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	dir      string
}

// New creates a store backed by the given directory.
//
// Layout: each session is a folder under <dir>:
//
//	<dir>/<name>/meta.json   - session metadata
//	<dir>/<name>/data.raw    - raw terminal bytes
//
// Old flat-file layout (<dir>/<name>.json + <dir>/<name>.raw) is migrated
// automatically on startup.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating store dir: %w", err)
	}
	s := &Store{
		sessions: make(map[string]*Session),
		dir:      dir,
	}
	if err := s.migrateFlat(); err != nil {
		return nil, fmt.Errorf("migrating old sessions: %w", err)
	}
	if err := s.loadExisting(); err != nil {
		return nil, err
	}
	return s, nil
}

// sessionDir returns the folder path for a named session.
func (s *Store) sessionDir(name string) string {
	return filepath.Join(s.dir, name)
}

// metaPath / dataPath return the file locations inside a session folder.
func (s *Store) metaPath(name string) string {
	return filepath.Join(s.sessionDir(name), "meta.json")
}

func (s *Store) dataPath(name string) string {
	return filepath.Join(s.sessionDir(name), "data.raw")
}

// migrateFlat moves any old <name>.json/.raw files into <name>/meta.json + data.raw.
func (s *Store) migrateFlat() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil // dir doesn't exist yet, nothing to migrate
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".json" && ext != ".raw" {
			continue
		}
		base := name[:len(name)-len(ext)]
		newDir := s.sessionDir(base)
		if err := os.MkdirAll(newDir, 0755); err != nil {
			continue
		}
		oldPath := filepath.Join(s.dir, name)
		var newPath string
		if ext == ".json" {
			newPath = filepath.Join(newDir, "meta.json")
		} else {
			newPath = filepath.Join(newDir, "data.raw")
		}
		os.Rename(oldPath, newPath)
	}
	return nil
}

// RunMeta holds metadata for creating a new session.
type RunMeta struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Command   string `json:"command,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Host      string `json:"host,omitempty"`
	User      string `json:"user,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
}

// Create creates a new active session.
func (s *Store) Create(m RunMeta) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if old, exists := s.sessions[m.Name]; exists {
		old.Close()
	}

	if err := os.MkdirAll(s.sessionDir(m.Name), 0755); err != nil {
		return nil, fmt.Errorf("creating session dir: %w", err)
	}
	f, err := os.Create(s.dataPath(m.Name))
	if err != nil {
		return nil, fmt.Errorf("creating session file: %w", err)
	}

	sess := &Session{
		Name:      m.Name,
		Mode:      m.Mode,
		Command:   m.Command,
		Dir:       m.Dir,
		Host:      m.Host,
		User:      m.User,
		GitBranch: m.GitBranch,
		StartedAt: time.Now(),
		Active:    true,
		viewers:   make(map[chan []byte]struct{}),
		file:      f,
	}
	s.sessions[m.Name] = sess
	s.saveMeta(sess)
	return sess, nil
}

// Rename changes a session's name.
func (s *Store) Rename(oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[oldName]
	if !ok {
		return fmt.Errorf("session %q not found", oldName)
	}
	if _, exists := s.sessions[newName]; exists {
		return fmt.Errorf("session %q already exists", newName)
	}

	// Rename the folder on disk.
	if err := os.Rename(s.sessionDir(oldName), s.sessionDir(newName)); err != nil {
		return fmt.Errorf("renaming session dir: %w", err)
	}

	delete(s.sessions, oldName)
	sess.Name = newName
	s.sessions[newName] = sess
	s.saveMeta(sess)
	return nil
}

// Delete removes a session and its files.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[name]
	if !ok {
		return fmt.Errorf("session %q not found", name)
	}
	sess.Close()
	delete(s.sessions, name)

	os.RemoveAll(s.sessionDir(name))
	return nil
}

// Get returns a session by name. Supports prefix matching - if no exact match
// is found and exactly one session starts with the given name, return that.
func (s *Store) Get(name string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sess, ok := s.sessions[name]; ok {
		return sess
	}
	// Prefix match.
	var match *Session
	for k, sess := range s.sessions {
		if len(k) > len(name) && k[:len(name)+1] == name+"-" {
			if match != nil {
				return nil // ambiguous
			}
			match = sess
		}
	}
	return match
}

// Resolve returns the full session name from a prefix. Used by CLI commands.
func (s *Store) Resolve(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.sessions[name]; ok {
		return name
	}
	for k := range s.sessions {
		if len(k) > len(name) && k[:len(name)+1] == name+"-" {
			return k
		}
	}
	return name
}

// List returns all sessions sorted by start time (newest first).
func (s *Store) List() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		list = append(list, sess)
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].StartedAt.Equal(list[j].StartedAt) {
			return list[i].StartedAt.After(list[j].StartedAt)
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// Status returns the current state of a session.
//
// Run mode (tmux, exit code may be recorded via OSC 9999 marker):
//   streaming - actively producing output
//   awaiting  - active but idle (shell prompt, waiting for input)
//   success   - ended cleanly, exit code 0
//   error     - ended with non-zero exit code
//   killed    - ended before any exit marker was captured (spout kill,
//               terminal closed, crash, etc.)
//
// Pipe mode (no exit code available):
//   streaming - bytes are flowing
//   success   - pipe closed after sending data (peekStdin gates creation,
//               so by definition bytes flowed)
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
func (sess *Session) Subscribe() (chan []byte, []byte, bool) {
	ch := make(chan []byte, 512)
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

	// Count newlines for line metric.
	for _, b := range data {
		if b == '\n' {
			sess.Lines++
		}
	}

	// Scan for spout exit marker: \x1b]9999;<code>\x07
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

	for ch := range sess.viewers {
		select {
		case ch <- data:
		default:
		}
	}
}

// scanExitMarker looks for the spout exit marker in a byte stream.
// Format: \x1b]9999;<digits>\x07
// Returns the exit code and true if found.
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

func (s *Store) saveMeta(sess *Session) {
	os.MkdirAll(s.sessionDir(sess.Name), 0755)
	data, _ := json.Marshal(sess)
	os.WriteFile(s.metaPath(sess.Name), data, 0644)
}

// SaveMeta persists session metadata (call after close or periodically).
func (s *Store) SaveMeta(sess *Session) {
	s.saveMeta(sess)
}

func (s *Store) loadExisting() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		data, err := os.ReadFile(s.metaPath(name))
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		sess.Active = false
		sess.viewers = make(map[chan []byte]struct{})

		if raw, err := os.ReadFile(s.dataPath(sess.Name)); err == nil {
			sess.history = raw
		}

		s.sessions[sess.Name] = &sess
	}
	return nil
}
