package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/names"
	"github.com/spf13/cobra"
)

var (
	runInto string // --into <run>: add a stream to an existing run
	runDir  string // --dir <path>: working directory for the new run/stream
	runYes  bool   // -y / --yes: auto-replace on name collision (no prompt)
)

var runCmd = &cobra.Command{
	Use:   "run [flags] [command [args...]]",
	Short: "Start a run, or add a stream to an existing one",
	Long: `Start a detached run, or add a stream to an existing one with --into. Spout flags must come BEFORE the command.

  spout run                            # interactive shell (or streams: blueprint)
  spout run python train.py
  spout run -n training python train.py
  spout run --dir /tmp python train.py
  spout run --into exp-1 tensorboard   # add a stream to a running run
  spout run -- mycmd --my-flag         # -- separates spout flags from cmd`,
	Args:               cobra.MinimumNArgs(0),
	RunE:               runCommand,
	DisableFlagParsing: false,
}

func init() {
	// Flags before positional args only - everything after the first
	// non-flag arg is treated as part of the user command.
	runCmd.Flags().SetInterspersed(false)
	runCmd.Flags().StringVar(&runInto, "into", "", "add a stream to an existing run")
	runCmd.Flags().StringVar(&runDir, "dir", "", "working directory")
	runCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip prompts")
	runCmd.GroupID = groupStream
	rootCmd.AddCommand(runCmd)
}

