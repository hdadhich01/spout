package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show run statistics",
	Long: `Show aggregate statistics across all runs.

  spout stats`,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, _ := resolveServer()

		runs, err := fetchRuns(addr)
		if err != nil {
			return fmt.Errorf("server %s: %s", cBold(addr), cRed(err.Error()))
		}

		if len(runs) == 0 {
			info("%s", cDim("no runs"))
			return nil
		}

		var activeRows, endedRows []runEntry
		for _, r := range runs {
			if isActive(r.Status) {
				activeRows = append(activeRows, r)
			} else {
				endedRows = append(endedRows, r)
			}
		}

		fmt.Fprintf(stderr, "\n")
		printStats(runs, activeRows, endedRows)
		fmt.Fprintf(stderr, "\n")
		return nil
	},
}

func init() {
	statsCmd.GroupID = groupSession
	rootCmd.AddCommand(statsCmd)
}
