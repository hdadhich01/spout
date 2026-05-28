package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var shareCmd = &cobra.Command{
	Use:   "share [run] [stream]",
	Short: "Show a run's URL, local path, and streaming status",
	Long: `Print the run URL + local path, copy to clipboard. Falls back to a server lookup when there's no local copy.

  spout share
  spout share ember
  spout share exp-2/training`,
	Args:              cobra.RangeArgs(0, 2),
	ValidArgsFunction: completeTmuxSessions,
	RunE:              runShare,
}

func runShare(cmd *cobra.Command, args []string) error {
	local, _, err := openLocal()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		picked, perr := pickLocalRun(local, "share")
		if perr != nil {
			return perr
		}
		args = []string{picked}
	}

	sess, lerr := resolveLocalSingle(local, args)
	if lerr != nil {
		// Fallback: no local copy. Look up on the configured server so a
		// fresh checkout / new machine can still get a URL.
		return shareViaServer(args)
	}

	serverAddr := sess.ServerURL
	if serverAddr == "" {
		serverAddr, _ = resolveServer()
	}
	runURL := fmt.Sprintf("http://%s/r/%s", serverAddr, sess.Name)
	fmt.Println(runURL)
	copyToClipboard(runURL)
	ok("%s %s to clipboard", cGreen("copied"), cAqua(runURL))

	label("local", cDim(shortPath(local.SessionDir(sess.Name))))

	_, active, reachable := sessionStatus(serverAddr, sess.Name)
	switch {
	case !reachable:
		label("streaming", cDim("unknown"))
	case active:
		label("streaming", cGreen("yes"))
	default:
		label("streaming", cDim("no"))
	}
	return nil
}

// shareViaServer is the no-local-copy fallback: ask the resolved server
// for the run, render its URL + streaming status. Used when the user is
// on a fresh machine or has cleaned their local archive.
func shareViaServer(pieces []string) error {
	addr, _ := resolveServer()
	if err := checkServer(addr); err != nil {
		return err
	}

	// Walk the run list once; that's the same call attach/kill/open make.
	// Match exact name first, then run-group, then label.
	name := strings.Join(pieces, "-") // "exp-2 training" -> "exp-2-training"
	if len(pieces) == 1 && strings.Contains(pieces[0], "/") {
		name = strings.Replace(pieces[0], "/", "-", 1)
	}

	for _, r := range fetchRunsIgnoreErr(addr) {
		if r.Name == name {
			runURL := fmt.Sprintf("http://%s/r/%s", addr, r.Name)
			fmt.Println(runURL)
			copyToClipboard(runURL)
			ok("%s %s to clipboard", cGreen("copied"), cAqua(runURL))
			label("local", cDim("not on this machine"))
			if r.Status == "streaming" || r.Status == "awaiting" {
				label("streaming", cGreen("yes"))
			} else {
				label("streaming", cDim("no"))
			}
			return nil
		}
	}
	return fmt.Errorf("no run named %s on %s", cBold(name), cBold(addr))
}

func init() {
	shareCmd.GroupID = groupSession
	rootCmd.AddCommand(shareCmd)
}