func runCommand(cmd *cobra.Command, args []string) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found — install with `apt install tmux` or `brew install tmux`")
	}

	// --into mode: add a stream to an existing run instead of starting a new one.
	if runInto != "" {
		if len(args) == 0 {
			return fmt.Errorf("--into needs a command to run")
		}
		return runIntoExisting(args)
	}

	// No args + a streams: blueprint -> launch the multi-stream run.
	if len(args) == 0 {
		cfg := config.Load()
		if len(cfg.Streams) > 0 {
			return runStreams(cfg)
		}
	}

	interactive := len(args) == 0

	if !interactive {
		// Preflight: verify the binary exists on PATH so obvious typos fail
		// before we spin up a tmux session and server record.
		if _, err := exec.LookPath(args[0]); err != nil {
			return fmt.Errorf("command not found: %s", cBold(args[0]))
		}
	}

	userChoseName := sessionName != ""
	if sessionName == "" {
		sessionName = names.Generate()
	}

	var userCmd string
	if !interactive {
		userCmd = shellQuote(args)
		if err := validateCommand(userCmd); err != nil {
			return err
		}
	}

	spoutBin, err := os.Executable()
	if err != nil {
		spoutBin = "spout"
	}

	addr, _ := resolveServer()
	if err := checkServer(addr); err != nil {
		return err
	}
	if err := resolveSessionName(addr, &sessionName, userChoseName, runYes); err != nil {
		return err
	}

	preCreate(addr, sessionName, userCmd)

	// Display the full run name everywhere (e.g. "chaos-3es9"), so the
	// attach/kill hints are copy-pasteable and match the URL. Resolution
	// still accepts the short word ("chaos") via prefix match.
	word := sessionName
	runURL := "http://" + addr + "/r/" + sessionName

	if interactive {
		// For an interactive shell we wrap the pane's first process in a
		// tiny inline script: print a colored banner, then `exec $SHELL`
		// so the user drops into a normal login-ish shell. Using a wrapper
		// (rather than `send-keys printf`) avoids the shell echoing the
		// `printf '...'` line itself when it ran, which looked messy and
		// rendered without ANSI because the echoed text is literal.
		//
		// ANSI truecolor codes here MUST match color.go:
		//   aqua  = \033[38;2;90;200;226m
		//   green = \033[38;2;34;197;94m
		//   dim   = \033[2m
		bannerScript := fmt.Sprintf(
			`printf '\n  \033[38;2;90;200;226mspout: \033[0mrun \033[1m%s\033[0m \033[38;2;34;197;94mstarted\033[0m\n  \033[38;2;90;200;226mspout: \033[0mdashboard at \033[38;2;90;200;226m%s\033[0m\n\n  \033[2mdetach:\033[0m Ctrl+b d\n\n'; exec $SHELL`,
			word, runURL)
		// -x 200 -y 50 forces a 200x50 virtual terminal while tmux is
		// detached. Without it, tmux defaults to ~80x24 and any output
		// produced before the user attaches would wrap at that width;
		// content captured to the dashboard would be hard-wrapped to a
		// narrow terminal forever. tmux reflows on attach to the user's
		// real size — a one-time visual flicker that's preferable to
		// permanently clipped/wrapped output.
		newArgs := []string{"new-session", "-d", "-s", sessionName, "-x", config.DefaultTerminalCols, "-y", config.DefaultTerminalRows}
		if runDir != "" {
			newArgs = append(newArgs, "-c", runDir)
		}
		newArgs = append(newArgs, "sh", "-c", bannerScript)
		if err := exec.Command("tmux", newArgs...).Run(); err != nil {
			return fmt.Errorf("creating run: %w", err)
		}

		pipePaneCmd := fmt.Sprintf("%s _stream --server %s --session %s", spoutBin, addr, sessionName)
		exec.Command("tmux", "pipe-pane", "-t", sessionName, pipePaneCmd).Run()

		copyToClipboard(runURL)

		// Attach (blocks until detach or session exit).
		attach := exec.Command("tmux", "attach-session", "-t", sessionName)
		attach.Stdin = os.Stdin
		attach.Stdout = os.Stdout
		attach.Stderr = os.Stderr
		attach.Run()

		fmt.Fprintln(os.Stderr)
		ok("%s run %s", cGreen("detached"), cBold(word))
		info("dashboard at %s", cAqua(runURL))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  %s  spout attach %s\n", cDim("attach:"), word)
		fmt.Fprintf(os.Stderr, "  %s    spout ls\n", cDim("list:"))
		fmt.Fprintf(os.Stderr, "  %s    spout kill %s\n", cDim("kill:"), word)
		fmt.Fprintln(os.Stderr)

		return nil
	}

	// Non-interactive: holding shell first, then pipe-pane, then send-keys.
	// Order matters - pipe-pane must be wired before the user's command
	// runs so fast-failing commands don't lose their exit marker.
	// -x 200 -y 50: see comment in the interactive branch above.
	newArgs := []string{"new-session", "-d", "-s", sessionName, "-x", config.DefaultTerminalCols, "-y", config.DefaultTerminalRows}
	if runDir != "" {
		newArgs = append(newArgs, "-c", runDir)
	}
	if err := exec.Command("tmux", newArgs...).Run(); err != nil {
		return fmt.Errorf("creating run: %w", err)
	}

	pipePaneCmd := fmt.Sprintf("%s _stream --server %s --session %s", spoutBin, addr, sessionName)
	if err := exec.Command("tmux", "pipe-pane", "-t", sessionName, pipePaneCmd).Run(); err != nil {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		return fmt.Errorf("wiring stream: %w", err)
	}

	// Respawn the holding shell with the wrapper as the pane's command.
	// send-keys would have the shell echo the whole typed line (including
	// our `; printf …; exec $SHELL` plumbing), which leaks into the stream
	// and confuses users. respawn-pane runs the wrapper directly, so the
	// pane shows only the user's command output, then a fresh shell prompt.
	shellCmd := fmt.Sprintf("%s; printf '\\033]9999;%%d\\007' $?; exec $SHELL", userCmd)
	if err := exec.Command("tmux", "respawn-pane", "-k", "-t", sessionName, shellCmd).Run(); err != nil {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		return fmt.Errorf("starting command: %w", err)
	}

	if failed, exitCode := waitForFastFail(addr, sessionName, 1500*time.Millisecond); failed {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		deleteRun(addr, sessionName, resolveServerTokenVar())
		fail("%s %s %d on startup", cBold(args[0]), cRed("exited"), exitCode)
		fmt.Fprintf(os.Stderr, "\n  re-run directly to see the error:  %s\n", cAqua(strings.Join(args, " ")))
		return ErrAlreadyReported
	}

	ok("%s run %s", cGreen("started"), cBold(word))
	info("dashboard at %s", cAqua(runURL))
	copyToClipboard(runURL)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s  spout attach %s\n", cDim("attach:"), word)
	fmt.Fprintf(os.Stderr, "  %s    spout ls\n", cDim("list:"))
	fmt.Fprintf(os.Stderr, "  %s    spout kill %s\n", cDim("kill:"), word)
	fmt.Fprintln(os.Stderr)

	return nil
}

