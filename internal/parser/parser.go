package parser

import (
	"regexp"
	"strings"
	"time"

	"github.com/hdadhich01/spout/internal/types"
)

var (
	// Strip cursor movement, screen clearing, and other control sequences.
	// Keep SGR (colors/styles) - those are rendered by ansi_up in the browser.
	cursorRegex = regexp.MustCompile(`\x1b\[\d*[ABCDEFGHJKST]`)
	clearRegex  = regexp.MustCompile(`\x1b\[\d*[JK]`)
	oscRegex    = regexp.MustCompile(`\x1b\].*?(\x07|\x1b\\)`)
)

// ParseLine cleans control sequences but preserves ANSI color/style codes
// for browser-side rendering.
func ParseLine(raw string) types.Line {
	cleaned := raw

	// Strip cursor movement and screen clearing.
	cleaned = cursorRegex.ReplaceAllString(cleaned, "")
	cleaned = clearRegex.ReplaceAllString(cleaned, "")
	cleaned = oscRegex.ReplaceAllString(cleaned, "")

	// Handle carriage returns - keep only the last segment (tqdm/progress bar spam).
	if i := strings.LastIndex(cleaned, "\r"); i >= 0 {
		cleaned = cleaned[i+1:]
	}

	return types.Line{
		Content:   cleaned,
		Timestamp: time.Now(),
	}
}
