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

With no command, opens an interactive shell session that streams to the
dashboard. You're attached immediately; detach with Ctrl+b d.

Spout flags must come BEFORE the command:

  spout run                            # interactive shell
  spout run ping google.com
  spout run -n training python train.py
  spout -l run python train.py --epochs 100
  spout run -- python -c "print('hi')"

If you need to pass flags that look like spout flags to your command,
use '--' to separate them:

  spout run -- mycommand -n -l --foo`,
	Args:               cobra.MinimumNArgs(0),
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
	if err := resolveSessionName(addr, &sessionName, userChoseName); err != nil {
		return err
	}

	preCreate(addr, sessionName, userCmd)

	word := names.Prefix(sessionName)
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
		banner := fmt.Sprintf(
			`printf '\n  \033[38;2;90;200;226mspout: \033[0msession \033[1m%s\033[0m \033[38;2;34;197;94mstarted\033[0m\n  \033[38;2;90;200;226mspout: \033[0mdashboard at \033[38;2;90;200;226m%s\033[0m\n\n  \033[2mdetach:\033[0m Ctrl+b d\n\n'; exec $SHELL`,
			word, runURL)
		tmuxNew := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-x", "200", "-y", "50", "sh", "-c", banner)
		if err := tmuxNew.Run(); err != nil {
			return fmt.Errorf("creating tmux session: %w", err)
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

		// After detach/exit, show info on the original terminal.
		fmt.Fprintln(os.Stderr)
		ok("session %s %s", cBold(word), cGreen("detached"))
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
	tmuxNew := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-x", "200", "-y", "50")
	if err := tmuxNew.Run(); err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}

	pipePaneCmd := fmt.Sprintf("%s _stream --server %s --session %s", spoutBin, addr, sessionName)
	tmuxPipe := exec.Command("tmux", "pipe-pane", "-t", sessionName, pipePaneCmd)
	if err := tmuxPipe.Run(); err != nil {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		return fmt.Errorf("attaching pipe-pane: %w", err)
	}

	// Non-interactive: inject the command, wait briefly for fast-fail, print info.
	shellCmd := fmt.Sprintf("%s; printf '\\033]9999;%%d\\007' $?; exec $SHELL", userCmd)
	tmuxSend := exec.Command("tmux", "send-keys", "-t", sessionName, shellCmd, "Enter")
	if err := tmuxSend.Run(); err != nil {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		return fmt.Errorf("sending command: %w", err)
	}

	if failed, exitCode := waitForFastFail(addr, sessionName, 1500*time.Millisecond); failed {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		deleteRun(addr, sessionName)
		fail("%s %s %d on startup - session not created", cBold(args[0]), cRed("exited"), exitCode)
		fmt.Fprintf(os.Stderr, "\n  Re-run directly to see the error:  %s\n", cAqua(strings.Join(args, " ")))
		return ErrAlreadyReported
	}

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