// runIntoExisting attaches a new stream (pane or window) to an already-
// running multi-stream run. The label comes from -n / --name when set,
// otherwise from the basename of the user's command. This is the
// `spout run --into <run> cmd...` path; it replaces the older standalone
// `spout add-stream` command.
func runIntoExisting(cmdArgs []string) error {
	runName := runInto

	// The tmux session has to be on this machine — we can only manipulate
	// local tmux. Runs on other machines are read-only here.
	if err := exec.Command("tmux", "has-session", "-t", runName).Run(); err != nil {
		return fmt.Errorf("run %s isn't running on this machine", cBold(runName))
	}

	// Pick a label: -n wins, otherwise derive from the command basename.
	label := strings.TrimSpace(sessionName)
	if label == "" {
		label = deriveLabel(cmdArgs[0])
	}
	if label == "" {
		return fmt.Errorf("couldn't derive a stream name from %q (use -n)", cmdArgs[0])
	}
	if !validLabel(label) {
		return fmt.Errorf("stream name %q must be letters, digits, '-', or '_'", label)
	}

	// Don't collide with an existing pane label in this run.
	if findPaneIDByLabel(runName, label) != "" {
		return fmt.Errorf("stream %s already exists in run %s", cBold(label), cBold(runName))
	}

	streamName := streamSessionName(runName, label)

	addr, _ := resolveServer()
	if err := checkServer(addr); err != nil {
		return err
	}
	if sessionExists(addr, streamName) {
		return fmt.Errorf("server already has %s — use a different stream name", cBold(streamName))
	}

	// Only allow adding to runs that are already multi-stream. Promoting a
	// standalone single-stream run would create inconsistent URLs (original
	// at /r/<run>, new at /r/<run>-<label>, no shared grouping).
	job, isGroup := lookupMultiStreamRun(addr, runName)
	if !isGroup {
		return fmt.Errorf("run %s isn't multi-stream — start a new run with a streams: yaml to add streams", cBold(runName))
	}

	s := config.Stream{
		Label:   label,
		Command: shellQuote(cmdArgs),
		Dir:     runDir,
	}
	preCreateStream(addr, job, runName, s)

	// Mirror runStreams' layout decision: existing >1 windows -> windows
	// mode, else split-window in the existing window.
	useWindows := windowCount(runName) > 1

	spoutBin, err := os.Executable()
	if err != nil {
		spoutBin = "spout"
	}
	tokenVar := resolveServerTokenVar()

	var paneTarget string
	if useWindows {
		newWinArgs := []string{"new-window", "-t", runName, "-n", label}
		if runDir != "" {
			newWinArgs = append(newWinArgs, "-c", runDir)
		}
		if err := exec.Command("tmux", newWinArgs...).Run(); err != nil {
			deleteRun(addr, streamName, tokenVar)
			return fmt.Errorf("creating window: %w", err)
		}
		paneTarget = runName + ":."
	} else {
		splitArgs := []string{"split-window", "-t", runName}
		if runDir != "" {
			splitArgs = append(splitArgs, "-c", runDir)
		}
		if err := exec.Command("tmux", splitArgs...).Run(); err != nil {
			deleteRun(addr, streamName, tokenVar)
			return fmt.Errorf("splitting pane: %w", err)
		}
		paneTarget = runName + ":."
	}

	// Mark the pane (title + user option) so attach/kill can find it later.
	exec.Command("tmux", "select-pane", "-t", paneTarget, "-T", label).Run()
	exec.Command("tmux", "set-option", "-t", paneTarget, "-p", "@spout-label", label).Run()

	pipeCmd := fmt.Sprintf("%s _stream --server %s --session %s", spoutBin, addr, streamName)
	if err := exec.Command("tmux", "pipe-pane", "-t", paneTarget, pipeCmd).Run(); err != nil {
		return fmt.Errorf("wiring stream: %w", err)
	}

	// See the comment above the respawn-pane in `spout run`: this avoids the
	// shell echoing our exit-marker wrapper into the stream.
	shellCmd := buildStreamShellCmd(s)
	if err := exec.Command("tmux", "respawn-pane", "-k", "-t", paneTarget, shellCmd).Run(); err != nil {
		return fmt.Errorf("sending command: %w", err)
	}

	if !useWindows {
		exec.Command("tmux", "select-layout", "-t", runName, "tiled").Run()
	}

	runURL := "http://" + addr + "/r/" + streamName
	ok("%s stream %s in run %s", cGreen("added"), cBold(label), cBold(runName))
	info("dashboard at %s", cAqua(runURL))
	copyToClipboard(runURL)
	return nil
}

