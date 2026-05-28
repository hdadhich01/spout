package cmd

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hdadhich01/spout/internal/store"
	"github.com/spf13/cobra"
)

// Flags shared by the unified rm/delete command. Any combination that
// selects >0 runs batch-deletes; with no args and no flags, we open the
// interactive menu.
var (
	rmAll    bool
	rmEnded  bool
	rmErrors bool
	rmOlder  string
	rmKeep   int
	rmForce  bool
)

var deleteCmd = &cobra.Command{
	Use:     "delete [name...]",
	Aliases: []string{"rm", "clean"},
	Short:   "Delete runs (by name, by filter, or interactively)",
	Long: `Delete runs. Targets come from args, filter flags, or an interactive menu.

  spout rm fox                        # by run name
  spout rm exp-2                      # whole run + every stream in it
  spout rm exp-2/training             # one stream
  spout rm fox bear cat               # multiple
  spout rm --ended                    # every ended run
  spout rm --errors                   # only errored runs
  spout rm --older 7d                 # older than (m/h/d/w)
  spout rm --keep 10                  # keep newest 10
  spout rm                            # interactive menu`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return deleteByArgs(args)
		}
		// Flag-driven batch.
		if rmAll || rmEnded || rmErrors || rmOlder != "" || rmKeep > 0 {
			return deleteByFilter()
		}
		// Nothing specified - fall to the interactive picker.
		return interactiveClean()
	},
}

// deleteByArgs handles the "spout rm name [name...]" path. Each arg is
// resolved against the local copy independently and may target one
// stream, one standalone run, or a whole multi-stream run group.
//
// For each session resolved:
//   1. Kills tmux backing if still running (with prompt unless -y).
//   2. Removes the server-side copy when run meta records a server AND
//      the env var named in meta.ServerToken resolves to a value.
//      Tokenless server copies stay untouched.
//   3. Removes the local copy folder.
func deleteByArgs(args []string) error {
	local, _, err := openLocal()
	if err != nil {
		return err
	}

	deleted := 0
	total := 0
	for _, arg := range args {
		hits, rerr := resolveLocalTargets(local, []string{arg}, !rmForce)
		if rerr != nil {
			fail("%v", rerr)
			continue
		}
		for _, sess := range hits {
			total++
			if deleteOneLocal(local, sess) {
				deleted++
			}
		}
	}
	if total > 1 {
		ok("%s %d of %d", cGreen("deleted"), deleted, total)
	}
	return nil
}

// deleteOneLocal removes a single run end-to-end: tmux backing, optional
// server-side copy, then the local copy folder. Returns true on a clean
// removal of the local copy.
func deleteOneLocal(local *store.FileStore, sess *store.Session) bool {
	tmuxSession := sess.Name
	if sess.Run != "" {
		tmuxSession = sess.Run
	}
	tmuxAlive := exec.Command("tmux", "has-session", "-t", tmuxSession).Run() == nil

	if tmuxAlive && !rmForce {
		warn("%s is %s", cBold(sess.Name), cYellow("still running"))
		if !confirm("kill it and delete?") {
			info("%s %s", cDim("skipped"), cBold(sess.Name))
			return false
		}
	}
	if tmuxAlive {
		exec.Command("tmux", "kill-session", "-t", tmuxSession).Run()
	}

	// Server-side delete is auth-gated. Tokenless = local-only with note.
	if sess.ServerURL != "" {
		token := os.Getenv(sess.ServerToken)
		if sess.ServerToken != "" && token != "" {
			if err := deleteRun(sess.ServerURL, sess.Name, sess.ServerToken); err != nil {
				warn("server delete %s: %v", cBold(sess.Name), err)
			} else {
				info("%s on server %s", cDim("deleted"), cDim(sess.ServerURL))
			}
		} else {
			info("%s — server runs aren't deletable without auth", cDim("local only"))
		}
	}

	if err := local.Delete(sess.Name); err != nil {
		fail("local delete %s: %v", cBold(sess.Name), err)
		return false
	}
	ok("%s %s", cGreen("deleted"), cBold(sess.Name))
	return true
}

