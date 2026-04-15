package cmd

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:               "open <name>",
	Short:             "Open a run in the browser",
	Long: `Open a run's dashboard URL in the default browser.

  spout open ember
  spout open ember-k9p1`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, found := findRunServer(args[0])
		if !found {
			warn("run %s %s on %s", cBold(args[0]), cYellow("not found"), cAqua(addr))
		}
		url := fmt.Sprintf("http://%s/r/%s", addr, args[0])
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
