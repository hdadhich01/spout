package cmd

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open [run] [stream]",
	Short: "Open a run in the browser",
	Long: `Open a run's dashboard URL in your browser.

  spout open
  spout open ember
  spout open exp-2/training`,
	Args:              cobra.RangeArgs(0, 2),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		var t target
		if len(args) == 0 {
			addr, _ := resolveServer()
			var err error
			t, err = pickRun(addr, "open", nil)
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
		// For a group, open the run's parent page if we had one; for now
		// open the first stream so the dashboard at least loads something.
		name := t.Name
		if t.Kind == targetGroup {
			name = t.Entry.Name
		}
		url := fmt.Sprintf("http://%s/r/%s", t.Addr, name)
		if err := openBrowser(url); err != nil {
			return fmt.Errorf("opening browser: %w", err)
		}
		ok("%s %s", cGreen("opened"), cAqua(url))
		return nil
	},
}

// openBrowser opens a URL in the default browser cross-platform.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux, bsd, etc.
		if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", url)
		} else {
			return fmt.Errorf("no browser opener found (install xdg-utils)")
		}
	}
	return cmd.Start()
}

func init() {
	openCmd.GroupID = groupSession
	rootCmd.AddCommand(openCmd)
}