// deleteByFilter selects runs according to the set flags, shows a preview,
// prompts (unless -y), and batch-deletes. Filters operate on the server
// (status / age) and so this path remains server-affiliated; the by-name
// path (deleteByArgs) is the local-affiliated one.
func deleteByFilter() error {
	addr, _ := resolveServer()
	tokenVar := resolveServerTokenVar()
	if err := checkServer(addr); err != nil {
		return err
	}
	runs, err := fetchRuns(addr)
	if err != nil {
		return fmt.Errorf("server %s: %s", cBold(addr), cRed(err.Error()))
	}

	var targets []runEntry
	switch {
	case rmAll:
		targets = runs
	case rmErrors:
		for _, r := range runs {
			if r.Status == "error" {
				targets = append(targets, r)
			}
		}
	case rmEnded:
		for _, r := range runs {
			if !isActive(r.Status) {
				targets = append(targets, r)
			}
		}
	case rmOlder != "":
		d, err := parseDuration(rmOlder)
		if err != nil {
			return fmt.Errorf("invalid duration %q (use 7d, 24h, 1w)", rmOlder)
		}
		cutoff := time.Now().Add(-d).UnixMilli()
		for _, r := range runs {
			if !isActive(r.Status) && r.StartedMs < cutoff {
				targets = append(targets, r)
			}
		}
	case rmKeep > 0:
		if len(runs) > rmKeep {
			targets = runs[rmKeep:]
		}
	}

	if len(targets) == 0 {
		info("%s", cDim("nothing to delete"))
		return nil
	}

	fmt.Fprintln(stderr)
	warn("%s %d run(s):", cYellow("about to delete"), len(targets))
	for i, r := range targets {
		if i >= 10 {
			fmt.Fprintf(stderr, "  %s\n", cDim(fmt.Sprintf("... and %d more", len(targets)-10)))
			break
		}
		fmt.Fprintf(stderr, "  %s %s  %s\n", statusDot(r.Status), cBold(r.Name), cDim(humanDuration(r.DurationMs)))
	}
	fmt.Fprintln(stderr)

	if !rmForce {
		if rmAll {
			if !confirmTyped("destructive - confirm deletion of ALL runs", "delete") {
				info("%s", cDim("cancelled"))
				return nil
			}
		} else if !confirm("proceed?") {
			info("%s", cDim("cancelled"))
			return nil
		}
	}

	// Kill any tmux sessions still backing selected targets FIRST so we
	// don't leave panes streaming into deleted server records. Dedup by
	// tmux session name so a group of 4 streams kills once, not four
	// times.
	killedSessions := map[string]struct{}{}
	for _, r := range targets {
		session := r.Name
		if r.Run != "" {
			session = r.Run
		}
		if _, done := killedSessions[session]; done {
			continue
		}
		if exec.Command("tmux", "has-session", "-t", session).Run() == nil {
			exec.Command("tmux", "kill-session", "-t", session).Run()
			killedSessions[session] = struct{}{}
		}
	}

	deleted := 0
	for _, r := range targets {
		if err := deleteRun(addr, r.Name, tokenVar); err == nil {
			deleted++
		}
	}
	ok("%s %d %s", cGreen("deleted"), deleted, pluralize("run", deleted))
	return nil
}

