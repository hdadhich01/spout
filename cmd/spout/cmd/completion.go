package cmd

import (
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// completeTmuxSessions returns active tmux session names for tab completion.
func completeTmuxSessions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var matches []string
	for _, name := range lines {
		if name != "" && strings.HasPrefix(name, toComplete) {
			matches = append(matches, name)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}
