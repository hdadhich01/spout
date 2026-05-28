//go:build !linux

package cmd

// captureUpstreamCommand: Linux uses /proc to find the producer at the other
// end of our stdin pipe; on other platforms we don't have a clean way (yet),
// so pipe-mode runs without an inferred command and the title falls back to
// the run name.
func captureUpstreamCommand() string { return "" }
