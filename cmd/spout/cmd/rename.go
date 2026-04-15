package cmd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

var renameCmd = &cobra.Command{
	Use:               "rename <old> <new>",
	Short:             "Rename a run",
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		oldName, newName := args[0], args[1]
		addr, found := findRunServer(oldName)
		if !found {
			return fmt.Errorf("run %s not found on any server", cBold(oldName))
		}
		url := fmt.Sprintf("http://%s/api/run/%s/rename", addr, oldName)
		body := fmt.Sprintf(`{"name":%q}`, newName)
		resp, err := http.Post(url, "application/json", strings.NewReader(body))
		if err != nil {
			return fmt.Errorf("connecting to server: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("rename failed (status %d)", resp.StatusCode)
		}
		ok("%s %s → %s", cGreen("renamed"), cBold(oldName), cBold(newName))
		return nil
	},
}

func init() {
	renameCmd.GroupID = groupSession
	rootCmd.AddCommand(renameCmd)
}
