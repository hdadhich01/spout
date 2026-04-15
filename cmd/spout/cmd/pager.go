package cmd

import (
	"os"
	"os/exec"

	"golang.org/x/term"
)

// withPager runs fn, capturing all output sent to `stderr`. If stderr is a
// terminal AND the output would exceed the terminal height, the output is
// piped to a pager (less by default). Otherwise it prints inline.
//
// Uses `less -FRX --mouse`:
//
//	-F        quit if content fits on one screen (no pager for short output)
//	-R        preserve ANSI color codes
//	-X        don't clear the screen on exit (output stays in scrollback)
//	--mouse   enable mouse wheel / touchpad scrolling
func withPager(fn func()) {
	// Not a terminal? Just print normally.
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		fn()
		return
	}

	// Pick a pager. PAGER env var wins, otherwise try less, then more.
	pagerCmd := os.Getenv("PAGER")
	if pagerCmd == "" {
		if _, err := exec.LookPath("less"); err == nil {
			pagerCmd = "less -FRX --mouse --wheel-lines=3"
		} else if _, err := exec.LookPath("more"); err == nil {
			pagerCmd = "more"
		} else {
			fn()
			return
		}
	}

	cmd := exec.Command("sh", "-c", pagerCmd)
	pipe, err := cmd.StdinPipe()
	if err != nil {
		fn()
		return
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fn()
		return
	}

	// Redirect the global stderr writer (used by label, section, printTable, etc.)
	// to the pager's stdin for the duration of fn.
	oldStderr := stderr
	stderr = pipe
	fn()
	stderr = oldStderr

	pipe.Close()
	cmd.Wait()
}
