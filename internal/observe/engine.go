package observe

import (
	"context"
	"fmt"
	"time"

	"github.com/hdadhich01/spout/internal/config"
)

// Engine tuning. Intervals are clamped so a model hint can't busy-loop the
// CLI or silence it; maxCalls caps per-run cost.
const (
	minInterval = 10 * time.Second
	maxInterval = 5 * time.Minute
	maxWindow   = 16 * 1024 // bytes of recent output handed to the model
	idleAfter   = 30 * time.Second
	callTimeout = 20 * time.Second
	maxCalls    = 200
)

// RunConfig is everything the engine needs to observe one run.
type RunConfig struct {
	Run     string
	Command string
	Mode    string
	Dir     string
	Observe *config.Observe
}

// Engine observes one run: it taps the byte stream (non-blocking), pre-filters
// cheaply, and periodically asks the model to characterize progress, emitting
// Events to its sinks. All scheduling/LLM work happens on its own goroutine so
// the streaming hot path is never blocked.
type Engine struct {
	rc        RunConfig
	client    Client
	pf        *prefilter
	sinks     []Sink
	detectors []string
	rules     []config.ObserveRule
	interval  time.Duration
	started   time.Time

	tap    chan []byte
	done   chan struct{}
	closed chan struct{}

	// Touched only by the run() goroutine.
	window []byte
	prev   *Observation
	calls  int
	seq    int
}

// New constructs an Engine and starts its loop. Returns nil when observe is
// disabled — Feed and Close are nil-safe, so callers needn't branch.
func New(rc RunConfig, sinks ...Sink) *Engine {
	o := rc.Observe
	if o == nil || !o.Enabled {
		return nil
	}
	e := &Engine{
		rc:        rc,
		client:    NewClient(o),
		pf:        newPrefilter(idleAfter),
		sinks:     sinks,
		detectors: activeDetectors(o),
		rules:     o.Rules,
		interval:  parseInterval(o.DefaultInterval),
		started:   time.Now(),
		tap:       make(chan []byte, 256),
		done:      make(chan struct{}),
		closed:    make(chan struct{}),
	}
	go e.run()
	return e
}

// Feed hands a chunk of output to the engine without ever blocking the
// caller. If the engine is busy, the chunk is dropped — sampling is fine.
// The caller must not mutate b after the call (Spout passes a fresh copy).
func (e *Engine) Feed(b []byte) {
	if e == nil {
		return
	}
	select {
	case e.tap <- b:
	default:
	}
}

// Close stops the loop, runs a final synthesis, flushes sinks, and returns.
// Bounded so a hung model call can't hang the CLI on exit.
func (e *Engine) Close() {
	if e == nil {
		return
	}
	close(e.done)
	select {
	case <-e.closed:
	case <-time.After(callTimeout + 2*time.Second):
	}
	for _, s := range e.sinks {
		s.Close()
	}
}

func (e *Engine) run() {
	defer close(e.closed)
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	last := time.Now()

	check := func(trigger string, final bool) {
		if !final {
			if e.calls >= maxCalls || time.Since(last) < minInterval {
				return
			}
		}
		last = time.Now()
		e.runCheck(trigger, final)
		if !final && e.prev != nil && e.prev.NextCheck > 0 {
			ticker.Reset(clampInterval(time.Duration(e.prev.NextCheck) * time.Second))
		}
	}

	for {
		select {
		case b := <-e.tap:
			e.pf.feed(b)
			e.appendWindow(b)
			if r := e.pf.trigger(); r != "" {
				check(r, false)
			}
		case <-ticker.C:
			s := e.pf.snapshot()
			if s.Dirty || (e.prev != nil && nonTerminal(e.prev.Status)) {
				check("tick", false)
			}
		case <-e.done:
			e.runCheck("exit", true) // final synthesis always runs
			return
		}
	}
}

func (e *Engine) runCheck(trigger string, final bool) {
	e.calls++
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	obs, err := e.client.Observe(ctx, Request{
		Command:   e.rc.Command,
		Mode:      e.rc.Mode,
		Dir:       e.rc.Dir,
		Elapsed:   time.Since(e.started),
		Window:    e.windowString(),
		Signals:   e.pf.snapshot(),
		Prev:      e.prev,
		Detectors: e.detectors,
		Rules:     e.rules,
		Final:     final,
	})
	e.pf.consume()
	if err != nil || obs == nil {
		return
	}
	e.prev = obs

	kind := KindObservation
	if final {
		kind = KindSynthesis
	}
	e.seq++
	ev := Event{
		ID:      fmt.Sprintf("%s-%d", e.rc.Run, e.seq),
		Run:     e.rc.Run,
		TS:      time.Now(),
		Kind:    kind,
		Trigger: trigger,
		Obs:     obs,
	}
	for _, s := range e.sinks {
		s.Emit(ev)
	}
}

// appendWindow keeps the last ~maxWindow bytes of output, bounding memory by
// compacting once the backing slice grows past twice the window.
func (e *Engine) appendWindow(b []byte) {
	e.window = append(e.window, b...)
	if len(e.window) > 2*maxWindow {
		keep := make([]byte, maxWindow)
		copy(keep, e.window[len(e.window)-maxWindow:])
		e.window = keep
	}
}

func (e *Engine) windowString() string {
	w := e.window
	if len(w) > maxWindow {
		w = w[len(w)-maxWindow:]
	}
	return stripANSI(string(w))
}

func activeDetectors(o *config.Observe) []string {
	var out []string
	for _, name := range []string{"classify", "loop", "drift", "amnesia"} {
		if o.Detector(name) {
			out = append(out, name)
		}
	}
	return out
}

func parseInterval(s string) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return clampInterval(d)
	}
	d, _ := time.ParseDuration(config.DefaultObserveInterval)
	return clampInterval(d)
}

func clampInterval(d time.Duration) time.Duration {
	switch {
	case d < minInterval:
		return minInterval
	case d > maxInterval:
		return maxInterval
	default:
		return d
	}
}
