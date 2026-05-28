package cmd

import (
	"fmt"
	"strings"
)

// All severity colors use 24-bit truecolor so the CLI and the web dashboard
// render the *same* hex value. The legacy 8-color ANSI codes (\033[31m etc.)
// looked different on every terminal theme - red often showed up as pink,
// yellow as mustard - which broke the "the dot color matches the text color
// matches the dashboard" promise.
//
// Every constant below must stay in lock-step with the equivalent CSS in
// web/static/*.html.
const (
	reset = "\033[0m"
	dim   = "\033[2m"
	bold  = "\033[1m"

	red       = "\033[38;2;185;28;28m"  // #b91c1c - error dot, error body
	green     = "\033[38;2;34;197;94m"  // #22c55e - streaming, ok body
	yellow    = "\033[38;2;234;179;8m"  // #eab308 - awaiting, warn body
	darkgreen = "\033[38;2;22;101;52m"  // #166534 - success (completed)
	aqua      = "\033[38;2;90;200;226m" // #5ac8e2 - brand / "spout:" / URLs
)

func cBold(s string) string      { return bold + s + reset }
func cDim(s string) string       { return dim + s + reset }
func cRed(s string) string       { return red + s + reset }
func cGreen(s string) string     { return green + s + reset }
func cYellow(s string) string    { return yellow + s + reset }
func cAqua(s string) string      { return aqua + s + reset }
func cDarkgreen(s string) string { return darkgreen + s + reset }

// Output style convention:
//
//   All four helpers print a brand-aqua "spout:" prefix and then the format
//   args verbatim. The CALLER is responsible for coloring the single key
//   verb that conveys the severity - "started" in cGreen, "not found" in
//   cYellow, "in use" in cRed, etc. This keeps the bulk of each message
//   readable default-white and draws the eye to the one word that matters.
//
//   info - neutral narration
//   ok   - successful action
//   warn - non-fatal issue
//   fail - error
//
// The four helpers are currently identical on purpose; the names exist so
// call sites self-document which severity they intend.
func say(format string, a ...any) {
	fmt.Fprintf(stderr, "%s%s\n", cAqua("spout: "), fmt.Sprintf(format, a...))
}

func info(format string, a ...any) { say(format, a...) }
func ok(format string, a ...any)   { say(format, a...) }
func warn(format string, a ...any) { say(format, a...) }
func fail(format string, a ...any) { say(format, a...) }

// ErrAlreadyReported is a sentinel returned by commands that have already
// printed their own formatted error via fail(). main.go uses this to exit
// non-zero without double-printing.
var ErrAlreadyReported = errAlreadyReported{}

type errAlreadyReported struct{}

func (errAlreadyReported) Error() string { return "already reported" }

// PrintError is the entry-point called from main when a command returns
// an error. It adds the "spout:" prefix and prints the error body. If the
// error already contains ANSI escapes, the caller has done its own
// coloring (e.g. addr in bold, reason in red) and we print it as-is.
// Otherwise we wrap the whole body in red.
// If the error is ErrAlreadyReported, skip - the caller already printed.
func PrintError(err error) {
	if err == ErrAlreadyReported {
		return
	}
	body := err.Error()
	if !strings.Contains(body, "\x1b[") {
		body = cRed(body)
	}
	fmt.Fprintf(stderr, "%s%s\n", cAqua("spout: "), body)
}

// label is the single "label  value" line used everywhere in the CLI.
// All labels are bold; value renders as the caller passed it. Padded to
// a fixed width so adjacent labels in a section line up. 9 chars covers
// the longest label across `spout config` / `spout server` / `spout
// doctor`'s flat rows ("clipboard"). For sections that need wider
// labels, use printRows (which auto-aligns within its own slice).
const labelWidth = 9

func label(name, value string) {
	pad := labelWidth - len(name)
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(stderr, "  %s%s  %s\n", cBold(name), strings.Repeat(" ", pad), value)
}

// row is one entry in a section that printRows aligns and renders.
//
// `mark` is the leading status glyph (✓ / ! / ✗) for `spout doctor`;
// leave empty when the section is purely informational. `name` is the
// row label (already plain — printRows applies bold + padding); `value`
// is the right-hand body, which the caller may colorize as it likes.
type row struct {
	mark  string
	name  string
	value string
}

// printRows renders a section of label/value rows with one consistent
// width across the slice. The width is the longest label in the slice,
// so each section can carry labels of any size without disturbing
// neighbouring sections (`spout config` profiles vs `spout doctor`
// streams, etc.).
func printRows(rows []row) {
	width := 0
	for _, r := range rows {
		if n := len(r.name); n > width {
			width = n
		}
	}
	for _, r := range rows {
		pad := strings.Repeat(" ", width-len(r.name))
		if r.mark != "" {
			fmt.Fprintf(stderr, "  %s %s%s  %s\n", r.mark, cBold(r.name), pad, r.value)
		} else {
			fmt.Fprintf(stderr, "  %s%s  %s\n", cBold(r.name), pad, r.value)
		}
	}
}

// markOk / markWarn / markFail are the leading glyphs printRows accepts
// in row.mark. Centralised so doctor stays consistent with any future
// command that wants the same severity language.
func markOk() string   { return cGreen("✓") }
func markWarn() string { return cYellow("!") }
func markFail() string { return cRed("✗") }

// statusText returns a colored description of a run's status.
// Colors must stay in sync with web/static/*.html statusInfo().
//   streaming - green,     active output
//   awaiting  - yellow,    idle at shell prompt (run mode only)
//   success   - dim green, completed cleanly
//   error     - red,       non-zero exit (run mode only)
//   killed    - gray,      session stopped by user before the command
//                           finished (run mode only)
func statusText(status string) string {
	switch status {
	case "streaming":
		return cGreen("streaming")
	case "awaiting":
		return cYellow("awaiting input")
	case "error":
		return cRed("exited with error")
	case "success":
		return cDarkgreen("completed")
	case "killed":
		return cDim("stopped by you")
	default:
		return cDim(status)
	}
}

// section prints an aqua+bold section header.
func section(name string) {
	fmt.Fprintf(stderr, "\n  %s\n", cAqua(cBold(name)))
}

// banner prints the spout brand: whale on the left, "Spout" text on the right.
func banner() {
	c := aqua
	r := reset

	// Whale: 4 content rows, padded to fixed width.
	whale := []string{
		"       ::.        ",
		"  (\\./)  .-\"\"-.   ",
		"   `\\'-'`      \\  ",
		"      '.___,_^__/ ",
	}

	// Spout text: 5 content rows. Body aligns with whale rows 2-4;
	// the underscore top is on whale row 1, the |_| descender hangs below.
	spout := []string{
		" _____             _   ",
		"|   __|___ ___ _ _| |_ ",
		"|__   | . | . | | |  _|",
		"|_____|  _|___|___|_|  ",
		"      |_|              ",
	}

	gap := "  "
	blank := strings.Repeat(" ", len(whale[0]))

	// Row 1: whale[0] + spout[0]
	// Row 2: whale[1] + spout[1]
	// Row 3: whale[2] + spout[2]
	// Row 4: whale[3] + spout[3]
	// Row 5: blank   + spout[4]
	for i := 0; i < 4; i++ {
		fmt.Fprintf(stderr, "%s%s%s%s%s%s%s\n", c, whale[i], r, gap, c, spout[i], r)
	}
	fmt.Fprintf(stderr, "%s%s%s%s%s\n", blank, gap, c, spout[4], r)
}
