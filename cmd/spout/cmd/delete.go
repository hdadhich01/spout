package cmd

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	cleanAll      bool
	cleanOlder    string
	cleanEnded    bool
	cleanErrors   bool
	cleanKeep     int
	cleanForce    bool
)

var deleteCmd = &cobra.Command{
	Use:     "delete <name> [name...]",
	Aliases: []string{"rm"},
	Short:   "Delete runs by name",
	Long: `Delete a run by name. Accepts multiple names.

  spout delete fox
  spout delete fox bear cat
  spout rm fox`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		deleted := 0
		for _, name := range args {
			addr, found := findRunServer(name)
			if !found {
				fail("%s %s on any server", cBold(name), cRed("not found"))
				continue
			}
			if err := deleteRun(addr, name); err != nil {
				fail("%s: %v", cBold(name), err)
				continue
			}
			ok("%s %s", cGreen("deleted"), cBold(name))
			deleted++
		}
		if len(args) > 1 {
			ok("%s %d of %d run(s)", cGreen("deleted"), deleted, len(args))
		}
		return nil
	},
}

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Bulk cleanup of runs",
	Long: `Bulk cleanup of runs from the server.

  spout clean                  # interactive: prompts before deleting
  spout clean --all            # delete everything
  spout clean --ended          # delete all ended runs (keep active)
  spout clean --errors         # delete only errored runs
  spout clean --older 7d       # delete runs older than 7 days (h/d/w)
  spout clean --keep 10        # keep last 10 runs, delete rest
  spout clean --ended -y       # skip confirmation`,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, _ := resolveServer()
		if err := checkServer(addr); err != nil {
			return err
		}

		runs, err := fetchRuns(addr)
		if err != nil {
			return fmt.Errorf("server %s: %s", cBold(addr), cRed(err.Error()))
		}

		// Pick which runs to delete based on flags.
		var targets []runEntry
		switch {
		case cleanAll:
			targets = runs
		case cleanErrors:
			for _, r := range runs {
				if r.Status == "error" {
					targets = append(targets, r)
				}
			}
		case cleanEnded:
			for _, r := range runs {
				if !isActive(r.Status) {
					targets = append(targets, r)
				}
			}
		case cleanOlder != "":
			d, err := parseDuration(cleanOlder)
			if err != nil {
				return fmt.Errorf("invalid duration %q (use 7d, 24h, 1w)", cleanOlder)
			}
			cutoff := time.Now().Add(-d).UnixMilli()
			for _, r := range runs {
				if !isActive(r.Status) && r.StartedMs < cutoff {
					targets = append(targets, r)
				}
			}
		case cleanKeep > 0:
			// Keep the most recent N (active + recent), delete the rest.
			// runs are already sorted newest-first by the server.
			if len(runs) > cleanKeep {
				targets = runs[cleanKeep:]
			}
		default:
			// Interactive mode - show what's available, ask user.
			return interactiveClean(addr, runs)
		}

		if len(targets) == 0 {
			info("%s", cDim("nothing to clean"))
			return nil
		}

		fmt.Fprintf(stderr, "\n")
		warn("%s %d run(s):", cYellow("about to delete"), len(targets))
		for i, r := range targets {
			if i >= 10 {
				fmt.Fprintf(stderr, "  %s\n", cDim(fmt.Sprintf("... and %d more", len(targets)-10)))
				break
			}
			fmt.Fprintf(stderr, "  %s %s  %s\n", statusDot(r.Status), cBold(displayName(r.Name)), cDim(humanDuration(r.DurationMs)))
		}
		fmt.Fprintf(stderr, "\n")

		if !cleanForce {
			// --all is destructive: require typing 'delete'.
			if cleanAll {
				if !confirmTyped("destructive - confirm deletion of ALL runs", "delete") {
					info("%s", cDim("cancelled"))
					return nil
				}
			} else if !confirm("proceed?") {
				info("%s", cDim("cancelled"))
				return nil
			}
		}

		deleted := 0
		for _, r := range targets {
			if err := deleteRun(addr, r.Name); err == nil {
				deleted++
			}
		}
		ok("%s %d run(s)", cGreen("deleted"), deleted)
		return nil
	},
}

func interactiveClean(addr string, runs []runEntry) error {
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

	// Build the menu dynamically - only show options that would do something.
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

	fmt.Fprintf(stderr, "\n")
	label("total", fmt.Sprintf("%d runs", len(runs)))
	label("ended", fmt.Sprintf("%d", ended))
	label("errors", fmt.Sprintf("%d", errored))
	fmt.Fprintf(stderr, "\n")

	for _, m := range menu {
		fmt.Fprintf(stderr, "  %s   %s\n", cAqua(cBold("["+m.key+"]")), m.label)
	}
	fmt.Fprintf(stderr, "  %s   cancel\n", cAqua(cBold("[q]")))
	fmt.Fprintf(stderr, "\n  > ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	var targets []runEntry
	switch input {
	case "e", "ended":
		if ended == 0 {
			info("%s", cDim("nothing to clean"))
			return nil
		}
		for _, r := range runs {
			if !isActive(r.Status) {
				targets = append(targets, r)
			}
		}
	case "r", "errors":
		if errored == 0 {
			info("%s", cDim("nothing to clean"))
			return nil
		}
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

	deleted := 0
	for _, r := range targets {
		if err := deleteRun(addr, r.Name); err == nil {
			deleted++
		}
	}
	ok("%s %d run(s)", cGreen("deleted"), deleted)
	return nil
}

func deleteRun(addr, name string) error {
	req, err := http.NewRequest("DELETE", "http://"+addr+"/api/run/"+name, nil)
	if err != nil {
		return err
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
	cleanCmd.Flags().BoolVar(&cleanAll, "all", false, "delete everything")
	cleanCmd.Flags().BoolVar(&cleanEnded, "ended", false, "delete all ended runs")
	cleanCmd.Flags().BoolVar(&cleanErrors, "errors", false, "delete errored runs")
	cleanCmd.Flags().StringVar(&cleanOlder, "older", "", "delete runs older than duration (e.g. 7d)")
	cleanCmd.Flags().IntVar(&cleanKeep, "keep", 0, "keep last N runs, delete the rest")
	cleanCmd.Flags().BoolVarP(&cleanForce, "yes", "y", false, "skip confirmation")
	deleteCmd.GroupID = groupSession
	cleanCmd.GroupID = groupSession
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(cleanCmd)
}
