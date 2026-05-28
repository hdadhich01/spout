package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

var attachTiled bool

var attachCmd = &cobra.Command{
	Use:   "attach [run] [stream]",
	Short: "Reattach to a running run",
	Long: `Reattach to a detached run. Detach again with Ctrl-b d.

  spout attach                        # picker
  spout attach ember                  # by run (sub-picker if multi-stream)
  spout attach ember --tiled          # see all streams at once
  spout attach training               # by stream label
  spout attach exp-2/training         # one stream of a run`,
	Args:              cobra.RangeArgs(0, 2),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		tmux, err := exec.LookPath("tmux")
		if err != nil {
			return fmt.Errorf("tmux not found")
		}

		var t target
		if len(args) == 0 {
			addr, _ := resolveServer()
			var err error
			t, err = pickRun(addr, "attach", func(r runEntry) bool { return isActive(r.Status) })
			if err != nil {
				return err
			}
		} else {
			targets, err := resolveTargets(args, true)
			if err != nil {
				return err
			}
			t = targets[0]
		}

		// If the target is a whole run group and the user hasn't asked
		// for the tiled view, drill one level deeper: ask which pane
		// inside the group to focus. Single-stream groups short-circuit
		// automatically. Direct-addressed streams (targetStream with a
		// Label already set) skip this; they've already told us.
		if t.Kind == targetGroup && !attachTiled {
			label, all, err := pickStreamInRun(t.Addr, t.Run)
			if err != nil {
				return err
			}
			if !all {
				t.Label = label
			}
		}

		// Pick which tmux session to attach to:
		//   - group   -> the run name IS the tmux session
		//   - stream  -> same (tmux sessions are per-run, panes per stream)
		//   - standalone -> session name == tmux name
		session := t.Name
		if t.Run != "" {
			session = t.Run
		}

		if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
			return fmt.Errorf("run %s isn't active", cBold(session))
		}

		// The session may have been attached previously with a zoomed
		// pane - clear that state before applying the user's current
		// choice so "all panes" always means tiled and a focused stream
		// always means zoomed on THAT stream.
		unzoomSession(session)

		// If we have a label, focus + zoom that one pane so the user
		// sees just that stream full-screen. Otherwise leave the tiled
		// layout alone. Ctrl-b z toggles zoom manually if needed.
		if t.Label != "" {
			focusAndZoomByLabel(session, t.Label)
		}

		return syscall.Exec(tmux, []string{"tmux", "attach-session", "-t", session}, os.Environ())
	},
}

// unzoomSession toggles zoom off if any pane in the session's active
// window is currently zoomed. tmux uses `window_zoomed_flag` (0 or 1)
// to report state; resize-pane -Z toggles it on the active pane. No-op
// on tmux sessions that aren't zoomed or when the query fails.
func unzoomSession(session string) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", session, "#{window_zoomed_flag}").Output()
	if err != nil {
		return
	}
	if strings.TrimSpace(string(out)) == "1" {
		exec.Command("tmux", "resize-pane", "-Z", "-t", session).Run()
	}
}

// focusAndZoomByLabel selects the pane whose title matches `label` and
// zooms it. `focusPaneByLabel` without zoom just shifted the "current
// pane" marker - tmux would still paint the full tiled grid on attach.
// Zooming gives the user the single-stream view they actually picked.
func focusAndZoomByLabel(session, label string) {
	paneID := paneIDByLabel(session, label)
	if paneID == "" {
		return
	}
	exec.Command("tmux", "select-pane", "-t", paneID).Run()
	exec.Command("tmux", "resize-pane", "-Z", "-t", paneID).Run()
}

// paneIDByLabel returns the tmux pane-id (e.g. "%5") whose label matches
// `label`, or empty if none match.
//
// Lookup checks every pane in the session (including panes in other
// windows — needed for windows-mode multi-stream runs from streams.go).
// Prefers the @spout-label pane option (set at launch and survives manual
// pane renames), falling back to pane title for older runs that didn't
// set the option.
func paneIDByLabel(session, label string) string {
	return findPaneIDByLabel(session, label)
}

func init() {
	attachCmd.Flags().BoolVarP(&attachTiled, "tiled", "t", false, "show all streams tiled")
	attachCmd.GroupID = groupSession
	rootCmd.AddCommand(attachCmd)
}
