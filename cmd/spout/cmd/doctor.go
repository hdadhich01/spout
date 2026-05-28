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

		// --- setup: install + system tools + project config file ---
		var setup []row

		version, rev := versionInfo()
		binPath, _ := os.Executable()
		vstr := version
		if rev != "" {
			vstr += cDim(" (" + rev + ")")
		}
		if binPath != "" {
			vstr += "  " + cDim(shortPath(binPath))
		}
		setup = append(setup, row{markOk(), "version", vstr})

		if _, err := exec.LookPath("tmux"); err == nil {
			setup = append(setup, row{markOk(), "tmux", cGreen("found")})
		} else {
			setup = append(setup, row{markFail(), "tmux", cRed("not found") + cDim(" (`spout run` won't work)")})
		}

		if t := detectClipboard(); t != "" {
			setup = append(setup, row{markOk(), "clipboard", t + " " + cGreen("found")})
		} else {
			setup = append(setup, row{markWarn(), "clipboard", cYellow("no tool found") + cDim(" (auto-copy disabled)")})
		}

		if t := detectBrowserOpener(); t != "" {
			setup = append(setup, row{markOk(), "browser", t + " " + cGreen("found")})
		} else {
			setup = append(setup, row{markWarn(), "browser", cYellow("no opener found") + cDim(" (`spout open` won't work)")})
		}

		sysPath := config.SystemPath()
		switch _, err := os.Stat(sysPath); {
		case err == nil:
			setup = append(setup, row{markOk(), "config", cAqua(shortPath(sysPath))})
		case os.IsNotExist(err):
			setup = append(setup, row{markWarn(), "config", cYellow("no system config") + cDim(" (run `spout login` to create one)")})
		default:
			setup = append(setup, row{markFail(), "config", cRed(err.Error())})
		}

		if err := cfg.Validate(); err != nil {
			setup = append(setup, row{markFail(), "schema", cRed(err.Error())})
		} else {
			setup = append(setup, row{markOk(), "schema", cGreen("valid")})
		}

		section("setup")
		printRows(setup)

		// --- server: reachability, optional auth, local copy path ---
		var server []row
		addr, _ := resolveServer()
		ok, reachable, authRequired, code := probeSpout(addr)
		switch {
		case !reachable:
			server = append(server, row{markFail(), "url", fmt.Sprintf("%s  %s", addr, cRed("unreachable"))})
		case authRequired:
			server = append(server, row{markWarn(), "url", fmt.Sprintf("%s  %s", addr, cYellow("auth required"))})
		case ok:
			server = append(server, row{markOk(), "url", fmt.Sprintf("%s  %s", addr, cGreen("compatible"))})
		case code == 200:
			server = append(server, row{markFail(), "url", fmt.Sprintf("%s  %s", addr, cRed("not compatible"))})
		default:
			server = append(server, row{markWarn(), "url", fmt.Sprintf("%s  %s %d", addr, cYellow("status"), code)})
		}

		if profile := profileFor(cfg, addr); profile != "" {
			if srv, haveSrv := cfg.Servers[profile]; haveSrv && srv.Token != "" {
				if v := os.Getenv(srv.Token); v != "" {
					server = append(server, row{markOk(), "token", cAqua(srv.Token) + " " + cGreen("set")})
				} else {
					server = append(server, row{markWarn(), "token", cAqua(srv.Token) + " " + cYellow("unset") + cDim(" (set it in ~/.config/spout/.env)")})
				}
			}
		}

		localPath := cfg.ResolveLocal()
		if err := os.MkdirAll(localPath, 0755); err != nil {
			server = append(server, row{markFail(), "local", fmt.Sprintf("%s  %s", cAqua(shortPath(localPath)), cRed(err.Error()))})
		} else if !isWritable(localPath) {
			server = append(server, row{markFail(), "local", fmt.Sprintf("%s  %s", cAqua(shortPath(localPath)), cRed("not writable"))})
		} else {
			server = append(server, row{markOk(), "local", cAqua(shortPath(localPath))})
		}

		section("server")
		printRows(server)

		// --- streams: per-stream preflight (only when configured) ---
		if n := len(cfg.Streams); n > 0 {
			issues := preflightStreams(cfg.Streams)
			countStr := fmt.Sprintf("%d", n)
			if len(issues) > 0 {
				countStr = fmt.Sprintf("%d, %s", n, cRed(fmt.Sprintf("%d issue(s)", len(issues))))
			}
			section(fmt.Sprintf("streams  %s", cDim("("+countStr+")")))
			printRows(streamRows(cfg.Streams, issues))
		}

		// --- observe: only when enabled with a local model ---
		if cfg.Observe != nil && cfg.Observe.Enabled && cfg.Observe.ModelType() == "local" {
			ep := cfg.Observe.Endpoint()
			section("observe")
			if probeObserveEndpoint(ep) {
				printRows([]row{{markOk(), "endpoint", fmt.Sprintf("%s  %s", cAqua(ep), cGreen("reachable"))}})
			} else {
				printRows([]row{{markFail(), "endpoint", fmt.Sprintf("%s  %s", cAqua(ep), cRed("unreachable"))}})
			}
		}

		fmt.Fprintf(stderr, "\n")
		return nil
	},
}

// streamRows turns the merged config's streams + the preflight issue list
// into a row per stream, marking only the failing ones with ✗. The
// per-stream issue text (`dir … does not exist` etc.) becomes the value.
func streamRows(streams []config.Stream, issues []string) []row {
	// Index issues by stream label. preflightStreams emits messages of the
	// form "[<label>] <body>" — pull <label> back out so we can match.
	issueFor := map[string]string{}
	for _, m := range issues {
		if !strings.HasPrefix(m, "[") {
			continue
		}
		end := strings.Index(m, "]")
		if end < 0 {
			continue
		}
		// Strip ANSI from the label component (preflight wraps it in cBold).
		labelANSI := m[1:end]
		labelClean := stripANSI(labelANSI)
		body := strings.TrimSpace(m[end+1:])
		issueFor[labelClean] = body
	}
	out := make([]row, 0, len(streams))
	for _, s := range streams {
		if body, bad := issueFor[s.Label]; bad {
			out = append(out, row{markFail(), s.Label, body})
		} else {
			out = append(out, row{markOk(), s.Label, cDim("ready")})
		}
	}
	return out
}

// stripANSI removes CSI escape sequences (`\x1b[…<final>`) so we can
// match raw labels back to their preflight messages. Cheap version —
// good enough for the colors color.go emits.
func stripANSI(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			i++ // ESC
			if i < len(s) && s[i] == '[' {
				i++ // [
			}
			// Consume params until the final byte (0x40..0x7e).
			for i < len(s) {
				c := s[i]
				i++
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}
			continue
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
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

// probeObserveEndpoint does a best-effort reachability check on a local LLM
// endpoint. Any HTTP response (including 404) counts as "something is
// listening"; only connection / DNS failures count as unreachable.
func probeObserveEndpoint(url string) bool {
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
