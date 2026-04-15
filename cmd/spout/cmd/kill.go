package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

var killForce bool

var killCmd = &cobra.Command{
	Use:   "kill <name>",
	Short: "Stop a running session",
	Long: `Kill a detached spout run (tmux session).

  spout kill ember
  spout kill ember-k9p1
  spout kill ember -y     # skip confirmation`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := exec.LookPath("tmux"); err != nil {
			return fmt.Errorf("tmux not found")
		}
		name := args[0]

		// Check the run's status on the server and show it.
		if !killForce {
			addr, _ := resolveServer()
			if runs, err := fetchRuns(addr); err == nil {
				for _, r := range runs {
					if r.Name == name || displayName(r.Name) == name {
						info("%s is %s", cBold(displayName(r.Name)), statusText(r.Status))
						if isActive(r.Status) {
							if !confirm("kill anyway?") {
								info("%s", cDim("cancelled"))
								return nil
							}
						}
						break
					}
				}
			}
		}

		if err := exec.Command("tmux", "kill-session", "-t", name).Run(); err != nil {
			return fmt.Errorf("killing %s: %w", name, err)
		}
		ok("%s %s", cGreen("killed"), cBold(name))
		return nil
	},
}

func init() {
	killCmd.Flags().BoolVarP(&killForce, "yes", "y", false, "skip confirmation")
	killCmd.GroupID = groupSession
	rootCmd.AddCommand(killCmd)
}
