package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/spf13/cobra"
)

const maxListed = 3

// showStatus is called when bare `spout` is run (no pipe, no args).
func showStatus(cmd *cobra.Command) error {
	addr, _ := resolveServer()

	banner()
	fmt.Fprintf(stderr, "\n")
	label("server", addr)

	runs, err := fetchRuns(addr)
	if err != nil {
		label("status", cRed(err.Error()))

		// Only suggest setup if no config exists at all.
		// (If a server is configured but unreachable, the user already knows.)
		if !config.Exists() {
			fmt.Fprintf(stderr, "\n")
			if confirm("no server configured - set one up now?") {
				return loginCmd.RunE(cmd, nil)
			}
		}
		printQuickHelp()
		return nil
	}

	active := 0
	for _, r := range runs {
		if isActive(r.Status) {
			active++
		}
	}

	label("status", cGreen("connected"))
	label("runs", fmt.Sprintf("%d active, %d total", active, len(runs)))

	// If the local spout.yaml declares a streams: blueprint, nudge the
	// user towards `spout run` - bare `spout` won't launch it.
	cfg := config.Load()
	if len(cfg.Streams) > 0 {
		unit := "streams"
		if len(cfg.Streams) == 1 {
			unit = "stream"
		}
		label("job", fmt.Sprintf("%s  %d %s  %s",
			cBold(cfg.Job),
			len(cfg.Streams),
			unit,
			cDim("(spout run to launch)")))
	}

	printActive(runs, active)
	printRecent(runs)
	printQuickHelp()
	return nil
}

func fetchRuns(addr string) ([]runEntry, error) {
	ok, reachable, authRequired, _ := probeSpout(addr)
	if !reachable {
		return nil, fmt.Errorf("unreachable")
	}
	if authRequired {
		return nil, fmt.Errorf("auth required")
	}
	if !ok {
		return nil, fmt.Errorf("not compatible")
	}

	client := http.Client{Timeout: 3e9}
	resp, err := client.Get("http://" + addr + "/api/runs")
	if err != nil {
		return nil, fmt.Errorf("unreachable")
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var runs []runEntry
	json.Unmarshal(body, &runs)
	return runs, nil
}

func isActive(status string) bool {
	return status == "streaming" || status == "awaiting"
}

func printActive(runs []runEntry, active int) {
	if active == 0 {
		return
	}
	section("active")
	rows, totalGroups, shown := sliceByGroup(runs, true, maxListed)
	printSummaryTable(rows)
	if totalGroups > shown {
		fmt.Fprintf(stderr, "  %s\n", cDim(fmt.Sprintf("+ %d more", totalGroups-shown)))
	}
}

func printRecent(runs []runEntry) {
	rows, totalGroups, shown := sliceByGroup(runs, false, maxListed)
	if totalGroups == 0 {
		return
	}
	section("recent")
	printSummaryTable(rows)
	if totalGroups > shown {
		fmt.Fprintf(stderr, "  %s\n", cDim(fmt.Sprintf("+ %d more", totalGroups-shown)))
	}
}

// sliceByGroup filters runs by active/ended, groups them by groupKey, and
// returns the rows belonging to the first `limit` groups. Also returns
// the total group count (for "+ N more") and the number actually included.
func sliceByGroup(runs []runEntry, active bool, limit int) (rows []runEntry, totalGroups, shown int) {
	// Walk once, bucket each run into its group, record group order.
	type bucket struct {
		key  string
		rows []runEntry
	}
	var order []string
	buckets := map[string]*bucket{}
	for _, r := range runs {
		if isActive(r.Status) != active {
			continue
		}
		k := groupKey(r)
		if b, ok := buckets[k]; ok {
			b.rows = append(b.rows, r)
			continue
		}
		buckets[k] = &bucket{key: k, rows: []runEntry{r}}
		order = append(order, k)
	}
	totalGroups = len(order)
	for _, k := range order {
		if limit > 0 && shown >= limit {
			break
		}
		rows = append(rows, buckets[k].rows...)
		shown++
	}
	return
}

type runEntry struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Mode       string `json:"mode"`
	Job        string `json:"job"`
	Run        string `json:"run"`
	Label      string `json:"label"`
	Bytes      int64  `json:"bytes"`
	Lines      int64  `json:"lines"`
	DurationMs int64  `json:"duration_ms"`
	StartedMs  int64  `json:"started_ms"`
}

