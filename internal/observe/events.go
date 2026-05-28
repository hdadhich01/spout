// Package observe is Spout's CLI-side observability engine. It taps the live
// byte stream of a run, cheaply pre-filters it, and periodically asks a model
// to characterize progress — emitting small structured Events (status,
// metrics, synthesis, and built-in agent-failure detectors).
//
// One engine, many consumers: the same Events are persisted locally, shipped
// to the server, and (later) exported as OTel spans. The engine runs on the
// producer side, on plaintext, before any Tier-2 encryption — see
// ARCHITECTURE.md.
package observe

import "time"

// Event kinds.
const (
	KindObservation = "observation" // a routine in-run check
	KindSynthesis   = "synthesis"   // the end-of-run summary
)

// Event is one observation emitted by the engine — the unit persisted to
// events.jsonl and (later) shipped to the server and OTel.
type Event struct {
	ID      string       `json:"id"`
	Run     string       `json:"run"`
	TS      time.Time    `json:"ts"`
	Kind    string       `json:"kind"`
	Trigger string       `json:"trigger,omitempty"` // tick | error | loop | idle | exit
	Obs     *Observation `json:"obs,omitempty"`
}

// Observation is the model's structured assessment of the run so far. The
// same shape is returned for routine checks and the final synthesis.
type Observation struct {
	Status    string            `json:"status"`              // on_track | warning | failed | done
	Summary   string            `json:"summary"`             // one-line human summary
	Severity  string            `json:"severity,omitempty"`  // info | warn | crit
	RunType   string            `json:"run_type,omitempty"`  // classify detector (build/training/agent/...)
	Metrics   []Metric          `json:"metrics,omitempty"`   // extracted numbers
	Detectors map[string]string `json:"detectors,omitempty"` // detector name -> evidence ("" = clear)
	Decisions []Decision        `json:"decisions,omitempty"` // decision-trail entries
	NextCheck int               `json:"next_check_secs,omitempty"`
	// Stub is true when this observation came from the network-free stub
	// (no LLM configured). The UI shows a small explainer banner in this
	// case so users understand why the summaries are minimal.
	Stub bool `json:"stub,omitempty"`
}

// Metric is one number the model pulled out of the output.
type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
	Trend string  `json:"trend,omitempty"` // up | down | flat
}

// Decision is one entry in the run's decision trail (agent runs).
type Decision struct {
	TS     time.Time `json:"ts"`
	Action string    `json:"action"`
	Reason string    `json:"reason,omitempty"`
}

// nonTerminal reports whether a status warrants continued checking.
func nonTerminal(status string) bool {
	switch status {
	case "done", "failed":
		return false
	default:
		return true
	}
}
