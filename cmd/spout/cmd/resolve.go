package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// target describes one thing the user can address by name on the CLI.
// Depending on context, a user's arg may resolve to:
//
//   - a standalone run          (Run=="", Label=="", Name=server session)
//   - a stream inside a run     (Run=exp-2, Label=training, Name=exp-2-training)
//   - a whole run group         (Run=exp-2, Label=="" so the caller knows
//                                it's the whole group; Name is the tmux
//                                session = the run name)
//
// Addr is the server where this target lives (via findRunServer fallback).
type target struct {
	Addr   string
	Name   string // server session name; empty when Kind==targetGroup
	Run    string // group key ("exp-2"), empty for pure standalone runs
	Label  string // stream label when addressing a single stream
	Kind   targetKind
	Entry  runEntry // full server metadata when we pulled it
}

type targetKind int

const (
	targetStandalone targetKind = iota // single session, no streams in the group
	targetStream                       // one stream inside a run
	targetGroup                        // the run itself (all its streams)
)

// resolveTargets finds the run(s) matching user-supplied name pieces.
//
//	resolveTargets("exp-2")            -> whole group if exp-2 is a run name
//	resolveTargets("training")         -> single stream (+ chooser if ambiguous)
//	resolveTargets("exp-2", "training") -> that exact stream
//	resolveTargets("exp-2/training")    -> same as above
//
// `allowChooser` controls whether an ambiguous label triggers an interactive
// picker or returns multiple candidates for the caller to handle. Used by
// attach/kill where we want interaction; used internally when we need a
// single unambiguous answer.
//
// The returned slice always has at least one target on success.
func resolveTargets(pieces []string, allowChooser bool) ([]target, error) {
	run, label := splitPieces(pieces)

	addr, _ := resolveServer()
	runs := fetchRunsIgnoreErr(addr)
	// Also peek at localhost as a fallback - same logic as findRunServer.
	if len(runs) == 0 {
		addr2, _ := resolveServer()
		if addr2 != addr {
			runs = fetchRunsIgnoreErr(addr2)
			if len(runs) > 0 {
				addr = addr2
			}
		}
	}

	// Case A: user supplied both run + label. One exact match required.
	if run != "" && label != "" {
		for _, r := range runs {
			if r.Run == run && r.Label == label {
				return []target{{Addr: addr, Name: r.Name, Run: r.Run, Label: r.Label, Kind: targetStream, Entry: r}}, nil
			}
		}
		return nil, fmt.Errorf("no stream %s in run %s", cBold(label), cBold(run))
	}

	// Case B: one identifier. Could be a run name (group), a stream label,
	// or a standalone run name.
	needle := run
	if needle == "" {
		needle = label
	}

	var groupMatch []runEntry
	var labelMatches []runEntry
	var nameMatches []runEntry
	var prefixMatches []runEntry
	for _, r := range runs {
		if needle == "" {
			continue
		}
		if r.Run == needle {
			groupMatch = append(groupMatch, r)
		}
		if r.Label == needle {
			labelMatches = append(labelMatches, r)
		}
		if r.Name == needle {
			nameMatches = append(nameMatches, r)
		}
		// Prefix match: a run "chaos-3es9" is reachable by typing "chaos".
		// Mirrors FileStore.Get so the short word printed by `spout run`
		// resolves the same way local commands (logs/delete) already do.
		if strings.HasPrefix(r.Name, needle+"-") {
			prefixMatches = append(prefixMatches, r)
		}
	}

	// 1. Exact run / stream name wins.
	if len(nameMatches) == 1 {
		r := nameMatches[0]
		k := targetStandalone
		if r.Run != "" {
			k = targetStream
		}
		return []target{{Addr: addr, Name: r.Name, Run: r.Run, Label: r.Label, Kind: k, Entry: r}}, nil
	}

	// 2. Whole-group match: "exp-2" and multiple streams share that Run.
	if len(groupMatch) > 0 {
		return []target{{Addr: addr, Name: needle, Run: needle, Kind: targetGroup, Entry: groupMatch[0]}}, nil
	}

	// 3. Exact stream label.
	if len(labelMatches) == 1 {
		r := labelMatches[0]
		return []target{{Addr: addr, Name: r.Name, Run: r.Run, Label: r.Label, Kind: targetStream, Entry: r}}, nil
	}
	if len(labelMatches) > 1 {
		if !allowChooser {
			return nil, ambiguityErr("streams named", needle, labelMatches)
		}
		return chooseAmbiguous(addr, labelMatches)
	}

	// 4. Prefix match (the short-word convenience: `attach chaos`).
	if len(prefixMatches) > 0 {
		return resolvePrefix(addr, needle, prefixMatches, allowChooser)
	}

	return nil, fmt.Errorf("no run or stream named %s", cBold(needle))
}

