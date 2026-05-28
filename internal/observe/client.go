package observe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hdadhich01/spout/internal/config"
)

// Client is the backing LLM the observer consults each check. Implementations:
// stubClient (dev, network-free) now; apiClient (Anthropic) / localClient /
// cliClient land in Step 2+.
type Client interface {
	Observe(ctx context.Context, req Request) (*Observation, error)
}

// Request is the payload handed to the model on each check. Window is the
// recent, ANSI-stripped output (the volatile part the model reasons over);
// everything else is run context.
type Request struct {
	Command   string
	Mode      string
	Dir       string
	Elapsed   time.Duration
	Window    string
	Signals   Signals
	Prev      *Observation
	Detectors []string             // active built-in detector names
	Rules     []config.ObserveRule // user-defined prompts, folded into the one call
	Final     bool                 // end-of-run synthesis
}

// NewClient builds the Client for a model config. A missing API key (or any
// not-yet-implemented type) falls back to the network-free stub so enabling
// observe never errors — the stub's summaries are clearly marked "[stub]".
func NewClient(o *config.Observe) Client {
	switch o.ModelType() {
	case "api":
		if key := o.APIKey(); key != "" {
			return newAPIClient(o, key)
		}
		return stubClient{}
	// case "local": return newLocalClient(o)   // Step 2+ (Ollama / OpenAI-compatible)
	// case "cli":   return newCLIClient(o)      // Step 2+ (shell out to an agent)
	default:
		return stubClient{}
	}
}

// stubClient is a deterministic, network-free Client for dev. It echoes the
// pre-filter signals so the whole loop is observable without spending tokens.
type stubClient struct{}

func (stubClient) Observe(_ context.Context, req Request) (*Observation, error) {
	obs := &Observation{
		Status:    "on_track",
		Severity:  "info",
		Detectors: map[string]string{},
		Stub:      true,
	}
	obs.RunType = classifyHeuristic(req.Window)

	if req.Signals.ErrorKeyword {
		obs.Status, obs.Severity = "warning", "warn"
	}
	if req.Signals.RepeatedLine {
		obs.Detectors["loop"] = "identical output lines repeating"
	}
	if req.Final {
		obs.Status = "done"
		if req.Signals.ErrorKeyword {
			obs.Status, obs.Severity = "failed", "crit"
		}
	}

	obs.Metrics = []Metric{{Name: "bytes", Value: float64(req.Signals.TotalBytes), Unit: "B"}}

	// Human-readable, doesn't pretend to be analysis (the banner explains
	// that this is from the stub).
	verb := "running"
	if req.Final {
		verb = "ended"
	}
	obs.Summary = fmt.Sprintf("%s — %s seen at ~%s/s", verb, humanBytes(req.Signals.TotalBytes), humanBytes(int64(req.Signals.BytesPerSec)))
	return obs, nil
}

// humanBytes formats a byte count for the stub summary.
func humanBytes(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	switch {
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/mb)
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/kb)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// classifyHeuristic guesses a run type from the output's character. Used by
// the stub (dev) and as a cheap fallback; the api client lets the model decide.
func classifyHeuristic(window string) string {
	w := strings.ToLower(window)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(w, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("epoch", "loss=", "loss:", "accuracy", "val_loss"):
		return "training"
	case has("tool_use", "assistant:", "thinking…", "i'll ", "let me "):
		return "agent"
	case has("compiling", "error[", "warning:", "cargo", "go build", "linking"):
		return "build"
	case has("pass", "fail", "ok  ", "=== run", "pytest", "test "):
		return "test"
	case has("alter table", "create index", "migrating", "migration"):
		return "migration"
	case has("aws_", "google_", "applying", "terraform", "kubectl", "rollout"):
		return "deploy"
	case has("rows/s", "processed", "%|"):
		return "data"
	default:
		return "generic"
	}
}
