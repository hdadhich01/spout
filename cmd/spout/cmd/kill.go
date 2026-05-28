package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/spf13/cobra"
)

var (
	killForce bool
	killAll   bool
)

var killCmd = &cobra.Command{
	Use:   "kill [run] [stream]",
	Short: "Stop a run or one of its streams",
	Long: `Stop a run or one of its streams. Whole-run kill ends every stream; stream kill leaves siblings running.

  spout kill                          # picker
  spout kill ember                    # by run name
  spout kill training                 # by stream label
  spout kill exp-2/training           # one stream
  spout kill --all                    # every active run`,
	Args:              cobra.RangeArgs(0, 2),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := exec.LookPath("tmux"); err != nil {
			return fmt.Errorf("tmux is required for 'spout kill' but not found")
		}

		if killAll {
			return killEverything(killForce)
		}

		var t target
		if len(args) == 0 {
			addr, _ := resolveServer()
			var err error
			t, err = pickRun(addr, "kill", func(r runEntry) bool { return isActive(r.Status) })
			if err != nil {
				return err
			}
		} else {
			targets, err := resolveTargets(args, !killForce)
			if err != nil {
				return err
			}
			t = targets[0]
		}

		// tmux session backing this target.
		tmuxSession := t.Name
		if t.Run != "" {
			tmuxSession = t.Run
		}
		tmuxAlive := exec.Command("tmux", "has-session", "-t", tmuxSession).Run() == nil

		// Pull the group's children once for accurate confirm + counters.
		var children []runEntry
		if t.Kind == targetGroup {
			for _, r := range fetchRunsIgnoreErr(t.Addr) {
				if r.Run == t.Run {
					children = append(children, r)
				}
			}
		}

		// Fast path: nothing to kill.
		if !tmuxAlive {
			info("%s is %s", cBold(displayTarget(t)), cDim("already ended"))
			return nil
		}

		if !killForce {
			switch t.Kind {
			case targetGroup:
				aggStatus := aggregateStatus(children)
				info("run %s is %s  (%d %s)",
					cBold(t.Run),
					statusText(aggStatus),
					len(children),
					pluralize("stream", len(children)))
				fmt.Fprintln(stderr)
				maxLabel := 0
				for _, c := range children {
					if len(c.Label) > maxLabel {
						maxLabel = len(c.Label)
					}
				}
				for _, c := range children {
					gap := strings.Repeat(" ", maxLabel-len(c.Label))
					fmt.Fprintf(stderr, "    %s  %s%s  %s\n",
						statusDot(c.Status),
						cBold(c.Label), gap,
						statusText(c.Status))
				}
				fmt.Fprintln(stderr)
				warnObserveDeps(streamLabelsOf(children))
				if anyActive(children) {
					if !confirm("kill all streams?") {
						info("%s", cDim("cancelled"))
						return nil
					}
				}

			case targetStream, targetStandalone:
				info("%s is %s", cBold(displayTarget(t)), statusText(t.Entry.Status))
				warnObserveDeps([]string{t.Label})
				if isActive(t.Entry.Status) {
					if !confirm("kill?") {
						info("%s", cDim("cancelled"))
						return nil
					}
				}
			}
		}

		switch t.Kind {
		case targetGroup:
			n := len(children)
			if err := exec.Command("tmux", "kill-session", "-t", t.Run).Run(); err != nil {
				info("%s was %s", cBold(t.Run), cDim("already ended"))
				return nil
			}
			ok("%s run %s  (%d %s)", cGreen("killed"), cBold(t.Run), n, pluralize("stream", n))

		case targetStream:
			paneID, err := findPaneByLabel(t.Run, t.Label)
			if err != nil {
				info("%s was %s", cBold(t.Run+"/"+t.Label), cDim("already ended"))
				return nil
			}
			if err := exec.Command("tmux", "kill-pane", "-t", paneID).Run(); err != nil {
				info("%s was %s", cBold(t.Run+"/"+t.Label), cDim("already ended"))
				return nil
			}
			ok("%s %s", cGreen("killed"), cBold(t.Run+"/"+t.Label))

		case targetStandalone:
			if err := exec.Command("tmux", "kill-session", "-t", t.Name).Run(); err != nil {
				info("%s was %s", cBold(t.Name), cDim("already ended"))
				return nil
			}
			ok("%s %s", cGreen("killed"), cBold(t.Name))
		}
		return nil
	},
}

// killEverything tears down every active run spout knows about.
func killEverything(force bool) error {
	addr, _ := resolveServer()
	runs := fetchRunsIgnoreErr(addr)

	// Dedup by tmux session name (Run for streams, Name for standalones).
	seen := map[string]int{}
	var order []string
	streamsPerTarget := map[string]int{}
	for _, r := range runs {
		if !isActive(r.Status) {
			continue
		}
		key := r.Name
		if r.Run != "" {
			key = r.Run
		}
		if _, ok := seen[key]; !ok {
			seen[key] = len(order)
			order = append(order, key)
		}
		streamsPerTarget[key]++
	}

	if len(order) == 0 {
		info("%s", cDim("nothing to kill"))
		return nil
	}

	if !force {
		warn("%s %d active %s:", cYellow("about to kill"), len(order), pluralize("run", len(order)))
		for _, k := range order {
			n := streamsPerTarget[k]
			if n > 1 {
				fmt.Fprintf(stderr, "  %s %s  %s\n", statusDot("streaming"), cBold(k), cDim(fmt.Sprintf("(%d streams)", n)))
			} else {
				fmt.Fprintf(stderr, "  %s %s\n", statusDot("streaming"), cBold(k))
			}
		}
		fmt.Fprintln(stderr)
		if !confirmTyped("destructive - confirm killing ALL", "kill") {
			info("%s", cDim("cancelled"))
			return nil
		}
	}

	killed := 0
	for _, k := range order {
		if err := exec.Command("tmux", "kill-session", "-t", k).Run(); err == nil {
			killed++
		}
	}
	ok("%s %d of %d %s", cGreen("killed"), killed, len(order), pluralize("run", len(order)))
	return nil
}