// resolvePrefix turns a set of prefix-matched runs into a single target.
// One match → that run. Many matches all in one group → the group.
// Otherwise ambiguous (chooser or error).
func resolvePrefix(addr, needle string, matches []runEntry, allowChooser bool) ([]target, error) {
	if len(matches) == 1 {
		r := matches[0]
		k := targetStandalone
		if r.Run != "" {
			k = targetStream
		}
		return []target{{Addr: addr, Name: r.Name, Run: r.Run, Label: r.Label, Kind: k, Entry: r}}, nil
	}
	// All in one run group → resolve to the group.
	grp := matches[0].Run
	allSameGroup := grp != ""
	for _, r := range matches {
		if r.Run != grp {
			allSameGroup = false
			break
		}
	}
	if allSameGroup {
		return []target{{Addr: addr, Name: grp, Run: grp, Kind: targetGroup, Entry: matches[0]}}, nil
	}
	if !allowChooser {
		return nil, ambiguityErr("runs matching", needle, matches)
	}
	return chooseAmbiguous(addr, matches)
}

// ambiguityErr formats a "be more specific" error listing the candidates.
func ambiguityErr(kind, needle string, matches []runEntry) error {
	lines := make([]string, 0, len(matches))
	for _, r := range matches {
		lines = append(lines, displayRunEntry(r))
	}
	return fmt.Errorf("%d %s %s — be specific: %s",
		len(matches), kind, cBold(needle), strings.Join(lines, ", "))
}

// splitPieces normalises the user's name arguments. Accepts any of:
//
//	["exp-2"]
//	["exp-2", "training"]
//	["exp-2/training"]
//
// and returns (run, label) with the parts that were supplied.
func splitPieces(pieces []string) (run, label string) {
	switch len(pieces) {
	case 0:
		return "", ""
	case 1:
		if i := strings.Index(pieces[0], "/"); i >= 0 {
			return pieces[0][:i], pieces[0][i+1:]
		}
		return pieces[0], ""
	default:
		return pieces[0], pieces[1]
	}
}

// chooseAmbiguous prompts when a single label matches multiple streams
// (e.g. two jobs both have a "training" pane). Shows a numbered list
// with job context. Empty input cancels.
func chooseAmbiguous(addr string, candidates []runEntry) ([]target, error) {
	fmt.Fprintln(stderr)
	info("%d streams named %s — pick one:", len(candidates), cBold(candidates[0].Label))
	fmt.Fprintln(stderr)
	for i, r := range candidates {
		fmt.Fprintf(stderr, "  %s %s\n",
			cAqua(cBold(fmt.Sprintf("[%d]", i+1))),
			displayRunEntry(r))
	}
	fmt.Fprintln(stderr)
	fmt.Fprint(stderr, "  > ")
	raw := strings.TrimSpace(askLine(""))
	if raw == "" {
		return nil, fmt.Errorf("cancelled")
	}
	var idx int
	if _, err := fmt.Sscanf(raw, "%d", &idx); err != nil || idx < 1 || idx > len(candidates) {
		return nil, fmt.Errorf("invalid choice %q", raw)
	}
	r := candidates[idx-1]
	return []target{{Addr: addr, Name: r.Name, Run: r.Run, Label: r.Label, Kind: targetStream, Entry: r}}, nil
}

// displayRunEntry renders a runEntry for chooser/error output. Includes
// a [job] prefix when the run carries one, so the same stream label
// across different jobs is visually disambiguated.
func displayRunEntry(r runEntry) string {
	var base string
	if r.Run != "" && r.Label != "" {
		base = cBold(r.Run) + cDim("/") + cBold(r.Label)
	} else {
		base = cBold(r.Name)
	}
	if r.Job != "" {
		return cDim("["+r.Job+"]") + " " + base
	}
	return base
}

// fetchRunsIgnoreErr is a thin wrapper that suppresses transport errors
// so resolveTargets can treat "no runs available" uniformly.
func fetchRunsIgnoreErr(addr string) []runEntry {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/runs")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	var runs []runEntry
	json.Unmarshal(body, &runs)
	return runs
}

