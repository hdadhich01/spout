package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login [profile]",
	Short: "Add or update a server profile",
	Long: `Interactively configure a server profile with URL and token.

  spout login work       # create/update the "work" profile
  spout login            # create/update the default server`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}

		// Load existing config first to check for conflicts.
		cfg := config.Load()

		// Warn if profile already exists.
		if profile != "" {
			if existing, ok := cfg.Servers[profile]; ok {
				warn("profile %s %s (%s)", cBold(profile), cYellow("already exists"), cAqua(existing.URL))
				if !confirm("overwrite?") {
					info("%s", cDim("cancelled"))
					return nil
				}
			}
		}

		// Get server URL.
		urlInput := askLine(cAqua(cBold("server url:")))
		if urlInput == "" {
			return fmt.Errorf("URL is required")
		}

		// Get token (optional).
		tokenInput := askLine(cAqua(cBold("token:")) + cDim(" (optional, enter to skip)"))

		if profile == "" {
			// Set as default server.
			cfg.DefaultServer = urlInput
		} else {
			// Add/update profile.
			if cfg.Servers == nil {
				cfg.Servers = make(map[string]config.Server)
			}
			cfg.Servers[profile] = config.Server{URL: urlInput}

			// Set as default if it's the only one or user has no default.
			if cfg.DefaultServer == "" || cfg.DefaultServer == config.DefaultRemoteHost {
				cfg.DefaultServer = profile
			}
		}

		// Write config.yaml.
		if err := config.WriteSystem(cfg); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		// Write token to .env if provided.
		if tokenInput != "" {
			envPath := filepath.Join(filepath.Dir(config.SystemPath()), ".env")
			if err := appendEnv(envPath, profile, tokenInput); err != nil {
				return fmt.Errorf("writing token: %w", err)
			}
		}

		if profile != "" {
			ok("%s profile %s", cGreen("saved"), cBold(profile))
			info("use with: %s", cAqua("spout -s "+profile))
		} else {
			ok("%s default server %s", cGreen("saved"), cAqua(urlInput))
		}
		info("config at %s", cAqua(shortPath(config.SystemPath())))
		return nil
	},
}

func appendEnv(path string, profile string, token string) error {
	key := "SPOUT_TOKEN"
	if profile != "" {
		key = "SPOUT_TOKEN_" + strings.ToUpper(profile)
	}

	// Read existing .env.
	existing, _ := os.ReadFile(path)
	lines := strings.Split(string(existing), "\n")

	// Replace if key exists, otherwise append.
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), key+"=") {
			lines[i] = key + "=" + token
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+"="+token)
	}

	content := strings.Join(lines, "\n")
	content = strings.TrimRight(content, "\n") + "\n"

	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)
	return os.WriteFile(path, []byte(content), 0600)
}

func init() {
	loginCmd.GroupID = groupSetup
	rootCmd.AddCommand(loginCmd)
}
