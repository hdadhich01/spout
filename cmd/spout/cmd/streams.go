package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/names"
)

// runStreams launches every entry in cfg.Streams as its own pane under a
// single tmux session. Each pane pipe-panes to the spout server as an
// independent run named "<run>-<label>", so the dashboard still shows
// per-stream status/history while the user only has one tmux session to
// manage locally.
//
// Wiring order (must stay in this order for the exit-marker race, same
// reason as run.go):
//   1. create the tmux session with a holding shell
//   2. per pane: split-window, pipe-pane, then send-keys the command
//   3. tile the layout
func runStreams(cfg config.Config) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required to launch streams but not found")
	}

	// Validate foreign keys before we spend time creating tmux sessions.
	if err := cfg.Validate(); err != nil {
		return err
	}

	addr, _ := resolveServer()
	if err := checkServer(addr); err != nil {
		return err
	}

	// Resolve the run name (tmux session name + per-stream prefix).
	// This may prompt the user for {input} - do it before we create any
	// server sessions so a canceled prompt leaves zero side effects.
	runName, err := resolveRunName(cfg)
	if err != nil {
		return err
	}

	// Check collision across all projected stream names BEFORE we commit to
	// any tmux sessions or server sessions. If any collides, bail with a
	// clear message that names one of them - user can `spout kill` or set
	// a different run_name.
	for _, s := range cfg.Streams {
		name := streamSessionName(runName, s.Label)
		if err := exec.Command("tmux", "has-session", "-t", name).Run(); err == nil {
			return fmt.Errorf("tmux session %s already exists - kill it or change run_name", cBold(name))
		}
		if sessionExists(addr, name) {
			return fmt.Errorf("run %s already exists on server - %s or change run_name", cBold(name), cAqua("spout kill "+name))
		}
	}

	spoutBin, err := os.Executable()
	if err != nil {
		spoutBin = "spout"
	}

	// Pre-create every session on the server with metadata so the dashboard
	// has them before the WebSocket stream starts.
	for _, s := range cfg.Streams {
		preCreateStream(addr, runName, s)
	}

	// Create the tmux session with a holding shell (no -x/-y forcing; let
	// tmux use the user's terminal size on attach).
	tmuxNew := exec.Command("tmux", "new-session", "-d", "-s", runName, "-x", "200", "-y", "50",
		"-c", config.ResolveStreamDir(cfg.Streams[0]))
	if err := tmuxNew.Run(); err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}

	// Wire up each stream. The first one goes into pane 0 (created by
	// new-session); subsequent streams get a split-window each.
	for i, s := range cfg.Streams {
		if i > 0 {
			split := exec.Command("tmux", "split-window", "-t", runName,
				"-c", config.ResolveStreamDir(s))
			if err := split.Run(); err != nil {
				exec.Command("tmux", "kill-session", "-t", runName).Run()
				return fmt.Errorf("splitting pane %d: %w", i, err)
			}
		}

		paneTarget := fmt.Sprintf("%s:0.%d", runName, i)
		streamName := streamSessionName(runName, s.Label)
		pipeCmd := fmt.Sprintf("%s _stream --server %s --session %s", spoutBin, addr, streamName)
		if err := exec.Command("tmux", "pipe-pane", "-t", paneTarget, pipeCmd).Run(); err != nil {
			exec.Command("tmux", "kill-session", "-t", runName).Run()
			return fmt.Errorf("pipe-pane on %s: %w", paneTarget, err)
		}

		shellCmd := buildStreamShellCmd(s)
		if err := exec.Command("tmux", "send-keys", "-t", paneTarget, shellCmd, "Enter").Run(); err != nil {
			exec.Command("tmux", "kill-session", "-t", runName).Run()
			return fmt.Errorf("sending command to %s: %w", paneTarget, err)
		}
	}

	// Tile so every pane gets equal space instead of the default split
	// cascading right.
	exec.Command("tmux", "select-layout", "-t", runName, "tiled").Run()

	// Print the info block. Each stream is a separate server run, so list
	// each one's URL. The user can attach to the whole tiled session with
	// `spout attach <run>`.
	ok("run %s %s", cBold(runName), cGreen("started"))
	if cfg.Job != "" {
		info("job %s  %s streams", cBold(cfg.Job), cBold(fmt.Sprintf("%d", len(cfg.Streams))))
	}
	fmt.Fprintln(os.Stderr)
	for _, s := range cfg.Streams {
		streamName := streamSessionName(runName, s.Label)
		url := "http://" + addr + "/r/" + streamName
		fmt.Fprintf(os.Stderr, "  %s %s  %s\n",
			cDim("["+s.Label+"]"),
			cBold(streamName),
			cAqua(url))
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s  spout attach %s\n", cDim("attach:"), runName)
	fmt.Fprintf(os.Stderr, "  %s    spout ls\n", cDim("list:"))
	fmt.Fprintf(os.Stderr, "  %s    spout kill %s\n", cDim("kill:"), runName)
	fmt.Fprintln(os.Stderr)

	return nil
}

// resolveRunName produces the session/run name for this invocation.
//
//   - No template: generate a random word-xxxx name.
//   - Template with {input}: prompt the user before doing anything else,
//     because we want to fail fast (and silently) if they ctrl-C out.
//   - Template with {n}: bump the per-job counter only on actual use,
//     so a template that omits {n} doesn't leak counter state.
func resolveRunName(cfg config.Config) (string, error) {
	if cfg.RunName == "" {
		return names.Generate(), nil
	}
	tmpl := cfg.RunName
	input := ""
	if config.RunNameNeedsInput(tmpl) {
		val := strings.TrimSpace(askLine(cAqua(cBold("run label:"))))
		if val == "" {
			return "", fmt.Errorf("run label required - template %q needs {input}", tmpl)
		}
		input = val
	}
	counter := 0
	if strings.Contains(tmpl, "{n}") {
		counter = config.NextRunCounter(cfg.Job)
	}
	return config.ExpandRunName(tmpl, counter, input), nil
}

// streamSessionName joins the run name with a stream's label.
func streamSessionName(run, label string) string {
	return run + "-" + label
}

// buildStreamShellCmd assembles the exact command string sent via
// `tmux send-keys` to run one stream. Follows the same pattern as run.go
// (exit marker + exec $SHELL to keep the pane alive for inspection).
func buildStreamShellCmd(s config.Stream) string {
	prefix := config.StreamShellPrefix(s)
	return fmt.Sprintf("%s%s; printf '\\033]9999;%%d\\007' $?; exec $SHELL", prefix, s.Command)
}

// preCreateStream registers the session on the server with stream-aware
// metadata (dir resolved, command set, label carried in the name). Silent
// on failure - the WebSocket ingest will still auto-create on first byte.
func preCreateStream(addr, runName string, s config.Stream) {
	m := collectMeta()
	body, _ := json.Marshal(map[string]string{
		"name":       streamSessionName(runName, s.Label),
		"mode":       "run",
		"command":    s.Command,
		"dir":        config.ResolveStreamDir(s),
		"host":       m.Host,
		"user":       m.User,
		"git_branch": m.GitBranch,
	})
	req, _ := http.NewRequest("POST", "http://"+addr+"/api/run", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}
