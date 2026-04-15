package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show resolved config",
	Long: `Show what files are loaded and what they resolve to.

  spout config

To create or edit configs:
  spout login            edit ~/.config/spout/spout.yaml
  spout init             create a spout.yaml in the current directory`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		addr, token := cfg.Resolve(serverAddr)
		if localMode {
			addr = config.DefaultLocalAddr()
			token = ""
		}

		ctx := config.DiscoverContext()
		yamls, envs := config.LoadedFiles()
		sysDir := filepath.Dir(config.SystemPath())

		// --- loaded: single tree, indented by depth, global at bottom ---
		fmt.Fprintf(stderr, "\n  %s\n", "all configs merged; deeper files take precedence")
		section("loaded")

		if len(yamls) == 0 && len(envs) == 0 {
			fmt.Fprintf(stderr, "  %s\n", cDim("(none - using defaults)"))
		} else {
			// Group yaml+env by directory into single lines.
			type dirInfo struct {
				yaml bool
				env  bool
				tag  string
			}

			dirMap := map[string]*dirInfo{}
			var dirOrder []string

			addDir := func(p string) {
				dir := filepath.Dir(p)
				if _, ok := dirMap[dir]; !ok {
					d := &dirInfo{}
					if dir == sysDir {
						d.tag = "global"
					} else if ctx.IsGitDir && dir == ctx.Ceiling {
						d.tag = "git root"
					} else if dir == ctx.Cwd {
						d.tag = "cwd"
					}
					dirMap[dir] = d
					dirOrder = append(dirOrder, dir)
				}
				if filepath.Base(p) == ".env" {
					dirMap[dir].env = true
				} else {
					dirMap[dir].yaml = true
				}
			}

			for _, p := range yamls {
				addDir(p)
			}
			for _, p := range envs {
				addDir(p)
			}

			// Sort: cwd first, then parent dirs walking up, global last.
			// Use a rank: global = 9999 (always last), others ranked by
			// how many path segments from ceiling (more segments = closer
			// to cwd = higher rank = earlier in list).
			rank := func(dir string) int {
				if dir == sysDir {
					return -1 // global always last
				}
				return strings.Count(dir, string(filepath.Separator))
			}
			for i := 0; i < len(dirOrder); i++ {
				for j := i + 1; j < len(dirOrder); j++ {
					if rank(dirOrder[j]) > rank(dirOrder[i]) {
						dirOrder[i], dirOrder[j] = dirOrder[j], dirOrder[i]
					}
				}
			}

			// Print like tree but upward: deepest (highest precedence) at
			// top with most indent, global root at bottom with no indent.
			// Each level indents one step right with │ connectors.
			//
			// Example with 3 files:
			//       └── ~/spout/test/spout.yaml (cwd)
			//   └── ~/spout/spout.yaml + .env (git root)
			//   ~/.config/spout/spout.yaml + .env (global)
			//
			// dirOrder is deepest-first from LoadedFiles - that's the
			// order we want (deepest printed first at top).

			n := len(dirOrder)
			for i, dir := range dirOrder {
				d := dirMap[dir]
				files := cAqua("spout.yaml")
				if d.yaml && d.env {
					files = cAqua("spout.yaml") + cDim(" + ") + cAqua(".env")
				} else if d.env && !d.yaml {
					files = cAqua(".env")
				}
				tagStr := ""
				if d.tag != "" {
					tagStr = " " + cDim("("+d.tag+")")
				}

				entry := shortPath(dir) + "/" + files + tagStr

				// Indent level: deepest = n-1, global = 0.
				indent := n - 1 - i

				if indent == 0 {
					// Root (global) - no connector.
					fmt.Fprintf(stderr, "  %s\n", entry)
				} else {
					pad := strings.Repeat("    ", indent-1)
					fmt.Fprintf(stderr, "  %s%s %s\n", pad, cDim("┌──"), entry)
				}
			}
		}

		// --- context ---
		section("context")
		if ctx.IsGitDir {
			label("repo", shortPath(ctx.Ceiling)+"  "+cDim("(git)"))
		} else {
			label("repo", cDim("none - walking up to ~/"))
		}

		// --- resolved ---
		section("resolved")
		label("server", serverStatusLine(addr))
		if token != "" {
			masked := token
			if len(masked) > 8 {
				masked = masked[:8] + "…"
			}
			label("token", masked)
		} else {
			label("token", cDim("none"))
		}
		if cfg.Name != "" {
			label("name", cfg.Name)
		}
		if len(cfg.Runs) > 0 {
			label("runs", fmt.Sprintf("%d sub-runs", len(cfg.Runs)))
			for _, r := range cfg.Runs {
				fmt.Fprintf(stderr, "            [%s] %s\n", r.Label, cDim(r.Command))
			}
		}

		if len(cfg.Servers) > 0 {
			section("profiles")
			for name, srv := range cfg.Servers {
				label(name, srv.URL)
			}
		}

		fmt.Fprintf(stderr, "\n")
		return nil
	},
}

// serverStatusLine renders "addr - reason" with only the reason colored,
// same shape as the `spout doctor` server row, so `spout config` and
// `spout doctor` read consistently.
func serverStatusLine(addr string) string {
	if addr == "" {
		return cDim("none configured")
	}
	ok, reachable, authRequired, code := probeSpout(addr)
	switch {
	case !reachable:
		return fmt.Sprintf("%s - %s", addr, cRed("unreachable"))
	case authRequired:
		return fmt.Sprintf("%s - %s", addr, cYellow("auth required"))
	case ok:
		return fmt.Sprintf("%s - %s", addr, cGreen("spout-compatible"))
	case code == 200:
		return fmt.Sprintf("%s - %s", addr, cRed("not spout-compatible"))
	default:
		return fmt.Sprintf("%s - %s %d", addr, cYellow("status"), code)
	}
}

// shortPath collapses the absolute $HOME prefix to "~" for readability:
//   /home/alice/.config/spout/spout.yaml  ->  ~/.config/spout/spout.yaml
// Paths that aren't under $HOME are returned verbatim (e.g. /etc/..., /tmp,
// or wherever a non-standard user setup lives).
func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	if p == home {
		return "~"
	}
	return "~" + p[len(home):]
}

func init() {
	configCmd.GroupID = groupSetup
	rootCmd.AddCommand(configCmd)
}
