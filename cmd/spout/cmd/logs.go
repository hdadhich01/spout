package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:               "logs <name>",
	Short:             "Print a run's output",
	Long: `Replay a run's terminal output to stdout. Includes ANSI colors.

  spout logs ember
  spout logs ember | less -R
  spout logs ember > output.txt`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, found := findRunServer(args[0])
		if !found {
			return fmt.Errorf("run %s not found on any known server", cBold(args[0]))
		}
		url := fmt.Sprintf("http://%s/api/run/%s/raw", addr, args[0])
		resp, err := http.Get(url)
		if err != nil {
			return fmt.Errorf("fetching logs: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	},
}

func init() {
	logsCmd.GroupID = groupSession
	rootCmd.AddCommand(logsCmd)
}
