package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check your spout environment",
	Long: `Run a series of checks to diagnose your spout installation.

  spout doctor`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		section("checks")

		// Binary / version
		version, rev := versionInfo()
		binPath, _ := os.Executable()
		vstr := version
		if rev != "" {
			vstr += cDim(" ("+rev+")")
		}
		if binPath != "" {
			vstr += "  " + cDim(shortPath(binPath))
		}
		pass("version", vstr)

		// tmux
		if _, err := exec.LookPath("tmux"); err == nil {
			pass("tmux", cGreen("found"))
		} else {
			fail2("tmux", cRed("not found")+" - `spout run` won't work")
		}

		// Clipboard
		clipTool := detectClipboard()
		if clipTool != "" {
			pass("clipboard", clipTool+" "+cGreen("found"))
		} else {
			warn2("clipboard", cYellow("no tool found")+" - auto-copy disabled")
		}

		// Browser opener
		opener := detectBrowserOpener()
		if opener != "" {
			pass("browser", opener+" "+cGreen("found"))
		} else {
			warn2("browser", cYellow("no opener found")+" - `spout open` won't work")
		}

		// Config file
		sysPath := config.SystemPath()
		if _, err := os.Stat(sysPath); err == nil {
			pass("config", cAqua(shortPath(sysPath)))
		} else if os.IsNotExist(err) {
			warn2("config", cYellow("no system config")+" (run `spout login` to create one)")
		} else {
			fail2("config", cRed(err.Error()))
		}

		// Schema validation (foreign keys + model endpoint requirement)
		if err := cfg.Validate(); err != nil {
			fail2("schema", cRed(err.Error()))
		} else {
			pass("schema", cGreen("valid"))
		}

		// Server reachability + compatibility
		addr, _ := resolveServer()
		ok, reachable, authRequired, code := probeSpout(addr)
		switch {
		case !reachable:
			fail2("server", fmt.Sprintf("%s  %s", addr, cRed("unreachable")))
		case authRequired:
			warn2("server", fmt.Sprintf("%s  %s", addr, cYellow("auth required")))
		case ok:
			pass("server", fmt.Sprintf("%s  %s", addr, cGreen("compatible")))
		case code == 200:
			fail2("server", fmt.Sprintf("%s  %s", addr, cRed("not compatible")))
		default:
			warn2("server", fmt.Sprintf("%s  %s %d", addr, cYellow("status"), code))
		}

		// Token (only when the resolved server profile declares one,
		// since the plain SPOUT_TOKEN fallback is expected to be absent
		// on no-auth local dev).
		if profile := profileFor(cfg, addr); profile != "" {
			if srv, haveSrv := cfg.Servers[profile]; haveSrv && srv.Token != "" {
				if v := os.Getenv(srv.Token); v != "" {
					pass("token", cAqua(srv.Token)+" "+cGreen("set"))
				} else {
					warn2("token", cAqua(srv.Token)+" "+cYellow("unset")+" (set it in ~/.config/spout/.env)")
				}
			}
		}

		// Storage: path + writable + free disk
		storagePath := config.DefaultStorageDir()
		if cfg.Storage != "" {
			storagePath = expandHome(cfg.Storage)
		}
		if err := os.MkdirAll(storagePath, 0755); err != nil {
			fail2("storage", fmt.Sprintf("%s  %s", cAqua(shortPath(storagePath)), cRed(err.Error())))
		} else if !isWritable(storagePath) {
			fail2("storage", fmt.Sprintf("%s  %s", cAqua(shortPath(storagePath)), cRed("not writable")))
		} else {
			free := diskFree(storagePath)
			pass("storage", fmt.Sprintf("%s  %s free", cAqua(shortPath(storagePath)), cGreen(free)))
		}

		// Streams preflight - only when the merged config defines any.
		if n := len(cfg.Streams); n > 0 {
			issues := preflightStreams(cfg.Streams)
			if len(issues) == 0 {
				pass("streams", fmt.Sprintf("%d %s", n, cGreen("ready")))
			} else {
				fail2("streams", fmt.Sprintf("%d defined, %s", n, cRed(fmt.Sprintf("%d issue(s)", len(issues)))))
				for _, msg := range issues {
					fmt.Fprintf(stderr, "              %s %s\n", cRed("✗"), msg)
				}
			}
		}

		// Watch endpoint (only when watch is enabled with a local model)
		if cfg.Watch != nil && cfg.Watch.Enabled &&
			cfg.Watch.Model != nil && cfg.Watch.Model.Type == "local" &&
			cfg.Watch.Model.Endpoint != "" {
			ep := cfg.Watch.Model.Endpoint
			if probeWatchEndpoint(ep) {
				pass("watch", fmt.Sprintf("%s  %s", cAqua(ep), cGreen("reachable")))
			} else {
				fail2("watch", fmt.Sprintf("%s  %s", cAqua(ep), cRed("unreachable")))
			}
		}

		fmt.Fprintf(stderr, "\n")
		return nil
	},
}

