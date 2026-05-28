package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "history"},
	Short:   "Browse all runs and stats",
	Long: `List all runs from the server. Long lists open in a pager.

  spout ls
  spout list
  spout history`,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, _ := resolveServer()

		runs, err := fetchRuns(addr)
		if err != nil {
			return fmt.Errorf("server %s: %s", cBold(addr), cRed(err.Error()))
		}

		if len(runs) == 0 {
			fmt.Fprintf(stderr, "%s\n", cDim("no runs"))
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

		withPager(func() {
			if len(activeRows) > 0 {
				section("active")
				printTable(activeRows)
			}
			if len(endedRows) > 0 {
				section("ended")
				printTable(endedRows)
			}
			fmt.Fprintf(stderr, "\n")
		})

		return nil
	},
}

// printStats shows aggregate stats.
func printStats(all, active, ended []runEntry) {
	var totalDur, totalLines, totalBytes int64
	var errored int
	var pipeCount, runCount int

	for _, r := range all {
		totalDur += r.DurationMs
		totalLines += r.Lines
		totalBytes += r.Bytes
		if r.Mode == "run" {
			runCount++
		} else {
			pipeCount++
		}
		if r.Status == "error" {
			errored++
		}
	}

	avgDur := int64(0)
	if len(all) > 0 {
		avgDur = totalDur / int64(len(all))
	}

	label("total", fmt.Sprintf("%d (%d active, %d ended)", len(all), len(active), len(ended)))
	label("modes", fmt.Sprintf("%d run, %d pipe", runCount, pipeCount))
	label("avg", humanDuration(avgDur))
	label("uptime", humanDuration(totalDur))
	label("lines", fmt.Sprintf("%d", totalLines))
	label("data", humanBytes(totalBytes))
	if errored > 0 {
		label("errors", cRed(fmt.Sprintf("%d", errored)))
	}
}

func init() {
	lsCmd.GroupID = groupSession
	rootCmd.AddCommand(lsCmd)
}