// printTable is the grouped/tree renderer used by `spout ls` and
// `spout stats`. Bare `spout` status uses printSummaryTable instead.
//
//   ●  exp-2                            (aggregate dot, run name bold)
//      ├─ training      run  1m    3L
//      ├─ gpu           run  1m   27L
//      └─ system        run  1m    2L
//   ●  theta            run  2m  166L   (standalone)
//
// Columns align across both parent-less standalone rows and stream child
// rows. We pad by visible width; tabwriter would miscount ANSI escapes.
func printTable(rows []runEntry) {
	// Keep groups in first-seen order so the outer caller's sort wins.
	type group struct {
		key      string
		parent   *runEntry // nil when this group holds a single standalone row
		children []runEntry
	}
	var order []string
	groups := map[string]*group{}
	for _, r := range rows {
		k := groupKey(r)
		if g, ok := groups[k]; ok {
			g.children = append(g.children, r)
			continue
		}
		g := &group{key: k}
		if r.Run != "" {
			// stream child; no parent row yet - we'll synthesize one below
			g.children = append(g.children, r)
		} else {
			// standalone - one row, no children
			rr := r
			g.parent = &rr
		}
		groups[k] = g
		order = append(order, k)
	}

	// For each stream group, synthesize a parent row by aggregating
	// the children's status / duration / lines.
	for _, k := range order {
		g := groups[k]
		if g.parent != nil {
			continue
		}
		g.parent = &runEntry{
			Name:       g.key,
			Run:        g.key,
			Mode:       g.children[0].Mode,
			Status:     aggregateStatus(g.children),
			DurationMs: maxDurationMs(g.children),
			Lines:      sumLines(g.children),
			StartedMs:  g.children[0].StartedMs,
		}
	}

	// Compute column widths across *child* rows (that's where columns
	// actually appear). Standalone rows use the same widths for parity.
	maxName, maxMode, maxDur := 0, 0, 0
	for _, k := range order {
		g := groups[k]
		if len(g.children) == 0 {
			// standalone - measure its own name/mode/dur
			r := *g.parent
			m := modeTag(r.Mode)
			d := humanDuration(r.DurationMs)
			n := r.Name
			if len(n) > maxName {
				maxName = len(n)
			}
			if len(m) > maxMode {
				maxMode = len(m)
			}
			if len(d) > maxDur {
				maxDur = len(d)
			}
			continue
		}
		for _, c := range g.children {
			n := entryLabel(c)
			m := modeTag(c.Mode)
			d := humanDuration(c.DurationMs)
			if len(n) > maxName {
				maxName = len(n)
			}
			if len(m) > maxMode {
				maxMode = len(m)
			}
			if len(d) > maxDur {
				maxDur = len(d)
			}
		}
	}

	for _, k := range order {
		g := groups[k]
		if len(g.children) == 0 {
			// standalone row
			r := *g.parent
			printLeafRow(r, entryLabel(r), maxName, maxMode, maxDur, "")
			continue
		}
		// parent row: dot + bold run name (no metrics - they'd be
		// ambiguous anyway).
		fmt.Fprintf(stderr, "  %s  %s\n", statusDot(g.parent.Status), cBold(g.parent.Run))
		for i, c := range g.children {
			branch := "├─"
			if i == len(g.children)-1 {
				branch = "└─"
			}
			printLeafRow(c, entryLabel(c), maxName, maxMode, maxDur, branch)
		}
	}
}

// printLeafRow prints one run's line. `branch` is either "" (standalone
// row) or a tree character like "├─" / "└─" for a child of a group.
//
// Child rows skip the mode column: a stream under a run is, by definition,
// a run - the word adds nothing. Standalone rows keep the mode tag so
// pipe vs run stays visible at a glance.
func printLeafRow(r runEntry, name string, maxName, maxMode, maxDur int, branch string) {
	dur := humanDuration(r.DurationMs)
	lines := fmt.Sprintf("%dL", r.Lines)

	if branch != "" {
		prefix := fmt.Sprintf("     %s %s ", cDim(branch), statusDot(r.Status))
		fmt.Fprintf(stderr, "%s%s%s  %s%s  %s\n",
			prefix,
			cBold(name), pad(name, maxName),
			cDim(dur), pad(dur, maxDur),
			cDim(lines))
		return
	}

	mode := modeTag(r.Mode)
	prefix := "  " + statusDot(r.Status) + "  "
	fmt.Fprintf(stderr, "%s%s%s  %s%s  %s%s  %s\n",
		prefix,
		cBold(name), pad(name, maxName),
		cDim(mode), pad(mode, maxMode),
		cDim(dur), pad(dur, maxDur),
		cDim(lines))
}

// aggregateStatus picks the most "alive" status across a set of children.
// streaming > awaiting > error > killed > success > ended.
// The parent row's dot should convey "what's going on here right now".
func aggregateStatus(rows []runEntry) string {
	rank := map[string]int{
		"streaming": 5,
		"awaiting":  4,
		"error":     3,
		"killed":    2,
		"success":   1,
	}
	best := ""
	bestRank := -1
	for _, r := range rows {
		if rank[r.Status] > bestRank {
			best = r.Status
			bestRank = rank[r.Status]
		}
	}
	if best == "" {
		return "ended"
	}
	return best
}