// pass / warn2 / fail2 share the same layout. If the value already contains
// ANSI escapes the caller has done its own coloring and we print as-is;
// otherwise the whole value gets the severity hue.
func checkRow(mark, name, value string, hue func(string) string) {
	if !strings.Contains(value, "\x1b[") {
		value = hue(value)
	}
	fmt.Fprintf(stderr, "  %s %s  %s\n", mark, cAqua(cBold(rpad(name, labelWidth))), value)
}

func pass(name, value string)  { checkRow(cGreen("✓"), name, value, cGreen) }
func warn2(name, value string) { checkRow(cYellow("!"), name, value, cYellow) }
func fail2(name, value string) { checkRow(cRed("✗"), name, value, cRed) }

func rpad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	pad := ""
	for i := 0; i < n-len(s); i++ {
		pad += " "
	}
	return s + pad
}

func detectClipboard() string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("pbcopy"); err == nil {
			return "pbcopy"
		}
	case "windows":
		if _, err := exec.LookPath("clip"); err == nil {
			return "clip"
		}
	default:
		for _, t := range []string{"xclip", "xsel", "wl-copy"} {
			if _, err := exec.LookPath(t); err == nil {
				return t
			}
		}
	}
	return ""
}

func detectBrowserOpener() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "windows":
		return "rundll32"
	default:
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return "xdg-open"
		}
	}
	return ""
}

// versionInfo reads the module version + VCS revision from the Go build
// info block. For an untagged `go install` (pseudo-version like
// v0.0.0-20260415-6bd88d3076b6+dirty) we collapse the whole thing to
// "dev" and just show the commit hash - the pseudo-version is noise.
func versionInfo() (version, revision string) {
	version = "dev"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	v := info.Main.Version
	// Use the tag if it looks like a real semver release, otherwise "dev".
	if v != "" && v != "(devel)" && !strings.HasPrefix(v, "v0.0.0-") {
		version = v
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			revision = s.Value[:7]
			return
		}
	}
	return
}

// expandHome turns a leading ~ into $HOME.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// isWritable touches a probe file in dir to confirm the user can actually
// create files there. Tolerates races (another caller removing the probe).
func isWritable(dir string) bool {
	probe := filepath.Join(dir, ".spout-doctor-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}

// diskFree returns the free space at path as a human-readable string.
// Falls back to "?" if the syscall fails (some weird filesystems).
func diskFree(path string) string {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return "?"
	}
	return humanBytes(int64(stat.Bavail) * int64(stat.Bsize))
}

// profileFor returns the first server profile whose URL matches addr.
// Empty string if no profile is in play (raw host:port, or no servers map).
func profileFor(cfg config.Config, addr string) string {
	for name, srv := range cfg.Servers {
		if srv.URL == addr {
			return name
		}
	}
	return ""
}

// preflightStreams runs quick cheap checks on every stream and returns
// one issue string per problem found. A stream with no issues produces
// nothing; preflight passes overall if the returned slice is empty.
//
//   - `dir:` must exist (if specified) so `cd` inside the stream won't fail
//   - first token of `command:` must be on PATH so the user catches typos
//     before tmux spins up N empty panes
func preflightStreams(streams []config.Stream) []string {
	var issues []string
	for _, s := range streams {
		dir := config.ResolveStreamDir(s)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			issues = append(issues, fmt.Sprintf("[%s] dir %s does not exist",
				cBold(s.Label), cAqua(shortPath(dir))))
			continue
		}
		bin := firstToken(s.Command)
		if bin == "" {
			continue
		}
		// Skip binaries given with an explicit path or shell keywords -
		// LookPath only checks PATH, it'd false-positive on these.
		if strings.ContainsAny(bin, "/") {
			if _, err := os.Stat(bin); err != nil {
				issues = append(issues, fmt.Sprintf("[%s] %s not found",
					cBold(s.Label), cAqua(bin)))
			}
			continue
		}
		if isShellKeyword(bin) {
			continue
		}
		if _, err := exec.LookPath(bin); err != nil {
			issues = append(issues, fmt.Sprintf("[%s] %s not on PATH",
				cBold(s.Label), cAqua(bin)))
		}
	}
	return issues
}

// firstToken returns the first whitespace-separated word from a command
// string. Good enough for typo detection; it won't handle quoted binaries
// with embedded spaces, but that's rare and a false-positive just surfaces
// as a doctor warning (not an execution block).
func firstToken(s string) string {
	s = strings.TrimLeft(s, " \t")
	for i, c := range s {
		if c == ' ' || c == '\t' {
			return s[:i]
		}
	}
	return s
}

// isShellKeyword returns true for builtins/flow-control that LookPath
// would fail on but which are perfectly valid at the start of a stream
// command (e.g. `while true; do ...`).
func isShellKeyword(tok string) bool {
	switch tok {
	case "while", "for", "if", "case", "until", "(":
		return true
	}
	return false
}

// probeWatchEndpoint does a best-effort reachability check on a local LLM
// endpoint. Any HTTP response (including 404) counts as "something is
// listening"; only connection / DNS failures count as unreachable.
func probeWatchEndpoint(url string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func init() {
	doctorCmd.GroupID = groupSetup
	rootCmd.AddCommand(doctorCmd)
}
