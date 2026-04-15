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

	"github.com/hdadhich01/spout/internal/names"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [-n name] command [args...]",
	Short: "Run a command in the background",
	Long: `Run a command in a detached tmux session and stream to the dashboard.
Your shell returns immediately. Reattach with 'spout attach <name>'.

Spout flags must come BEFORE the command:

  spout run ping google.com
  spout run -n training python train.py
  spout -l run python train.py --epochs 100
  spout run -- python -c "print('hi')"

If you need to pass flags that look like spout flags to your command,
use '--' to separate them:

  spout run -- mycommand -n -l --foo`,
	Args:               cobra.MinimumNArgs(1),
	RunE:               runCommand,
	DisableFlagParsing: false,
}

func init() {
	// Flags before positional args only - everything after the first
	// non-flag arg is treated as part of the user command.
	runCmd.Flags().SetInterspersed(false)
	runCmd.GroupID = groupStream
	rootCmd.AddCommand(runCmd)
}

func runCommand(cmd *cobra.Command, args []string) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required for 'spout run' but not found\n\n  Install: sudo apt install tmux  (Debian/Ubuntu)\n           brew install tmux     (macOS)")
	}

	// Preflight: verify the binary exists on PATH so obvious typos fail
	// before we spin up a tmux session and server record.
	if _, err := exec.LookPath(args[0]); err != nil {
		return fmt.Errorf("command not found: %s", cBold(args[0]))
	}

	userChoseName := sessionName != ""
	if sessionName == "" {
		sessionName = names.Generate()
	}

	// Properly shell-quote each arg so embedded spaces/quotes survive `sh -c`.
	userCmd := shellQuote(args)

	if err := validateCommand(userCmd); err != nil {
		return err
	}

	spoutBin, err := os.Executable()
	if err != nil {
		spoutBin = "spout"
	}

	addr, _ := resolveServer()
	if err := checkServer(addr); err != nil {
		return err
	}
	if err := resolveSessionName(addr, &sessionName, userChoseName); err != nil {
		return err
	}

	// Pre-create the session on the server with full metadata.
	preCreate(addr, sessionName, userCmd)

	// IMPORTANT: create the tmux session with a holding shell FIRST, then wire
	// up pipe-pane, and only then send the user's command via send-keys. If
	// we started the command as part of new-session, fast-failing commands
	// (missing script, crash on startup) finish before pipe-pane attaches,
	// so the exit marker is lost and the run looks like `ended` instead of
	// `error`. By the time send-keys fires, the pipe is already listening.
	tmuxNew := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-x", "200", "-y", "50")
	if err := tmuxNew.Run(); err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}

	// Attach pipe-pane to stream all pane output to the server.
	pipePaneCmd := fmt.Sprintf("%s _stream --server %s --session %s", spoutBin, addr, sessionName)
	tmuxPipe := exec.Command("tmux", "pipe-pane", "-t", sessionName, pipePaneCmd)
	if err := tmuxPipe.Run(); err != nil {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		return fmt.Errorf("attaching pipe-pane: %w", err)
	}

	// Now inject the actual command. The OSC sequence \x1b]9999;<code>\x07
	// is invisible to xterm.js but the spout server scans for it to record
	// the exit code. `exec $SHELL` keeps the pane alive after the command
	// finishes so the user can inspect output or run more commands.
	shellCmd := fmt.Sprintf("%s; printf '\\033]9999;%%d\\007' $?; exec $SHELL", userCmd)
	tmuxSend := exec.Command("tmux", "send-keys", "-t", sessionName, shellCmd, "Enter")
	if err := tmuxSend.Run(); err != nil {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		return fmt.Errorf("sending command: %w", err)
	}

	// Short fast-fail window: poll the server to see if the command errored
	// before we even printed "session started". If it did, tear down the
	// session and surface the error to the user - there's no point leaving
	// a dead session around for a command that never got off the ground.
	if failed, exitCode := waitForFastFail(addr, sessionName, 1500*time.Millisecond); failed {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		deleteRun(addr, sessionName)
		fail("%s %s %d on startup - session not created", cBold(args[0]), cRed("exited"), exitCode)
		fmt.Fprintf(os.Stderr, "\n  Re-run directly to see the error:  %s\n", cAqua(strings.Join(args, " ")))
		return ErrAlreadyReported
	}

	word := names.Prefix(sessionName)
	runURL := "http://" + addr + "/r/" + sessionName
	ok("session %s %s", cBold(word), cGreen("started"))
	info("dashboard at %s", cAqua(runURL))
	copyToClipboard(runURL)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s  spout attach %s\n", cDim("attach:"), word)
	fmt.Fprintf(os.Stderr, "  %s    spout ls\n", cDim("list:"))
	fmt.Fprintf(os.Stderr, "  %s    spout kill %s\n", cDim("kill:"), word)
	fmt.Fprintln(os.Stderr)

	return nil
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

// preCreate sends session metadata to the server so it's available
// before the stream connects.
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
	})
	http.Post("http://"+addr+"/api/run", "application/json", strings.NewReader(string(body)))
}
