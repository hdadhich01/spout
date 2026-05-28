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
	Long: `Show which spout.yaml files are loaded and what they resolve to. Use 'spout login' or 'spout init' to create configs.

  spout config`,
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

		// --- sources: the tree of yaml + .env files that got merged ---
		section("sources")

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

				entry := prettyPath(dir) + files + tagStr

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

		// --- resolved: the actual values that will be used ---
		section("resolved")
		label("server", serverStatusLine(addr))

		// Token row. Tokens are explicit-only: if the matched profile
		// names an env var via `token:`, show it + its set/unset state;
		// otherwise show none.
		profile := ""
		if cfg.Servers != nil {
			for name, srv := range cfg.Servers {
				if srv.URL == addr {
					profile = name
					break
				}
			}
		}
		tokenVar := cfg.TokenVarFor(profile)
		switch {
		case tokenVar == "":
			label("token", cDim("none"))
		case token != "":
			label("token", fmt.Sprintf("%s  %s", cAqua(tokenVar), cGreen("set")))
		default:
			label("token", fmt.Sprintf("%s  %s", cAqua(tokenVar), cDim("unset")))
		}

		if cfg.Job != "" {
			label("job", cfg.Job)
		}
		if cfg.RunName != "" {
			label("run_name", cfg.RunName)
		}
		// Show only the local (CLI canonical) path. The server's storage
		// dir is intentionally omitted from CLI surfaces — it's a server
		// concern, surfaced by `spout server` when you start one.
		label("local", cAqua(shortPath(cfg.ResolveLocal())))
		if cfg.History != nil {
			label("history", fmt.Sprintf("%t", *cfg.History))
		}
		if cfg.Observe != nil {
			rules := len(cfg.Observe.Rules)
			state := cDim("off")
			if cfg.Observe.Enabled {
				state = cGreen("on")
			}
			label("observe", fmt.Sprintf("%s  %s  %d rule(s)", state, cfg.Observe.ModelType(), rules))
		}

		// --- streams: one section with column-aligned labels ---
		if len(cfg.Streams) > 0 {
			section(fmt.Sprintf("streams  %s", cDim(fmt.Sprintf("(%d)", len(cfg.Streams)))))
			maxLabel := 0
			for _, s := range cfg.Streams {
				if n := len(s.Label); n > maxLabel {
					maxLabel = n
				}
			}
			for _, s := range cfg.Streams {
				pad := strings.Repeat(" ", maxLabel-len(s.Label))
				fmt.Fprintf(stderr, "  %s%s  %s\n", cBold("["+s.Label+"]"), pad, cDim(s.Command))
			}
		}

		// --- profiles: name, url, token var (if any) ---
		if len(cfg.Servers) > 0 {
			section(fmt.Sprintf("profiles  %s", cDim(fmt.Sprintf("(%d)", len(cfg.Servers)))))
			maxName := 0
			for name := range cfg.Servers {
				if n := len(name); n > maxName {
					maxName = n
				}
			}
			for name, srv := range cfg.Servers {
				pad := strings.Repeat(" ", maxName-len(name))
				line := cAqua(srv.URL)
				if srv.Token != "" {
					line += "  " + cDim("→ "+srv.Token)
				}
				fmt.Fprintf(stderr, "  %s%s  %s\n", cBold(name), pad, line)
			}
		}

		// --- validation errors, only if any ---
		if err := cfg.Validate(); err != nil {
			section("invalid")
			fmt.Fprintf(stderr, "  %s %s\n", cRed("✗"), cRed(err.Error()))
		}

		// ctx reference kept for future use (git root hint, etc.)
		_ = ctx

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
		return fmt.Sprintf("%s  %s", addr, cRed("unreachable"))
	case authRequired:
		return fmt.Sprintf("%s  %s", addr, cYellow("auth required"))
	case ok:
		return fmt.Sprintf("%s  %s", addr, cGreen("compatible"))
	case code == 200:
		return fmt.Sprintf("%s  %s", addr, cRed("not compatible"))
	default:
		return fmt.Sprintf("%s  %s %d", addr, cYellow("status"), code)
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

// prettyPath renders an absolute path for human display AND terminal paste.
// It prefers a relative path (./, ../, ../../, ./sub/) when the target is
// on cwd's direct ancestor-or-descendant chain; otherwise falls back to
// shortPath ("~/..." or absolute). The return value always has a trailing
// slash when it represents a directory, so callers can append a filename.
//
// Examples (cwd = /home/alice/spout):
//   /home/alice/spout              -> ./
//   /home/alice/spout/test         -> ./test/
//   /home/alice                    -> ../
//   /                              -> ~/ or absolute (sibling path)
//   /home/alice/.config/spout      -> ~/.config/spout/  (sibling of cwd)
func prettyPath(p string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return shortPath(p) + "/"
	}
	sep := string(filepath.Separator)

	if p == cwd {
		return "./"
	}
	// Target is inside cwd.
	if strings.HasPrefix(p, cwd+sep) {
		rel, err := filepath.Rel(cwd, p)
		if err == nil {
			return "./" + rel + "/"
		}
	}
	// cwd is inside target (target is an ancestor).
	if strings.HasPrefix(cwd, p+sep) {
		rel, err := filepath.Rel(cwd, p)
		if err == nil {
			return rel + "/"
		}
	}
	// Sibling or unrelated - prefer ~/ form.
	return shortPath(p) + "/"
}

func init() {
	configCmd.GroupID = groupSetup
	rootCmd.AddCommand(configCmd)
}
