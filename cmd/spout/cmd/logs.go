package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [run] [stream]",
	Short: "Print a run's output from the local copy",
	Long: `Replay a run's output (ANSI colors preserved). Reads the local copy only.

  spout logs ember
  spout logs exp-2/training            # one stream
  spout logs ember | less -R`,
	Args:              cobra.RangeArgs(0, 2),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		local, _, err := openLocal()
		if err != nil {
			return err
		}

		if len(args) == 0 {
			picked, perr := pickLocalRun(local, "read")
			if perr != nil {
				return perr
			}
			args = []string{picked}
		}

		sess, err := resolveLocalSingle(local, args)
		if err != nil {
			return err
		}

		f, err := os.Open(local.DataPath(sess.Name))
		if err != nil {
			return fmt.Errorf("opening local copy: %w", err)
		}
		defer f.Close()
		_, err = io.Copy(os.Stdout, f)
		return err
	},
}

func init() {
	logsCmd.GroupID = groupSession
	rootCmd.AddCommand(logsCmd)
}
