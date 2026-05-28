package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// Sink receives events as the engine emits them. Implementations so far:
// FileSink (local events.jsonl). Step 2 adds a server transport and OTel.
type Sink interface {
	Emit(Event)
	Close()
}

// FileSink appends events as JSON lines to a run's events.jsonl. This is the
// canonical local record (ARCHITECTURE.md §2: local archive is canonical).
type FileSink struct {
	mu sync.Mutex
	f  *os.File
}

// NewFileSink opens (creating, appending) the events file at path.
func NewFileSink(path string) (*FileSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &FileSink{f: f}, nil
}

func (s *FileSink) Emit(e Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.f.Write(append(b, '\n'))
}

func (s *FileSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.f.Close()
}

// ServerSink ships events to the spout server's events endpoint so the
// dashboard can render them live. Best-effort: the local FileSink is the
// canonical record (ARCHITECTURE.md §2), so POST failures are swallowed.
type ServerSink struct {
	url      string // http://<addr>/api/run/<run>/events
	tokenVar string // env-var NAME holding the Bearer token, or ""
	http     *http.Client
}

// NewServerSink builds a sink targeting addr for run, authenticating with the
// token in env var tokenVar (when set), mirroring the CLI's rename/delete calls.
func NewServerSink(addr, run, tokenVar string) *ServerSink {
	return &ServerSink{
		url:      fmt.Sprintf("http://%s/api/run/%s/events", addr, run),
		tokenVar: tokenVar,
		http:     &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *ServerSink) Emit(e Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.tokenVar != "" {
		if v := os.Getenv(s.tokenVar); v != "" {
			req.Header.Set("Authorization", "Bearer "+v)
		}
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func (s *ServerSink) Close() {}
