package cmd

import (
	"fmt"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/server"
	"github.com/spf13/cobra"
)

// tokenCmd groups the ingest-token management subcommands. Ingest tokens are
// the "send output" credential remote senders present; same-box use needs none.
// A running server picks up changes live (the store reloads on file change).
var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage ingest tokens (who may send output to this server)",
	Long: `Manage ingest tokens — the credential a remote sender presents to push
runs to this server. Same-box senders need none. Stored at ` + config.TokensPath() + `.

  spout server token create alice
  spout server token list
  spout server token revoke alice`,
}

var tokenCreateCmd = &cobra.Command{
	Use:   "create <label>",
	Short: "Mint a new ingest token",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ts := server.LoadTokenStore(config.TokensPath(), "")
		e, err := ts.Add(args[0])
		if err != nil {
			return err
		}
		ok("created token %s", cBold(e.Label))
		fmt.Fprintln(stderr)
		label("token", cAqua(e.Token))
		info("%s", cDim("the sender sets this as the env var named by servers[<profile>].token"))
		return nil
	},
}

var tokenListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List ingest tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		ts := server.LoadTokenStore(config.TokensPath(), "")
		entries := ts.List()
		if len(entries) == 0 {
			info("no ingest tokens — create one with %s", cAqua("spout server token create <label>"))
			return nil
		}
		section(fmt.Sprintf("tokens  %s", cDim(fmt.Sprintf("(%d)", len(entries)))))
		for _, e := range entries {
			label(e.Label, fmt.Sprintf("%s  %s", cDim(maskToken(e.Token)), cDim(e.Created.Format("2006-01-02"))))
		}
		return nil
	},
}

var tokenRevokeCmd = &cobra.Command{
	Use:     "revoke <label|token>",
	Aliases: []string{"rm", "delete"},
	Short:   "Revoke an ingest token",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ts := server.LoadTokenStore(config.TokensPath(), "")
		if err := ts.Revoke(args[0]); err != nil {
			return err
		}
		ok("revoked %s", cBold(args[0]))
		return nil
	},
}

// maskToken shows just enough of a token to recognize it without leaking it.
func maskToken(t string) string {
	if len(t) <= 12 {
		return t
	}
	return t[:8] + "…" + t[len(t)-4:]
}

func init() {
	tokenCmd.AddCommand(tokenCreateCmd, tokenListCmd, tokenRevokeCmd)
	serverCmd.AddCommand(tokenCmd)
}
