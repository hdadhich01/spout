package observe

import (
	"fmt"
	"strings"
	"time"
)

// systemPrompt is the frozen instruction block — identical across every check
// of a run — so it sits behind the prompt-cache breakpoint. Only the active
// detectors and user rules vary, and those are stable for a given run too.
func systemPrompt(req Request) string {
	var b strings.Builder
	b.WriteString(`You are Spout's run observer. You watch the raw terminal output of a long-running command and report a concise, structured assessment. You are not a chatbot — you emit only JSON.

Return a single JSON object, no prose and no code fences, matching exactly:
{
  "status": "on_track" | "warning" | "failed" | "done",
  "summary": "one concise sentence a developer can read on their phone",
  "severity": "info" | "warn" | "crit",
  "run_type": "build|test|migration|deploy|training|data|agent|generic",
  "metrics": [{"name":"loss","value":0.19,"unit":"","trend":"down"}],
  "detectors": {"loop":"evidence or empty string"},
  "decisions": [{"action":"what was decided/done","reason":"why"}],
  "next_check_secs": 60
}

Rules:
- "done" only when the run clearly finished successfully; "failed" when it ended in error.
- Keep summary under 100 characters. Omit metrics or detectors you can't support with evidence.
- decisions: only for agent runs — list notable choices made since the last check (max 3), newest last. Empty array otherwise.
- next_check_secs is how many seconds until another look is worthwhile (10–300): stable → larger, trouble → smaller.`)

	if len(req.Detectors) > 0 {
		b.WriteString("\n\nActive detectors — set each key in `detectors` to one line of evidence, or \"\" if clear:")
		for _, d := range req.Detectors {
			b.WriteString("\n- " + d + ": " + detectorSpec(d))
		}
	}
	if len(req.Rules) > 0 {
		b.WriteString("\n\nUser rules — fold these into your assessment; reflect a triggered rule in `summary` and `severity`:")
		for _, r := range req.Rules {
			b.WriteString(fmt.Sprintf("\n- %s: %s", r.Name, r.Prompt))
		}
	}
	return b.String()
}

func detectorSpec(name string) string {
	switch name {
	case "classify":
		return "infer run_type from the output's character."
	case "loop":
		return "the process keeps repeating the same failing action/command/error with no progress."
	case "drift":
		return "an agent is diverging from its stated goal or task."
	case "amnesia":
		return "an agent re-asks or re-does settled work, or drops a constraint it was following."
	}
	return ""
}

// userPrompt is the volatile part: run context + recent output. It sits after
// the cache breakpoint, so each check only re-sends what actually changed.
func userPrompt(req Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Command: %s\n", req.Command)
	if req.Dir != "" {
		fmt.Fprintf(&b, "Dir: %s\n", req.Dir)
	}
	fmt.Fprintf(&b, "Elapsed: %s\n", req.Elapsed.Round(time.Second))
	fmt.Fprintf(&b, "Bytes seen: %d (~%.0f B/s)\n", req.Signals.TotalBytes, req.Signals.BytesPerSec)

	var flags []string
	if req.Signals.ErrorKeyword {
		flags = append(flags, "error-keyword")
	}
	if req.Signals.RepeatedLine {
		flags = append(flags, "repeated-lines")
	}
	if req.Signals.Idle {
		flags = append(flags, "idle")
	}
	if len(flags) > 0 {
		fmt.Fprintf(&b, "Local signals: %s\n", strings.Join(flags, ", "))
	}
	if req.Prev != nil {
		fmt.Fprintf(&b, "Previous status: %s — %s\n", req.Prev.Status, req.Prev.Summary)
	}
	if req.Final {
		b.WriteString("\nThe run has ENDED. Produce a final synthesis (status must be done or failed).\n")
	}

	b.WriteString("\nRecent output (most recent last):\n----\n")
	b.WriteString(req.Window)
	b.WriteString("\n----\n")
	return b.String()
}
