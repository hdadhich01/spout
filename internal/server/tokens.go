package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenEntry is one named ingest credential — a "user" allowed to push runs to
// this server.
type TokenEntry struct {
	Label   string    `json:"label"`
	Token   string    `json:"token"`
	Created time.Time `json:"created"`
}

// TokenStore is a file-backed set of ingest tokens. The running server and the
// `spout server token` CLI share one file: every method reloads it when its
// mtime changes, so a token created on the CLI is honored by a live server
// (and vice versa) without a restart.
type TokenStore struct {
	mu        sync.Mutex
	path      string
	entries   []TokenEntry
	legacy    string // SPOUT_TOKEN, always valid when set (back-compat)
	loadedMod time.Time
}

// LoadTokenStore opens the token file at path and records a legacy SPOUT_TOKEN
// that stays valid for back-compat ("" = none).
func LoadTokenStore(path, legacy string) *TokenStore {
	s := &TokenStore{path: path, legacy: legacy}
	s.mu.Lock()
	s.reloadLocked()
	s.mu.Unlock()
	return s
}

// reloadLocked re-reads the file when its mtime has changed. Caller holds mu.
func (s *TokenStore) reloadLocked() {
	fi, err := os.Stat(s.path)
	if err != nil || fi.ModTime().Equal(s.loadedMod) {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var entries []TokenEntry
	if json.Unmarshal(data, &entries) == nil {
		s.entries = entries
		s.loadedMod = fi.ModTime()
	}
}

func (s *TokenStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s.entries, "", "  ")
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return err
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.loadedMod = fi.ModTime()
	}
	return nil
}

// HasAny reports whether any ingest credential exists (a stored token or the
// legacy SPOUT_TOKEN). Used to decide whether auth is active at all.
func (s *TokenStore) HasAny() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	return s.legacy != "" || len(s.entries) > 0
}

// Valid reports whether token matches a stored entry or the legacy SPOUT_TOKEN.
func (s *TokenStore) Valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	if s.legacy != "" && token == s.legacy {
		return true
	}
	for _, e := range s.entries {
		if e.Token == token {
			return true
		}
	}
	return false
}

// Add mints a new token under label and persists it.
func (s *TokenStore) Add(label string) (TokenEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	for _, e := range s.entries {
		if e.Label == label {
			return TokenEntry{}, fmt.Errorf("a token labeled %q already exists", label)
		}
	}
	tok, err := genToken()
	if err != nil {
		return TokenEntry{}, err
	}
	e := TokenEntry{Label: label, Token: tok, Created: time.Now()}
	s.entries = append(s.entries, e)
	if err := s.saveLocked(); err != nil {
		return TokenEntry{}, err
	}
	return e, nil
}

// Revoke removes a token by its label or its value.
func (s *TokenStore) Revoke(labelOrToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	kept := make([]TokenEntry, 0, len(s.entries))
	found := false
	for _, e := range s.entries {
		if e.Label == labelOrToken || e.Token == labelOrToken {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return fmt.Errorf("no token matching %q", labelOrToken)
	}
	s.entries = kept
	return s.saveLocked()
}

// List returns a copy of all entries.
func (s *TokenStore) List() []TokenEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	return append([]TokenEntry(nil), s.entries...)
}

func genToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "spt_" + hex.EncodeToString(b), nil
}
