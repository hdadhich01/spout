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
	var rows []runEntry
	for _, r := range runs {
		if !isActive(r.Status) {
			continue
		}
		if len(rows) >= maxListed {
			break
		}
		rows = append(rows, r)
	}
	printTable(rows)
	if active > maxListed {
		fmt.Fprintf(stderr, "  %s\n", cDim(fmt.Sprintf("+ %d more", active-maxListed)))
	}
}

func printRecent(runs []runEntry) {
	total := 0
	for _, r := range runs {
		if !isActive(r.Status) {
			total++
		}
	}
	if total == 0 {
		return
	}
	section("recent")
	var rows []runEntry
	for _, r := range runs {
		if isActive(r.Status) {
			continue
		}
		if len(rows) >= maxListed {
			break
		}
		rows = append(rows, r)
	}
	printTable(rows)
	if total > maxListed {
		fmt.Fprintf(stderr, "  %s\n", cDim(fmt.Sprintf("+ %d more", total-maxListed)))
	}
}

type runEntry struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Mode       string `json:"mode"`
	Bytes      int64  `json:"bytes"`
	Lines      int64  `json:"lines"`
	DurationMs int64  `json:"duration_ms"`
	StartedMs  int64  `json:"started_ms"`
}

// printTable prints rows with column-aligned metadata. We can't use
// text/tabwriter here: every field is wrapped in ANSI escapes (colored
// dot, bold name, dim metadata), and tabwriter counts those escape bytes
// as visible width - so rows with longer ANSI sequences (truecolor dots,
// etc.) shifted everything right. Instead, we pad by the *visible* width.
//
//   ●  name    mode    duration    lines
func printTable(rows []runEntry) {
	type cols struct{ name, mode, dur, lines string }
	prepared := make([]cols, len(rows))
	maxName, maxMode, maxDur := 0, 0, 0
	for i, r := range rows {
		prepared[i] = cols{
			name:  displayName(r.Name),
			mode:  modeTag(r.Mode),
			dur:   humanDuration(r.DurationMs),
			lines: fmt.Sprintf("%dL", r.Lines),
		}
		if n := len(prepared[i].name); n > maxName {
			maxName = n
		}
		if n := len(prepared[i].mode); n > maxMode {
			maxMode = n
		}
		if n := len(prepared[i].dur); n > maxDur {
			maxDur = n
		}
	}

	for i, r := range rows {
		c := prepared[i]
		fmt.Fprintf(stderr, "  %s  %s%s  %s%s  %s%s  %s\n",
			statusDot(r.Status),
			cBold(c.name), pad(c.name, maxName),
			cDim(c.mode), pad(c.mode, maxMode),
			cDim(c.dur), pad(c.dur, maxDur),
			cDim(c.lines))
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

func displayName(name string) string {
	i := strings.LastIndex(name, "-")
	if i > 0 {
		return name[:i]
	}
	return name
}
