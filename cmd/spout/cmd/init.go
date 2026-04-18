package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a spout.yaml here",
	Long: `Create a spout.yaml in the current directory from the project template.

All spout config files use the same name (spout.yaml) and the same
schema. The global one lives at ~/.config/spout/spout.yaml. Project
ones live in any directory and walk up from cwd to the git root.
Deeper files override upper layers.

  spout init

The template source is internal/config/templates/project.yaml in the
spout repo - edit it there to change what this command writes.

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
		if err := os.WriteFile(path, []byte(config.ProjectTemplate), 0644); err != nil {
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
