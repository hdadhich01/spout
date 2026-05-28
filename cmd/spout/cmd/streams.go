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

// paneLayoutThreshold is the cutoff at which we stop putting all streams
// in one tiled window and switch to one stream per window. Tiled is
// readable up to ~4 panes; beyond that each pane gets so small the
// "see everything at once" benefit goes away. Beyond the threshold,
// streams become separate windows the user navigates with C-b 1/2/3...
const paneLayoutThreshold = 4

// runStreams launches every entry in cfg.Streams under a single tmux
// session. Each stream pipe-panes to the spout server as an independent
// run named "<run>-<label>", so the dashboard still shows per-stream
// status/history while the user only has one tmux session to manage.
//
// Layout depends on stream count:
//   - len <= paneLayoutThreshold: all streams as panes in window 0,
//     tiled. Best for at-a-glance monitoring of parallel work.
//   - len >  paneLayoutThreshold: each stream gets its own window
//     (named after its label). User flips via C-b 1, C-b 2, C-b w.
//     Avoids unreadable tiles when there are many streams.
//
// Wiring order (must stay in this order for the exit-marker race, same
// reason as run.go):
//   1. create the tmux session with a holding shell
//   2. per stream: position the pane (split-window or new-window),
//      pipe-pane, send-keys the command
//   3. tile the layout (pane-mode only)
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
			return fmt.Errorf("run %s already exists — kill it or change run_name", cBold(name))
		}
		if sessionExists(addr, name) {
			return fmt.Errorf("run %s already exists on server — %s or change run_name", cBold(name), cAqua("spout kill "+name))
		}
	}

	spoutBin, err := os.Executable()
	if err != nil {
		spoutBin = "spout"
	}

	// Pre-create every session on the server with metadata so the dashboard
	// has them before the WebSocket stream starts.
	for _, s := range cfg.Streams {
		preCreateStream(addr, cfg.Job, runName, s)
	}

	useWindows := len(cfg.Streams) > paneLayoutThreshold

	// Create the tmux session. -x 200 -y 50 forces a virtual terminal size
	// while detached, so output produced before the user attaches doesn't
	// wrap at tmux's default 80x24. tmux reflows on attach to the actual
	// terminal size; the trade-off is a one-time reflow flicker, which is
	// preferable to clipped output. Same in run.go.
	tmuxNew := exec.Command("tmux", "new-session", "-d", "-s", runName, "-x", config.DefaultTerminalCols, "-y", config.DefaultTerminalRows,
		"-c", config.ResolveStreamDir(cfg.Streams[0]))
	if err := tmuxNew.Run(); err != nil {
		return fmt.Errorf("creating run: %w", err)
	}

	// In windows mode, name window 0 after the first stream up front so
	// the C-b w window list reads cleanly.
	if useWindows {
		exec.Command("tmux", "rename-window", "-t", runName+":0", cfg.Streams[0].Label).Run()
	}

	// Wire up each stream. First stream goes into the pane that
	// new-session already created. Subsequent streams either split the
	// existing window (pane mode) or get a fresh window (windows mode).
	for i, s := range cfg.Streams {
		if i > 0 {
			if useWindows {
				newWin := exec.Command("tmux", "new-window", "-t", runName,
					"-n", s.Label,
					"-c", config.ResolveStreamDir(s))
				if err := newWin.Run(); err != nil {
					exec.Command("tmux", "kill-session", "-t", runName).Run()
					return fmt.Errorf("creating window %d: %w", i, err)
				}
			} else {
				split := exec.Command("tmux", "split-window", "-t", runName,
					"-c", config.ResolveStreamDir(s))
				if err := split.Run(); err != nil {
					exec.Command("tmux", "kill-session", "-t", runName).Run()
					return fmt.Errorf("splitting pane %d: %w", i, err)
				}
			}
		}

		// Pane targeting:
		//   - pane mode:    all streams are panes 0..N-1 in window 0
		//   - windows mode: each stream is the only pane (.0) in its
		//                   own window i
		var paneTarget string
		if useWindows {
			paneTarget = fmt.Sprintf("%s:%d.0", runName, i)
		} else {
			paneTarget = fmt.Sprintf("%s:0.%d", runName, i)
		}
		streamName := streamSessionName(runName, s.Label)

		// Record the label two ways for later lookup by `spout attach/kill`:
		// pane title is human-visible in the status bar; @spout-label is a
		// pane-scoped user option that survives if the user manually
		// renames the pane. Lookup prefers @spout-label, falls back to
		// pane title (see findPaneByLabel).
		exec.Command("tmux", "select-pane", "-t", paneTarget, "-T", s.Label).Run()
		exec.Command("tmux", "set-option", "-t", paneTarget, "-p", "@spout-label", s.Label).Run()

		pipeCmd := fmt.Sprintf("%s _stream --server %s --session %s", spoutBin, addr, streamName)
		if err := exec.Command("tmux", "pipe-pane", "-t", paneTarget, pipeCmd).Run(); err != nil {
			exec.Command("tmux", "kill-session", "-t", runName).Run()
			return fmt.Errorf("pipe-pane on %s: %w", paneTarget, err)
		}

		// respawn-pane (not send-keys) so the shell doesn't echo our exit-
		// marker wrapper into the captured stream. See `spout run` for the
		// rationale + comment.
		shellCmd := buildStreamShellCmd(s)
		if err := exec.Command("tmux", "respawn-pane", "-k", "-t", paneTarget, shellCmd).Run(); err != nil {
			exec.Command("tmux", "kill-session", "-t", runName).Run()
			return fmt.Errorf("starting command on %s: %w", paneTarget, err)
		}
	}

	// Tile only applies when multiple streams share one window.
	if !useWindows && len(cfg.Streams) > 1 {
		exec.Command("tmux", "select-layout", "-t", runName, "tiled").Run()
	}
	// In windows mode, leave the active window on the first stream so the
	// user lands on something predictable when they attach.
	if useWindows {
		exec.Command("tmux", "select-window", "-t", runName+":0").Run()
	}

	// Print the info block. Each stream is a separate server run, so list
	// each one's URL. The user can attach to the whole tiled session with
	// `spout attach <run>`.
	// Single start line mirrors kill's shape: "started run X (...)".
	// When a job name is set, it prefixes the stream count for context.
	unit := "streams"
	if len(cfg.Streams) == 1 {
		unit = "stream"
	}
	layout := "tiled"
	if useWindows {
		layout = "windows"
	}
	detail := fmt.Sprintf("%d %s, %s", len(cfg.Streams), unit, layout)
	if cfg.Job != "" {
		detail = fmt.Sprintf("job %s, %d %s, %s", cBold(cfg.Job), len(cfg.Streams), unit, layout)
	}
	ok("%s run %s  (%s)", cGreen("started"), cBold(runName), detail)
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
	if useWindows {
		fmt.Fprintf(os.Stderr, "  %s  C-b 0..%d to switch streams (or C-b w for the list)\n",
			cDim("nav:"), len(cfg.Streams)-1)
	}
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

// preCreateStream registers the session on the server with job/run/label
// metadata plus the resolved dir and command. Silent on failure - the
// WebSocket ingest will still auto-create on first byte.
func preCreateStream(addr, job, runName string, s config.Stream) {
	m := collectMeta()
	body, _ := json.Marshal(map[string]string{
		"name":       streamSessionName(runName, s.Label),
		"mode":       "run",
		"job":        job,
		"run":        runName,
		"label":      s.Label,
		"command":    s.Command,
		"dir":        config.ResolveStreamDir(s),
		"host":       m.Host,
		"user":       m.User,
		"git_branch": m.GitBranch,
		"git_commit": m.GitCommit,
	})
	req, _ := http.NewRequest("POST", "http://"+addr+"/api/run", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	addServerAuth(req)
	client := http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}