func maxDurationMs(rows []runEntry) int64 {
	var m int64
	for _, r := range rows {
		if r.DurationMs > m {
			m = r.DurationMs
		}
	}
	return m
}

func sumLines(rows []runEntry) int64 {
	var s int64
	for _, r := range rows {
		s += r.Lines
	}
	return s
}

// printSummaryTable is the collapsed renderer: one line per group.
// Streams get rolled up into a single row showing the run name and a
// "N streams" marker; standalone runs render as their plain name.
// Used by bare `spout` where the screen should fit on one page.
func printSummaryTable(rows []runEntry) {
	type bucket struct {
		key      string
		parent   runEntry
		children []runEntry
	}
	var order []string
	buckets := map[string]*bucket{}
	for _, r := range rows {
		k := groupKey(r)
		if b, ok := buckets[k]; ok {
			b.children = append(b.children, r)
			continue
		}
		b := &bucket{key: k}
		if r.Run != "" {
			b.children = append(b.children, r)
		} else {
			b.parent = r
		}
		buckets[k] = b
		order = append(order, k)
	}

	type row struct {
		dot, name, meta, dur, lines string
	}
	prepared := make([]row, 0, len(order))
	maxName, maxMeta, maxDur := 0, 0, 0
	for _, k := range order {
		b := buckets[k]
		var rr row
		if len(b.children) == 0 {
			// standalone
			rr = row{
				dot:   statusDot(b.parent.Status),
				name:  b.parent.Name,
				meta:  modeTag(b.parent.Mode),
				dur:   humanDuration(b.parent.DurationMs),
				lines: fmt.Sprintf("%dL", b.parent.Lines),
			}
		} else {
			unit := "streams"
			if len(b.children) == 1 {
				unit = "stream"
			}
			rr = row{
				dot:   statusDot(aggregateStatus(b.children)),
				name:  k,
				meta:  fmt.Sprintf("%d %s", len(b.children), unit),
				dur:   humanDuration(maxDurationMs(b.children)),
				lines: fmt.Sprintf("%dL", sumLines(b.children)),
			}
		}
		if len(rr.name) > maxName {
			maxName = len(rr.name)
		}
		if len(rr.meta) > maxMeta {
			maxMeta = len(rr.meta)
		}
		if len(rr.dur) > maxDur {
			maxDur = len(rr.dur)
		}
		prepared = append(prepared, rr)
	}

	for _, rr := range prepared {
		fmt.Fprintf(stderr, "  %s  %s%s  %s%s  %s%s  %s\n",
			rr.dot,
			cBold(rr.name), pad(rr.name, maxName),
			cDim(rr.meta), pad(rr.meta, maxMeta),
			cDim(rr.dur), pad(rr.dur, maxDur),
			cDim(rr.lines))
	}
}

// pad returns enough spaces to bring a visible string up to target width.
func pad(s string, target int) string {
	if n := target - len(s); n > 0 {
		return strings.Repeat(" ", n)
	}
	return ""
}

// printRunLine is kept for ls.go which prints rows individually.
func printRunLine(r runEntry) {
	printTable([]runEntry{r})
}

func statusDot(status string) string {
	switch status {
	case "streaming":
		return cGreen("●")
	case "awaiting":
		return cYellow("●")
	case "error":
		return cRed("●")
	case "success":
		// Dark green - done cleanly, understated but clearly distinct from
		// bright streaming-green.
		return cDarkgreen("●")
	case "killed":
		// Filled gray - user-initiated stop (distinct from success dim green).
		return cDim("●")
	default:
		return cDim("●")
	}
}

func modeTag(mode string) string {
	if mode == "run" {
		return "run"
	}
	return "pipe"
}

func printQuickHelp() {
	fmt.Fprintf(stderr, "\n")
	label("pipe", "command | spout")
	label("run", "spout run command")
	label("help", "spout --help")
	fmt.Fprintf(stderr, "\n")
}

// entryLabel picks the visible label for a run entry. A stream (has Label)
// shows its label; a standalone run shows its full name — the same string
// that appears in the URL and the `spout run` hints. The short word can
// still be typed as input (resolveTargets prefix-matches), but the
// canonical name is what we display.
func entryLabel(r runEntry) string {
	if r.Label != "" {
		return r.Label
	}
	return r.Name
}

// groupKey returns the grouping key for a run. Streams with the same Run
// share a key; standalone runs get their own unique key based on Name.
func groupKey(r runEntry) string {
	if r.Run != "" {
		return r.Run
	}
	return r.Name
}
