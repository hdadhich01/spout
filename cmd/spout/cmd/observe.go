package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hdadhich01/spout/internal/observe"
	"github.com/spf13/cobra"
)

var observeCmd = &cobra.Command{
	Use:   "observe [run] [stream]",
	Short: "Show a run's observability events",
	Long: `Print the observer's assessment of a run from the local copy: latest
status, extracted metrics, triggered detectors, and the full timeline.

Observability is opt-in — enable it with ` + "`observe.enabled`" + ` in spout.yaml
or the --observe flag on the run.

  spout observe ember
  spout observe exp-2/training`,
	Args:              cobra.RangeArgs(0, 2),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		local, _, err := openLocal()
		if err != nil {
			return err
		}

		if len(args) == 0 {
			picked, perr := pickLocalRun(local, "observe")
			if perr != nil {
				return perr
			}
			args = []string{picked}
		}

		sess, err := resolveLocalSingle(local, args)
		if err != nil {
			return err
		}

		events, err := readEvents(filepath.Join(local.SessionDir(sess.Name), "events.jsonl"))
		if err != nil {
			return err
		}
		if len(events) == 0 {
			info("no observe events for %s — enable %s in spout.yaml or pass %s",
				cBold(sess.Name), cAqua("observe"), cAqua("--observe"))
			return nil
		}
		printObserve(sess.Name, events)
		return nil
	},
}

// readEvents parses a run's events.jsonl into Events, skipping bad lines.
func readEvents(path string) ([]observe.Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []observe.Event
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e observe.Event
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// obsStatusColored renders an observe status in the shared palette.
func obsStatusColored(status string) string {
	switch status {
	case "on_track":
		return cGreen("on track")
	case "warning":
		return cYellow("warning")
	case "failed":
		return cRed("failed")
	case "done":
		return cGreen("done")
	default:
		return cDim(status)
	}
}

func obsDot(status string) string {
	switch status {
	case "on_track", "done":
		return cGreen("●")
	case "warning":
		return cYellow("●")
	case "failed":
		return cRed("●")
	default:
		return cDim("●")
	}
}

func printObserve(name string, events []observe.Event) {
	fmt.Fprintln(stderr)
	section(fmt.Sprintf("observe  %s", cDim(name)))

	if latest := events[len(events)-1].Obs; latest != nil {
		label("status", obsStatusColored(latest.Status))
		if latest.RunType != "" {
			label("type", latest.RunType)
		}
		if latest.Summary != "" {
			label("summary", latest.Summary)
		}
		for _, m := range latest.Metrics {
			unit := ""
			if m.Unit != "" {
				unit = " " + m.Unit
			}
			label("metric", fmt.Sprintf("%s = %v%s", m.Name, m.Value, unit))
		}
		for det, evidence := range latest.Detectors {
			if evidence != "" {
				label(det, cRed(evidence))
			}
		}
	}

	fmt.Fprintln(stderr)
	section(fmt.Sprintf("timeline  %s", cDim(fmt.Sprintf("(%d)", len(events)))))
	for _, e := range events {
		summary := ""
		status := ""
		if e.Obs != nil {
			summary, status = e.Obs.Summary, e.Obs.Status
		}
		fmt.Fprintf(stderr, "  %s  %s  %s\n", cDim(e.TS.Format("15:04:05")), obsDot(status), summary)
	}
	fmt.Fprintln(stderr)
}

func init() {
	observeCmd.GroupID = groupSession
	rootCmd.AddCommand(observeCmd)
}
