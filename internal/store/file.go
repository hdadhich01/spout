package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileStore is the default Store implementation: each session is a folder
// under <dir> with meta.json + data.raw.
//
// Layout:
//
//	<dir>/<name>/meta.json   — session metadata (JSON)
//	<dir>/<name>/data.raw    — raw terminal bytes (append-only)
//
// Old flat-file layout (<dir>/<name>.json + <dir>/<name>.raw) is migrated
// automatically on startup.
type FileStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	dir      string
}

// New creates a FileStore backed by the given directory. The returned
// concrete *FileStore satisfies the Store interface.
func New(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating store dir: %w", err)
	}
	s := &FileStore{
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
func (s *FileStore) sessionDir(name string) string {
	return filepath.Join(s.dir, name)
}

// metaPath / dataPath return the file locations inside a session folder.
func (s *FileStore) metaPath(name string) string {
	return filepath.Join(s.sessionDir(name), "meta.json")
}

func (s *FileStore) dataPath(name string) string {
	return filepath.Join(s.sessionDir(name), "data.raw")
}

func (s *FileStore) eventsPath(name string) string {
	return filepath.Join(s.sessionDir(name), "events.jsonl")
}

// AppendEvent appends one raw event line to the run's events.jsonl. Append-only,
// alongside data.raw — same folder-per-run layout. Creates the folder if the
// run hasn't been touched yet (events can race ahead of metadata).
func (s *FileStore) AppendEvent(name string, raw []byte) error {
	if err := os.MkdirAll(s.sessionDir(name), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.eventsPath(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(bytes.TrimRight(raw, "\n"), '\n'))
	return err
}

// Events returns the run's raw events.jsonl, or nil if absent.
func (s *FileStore) Events(name string) []byte {
	data, err := os.ReadFile(s.eventsPath(name))
	if err != nil {
		return nil
	}
	return data
}

// SessionDir returns the absolute folder path for a named session
// (`<store-root>/<name>`). Used by CLI read commands that need to
// stream `data.raw` directly from disk instead of going through the
// in-memory `Session.history`.
func (s *FileStore) SessionDir(name string) string {
	return s.sessionDir(name)
}

// DataPath returns the absolute path of the raw byte file for a session
// (`<store-root>/<name>/data.raw`).
func (s *FileStore) DataPath(name string) string {
	return s.dataPath(name)
}

// migrateFlat moves any old <name>.json/.raw files into <name>/meta.json + data.raw.
func (s *FileStore) migrateFlat() error {
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

// Create creates a new active session.
func (s *FileStore) Create(m RunMeta) (*Session, error) {
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
		Name:        m.Name,
		Mode:        m.Mode,
		Job:         m.Job,
		Run:         m.Run,
		Label:       m.Label,
		Command:     m.Command,
		Dir:         m.Dir,
		Host:        m.Host,
		User:        m.User,
		GitBranch:   m.GitBranch,
		GitCommit:   m.GitCommit,
		ServerURL:   m.ServerURL,
		ServerToken: m.ServerToken,
		StartedAt:   time.Now(),
		Active:      true,
		viewers:     make(map[chan []byte]struct{}),
		file:        f,
	}
	s.sessions[m.Name] = sess
	s.saveMeta(sess)
	return sess, nil
}

// Rename changes a session's name.
func (s *FileStore) Rename(oldName, newName string) error {
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
func (s *FileStore) Delete(name string) error {
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
func (s *FileStore) Get(name string) *Session {
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
func (s *FileStore) Resolve(name string) string {
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
func (s *FileStore) List() []*Session {
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

func (s *FileStore) saveMeta(sess *Session) {
	os.MkdirAll(s.sessionDir(sess.Name), 0755)
	data, _ := json.Marshal(sess)
	os.WriteFile(s.metaPath(sess.Name), data, 0644)
}

// SaveMeta persists session metadata (call after close or periodically).
func (s *FileStore) SaveMeta(sess *Session) {
	s.saveMeta(sess)
}

func (s *FileStore) loadExisting() error {
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
