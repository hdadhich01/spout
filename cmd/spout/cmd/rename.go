package cmd

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var renameCmd = &cobra.Command{
	Use:   "rename [run|run/stream] [new-name]",
	Short: "Rename a run or a single stream",
	Long: `Rename a run or a single stream. Operates on the local copy; server-side rename needs auth (token). Run groups can't be renamed as a unit.

  spout rename old new
  spout rename old                     # prompts for new name
  spout rename                         # picker, then prompt
  spout rename exp-2/training newlabel # one stream`,
	Args:              cobra.RangeArgs(0, 2),
	ValidArgsFunction: completeTmuxSessions,
	RunE: func(cmd *cobra.Command, args []string) error {
		local, _, err := openLocal()
		if err != nil {
			return err
		}

		// First positional becomes the addressing piece for resolveLocalSingle;
		// last positional (if 2 args) is the new name. With 0 args, picker;
		// with 1 arg, prompt for new name.
		var addrPieces []string
		var newName string
		switch len(args) {
		case 0:
			picked, perr := pickLocalRun(local, "rename")
			if perr != nil {
				return perr
			}
			addrPieces = []string{picked}
		case 1:
			addrPieces = []string{args[0]}
		case 2:
			addrPieces = []string{args[0]}
			newName = args[1]
		}

		sess, err := resolveLocalSingle(local, addrPieces)
		if err != nil {
			return err
		}
		oldName := sess.Name

		if newName == "" {
			newName = strings.TrimSpace(askLine(fmt.Sprintf("  new name for %s: ", cBold(oldName))))
			if newName == "" {
				info("%s", cDim("cancelled"))
				return nil
			}
		}

		// Server-side rename is auth-gated. Try it first so a server failure
		// leaves the local copy at the old name (recoverable).
		if sess.ServerURL != "" {
			token := os.Getenv(sess.ServerToken)
			if sess.ServerToken != "" && token != "" {
				if err := renameRun(sess.ServerURL, oldName, newName, sess.ServerToken); err != nil {
					return fmt.Errorf("server rename: %w", err)
				}
				info("%s on %s", cDim("renamed"), cDim(sess.ServerURL))
			} else {
				info("%s — server runs need auth to rename", cDim("local only"))
			}
		}

		if err := local.Rename(oldName, newName); err != nil {
			return fmt.Errorf("local rename: %w", err)
		}
		ok("%s %s → %s", cGreen("renamed"), cBold(oldName), cBold(newName))
		return nil
	},
}

// renameRun POSTs the rename to the server's /api/run/<name>/rename. If
// tokenVar resolves to a non-empty env value, an Authorization header
// (Bearer ...) is attached.
func renameRun(addr, oldName, newName, tokenVar string) error {
	url := fmt.Sprintf("http://%s/api/run/%s/rename", addr, oldName)
	body := fmt.Sprintf(`{"name":%q}`, newName)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if tokenVar != "" {
		if v := os.Getenv(tokenVar); v != "" {
			req.Header.Set("Authorization", "Bearer "+v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func init() {
	renameCmd.GroupID = groupSession
	rootCmd.AddCommand(renameCmd)
}
