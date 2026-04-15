package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var shareCmd = &cobra.Command{
	Use:   "share <run>",
	Short: "Copy a run's URL to the clipboard",
	Long: `Prints the full URL for a run so you can share it.

  spout share ember
  spout share ember-k9p1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		addr, found := findRunServer(name)
		if !found {
			warn("run %s %s on %s", cBold(name), cYellow("not found"), cAqua(addr))
		}
		url := fmt.Sprintf("http://%s/r/%s", addr, name)
		fmt.Println(url)
		copyToClipboard(url)
		ok("%s %s to clipboard", cGreen("copied"), cAqua(url))
		return nil
	},
}

func init() {
	shareCmd.GroupID = groupSession
	rootCmd.AddCommand(shareCmd)
}
