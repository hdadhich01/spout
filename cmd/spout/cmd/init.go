package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// defaultProjectConfig is what `spout init` writes. Only `server` is set;
// everything else is commented out as a reference for the user.
const defaultProjectConfig = `# spout.yaml - project config
#
# Discovered automatically by walking up from cwd to the git root.
# Overrides ~/.config/spout/config.yaml on conflicts.
# Safe to commit (tokens go in .env, not here).

# Server to use for runs from this project. Can be:
#   - a profile name from your system config (e.g. "work")
#   - a host:port (e.g. "spout.company.internal:3000")
#   - "localhost:3000" for the local server
server: localhost:3000

# Session name (defaults to folder name). Groups runs on the dashboard.
# name: my-project

# Project-scoped server profiles (overrides system config on name conflicts).
# Tokens still go in .env, not here.
# servers:
#   staging:
#     url: staging.internal:4000

# Runs to start together with the bare 'spout' command.
# Each gets its own tab in the dashboard. Labels are freeform.
# runs:
#   - label: training
#     command: python train.py --epochs 100
#   - label: gpu
#     command: watch -n 2 nvidia-smi
#   - label: logs
#     command: tail -f output.log
`

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a spout.yaml here",
	Long: `Create a spout.yaml in the current directory.

All spout config files use the same name (spout.yaml) and the same
schema. The system one lives at ~/.config/spout/spout.yaml. Project
ones live in any directory and walk up from cwd to the git root.
Deeper files override upper layers.

  spout init

Safe to commit to git - tokens go in .env (next to the yaml).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := filepath.Join(".", "spout.yaml")
		if _, err := os.Stat(path); err == nil {
			warn("spout.yaml %s at %s", cYellow("already exists"), cAqua(path))
			if !confirm("overwrite?") {
				info("%s", cDim("cancelled"))
				return nil
			}
		}
		if err := os.WriteFile(path, []byte(defaultProjectConfig), 0644); err != nil {
			return fmt.Errorf("writing spout.yaml: %w", err)
		}
		ok("%s %s", cGreen("created"), cAqua(path))
		return nil
	},
}

func init() {
	initCmd.GroupID = groupSetup
	rootCmd.AddCommand(initCmd)
}