// displayTarget renders a target's human-facing name. Includes job
// prefix when present so cross-job ambiguity is visible.
//
//	[ml-training] exp-2/training
//	exp-2/training            (no job)
//	fox-a3f2                  (standalone)
func displayTarget(t target) string {
	job := t.Entry.Job
	var base string
	if t.Run != "" && t.Label != "" {
		base = t.Run + "/" + t.Label
	} else if t.Run != "" {
		base = t.Run
	} else {
		base = t.Name
	}
	if job != "" {
		return cDim("["+job+"]") + " " + base
	}
	return base
}

// pluralize returns "stream" or "streams" depending on n.
func pluralize(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// streamLabelsOf collects the labels from a list of stream runEntries.
// Used to scan watch rules for dependencies.
func streamLabelsOf(runs []runEntry) []string {
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		if r.Label != "" {
			out = append(out, r.Label)
		}
	}
	return out
}

// anyActive returns true if any run in the list is still active.
func anyActive(runs []runEntry) bool {
	for _, r := range runs {
		if isActive(r.Status) {
			return true
		}
	}
	return false
}

// warnObserveDeps prints a one-liner per observe rule from the cwd's
// spout.yaml that depends on any of the given stream labels. Best-
// effort: silently no-ops when there's no config or no observe block.
//
// Informational: killing the stream removes a source the rule reads.
func warnObserveDeps(labels []string) {
	if len(labels) == 0 {
		return
	}
	cfg := config.Load()
	if cfg.Observe == nil || len(cfg.Observe.Rules) == 0 {
		return
	}
	want := map[string]bool{}
	for _, l := range labels {
		want[l] = true
	}
	for _, rule := range cfg.Observe.Rules {
		hit := false
		for _, src := range rule.Sources {
			if want[src] {
				hit = true
				break
			}
		}
		if hit {
			info("observe rule %s depends on this", cBold(rule.Name))
		}
	}
}

// findPaneByLabel looks up the tmux pane for stream `label` inside the
// given session. Returns the pane-id (e.g. "%5") ready for kill-pane.
func findPaneByLabel(session, label string) (string, error) {
	if id := findPaneIDByLabel(session, label); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("no stream %q in run %q", label, session)
}

// findPaneIDByLabel walks every pane in the tmux session (across all
// windows) and returns the first pane whose label matches. The label
// is stored two ways at launch (streams.go): as the pane title (visible
// in tmux's status bar) and as the pane-scoped user option @spout-label
// (which survives if the user manually renames the pane). The lookup
// prefers @spout-label, then pane title.
//
// Returns "" if no match — callers translate that to a friendly error.
//
// The `-s` flag is critical: it lists panes session-wide. Multi-stream
// runs with > paneLayoutThreshold streams put each stream in its own
// window, and a session-wide search is the only way to find them all.
func findPaneIDByLabel(session, label string) string {
	// Tab separator because pane titles may contain spaces (a manually
	// renamed pane). Field order: pane_id, @spout-label (may be empty),
	// pane_title.
	const sep = "\t"
	out, err := exec.Command("tmux", "list-panes", "-s", "-t", session,
		"-F", "#{pane_id}"+sep+"#{@spout-label}"+sep+"#{pane_title}").Output()
	if err != nil {
		return ""
	}
	for _, line := range splitLines(string(out)) {
		fields := strings.SplitN(line, sep, 3)
		if len(fields) < 3 {
			continue
		}
		id, opt, title := fields[0], fields[1], fields[2]
		if opt == label || (opt == "" && title == label) {
			return id
		}
	}
	return ""
}

// splitLines splits on \n and drops trailing empties. Used for parsing
// tmux list-panes output.
func splitLines(s string) []string {
	out := strings.Split(s, "\n")
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// cutSpace splits a string on the first run of whitespace.
// Returns (head, tail, ok). ok is false when there's no whitespace
// (caller treats that as a malformed line).
func cutSpace(s string) (head, tail string, ok bool) {
	for i, c := range s {
		if c == ' ' || c == '\t' {
			tail = strings.TrimLeft(s[i+1:], " \t")
			return s[:i], tail, true
		}
	}
	return s, "", false
}

func init() {
	killCmd.Flags().BoolVarP(&killForce, "yes", "y", false, "skip confirmation")
	killCmd.Flags().BoolVarP(&killAll, "all", "a", false, "kill every active run")
	killCmd.GroupID = groupSession
	rootCmd.AddCommand(killCmd)
}