// deriveLabel turns a command basename into a usable stream label.
// Strips the path prefix and common script extensions.
func deriveLabel(arg0 string) string {
	if i := strings.LastIndex(arg0, "/"); i >= 0 {
		arg0 = arg0[i+1:]
	}
	for _, ext := range []string{".sh", ".py", ".rb", ".js", ".ts"} {
		if strings.HasSuffix(arg0, ext) {
			arg0 = strings.TrimSuffix(arg0, ext)
			break
		}
	}
	return arg0
}

// validLabel: stream labels become URL path segments and tmux window
// names; keep them ASCII-safe.
func validLabel(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// windowCount returns how many tmux windows the session currently has.
// Used to detect pane-mode (1 window with multiple panes) vs windows-mode
// (each stream in its own window).
func windowCount(session string) int {
	out, err := exec.Command("tmux", "list-windows", "-t", session, "-F", "x").Output()
	if err != nil {
		return 0
	}
	return len(splitLines(string(out)))
}

// lookupMultiStreamRun checks whether a run on the server is a multi-
// stream group (has streams whose Run==runName) and returns its Job for
// metadata propagation. Returns ok=false for standalone or missing runs.
func lookupMultiStreamRun(addr, runName string) (job string, ok bool) {
	for _, r := range fetchRunsIgnoreErr(addr) {
		if r.Run == runName {
			return r.Job, true
		}
	}
	return "", false
}

// waitForFastFail polls the server to detect commands that error out
// immediately (missing script, crash on startup, bad flags). Returns
// (true, exit code) if a non-zero exit marker arrives within the timeout.
func waitForFastFail(addr, name string, timeout time.Duration) (bool, int) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		resp, err := http.Get("http://" + addr + "/api/run/" + name)
		if err != nil {
			continue
		}
		var info struct {
			HasExit  bool `json:"has_exit"`
			ExitCode int  `json:"exit_code"`
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err := json.Unmarshal(body, &info); err != nil {
			continue
		}
		if info.HasExit && info.ExitCode != 0 {
			return true, info.ExitCode
		}
	}
	return false, 0
}

// shellQuote joins args into a single shell-safe command string.
// Each arg is wrapped in single quotes if it contains anything that needs
// escaping. Single quotes inside args are escaped with '\''.
func shellQuote(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if needsQuoting(a) {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '/' || c == '=' || c == ':' || c == ',' || c == '+' || c == '@' {
			continue
		}
		return true
	}
	return false
}

// preCreate sends run metadata to the server so it's available before
// the stream connects.
func preCreate(addr, name, command string) {
	m := collectMeta()
	body, _ := json.Marshal(map[string]string{
		"name":       name,
		"mode":       "run",
		"command":    command,
		"dir":        m.Dir,
		"host":       m.Host,
		"user":       m.User,
		"git_branch": m.GitBranch,
		"git_commit": m.GitCommit,
	})
	req, err := http.NewRequest("POST", "http://"+addr+"/api/run", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	addServerAuth(req)
	http.DefaultClient.Do(req)
}
