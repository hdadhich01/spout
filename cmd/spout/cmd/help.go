package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// customHelp prints the spout help in our visual style:
// - banner at the top
// - commands grouped by Group ID with aqua+bold section headers
// - command names plain (bold only), descriptions in dim
// - aliases inline next to each command name
func customHelp(cmd *cobra.Command, args []string) {
	if cmd != rootCmd {
		defaultHelp(cmd)
		return
	}

	banner()
	fmt.Fprintf(stderr, "\n  %s\n", cDim("Pipe any command into a live web dashboard."))

	// Group commands by GroupID.
	groups := map[string][]*cobra.Command{}
	for _, sub := range cmd.Commands() {
		if sub.Hidden || !sub.IsAvailableCommand() {
			continue
		}
		gid := sub.GroupID
		if gid == "" {
			gid = "_other"
		}
		groups[gid] = append(groups[gid], sub)
	}

	// Compute max display-name width across all visible commands for alignment.
	maxName := 0
	for _, cmds := range groups {
		for _, sub := range cmds {
			main, others := nameParts(sub)
			n := len(main)
			if len(others) > 0 {
				n += len(" (") + len(strings.Join(others, "/")) + len(")")
			}
			if n > maxName {
				maxName = n
			}
		}
	}

	// Print groups in declared order, sorted by main (shortest) name.
	for _, g := range cmd.Groups() {
		cmds := groups[g.ID]
		if len(cmds) == 0 {
			continue
		}
		sort.Slice(cmds, func(i, j int) bool {
			ai, _ := nameParts(cmds[i])
			aj, _ := nameParts(cmds[j])
			return ai < aj
		})
		section(g.Title)
		for _, sub := range cmds {
			printCmdLine(sub, maxName)
		}
	}

	// Ungrouped (e.g., completion).
	if other := groups["_other"]; len(other) > 0 {
		sort.Slice(other, func(i, j int) bool {
			ai, _ := nameParts(other[i])
			aj, _ := nameParts(other[j])
			return ai < aj
		})
		section("other")
		for _, sub := range other {
			printCmdLine(sub, maxName)
		}
	}

	// Flags.
	section("flags")
	fmt.Fprint(stderr, indentFlags(cmd.Flags().FlagUsages()))

	fmt.Fprintf(stderr, "\n  %s %s for details on any command.\n\n",
		cDim("Run"),
		cBold("spout <command> --help"))
}

// nameParts returns the shortest name as the "main" + all other names as aliases.
// If a command is registered as `delete` with alias `rm`, returns ("rm", ["delete"]).
func nameParts(c *cobra.Command) (main string, others []string) {
	all := append([]string{c.Name()}, c.Aliases...)
	main = all[0]
	for _, n := range all[1:] {
		if len(n) < len(main) {
			main = n
		}
	}
	for _, n := range all {
		if n != main {
			others = append(others, n)
		}
	}
	sort.Strings(others)
	return main, others
}

// printCmdLine prints one command row: bold main name + dim aliases + dim description.
func printCmdLine(c *cobra.Command, maxName int) {
	main, others := nameParts(c)
	aliasPart := ""
	if len(others) > 0 {
		aliasPart = " (" + strings.Join(others, "/") + ")"
	}
	full := main + aliasPart
	pad := strings.Repeat(" ", maxName-len(full)+2)
	fmt.Fprintf(stderr, "  %s%s%s%s\n",
		cBold(main),
		cDim(aliasPart),
		pad,
		cDim(c.Short))
}

// indentFlags re-indents cobra's default flag usage block to match our style.
func indentFlags(usage string) string {
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(usage, "\n"), "\n") {
		if line == "" {
			continue
		}
		out.WriteString("  ")
		out.WriteString(strings.TrimLeft(line, " "))
		out.WriteString("\n")
	}
	return out.String()
}

// defaultHelp prints subcommand help in our visual style.
// Splits Long into description (first paragraph) and examples (any indented lines).
func defaultHelp(c *cobra.Command) {
	fmt.Fprintln(stderr)

	// Description: first paragraph of Long, or Short if no Long. Wrap at
	// 76 columns + 2-char indent so it stays inside an 80-col terminal.
	desc, examples := splitLong(c.Long)
	if desc == "" {
		desc = c.Short
	}
	if desc != "" {
		for _, line := range wrapText(desc, 76) {
			fmt.Fprintf(stderr, "  %s\n", cDim(line))
		}
	}

	section("usage")
	fmt.Fprintf(stderr, "  %s\n", c.UseLine())

	if len(c.Aliases) > 0 {
		section("aliases")
		fmt.Fprintf(stderr, "  %s\n", strings.Join(c.Aliases, ", "))
	}

	if len(examples) > 0 {
		section("examples")
		for _, line := range examples {
			fmt.Fprintf(stderr, "  %s\n", cDim(line))
		}
	}

	if c.HasAvailableFlags() {
		section("flags")
		fmt.Fprint(stderr, indentFlags(c.LocalFlags().FlagUsages()))
	}
	if c.HasAvailableInheritedFlags() {
		section("global flags")
		fmt.Fprint(stderr, indentFlags(c.InheritedFlags().FlagUsages()))
	}
	fmt.Fprintln(stderr)
}

// wrapText breaks `text` into lines no longer than `width`. Words longer
// than width keep their own line. Used to keep help descriptions inside
// an 80-col terminal regardless of how long the Long field is.
func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	lines = append(lines, cur)
	return lines
}

// splitLong takes a Long help text and splits it into a description (first
// paragraph of plain text) and a slice of example lines (indented commands).
func splitLong(long string) (desc string, examples []string) {
	if long == "" {
		return "", nil
	}
	var descLines []string
	for _, line := range strings.Split(long, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Indented lines (typically `  spout cmd ...`) are treated as examples.
		if strings.HasPrefix(line, "  ") {
			examples = append(examples, trimmed)
		} else {
			descLines = append(descLines, trimmed)
		}
	}
	desc = strings.Join(descLines, " ")
	return desc, examples
}