// interactiveClean is the no-args, no-flags fallback. Shows per-bucket
// counts and a small keyed menu. Picking a bucket is equivalent to
// running `spout rm --<bucket>`.
func interactiveClean() error {
	addr, _ := resolveServer()
	tokenVar := resolveServerTokenVar()
	if err := checkServer(addr); err != nil {
		return err
	}
	runs, err := fetchRuns(addr)
	if err != nil {
		return fmt.Errorf("server %s: %s", cBold(addr), cRed(err.Error()))
	}
	if len(runs) == 0 {
		info("%s", cDim("no runs"))
		return nil
	}

	ended := 0
	errored := 0
	for _, r := range runs {
		if !isActive(r.Status) {
			ended++
		}
		if r.Status == "error" {
			errored++
		}
	}

	type choice struct {
		key, label string
		filter     func(runEntry) bool
	}
	var menu []choice
	if ended > 0 {
		menu = append(menu, choice{"e", fmt.Sprintf("delete all ended (%d)", ended),
			func(r runEntry) bool { return !isActive(r.Status) }})
	}
	if errored > 0 {
		menu = append(menu, choice{"r", fmt.Sprintf("delete all errored (%d)", errored),
			func(r runEntry) bool { return r.Status == "error" }})
	}
	menu = append(menu, choice{"a", fmt.Sprintf("delete everything (%d)", len(runs)), nil})

	fmt.Fprintln(stderr)
	label("total", fmt.Sprintf("%d runs", len(runs)))
	label("ended", fmt.Sprintf("%d", ended))
	label("errors", fmt.Sprintf("%d", errored))
	fmt.Fprintln(stderr)

	for _, m := range menu {
		fmt.Fprintf(stderr, "  %s   %s\n", cAqua(cBold("["+m.key+"]")), m.label)
	}
	fmt.Fprintf(stderr, "  %s   cancel\n", cAqua(cBold("[q]")))
	fmt.Fprintln(stderr)
	fmt.Fprint(stderr, "  > ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	var targets []runEntry
	switch input {
	case "e", "ended":
		for _, r := range runs {
			if !isActive(r.Status) {
				targets = append(targets, r)
			}
		}
	case "r", "errors":
		for _, r := range runs {
			if r.Status == "error" {
				targets = append(targets, r)
			}
		}
	case "a", "all":
		targets = runs
	default:
		info("%s", cDim("cancelled"))
		return nil
	}

	if len(targets) == 0 {
		info("%s", cDim("nothing to delete"))
		return nil
	}

	// Kill tmux backings first (dedup by session) so we don't leak panes.
	killedSessions := map[string]struct{}{}
	for _, r := range targets {
		session := r.Name
		if r.Run != "" {
			session = r.Run
		}
		if _, done := killedSessions[session]; done {
			continue
		}
		if exec.Command("tmux", "has-session", "-t", session).Run() == nil {
			exec.Command("tmux", "kill-session", "-t", session).Run()
			killedSessions[session] = struct{}{}
		}
	}

	deleted := 0
	for _, r := range targets {
		if err := deleteRun(addr, r.Name, tokenVar); err == nil {
			deleted++
		}
	}
	ok("%s %d %s", cGreen("deleted"), deleted, pluralize("run", deleted))
	return nil
}

// deleteRun issues a DELETE against the server's /api/run/<name>. If
// tokenVar names an env var with a non-empty value, an Authorization
// header is attached (Bearer <value>). Tokenless calls are best-effort
// and rely on the server being in open mode (SPOUT_TOKEN unset).
func deleteRun(addr, name, tokenVar string) error {
	req, err := http.NewRequest("DELETE", "http://"+addr+"/api/run/"+name, nil)
	if err != nil {
		return err
	}
	if tokenVar != "" {
		if v := os.Getenv(tokenVar); v != "" {
			req.Header.Set("Authorization", "Bearer "+v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// parseDuration accepts simple suffixes: 30m, 24h, 7d, 2w
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	last := s[len(s)-1]
	num := s[:len(s)-1]
	var n int
	if _, err := fmt.Sscanf(num, "%d", &n); err != nil {
		return 0, err
	}
	switch last {
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid suffix %c (use m/h/d/w)", last)
}

func init() {
	deleteCmd.Flags().BoolVarP(&rmAll, "all", "a", false, "every run")
	deleteCmd.Flags().BoolVarP(&rmEnded, "ended", "e", false, "every ended run")
	deleteCmd.Flags().BoolVar(&rmErrors, "errors", false, "every errored run")
	deleteCmd.Flags().StringVar(&rmOlder, "older", "", "older than (30m/24h/7d/2w)")
	deleteCmd.Flags().IntVar(&rmKeep, "keep", 0, "keep newest N, delete rest")
	deleteCmd.Flags().BoolVarP(&rmForce, "yes", "y", false, "skip confirmation")
	deleteCmd.GroupID = groupSession
	rootCmd.AddCommand(deleteCmd)
}