// pickRun shows a numbered list of runs and returns the one the user
// selected. `filter` narrows what's shown (active-only vs ended-only
// vs all); `promptText` is the action verb like "attach" or "kill".
// Returns an error if the user cancels with empty input or invalid choice.
//
// Used when a name-taking command was invoked with zero args - this is
// the "make the UX interactive by default" path. The same command, given
// args or a --all flag, skips this and operates directly.
func pickRun(addr, promptText string, filter func(runEntry) bool) (target, error) {
	runs := fetchRunsIgnoreErr(addr)
	// Group siblings of the same Run together; standalone runs get their
	// own one-item group.
	type entry struct {
		key      string
		children []runEntry // len>=1; children share a group
	}
	seen := map[string]int{}
	var entries []entry
	for _, r := range runs {
		if filter != nil && !filter(r) {
			continue
		}
		k := groupKey(r)
		if i, ok := seen[k]; ok {
			entries[i].children = append(entries[i].children, r)
			continue
		}
		seen[k] = len(entries)
		entries = append(entries, entry{key: k, children: []runEntry{r}})
	}

	if len(entries) == 0 {
		return target{}, fmt.Errorf("no runs to %s", promptText)
	}

	fmt.Fprintln(stderr)
	info("pick a run to %s:", cBold(promptText))
	fmt.Fprintln(stderr)
	for i, e := range entries {
		dot := statusDot(aggregateStatus(e.children))
		jobPrefix := ""
		if e.children[0].Job != "" {
			jobPrefix = cDim("["+e.children[0].Job+"] ")
		}
		detail := ""
		if len(e.children) > 1 || e.children[0].Run != "" {
			unit := "streams"
			if len(e.children) == 1 {
				unit = "stream"
			}
			detail = cDim(fmt.Sprintf(" (%d %s)", len(e.children), unit))
		}
		fmt.Fprintf(stderr, "  %s %s  %s%s%s\n",
			cAqua(cBold(fmt.Sprintf("[%d]", i+1))),
			dot,
			jobPrefix,
			cBold(e.key),
			detail)
	}
	fmt.Fprintln(stderr)
	fmt.Fprint(stderr, "  > ")
	raw := strings.TrimSpace(askLine(""))
	if raw == "" {
		return target{}, fmt.Errorf("cancelled")
	}
	var idx int
	if _, err := fmt.Sscanf(raw, "%d", &idx); err != nil || idx < 1 || idx > len(entries) {
		return target{}, fmt.Errorf("invalid choice %q", raw)
	}

	picked := entries[idx-1]
	first := picked.children[0]
	if first.Run != "" {
		return target{
			Addr:  addr,
			Name:  first.Run,
			Run:   first.Run,
			Kind:  targetGroup,
			Entry: first,
		}, nil
	}
	return target{
		Addr:  addr,
		Name:  first.Name,
		Kind:  targetStandalone,
		Entry: first,
	}, nil
}

// pickStreamInRun shows a numbered list of streams inside one run group
// and asks the user which one to focus. An `[a]` shortcut selects
// "attach all panes" (tiled view, no pane focused). Single-stream
// groups short-circuit: there's nothing to pick between. Returns the
// chosen label, or empty string for "all panes".
//
// Used by `spout attach <run>` so the user gets a stream-level chooser
// with status codes before tmux takes over the terminal.
func pickStreamInRun(addr, runName string) (label string, all bool, err error) {
	runs := fetchRunsIgnoreErr(addr)
	var streams []runEntry
	for _, r := range runs {
		if r.Run == runName {
			streams = append(streams, r)
		}
	}
	if len(streams) == 0 {
		return "", true, nil // no server metadata: fall through to all-panes
	}
	if len(streams) == 1 {
		return streams[0].Label, false, nil
	}

	fmt.Fprintln(stderr)
	info("pick a stream in %s to focus:", cBold(runName))
	fmt.Fprintln(stderr)
	// Width of the longest label so status codes line up.
	maxLabel := 0
	for _, s := range streams {
		if len(s.Label) > maxLabel {
			maxLabel = len(s.Label)
		}
	}
	for i, s := range streams {
		gap := strings.Repeat(" ", maxLabel-len(s.Label))
		fmt.Fprintf(stderr, "  %s %s  %s%s  %s\n",
			cAqua(cBold(fmt.Sprintf("[%d]", i+1))),
			statusDot(s.Status),
			cBold(s.Label), gap,
			statusText(s.Status))
	}
	fmt.Fprintf(stderr, "  %s %s  %s\n",
		cAqua(cBold("[a]")),
		cDim("●"),
		cDim("attach all panes (tiled)"))
	fmt.Fprintln(stderr)
	fmt.Fprint(stderr, "  > ")
	raw := strings.TrimSpace(askLine(""))
	if raw == "" {
		return "", false, fmt.Errorf("cancelled")
	}
	if raw == "a" || raw == "A" || raw == "all" {
		return "", true, nil
	}
	var idx int
	if _, e := fmt.Sscanf(raw, "%d", &idx); e != nil || idx < 1 || idx > len(streams) {
		return "", false, fmt.Errorf("invalid choice %q", raw)
	}
	return streams[idx-1].Label, false, nil
}

// Silence unused-import lints if any path doesn't pull os in the future.
var _ = os.Stderr
