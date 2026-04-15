package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"
)

var attachCmd = &cobra.Command{
	Use:   "attach <name>",
	Short: "Reattach to a background run",
	Long: `Reattach to a detached spout run session (tmux).

  spout attach ember
  spout attach ember-k9p1

Detach again with Ctrl-b d.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		tmux, err := exec.LookPath("tmux")
		if err != nil {
			return fmt.Errorf("tmux not found")
		}
		// Replace the current process with tmux attach.
		return syscall.Exec(tmux, []string{"tmux", "attach-session", "-t", args[0]}, os.Environ())
	},
}

func init() {
	attachCmd.GroupID = groupSession
	rootCmd.AddCommand(attachCmd)
}
