package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// confirm asks a yes/no question. Default is no.
func confirm(prompt string) bool {
	fmt.Fprintf(stderr, "  %s %s ", prompt, cDim("[y/N]"))
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}

// confirmYes is like confirm but defaults to yes.
func confirmYes(prompt string) bool {
	fmt.Fprintf(stderr, "  %s %s ", prompt, cDim("[Y/n]"))
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "" || input == "y" || input == "yes"
}

// confirmTyped requires the user to type a specific word to confirm.
// Used for destructive operations.
func confirmTyped(prompt, word string) bool {
	fmt.Fprintf(stderr, "  %s %s ", prompt, cDim("(type '"+word+"' to confirm)"))
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input) == word
}

// askLine prompts for a line of input with no default.
func askLine(prompt string) string {
	fmt.Fprintf(stderr, "  %s ", prompt)
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input)
}
