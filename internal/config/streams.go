package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ResolveStreamDir returns the absolute working directory for a stream.
//
//   - if s.Dir is absolute, use it
//   - if s.Dir is relative, resolve it against the yaml file that owned it
//   - if s.Dir is empty, default to the dir containing the yaml file
//
// Falls back to cwd if SourceFile was never set (only happens for
// configs built in memory rather than loaded from disk).
func ResolveStreamDir(s Stream) string {
	base := filepath.Dir(s.SourceFile)
	if base == "." || base == "" {
		base, _ = os.Getwd()
	}
	if s.Dir == "" {
		return base
	}
	if filepath.IsAbs(s.Dir) {
		return s.Dir
	}
	return filepath.Clean(filepath.Join(base, s.Dir))
}

// RunNameNeedsInput reports whether a template contains the {input}
// placeholder. Streams launch code checks this up front so it can prompt
// the user before touching tmux or the server.
func RunNameNeedsInput(template string) bool {
	return strings.Contains(template, "{input}")
}

// ExpandRunName substitutes variables in a run_name template.
//
//	{n}           auto-incrementing counter for this job
//	{input}       value the user typed at the prompt (must be passed in)
//	{date}        YYYY-MM-DD
//	{time}        HH-mm-ss
//	{ts}          Unix timestamp
//	{t:FORMAT}    custom datetime using ISO/Moment-style tokens, see below
//
// FORMAT tokens (case-sensitive, same convention as Moment.js/Java):
//
//	YYYY  4-digit year      YY  2-digit year
//	MM    month (01..12)    DD / dd  day (01..31)
//	HH    hour 24 (00..23)  hh       hour 12 (01..12)
//	mm    minute (00..59)   ss       second (00..59)
//
// Everything else in FORMAT is emitted verbatim. Examples:
//
//	exp-{n}                    -> exp-3
//	{t:YYYY-MM-DD}             -> 2026-04-16
//	run-{t:dd/MM HH:mm}        -> run-16/04 14:30
//	{input}-{n}                -> my-experiment-1 (after prompting)
func ExpandRunName(template string, counter int, input string) string {
	if template == "" {
		return ""
	}
	now := time.Now()

	// Resolve {t:FORMAT} first so its contents don't collide with the
	// simpler literal tokens below.
	out := expandCustomTime(template, now)

	replacer := strings.NewReplacer(
		"{n}", strconv.Itoa(counter),
		"{date}", now.Format("2006-01-02"),
		"{time}", now.Format("15-04-05"),
		"{ts}", strconv.FormatInt(now.Unix(), 10),
		"{input}", input,
	)
	return replacer.Replace(out)
}

// expandCustomTime replaces every {t:FORMAT} occurrence in s with `now`
// formatted per our Moment-style token set. Returns s unchanged if no
// {t: occurrences.
func expandCustomTime(s string, now time.Time) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "{t:")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.Index(s[i:], "}")
		if j < 0 {
			// Unclosed {t: - emit literally and stop.
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(now.Format(momentToGo(s[i+3 : i+j])))
		s = s[i+j+1:]
	}
}

// momentToGo rewrites Moment.js/Java-style format tokens to Go's
// reference-time layout. Longer tokens are replaced before shorter ones
// so YYYY binds before YY.
var momentOrder = []struct{ from, to string }{
	{"YYYY", "2006"},
	{"YY", "06"},
	{"MM", "01"},
	{"DD", "02"},
	{"dd", "02"},
	{"HH", "15"},
	{"hh", "03"},
	{"mm", "04"},
	{"ss", "05"},
}

func momentToGo(format string) string {
	// Walk the string and emit either a matched token's Go equivalent or
	// the raw byte. Avoids rematching inside already-substituted text
	// (which a naive ReplaceAll loop could do).
	var b strings.Builder
	for i := 0; i < len(format); {
		matched := false
		for _, t := range momentOrder {
			if strings.HasPrefix(format[i:], t.from) {
				b.WriteString(t.to)
				i += len(t.from)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(format[i])
			i++
		}
	}
	return b.String()
}

// NextRunCounter returns the next {n} for a given job, using a simple
// file-backed counter at ~/.config/spout/counters/<job>.txt.
// Missing file / read error → returns 1 (first invocation).
// Sanitizes job so it's safe as a filename.
func NextRunCounter(job string) int {
	if job == "" {
		return 1
	}
	p := counterPath(job)
	os.MkdirAll(filepath.Dir(p), 0755)

	n := 1
	if data, err := os.ReadFile(p); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			n = v + 1
		}
	}
	os.WriteFile(p, []byte(strconv.Itoa(n)), 0644)
	return n
}

func counterPath(job string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			return r
		}
		return '_'
	}, job)
	return filepath.Join(ConfigDir(), "counters", safe+".txt")
}

// StreamEnv returns the composed environment for a stream: process env
// (already including walked .env values, set by loadEnvTokens) overlaid by
// the stream's own env map. Stream-level values win per Q2.
//
// Returned in the form ["KEY=VALUE", ...] suitable for exec.Cmd.Env or for
// composing into a shell prefix.
func StreamEnv(s Stream) []string {
	base := os.Environ()
	if len(s.Env) == 0 {
		return base
	}
	// Build a lookup so we can remove matching keys from base and append
	// the stream-level versions at the end.
	override := make(map[string]string, len(s.Env))
	for k, v := range s.Env {
		override[k] = v
	}
	out := make([]string, 0, len(base)+len(override))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := override[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range override {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

// StreamShellPrefix returns a bash/sh-compatible prefix that exports the
// stream's env vars inline. Empty string if there are no overrides.
//
//	StreamShellPrefix({FOO: bar}) -> `export FOO='bar'; `
func StreamShellPrefix(s Stream) string {
	if len(s.Env) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range s.Env {
		fmt.Fprintf(&b, "export %s=%s; ", k, shellQuote(v))
	}
	return b.String()
}

// shellQuote wraps a value in single quotes so embedded spaces / special
// chars survive being passed through `sh -c`.
func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
