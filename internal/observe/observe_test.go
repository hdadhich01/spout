package observe

import (
	"sync"
	"testing"
	"time"

	"github.com/hdadhich01/spout/internal/config"
)

// memSink collects emitted events for assertions.
type memSink struct {
	mu     sync.Mutex
	events []Event
}

func (m *memSink) Emit(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}
func (m *memSink) Close() {}
func (m *memSink) all() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event(nil), m.events...)
}

func TestPrefilterSignals(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		wantLoop  bool
	}{
		{"plain", "hello\nworld\n", false, false},
		{"error keyword", "starting\nFATAL: disk full\n", true, false},
		{"ansi-wrapped error", "\x1b[31mError:\x1b[0m boom\n", true, false},
		{"repeated lines", "retry\nretry\nretry\nretry\n", false, true},
		{"distinct lines no loop", "a\nb\nc\nd\n", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pf := newPrefilter(0)
			pf.feed([]byte(tt.input))
			s := pf.snapshot()
			if s.ErrorKeyword != tt.wantErr {
				t.Errorf("ErrorKeyword = %v, want %v", s.ErrorKeyword, tt.wantErr)
			}
			if s.RepeatedLine != tt.wantLoop {
				t.Errorf("RepeatedLine = %v, want %v", s.RepeatedLine, tt.wantLoop)
			}
		})
	}
}

func TestStubClientStatus(t *testing.T) {
	c := stubClient{}
	got, _ := c.Observe(nil, Request{Signals: Signals{ErrorKeyword: true}})
	if got.Status != "warning" {
		t.Errorf("in-run error status = %q, want warning", got.Status)
	}
	got, _ = c.Observe(nil, Request{Final: true, Signals: Signals{ErrorKeyword: true}})
	if got.Status != "failed" {
		t.Errorf("final error status = %q, want failed", got.Status)
	}
	got, _ = c.Observe(nil, Request{Signals: Signals{RepeatedLine: true}})
	if got.Detectors["loop"] == "" {
		t.Error("expected loop detector to be populated on repeated lines")
	}
}

func TestEngineEmitsSynthesis(t *testing.T) {
	on := true
	sink := &memSink{}
	e := New(RunConfig{
		Run:     "test-run",
		Command: "pytest",
		Mode:    "pipe",
		Observe: &config.Observe{Enabled: true, Detectors: &config.ObserveDetectors{Loop: &on}},
	}, sink)
	if e == nil {
		t.Fatal("engine should be non-nil when observe is enabled")
	}

	e.Feed([]byte("running tests\n"))
	e.Feed([]byte("FAILED: assertion error\n"))
	time.Sleep(30 * time.Millisecond) // let the loop drain the tap
	e.Close()

	events := sink.all()
	if len(events) == 0 {
		t.Fatal("expected at least the synthesis event")
	}
	last := events[len(events)-1]
	if last.Kind != KindSynthesis {
		t.Errorf("last event kind = %q, want %q", last.Kind, KindSynthesis)
	}
	if last.Obs == nil || last.Obs.Status != "failed" {
		t.Errorf("synthesis status = %+v, want failed (error keyword seen)", last.Obs)
	}
}

func TestEngineNilWhenDisabled(t *testing.T) {
	if e := New(RunConfig{Observe: nil}); e != nil {
		t.Error("engine should be nil when observe is nil")
	}
	if e := New(RunConfig{Observe: &config.Observe{Enabled: false}}); e != nil {
		t.Error("engine should be nil when observe is disabled")
	}
	// Nil engine methods must be safe.
	var e *Engine
	e.Feed([]byte("x"))
	e.Close()
}
