package cmd

import (
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// completeTmuxSessions returns suggestions for a run / stream arg.
//
// First arg: active tmux session names ∪ distinct stream labels seen on
// the resolved server. This lets users tab-complete `spout kill training`
// even when the run tmux session is named something else.
//
// Second arg: if the first arg resolves to a run group, return the labels
// of that group's streams. Otherwise no completion.
func completeTmuxSessions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	seen := map[string]struct{}{}

	// Start with tmux session names (the most common tab-complete path).
	if len(args) == 0 {
		if out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output(); err == nil {
			for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if name != "" {
					seen[name] = struct{}{}
				}
			}
		}
	}

	// Pull server run list for labels / run names. Best-effort - if the
	// server is unreachable the tmux names alone are still useful.
	addr, _ := resolveServer()
	runs := fetchRunsIgnoreErr(addr)

	if len(args) == 1 {
		// Second positional: labels within the run matching args[0].
		var out []string
		for _, r := range runs {
			if r.Run == args[0] && strings.HasPrefix(r.Label, toComplete) {
				out = append(out, r.Label)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}

	// First positional: also offer Run names and stream labels.
	for _, r := range runs {
		if r.Run != "" {
			seen[r.Run] = struct{}{}
		}
		if r.Label != "" {
			seen[r.Label] = struct{}{}
		}
		seen[r.Name] = struct{}{}
	}

	var matches []string
	for name := range seen {
		if strings.HasPrefix(name, toComplete) {
			matches = append(matches, name)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}
